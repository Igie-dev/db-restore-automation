package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"db-restore-automation/internal/config"
)

const (
	linuxExecutableName   = "db-restore-automation"
	windowsExecutableName = "db-restore-automation.exe"
)

func RunScheduleLinux(
	ctx context.Context,
	configPath string,
	rootDir string,
	out io.Writer,
) int {
	if ctx == nil {
		ctx = context.Background()
	}

	if out == nil {
		out = os.Stdout
	}

	normalizedConfigPath, err := scheduleAbsolutePath(configPath, "config path")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	normalizedRootDir, err := scheduleAbsolutePath(rootDir, "root directory")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if err := ctx.Err(); err != nil {
		fmt.Fprintf(
			os.Stderr,
			"schedule_linux=context_cancelled error=%s\n",
			scheduleSingleLine(err.Error()),
		)
		return 1
	}

	cfg, err := loadValidForSchedule(normalizedConfigPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	executablePath := filepath.Join(
		normalizedRootDir,
		linuxExecutableName,
	)

	logDirectory := filepath.Join(
		normalizedRootDir,
		"logs",
	)

	generatedJobs := 0

	if err := scheduleWriteLine(
		out,
		"# Auto-generated Linux cron entries.",
	); err != nil {
		return scheduleOutputError("linux", err)
	}

	if err := scheduleWriteLine(
		out,
		"# Add these entries to the crontab of the account that owns the required database credentials.",
	); err != nil {
		return scheduleOutputError("linux", err)
	}

	for _, job := range cfg.Jobs {
		if err := ctx.Err(); err != nil {
			fmt.Fprintf(
				os.Stderr,
				"schedule_linux=context_cancelled generated_jobs=%d error=%s\n",
				generatedJobs,
				scheduleSingleLine(err.Error()),
			)
			return 1
		}

		if !job.IsEnabled() || !job.ScheduleEnabled() {
			continue
		}

		jobName := strings.TrimSpace(job.Name)
		if jobName == "" {
			fmt.Fprintln(
				os.Stderr,
				"schedule_linux=invalid_job error=job name must not be empty",
			)
			return 2
		}

		if err := scheduleValidateSingleLine(jobName, "job name"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}

		cronExpression := strings.TrimSpace(job.Schedule.LinuxCron)
		if cronExpression == "" {
			if err := scheduleWritef(
				out,
				"\n# %s skipped: schedule.linux_cron is empty\n",
				scheduleComment(jobName),
			); err != nil {
				return scheduleOutputError("linux", err)
			}

			continue
		}

		if err := scheduleValidateSingleLine(
			cronExpression,
			fmt.Sprintf("Linux cron expression for job %q", jobName),
		); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}

		logFileName := safeLogName(jobName) + "-cron.log"
		logFilePath := filepath.Join(logDirectory, logFileName)

		command := fmt.Sprintf(
			"cd %s && mkdir -p %s && %s restore --config %s --job %s >> %s 2>&1",
			shellQuote(normalizedRootDir),
			shellQuote(logDirectory),
			shellQuote(executablePath),
			shellQuote(normalizedConfigPath),
			shellQuote(jobName),
			shellQuote(logFilePath),
		)

		// In crontab commands, an unescaped percent sign is converted into
		// a newline and the remaining text is sent to the command's stdin.
		// Escape all percent signs in the generated command.
		command = escapeCronPercents(command)

		if err := scheduleWritef(
			out,
			"\n# %s\n%s %s\n",
			scheduleComment(jobName),
			cronExpression,
			command,
		); err != nil {
			return scheduleOutputError("linux", err)
		}

		generatedJobs++
	}

	if generatedJobs == 0 {
		if err := scheduleWriteLine(
			out,
			"\n# No enabled Linux jobs with a configured schedule were found.",
		); err != nil {
			return scheduleOutputError("linux", err)
		}
	}

	return 0
}

func RunScheduleWindows(
	ctx context.Context,
	configPath string,
	rootDir string,
	out io.Writer,
) int {
	if ctx == nil {
		ctx = context.Background()
	}

	if out == nil {
		out = os.Stdout
	}

	normalizedConfigPath, err := scheduleAbsolutePath(configPath, "config path")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	normalizedRootDir, err := scheduleAbsolutePath(rootDir, "root directory")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if err := ctx.Err(); err != nil {
		fmt.Fprintf(
			os.Stderr,
			"schedule_windows=context_cancelled error=%s\n",
			scheduleSingleLine(err.Error()),
		)
		return 1
	}

	cfg, err := loadValidForSchedule(normalizedConfigPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	executablePath := filepath.Join(
		normalizedRootDir,
		windowsExecutableName,
	)

	headerLines := []string{
		"# Auto-generated Windows Task Scheduler script.",
		"# Run this script in PowerShell using the Windows account that owns",
		"# the required pgpass.conf, MySQL login path, Oracle Wallet, or",
		"# Dell PowerProtect lockbox credentials.",
		"",
		"$ErrorActionPreference = 'Stop'",
		"Import-Module ScheduledTasks -ErrorAction Stop",
		"",
		fmt.Sprintf(
			"$exePath = '%s'",
			psQuote(executablePath),
		),
		fmt.Sprintf(
			"$configPath = '%s'",
			psQuote(normalizedConfigPath),
		),
		fmt.Sprintf(
			"$workingDirectory = '%s'",
			psQuote(normalizedRootDir),
		),
		"",
		"if (-not (Test-Path -LiteralPath $exePath -PathType Leaf)) {",
		"    throw \"Executable not found: $exePath\"",
		"}",
		"",
		"if (-not (Test-Path -LiteralPath $configPath -PathType Leaf)) {",
		"    throw \"Configuration file not found: $configPath\"",
		"}",
		"",
		"if (-not (Test-Path -LiteralPath $workingDirectory -PathType Container)) {",
		"    throw \"Working directory not found: $workingDirectory\"",
		"}",
		"",
		// Do not start another instance when the previous restore is still
		// running. Database restore jobs must not overlap.
		"$taskSettings = New-ScheduledTaskSettingsSet `",
		"    -MultipleInstances IgnoreNew `",
		"    -StartWhenAvailable",
	}

	for _, line := range headerLines {
		if err := scheduleWriteLine(out, line); err != nil {
			return scheduleOutputError("windows", err)
		}
	}

	generatedJobs := 0

	for _, job := range cfg.Jobs {
		if err := ctx.Err(); err != nil {
			fmt.Fprintf(
				os.Stderr,
				"schedule_windows=context_cancelled generated_jobs=%d error=%s\n",
				generatedJobs,
				scheduleSingleLine(err.Error()),
			)
			return 1
		}

		if !job.IsEnabled() || !job.ScheduleEnabled() {
			continue
		}

		jobName := strings.TrimSpace(job.Name)
		if jobName == "" {
			fmt.Fprintln(
				os.Stderr,
				"schedule_windows=invalid_job error=job name must not be empty",
			)
			return 2
		}

		if err := scheduleValidateSingleLine(jobName, "job name"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}

		windowsTime := strings.TrimSpace(job.Schedule.WindowsTime)
		if windowsTime == "" {
			if err := scheduleWritef(
				out,
				"\n# %s skipped: schedule.windows_time is empty\n",
				scheduleComment(jobName),
			); err != nil {
				return scheduleOutputError("windows", err)
			}

			continue
		}

		normalizedTime, err := normalizeWindowsScheduleTime(windowsTime)
		if err != nil {
			fmt.Fprintf(
				os.Stderr,
				"schedule_windows=invalid_time job=%s error=%s\n",
				scheduleSingleLine(jobName),
				scheduleSingleLine(err.Error()),
			)
			return 2
		}

		frequency := strings.ToUpper(
			strings.TrimSpace(job.Schedule.WindowsFrequency),
		)

		if frequency == "" {
			frequency = "DAILY"
		}

		// DAILY and MONTHLY are supported. New-ScheduledTaskTrigger has no
		// monthly option, so a MONTHLY schedule builds a CIM monthly trigger.
		var triggerLines []string

		switch frequency {
		case "DAILY":
			triggerLines = []string{
				fmt.Sprintf(
					"$taskTrigger = New-ScheduledTaskTrigger -Daily -At '%s'",
					psQuote(normalizedTime),
				),
			}

		case "MONTHLY":
			day := job.Schedule.DayOfMonth
			if day < 1 || day > 31 {
				fmt.Fprintf(
					os.Stderr,
					"schedule_windows=invalid_day_of_month job=%s day_of_month=%d error=MONTHLY requires day_of_month between 1 and 31\n",
					scheduleSingleLine(jobName),
					day,
				)
				return 2
			}

			// MSFT_TaskMonthlyTrigger.DaysOfMonth is a bitmask: day N sets
			// bit N-1 (day 1 => 1, day 2 => 2, day 3 => 4, ...).
			daysOfMonthMask := 1 << (uint(day) - 1)

			triggerLines = []string{
				"$taskTrigger = New-CimInstance `",
				"    -CimClass (Get-CimClass -Namespace 'Root/Microsoft/Windows/TaskScheduler' -ClassName 'MSFT_TaskMonthlyTrigger') `",
				"    -ClientOnly",
				fmt.Sprintf(
					"$taskTrigger.DaysOfMonth = %d  # day-of-month %d",
					daysOfMonthMask,
					day,
				),
				fmt.Sprintf(
					"$taskTrigger.StartBoundary = ([DateTime]'%s').ToString('s')",
					psQuote(normalizedTime),
				),
				"$taskTrigger.Enabled = $true",
			}

		default:
			fmt.Fprintf(
				os.Stderr,
				"schedule_windows=unsupported_frequency job=%s frequency=%s error=only DAILY and MONTHLY are supported\n",
				scheduleSingleLine(jobName),
				scheduleSingleLine(frequency),
			)
			return 2
		}

		taskName, err := windowsTaskName(jobName)
		if err != nil {
			fmt.Fprintf(
				os.Stderr,
				"schedule_windows=invalid_task_name job=%s error=%s\n",
				scheduleSingleLine(jobName),
				scheduleSingleLine(err.Error()),
			)
			return 2
		}

		taskArguments := strings.Join(
			[]string{
				windowsCommandLineArgument("restore"),
				windowsCommandLineArgument("--config"),
				windowsCommandLineArgument(normalizedConfigPath),
				windowsCommandLineArgument("--job"),
				windowsCommandLineArgument(jobName),
			},
			" ",
		)

		description := fmt.Sprintf(
			"Runs the database restore automation job %q.",
			jobName,
		)

		lines := []string{
			"",
			fmt.Sprintf("# %s", scheduleComment(taskName)),
			fmt.Sprintf("$taskName = '%s'", psQuote(taskName)),
			fmt.Sprintf(
				"$taskDescription = '%s'",
				psQuote(description),
			),
			fmt.Sprintf(
				"$taskArguments = '%s'",
				psQuote(taskArguments),
			),
			"$taskAction = New-ScheduledTaskAction `",
			"    -Execute $exePath `",
			"    -Argument $taskArguments `",
			"    -WorkingDirectory $workingDirectory",
			"",
		}

		lines = append(lines, triggerLines...)

		lines = append(
			lines,
			"",
			"Register-ScheduledTask `",
			"    -TaskName $taskName `",
			"    -Description $taskDescription `",
			"    -Action $taskAction `",
			"    -Trigger $taskTrigger `",
			"    -Settings $taskSettings `",
			"    -Force | Out-Null",
			"",
			"Write-Host \"Created scheduled task: $taskName\"",
		)

		for _, line := range lines {
			if err := scheduleWriteLine(out, line); err != nil {
				return scheduleOutputError("windows", err)
			}
		}

		generatedJobs++
	}

	if generatedJobs == 0 {
		if err := scheduleWriteLine(
			out,
			"\n# No enabled Windows jobs with a configured schedule were found.",
		); err != nil {
			return scheduleOutputError("windows", err)
		}
	}

	return 0
}

func loadValidForSchedule(configPath string) (config.Config, error) {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return config.Config{}, fmt.Errorf(
			"schedule=config_path_required",
		)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return config.Config{}, fmt.Errorf(
			"schedule=config_load_failed path=%s error=%w",
			configPath,
			err,
		)
	}

	if err := config.Validate(cfg); err != nil {
		return config.Config{}, fmt.Errorf(
			"schedule=config_validation_failed path=%s error=%w",
			configPath,
			err,
		)
	}

	return cfg, nil
}

func scheduleAbsolutePath(value string, fieldName string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf(
			"schedule=invalid_path field=%s error=value is required",
			scheduleSingleLine(fieldName),
		)
	}

	if err := scheduleValidateSingleLine(value, fieldName); err != nil {
		return "", err
	}

	absolutePath, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf(
			"schedule=invalid_path field=%s value=%s error=%w",
			scheduleSingleLine(fieldName),
			scheduleSingleLine(value),
			err,
		)
	}

	return filepath.Clean(absolutePath), nil
}

func normalizeWindowsScheduleTime(value string) (string, error) {
	value = strings.TrimSpace(value)

	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return "", fmt.Errorf(
			"invalid Windows schedule time %q; expected HH:mm using 24-hour time",
			value,
		)
	}

	return parsed.Format("15:04"), nil
}

func windowsTaskName(jobName string) (string, error) {
	jobName = strings.TrimSpace(jobName)
	if jobName == "" {
		return "", fmt.Errorf("job name must not be empty")
	}

	taskName := "DB Restore - " + jobName

	for _, r := range taskName {
		if unicode.IsControl(r) {
			return "", fmt.Errorf(
				"task name contains a control character",
			)
		}

		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|':
			return "", fmt.Errorf(
				"task name contains invalid character %q",
				r,
			)
		}
	}

	if len([]rune(taskName)) > 200 {
		return "", fmt.Errorf(
			"task name exceeds the maximum supported length of 200 characters",
		)
	}

	return taskName, nil
}

func windowsCommandLineArgument(value string) string {
	if value == "" {
		return `""`
	}

	if !strings.ContainsAny(value, " \t\n\v\"") {
		return value
	}

	var builder strings.Builder
	builder.WriteByte('"')

	backslashes := 0

	for _, r := range value {
		if r == '\\' {
			backslashes++
			continue
		}

		if r == '"' {
			// Backslashes before a quote must be doubled, and the quote
			// itself must be escaped with an additional backslash.
			builder.WriteString(strings.Repeat("\\", backslashes*2+1))
			builder.WriteRune('"')
			backslashes = 0
			continue
		}

		if backslashes > 0 {
			builder.WriteString(strings.Repeat("\\", backslashes))
			backslashes = 0
		}

		builder.WriteRune(r)
	}

	// Backslashes immediately before the closing quote must be doubled.
	if backslashes > 0 {
		builder.WriteString(strings.Repeat("\\", backslashes*2))
	}

	builder.WriteByte('"')

	return builder.String()
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}

	isSafe := strings.IndexFunc(value, func(r rune) bool {
		return !((r >= 'A' && r <= 'Z') ||
			(r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') ||
			strings.ContainsRune("_./:-", r))
	}) == -1

	if isSafe {
		return value
	}

	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func escapeCronPercents(value string) string {
	return strings.ReplaceAll(value, "%", `\%`)
}

func psQuote(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func safeLogName(value string) string {
	value = strings.TrimSpace(value)

	var builder strings.Builder

	for _, r := range value {
		switch {
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)

		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)

		case r >= '0' && r <= '9':
			builder.WriteRune(r)

		case r == '_', r == '-', r == '.':
			builder.WriteRune(r)

		default:
			builder.WriteByte('_')
		}
	}

	result := strings.Trim(builder.String(), ".")
	result = strings.TrimSpace(result)

	if result == "" {
		return "job"
	}

	const maximumLength = 100
	if len(result) > maximumLength {
		result = result[:maximumLength]
	}

	return result
}

func scheduleValidateSingleLine(
	value string,
	fieldName string,
) error {
	if strings.ContainsRune(value, '\x00') {
		return fmt.Errorf(
			"schedule=invalid_value field=%s error=value contains a null character",
			scheduleSingleLine(fieldName),
		)
	}

	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf(
			"schedule=invalid_value field=%s error=value must be on one line",
			scheduleSingleLine(fieldName),
		)
	}

	return nil
}

func scheduleComment(value string) string {
	value = scheduleSingleLine(value)
	value = strings.ReplaceAll(value, "#", "_")

	return value
}

func scheduleSingleLine(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")

	return value
}

func scheduleWriteLine(out io.Writer, value string) error {
	_, err := fmt.Fprintln(out, value)
	return err
}

func scheduleWritef(
	out io.Writer,
	format string,
	args ...any,
) error {
	_, err := fmt.Fprintf(out, format, args...)
	return err
}

func scheduleOutputError(platform string, err error) int {
	fmt.Fprintf(
		os.Stderr,
		"schedule_%s=output_failed error=%s\n",
		scheduleSingleLine(platform),
		scheduleSingleLine(err.Error()),
	)

	return 2
}
