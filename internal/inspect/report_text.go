package inspect

import (
	"fmt"
	"io"
	"strings"

	"db-restore-automation/internal/inspect/common"
)

func writeTextReport(writer io.Writer, report Report) error {
	if _, err := fmt.Fprintln(writer, "Database restore environment inspection"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer, strings.Repeat("=", 39)); err != nil {
		return err
	}
	fmt.Fprintf(writer, "Generated : %s\n", report.GeneratedAt.Format("2006-01-02 15:04:05 -07:00"))
	fmt.Fprintf(writer, "OS        : %s\n", report.OperatingSystem)
	fmt.Fprintf(writer, "Hostname  : %s\n", report.Hostname)
	if report.ConfigPath != "" {
		fmt.Fprintf(writer, "Config    : %s\n", report.ConfigPath)
	}
	fmt.Fprintf(writer, "Connection tests: %t\n", report.TestConnection)

	for _, job := range report.Jobs {
		fmt.Fprintln(writer)
		fmt.Fprintf(writer, "Job: %s\n", job.Name)
		fmt.Fprintf(writer, "Type: %s (%s)\n", job.Type, common.DisplayProviderName(job.Type))
		fmt.Fprintf(writer, "Enabled: %t\n", job.Enabled)
		fmt.Fprintln(writer, strings.Repeat("-", 72))
		for _, check := range job.Checks {
			status := strings.ToUpper(string(check.Status))
			fmt.Fprintf(writer, "[%-4s] %s", status, check.Name)
			if check.Message != "" {
				fmt.Fprintf(writer, ": %s", check.Message)
			}
			fmt.Fprintln(writer)
			if check.Value != "" {
				fmt.Fprintf(writer, "       Value : %s\n", check.Value)
			}
			if check.Source != "" {
				fmt.Fprintf(writer, "       Source: %s\n", check.Source)
			}
		}

		if len(job.Candidates) > 0 {
			fmt.Fprintln(writer)
			fmt.Fprintln(writer, "Discovered candidates:")
			for _, candidate := range job.Candidates {
				fmt.Fprintf(writer, "  %-12s %s\n", candidate.Kind+":", candidate.Value)
				if candidate.Source != "" {
					fmt.Fprintf(writer, "               source: %s\n", candidate.Source)
				}
			}
		}
	}

	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Summary")
	fmt.Fprintln(writer, strings.Repeat("-", 72))
	fmt.Fprintf(writer, "PASS=%d WARN=%d FAIL=%d INFO=%d\n", report.Summary.Pass, report.Summary.Warn, report.Summary.Fail, report.Summary.Info)
	return nil
}
