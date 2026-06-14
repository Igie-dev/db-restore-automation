package restore

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
	"db-restore-automation/internal/shell"
)

var oracleIdentifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_$#]*$`)

type OracleDataPumpProvider struct {
	Logger *logging.Logger
	Runner shell.Runner
}

func (p OracleDataPumpProvider) Restore(ctx context.Context, cfg config.Config, job config.JobConfig, opts Options) error {
	credentialMethod := job.CredentialMethod()
	p.Logger.Info(fmt.Sprintf("job=%s type=oracle backup_file=%s credential_method=%s command_category=oracle-impdp status=start", job.Name, opts.BackupFile, credentialMethod))
	if opts.DryRun {
		p.Logger.Warn(fmt.Sprintf("job=%s type=oracle dry_run=true action=restore_skipped schema=%s directory=%s connect_string=%s", job.Name, job.Target.Schema, job.Target.OracleDirectory, job.Target.ConnectString))
		return nil
	}
	if credentialMethod != "oracle_wallet" {
		return fmt.Errorf("unsupported Oracle credential_method %q", credentialMethod)
	}
	if strings.ContainsAny(job.Target.ConnectString, " \r\n\t") || strings.TrimSpace(job.Target.ConnectString) == "" {
		return fmt.Errorf("unsafe Oracle connect string")
	}
	if !oracleIdentifierPattern.MatchString(job.Target.Schema) {
		return fmt.Errorf("unsafe Oracle schema: %s", job.Target.Schema)
	}
	if !oracleIdentifierPattern.MatchString(job.Target.OracleDirectory) {
		return fmt.Errorf("unsafe Oracle directory: %s", job.Target.OracleDirectory)
	}
	if err := requireFile(opts.BackupFile, "Oracle backup file"); err != nil {
		return err
	}
	if !strings.HasSuffix(strings.ToLower(opts.BackupFile), ".dmp") {
		return fmt.Errorf("unsupported Oracle backup type: %s", opts.BackupFile)
	}
	dumpFile := filepath.Base(opts.BackupFile)
	if strings.ContainsAny(dumpFile, `/\`+"\r\n") {
		return fmt.Errorf("unsafe Oracle dump file name: %s", dumpFile)
	}
	importLog := regexp.MustCompile(`[^A-Za-z0-9_.-]`).ReplaceAllString(job.Name, "_") + "-impdp.log"
	p.Logger.Info(fmt.Sprintf("job=%s type=oracle oracle_note=Data Pump dump file must exist in the Oracle directory object path accessible by the Oracle database server dumpfile=%s directory=%s", job.Name, dumpFile, job.Target.OracleDirectory))

	args := []string{
		job.Target.ConnectString,
		"schemas=" + job.Target.Schema,
		"directory=" + job.Target.OracleDirectory,
		"dumpfile=" + dumpFile,
		"table_exists_action=replace",
		"logfile=" + importLog,
	}
	result, err := p.Runner.Run(ctx, shell.Command{Category: "oracle-impdp", Executable: cfg.ToolPath(job, "oracle", "impdp", "impdp"), Args: args})
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("oracle-impdp failed with exit code %d", result.ExitCode)
	}
	return nil
}
