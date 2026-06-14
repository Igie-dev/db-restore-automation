package restore

import (
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
	"db-restore-automation/internal/shell"
)

var mysqlDatabasePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

type MySQLProvider struct {
	Logger *logging.Logger
	Runner shell.Runner
}

func (p MySQLProvider) Restore(ctx context.Context, cfg config.Config, job config.JobConfig, opts Options) error {
	credentialMethod := job.CredentialMethod()
	p.Logger.Info(fmt.Sprintf("job=%s type=mysql backup_file=%s credential_method=%s command_category=mysql-restore status=start", job.Name, opts.BackupFile, credentialMethod))

	if opts.DryRun {
		p.Logger.Warn(fmt.Sprintf("job=%s type=mysql dry_run=true action=restore_skipped target_database=%s credential_method=%s", job.Name, job.Target.Database, credentialMethod))
		return nil
	}
	if strings.TrimSpace(job.Target.Database) == "" || !mysqlDatabasePattern.MatchString(job.Target.Database) {
		return fmt.Errorf("unsafe MySQL database name: %s", job.Target.Database)
	}
	if err := requireFile(opts.BackupFile, "backup file"); err != nil {
		return err
	}
	baseArgs, err := mysqlBaseArgs(job)
	if err != nil {
		return err
	}
	mysql := cfg.ToolPath(job, "mysql", "mysql", "mysql")
	dbIdentifier := strings.ReplaceAll(job.Target.Database, "`", "``")
	ddl := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`; CREATE DATABASE `%s`;", dbIdentifier, dbIdentifier)
	if err := p.runOK(ctx, "mysql-recreate-database", mysql, append(baseArgs, "-e", ddl), nil); err != nil {
		return err
	}

	backupLower := strings.ToLower(opts.BackupFile)
	switch {
	case strings.HasSuffix(backupLower, ".sql"):
		input, err := os.Open(opts.BackupFile)
		if err != nil {
			return err
		}
		defer input.Close()
		return p.runOK(ctx, "mysql-restore-sql", mysql, append(baseArgs, job.Target.Database), input)
	case strings.HasSuffix(backupLower, ".sql.gz"):
		input, err := os.Open(opts.BackupFile)
		if err != nil {
			return err
		}
		defer input.Close()
		gz, err := gzip.NewReader(input)
		if err != nil {
			return err
		}
		defer gz.Close()
		return p.runOK(ctx, "mysql-restore-sql", mysql, append(baseArgs, job.Target.Database), gz)
	default:
		return fmt.Errorf("unsupported MySQL backup type: %s", opts.BackupFile)
	}
}

func mysqlBaseArgs(job config.JobConfig) ([]string, error) {
	switch job.CredentialMethod() {
	case "login_path":
		if strings.TrimSpace(job.Target.LoginPath) == "" {
			return nil, fmt.Errorf("target.login_path is required when credential_method=login_path")
		}
		return []string{"--login-path=" + job.Target.LoginPath}, nil
	case "defaults_file":
		if strings.TrimSpace(job.Target.DefaultsFile) == "" {
			return nil, fmt.Errorf("target.defaults_file is required when credential_method=defaults_file")
		}
		if err := requireFile(job.Target.DefaultsFile, "MySQL defaults file"); err != nil {
			return nil, err
		}
		args := []string{"--defaults-extra-file=" + job.Target.DefaultsFile}
		if strings.TrimSpace(job.Target.Host) != "" {
			args = append(args, "-h", job.Target.Host)
		}
		if job.Target.Port != 0 {
			args = append(args, "-P", fmt.Sprint(job.Target.Port))
		}
		if strings.TrimSpace(job.Target.Username) != "" {
			args = append(args, "-u", job.Target.Username)
		}
		return args, nil
	default:
		return nil, fmt.Errorf("unsupported MySQL credential_method %q", job.CredentialMethod())
	}
}

func (p MySQLProvider) runOK(ctx context.Context, category, executable string, args []string, stdin any) error {
	var reader = ioReader(stdin)
	result, err := p.Runner.Run(ctx, shell.Command{Category: category, Executable: executable, Args: args, Stdin: reader})
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("%s failed with exit code %d", category, result.ExitCode)
	}
	return nil
}
