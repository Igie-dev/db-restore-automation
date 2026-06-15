package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultLogDirectory = "logs"
	defaultLogFileName  = "restore.log"

	logDirectoryPermissions os.FileMode = 0755
	logFilePermissions      os.FileMode = 0644
)

type Logger struct {
	filePath string
	mu       sync.Mutex
}

func New(rootDir string) (*Logger, error) {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return nil, fmt.Errorf(
			"logger_initialization_failed: root directory is required",
		)
	}

	if err := validateLogPathValue(rootDir, "root directory"); err != nil {
		return nil, err
	}

	absoluteRootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf(
			"logger_initialization_failed: resolve root directory %q: %w",
			rootDir,
			err,
		)
	}

	absoluteRootDir = filepath.Clean(absoluteRootDir)

	logFile := strings.TrimSpace(os.Getenv("LOG_FILE"))
	if logFile == "" {
		logFile = filepath.Join(
			absoluteRootDir,
			defaultLogDirectory,
			defaultLogFileName,
		)
	} else {
		if err := validateLogPathValue(logFile, "LOG_FILE"); err != nil {
			return nil, err
		}

		// Resolve relative LOG_FILE values against the application root,
		// rather than the caller's current working directory.
		if !filepath.IsAbs(logFile) {
			logFile = filepath.Join(
				absoluteRootDir,
				logFile,
			)
		}
	}

	absoluteLogFile, err := filepath.Abs(logFile)
	if err != nil {
		return nil, fmt.Errorf(
			"logger_initialization_failed: resolve log file path %q: %w",
			logFile,
			err,
		)
	}

	absoluteLogFile = filepath.Clean(absoluteLogFile)
	logDirectory := filepath.Dir(absoluteLogFile)

	if err := os.MkdirAll(
		logDirectory,
		logDirectoryPermissions,
	); err != nil {
		return nil, fmt.Errorf(
			"logger_initialization_failed: create log directory %q: %w",
			logDirectory,
			err,
		)
	}

	directoryInfo, err := os.Stat(logDirectory)
	if err != nil {
		return nil, fmt.Errorf(
			"logger_initialization_failed: inspect log directory %q: %w",
			logDirectory,
			err,
		)
	}

	if !directoryInfo.IsDir() {
		return nil, fmt.Errorf(
			"logger_initialization_failed: log directory path is not a directory: %q",
			logDirectory,
		)
	}

	if err := validateExistingLogFile(absoluteLogFile); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(
		absoluteLogFile,
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		logFilePermissions,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"logger_initialization_failed: open log file %q: %w",
			absoluteLogFile,
			err,
		)
	}

	fileInfo, statErr := file.Stat()
	closeErr := file.Close()

	if statErr != nil {
		return nil, fmt.Errorf(
			"logger_initialization_failed: inspect opened log file %q: %w",
			absoluteLogFile,
			statErr,
		)
	}

	if !fileInfo.Mode().IsRegular() {
		return nil, fmt.Errorf(
			"logger_initialization_failed: log file is not a regular file: %q",
			absoluteLogFile,
		)
	}

	if closeErr != nil {
		return nil, fmt.Errorf(
			"logger_initialization_failed: close log file %q: %w",
			absoluteLogFile,
			closeErr,
		)
	}

	return &Logger{
		filePath: absoluteLogFile,
	}, nil
}

func (l *Logger) FilePath() string {
	if l == nil {
		return ""
	}

	return l.filePath
}

func (l *Logger) Info(message string) {
	l.write("INFO", message)
}

func (l *Logger) Warn(message string) {
	l.write("WARN", message)
}

func (l *Logger) Error(message string) {
	l.write("ERROR", message)
}

func (l *Logger) Success(message string) {
	l.write("SUCCESS", message)
}

func (l *Logger) write(level string, message string) {
	level = normalizeLogLevel(level)

	line := fmt.Sprintf(
		"%s [%s] %s",
		time.Now().UTC().Format(time.RFC3339Nano),
		level,
		Sanitize(message),
	)

	if l == nil || strings.TrimSpace(l.filePath) == "" {
		_, _ = fmt.Fprintln(
			os.Stderr,
			line,
		)
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := appendLogLine(l.filePath, line); err != nil {
		_, _ = fmt.Fprintf(
			os.Stderr,
			"%s [ERROR] logger_write_failed file=%q error=%s\n",
			time.Now().UTC().Format(time.RFC3339Nano),
			l.filePath,
			Sanitize(err.Error()),
		)
	}

	console := os.Stdout
	if level == "ERROR" {
		console = os.Stderr
	}

	if _, err := fmt.Fprintln(console, line); err != nil &&
		console != os.Stderr {
		_, _ = fmt.Fprintf(
			os.Stderr,
			"%s [ERROR] logger_console_write_failed error=%s\n",
			time.Now().UTC().Format(time.RFC3339Nano),
			Sanitize(err.Error()),
		)
	}
}

func appendLogLine(filePath string, line string) error {
	file, err := os.OpenFile(
		filePath,
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		logFilePermissions,
	)
	if err != nil {
		return fmt.Errorf(
			"open log file: %w",
			err,
		)
	}

	fileInfo, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()

		return fmt.Errorf(
			"inspect log file: %w",
			statErr,
		)
	}

	if !fileInfo.Mode().IsRegular() {
		_ = file.Close()

		return fmt.Errorf(
			"log target is no longer a regular file",
		)
	}

	_, writeErr := fmt.Fprintln(file, line)
	closeErr := file.Close()

	if writeErr != nil {
		if closeErr != nil {
			return fmt.Errorf(
				"write log file: %v; close log file: %w",
				writeErr,
				closeErr,
			)
		}

		return fmt.Errorf(
			"write log file: %w",
			writeErr,
		)
	}

	if closeErr != nil {
		return fmt.Errorf(
			"close log file: %w",
			closeErr,
		)
	}

	return nil
}

func validateExistingLogFile(filePath string) error {
	info, err := os.Lstat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return fmt.Errorf(
			"logger_initialization_failed: inspect log file %q: %w",
			filePath,
			err,
		)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf(
			"logger_initialization_failed: log file must not be a symbolic link: %q",
			filePath,
		)
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf(
			"logger_initialization_failed: log file is not a regular file: %q",
			filePath,
		)
	}

	return nil
}

func validateLogPathValue(value string, fieldName string) error {
	if strings.ContainsRune(value, '\x00') {
		return fmt.Errorf(
			"logger_initialization_failed: %s contains a null character",
			fieldName,
		)
	}

	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf(
			"logger_initialization_failed: %s must be a single-line path",
			fieldName,
		)
	}

	return nil
}

func normalizeLogLevel(level string) string {
	level = strings.ToUpper(
		strings.TrimSpace(level),
	)

	switch level {
	case "INFO", "WARN", "ERROR", "SUCCESS":
		return level

	case "":
		return "INFO"

	default:
		return Sanitize(level)
	}
}

func Sanitize(message string) string {
	if message == "" {
		return ""
	}

	var builder strings.Builder
	builder.Grow(len(message))

	for _, character := range message {
		switch character {
		case '\r':
			builder.WriteString(`\r`)

		case '\n':
			builder.WriteString(`\n`)

		case '\t':
			builder.WriteString(`\t`)

		case '\b':
			builder.WriteString(`\b`)

		case '\f':
			builder.WriteString(`\f`)

		case '\v':
			builder.WriteString(`\v`)

		case '\u2028':
			builder.WriteString(`\u2028`)

		case '\u2029':
			builder.WriteString(`\u2029`)

		default:
			if character < 0x20 ||
				(character >= 0x7F && character <= 0x9F) {
				_, _ = fmt.Fprintf(
					&builder,
					`\u%04X`,
					character,
				)
				continue
			}

			builder.WriteRune(character)
		}
	}

	return builder.String()
}

func KV(key string, value any) string {
	key = sanitizeKey(key)

	return fmt.Sprintf(
		"%s=%s",
		key,
		Sanitize(fmt.Sprint(value)),
	)
}

func sanitizeKey(key string) string {
	key = strings.TrimSpace(key)

	var builder strings.Builder
	builder.Grow(len(key))

	previousUnderscore := false

	for _, character := range key {
		isAllowed := (character >= 'A' && character <= 'Z') ||
			(character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '_' ||
			character == '-' ||
			character == '.'

		if isAllowed {
			builder.WriteRune(character)
			previousUnderscore = false
			continue
		}

		if !previousUnderscore {
			builder.WriteByte('_')
			previousUnderscore = true
		}
	}

	result := strings.Trim(
		builder.String(),
		"_.-",
	)

	if result == "" {
		return "value"
	}

	return result
}

