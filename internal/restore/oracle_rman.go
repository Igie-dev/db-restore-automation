package restore

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
	"db-restore-automation/internal/shell"
)

const rmanLogTailMaximumBytes = 2400

type OracleRmanProvider struct {
	Logger *logging.Logger
	Runner shell.Runner
}

type rmanLogSnapshot struct {
	Exists       bool
	Size         int64
	ModifiedTime time.Time
}

func (p OracleRmanProvider) Restore(
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
			"Oracle RMAN restore cancelled before validation: %w",
			err,
		)
	}

	jobName := strings.TrimSpace(job.Name)
	credentialMethod := job.CredentialMethod()
	restoreScope := job.RestoreScope()

	target := strings.TrimSpace(job.RMAN.Target)
	catalog := strings.TrimSpace(job.RMAN.Catalog)
	commandFile := strings.TrimSpace(job.RMAN.CommandFile)
	logFile := strings.TrimSpace(job.RMAN.LogFile)
	oracleHome := strings.TrimSpace(job.RMAN.OracleHome)
	oracleSID := strings.TrimSpace(job.RMAN.OracleSID)
	logFileConfigured := logFile != ""

	rmanExecutable := strings.TrimSpace(
		cfg.ToolPath(
			job,
			config.TypeOracleRMAN,
			"rman",
			"rman",
		),
	)

	if err := rmanValidateValue(
		"tools.oracle_rman.rman",
		rmanExecutable,
	); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	if !rmanOneOf(
		credentialMethod,
		config.DefaultOracleRMANCredentialMethod,
		"oracle_wallet",
	) {
		return fmt.Errorf(
			"job=%q unsupported RMAN credential_method %q; expected os_auth or oracle_wallet",
			jobName,
			credentialMethod,
		)
	}

	if restoreScope != config.DefaultRMANScope {
		return fmt.Errorf(
			"job=%q unsupported RMAN restore_scope %q; expected %q",
			jobName,
			restoreScope,
			config.DefaultRMANScope,
		)
	}

	if err := rmanValidateConnectionSpec(
		"rman.target",
		target,
	); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	if catalog != "" {
		if err := rmanValidateConnectionSpec(
			"rman.catalog",
			catalog,
		); err != nil {
			return fmt.Errorf(
				"job=%q %w",
				jobName,
				err,
			)
		}
	}

	if err := rmanValidateValue(
		"rman.command_file",
		commandFile,
	); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	if err := rmanValidateValue(
		"rman.oracle_home",
		oracleHome,
	); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	if err := rmanValidateValue(
		"rman.oracle_sid",
		oracleSID,
	); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	if !rmanValidOracleSID(oracleSID) {
		return fmt.Errorf(
			"job=%q rman.oracle_sid %q contains invalid characters",
			jobName,
			oracleSID,
		)
	}

	absoluteCommandFile, err := rmanAbsolutePath(
		commandFile,
		"rman.command_file",
	)
	if err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	absoluteOracleHome, err := rmanAbsolutePath(
		oracleHome,
		"rman.oracle_home",
	)
	if err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	absoluteLogFile := ""
	if logFileConfigured {
		absoluteLogFile, err = rmanAbsolutePath(
			logFile,
			"rman.log_file",
		)
		if err != nil {
			return fmt.Errorf(
				"job=%q %w",
				jobName,
				err,
			)
		}

		if rmanSamePath(
			absoluteCommandFile,
			absoluteLogFile,
		) {
			return fmt.Errorf(
				"job=%q rman.command_file and rman.log_file must not reference the same path",
				jobName,
			)
		}

		if err := rmanValidateLogTarget(
			absoluteLogFile,
		); err != nil {
			return fmt.Errorf(
				"job=%q %w",
				jobName,
				err,
			)
		}
	}

	if err := rmanRequireReadableRegularFile(
		absoluteCommandFile,
		"RMAN command file",
	); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	if err := rmanRequireDirectory(
		absoluteOracleHome,
		"ORACLE_HOME",
	); err != nil {
		return fmt.Errorf(
			"job=%q %w",
			jobName,
			err,
		)
	}

	environment := rmanBuildEnvironment(
		absoluteOracleHome,
		oracleSID,
	)

	args := []string{
		"target",
		target,
	}

	if catalog != "" {
		args = append(
			args,
			"catalog",
			catalog,
		)
	}

	args = append(
		args,
		"cmdfile="+absoluteCommandFile,
	)

	if logFileConfigured {
		args = append(
			args,
			"log="+absoluteLogFile,
		)
	}

	logFileValue := "<not-configured; output captured by command runner>"
	if logFileConfigured {
		logFileValue = absoluteLogFile
	}

	p.logInfo(fmt.Sprintf(
		"job=%s type=oracle_rman target=%s credential_method=%s restore_scope=%s command_file=%s log_file=%s oracle_home=%s oracle_sid=%s",
		jobName,
		target,
		credentialMethod,
		restoreScope,
		absoluteCommandFile,
		logFileValue,
		absoluteOracleHome,
		oracleSID,
	))

	p.logInfo(fmt.Sprintf(
		"job=%s type=oracle_rman rman_executable=%s catalog_configured=%t rman_log_configured=%t",
		jobName,
		rmanExecutable,
		catalog != "",
		logFileConfigured,
	))

	if opts.DryRun {
		p.logWarn(fmt.Sprintf(
			"job=%s type=oracle_rman dry_run=true action=restore_skipped command_file=%s log_file=%s credential_method=%s restore_scope=%s",
			jobName,
			absoluteCommandFile,
			logFileValue,
			credentialMethod,
			restoreScope,
		))

		return nil
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"job=%q Oracle RMAN restore cancelled before execution: %w",
			jobName,
			err,
		)
	}

	if !shell.ExecutableAvailable(rmanExecutable) {
		return fmt.Errorf(
			"job=%q Oracle RMAN executable not found or not executable: %q",
			jobName,
			rmanExecutable,
		)
	}

	var beforeLog rmanLogSnapshot

	if logFileConfigured {
		logDirectory := filepath.Dir(absoluteLogFile)

		if err := os.MkdirAll(logDirectory, 0755); err != nil {
			return fmt.Errorf(
				"job=%q create RMAN log directory %q: %w",
				jobName,
				logDirectory,
				err,
			)
		}

		if err := rmanRequireDirectory(
			logDirectory,
			"RMAN log directory",
		); err != nil {
			return fmt.Errorf(
				"job=%q %w",
				jobName,
				err,
			)
		}

		if err := rmanValidateLogTarget(
			absoluteLogFile,
		); err != nil {
			return fmt.Errorf(
				"job=%q %w",
				jobName,
				err,
			)
		}

		beforeLog, err = captureRMANLogSnapshot(
			absoluteLogFile,
		)
		if err != nil {
			return fmt.Errorf(
				"job=%q inspect RMAN log before execution: %w",
				jobName,
				err,
			)
		}
	}

	startedAt := time.Now()

	result, runErr := p.Runner.Run(
		ctx,
		shell.Command{
			Category:   "oracle-rman",
			Executable: rmanExecutable,
			Args:       append([]string(nil), args...),
			Env:        append([]string(nil), environment...),
		},
	)

	if logFileConfigured {
		afterLog, snapshotErr := captureRMANLogSnapshot(
			absoluteLogFile,
		)

		logChanged := false
		if snapshotErr != nil {
			p.logWarn(fmt.Sprintf(
				"job=%s type=oracle_rman action=inspect_log_failed log_file=%s error=%s",
				jobName,
				absoluteLogFile,
				snapshotErr.Error(),
			))
		} else {
			logChanged = rmanLogWasUpdated(
				beforeLog,
				afterLog,
				startedAt,
			)
		}

		if logChanged {
			logTail := tailTextFile(
				absoluteLogFile,
				rmanLogTailMaximumBytes,
			)

			if logTail != "" {
				if runErr != nil || result.ExitCode != 0 {
					p.logError(fmt.Sprintf(
						"job=%s command_category=oracle-rman rman_log_tail=%s",
						jobName,
						logTail,
					))
				} else {
					p.logInfo(fmt.Sprintf(
						"job=%s command_category=oracle-rman rman_log_tail=%s",
						jobName,
						logTail,
					))
				}
			}
		} else {
			p.logWarn(fmt.Sprintf(
				"job=%s command_category=oracle-rman rman_log_updated=false log_file=%s",
				jobName,
				absoluteLogFile,
			))
		}
	} else {
		p.logInfo(fmt.Sprintf(
			"job=%s command_category=oracle-rman rman_log_configured=false output_source=command_runner",
			jobName,
		))
	}

	if runErr != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return fmt.Errorf(
				"job=%q Oracle RMAN restore cancelled: %w",
				jobName,
				contextErr,
			)
		}

		return fmt.Errorf(
			"job=%q oracle-rman execution failed with exit code %d: %w",
			jobName,
			result.ExitCode,
			runErr,
		)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf(
			"job=%q oracle-rman failed with exit code %d",
			jobName,
			result.ExitCode,
		)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"job=%q Oracle RMAN restore context ended after execution: %w",
			jobName,
			err,
		)
	}

	if logFileConfigured {
		p.logSuccess(fmt.Sprintf(
			"job=%s command_category=oracle-rman status=success exit_code=%d rman_log_file=%s",
			jobName,
			result.ExitCode,
			absoluteLogFile,
		))
	} else {
		p.logSuccess(fmt.Sprintf(
			"job=%s command_category=oracle-rman status=success exit_code=%d rman_log_file=not_configured output_source=command_runner",
			jobName,
			result.ExitCode,
		))
	}

	return nil
}

func rmanValidateConnectionSpec(
	field string,
	value string,
) error {
	if err := rmanValidateValue(field, value); err != nil {
		return err
	}

	if strings.ContainsAny(value, " \t") {
		return fmt.Errorf(
			"%s must not contain whitespace",
			field,
		)
	}

	if rmanContainsInlinePassword(value) {
		return fmt.Errorf(
			"%s appears to contain an inline password; use OS authentication or Oracle Wallet",
			field,
		)
	}

	return nil
}

func rmanContainsInlinePassword(value string) bool {
	value = strings.TrimSpace(value)

	if value == "/" ||
		strings.HasPrefix(value, "/@") {
		return false
	}

	return strings.Contains(value, "/")
}

func rmanValidateValue(
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

func rmanAbsolutePath(
	value string,
	field string,
) (string, error) {
	if err := rmanValidateValue(field, value); err != nil {
		return "", err
	}

	absolutePath, err := filepath.Abs(
		strings.TrimSpace(value),
	)
	if err != nil {
		return "", fmt.Errorf(
			"resolve %s path %q: %w",
			field,
			value,
			err,
		)
	}

	return filepath.Clean(absolutePath), nil
}

func rmanRequireReadableRegularFile(
	path string,
	description string,
) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf(
				"%s does not exist: %q",
				description,
				path,
			)
		}

		return fmt.Errorf(
			"inspect %s %q: %w",
			description,
			path,
			err,
		)
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf(
			"%s is not a regular file: %q",
			description,
			path,
		)
	}

	if info.Size() <= 0 {
		return fmt.Errorf(
			"%s is empty: %q",
			description,
			path,
		)
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf(
			"open %s %q: %w",
			description,
			path,
			err,
		)
	}

	if closeErr := file.Close(); closeErr != nil {
		return fmt.Errorf(
			"close %s %q after readability check: %w",
			description,
			path,
			closeErr,
		)
	}

	return nil
}

func rmanRequireDirectory(
	path string,
	description string,
) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf(
				"%s does not exist: %q",
				description,
				path,
			)
		}

		return fmt.Errorf(
			"inspect %s %q: %w",
			description,
			path,
			err,
		)
	}

	if !info.IsDir() {
		return fmt.Errorf(
			"%s is not a directory: %q",
			description,
			path,
		)
	}

	return nil
}

func rmanValidateLogTarget(
	logFile string,
) error {
	info, err := os.Lstat(logFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return fmt.Errorf(
			"inspect RMAN log file %q: %w",
			logFile,
			err,
		)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf(
			"RMAN log file must not be a symbolic link: %q",
			logFile,
		)
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf(
			"RMAN log path is not a regular file: %q",
			logFile,
		)
	}

	return nil
}

func rmanSamePath(
	left string,
	right string,
) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)

	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}

	return left == right
}

func rmanValidOracleSID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	for index, character := range value {
		isLetter := (character >= 'A' && character <= 'Z') ||
			(character >= 'a' && character <= 'z')

		isDigit := character >= '0' && character <= '9'

		isAllowedSpecial := character == '_' ||
			character == '$' ||
			character == '#'

		if index == 0 && !isLetter {
			return false
		}

		if !isLetter &&
			!isDigit &&
			!isAllowedSpecial {
			return false
		}
	}

	return true
}

func rmanBuildEnvironment(
	oracleHome string,
	oracleSID string,
) []string {
	environment := append(
		[]string(nil),
		os.Environ()...,
	)

	environment = rmanSetEnvironmentValue(
		environment,
		"ORACLE_HOME",
		oracleHome,
	)

	environment = rmanSetEnvironmentValue(
		environment,
		"ORACLE_SID",
		oracleSID,
	)

	pathSeparator := string(os.PathListSeparator)
	oracleBin := filepath.Join(
		oracleHome,
		"bin",
	)

	currentPath := os.Getenv("PATH")
	combinedPath := oracleBin

	if strings.TrimSpace(currentPath) != "" {
		combinedPath += pathSeparator + currentPath
	}

	environment = rmanSetEnvironmentValue(
		environment,
		"PATH",
		combinedPath,
	)

	return environment
}

func rmanSetEnvironmentValue(
	environment []string,
	key string,
	value string,
) []string {
	result := make(
		[]string,
		0,
		len(environment)+1,
	)

	for _, item := range environment {
		separatorIndex := strings.IndexByte(
			item,
			'=',
		)

		if separatorIndex < 0 {
			result = append(
				result,
				item,
			)
			continue
		}

		existingKey := item[:separatorIndex]

		keysEqual := existingKey == key
		if runtime.GOOS == "windows" {
			keysEqual = strings.EqualFold(
				existingKey,
				key,
			)
		}

		if keysEqual {
			continue
		}

		result = append(
			result,
			item,
		)
	}

	result = append(
		result,
		key+"="+value,
	)

	return result
}

func captureRMANLogSnapshot(
	path string,
) (rmanLogSnapshot, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return rmanLogSnapshot{}, nil
		}

		return rmanLogSnapshot{}, err
	}

	if !info.Mode().IsRegular() {
		return rmanLogSnapshot{}, fmt.Errorf(
			"RMAN log path is not a regular file: %q",
			path,
		)
	}

	return rmanLogSnapshot{
		Exists:       true,
		Size:         info.Size(),
		ModifiedTime: info.ModTime(),
	}, nil
}

func rmanLogWasUpdated(
	before rmanLogSnapshot,
	after rmanLogSnapshot,
	startedAt time.Time,
) bool {
	if !after.Exists {
		return false
	}

	if !before.Exists {
		return true
	}

	if before.Size != after.Size {
		return true
	}

	if !before.ModifiedTime.Equal(after.ModifiedTime) {
		return true
	}

	// This fallback helps on filesystems with coarse timestamp resolution.
	return !after.ModifiedTime.Before(
		startedAt.Add(-2 * time.Second),
	)
}

func tailTextFile(
	path string,
	maxBytes int,
) string {
	if maxBytes <= 0 {
		return ""
	}

	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil ||
		!info.Mode().IsRegular() ||
		info.Size() <= 0 {
		return ""
	}

	readSize := int64(maxBytes)
	if info.Size() < readSize {
		readSize = info.Size()
	}

	offset := info.Size() - readSize

	if _, err := file.Seek(
		offset,
		io.SeekStart,
	); err != nil {
		return ""
	}

	data, err := io.ReadAll(
		io.LimitReader(
			file,
			readSize,
		),
	)
	if err != nil {
		return ""
	}

	text := strings.ToValidUTF8(
		string(data),
		"\uFFFD",
	)

	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	return logging.Sanitize(text)
}

func rmanOneOf(
	value string,
	allowed ...string,
) bool {
	value = strings.TrimSpace(value)

	for _, candidate := range allowed {
		if strings.EqualFold(
			value,
			strings.TrimSpace(candidate),
		) {
			return true
		}
	}

	return false
}

func (p OracleRmanProvider) logInfo(
	message string,
) {
	if p.Logger != nil {
		p.Logger.Info(message)
	}
}

func (p OracleRmanProvider) logWarn(
	message string,
) {
	if p.Logger != nil {
		p.Logger.Warn(message)
	}
}

func (p OracleRmanProvider) logError(
	message string,
) {
	if p.Logger != nil {
		p.Logger.Error(message)
	}
}

func (p OracleRmanProvider) logSuccess(
	message string,
) {
	if p.Logger != nil {
		p.Logger.Success(message)
	}
}
