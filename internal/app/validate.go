package app

import (
	"context"
	"fmt"
	"os"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
	"db-restore-automation/internal/safety"
)

func RunValidate(ctx context.Context, configPath string) int {
	_ = ctx
	logger, err := newLogger()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	logger.Info(fmt.Sprintf("config_validation=start config=%s", configPath))

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error(fmt.Sprintf("config_validation=result_failure reason=load error=%s", err.Error()))
		return 2
	}
	if err := config.Validate(cfg); err != nil {
		logger.Error(fmt.Sprintf("config_validation=end result=failure error=%s", err.Error()))
		return 1
	}

	enabled := 0
	failures := 0
	checker := safety.Checker{Logger: logger}
	for _, job := range cfg.Jobs {
		if job.IsEnabled() {
			enabled++
			if err := checker.Validate(job); err != nil {
				failures++
				logger.Error(fmt.Sprintf("job=%s type=%s validation=failure reason=safety_blocked", job.Name, job.TypeName()))
			}
		}
	}
	if failures > 0 {
		logger.Error(fmt.Sprintf("config_validation=end result=failure enabled_jobs=%d failures=%d", enabled, failures))
		return 1
	}
	logger.Success(fmt.Sprintf("config_validation=end result=success enabled_jobs=%d", enabled))
	return 0
}

func newLogger() (*logging.Logger, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return logging.New(root)
}
