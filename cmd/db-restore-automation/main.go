package main

import (
	"context"
	"flag"
	"fmt"
	"os"

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
	if len(args) == 0 {
		usage()
		return exitUsage
	}

	ctx := context.Background()

	switch args[0] {
	case "validate":
		fs := flag.NewFlagSet("validate", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		configPath := fs.String("config", "", "restore job YAML config")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || *configPath == "" {
			usage()
			return exitUsage
		}
		return app.RunValidate(ctx, *configPath)

	case "restore":
		fs := flag.NewFlagSet("restore", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		configPath := fs.String("config", "", "restore job YAML config")
		jobName := fs.String("job", "", "restore job name")
		dryRun := fs.Bool("dry-run", false, "log restore actions without executing provider commands")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || *configPath == "" {
			usage()
			return exitUsage
		}
		return app.RunRestore(ctx, app.RestoreOptions{
			ConfigPath: *configPath,
			JobName:    *jobName,
			DryRun:     *dryRun,
		})

	case "schedule":
		if len(args) < 2 {
			usage()
			return exitUsage
		}
		fs := flag.NewFlagSet("schedule "+args[1], flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		configPath := fs.String("config", "", "restore job YAML config")
		rootDir := fs.String("root-dir", "", "installed root directory")
		if err := fs.Parse(args[2:]); err != nil || fs.NArg() != 0 || *configPath == "" || *rootDir == "" {
			usage()
			return exitUsage
		}
		switch args[1] {
		case "linux":
			return app.RunScheduleLinux(ctx, *configPath, *rootDir, os.Stdout)
		case "windows":
			return app.RunScheduleWindows(ctx, *configPath, *rootDir, os.Stdout)
		default:
			usage()
			return exitUsage
		}

	default:
		usage()
		return exitUsage
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  db-restore-automation validate --config <config-file>")
	fmt.Fprintln(os.Stderr, "  db-restore-automation restore --config <config-file> [--job <job-name>] [--dry-run]")
	fmt.Fprintln(os.Stderr, "  db-restore-automation schedule windows --config <config-file> --root-dir <root-directory>")
	fmt.Fprintln(os.Stderr, "  db-restore-automation schedule linux --config <config-file> --root-dir <root-directory>")
}
