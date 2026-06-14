package alerts

import (
	"context"
	"time"
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

type Notifier interface {
	Notify(ctx context.Context, event Event) error
}
