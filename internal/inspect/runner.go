package inspect

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"db-restore-automation/internal/inspect/common"
	"db-restore-automation/internal/inspect/mysql"
	"db-restore-automation/internal/inspect/oracle"
	"db-restore-automation/internal/inspect/oraclerman"
	"db-restore-automation/internal/inspect/postgres"
	"db-restore-automation/internal/inspect/powerprotect"
)

func runInspection(ctx context.Context, config *common.Config, options Options) (Report, error) {
	hostname, _ := os.Hostname()
	report := Report{
		GeneratedAt:     time.Now(),
		OperatingSystem: runtime.GOOS,
		Hostname:        hostname,
		TestConnection:  options.TestConnection,
	}
	if config != nil {
		report.ConfigPath = config.Path
	}

	jobs, err := selectJobs(config, options)
	if err != nil {
		return Report{}, err
	}

	for _, job := range jobs {
		select {
		case <-ctx.Done():
			return Report{}, ctx.Err()
		default:
		}

		jobInspector, err := inspectorFor(job.Type)
		if err != nil {
			report.Jobs = append(report.Jobs, JobReport{
				Name:    job.Name,
				Type:    job.Type,
				Enabled: job.Enabled,
				Checks: []Check{{
					Status:  StatusFail,
					Name:    "provider",
					Message: err.Error(),
				}},
			})
			continue
		}

		jobContext, cancel := context.WithTimeout(ctx, options.Timeout)
		jobReport := jobInspector.Inspect(jobContext, common.Request{
			Config:            config,
			Job:               job,
			Options:           options,
			ConnectionTimeout: options.Timeout,
		})
		cancel()
		jobReport.SortCandidates()
		report.Jobs = append(report.Jobs, jobReport)
	}

	report.RecalculateSummary()
	return report, nil
}

func selectJobs(config *common.Config, options Options) ([]common.Job, error) {
	if options.Discover {
		var jobs []common.Job
		for _, providerType := range common.SupportedProviderTypes() {
			if options.ProviderType != "" && common.NormalizeProviderType(options.ProviderType) != providerType {
				continue
			}
			jobs = append(jobs, common.Job{
				Name:    "discover-" + providerType,
				Type:    providerType,
				Enabled: true,
				Data:    map[string]any{},
			})
		}
		if len(jobs) == 0 {
			return nil, fmt.Errorf("no provider matched --type %q", options.ProviderType)
		}
		return jobs, nil
	}

	if config == nil {
		return nil, fmt.Errorf("configuration is required unless --discover is used")
	}

	var jobs []common.Job
	for _, job := range config.Jobs {
		if !options.IncludeDisabled && !job.Enabled {
			continue
		}
		if options.JobName != "" && !strings.EqualFold(strings.TrimSpace(options.JobName), job.Name) {
			continue
		}
		if options.ProviderType != "" && common.NormalizeProviderType(options.ProviderType) != job.Type {
			continue
		}
		jobs = append(jobs, job)
	}

	if len(jobs) == 0 {
		switch {
		case options.JobName != "":
			return nil, fmt.Errorf("job %q was not found or is disabled", options.JobName)
		case options.ProviderType != "":
			return nil, fmt.Errorf("no enabled jobs found for provider type %q", options.ProviderType)
		default:
			return nil, fmt.Errorf("no enabled jobs were found")
		}
	}

	sort.SliceStable(jobs, func(a, b int) bool {
		return strings.ToLower(jobs[a].Name) < strings.ToLower(jobs[b].Name)
	})
	return jobs, nil
}

func inspectorFor(providerType string) (common.Inspector, error) {
	switch common.NormalizeProviderType(providerType) {
	case "postgres":
		return postgres.Inspector{}, nil
	case "mysql":
		return mysql.Inspector{}, nil
	case "oracle":
		return oracle.Inspector{}, nil
	case "oracle_rman":
		return oraclerman.Inspector{}, nil
	case "mssql_powerprotect":
		return powerprotect.Inspector{}, nil
	default:
		return nil, fmt.Errorf("%s", common.UnsupportedProviderMessage(providerType))
	}
}
