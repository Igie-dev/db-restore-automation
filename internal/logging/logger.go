package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Logger struct {
	filePath string
	mu       sync.Mutex
}

func New(rootDir string) (*Logger, error) {
	logFile := os.Getenv("LOG_FILE")
	if strings.TrimSpace(logFile) == "" {
		logFile = filepath.Join(rootDir, "logs", "restore.log")
	}
	if err := os.MkdirAll(filepath.Dir(logFile), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	return &Logger{filePath: logFile}, nil
}

func (l *Logger) FilePath() string {
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

func (l *Logger) write(level, message string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	line := fmt.Sprintf("%s [%s] %s", time.Now().Format("2006-01-02T15:04:05-07:00"), level, Sanitize(message))
	f, err := os.OpenFile(l.filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		_, _ = fmt.Fprintln(f, line)
		_ = f.Close()
	}
	fmt.Println(line)
}

func Sanitize(message string) string {
	message = strings.ReplaceAll(message, "\r", `\r`)
	message = strings.ReplaceAll(message, "\n", `\n`)
	return message
}

func KV(key string, value any) string {
	return fmt.Sprintf("%s=%v", key, value)
}
