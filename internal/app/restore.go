package app

import (
	"context"
	"fmt"
	"os"
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
}

func RunRestore(ctx context.Context, opts RestoreOptions) int {
	logger, err := newLogger()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	selectedText := opts.JobName
	if selectedText == "" {
		selectedText = "all"
	}
	logger.Info(fmt.Sprintf("restore_run=start config=%s job=%s dry_run=%v", opts.ConfigPath, selectedText, opts.DryRun))

	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		logger.Error(fmt.Sprintf("restore_run=config_load_failed error=%s", err.Error()))
		return 2
	}
	if err := config.Validate(cfg); err != nil {
		logger.Error(fmt.Sprintf("restore_run=config_validation_failed error=%s", err.Error()))
		return 2
	}

	jobs, err := selectJobs(cfg, opts.JobName)
	if err != nil {
		logger.Error(err.Error())
		printAvailableJobs(cfg)
		return 2
	}

	resolver := backup.Resolver{Logger: logger}
	checker := safety.Checker{Logger: logger}
	alertManager := alerts.NewManager(cfg.Alerts, logger)
	host, _ := os.Hostname()

	failures := 0
	enabledJobs := 0

	for _, job := range jobs {
		jobName := job.Name
		jobType := job.TypeName()
		if !job.IsEnabled() {
			logger.Info(fmt.Sprintf("job=%s type=%s enabled=false action=skip", jobName, jobType))
			continue
		}
		enabledJobs++
		started := time.Now()
		source := job.SourceText()
		target := job.TargetText()
		credentialMethod := job.CredentialMethod()
		logger.Info(fmt.Sprintf("job=%s type=%s source_database=%s target=%s credential_method=%s status=start", jobName, jobType, source, target, credentialMethod))

		result := "success"
		errorMessage := ""
		backupFile := ""

		if err := checker.Validate(job); err != nil {
			result = "failure"
			errorMessage = err.Error()
		}
		if result == "success" {
			if err := checker.Confirm(job, opts.DryRun); err != nil {
				result = "failure"
				errorMessage = err.Error()
				logger.Error(fmt.Sprintf("job=%s type=%s result=failure reason=confirmation_failed", jobName, jobType))
			}
		}
		if result == "success" {
			if job.UsesBackupFile() {
				backupFile, err = resolver.Latest(job)
				if err != nil {
					if opts.DryRun {
						backupFile = "not_found_dry_run"
						logger.Warn(fmt.Sprintf("job=%s type=%s dry_run=true selected_backup=not_found backup_check=skipped", jobName, jobType))
					} else {
						result = "failure"
						errorMessage = err.Error()
						logger.Error(fmt.Sprintf("job=%s type=%s result=failure reason=no_backup_file", jobName, jobType))
					}
				} else {
					logger.Info(fmt.Sprintf("job=%s type=%s selected_backup=%s", jobName, jobType, backupFile))
				}
			} else {
				providerName := "not_applicable"
				if jobType == config.TypeOracleRMAN {
					providerName = "OracleRMAN"
				}
				if jobType == config.TypeMSSQLPowerProtect {
					providerName = "DellPowerProtect"
				}
				logger.Info(fmt.Sprintf("job=%s type=%s selected_backup=not_applicable restore_provider=%s", jobName, jobType, providerName))
			}
		}
		if result == "success" {
			provider, err := restore.ProviderFor(jobType, logger)
			if err != nil {
				result = "failure"
				errorMessage = err.Error()
			} else {
				logger.Info(fmt.Sprintf("job=%s type=%s restore_provider=%T", jobName, jobType, provider))
				if err := provider.Restore(ctx, cfg, job, restore.Options{DryRun: opts.DryRun, BackupFile: backupFile}); err != nil {
					result = "failure"
					errorMessage = err.Error()
				}
			}
		}

		finished := time.Now()
		duration := finished.Sub(started)
		if result != "success" {
			failures++
			logger.Error(fmt.Sprintf("job=%s type=%s status=end result=failure duration=%s error=%s", jobName, jobType, duration.Round(time.Millisecond), errorMessage))
		} else if opts.DryRun {
			logger.Success(fmt.Sprintf("job=%s type=%s status=end result=success dry_run=true duration=%s", jobName, jobType, duration.Round(time.Millisecond)))
		} else {
			logger.Success(fmt.Sprintf("job=%s type=%s status=end result=success duration=%s", jobName, jobType, duration.Round(time.Millisecond)))
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
	}

	logger.Info(fmt.Sprintf("restore_run=end enabled_jobs=%d failures=%d", enabledJobs, failures))
	if failures > 0 {
		return 1
	}
	return 0
}

func selectJobs(cfg config.Config, jobName string) ([]config.JobConfig, error) {
	if jobName == "" {
		return cfg.Jobs, nil
	}
	for _, job := range cfg.Jobs {
		if job.Name == jobName {
			return []config.JobConfig{job}, nil
		}
	}
	return nil, fmt.Errorf("restore_run=job_not_found job=%s", jobName)
}

func printAvailableJobs(cfg config.Config) {
	fmt.Fprintln(os.Stderr, "Available jobs:")
	for _, job := range cfg.Jobs {
		fmt.Fprintf(os.Stderr, "  - %s\n", job.Name)
	}
}
