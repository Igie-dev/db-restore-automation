package alerts

import (
	"context"
	"fmt"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
)

type Manager struct {
	logger    *logging.Logger
	cfg       config.AlertsConfig
	notifiers []Notifier
}

func NewManager(cfg config.AlertsConfig, logger *logging.Logger) Manager {
	m := Manager{cfg: cfg, logger: logger}
	if !cfg.Enabled {
		return m
	}
	if cfg.Slack.Enabled {
		m.notifiers = append(m.notifiers, SlackNotifier{Config: cfg.Slack})
	}
	if cfg.Email.Enabled {
		m.notifiers = append(m.notifiers, EmailNotifier{Config: cfg.Email})
	}
	return m
}

func (m Manager) Notify(ctx context.Context, event Event) {
	if !m.cfg.Enabled {
		return
	}
	if event.DryRun && !m.cfg.NotifyOn.DryRun {
		return
	}
	if event.Result == "success" && !m.cfg.NotifyOn.Success {
		return
	}
	if event.Result == "failure" && !m.cfg.NotifyOn.Failure {
		return
	}
	for _, notifier := range m.notifiers {
		if err := notifier.Notify(ctx, event); err != nil && m.logger != nil {
			m.logger.Warn(fmt.Sprintf("alert=failure notifier=%T job=%s error=%s", notifier, event.JobName, err.Error()))
		}
	}
}
