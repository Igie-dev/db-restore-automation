package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
	"db-restore-automation/internal/safety"
)

func RunValidate(ctx context.Context, configPath string) int {
	if ctx == nil {
		ctx = context.Background()
	}

	configPath = strings.TrimSpace(configPath)

	logger, err := newLogger()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if configPath == "" {
		logger.Error(
			"config_validation=end result=failure reason=config_path_required",
		)
		return 2
	}

	logger.Info(fmt.Sprintf(
		"config_validation=start config=%s",
		validationLogValue(configPath),
	))

	if err := ctx.Err(); err != nil {
		logger.Error(fmt.Sprintf(
			"config_validation=end result=failure reason=context_cancelled error=%s",
			validationLogValue(err.Error()),
		))
		return 1
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error(fmt.Sprintf(
			"config_validation=end result=failure reason=config_load_failed config=%s error=%s",
			validationLogValue(configPath),
			validationLogValue(err.Error()),
		))
		return 2
	}

	if err := ctx.Err(); err != nil {
		logger.Error(fmt.Sprintf(
			"config_validation=end result=failure reason=context_cancelled error=%s",
			validationLogValue(err.Error()),
		))
		return 1
	}

	if err := config.Validate(cfg); err != nil {
		logger.Error(fmt.Sprintf(
			"config_validation=end result=failure reason=config_invalid config=%s error=%s",
			validationLogValue(configPath),
			validationLogValue(err.Error()),
		))
		return 1
	}

	checker := safety.Checker{
		Logger: logger,
	}

	totalJobs := len(cfg.Jobs)
	enabledJobs := 0
	disabledJobs := 0
	validatedJobs := 0
	failures := 0

	for _, job := range cfg.Jobs {
		if err := ctx.Err(); err != nil {
			logger.Error(fmt.Sprintf(
				"config_validation=end result=failure reason=context_cancelled total_jobs=%d enabled_jobs=%d validated_jobs=%d failures=%d error=%s",
				totalJobs,
				enabledJobs,
				validatedJobs,
				failures,
				validationLogValue(err.Error()),
			))
			return 1
		}

		jobName := strings.TrimSpace(job.Name)
		jobType := strings.TrimSpace(job.TypeName())

		if !job.IsEnabled() {
			disabledJobs++

			logger.Info(fmt.Sprintf(
				"job=%s type=%s enabled=false validation=skipped",
				validationLogValue(jobName),
				validationLogValue(jobType),
			))
			continue
		}

		enabledJobs++
		validatedJobs++

		if err := checker.Validate(job); err != nil {
			failures++

			logger.Error(fmt.Sprintf(
				"job=%s type=%s validation=failure reason=safety_validation_failed error=%s",
				validationLogValue(jobName),
				validationLogValue(jobType),
				validationLogValue(err.Error()),
			))
			continue
		}

		logger.Info(fmt.Sprintf(
			"job=%s type=%s validation=success",
			validationLogValue(jobName),
			validationLogValue(jobType),
		))
	}

	if err := ctx.Err(); err != nil {
		logger.Error(fmt.Sprintf(
			"config_validation=end result=failure reason=context_cancelled total_jobs=%d enabled_jobs=%d disabled_jobs=%d validated_jobs=%d failures=%d error=%s",
			totalJobs,
			enabledJobs,
			disabledJobs,
			validatedJobs,
			failures,
			validationLogValue(err.Error()),
		))
		return 1
	}

	if failures > 0 {
		logger.Error(fmt.Sprintf(
			"config_validation=end result=failure total_jobs=%d enabled_jobs=%d disabled_jobs=%d validated_jobs=%d failures=%d",
			totalJobs,
			enabledJobs,
			disabledJobs,
			validatedJobs,
			failures,
		))
		return 1
	}

	if enabledJobs == 0 {
		logger.Warn(fmt.Sprintf(
			"config_validation=no_enabled_jobs total_jobs=%d disabled_jobs=%d",
			totalJobs,
			disabledJobs,
		))
	}

	logger.Success(fmt.Sprintf(
		"config_validation=end result=success total_jobs=%d enabled_jobs=%d disabled_jobs=%d validated_jobs=%d failures=0",
		totalJobs,
		enabledJobs,
		disabledJobs,
		validatedJobs,
	))

	return 0
}

func newLogger() (*logging.Logger, error) {
	root, err := os.Getwd()
	if err != nil {
		executablePath, executableErr := os.Executable()
		if executableErr != nil {
			return nil, fmt.Errorf(
				"logger_initialization_failed: get working directory: %w; get executable path: %v",
				err,
				executableErr,
			)
		}

		root = filepath.Dir(executablePath)
	}

	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf(
			"logger_initialization_failed: resolved application root is empty",
		)
	}

	logger, err := logging.New(root)
	if err != nil {
		return nil, fmt.Errorf(
			"logger_initialization_failed root=%s error=%w",
			validationLogValue(root),
			err,
		)
	}

	return logger, nil
}

func validationLogValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")

	return value
}