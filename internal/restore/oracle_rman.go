package restore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
	"db-restore-automation/internal/shell"
)

type OracleRmanProvider struct {
	Logger *logging.Logger
	Runner shell.Runner
}

func (p OracleRmanProvider) Restore(ctx context.Context, cfg config.Config, job config.JobConfig, opts Options) error {
	rmanExe := cfg.ToolPath(job, "oracle_rman", "rman", "rman")
	credentialMethod := job.CredentialMethod()
	restoreScope := job.RestoreScope()

	if strings.TrimSpace(job.RMAN.Target) == "" {
		return fmt.Errorf("rman.target is required")
	}
	if strings.TrimSpace(job.RMAN.CommandFile) == "" {
		return fmt.Errorf("rman.command_file is required")
	}
	if strings.TrimSpace(job.RMAN.LogFile) == "" {
		return fmt.Errorf("rman.log_file is required")
	}
	if credentialMethod != "os_auth" && credentialMethod != "oracle_wallet" {
		return fmt.Errorf("unsupported RMAN credential_method %q", credentialMethod)
	}
	if err := requireFile(job.RMAN.CommandFile, "RMAN command file"); err != nil {
		return err
	}
	if dir := filepath.Dir(job.RMAN.LogFile); dir != "." && strings.TrimSpace(dir) != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create RMAN log directory: %w", err)
		}
	}

	env := []string{}
	if strings.TrimSpace(job.RMAN.OracleHome) != "" {
		info, err := os.Stat(job.RMAN.OracleHome)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("ORACLE_HOME does not exist: %s", job.RMAN.OracleHome)
		}
		env = append(env, "ORACLE_HOME="+job.RMAN.OracleHome)
		separator := ":"
		if runtime.GOOS == "windows" {
			separator = ";"
		}
		env = append(env, "PATH="+filepath.Join(job.RMAN.OracleHome, "bin")+separator+os.Getenv("PATH"))
	}
	if strings.TrimSpace(job.RMAN.OracleSID) != "" {
		env = append(env, "ORACLE_SID="+job.RMAN.OracleSID)
	}

	args := []string{"target", job.RMAN.Target}
	if strings.TrimSpace(job.RMAN.Catalog) != "" {
		args = append(args, "catalog", job.RMAN.Catalog)
	}
	args = append(args, "cmdfile="+job.RMAN.CommandFile, "log="+job.RMAN.LogFile)

	p.Logger.Info(fmt.Sprintf("job=%s type=oracle_rman target=%s credential_method=%s restore_scope=%s command_file=%s log_file=%s oracle_home=%s oracle_sid=%s", job.Name, job.RMAN.Target, credentialMethod, restoreScope, job.RMAN.CommandFile, job.RMAN.LogFile, job.RMAN.OracleHome, job.RMAN.OracleSID))
	if opts.DryRun {
		p.Logger.Warn(fmt.Sprintf("job=%s type=oracle_rman dry_run=true action=restore_skipped command_file=%s log_file=%s", job.Name, job.RMAN.CommandFile, job.RMAN.LogFile))
		return nil
	}

	result, err := p.Runner.Run(ctx, shell.Command{Category: "oracle-rman", Executable: rmanExe, Args: args, Env: env})
	logTail := tailTextFile(job.RMAN.LogFile, 2400)
	if result.ExitCode != 0 || err != nil {
		if logTail != "" {
			p.Logger.Error(fmt.Sprintf("command_category=oracle-rman rman_log_tail=%s", logTail))
		}
		return fmt.Errorf("oracle-rman failed with exit code %d", result.ExitCode)
	}
	p.Logger.Success(fmt.Sprintf("command_category=oracle-rman status=success exit_code=0 rman_log_file=%s", job.RMAN.LogFile))
	if logTail != "" {
		p.Logger.Info(fmt.Sprintf("command_category=oracle-rman rman_log_tail=%s", logTail))
	}
	return nil
}

func tailTextFile(path string, maxBytes int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) > maxBytes {
		data = data[len(data)-maxBytes:]
	}
	return logging.Sanitize(string(data))
}
