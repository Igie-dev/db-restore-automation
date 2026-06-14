package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"db-restore-automation/internal/config"
)

func RunScheduleLinux(ctx context.Context, configPath, rootDir string, out io.Writer) int {
	_ = ctx
	cfg, err := loadValidForSchedule(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	root := shellQuote(rootDir)
	cfgPath := shellQuote(configPath)
	for _, job := range cfg.Jobs {
		if !job.IsEnabled() || !job.ScheduleEnabled() {
			continue
		}
		if strings.TrimSpace(job.Schedule.LinuxCron) == "" {
			fmt.Fprintf(out, "# %s skipped: schedule.linux_cron is empty\n", job.Name)
			continue
		}
		fmt.Fprintf(out, "# %s\n", job.Name)
		fmt.Fprintf(out, "%s cd %s && ./db-restore-automation restore --config %s --job %s >> ./logs/%s-cron.log 2>&1\n", job.Schedule.LinuxCron, root, cfgPath, shellQuote(job.Name), safeLogName(job.Name))
	}
	return 0
}

func RunScheduleWindows(ctx context.Context, configPath, rootDir string, out io.Writer) int {
	_ = ctx
	cfg, err := loadValidForSchedule(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	exe := filepath.Join(rootDir, "db-restore-automation.exe")
	fmt.Fprintln(out, "# Auto-generated Windows Task Scheduler commands")
	fmt.Fprintln(out, "$ErrorActionPreference = 'Stop'")
	for _, job := range cfg.Jobs {
		if !job.IsEnabled() || !job.ScheduleEnabled() {
			continue
		}
		frequency := strings.ToUpper(strings.TrimSpace(job.Schedule.WindowsFrequency))
		if frequency == "" {
			frequency = "DAILY"
		}
		taskName := "DB Restore - " + job.Name
		runCommand := fmt.Sprintf("%s restore --config %s --job %s", psArgument(exe), psArgument(configPath), psArgument(job.Name))
		fmt.Fprintf(out, "\n# %s\n", taskName)
		fmt.Fprintf(out, "$taskName = '%s'\n", psQuote(taskName))
		fmt.Fprintf(out, "$taskRunCommand = '%s'\n", psQuote(runCommand))
		fmt.Fprintf(out, "schtasks.exe /Create /TN $taskName /SC '%s' /ST '%s' /TR $taskRunCommand /F\n", psQuote(frequency), psQuote(job.Schedule.WindowsTime))
		fmt.Fprintln(out, "if ($LASTEXITCODE -ne 0) { throw \"Failed to create scheduled task: $taskName\" }")
	}
	return 0
}

func loadValidForSchedule(configPath string) (config.Config, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return config.Config{}, err
	}
	if err := config.Validate(cfg); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || strings.ContainsRune("_./:-", r))
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func psQuote(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func psArgument(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func safeLogName(value string) string {
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
