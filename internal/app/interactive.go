package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"db-restore-automation/internal/config"
)

const (
	exitCodeOK      = 0
	exitCodeFailure = 1
)

// RunInteractive presents a guided, menu-driven interface so an operator can
// pick an action and a job by title instead of remembering command-line flags.
// It collects inputs and then delegates to the same RunValidate, RunRestore,
// and RunSchedule* entry points used by the non-interactive commands, so there
// is a single restore engine and no duplicated behavior.
func RunInteractive(
	ctx context.Context,
	in io.Reader,
	out io.Writer,
) int {
	if ctx == nil {
		ctx = context.Background()
	}

	if in == nil {
		in = os.Stdin
	}

	if out == nil {
		out = os.Stdout
	}

	reader := bufio.NewReader(in)

	fmt.Fprintln(out, "=== Database Restore Automation ===")
	fmt.Fprintln(out, "Guided mode. Type the number of an option, or 'q' to quit.")

	for {
		if err := ctx.Err(); err != nil {
			fmt.Fprintf(out, "\nCancelled: %s\n", singleLineValue(err.Error()))
			return exitCodeFailure
		}

		fmt.Fprintln(out)
		fmt.Fprintln(out, "Select an action:")
		fmt.Fprintln(out, "  1) Validate a configuration file")
		fmt.Fprintln(out, "  2) Run a restore job")
		fmt.Fprintln(out, "  3) Generate scheduler entries")
		fmt.Fprintln(out, "  4) Quit")

		choice, ok := promptLine(reader, out, "Enter choice [1-4]")
		if !ok {
			fmt.Fprintln(out, "\nInput closed. Exiting.")
			return exitCodeOK
		}

		switch strings.ToLower(choice) {
		case "1", "validate":
			interactiveValidate(ctx, reader, out)

		case "2", "restore":
			interactiveRestore(ctx, reader, out)

		case "3", "schedule":
			interactiveSchedule(ctx, reader, out)

		case "4", "q", "quit", "exit":
			fmt.Fprintln(out, "Goodbye.")
			return exitCodeOK

		case "":
			continue

		default:
			fmt.Fprintf(out, "Unrecognized option %q. Choose 1, 2, 3, or 4.\n", choice)
		}
	}
}

func interactiveValidate(
	ctx context.Context,
	reader *bufio.Reader,
	out io.Writer,
) {
	configPath, ok := promptConfigPath(reader, out)
	if !ok {
		return
	}

	fmt.Fprintf(out, "\nValidating %s ...\n", configPath)
	code := RunValidate(ctx, configPath)
	reportExit(out, code)
}

func interactiveRestore(
	ctx context.Context,
	reader *bufio.Reader,
	out io.Writer,
) {
	configPath, ok := promptConfigPath(reader, out)
	if !ok {
		return
	}

	// Load the configuration only to display selectable job titles. RunRestore
	// re-loads and re-validates it, so a stale read here cannot bypass any
	// safety checks.
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(out, "Could not read configuration: %s\n", singleLineValue(err.Error()))
		return
	}

	if err := config.Validate(cfg); err != nil {
		fmt.Fprintf(out, "Configuration is invalid:\n%s\n", err.Error())
		return
	}

	if len(cfg.Jobs) == 0 {
		fmt.Fprintln(out, "No jobs are defined in this configuration.")
		return
	}

	jobName, runAll, ok := promptJobSelection(reader, out, cfg.Jobs)
	if !ok {
		return
	}

	dryRun, ok := promptYesNo(reader, out, "Dry run (validate and log only, no changes)?", true)
	if !ok {
		return
	}

	target := jobName
	if runAll {
		target = "all enabled jobs"
	}

	mode := "REAL restore"
	if dryRun {
		mode = "dry run"
	}

	confirm, ok := promptYesNo(
		reader,
		out,
		fmt.Sprintf("Proceed with %s of %q?", mode, target),
		dryRun,
	)
	if !ok {
		return
	}

	if !confirm {
		fmt.Fprintln(out, "Restore cancelled.")
		return
	}

	fmt.Fprintf(out, "\nStarting %s ...\n", mode)
	code := RunRestore(ctx, RestoreOptions{
		ConfigPath: configPath,
		JobName:    jobName,
		DryRun:     dryRun,
	})
	reportExit(out, code)
}

func interactiveSchedule(
	ctx context.Context,
	reader *bufio.Reader,
	out io.Writer,
) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Target platform:")
	fmt.Fprintln(out, "  1) Linux cron entries")
	fmt.Fprintln(out, "  2) Windows Task Scheduler script")

	platformChoice, ok := promptLine(reader, out, "Enter choice [1-2]")
	if !ok {
		return
	}

	var platform string

	switch strings.ToLower(platformChoice) {
	case "1", "linux":
		platform = "linux"

	case "2", "windows":
		platform = "windows"

	default:
		fmt.Fprintf(out, "Unrecognized platform %q.\n", platformChoice)
		return
	}

	configPath, ok := promptConfigPath(reader, out)
	if !ok {
		return
	}

	defaultRoot, err := os.Getwd()
	if err != nil {
		defaultRoot = ""
	}

	rootDir, ok := promptLineWithDefault(
		reader,
		out,
		"Installed application root directory",
		defaultRoot,
	)
	if !ok {
		return
	}

	if strings.TrimSpace(rootDir) == "" {
		fmt.Fprintln(out, "A root directory is required.")
		return
	}

	fmt.Fprintf(out, "\nGenerating %s scheduler entries:\n\n", platform)

	var code int

	switch platform {
	case "linux":
		code = RunScheduleLinux(ctx, configPath, rootDir, out)

	case "windows":
		code = RunScheduleWindows(ctx, configPath, rootDir, out)
	}

	reportExit(out, code)
}

// promptJobSelection lists every configured job by title and returns the chosen
// job name. The boolean runAll is true when the operator selects "all enabled
// jobs", in which case the returned job name is empty (RunRestore treats an
// empty job name as "all").
func promptJobSelection(
	reader *bufio.Reader,
	out io.Writer,
	jobs []config.JobConfig,
) (jobName string, runAll bool, ok bool) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Available jobs:")

	nameWidth := len("all enabled jobs")
	for _, job := range jobs {
		if width := len(strings.TrimSpace(job.Name)); width > nameWidth {
			nameWidth = width
		}
	}

	for index, job := range jobs {
		state := "disabled"
		if job.IsEnabled() {
			state = "enabled"
		}

		fmt.Fprintf(
			out,
			"  %2d) %-*s  [%-18s] %s\n",
			index+1,
			nameWidth,
			strings.TrimSpace(job.Name),
			strings.TrimSpace(job.TypeName()),
			state,
		)
	}

	fmt.Fprintf(out, "   A) %-*s  (run every enabled job)\n", nameWidth, "all enabled jobs")
	fmt.Fprintln(out, "   0) Back")

	for {
		choice, lineOk := promptLine(reader, out, "Select a job")
		if !lineOk {
			return "", false, false
		}

		normalized := strings.ToLower(choice)

		switch normalized {
		case "0", "b", "back":
			return "", false, false

		case "a", "all":
			return "", true, true
		}

		number, err := strconv.Atoi(choice)
		if err != nil || number < 1 || number > len(jobs) {
			fmt.Fprintf(out, "Enter a number between 1 and %d, A for all, or 0 to go back.\n", len(jobs))
			continue
		}

		selected := strings.TrimSpace(jobs[number-1].Name)
		if selected == "" {
			fmt.Fprintln(out, "That job has no name and cannot be selected directly.")
			continue
		}

		return selected, false, true
	}
}

func promptConfigPath(
	reader *bufio.Reader,
	out io.Writer,
) (string, bool) {
	value, ok := promptLineWithDefault(
		reader,
		out,
		"Configuration file path",
		defaultConfigPath(),
	)
	if !ok {
		return "", false
	}

	value = strings.TrimSpace(value)
	if value == "" {
		fmt.Fprintln(out, "A configuration file path is required.")
		return "", false
	}

	return value, true
}

// defaultConfigPath suggests the bundled config for the current platform when
// it exists, so the operator can usually accept the default with Enter.
func defaultConfigPath() string {
	name := "config/restore-jobs.linux.yml"
	if runtime.GOOS == "windows" {
		name = "config/restore-jobs.windows.yml"
	}

	if info, err := os.Stat(name); err == nil && info.Mode().IsRegular() {
		return filepath.Clean(name)
	}

	return ""
}

func promptYesNo(
	reader *bufio.Reader,
	out io.Writer,
	question string,
	defaultYes bool,
) (bool, bool) {
	hint := "[y/N]"
	if defaultYes {
		hint = "[Y/n]"
	}

	for {
		fmt.Fprintf(out, "%s %s: ", question, hint)

		line, ok := readLine(reader)
		if !ok {
			return false, false
		}

		switch strings.ToLower(strings.TrimSpace(line)) {
		case "":
			return defaultYes, true

		case "y", "yes":
			return true, true

		case "n", "no":
			return false, true

		default:
			fmt.Fprintln(out, "Please answer y or n.")
		}
	}
}

func promptLine(
	reader *bufio.Reader,
	out io.Writer,
	label string,
) (string, bool) {
	fmt.Fprintf(out, "%s: ", label)

	line, ok := readLine(reader)
	if !ok {
		return "", false
	}

	return strings.TrimSpace(line), true
}

func promptLineWithDefault(
	reader *bufio.Reader,
	out io.Writer,
	label string,
	defaultValue string,
) (string, bool) {
	defaultValue = strings.TrimSpace(defaultValue)

	if defaultValue != "" {
		fmt.Fprintf(out, "%s [%s]: ", label, defaultValue)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}

	line, ok := readLine(reader)
	if !ok {
		return "", false
	}

	line = strings.TrimSpace(line)
	if line == "" {
		return defaultValue, true
	}

	return line, true
}

// readLine returns the next input line. The boolean is false on EOF (for
// example when stdin is closed or piped input is exhausted), letting callers
// exit cleanly instead of looping forever.
func readLine(reader *bufio.Reader) (string, bool) {
	line, err := reader.ReadString('\n')

	if line == "" && err != nil {
		return "", false
	}

	return strings.TrimRight(line, "\r\n"), true
}

func reportExit(out io.Writer, code int) {
	if code == exitCodeOK {
		fmt.Fprintln(out, "Completed successfully.")
		return
	}

	fmt.Fprintf(out, "Finished with a non-zero exit code: %d\n", code)
}

func singleLineValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")

	return value
}
