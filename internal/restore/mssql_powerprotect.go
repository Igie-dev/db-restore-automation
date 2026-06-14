package restore

import (
	"context"
	"fmt"
	"os"
	"strings"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
	"db-restore-automation/internal/shell"
)

type MssqlPowerProtectProvider struct {
	Logger *logging.Logger
	Runner shell.Runner
}

func (p MssqlPowerProtectProvider) Restore(ctx context.Context, cfg config.Config, job config.JobConfig, opts Options) error {
	credentialMethod := job.CredentialMethod()
	if credentialMethod != "lockbox" {
		return fmt.Errorf("unsupported PowerProtect credential_method %q", credentialMethod)
	}
	if strings.TrimSpace(job.Source.Database) == "" || strings.TrimSpace(job.Target.Database) == "" {
		return fmt.Errorf("source.database and target.database are required")
	}
	required := map[string]string{
		"powerprotect.dd_host":      job.PowerProtect.DDHost,
		"powerprotect.dd_user":      job.PowerProtect.DDUser,
		"powerprotect.device_path":  job.PowerProtect.DevicePath,
		"powerprotect.lockbox_path": job.PowerProtect.LockboxPath,
		"powerprotect.client":       job.PowerProtect.Client,
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%s is required and must not contain newlines", name)
		}
	}
	relocationMap, err := powerProtectRelocationMap(job.Relocate)
	if err != nil {
		return err
	}
	skipClientResolution := "TRUE"
	if !job.PowerProtectSkipClientResolution() {
		skipClientResolution = "FALSE"
	}
	args := []string{
		"-a", "NSR_DFA_SI_DD_HOST=" + job.PowerProtect.DDHost,
		"-a", "NSR_DFA_SI_DD_USER=" + job.PowerProtect.DDUser,
		"-a", "NSR_DFA_SI_DEVICE_PATH=" + job.PowerProtect.DevicePath,
		"-a", "NSR_DFA_SI_DD_LOCKBOX_PATH=" + job.PowerProtect.LockboxPath,
		"-c", job.PowerProtect.Client,
		"-a", "SKIP_CLIENT_RESOLUTION=" + skipClientResolution,
		"-C", relocationMap,
		"-f",
		"-S", job.PowerProtectRestoreType(),
		"-d", "MSSQL:" + job.Target.Database,
		"MSSQL:" + job.Source.Database,
	}

	ddbmsqlrc := cfg.ToolPath(job, "mssql_powerprotect", "ddbmsqlrc", "ddbmsqlrc.exe")
	p.Logger.Info(fmt.Sprintf("job=%s type=mssql_powerprotect source_database=%s target_database=%s credential_method=%s restore_provider=DellPowerProtect", job.Name, job.Source.Database, job.Target.Database, credentialMethod))
	p.Logger.Info(fmt.Sprintf("job=%s type=mssql_powerprotect selected_backup=not_applicable restore_provider=DellPowerProtect", job.Name))
	p.Logger.Info(fmt.Sprintf("job=%s type=mssql_powerprotect ddbmsqlrc=%s", job.Name, ddbmsqlrc))
	p.Logger.Info(fmt.Sprintf("job=%s type=mssql_powerprotect dd_host=%s dd_user=%s client=%s restore_type=%s skip_client_resolution=%s", job.Name, job.PowerProtect.DDHost, job.PowerProtect.DDUser, job.PowerProtect.Client, job.PowerProtectRestoreType(), skipClientResolution))
	p.Logger.Info(fmt.Sprintf("job=%s type=mssql_powerprotect device_path=%s", job.Name, job.PowerProtect.DevicePath))
	p.Logger.Info(fmt.Sprintf("job=%s type=mssql_powerprotect lockbox_path=%s", job.Name, job.PowerProtect.LockboxPath))
	p.Logger.Info(fmt.Sprintf("job=%s type=mssql_powerprotect relocate_map=%s", job.Name, relocationMap))

	if opts.DryRun {
		p.Logger.Warn(fmt.Sprintf("job=%s type=mssql_powerprotect dry_run=true action=restore_skipped source_database=%s target_database=%s credential_method=%s selected_backup=not_applicable restore_provider=DellPowerProtect", job.Name, job.Source.Database, job.Target.Database, credentialMethod))
		return nil
	}
	if !shell.ExecutableAvailable(ddbmsqlrc) {
		return fmt.Errorf("PowerProtect executable not found: %s", ddbmsqlrc)
	}
	if info, err := os.Stat(job.PowerProtect.LockboxPath); err != nil || !info.IsDir() {
		return fmt.Errorf("PowerProtect lockbox path does not exist: %s", job.PowerProtect.LockboxPath)
	}

	result, err := p.Runner.Run(ctx, shell.Command{Category: "mssql-powerprotect-ddbmsqlrc", Executable: ddbmsqlrc, Args: args})
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("mssql-powerprotect-ddbmsqlrc failed with exit code %d", result.ExitCode)
	}
	return nil
}

func powerProtectRelocationMap(relocate []config.RelocateConfig) (string, error) {
	if len(relocate) == 0 {
		return "", fmt.Errorf("at least one relocate entry is required")
	}
	entries := make([]string, 0, len(relocate))
	for _, item := range relocate {
		if strings.TrimSpace(item.LogicalName) == "" || strings.TrimSpace(item.PhysicalPath) == "" {
			return "", fmt.Errorf("relocate.logical_name and relocate.physical_path are required")
		}
		if strings.ContainsAny(item.LogicalName, "\r\n") || strings.ContainsAny(item.PhysicalPath, "\r\n") {
			return "", fmt.Errorf("relocate values must not contain newlines")
		}
		logical := strings.ReplaceAll(item.LogicalName, "'", "''")
		physical := strings.ReplaceAll(item.PhysicalPath, "'", "''")
		entries = append(entries, fmt.Sprintf("'%s'='%s'", logical, physical))
	}
	return strings.Join(entries, ","), nil
}
