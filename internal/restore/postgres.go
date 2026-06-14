package restore

import (
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
	"db-restore-automation/internal/shell"
)

var pgDatabasePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

type PostgresProvider struct {
	Logger *logging.Logger
	Runner shell.Runner
}

func (p PostgresProvider) Restore(ctx context.Context, cfg config.Config, job config.JobConfig, opts Options) error {
	jobName := job.Name
	credentialMethod := job.CredentialMethod()
	p.Logger.Info(fmt.Sprintf("job=%s type=postgres backup_file=%s credential_method=%s command_category=postgres-restore status=start", jobName, opts.BackupFile, credentialMethod))

	if opts.DryRun {
		p.Logger.Warn(fmt.Sprintf("job=%s type=postgres dry_run=true action=restore_skipped target_database=%s credential_method=%s", jobName, job.Target.Database, credentialMethod))
		return nil
	}
	if credentialMethod != "pgpass" {
		return fmt.Errorf("unsupported PostgreSQL credential_method %q", credentialMethod)
	}
	if err := requireSafePostgresDatabase(job.Target.Database, "target.database"); err != nil {
		return err
	}
	if err := requireSafePostgresDatabase(job.Target.MaintenanceDatabase, "target.maintenance_database"); err != nil {
		return err
	}
	if strings.TrimSpace(job.Target.Host) == "" || job.Target.Port == 0 || strings.TrimSpace(job.Target.Username) == "" {
		return fmt.Errorf("missing PostgreSQL target host, port, or username")
	}
	if err := requireFile(opts.BackupFile, "backup file"); err != nil {
		return err
	}

	psql := cfg.ToolPath(job, "postgres", "psql", "psql")
	pgRestore := cfg.ToolPath(job, "postgres", "pg_restore", "pg_restore")
	dropdb := cfg.ToolPath(job, "postgres", "dropdb", "dropdb")
	createdb := cfg.ToolPath(job, "postgres", "createdb", "createdb")
	baseArgs := []string{"-h", job.Target.Host, "-p", fmt.Sprint(job.Target.Port), "-U", job.Target.Username}

	escapedDb := strings.ReplaceAll(job.Target.Database, "'", "''")
	terminateSQL := fmt.Sprintf("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid();", escapedDb)
	if err := p.runOK(ctx, "postgres-terminate-connections", psql, append(baseArgs, "-d", job.Target.MaintenanceDatabase, "-v", "ON_ERROR_STOP=1", "-c", terminateSQL)); err != nil {
		return err
	}
	if err := p.runOK(ctx, "postgres-drop-database", dropdb, append(baseArgs, "--if-exists", job.Target.Database)); err != nil {
		return err
	}
	if err := p.runOK(ctx, "postgres-create-database", createdb, append(baseArgs, job.Target.Database)); err != nil {
		return err
	}

	backupLower := strings.ToLower(opts.BackupFile)
	switch {
	case strings.HasSuffix(backupLower, ".sql"):
		return p.runOK(ctx, "postgres-restore-sql", psql, append(baseArgs, "-d", job.Target.Database, "-v", "ON_ERROR_STOP=1", "-f", opts.BackupFile))
	case strings.HasSuffix(backupLower, ".sql.gz"):
		temp, err := gunzipToTemp(opts.BackupFile)
		if err != nil {
			return err
		}
		defer os.Remove(temp)
		return p.runOK(ctx, "postgres-restore-sql", psql, append(baseArgs, "-d", job.Target.Database, "-v", "ON_ERROR_STOP=1", "-f", temp))
	case strings.HasSuffix(backupLower, ".dump"), strings.HasSuffix(backupLower, ".backup"):
		return p.runOK(ctx, "postgres-restore-custom", pgRestore, append(baseArgs, "-d", job.Target.Database, "--no-owner", "--exit-on-error", opts.BackupFile))
	default:
		return fmt.Errorf("unsupported PostgreSQL backup type: %s", opts.BackupFile)
	}
}

func (p PostgresProvider) runOK(ctx context.Context, category, executable string, args []string) error {
	result, err := p.Runner.Run(ctx, shell.Command{Category: category, Executable: executable, Args: args})
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("%s failed with exit code %d", category, result.ExitCode)
	}
	return nil
}

func requireSafePostgresDatabase(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if !pgDatabasePattern.MatchString(value) {
		return fmt.Errorf("unsafe PostgreSQL database name for %s: %s", field, value)
	}
	return nil
}

func requireFile(path, label string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%s is required", label)
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return fmt.Errorf("%s does not exist: %s", label, path)
	}
	return nil
}

func gunzipToTemp(path string) (string, error) {
	input, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer input.Close()
	gz, err := gzip.NewReader(input)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	temp, err := os.CreateTemp("", strings.TrimSuffix(filepath.Base(path), ".gz")+"-*.sql")
	if err != nil {
		return "", err
	}
	defer temp.Close()
	if _, err := temp.ReadFrom(gz); err != nil {
		_ = os.Remove(temp.Name())
		return "", err
	}
	return temp.Name(), nil
}
