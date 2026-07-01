package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"db-restore-automation/internal/alerts"
	"db-restore-automation/internal/backup"
	"db-restore-automation/internal/config"
	"db-restore-automation/internal/restore"
	"db-restore-automation/internal/safety"
)

type RestoreOptions struct {
	ConfigPath string
	JobName    string
	DryRun     bool
	Timeout    time.Duration // default per-job timeout when the YAML field is absent
}

func RunRestore(ctx context.Context, opts RestoreOptions) int {
	logger, err := newLogger()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if ctx == nil {
		ctx = context.Background()
	}

	opts.ConfigPath = strings.TrimSpace(opts.ConfigPath)
	opts.JobName = strings.TrimSpace(opts.JobName)

	if opts.ConfigPath == "" {
		logger.Error("restore_run=config_path_required")
		return 2
	}

	selectedText := opts.JobName
	if selectedText == "" {
		selectedText = "all"
	}

	logger.Info(fmt.Sprintf(
		"restore_run=start config=%s job=%s dry_run=%v",
		opts.ConfigPath,
		selectedText,
		opts.DryRun,
	))

	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		logger.Error(fmt.Sprintf(
			"restore_run=config_load_failed error=%s",
			sanitizeLogValue(err.Error()),
		))
		return 2
	}

	if err := config.Validate(cfg); err != nil {
		logger.Error(fmt.Sprintf(
			"restore_run=config_validation_failed error=%s",
			sanitizeLogValue(err.Error()),
		))
		return 2
	}

	jobs, err := selectJobs(cfg, opts.JobName)
	if err != nil {
		logger.Error(sanitizeLogValue(err.Error()))
		printAvailableJobs(cfg)
		return 2
	}

	resolver := backup.Resolver{
		Logger: logger,
	}

	checker := safety.Checker{
		Logger: logger,
	}

	alertManager := alerts.NewManager(cfg.Alerts, logger)

	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
		logger.Warn(fmt.Sprintf(
			"restore_run=hostname_lookup_failed error=%s",
			sanitizeLogValue(err.Error()),
		))
	} else {
		host = strings.TrimSpace(host)
		if host == "" {
			host = "unknown"
		}
	}

	selectedEnabledJobs := countEnabledJobs(jobs)
	processedJobs := 0
	failures := 0
	cancelled := false

	if selectedEnabledJobs == 0 {
		logger.Warn(fmt.Sprintf(
			"restore_run=no_enabled_jobs selected_jobs=%d",
			len(jobs),
		))
	}

	for _, job := range jobs {
		jobName := strings.TrimSpace(job.Name)
		jobType := strings.TrimSpace(job.TypeName())

		if !job.IsEnabled() {
			logger.Info(fmt.Sprintf(
				"job=%s type=%s enabled=false action=skip",
				sanitizeLogValue(jobName),
				sanitizeLogValue(jobType),
			))
			continue
		}

		if err := ctx.Err(); err != nil {
			cancelled = true
			failures++

			logger.Error(fmt.Sprintf(
				"restore_run=cancelled before_job=%s error=%s",
				sanitizeLogValue(jobName),
				sanitizeLogValue(err.Error()),
			))
			break
		}

		processedJobs++

		jobCtx := ctx
		jobCancel := func() {}

		if d, ok := job.JobTimeout(); ok {
			var derived context.CancelFunc
			jobCtx, derived = context.WithTimeout(ctx, d)
			jobCancel = derived

			logger.Info(fmt.Sprintf(
				"job=%s type=%s timeout=%s source=config",
				sanitizeLogValue(jobName),
				sanitizeLogValue(jobType),
				d,
			))
		} else if opts.Timeout > 0 {
			var derived context.CancelFunc
			jobCtx, derived = context.WithTimeout(ctx, opts.Timeout)
			jobCancel = derived

			logger.Info(fmt.Sprintf(
				"job=%s type=%s timeout=%s source=cli_default",
				sanitizeLogValue(jobName),
				sanitizeLogValue(jobType),
				opts.Timeout,
			))
		}

		started := time.Now()
		source := job.SourceText()
		target := job.TargetText()
		credentialMethod := job.CredentialMethod()

		logger.Info(fmt.Sprintf(
			"job=%s type=%s source_database=%s target=%s credential_method=%s status=start",
			sanitizeLogValue(jobName),
			sanitizeLogValue(jobType),
			sanitizeLogValue(source),
			sanitizeLogValue(target),
			sanitizeLogValue(credentialMethod),
		))

		result := "success"
		errorMessage := ""
		backupFile := ""

		if err := checker.Validate(job); err != nil {
			result = "failure"
			errorMessage = err.Error()

			logger.Error(fmt.Sprintf(
				"job=%s type=%s result=failure reason=safety_validation_failed error=%s",
				sanitizeLogValue(jobName),
				sanitizeLogValue(jobType),
				sanitizeLogValue(errorMessage),
			))
		}

		if result == "success" {
			if err := checker.Confirm(job, opts.DryRun); err != nil {
				result = "failure"
				errorMessage = err.Error()

				logger.Error(fmt.Sprintf(
					"job=%s type=%s result=failure reason=confirmation_failed error=%s",
					sanitizeLogValue(jobName),
					sanitizeLogValue(jobType),
					sanitizeLogValue(errorMessage),
				))
			}
		}

		if result == "success" {
			if job.UsesBackupFile() {
				backupFile, err = resolver.Latest(job)
				if err != nil {
					result = "failure"
					errorMessage = err.Error()

					logger.Error(fmt.Sprintf(
						"job=%s type=%s result=failure reason=no_backup_file dry_run=%v error=%s",
						sanitizeLogValue(jobName),
						sanitizeLogValue(jobType),
						opts.DryRun,
						sanitizeLogValue(errorMessage),
					))
				} else {
					backupFile = strings.TrimSpace(backupFile)

					if backupFile == "" {
						result = "failure"
						errorMessage = "backup resolver returned an empty backup file path"

						logger.Error(fmt.Sprintf(
							"job=%s type=%s result=failure reason=empty_backup_file",
							sanitizeLogValue(jobName),
							sanitizeLogValue(jobType),
						))
					} else {
						logger.Info(fmt.Sprintf(
							"job=%s type=%s selected_backup=%s",
							sanitizeLogValue(jobName),
							sanitizeLogValue(jobType),
							sanitizeLogValue(backupFile),
						))
					}
				}
			} else {
				providerName := restoreProviderName(jobType)

				logger.Info(fmt.Sprintf(
					"job=%s type=%s selected_backup=not_applicable restore_provider=%s",
					sanitizeLogValue(jobName),
					sanitizeLogValue(jobType),
					sanitizeLogValue(providerName),
				))
			}
		}

		if result == "success" {
			if err := jobCtx.Err(); err != nil {
				result = "failure"
				errorMessage = err.Error()

				// A per-job timeout fails only this job. The run is marked
				// cancelled only when the parent context itself is done.
				reason := "job_timeout_before_restore"
				if ctx.Err() != nil {
					cancelled = true
					reason = "context_cancelled_before_restore"
				}

				logger.Error(fmt.Sprintf(
					"job=%s type=%s result=failure reason=%s error=%s",
					sanitizeLogValue(jobName),
					sanitizeLogValue(jobType),
					reason,
					sanitizeLogValue(errorMessage),
				))
			}
		}

		if result == "success" {
			provider, providerErr := restore.ProviderFor(jobType, logger)
			if providerErr != nil {
				result = "failure"
				errorMessage = providerErr.Error()

				logger.Error(fmt.Sprintf(
					"job=%s type=%s result=failure reason=provider_not_available error=%s",
					sanitizeLogValue(jobName),
					sanitizeLogValue(jobType),
					sanitizeLogValue(errorMessage),
				))
			} else {
				logger.Info(fmt.Sprintf(
					"job=%s type=%s restore_provider=%T",
					sanitizeLogValue(jobName),
					sanitizeLogValue(jobType),
					provider,
				))

				restoreErr := provider.Restore(
					jobCtx,
					cfg,
					job,
					restore.Options{
						DryRun:     opts.DryRun,
						BackupFile: backupFile,
					},
				)

				if restoreErr != nil {
					result = "failure"
					errorMessage = restoreErr.Error()

					logger.Error(fmt.Sprintf(
						"job=%s type=%s result=failure reason=restore_failed error=%s",
						sanitizeLogValue(jobName),
						sanitizeLogValue(jobType),
						sanitizeLogValue(errorMessage),
					))
				}
			}
		}

		jobCancel()

		finished := time.Now()
		duration := finished.Sub(started)

		if result != "success" {
			failures++

			logger.Error(fmt.Sprintf(
				"job=%s type=%s status=end result=failure dry_run=%v duration=%s error=%s",
				sanitizeLogValue(jobName),
				sanitizeLogValue(jobType),
				opts.DryRun,
				duration.Round(time.Millisecond),
				sanitizeLogValue(errorMessage),
			))
		} else if opts.DryRun {
			logger.Success(fmt.Sprintf(
				"job=%s type=%s status=end result=success dry_run=true duration=%s",
				sanitizeLogValue(jobName),
				sanitizeLogValue(jobType),
				duration.Round(time.Millisecond),
			))
		} else {
			logger.Success(fmt.Sprintf(
				"job=%s type=%s status=end result=success dry_run=false duration=%s",
				sanitizeLogValue(jobName),
				sanitizeLogValue(jobType),
				duration.Round(time.Millisecond),
			))
		}

		alertManager.Notify(ctx, alerts.Event{
			JobName:     jobName,
			JobType:     jobType,
			Source:      source,
			Target:      target,
			Result:      result,
			DryRun:      opts.DryRun,
			Error:       errorMessage,
			StartedAt:   started,
			FinishedAt:  finished,
			Duration:    duration,
			Host:        host,
			MainLogFile: logger.FilePath(),
			ProviderLog: restore.ProviderLog(job),
		})

		if err := ctx.Err(); err != nil {
			cancelled = true

			logger.Warn(fmt.Sprintf(
				"restore_run=context_cancelled action=stop_remaining_jobs error=%s",
				sanitizeLogValue(err.Error()),
			))
			break
		}
	}

	logger.Info(fmt.Sprintf(
		"restore_run=end selected_jobs=%d enabled_jobs=%d processed_jobs=%d failures=%d cancelled=%v",
		len(jobs),
		selectedEnabledJobs,
		processedJobs,
		failures,
		cancelled,
	))

	if failures > 0 || cancelled {
		return 1
	}

	return 0
}

func selectJobs(cfg config.Config, jobName string) ([]config.JobConfig, error) {
	jobName = strings.TrimSpace(jobName)

	if jobName == "" {
		return cfg.Jobs, nil
	}

	for _, job := range cfg.Jobs {
		if strings.EqualFold(strings.TrimSpace(job.Name), jobName) {
			return []config.JobConfig{job}, nil
		}
	}

	return nil, fmt.Errorf(
		"restore_run=job_not_found job=%s",
		sanitizeLogValue(jobName),
	)
}

func countEnabledJobs(jobs []config.JobConfig) int {
	count := 0

	for _, job := range jobs {
		if job.IsEnabled() {
			count++
		}
	}

	return count
}

func restoreProviderName(jobType string) string {
	switch jobType {
	case config.TypeOracleRMAN:
		return "OracleRMAN"

	case config.TypeMSSQLPowerProtect:
		return "DellPowerProtect"

	default:
		return "not_applicable"
	}
}

func sanitizeLogValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")

	return value
}

func printAvailableJobs(cfg config.Config) {
	fmt.Fprintln(os.Stderr, "Available jobs:")

	if len(cfg.Jobs) == 0 {
		fmt.Fprintln(os.Stderr, "  (none)")
		return
	}

	for _, job := range cfg.Jobs {
		status := "enabled"
		if !job.IsEnabled() {
			status = "disabled"
		}

		fmt.Fprintf(
			os.Stderr,
			"  - %s (%s)\n",
			strings.TrimSpace(job.Name),
			status,
		)
	}
}