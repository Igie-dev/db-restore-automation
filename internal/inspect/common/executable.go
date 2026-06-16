package common

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func ResolveExecutable(configured, fallback string) (string, error) {
	candidate := strings.TrimSpace(os.ExpandEnv(configured))
	if candidate == "" {
		candidate = fallback
	}
	if candidate == "" {
		return "", errors.New("executable is not configured")
	}

	if strings.ContainsAny(candidate, `/\`) || filepath.IsAbs(candidate) {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			return "", fmt.Errorf("path is a directory")
		}
		return absolute, nil
	}

	path, err := exec.LookPath(candidate)
	if err != nil {
		return "", err
	}
	return path, nil
}

func InspectExecutable(ctx context.Context, report *JobReport, name, configured, fallback string, versionArgs ...string) string {
	return inspectExecutableWithRequirement(ctx, report, name, configured, fallback, true, versionArgs...)
}

func InspectOptionalExecutable(ctx context.Context, report *JobReport, name, configured, fallback string, versionArgs ...string) string {
	return inspectExecutableWithRequirement(ctx, report, name, configured, fallback, false, versionArgs...)
}

func inspectExecutableWithRequirement(
	ctx context.Context,
	report *JobReport,
	name string,
	configured string,
	fallback string,
	required bool,
	versionArgs ...string,
) string {
	path, err := ResolveExecutable(configured, fallback)
	if err != nil {
		if required {
			report.Fail(name, fmt.Sprintf("executable was not found: %v", err), configured)
		} else {
			report.Warn(name, fmt.Sprintf("optional executable was not found: %v", err), configured)
		}
		return ""
	}

	report.Pass(name, "executable found", path)
	if len(versionArgs) == 0 {
		return path
	}

	output, err := RunReadOnlyCommand(ctx, path, versionArgs, nil, "")
	if err != nil {
		report.Warn(name+" version", fmt.Sprintf("unable to read version: %v", err), CompactOutput(output, 500))
		return path
	}
	report.Pass(name+" version", "version command succeeded", CompactOutput(output, 500))
	return path
}

func RunReadOnlyCommand(ctx context.Context, executable string, args []string, env []string, stdin string) (string, error) {
	command := exec.CommandContext(ctx, executable, args...)
	if len(env) > 0 {
		command.Env = append(os.Environ(), env...)
	}
	if stdin != "" {
		command.Stdin = strings.NewReader(stdin)
	}
	output, err := command.CombinedOutput()
	return Redact(string(output)), err
}
