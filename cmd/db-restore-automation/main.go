package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"db-restore-automation/internal/app"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	return runWithContext(ctx, args)
}

func runWithContext(
	ctx context.Context,
	args []string,
) int {
	if ctx == nil {
		ctx = context.Background()
	}

	if err := ctx.Err(); err != nil {
		fmt.Fprintf(
			os.Stderr,
			"operation cancelled before command execution: %v\n",
			err,
		)
		return exitFailure
	}

	if len(args) == 0 {
		// With no arguments, launch the guided menu when attached to a
		// terminal. When stdin is redirected (scripts, pipes, cron) fall back
		// to printing usage so non-interactive callers are not left waiting on
		// a prompt that will never be answered.
		if stdinIsInteractive() {
			return app.RunInteractive(ctx, os.Stdin, os.Stdout)
		}

		usage()
		return exitUsage
	}

	command := strings.ToLower(
		strings.TrimSpace(args[0]),
	)

	switch command {
	case "help", "-h", "--help":
		usage()
		return exitOK

	case "interactive", "menu":
		return app.RunInteractive(
			ctx,
			os.Stdin,
			os.Stdout,
		)

	case "validate":
		return runValidateCommand(
			ctx,
			args[1:],
		)

	case "restore":
		return runRestoreCommand(
			ctx,
			args[1:],
		)

	case "schedule":
		return runScheduleCommand(
			ctx,
			args[1:],
		)

	default:
		fmt.Fprintf(
			os.Stderr,
			"unknown command %q\n\n",
			args[0],
		)

		usage()

		return exitUsage
	}
}

func runValidateCommand(
	ctx context.Context,
	args []string,
) int {
	flagSet := newFlagSet(
		"validate",
		"db-restore-automation validate --config <config-file>",
	)

	configPath := flagSet.String(
		"config",
		"",
		"restore job YAML configuration file",
	)

	if exitCode, stop := parseFlags(
		flagSet,
		args,
	); stop {
		return exitCode
	}

	if flagSet.NArg() != 0 {
		printUnexpectedArguments(
			flagSet,
			flagSet.Args(),
		)

		return exitUsage
	}

	normalizedConfigPath := strings.TrimSpace(
		*configPath,
	)

	if normalizedConfigPath == "" {
		printMissingFlag(
			flagSet,
			"config",
		)

		return exitUsage
	}

	if err := ctx.Err(); err != nil {
		return cancelledExitCode(
			"validate",
			err,
		)
	}

	return app.RunValidate(
		ctx,
		normalizedConfigPath,
	)
}

func runRestoreCommand(
	ctx context.Context,
	args []string,
) int {
	flagSet := newFlagSet(
		"restore",
		"db-restore-automation restore --config <config-file> [--job <job-name>] [--dry-run]",
	)

	configPath := flagSet.String(
		"config",
		"",
		"restore job YAML configuration file",
	)

	jobName := flagSet.String(
		"job",
		"",
		"restore only the specified job",
	)

	dryRun := flagSet.Bool(
		"dry-run",
		false,
		"validate and log restore actions without executing provider commands",
	)

	timeout := flagSet.Duration(
		"timeout",
		0,
		"default per-job wall-clock timeout when a job has no timeout in its config (e.g. 2h, 90m, 1h30m); per-job config takes precedence",
	)

	if exitCode, stop := parseFlags(
		flagSet,
		args,
	); stop {
		return exitCode
	}

	if flagSet.NArg() != 0 {
		printUnexpectedArguments(
			flagSet,
			flagSet.Args(),
		)

		return exitUsage
	}

	normalizedConfigPath := strings.TrimSpace(
		*configPath,
	)

	if normalizedConfigPath == "" {
		printMissingFlag(
			flagSet,
			"config",
		)

		return exitUsage
	}

	normalizedJobName := strings.TrimSpace(
		*jobName,
	)

	if err := ctx.Err(); err != nil {
		return cancelledExitCode(
			"restore",
			err,
		)
	}

	return app.RunRestore(
		ctx,
		app.RestoreOptions{
			ConfigPath: normalizedConfigPath,
			JobName:    normalizedJobName,
			DryRun:     *dryRun,
			Timeout:    *timeout,
		},
	)
}

func runScheduleCommand(
	ctx context.Context,
	args []string,
) int {
	if len(args) == 0 {
		usageSchedule()
		return exitUsage
	}

	platform := strings.ToLower(
		strings.TrimSpace(args[0]),
	)

	switch platform {
	case "help", "-h", "--help":
		usageSchedule()
		return exitOK

	case "linux", "windows":
	default:
		fmt.Fprintf(
			os.Stderr,
			"unsupported schedule platform %q; expected linux or windows\n\n",
			args[0],
		)

		usageSchedule()

		return exitUsage
	}

	usageLine := fmt.Sprintf(
		"db-restore-automation schedule %s --config <config-file> --root-dir <root-directory>",
		platform,
	)

	flagSet := newFlagSet(
		"schedule "+platform,
		usageLine,
	)

	configPath := flagSet.String(
		"config",
		"",
		"restore job YAML configuration file",
	)

	rootDir := flagSet.String(
		"root-dir",
		"",
		"installed application root directory",
	)

	if exitCode, stop := parseFlags(
		flagSet,
		args[1:],
	); stop {
		return exitCode
	}

	if flagSet.NArg() != 0 {
		printUnexpectedArguments(
			flagSet,
			flagSet.Args(),
		)

		return exitUsage
	}

	normalizedConfigPath := strings.TrimSpace(
		*configPath,
	)

	if normalizedConfigPath == "" {
		printMissingFlag(
			flagSet,
			"config",
		)

		return exitUsage
	}

	normalizedRootDir := strings.TrimSpace(
		*rootDir,
	)

	if normalizedRootDir == "" {
		printMissingFlag(
			flagSet,
			"root-dir",
		)

		return exitUsage
	}

	if err := ctx.Err(); err != nil {
		return cancelledExitCode(
			"schedule "+platform,
			err,
		)
	}

	switch platform {
	case "linux":
		return app.RunScheduleLinux(
			ctx,
			normalizedConfigPath,
			normalizedRootDir,
			os.Stdout,
		)

	case "windows":
		return app.RunScheduleWindows(
			ctx,
			normalizedConfigPath,
			normalizedRootDir,
			os.Stdout,
		)

	default:
		// The platform is validated before flag parsing. This branch is kept
		// as a defensive fallback in case the supported values are changed.
		fmt.Fprintf(
			os.Stderr,
			"unsupported schedule platform %q\n",
			platform,
		)

		return exitUsage
	}
}

func newFlagSet(
	name string,
	usageLine string,
) *flag.FlagSet {
	flagSet := flag.NewFlagSet(
		name,
		flag.ContinueOnError,
	)

	flagSet.SetOutput(os.Stderr)

	flagSet.Usage = func() {
		fmt.Fprintln(
			flagSet.Output(),
			"Usage:",
		)

		fmt.Fprintf(
			flagSet.Output(),
			"  %s\n\n",
			usageLine,
		)

		fmt.Fprintln(
			flagSet.Output(),
			"Options:",
		)

		flagSet.PrintDefaults()
	}

	return flagSet
}

func parseFlags(
	flagSet *flag.FlagSet,
	args []string,
) (exitCode int, stop bool) {
	err := flagSet.Parse(args)
	if err == nil {
		return exitOK, false
	}

	if errors.Is(err, flag.ErrHelp) {
		return exitOK, true
	}

	fmt.Fprintln(
		flagSet.Output(),
	)

	flagSet.Usage()

	return exitUsage, true
}

func printMissingFlag(
	flagSet *flag.FlagSet,
	flagName string,
) {
	fmt.Fprintf(
		flagSet.Output(),
		"missing required flag: --%s\n\n",
		flagName,
	)

	flagSet.Usage()
}

func printUnexpectedArguments(
	flagSet *flag.FlagSet,
	args []string,
) {
	fmt.Fprintf(
		flagSet.Output(),
		"unexpected positional arguments: %s\n\n",
		strings.Join(args, " "),
	)

	flagSet.Usage()
}

func stdinIsInteractive() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}

func cancelledExitCode(
	command string,
	err error,
) int {
	fmt.Fprintf(
		os.Stderr,
		"%s command cancelled: %v\n",
		command,
		err,
	)

	return exitFailure
}

func usage() {
	fmt.Fprintln(
		os.Stderr,
		"Usage:",
	)

	fmt.Fprintln(
		os.Stderr,
		"  db-restore-automation interactive",
	)

	fmt.Fprintln(
		os.Stderr,
		"  db-restore-automation validate --config <config-file>",
	)

	fmt.Fprintln(
		os.Stderr,
		"  db-restore-automation restore --config <config-file> [--job <job-name>] [--dry-run]",
	)

	fmt.Fprintln(
		os.Stderr,
		"  db-restore-automation schedule windows --config <config-file> --root-dir <root-directory>",
	)

	fmt.Fprintln(
		os.Stderr,
		"  db-restore-automation schedule linux --config <config-file> --root-dir <root-directory>",
	)

	fmt.Fprintln(
		os.Stderr,
	)

	fmt.Fprintln(
		os.Stderr,
		"Commands:",
	)

	fmt.Fprintln(
		os.Stderr,
		"  interactive  Launch the guided menu to pick an action and job by title.",
	)

	fmt.Fprintln(
		os.Stderr,
		"  validate   Validate configuration and enabled-job safety rules.",
	)

	fmt.Fprintln(
		os.Stderr,
		"  restore    Run one enabled restore job or all enabled restore jobs.",
	)

	fmt.Fprintln(
		os.Stderr,
		"  schedule   Generate Linux cron entries or a Windows scheduler script.",
	)

	fmt.Fprintln(
		os.Stderr,
	)

	fmt.Fprintln(
		os.Stderr,
		"Run a command with --help to see its options.",
	)
}

func usageSchedule() {
	fmt.Fprintln(
		os.Stderr,
		"Usage:",
	)

	fmt.Fprintln(
		os.Stderr,
		"  db-restore-automation schedule windows --config <config-file> --root-dir <root-directory>",
	)

	fmt.Fprintln(
		os.Stderr,
		"  db-restore-automation schedule linux --config <config-file> --root-dir <root-directory>",
	)
}
