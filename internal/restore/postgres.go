package restore

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
	"db-restore-automation/internal/shell"
)

var pgDatabasePattern = regexp.MustCompile(
	`^[A-Za-z0-9_]+$`,
)

type postgresPreparedBackup struct {
	backupType string
	path       string
	reader     io.ReadCloser
	tempPath   string
}

type postgresContextReader struct {
	ctx    context.Context
	reader io.Reader
}

type PostgresProvider struct {
	Logger *logging.Logger
	Runner shell.Runner
}

func (p PostgresProvider) Restore(
	ctx context.Context,
	cfg config.Config,
	job config.JobConfig,
	opts Options,
) (restoreErr error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"PostgreSQL restore cancelled before validation: %w",
			err,
		)
	}

	jobName := strings.TrimSpace(job.Name)
	credentialMethod := job.CredentialMethod()

	backupFile := strings.TrimSpace(opts.BackupFile)
	host := strings.TrimSpace(job.Target.Host)
	username := strings.TrimSpace(job.Target.Username)
	targetDatabase := strings.TrimSpace(job.Target.Database)
	maintenanceDatabase := strings.TrimSpace(
		job.Target.MaintenanceDatabase,
	)
	port := job.Target.Port

	p.logInfo(fmt.Sprintf(
		"job=%s type=postgres backup_file=%s target_database=%s credential_method=%s command_category=postgres-restore status=start",
		jobName,
		backupFile,
		targetDatabase,
		credentialMethod,
	))

	if credentialMethod != config.DefaultPostgresCredentialMethod {
		return fmt.Errorf(
			"job=%q unsupported PostgreSQL credential_method %q; expected %q",
			jobName,
			credentialMethod,
			config.DefaultPostgresCredentialMethod,
		)
	}

	if err := requireSafePostgresDatabase(
		targetDatabase,
		"target.database",
	); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	if err := requireSafePostgresDatabase(
		maintenanceDatabase,
		"target.maintenance_database",
	); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	if targetDatabase == maintenanceDatabase {
		return fmt.Errorf(
			"job=%q target.database and target.maintenance_database must not be the same database",
			jobName,
		)
	}

	if strings.EqualFold(targetDatabase, "template0") ||
		strings.EqualFold(targetDatabase, "template1") {
		return fmt.Errorf(
			"job=%q PostgreSQL system template database must not be used as the restore target: %q",
			jobName,
			targetDatabase,
		)
	}

	if err := validatePostgresConnectionValue(
		"target.host",
		host,
	); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	if err := validatePostgresConnectionValue(
		"target.username",
		username,
	); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	if port < 1 || port > 65535 {
		return fmt.Errorf(
			"job=%q target.port must be between 1 and 65535",
			jobName,
		)
	}

	if backupFile == "" {
		return fmt.Errorf(
			"job=%q backup file is required",
			jobName,
		)
	}

	preparedBackup, err := preparePostgresBackup(
		ctx,
		backupFile,
	)
	if err != nil {
		return fmt.Errorf(
			"job=%q prepare PostgreSQL backup: %w",
			jobName,
			err,
		)
	}

	defer func() {
		if preparedBackup.reader != nil {
			if closeErr := preparedBackup.reader.Close(); closeErr != nil {
				if restoreErr == nil {
					restoreErr = fmt.Errorf(
						"close prepared PostgreSQL backup %q: %w",
						preparedBackup.path,
						closeErr,
					)
				} else {
					p.logWarn(fmt.Sprintf(
						"job=%s type=postgres action=close_backup_failed backup_file=%s error=%s",
						jobName,
						preparedBackup.path,
						closeErr.Error(),
					))
				}
			}
		}

		if preparedBackup.tempPath != "" {
			if removeErr := os.Remove(
				preparedBackup.tempPath,
			); removeErr != nil &&
				!os.IsNotExist(removeErr) {
				p.logWarn(fmt.Sprintf(
					"job=%s type=postgres action=remove_temporary_backup_failed temp_file=%s error=%s",
					jobName,
					preparedBackup.tempPath,
					removeErr.Error(),
				))
			}
		}
	}()

	psqlExecutable := strings.TrimSpace(
		cfg.ToolPath(
			job,
			config.TypePostgres,
			"psql",
			"psql",
		),
	)

	pgRestoreExecutable := strings.TrimSpace(
		cfg.ToolPath(
			job,
			config.TypePostgres,
			"pg_restore",
			"pg_restore",
		),
	)

	dropDBExecutable := strings.TrimSpace(
		cfg.ToolPath(
			job,
			config.TypePostgres,
			"dropdb",
			"dropdb",
		),
	)

	createDBExecutable := strings.TrimSpace(
		cfg.ToolPath(
			job,
			config.TypePostgres,
			"createdb",
			"createdb",
		),
	)

	requiredTools := []struct {
		field string
		value string
	}{
		{
			field: "tools.postgres.psql",
			value: psqlExecutable,
		},
		{
			field: "tools.postgres.dropdb",
			value: dropDBExecutable,
		},
		{
			field: "tools.postgres.createdb",
			value: createDBExecutable,
		},
	}

	if preparedBackup.backupType == "custom" {
		requiredTools = append(
			requiredTools,
			struct {
				field string
				value string
			}{
				field: "tools.postgres.pg_restore",
				value: pgRestoreExecutable,
			},
		)
	}

	for _, tool := range requiredTools {
		if err := validatePostgresConnectionValue(
			tool.field,
			tool.value,
		); err != nil {
			return fmt.Errorf(
				"job=%q %w",
				jobName,
				err,
			)
		}
	}

	p.logInfo(fmt.Sprintf(
		"job=%s type=postgres backup_type=%s prepared_backup=%s host=%s port=%d username=%s target_database=%s maintenance_database=%s",
		jobName,
		preparedBackup.backupType,
		preparedBackup.path,
		host,
		port,
		username,
		targetDatabase,
		maintenanceDatabase,
	))

	p.logInfo(fmt.Sprintf(
		"job=%s type=postgres psql=%s pg_restore=%s dropdb=%s createdb=%s",
		jobName,
		psqlExecutable,
		pgRestoreExecutable,
		dropDBExecutable,
		createDBExecutable,
	))

	if opts.DryRun {
		p.logWarn(fmt.Sprintf(
			"job=%s type=postgres dry_run=true action=restore_skipped target_database=%s maintenance_database=%s credential_method=%s backup_file=%s backup_type=%s",
			jobName,
			targetDatabase,
			maintenanceDatabase,
			credentialMethod,
			backupFile,
			preparedBackup.backupType,
		))

		return nil
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"job=%q PostgreSQL restore cancelled before execution: %w",
			jobName,
			err,
		)
	}

	if !shell.ExecutableAvailable(psqlExecutable) {
		return fmt.Errorf(
			"job=%q PostgreSQL psql executable not found or not executable: %q",
			jobName,
			psqlExecutable,
		)
	}

	if !shell.ExecutableAvailable(dropDBExecutable) {
		return fmt.Errorf(
			"job=%q PostgreSQL dropdb executable not found or not executable: %q",
			jobName,
			dropDBExecutable,
		)
	}

	if !shell.ExecutableAvailable(createDBExecutable) {
		return fmt.Errorf(
			"job=%q PostgreSQL createdb executable not found or not executable: %q",
			jobName,
			createDBExecutable,
		)
	}

	if preparedBackup.backupType == "custom" {
		if !shell.ExecutableAvailable(
			pgRestoreExecutable,
		) {
			return fmt.Errorf(
				"job=%q PostgreSQL pg_restore executable not found or not executable: %q",
				jobName,
				pgRestoreExecutable,
			)
		}

		// Validate the custom-format archive before terminating connections
		// or dropping the existing target database.
		if err := p.preflightPostgresCustomBackup(
			ctx,
			pgRestoreExecutable,
			preparedBackup.path,
		); err != nil {
			return fmt.Errorf(
				"job=%q PostgreSQL custom backup validation failed: %w",
				jobName,
				err,
			)
		}
	}

	baseArgs := []string{
		"--host",
		host,
		"--port",
		strconv.Itoa(port),
		"--username",
		username,
		"--no-password",
	}

	escapedDatabase := strings.ReplaceAll(
		targetDatabase,
		"'",
		"''",
	)

	terminateSQL := fmt.Sprintf(
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid();",
		escapedDatabase,
	)

	terminateArgs := postgresCloneStrings(baseArgs)
	terminateArgs = append(
		terminateArgs,
		"--dbname",
		maintenanceDatabase,
		"--no-psqlrc",
		"--set",
		"ON_ERROR_STOP=1",
		"--command",
		terminateSQL,
	)

	if err := p.runPostgresOK(
		ctx,
		"postgres-terminate-connections",
		psqlExecutable,
		terminateArgs,
		nil,
	); err != nil {
		return fmt.Errorf(
			"job=%q terminate active connections to PostgreSQL database %q: %w",
			jobName,
			targetDatabase,
			err,
		)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"job=%q PostgreSQL restore cancelled after terminating database connections: %w",
			jobName,
			err,
		)
	}

	dropArgs := postgresCloneStrings(baseArgs)
	dropArgs = append(
		dropArgs,
		"--maintenance-db",
		maintenanceDatabase,
		"--if-exists",
		targetDatabase,
	)

	if err := p.runPostgresOK(
		ctx,
		"postgres-drop-database",
		dropDBExecutable,
		dropArgs,
		nil,
	); err != nil {
		return fmt.Errorf(
			"job=%q drop PostgreSQL database %q: %w",
			jobName,
			targetDatabase,
			err,
		)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"job=%q PostgreSQL restore cancelled after dropping database %q: %w",
			jobName,
			targetDatabase,
			err,
		)
	}

	createArgs := postgresCloneStrings(baseArgs)
	createArgs = append(
		createArgs,
		"--maintenance-db",
		maintenanceDatabase,
		targetDatabase,
	)

	if err := p.runPostgresOK(
		ctx,
		"postgres-create-database",
		createDBExecutable,
		createArgs,
		nil,
	); err != nil {
		return fmt.Errorf(
			"job=%q create PostgreSQL database %q: %w",
			jobName,
			targetDatabase,
			err,
		)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"job=%q PostgreSQL restore cancelled after creating database %q: %w",
			jobName,
			targetDatabase,
			err,
		)
	}

	switch preparedBackup.backupType {
	case "sql":
		restoreArgs := postgresCloneStrings(baseArgs)
		restoreArgs = append(
			restoreArgs,
			"--dbname",
			targetDatabase,
			"--no-psqlrc",
			"--set",
			"ON_ERROR_STOP=1",
		)

		if err := p.runPostgresOK(
			ctx,
			"postgres-restore-sql",
			psqlExecutable,
			restoreArgs,
			preparedBackup.reader,
		); err != nil {
			return fmt.Errorf(
				"job=%q restore PostgreSQL SQL backup into database %q: %w",
				jobName,
				targetDatabase,
				err,
			)
		}

	case "custom":
		restoreArgs := postgresCloneStrings(baseArgs)
		restoreArgs = append(
			restoreArgs,
			"--dbname",
			targetDatabase,
			"--no-owner",
			"--exit-on-error",
			preparedBackup.path,
		)

		if err := p.runPostgresOK(
			ctx,
			"postgres-restore-custom",
			pgRestoreExecutable,
			restoreArgs,
			nil,
		); err != nil {
			return fmt.Errorf(
				"job=%q restore PostgreSQL custom backup into database %q: %w",
				jobName,
				targetDatabase,
				err,
			)
		}

	default:
		return fmt.Errorf(
			"job=%q unsupported prepared PostgreSQL backup type %q",
			jobName,
			preparedBackup.backupType,
		)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"job=%q PostgreSQL restore context ended after execution: %w",
			jobName,
			err,
		)
	}

	p.logInfo(fmt.Sprintf(
		"job=%s type=postgres command_category=postgres-restore status=completed target_database=%s backup_file=%s backup_type=%s",
		jobName,
		targetDatabase,
		backupFile,
		preparedBackup.backupType,
	))

	return nil
}

func preparePostgresBackup(
	ctx context.Context,
	backupFile string,
) (postgresPreparedBackup, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	backupFile = strings.TrimSpace(backupFile)

	if err := requireFile(
		backupFile,
		"PostgreSQL backup file",
	); err != nil {
		return postgresPreparedBackup{}, err
	}

	backupType, err := postgresBackupType(backupFile)
	if err != nil {
		return postgresPreparedBackup{}, err
	}

	switch backupType {
	case "sql":
		input, err := os.Open(backupFile)
		if err != nil {
			return postgresPreparedBackup{}, fmt.Errorf(
				"open PostgreSQL SQL backup %q: %w",
				backupFile,
				err,
			)
		}

		return postgresPreparedBackup{
			backupType: backupType,
			path:       backupFile,
			reader:     input,
		}, nil

	case "sql.gz":
		tempPath, err := gunzipToTempContext(
			ctx,
			backupFile,
		)
		if err != nil {
			return postgresPreparedBackup{}, err
		}

		input, err := os.Open(tempPath)
		if err != nil {
			_ = os.Remove(tempPath)

			return postgresPreparedBackup{}, fmt.Errorf(
				"open decompressed PostgreSQL backup %q: %w",
				tempPath,
				err,
			)
		}

		return postgresPreparedBackup{
			backupType: "sql",
			path:       tempPath,
			reader:     input,
			tempPath:   tempPath,
		}, nil

	case "custom":
		return postgresPreparedBackup{
			backupType: backupType,
			path:       backupFile,
		}, nil

	default:
		return postgresPreparedBackup{}, fmt.Errorf(
			"unsupported prepared PostgreSQL backup type %q",
			backupType,
		)
	}
}

func postgresBackupType(
	backupFile string,
) (string, error) {
	backupFile = strings.TrimSpace(backupFile)
	backupLower := strings.ToLower(backupFile)

	switch {
	case strings.HasSuffix(backupLower, ".sql.gz"):
		return "sql.gz", nil

	case strings.HasSuffix(backupLower, ".sql"):
		return "sql", nil

	case strings.HasSuffix(backupLower, ".dump"),
		strings.HasSuffix(backupLower, ".backup"):
		return "custom", nil

	default:
		return "", fmt.Errorf(
			"unsupported PostgreSQL backup type %q; expected .sql, .sql.gz, .dump, or .backup",
			backupFile,
		)
	}
}

func (p PostgresProvider) preflightPostgresCustomBackup(
	ctx context.Context,
	pgRestoreExecutable string,
	backupFile string,
) error {
	tempList, err := os.CreateTemp(
		"",
		"postgres-restore-list-*.txt",
	)
	if err != nil {
		return fmt.Errorf(
			"create temporary pg_restore list file: %w",
			err,
		)
	}

	tempListPath := tempList.Name()

	if closeErr := tempList.Close(); closeErr != nil {
		_ = os.Remove(tempListPath)

		return fmt.Errorf(
			"close temporary pg_restore list file: %w",
			closeErr,
		)
	}

	defer func() {
		if removeErr := os.Remove(tempListPath); removeErr != nil &&
			!os.IsNotExist(removeErr) {
			p.logWarn(fmt.Sprintf(
				"type=postgres action=remove_pg_restore_list_failed file=%s error=%s",
				tempListPath,
				removeErr.Error(),
			))
		}
	}()

	args := []string{
		"--list",
		"--file",
		tempListPath,
		backupFile,
	}

	if err := p.runPostgresOK(
		ctx,
		"postgres-validate-custom-backup",
		pgRestoreExecutable,
		args,
		nil,
	); err != nil {
		return err
	}

	info, err := os.Stat(tempListPath)
	if err != nil {
		return fmt.Errorf(
			"inspect pg_restore archive list %q: %w",
			tempListPath,
			err,
		)
	}

	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return fmt.Errorf(
			"pg_restore archive list is empty; backup may be invalid: %q",
			backupFile,
		)
	}

	return nil
}

func (p PostgresProvider) runPostgresOK(
	ctx context.Context,
	category string,
	executable string,
	args []string,
	stdin io.Reader,
) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"%s cancelled before execution: %w",
			category,
			err,
		)
	}

	result, runErr := p.Runner.Run(
		ctx,
		shell.Command{
			Category:   category,
			Executable: executable,
			Args:       postgresCloneStrings(args),
			Stdin:      stdin,
		},
	)

	if runErr != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return fmt.Errorf(
				"%s cancelled: %w",
				category,
				contextErr,
			)
		}

		return fmt.Errorf(
			"%s execution failed with exit code %d: %w",
			category,
			result.ExitCode,
			runErr,
		)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf(
			"%s failed with exit code %d",
			category,
			result.ExitCode,
		)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"%s context ended after execution: %w",
			category,
			err,
		)
	}

	return nil
}

func requireSafePostgresDatabase(
	value string,
	field string,
) error {
	value = strings.TrimSpace(value)

	if value == "" {
		return fmt.Errorf(
			"%s is required",
			field,
		)
	}

	if !pgDatabasePattern.MatchString(value) {
		return fmt.Errorf(
			"unsafe PostgreSQL database name for %s: %q; only letters, numbers, and underscores are allowed",
			field,
			value,
		)
	}

	return nil
}

func validatePostgresConnectionValue(
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

func requireFile(
	path string,
	label string,
) error {
	path = strings.TrimSpace(path)

	if path == "" {
		return fmt.Errorf(
			"%s is required",
			label,
		)
	}

	if strings.ContainsRune(path, '\x00') {
		return fmt.Errorf(
			"%s path must not contain a null character",
			label,
		)
	}

	if strings.ContainsAny(path, "\r\n") {
		return fmt.Errorf(
			"%s path must be a single-line value",
			label,
		)
	}

	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf(
				"%s does not exist: %q",
				label,
				path,
			)
		}

		return fmt.Errorf(
			"inspect %s %q: %w",
			label,
			path,
			err,
		)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf(
			"%s must not be a symbolic link: %q",
			label,
			path,
		)
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf(
			"%s is not a regular file: %q",
			label,
			path,
		)
	}

	if info.Size() <= 0 {
		return fmt.Errorf(
			"%s is empty: %q",
			label,
			path,
		)
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf(
			"open %s %q: %w",
			label,
			path,
			err,
		)
	}

	if closeErr := file.Close(); closeErr != nil {
		return fmt.Errorf(
			"close %s %q after readability check: %w",
			label,
			path,
			closeErr,
		)
	}

	return nil
}

func gunzipToTemp(
	path string,
) (string, error) {
	return gunzipToTempContext(
		context.Background(),
		path,
	)
}

func gunzipToTempContext(
	ctx context.Context,
	path string,
) (tempPath string, returnErr error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf(
			"gzip extraction cancelled before opening backup: %w",
			err,
		)
	}

	if err := requireFile(
		path,
		"gzip backup file",
	); err != nil {
		return "", err
	}

	input, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf(
			"open gzip backup %q: %w",
			path,
			err,
		)
	}

	defer func() {
		if closeErr := input.Close(); closeErr != nil &&
			returnErr == nil {
			returnErr = fmt.Errorf(
				"close gzip backup %q: %w",
				path,
				closeErr,
			)
		}
	}()

	gzipReader, err := gzip.NewReader(input)
	if err != nil {
		return "", fmt.Errorf(
			"open gzip stream %q: %w",
			path,
			err,
		)
	}

	defer func() {
		if closeErr := gzipReader.Close(); closeErr != nil &&
			returnErr == nil {
			returnErr = fmt.Errorf(
				"close gzip reader for %q: %w",
				path,
				closeErr,
			)
		}
	}()

	baseName := strings.TrimSuffix(
		filepath.Base(path),
		filepath.Ext(path),
	)

	baseName = strings.TrimSuffix(
		baseName,
		".sql",
	)

	if baseName == "" {
		baseName = "postgres-backup"
	}

	baseName = regexp.MustCompile(
		`[^A-Za-z0-9_.-]+`,
	).ReplaceAllString(
		baseName,
		"_",
	)

	tempFile, err := os.CreateTemp(
		"",
		baseName+"-*.sql",
	)
	if err != nil {
		return "", fmt.Errorf(
			"create temporary SQL file: %w",
			err,
		)
	}

	tempPath = tempFile.Name()
	completed := false

	defer func() {
		if closeErr := tempFile.Close(); closeErr != nil &&
			returnErr == nil {
			returnErr = fmt.Errorf(
				"close temporary SQL file %q: %w",
				tempPath,
				closeErr,
			)
		}

		if !completed {
			_ = os.Remove(tempPath)
		}
	}()

	contextReader := &postgresContextReader{
		ctx:    ctx,
		reader: gzipReader,
	}

	written, err := io.Copy(
		tempFile,
		contextReader,
	)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return "", fmt.Errorf(
				"decompress PostgreSQL backup cancelled: %w",
				contextErr,
			)
		}

		return "", fmt.Errorf(
			"decompress PostgreSQL backup %q: %w",
			path,
			err,
		)
	}

	if written <= 0 {
		return "", fmt.Errorf(
			"decompressed PostgreSQL backup is empty: %q",
			path,
		)
	}

	if err := tempFile.Sync(); err != nil {
		return "", fmt.Errorf(
			"flush temporary SQL file %q: %w",
			tempPath,
			err,
		)
	}

	if err := tempFile.Close(); err != nil {
		return "", fmt.Errorf(
			"close temporary SQL file %q: %w",
			tempPath,
			err,
		)
	}

	completed = true

	return tempPath, nil
}

func (r *postgresContextReader) Read(
	buffer []byte,
) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}

	count, err := r.reader.Read(buffer)

	if err == nil {
		if contextErr := r.ctx.Err(); contextErr != nil {
			return count, contextErr
		}
	}

	return count, err
}

func postgresCloneStrings(
	values []string,
) []string {
	if len(values) == 0 {
		return nil
	}

	cloned := make(
		[]string,
		len(values),
	)

	copy(cloned, values)

	return cloned
}

func (p PostgresProvider) logInfo(
	message string,
) {
	if p.Logger != nil {
		p.Logger.Info(message)
	}
}

func (p PostgresProvider) logWarn(
	message string,
) {
	if p.Logger != nil {
		p.Logger.Warn(message)
	}
}

