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
	"strings"
	"syscall"
	"time"

	"db-restore-automation/internal/logging"
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

func (r Runner) Run(ctx context.Context, cmdSpec Command) (Result, error) {
	if strings.TrimSpace(cmdSpec.Executable) == "" {
		return Result{ExitCode: -1}, errors.New("empty executable path")
	}

	stdout, err := os.CreateTemp("", safeName(cmdSpec.Category)+"-*-stdout.log")
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	defer stdout.Close()

	stderr, err := os.CreateTemp("", safeName(cmdSpec.Category)+"-*-stderr.log")
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	defer stderr.Close()

	if r.Logger != nil {
		r.Logger.Info(fmt.Sprintf("command_category=%s status=start executable=%s stdout_file=%s stderr_file=%s", cmdSpec.Category, filepath.Base(cmdSpec.Executable), stdout.Name(), stderr.Name()))
	}

	cmd := exec.CommandContext(ctx, cmdSpec.Executable, cmdSpec.Args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if cmdSpec.Stdin != nil {
		cmd.Stdin = cmdSpec.Stdin
	}
	if len(cmdSpec.Env) > 0 {
		cmd.Env = append(os.Environ(), cmdSpec.Env...)
	}

	started := time.Now()
	err = cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = exitCodeFromError(err)
	}
	_ = stdout.Sync()
	_ = stderr.Sync()

	result := Result{
		ExitCode:   exitCode,
		StdoutFile: stdout.Name(),
		StderrFile: stderr.Name(),
		StdoutTail: tailFile(stdout.Name(), 1200),
		StderrTail: tailFile(stderr.Name(), 1200),
	}

	if r.Logger != nil {
		levelMessage := fmt.Sprintf("command_category=%s status=%s exit_code=%d stdout_file=%s stderr_file=%s duration=%s", cmdSpec.Category, status(exitCode), exitCode, result.StdoutFile, result.StderrFile, time.Since(started).Round(time.Millisecond))
		if exitCode == 0 {
			r.Logger.Info(levelMessage)
		} else {
			r.Logger.Error(levelMessage)
			if result.StderrTail != "" {
				r.Logger.Error(fmt.Sprintf("command_category=%s stderr_tail=%s", cmdSpec.Category, result.StderrTail))
			}
			if result.StdoutTail != "" {
				r.Logger.Error(fmt.Sprintf("command_category=%s stdout_tail=%s", cmdSpec.Category, result.StdoutTail))
			}
		}
	}

	return result, err
}

func exitCodeFromError(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return status.ExitStatus()
		}
	}
	return 1
}

func status(exitCode int) string {
	if exitCode == 0 {
		return "success"
	}
	return "failed"
}

func safeName(value string) string {
	if value == "" {
		return "db-restore"
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func tailFile(path string, maxBytes int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	data = bytes.TrimSpace(data)
	if len(data) > maxBytes {
		data = data[len(data)-maxBytes:]
	}
	return logging.Sanitize(string(data))
}

func ExecutableAvailable(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	if strings.ContainsAny(path, `/\`) || filepath.IsAbs(path) {
		info, err := os.Stat(path)
		return err == nil && !info.IsDir()
	}
	_, err := exec.LookPath(path)
	return err == nil
}
