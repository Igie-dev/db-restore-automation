package restore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
	"db-restore-automation/internal/shell"
)

type MssqlPowerProtectProvider struct {
	Logger *logging.Logger
	Runner shell.Runner
}

func (p MssqlPowerProtectProvider) Restore(
	ctx context.Context,
	cfg config.Config,
	job config.JobConfig,
	opts Options,
) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"PowerProtect restore cancelled before validation: %w",
			err,
		)
	}

	jobName := strings.TrimSpace(job.Name)
	sourceDatabase := strings.TrimSpace(job.Source.Database)
	targetDatabase := strings.TrimSpace(job.Target.Database)
	credentialMethod := job.CredentialMethod()
	restoreType := job.PowerProtectRestoreType()

	ddHost := strings.TrimSpace(job.PowerProtect.DDHost)
	ddUser := strings.TrimSpace(job.PowerProtect.DDUser)
	devicePath := strings.TrimSpace(job.PowerProtect.DevicePath)
	lockboxPath := strings.TrimSpace(job.PowerProtect.LockboxPath)
	client := strings.TrimSpace(job.PowerProtect.Client)

	if credentialMethod != config.DefaultPowerProtectCredentialMethod {
		return fmt.Errorf(
			"job=%q unsupported PowerProtect credential_method %q; expected %q",
			jobName,
			credentialMethod,
			config.DefaultPowerProtectCredentialMethod,
		)
	}

	requiredValues := []struct {
		name  string
		value string
	}{
		{
			name:  "source.database",
			value: sourceDatabase,
		},
		{
			name:  "target.database",
			value: targetDatabase,
		},
		{
			name:  "powerprotect.dd_host",
			value: ddHost,
		},
		{
			name:  "powerprotect.dd_user",
			value: ddUser,
		},
		{
			name:  "powerprotect.device_path",
			value: devicePath,
		},
		{
			name:  "powerprotect.lockbox_path",
			value: lockboxPath,
		},
		{
			name:  "powerprotect.client",
			value: client,
		},
	}

	for _, required := range requiredValues {
		if err := validatePowerProtectValue(
			required.name,
			required.value,
		); err != nil {
			return fmt.Errorf(
				"job=%q %w",
				jobName,
				err,
			)
		}
	}

	if err := validatePowerProtectValue(
		"powerprotect.restore_type",
		restoreType,
	); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	if restoreType != config.DefaultPowerProtectRun {
		return fmt.Errorf(
			"job=%q unsupported PowerProtect restore_type %q; expected %q",
			jobName,
			restoreType,
			config.DefaultPowerProtectRun,
		)
	}

	relocationMap, err := powerProtectRelocationMap(job.Relocate)
	if err != nil {
		return fmt.Errorf(
			"job=%q invalid PowerProtect relocation configuration: %w",
			jobName,
			err,
		)
	}

	skipClientResolution := "FALSE"
	if job.PowerProtectSkipClientResolution() {
		skipClientResolution = "TRUE"
	}

	ddbmsqlrc := strings.TrimSpace(
		cfg.ToolPath(
			job,
			config.TypeMSSQLPowerProtect,
			"ddbmsqlrc",
			"ddbmsqlrc.exe",
		),
	)

	if err := validatePowerProtectValue(
		"tools.mssql_powerprotect.ddbmsqlrc",
		ddbmsqlrc,
	); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	args := []string{
		"-a",
		"NSR_DFA_SI_DD_HOST=" + ddHost,

		"-a",
		"NSR_DFA_SI_DD_USER=" + ddUser,

		"-a",
		"NSR_DFA_SI_DEVICE_PATH=" + devicePath,

		"-a",
		"NSR_DFA_SI_DD_LOCKBOX_PATH=" + lockboxPath,

		"-c",
		client,

		"-a",
		"SKIP_CLIENT_RESOLUTION=" + skipClientResolution,

		"-C",
		relocationMap,

		"-f",

		"-S",
		restoreType,

		"-d",
		"MSSQL:" + targetDatabase,

		"MSSQL:" + sourceDatabase,
	}

	p.logInfo(fmt.Sprintf(
		"job=%s type=mssql_powerprotect source_database=%s target_database=%s credential_method=%s restore_provider=DellPowerProtect",
		jobName,
		sourceDatabase,
		targetDatabase,
		credentialMethod,
	))

	p.logInfo(fmt.Sprintf(
		"job=%s type=mssql_powerprotect selected_backup=not_applicable restore_provider=DellPowerProtect",
		jobName,
	))

	p.logInfo(fmt.Sprintf(
		"job=%s type=mssql_powerprotect ddbmsqlrc=%s",
		jobName,
		ddbmsqlrc,
	))

	p.logInfo(fmt.Sprintf(
		"job=%s type=mssql_powerprotect dd_host=%s dd_user=%s client=%s restore_type=%s skip_client_resolution=%s",
		jobName,
		ddHost,
		ddUser,
		client,
		restoreType,
		skipClientResolution,
	))

	p.logInfo(fmt.Sprintf(
		"job=%s type=mssql_powerprotect device_path=%s",
		jobName,
		devicePath,
	))

	p.logInfo(fmt.Sprintf(
		"job=%s type=mssql_powerprotect lockbox_path=%s",
		jobName,
		lockboxPath,
	))

	p.logInfo(fmt.Sprintf(
		"job=%s type=mssql_powerprotect relocate_map=%s",
		jobName,
		relocationMap,
	))

	if opts.DryRun {
		p.logWarn(fmt.Sprintf(
			"job=%s type=mssql_powerprotect dry_run=true action=restore_skipped source_database=%s target_database=%s credential_method=%s selected_backup=not_applicable restore_provider=DellPowerProtect",
			jobName,
			sourceDatabase,
			targetDatabase,
			credentialMethod,
		))

		return nil
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"job=%q PowerProtect restore cancelled before execution: %w",
			jobName,
			err,
		)
	}

	if !shell.ExecutableAvailable(ddbmsqlrc) {
		return fmt.Errorf(
			"job=%q PowerProtect executable not found or not executable: %q",
			jobName,
			ddbmsqlrc,
		)
	}

	if err := validatePowerProtectLockboxPath(lockboxPath); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	command := shell.Command{
		Category:   "mssql-powerprotect-ddbmsqlrc",
		Executable: ddbmsqlrc,
		Args:       args,
	}

	result, runErr := p.Runner.Run(ctx, command)

	if runErr != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return fmt.Errorf(
				"job=%q PowerProtect restore cancelled: %w",
				jobName,
				contextErr,
			)
		}

		return fmt.Errorf(
			"job=%q mssql-powerprotect-ddbmsqlrc execution failed with exit code %d: %w",
			jobName,
			result.ExitCode,
			runErr,
		)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf(
			"job=%q mssql-powerprotect-ddbmsqlrc failed with exit code %d",
			jobName,
			result.ExitCode,
		)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"job=%q PowerProtect restore context ended after execution: %w",
			jobName,
			err,
		)
	}

	p.logInfo(fmt.Sprintf(
		"job=%s type=mssql_powerprotect restore_provider=DellPowerProtect action=restore_completed exit_code=%d",
		jobName,
		result.ExitCode,
	))

	return nil
}

func powerProtectRelocationMap(
	relocate []config.RelocateConfig,
) (string, error) {
	if len(relocate) == 0 {
		return "", fmt.Errorf(
			"at least one relocate entry is required",
		)
	}

	entries := make(
		[]string,
		0,
		len(relocate),
	)

	seenLogicalNames := make(map[string]int)
	seenPhysicalPaths := make(map[string]int)

	for index, item := range relocate {
		logicalName := strings.TrimSpace(item.LogicalName)
		physicalPath := strings.TrimSpace(item.PhysicalPath)

		if err := validatePowerProtectValue(
			fmt.Sprintf(
				"relocate[%d].logical_name",
				index,
			),
			logicalName,
		); err != nil {
			return "", err
		}

		if err := validatePowerProtectValue(
			fmt.Sprintf(
				"relocate[%d].physical_path",
				index,
			),
			physicalPath,
		); err != nil {
			return "", err
		}

		logicalKey := strings.ToLower(logicalName)
		if previousIndex, exists := seenLogicalNames[logicalKey]; exists {
			return "", fmt.Errorf(
				"relocate[%d].logical_name duplicates relocate[%d].logical_name: %q",
				index,
				previousIndex,
				logicalName,
			)
		}

		seenLogicalNames[logicalKey] = index

		physicalKey := strings.ToLower(
			filepath.Clean(physicalPath),
		)

		if previousIndex, exists := seenPhysicalPaths[physicalKey]; exists {
			return "", fmt.Errorf(
				"relocate[%d].physical_path duplicates relocate[%d].physical_path: %q",
				index,
				previousIndex,
				physicalPath,
			)
		}

		seenPhysicalPaths[physicalKey] = index

		escapedLogicalName := strings.ReplaceAll(
			logicalName,
			"'",
			"''",
		)

		escapedPhysicalPath := strings.ReplaceAll(
			physicalPath,
			"'",
			"''",
		)

		entries = append(
			entries,
			fmt.Sprintf(
				"'%s'='%s'",
				escapedLogicalName,
				escapedPhysicalPath,
			),
		)
	}

	return strings.Join(entries, ","), nil
}

func validatePowerProtectValue(
	field string,
	value string,
) error {
	value = strings.TrimSpace(value)

	if value == "" {
		return fmt.Errorf(
			"%s is required",
			field,
		)
	}

	if strings.ContainsRune(value, '\x00') {
		return fmt.Errorf(
			"%s must not contain a null character",
			field,
		)
	}

	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf(
			"%s must be a single-line value",
			field,
		)
	}

	return nil
}

func validatePowerProtectLockboxPath(
	lockboxPath string,
) error {
	info, err := os.Stat(lockboxPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf(
				"PowerProtect lockbox path does not exist: %q",
				lockboxPath,
			)
		}

		return fmt.Errorf(
			"inspect PowerProtect lockbox path %q: %w",
			lockboxPath,
			err,
		)
	}

	if !info.IsDir() {
		return fmt.Errorf(
			"PowerProtect lockbox path is not a directory: %q",
			lockboxPath,
		)
	}

	return nil
}

func (p MssqlPowerProtectProvider) logInfo(
	message string,
) {
	if p.Logger != nil {
		p.Logger.Info(message)
	}
}

func (p MssqlPowerProtectProvider) logWarn(
	message string,
) {
	if p.Logger != nil {
		p.Logger.Warn(message)
	}
}

