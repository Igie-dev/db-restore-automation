package alerts

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	ResultSuccess = "success"
	ResultFailure = "failure"
)

type Event struct {
	JobName     string
	JobType     string
	Source      string
	Target      string
	Result      string
	DryRun      bool
	Error       string
	StartedAt   time.Time
	FinishedAt  time.Time
	Duration    time.Duration
	Host        string
	MainLogFile string
	ProviderLog string
}

type EventValidationError struct {
	Errors []string
}

func (e EventValidationError) Error() string {
	if len(e.Errors) == 0 {
		return "alert event validation failed"
	}

	return strings.Join(e.Errors, "; ")
}

type Notifier interface {
	Notify(
		ctx context.Context,
		event Event,
	) error
}

// Normalize returns a copy of the event with predictable text, result,
// timestamp, and duration values.
//
// Both StartedAt and FinishedAt are converted to UTC. When both timestamps
// are available, they are treated as the source of truth for Duration.
func (e Event) Normalize() Event {
	e.JobName = strings.TrimSpace(e.JobName)
	e.JobType = strings.ToLower(
		strings.TrimSpace(e.JobType),
	)

	e.Source = strings.TrimSpace(e.Source)
	e.Target = strings.TrimSpace(e.Target)

	e.Result = strings.ToLower(
		strings.TrimSpace(e.Result),
	)

	e.Error = strings.TrimSpace(e.Error)
	e.Host = strings.TrimSpace(e.Host)
	e.MainLogFile = strings.TrimSpace(e.MainLogFile)
	e.ProviderLog = strings.TrimSpace(e.ProviderLog)

	if !e.StartedAt.IsZero() {
		e.StartedAt = e.StartedAt.UTC()
	}

	if !e.FinishedAt.IsZero() {
		e.FinishedAt = e.FinishedAt.UTC()
	}

	if !e.StartedAt.IsZero() &&
		!e.FinishedAt.IsZero() {
		e.Duration = e.FinishedAt.Sub(e.StartedAt)
	}

	return e
}

// Validate verifies that the event contains enough consistent information
// for notification delivery.
func (e Event) Validate() error {
	e = e.Normalize()

	validationErrors := make([]string, 0)

	if e.JobName == "" {
		validationErrors = append(
			validationErrors,
			"job name is required",
		)
	} else if alertContainsUnsafeControlCharacter(e.JobName) {
		validationErrors = append(
			validationErrors,
			"job name must be a single-line value",
		)
	}

	if e.JobType == "" {
		validationErrors = append(
			validationErrors,
			"job type is required",
		)
	} else if alertContainsUnsafeControlCharacter(e.JobType) {
		validationErrors = append(
			validationErrors,
			"job type must be a single-line value",
		)
	}

	switch e.Result {
	case ResultSuccess:
	case ResultFailure:

	default:
		validationErrors = append(
			validationErrors,
			fmt.Sprintf(
				"unsupported result %q; expected %q or %q",
				e.Result,
				ResultSuccess,
				ResultFailure,
			),
		)
	}

	if e.StartedAt.IsZero() {
		validationErrors = append(
			validationErrors,
			"started time is required",
		)
	}

	if e.FinishedAt.IsZero() {
		validationErrors = append(
			validationErrors,
			"finished time is required",
		)
	}

	if !e.StartedAt.IsZero() &&
		!e.FinishedAt.IsZero() &&
		e.FinishedAt.Before(e.StartedAt) {
		validationErrors = append(
			validationErrors,
			"finished time must not be before started time",
		)
	}

	if e.Duration < 0 {
		validationErrors = append(
			validationErrors,
			"duration must not be negative",
		)
	}

	if e.Result == ResultFailure && e.Error == "" {
		validationErrors = append(
			validationErrors,
			"failure event must include an error message",
		)
	}

	if alertContainsUnsafeControlCharacter(e.Host) {
		validationErrors = append(
			validationErrors,
			"host must be a single-line value",
		)
	}

	if alertContainsUnsafeControlCharacter(e.MainLogFile) {
		validationErrors = append(
			validationErrors,
			"main log file must be a single-line value",
		)
	}

	if alertContainsUnsafeControlCharacter(e.ProviderLog) {
		validationErrors = append(
			validationErrors,
			"provider log must be a single-line value",
		)
	}

	if len(validationErrors) > 0 {
		return EventValidationError{
			Errors: validationErrors,
		}
	}

	return nil
}

// PrepareEvent normalizes and validates an event before it is passed to a
// notifier.
func PrepareEvent(event Event) (Event, error) {
	normalized := event.Normalize()

	if err := normalized.Validate(); err != nil {
		return Event{}, err
	}

	return normalized, nil
}

func (e Event) IsSuccess() bool {
	return strings.EqualFold(
		strings.TrimSpace(e.Result),
		ResultSuccess,
	)
}

func (e Event) IsFailure() bool {
	return strings.EqualFold(
		strings.TrimSpace(e.Result),
		ResultFailure,
	)
}

func (e Event) EffectiveDuration() time.Duration {
	e = e.Normalize()

	if e.Duration < 0 {
		return 0
	}

	return e.Duration
}

func alertContainsUnsafeControlCharacter(
	value string,
) bool {
	return strings.ContainsAny(
		value,
		"\r\n\x00",
	)
}
