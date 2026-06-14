package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"db-restore-automation/internal/config"
)

type SlackNotifier struct {
	Config config.SlackConfig
}

func (n SlackNotifier) Notify(ctx context.Context, event Event) error {
	url := os.Getenv(n.Config.WebhookURLEnv)
	if url == "" {
		return fmt.Errorf("Slack webhook environment variable is empty: %s", n.Config.WebhookURLEnv)
	}
	payload := map[string]string{
		"text": formatEvent(event),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("Slack webhook returned status %s", resp.Status)
	}
	return nil
}

func formatEvent(event Event) string {
	text := fmt.Sprintf("db restore %s\njob=%s type=%s source=%s target=%s dry_run=%v started=%s finished=%s duration=%s host=%s main_log=%s",
		event.Result,
		event.JobName,
		event.JobType,
		event.Source,
		event.Target,
		event.DryRun,
		event.StartedAt.Format(time.RFC3339),
		event.FinishedAt.Format(time.RFC3339),
		event.Duration.Round(time.Second),
		event.Host,
		event.MainLogFile,
	)
	if event.ProviderLog != "" {
		text += " provider_log=" + event.ProviderLog
	}
	if event.Error != "" {
		text += " error=" + event.Error
	}
	return text
}
