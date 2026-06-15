package restore

import (
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

var (
	oracleIdentifierPattern = regexp.MustCompile(
		`^[A-Za-z][A-Za-z0-9_$#]*$`,
	)

	oracleDumpFilePattern = regexp.MustCompile(
		`(?i)^[A-Za-z0-9][A-Za-z0-9_.-]*\.dmp$`,
	)

	oracleLogNameUnsafePattern = regexp.MustCompile(
		`[^A-Za-z0-9_.-]+`,
	)
)

type OracleDataPumpProvider struct {
	Logger *logging.Logger
	Runner shell.Runner
}

func (p OracleDataPumpProvider) Restore(
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
			"Oracle Data Pump restore cancelled before validation: %w",
			err,
		)
	}

	jobName := strings.TrimSpace(job.Name)
	backupFile := strings.TrimSpace(opts.BackupFile)
	connectString := strings.TrimSpace(job.Target.ConnectString)
	schema := strings.TrimSpace(job.Target.Schema)
	oracleDirectory := strings.TrimSpace(
		job.Target.OracleDirectory,
	)
	credentialMethod := job.CredentialMethod()

	p.logInfo(fmt.Sprintf(
		"job=%s type=oracle backup_file=%s credential_method=%s command_category=oracle-impdp status=start",
		jobName,
		backupFile,
		credentialMethod,
	))

	if credentialMethod != config.DefaultOracleCredentialMethod {
		return fmt.Errorf(
			"job=%q unsupported Oracle credential_method %q; expected %q",
			jobName,
			credentialMethod,
			config.DefaultOracleCredentialMethod,
		)
	}

	if err := validateOracleValue(
		"target.connect_string",
		connectString,
	); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	if schema == "" ||
		!oracleIdentifierPattern.MatchString(schema) {
		return fmt.Errorf(
			"job=%q unsafe Oracle schema %q",
			jobName,
			schema,
		)
	}

	if oracleDirectory == "" ||
		!oracleIdentifierPattern.MatchString(oracleDirectory) {
		return fmt.Errorf(
			"job=%q unsafe Oracle directory object %q",
			jobName,
			oracleDirectory,
		)
	}

	if backupFile == "" {
		return fmt.Errorf(
			"job=%q Oracle backup file is required",
			jobName,
		)
	}

	if err := validateOracleBackupFile(backupFile); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	dumpFile := filepath.Base(backupFile)

	if !oracleDumpFilePattern.MatchString(dumpFile) {
		return fmt.Errorf(
			"job=%q unsafe Oracle dump file name %q; expected a simple .dmp filename",
			jobName,
			dumpFile,
		)
	}

	importLog := oracleImportLogName(jobName)

	impdpExecutable := strings.TrimSpace(
		cfg.ToolPath(
			job,
			config.TypeOracle,
			"impdp",
			"impdp",
		),
	)

	if err := validateOracleValue(
		"tools.oracle.impdp",
		impdpExecutable,
	); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	args := []string{
		connectString,
		"schemas=" + schema,
		"directory=" + oracleDirectory,
		"dumpfile=" + dumpFile,
		"table_exists_action=replace",
		"logfile=" + importLog,
	}

	p.logInfo(fmt.Sprintf(
		"job=%s type=oracle schema=%s oracle_directory=%s dumpfile=%s logfile=%s credential_method=%s",
		jobName,
		schema,
		oracleDirectory,
		dumpFile,
		importLog,
		credentialMethod,
	))

	p.logInfo(fmt.Sprintf(
		"job=%s type=oracle impdp=%s",
		jobName,
		impdpExecutable,
	))

	p.logInfo(fmt.Sprintf(
		"job=%s type=oracle oracle_note=Data_Pump_dump_file_must_exist_in_the_Oracle_directory_object_path_accessible_by_the_database_server dumpfile=%s directory=%s local_backup_file=%s",
		jobName,
		dumpFile,
		oracleDirectory,
		backupFile,
	))

	if opts.DryRun {
		p.logWarn(fmt.Sprintf(
			"job=%s type=oracle dry_run=true action=restore_skipped schema=%s directory=%s dumpfile=%s credential_method=%s",
			jobName,
			schema,
			oracleDirectory,
			dumpFile,
			credentialMethod,
		))

		return nil
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"job=%q Oracle Data Pump restore cancelled before execution: %w",
			jobName,
			err,
		)
	}

	if !shell.ExecutableAvailable(impdpExecutable) {
		return fmt.Errorf(
			"job=%q Oracle Data Pump executable not found or not executable: %q",
			jobName,
			impdpExecutable,
		)
	}

	result, runErr := p.Runner.Run(
		ctx,
		shell.Command{
			Category:   "oracle-impdp",
			Executable: impdpExecutable,
			Args:       args,
		},
	)

	if runErr != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return fmt.Errorf(
				"job=%q Oracle Data Pump restore cancelled: %w",
				jobName,
				contextErr,
			)
		}

		return fmt.Errorf(
			"job=%q oracle-impdp execution failed with exit code %d: %w",
			jobName,
			result.ExitCode,
			runErr,
		)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf(
			"job=%q oracle-impdp failed with exit code %d",
			jobName,
			result.ExitCode,
		)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"job=%q Oracle Data Pump restore context ended after execution: %w",
			jobName,
			err,
		)
	}

	p.logInfo(fmt.Sprintf(
		"job=%s type=oracle command_category=oracle-impdp status=completed exit_code=%d schema=%s dumpfile=%s logfile=%s",
		jobName,
		result.ExitCode,
		schema,
		dumpFile,
		importLog,
	))

	return nil
}

func validateOracleBackupFile(
	backupFile string,
) error {
	backupFile = strings.TrimSpace(backupFile)

	if err := validateOracleValue(
		"Oracle backup file",
		backupFile,
	); err != nil {
		return err
	}

	if !strings.EqualFold(
		filepath.Ext(backupFile),
		".dmp",
	) {
		return fmt.Errorf(
			"unsupported Oracle backup type %q; expected a .dmp file",
			backupFile,
		)
	}

	info, err := os.Lstat(backupFile)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf(
				"Oracle backup file does not exist: %q",
				backupFile,
			)
		}

		return fmt.Errorf(
			"inspect Oracle backup file %q: %w",
			backupFile,
			err,
		)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf(
			"Oracle backup file must not be a symbolic link: %q",
			backupFile,
		)
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf(
			"Oracle backup path is not a regular file: %q",
			backupFile,
		)
	}

	if info.Size() <= 0 {
		return fmt.Errorf(
			"Oracle backup file is empty: %q",
			backupFile,
		)
	}

	file, err := os.Open(backupFile)
	if err != nil {
		return fmt.Errorf(
			"open Oracle backup file %q: %w",
			backupFile,
			err,
		)
	}

	closeErr := file.Close()
	if closeErr != nil {
		return fmt.Errorf(
			"close Oracle backup file %q after readability check: %w",
			backupFile,
			closeErr,
		)
	}

	return nil
}

func validateOracleValue(
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

func oracleImportLogName(
	jobName string,
) string {
	jobName = strings.TrimSpace(jobName)

	safeName := oracleLogNameUnsafePattern.ReplaceAllString(
		jobName,
		"_",
	)

	safeName = strings.Trim(
		safeName,
		"_.-",
	)

	if safeName == "" {
		safeName = "oracle-restore"
	}

	const maximumBaseLength = 100
	if len(safeName) > maximumBaseLength {
		safeName = safeName[:maximumBaseLength]
		safeName = strings.TrimRight(
			safeName,
			"_.-",
		)

		if safeName == "" {
			safeName = "oracle-restore"
		}
	}

	return safeName + "-impdp.log"
}

func (p OracleDataPumpProvider) logInfo(
	message string,
) {
	if p.Logger != nil {
		p.Logger.Info(message)
	}
}

func (p OracleDataPumpProvider) logWarn(
	message string,
) {
	if p.Logger != nil {
		p.Logger.Warn(message)
	}
}
