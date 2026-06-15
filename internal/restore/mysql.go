package restore

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
	"db-restore-automation/internal/shell"
)

var (
	mysqlDatabasePattern  = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	mysqlLoginPathPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

type MySQLProvider struct {
	Logger *logging.Logger
	Runner shell.Runner
}

func (p MySQLProvider) Restore(
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
			"MySQL restore cancelled before validation: %w",
			err,
		)
	}

	jobName := strings.TrimSpace(job.Name)
	targetDatabase := strings.TrimSpace(job.Target.Database)
	backupFile := strings.TrimSpace(opts.BackupFile)
	credentialMethod := job.CredentialMethod()

	p.logInfo(fmt.Sprintf(
		"job=%s type=mysql backup_file=%s target_database=%s credential_method=%s command_category=mysql-restore status=start",
		jobName,
		backupFile,
		targetDatabase,
		credentialMethod,
	))

	if targetDatabase == "" ||
		!mysqlDatabasePattern.MatchString(targetDatabase) {
		return fmt.Errorf(
			"job=%q unsafe MySQL database name %q; only letters, numbers, and underscores are allowed",
			jobName,
			targetDatabase,
		)
	}

	if backupFile == "" {
		return fmt.Errorf(
			"job=%q backup file is required",
			jobName,
		)
	}

	backupType, err := mysqlBackupType(backupFile)
	if err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	if err := requireFile(backupFile, "backup file"); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	baseArgs, err := mysqlBaseArgs(job)
	if err != nil {
		return fmt.Errorf(
			"job=%q invalid MySQL connection configuration: %w",
			jobName,
			err,
		)
	}

	mysqlExecutable := strings.TrimSpace(
		cfg.ToolPath(
			job,
			config.TypeMySQL,
			"mysql",
			"mysql",
		),
	)

	if err := validateMySQLValue(
		"tools.mysql.mysql",
		mysqlExecutable,
	); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	// Open and validate the backup before dropping the target database.
	// This prevents destroying the target and then discovering that the
	// selected backup is unreadable or has an invalid gzip header.
	backupReader, closeBackup, err := openMySQLBackup(
		backupFile,
		backupType,
	)
	if err != nil {
		return fmt.Errorf(
			"job=%q prepare MySQL backup %q: %w",
			jobName,
			backupFile,
			err,
		)
	}

	defer func() {
		if closeErr := closeBackup(); closeErr != nil {
			wrappedCloseErr := fmt.Errorf(
				"close MySQL backup %q: %w",
				backupFile,
				closeErr,
			)

			if restoreErr == nil {
				restoreErr = wrappedCloseErr
				return
			}

			restoreErr = fmt.Errorf(
				"%v; additionally, %w",
				restoreErr,
				wrappedCloseErr,
			)
		}
	}()

	p.logInfo(fmt.Sprintf(
		"job=%s type=mysql mysql_executable=%s backup_type=%s backup_file=%s",
		jobName,
		mysqlExecutable,
		backupType,
		backupFile,
	))

	if opts.DryRun {
		p.logWarn(fmt.Sprintf(
			"job=%s type=mysql dry_run=true action=restore_skipped target_database=%s credential_method=%s backup_file=%s backup_type=%s",
			jobName,
			targetDatabase,
			credentialMethod,
			backupFile,
			backupType,
		))

		return nil
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"job=%q MySQL restore cancelled before execution: %w",
			jobName,
			err,
		)
	}

	if !shell.ExecutableAvailable(mysqlExecutable) {
		return fmt.Errorf(
			"job=%q MySQL executable not found or not executable: %q",
			jobName,
			mysqlExecutable,
		)
	}

	databaseIdentifier := strings.ReplaceAll(
		targetDatabase,
		"`",
		"``",
	)

	recreateDDL := fmt.Sprintf(
		"DROP DATABASE IF EXISTS `%s`; CREATE DATABASE `%s`;",
		databaseIdentifier,
		databaseIdentifier,
	)

	recreateArgs := cloneStrings(baseArgs)
	recreateArgs = append(
		recreateArgs,
		"--execute",
		recreateDDL,
	)

	if err := p.runOK(
		ctx,
		"mysql-recreate-database",
		mysqlExecutable,
		recreateArgs,
		nil,
	); err != nil {
		return fmt.Errorf(
			"job=%q recreate MySQL database %q: %w",
			jobName,
			targetDatabase,
			err,
		)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"job=%q MySQL restore cancelled after recreating database %q: %w",
			jobName,
			targetDatabase,
			err,
		)
	}

	restoreArgs := cloneStrings(baseArgs)
	restoreArgs = append(
		restoreArgs,
		"--database",
		targetDatabase,
	)

	if err := p.runOK(
		ctx,
		"mysql-restore-sql",
		mysqlExecutable,
		restoreArgs,
		backupReader,
	); err != nil {
		return fmt.Errorf(
			"job=%q restore MySQL database %q from %q: %w",
			jobName,
			targetDatabase,
			backupFile,
			err,
		)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"job=%q MySQL restore context ended after execution: %w",
			jobName,
			err,
		)
	}

	p.logInfo(fmt.Sprintf(
		"job=%s type=mysql command_category=mysql-restore status=completed target_database=%s backup_file=%s backup_type=%s",
		jobName,
		targetDatabase,
		backupFile,
		backupType,
	))

	return nil
}

func mysqlBaseArgs(job config.JobConfig) ([]string, error) {
	credentialMethod := job.CredentialMethod()

	host := strings.TrimSpace(job.Target.Host)
	username := strings.TrimSpace(job.Target.Username)
	loginPath := strings.TrimSpace(job.Target.LoginPath)
	defaultsFile := strings.TrimSpace(job.Target.DefaultsFile)
	port := job.Target.Port

	if host != "" {
		if err := validateMySQLValue(
			"target.host",
			host,
		); err != nil {
			return nil, err
		}
	}

	if username != "" {
		if err := validateMySQLValue(
			"target.username",
			username,
		); err != nil {
			return nil, err
		}
	}

	if port < 0 || port > 65535 {
		return nil, fmt.Errorf(
			"target.port must be between 1 and 65535 when provided",
		)
	}

	var args []string

	switch credentialMethod {
	case "login_path":
		if loginPath == "" {
			return nil, fmt.Errorf(
				"target.login_path is required when credential_method=login_path",
			)
		}

		if !mysqlLoginPathPattern.MatchString(loginPath) {
			return nil, fmt.Errorf(
				"target.login_path %q contains invalid characters",
				loginPath,
			)
		}

		if defaultsFile != "" {
			return nil, fmt.Errorf(
				"target.defaults_file must be empty when credential_method=login_path",
			)
		}

		args = append(
			args,
			"--login-path="+loginPath,
		)

	case "defaults_file":
		if defaultsFile == "" {
			return nil, fmt.Errorf(
				"target.defaults_file is required when credential_method=defaults_file",
			)
		}

		if err := validateMySQLValue(
			"target.defaults_file",
			defaultsFile,
		); err != nil {
			return nil, err
		}

		if loginPath != "" {
			return nil, fmt.Errorf(
				"target.login_path must be empty when credential_method=defaults_file",
			)
		}

		if err := requireFile(
			defaultsFile,
			"MySQL defaults file",
		); err != nil {
			return nil, err
		}

		// MySQL requires --defaults-extra-file to appear before other
		// command-line options.
		args = append(
			args,
			"--defaults-extra-file="+defaultsFile,
		)

	default:
		return nil, fmt.Errorf(
			"unsupported MySQL credential_method %q",
			credentialMethod,
		)
	}

	// Explicit target values override matching values stored in the login
	// path or defaults file while keeping passwords outside the YAML file.
	if host != "" {
		args = append(
			args,
			"--host",
			host,
		)
	}

	if port > 0 {
		args = append(
			args,
			"--port",
			strconv.Itoa(port),
		)
	}

	if username != "" {
		args = append(
			args,
			"--user",
			username,
		)
	}

	// Prevent the client from prompting indefinitely for a password when
	// the configured credential store is missing or invalid.
	args = append(
		args,
		"--connect-timeout=30",
	)

	return args, nil
}

func mysqlBackupType(backupFile string) (string, error) {
	backupLower := strings.ToLower(
		strings.TrimSpace(backupFile),
	)

	switch {
	case strings.HasSuffix(backupLower, ".sql.gz"):
		return "sql.gz", nil

	case strings.HasSuffix(backupLower, ".sql"):
		return "sql", nil

	default:
		return "", fmt.Errorf(
			"unsupported MySQL backup type %q; expected .sql or .sql.gz",
			backupFile,
		)
	}
}

func openMySQLBackup(
	backupFile string,
	backupType string,
) (io.Reader, func() error, error) {
	input, err := os.Open(backupFile)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"open backup file: %w",
			err,
		)
	}

	switch backupType {
	case "sql":
		return input, input.Close, nil

	case "sql.gz":
		gzipReader, err := gzip.NewReader(input)
		if err != nil {
			closeErr := input.Close()
			if closeErr != nil {
				return nil, nil, fmt.Errorf(
					"open gzip stream: %v; close backup file: %w",
					err,
					closeErr,
				)
			}

			return nil, nil, fmt.Errorf(
				"open gzip stream: %w",
				err,
			)
		}

		closeFn := func() error {
			gzipCloseErr := gzipReader.Close()
			fileCloseErr := input.Close()

			switch {
			case gzipCloseErr != nil && fileCloseErr != nil:
				return fmt.Errorf(
					"close gzip reader: %v; close backup file: %w",
					gzipCloseErr,
					fileCloseErr,
				)

			case gzipCloseErr != nil:
				return fmt.Errorf(
					"close gzip reader: %w",
					gzipCloseErr,
				)

			case fileCloseErr != nil:
				return fmt.Errorf(
					"close backup file: %w",
					fileCloseErr,
				)

			default:
				return nil
			}
		}

		return gzipReader, closeFn, nil

	default:
		_ = input.Close()

		return nil, nil, fmt.Errorf(
			"unsupported prepared MySQL backup type %q",
			backupType,
		)
	}
}

func (p MySQLProvider) runOK(
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
			Args:       cloneStrings(args),
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

func validateMySQLValue(
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

func cloneStrings(values []string) []string {
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

func (p MySQLProvider) logInfo(message string) {
	if p.Logger != nil {
		p.Logger.Info(message)
	}
}

func (p MySQLProvider) logWarn(message string) {
	if p.Logger != nil {
		p.Logger.Warn(message)
	}
}

