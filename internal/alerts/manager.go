package alerts

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
)

const alertNotifierTimeout = 30 * time.Second

type Manager struct {
	logger    *logging.Logger
	cfg       config.AlertsConfig
	notifiers []Notifier
}

func NewManager(
	cfg config.AlertsConfig,
	logger *logging.Logger,
) Manager {
	manager := Manager{
		cfg:    cfg,
		logger: logger,
	}

	if !cfg.Enabled {
		return manager
	}

	if cfg.Slack.Enabled {
		manager.notifiers = append(
			manager.notifiers,
			SlackNotifier{
				Config: cfg.Slack,
			},
		)
	}

	if cfg.Email.Enabled {
		manager.notifiers = append(
			manager.notifiers,
			EmailNotifier{
				Config: cfg.Email,
			},
		)
	}

	return manager
}

func (m Manager) Notify(
	ctx context.Context,
	event Event,
) {
	if !m.cfg.Enabled {
		return
	}

	if ctx == nil {
		ctx = context.Background()
	}

	result := strings.ToLower(
		strings.TrimSpace(event.Result),
	)

	if !m.shouldNotify(event, result) {
		return
	}

	if len(m.notifiers) == 0 {
		m.logWarn(fmt.Sprintf(
			"alert=skipped reason=no_enabled_notifiers job=%s result=%s dry_run=%t",
			strings.TrimSpace(event.JobName),
			result,
			event.DryRun,
		))

		return
	}

	for _, notifier := range m.notifiers {
		if notifierIsNil(notifier) {
			m.logWarn(fmt.Sprintf(
				"alert=failure notifier=nil job=%s result=%s dry_run=%t error=no notifier implementation",
				strings.TrimSpace(event.JobName),
				result,
				event.DryRun,
			))

			continue
		}

		// Restore execution may have ended because its context was cancelled.
		// Alerts still need an opportunity to report that failure, so remove
		// cancellation from the operation context while retaining its values.
		notifierContext, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			alertNotifierTimeout,
		)

		err := notifySafely(
			notifierContext,
			notifier,
			event,
		)

		cancel()

		if err != nil {
			m.logWarn(fmt.Sprintf(
				"alert=failure notifier=%T job=%s result=%s dry_run=%t error=%s",
				notifier,
				strings.TrimSpace(event.JobName),
				result,
				event.DryRun,
				err.Error(),
			))

			continue
		}

		m.logInfo(fmt.Sprintf(
			"alert=success notifier=%T job=%s result=%s dry_run=%t",
			notifier,
			strings.TrimSpace(event.JobName),
			result,
			event.DryRun,
		))
	}
}

func (m Manager) shouldNotify(
	event Event,
	result string,
) bool {
	// Dry-run notifications are controlled only by notify_on.dry_run.
	// They must not also require notify_on.success or notify_on.failure.
	if event.DryRun {
		return m.cfg.NotifyOn.DryRun
	}

	switch result {
	case "success":
		return m.cfg.NotifyOn.Success

	case "failure":
		return m.cfg.NotifyOn.Failure

	default:
		m.logWarn(fmt.Sprintf(
			"alert=skipped reason=unsupported_result job=%s result=%s dry_run=false",
			strings.TrimSpace(event.JobName),
			result,
		))

		return false
	}
}

func notifySafely(
	ctx context.Context,
	notifier Notifier,
	event Event,
) (notifyErr error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}

		notifyErr = fmt.Errorf(
			"notifier panic: %v",
			recovered,
		)
	}()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"notification context unavailable before send: %w",
			err,
		)
	}

	if err := notifier.Notify(ctx, event); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return fmt.Errorf(
				"notification timed out or was cancelled: %w",
				contextErr,
			)
		}

		return err
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"notification context ended after send: %w",
			err,
		)
	}

	return nil
}

func notifierIsNil(
	notifier Notifier,
) bool {
	if notifier == nil {
		return true
	}

	value := reflect.ValueOf(notifier)

	switch value.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice:
		return value.IsNil()

	default:
		return false
	}
}

func (m Manager) logInfo(
	message string,
) {
	if m.logger != nil {
		m.logger.Info(message)
	}
}

func (m Manager) logWarn(
	message string,
) {
	if m.logger != nil {
		m.logger.Warn(message)
	}
}

