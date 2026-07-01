package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"db-restore-automation/internal/logging"
)

const (
	commandOutputTailBytes = 1200
	commandTempNameLimit   = 80
)

type Runner struct {
	Logger *logging.Logger
}

type Command struct {
	Category   string
	Executable string
	Args       []string
	Env        []string
	Stdin      io.Reader
}

type Result struct {
	ExitCode   int
	StdoutFile string
	StderrFile string
	StdoutTail string
	StderrTail string
}

func (r Runner) Run(
	ctx context.Context,
	cmdSpec Command,
) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if err := ctx.Err(); err != nil {
		return Result{
				ExitCode: -1,
			},
			fmt.Errorf(
				"command cancelled before execution: %w",
				err,
			)
	}

	category := strings.TrimSpace(cmdSpec.Category)
	if category == "" {
		category = "db-restore"
	}

	executable := strings.TrimSpace(cmdSpec.Executable)
	if err := validateExecutableValue(executable); err != nil {
		return Result{
			ExitCode: -1,
		}, err
	}

	args, err := validateAndCloneArguments(
		cmdSpec.Args,
	)
	if err != nil {
		return Result{
			ExitCode: -1,
		}, fmt.Errorf(
			"command_category=%s %w",
			category,
			err,
		)
	}

	environment, err := prepareCommandEnvironment(
		cmdSpec.Env,
	)
	if err != nil {
		return Result{
			ExitCode: -1,
		}, fmt.Errorf(
			"command_category=%s invalid environment: %w",
			category,
			err,
		)
	}

	stdout, err := createCommandOutputFile(
		category,
		"stdout",
	)
	if err != nil {
		return Result{
			ExitCode: -1,
		}, fmt.Errorf(
			"command_category=%s create stdout capture file: %w",
			category,
			err,
		)
	}

	stderr, err := createCommandOutputFile(
		category,
		"stderr",
	)
	if err != nil {
		stdoutPath := stdout.Name()

		_ = stdout.Close()
		_ = os.Remove(stdoutPath)

		return Result{
			ExitCode: -1,
		}, fmt.Errorf(
			"command_category=%s create stderr capture file: %w",
			category,
			err,
		)
	}

	stdoutPath := stdout.Name()
	stderrPath := stderr.Name()

	result := Result{
		ExitCode:   -1,
		StdoutFile: stdoutPath,
		StderrFile: stderrPath,
	}

	r.logInfo(fmt.Sprintf(
		"command_category=%s status=start executable=%s stdout_file=%s stderr_file=%s",
		category,
		filepath.Base(executable),
		stdoutPath,
		stderrPath,
	))

	command := exec.CommandContext(
		ctx,
		executable,
		args...,
	)

	command.Stdout = stdout
	command.Stderr = stderr
	command.Stdin = cmdSpec.Stdin

	if environment != nil {
		command.Env = environment
	}

	startedAt := time.Now()

	var runErr error

	if startErr := command.Start(); startErr != nil {
		runErr = startErr
	} else {
		done := make(chan struct{})

		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()

			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					r.logInfo(fmt.Sprintf(
						"command_category=%s status=running elapsed=%s stdout_file=%s stderr_file=%s",
						category,
						time.Since(startedAt).Round(time.Second),
						stdoutPath,
						stderrPath,
					))
				}
			}
		}()

		runErr = command.Wait()
		close(done)
	}

	duration := time.Since(startedAt).Round(
		time.Millisecond,
	)

	result.ExitCode = commandExitCode(
		command,
		runErr,
	)

	stdoutFinalizeErr := finalizeCommandOutputFile(
		stdout,
		"stdout",
	)

	stderrFinalizeErr := finalizeCommandOutputFile(
		stderr,
		"stderr",
	)

	result.StdoutTail = tailFile(
		stdoutPath,
		commandOutputTailBytes,
	)

	result.StderrTail = tailFile(
		stderrPath,
		commandOutputTailBytes,
	)

	finalizeErr := errors.Join(
		stdoutFinalizeErr,
		stderrFinalizeErr,
	)

	commandErr := runErr

	if ctxErr := ctx.Err(); ctxErr != nil {
		commandErr = errors.Join(
			commandErr,
			fmt.Errorf(
				"command context ended: %w",
				ctxErr,
			),
		)
	}

	combinedErr := errors.Join(
		commandErr,
		finalizeErr,
	)

	succeeded := combinedErr == nil &&
		result.ExitCode == 0

	statusValue := "failed"
	if succeeded {
		statusValue = "success"
	}

	levelMessage := fmt.Sprintf(
		"command_category=%s status=%s exit_code=%d stdout_file=%s stderr_file=%s duration=%s",
		category,
		statusValue,
		result.ExitCode,
		result.StdoutFile,
		result.StderrFile,
		duration,
	)

	if succeeded {
		r.logInfo(levelMessage)
		return result, nil
	}

	r.logError(levelMessage)

	if combinedErr != nil {
		r.logError(fmt.Sprintf(
			"command_category=%s command_error=%s",
			category,
			combinedErr.Error(),
		))
	}

	if result.StderrTail != "" {
		r.logError(fmt.Sprintf(
			"command_category=%s stderr_tail=%s",
			category,
			result.StderrTail,
		))
	}

	if result.StdoutTail != "" {
		r.logError(fmt.Sprintf(
			"command_category=%s stdout_tail=%s",
			category,
			result.StdoutTail,
		))
	}

	if combinedErr != nil {
		return result, fmt.Errorf(
			"command_category=%s execution failed: %w",
			category,
			combinedErr,
		)
	}

	return result, fmt.Errorf(
		"command_category=%s failed with exit code %d",
		category,
		result.ExitCode,
	)
}

func validateExecutableValue(
	executable string,
) error {
	if executable == "" {
		return errors.New(
			"empty executable path",
		)
	}

	if strings.ContainsRune(executable, '\x00') {
		return errors.New(
			"executable path contains a null character",
		)
	}

	if strings.ContainsAny(executable, "\r\n") {
		return errors.New(
			"executable path must be a single-line value",
		)
	}

	return nil
}

func validateAndCloneArguments(
	args []string,
) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}

	cloned := make(
		[]string,
		len(args),
	)

	for index, argument := range args {
		if strings.ContainsRune(argument, '\x00') {
			return nil, fmt.Errorf(
				"argument[%d] contains a null character",
				index,
			)
		}

		cloned[index] = argument
	}

	return cloned, nil
}

func prepareCommandEnvironment(
	overrides []string,
) ([]string, error) {
	if len(overrides) == 0 {
		// A nil Cmd.Env makes os/exec inherit the current process
		// environment directly.
		return nil, nil
	}

	return mergeEnvironment(
		os.Environ(),
		overrides,
	)
}

func mergeEnvironment(
	base []string,
	overrides []string,
) ([]string, error) {
	result := make(
		[]string,
		0,
		len(base)+len(overrides),
	)

	indexByKey := make(map[string]int)

	add := func(
		entry string,
		source string,
	) error {
		key, err := environmentEntryKey(entry)
		if err != nil {
			return fmt.Errorf(
				"%s environment entry %q: %w",
				source,
				entry,
				err,
			)
		}

		normalizedKey := normalizeEnvironmentKey(key)

		if existingIndex, exists := indexByKey[normalizedKey]; exists {
			result[existingIndex] = entry
			return nil
		}

		indexByKey[normalizedKey] = len(result)
		result = append(
			result,
			entry,
		)

		return nil
	}

	for _, entry := range base {
		if err := add(entry, "base"); err != nil {
			return nil, err
		}
	}

	for _, entry := range overrides {
		if strings.ContainsRune(entry, '\x00') {
			return nil, errors.New(
				"environment entry contains a null character",
			)
		}

		if strings.ContainsAny(entry, "\r\n") {
			return nil, errors.New(
				"environment entry must be a single-line value",
			)
		}

		if err := add(entry, "override"); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func environmentEntryKey(
	entry string,
) (string, error) {
	if entry == "" {
		return "", errors.New(
			"environment entry is empty",
		)
	}

	separatorIndex := strings.IndexByte(
		entry,
		'=',
	)

	// Windows can contain special drive-current-directory variables such
	// as "=C:=C:\path". Their key ends at the second equals sign.
	if separatorIndex == 0 &&
		runtime.GOOS == "windows" {
		nextSeparator := strings.IndexByte(
			entry[1:],
			'=',
		)

		if nextSeparator < 0 {
			return "", errors.New(
				"environment entry does not contain a value separator",
			)
		}

		separatorIndex = nextSeparator + 1
	}

	if separatorIndex <= 0 {
		return "", errors.New(
			"environment entry must use KEY=value format",
		)
	}

	key := entry[:separatorIndex]

	if strings.ContainsRune(key, '\x00') {
		return "", errors.New(
			"environment variable name contains a null character",
		)
	}

	return key, nil
}

func normalizeEnvironmentKey(
	key string,
) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}

	return key
}

func createCommandOutputFile(
	category string,
	stream string,
) (*os.File, error) {
	tempPattern := fmt.Sprintf(
		"%s-*-%s.log",
		safeName(category),
		safeName(stream),
	)

	file, err := os.CreateTemp(
		"",
		tempPattern,
	)
	if err != nil {
		return nil, err
	}

	return file, nil
}

func finalizeCommandOutputFile(
	file *os.File,
	stream string,
) error {
	if file == nil {
		return fmt.Errorf(
			"%s capture file is nil",
			stream,
		)
	}

	syncErr := file.Sync()
	closeErr := file.Close()

	switch {
	case syncErr != nil && closeErr != nil:
		return fmt.Errorf(
			"finalize %s capture file: sync: %v; close: %w",
			stream,
			syncErr,
			closeErr,
		)

	case syncErr != nil:
		return fmt.Errorf(
			"sync %s capture file: %w",
			stream,
			syncErr,
		)

	case closeErr != nil:
		return fmt.Errorf(
			"close %s capture file: %w",
			stream,
			closeErr,
		)

	default:
		return nil
	}
}

func commandExitCode(
	command *exec.Cmd,
	runErr error,
) int {
	if runErr == nil {
		return 0
	}

	if command != nil &&
		command.ProcessState != nil {
		exitCode := command.ProcessState.ExitCode()
		if exitCode >= 0 {
			return exitCode
		}
	}

	var exitError *exec.ExitError
	if errors.As(runErr, &exitError) {
		exitCode := exitError.ExitCode()
		if exitCode >= 0 {
			return exitCode
		}
	}

	// -1 means the command did not provide a normal process exit status,
	// such as when it could not be started or was forcibly terminated.
	return -1
}

func safeName(
	value string,
) string {
	value = strings.TrimSpace(value)

	if value == "" {
		return "db-restore"
	}

	var builder strings.Builder

	for _, character := range value {
		switch {
		case character >= 'A' && character <= 'Z':
			builder.WriteRune(character)

		case character >= 'a' && character <= 'z':
			builder.WriteRune(character)

		case character >= '0' && character <= '9':
			builder.WriteRune(character)

		case character == '_',
			character == '-',
			character == '.':
			builder.WriteRune(character)

		default:
			builder.WriteByte('_')
		}
	}

	result := strings.Trim(
		builder.String(),
		"_.-",
	)

	if result == "" {
		result = "db-restore"
	}

	if len(result) > commandTempNameLimit {
		result = result[:commandTempNameLimit]
		result = strings.TrimRight(
			result,
			"_.-",
		)

		if result == "" {
			result = "db-restore"
		}
	}

	return result
}

func tailFile(
	path string,
	maxBytes int,
) string {
	path = strings.TrimSpace(path)

	if path == "" || maxBytes <= 0 {
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

	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return ""
	}

	text := strings.ToValidUTF8(
		string(data),
		"\uFFFD",
	)

	return logging.Sanitize(text)
}

func ExecutableAvailable(
	path string,
) bool {
	path = strings.TrimSpace(path)

	if path == "" ||
		strings.ContainsRune(path, '\x00') ||
		strings.ContainsAny(path, "\r\n") {
		return false
	}

	resolvedPath, err := exec.LookPath(path)
	if err != nil {
		return false
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return false
	}

	return info.Mode().IsRegular()
}

func (r Runner) logInfo(
	message string,
) {
	if r.Logger != nil {
		r.Logger.Info(message)
	}
}

func (r Runner) logError(
	message string,
) {
	if r.Logger != nil {
		r.Logger.Error(message)
	}
}

