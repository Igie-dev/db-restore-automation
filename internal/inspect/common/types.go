package common

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
	StatusInfo Status = "info"
)

const (
	ExitOK       = 0
	ExitWarnings = 1
	ExitFailure  = 2
	ExitUsage    = 3
)

type Check struct {
	Status  Status `json:"status"`
	Name    string `json:"name"`
	Message string `json:"message,omitempty"`
	Value   string `json:"value,omitempty"`
	Source  string `json:"source,omitempty"`
}

type Candidate struct {
	Kind         string    `json:"kind"`
	Value        string    `json:"value"`
	Source       string    `json:"source,omitempty"`
	ModifiedTime time.Time `json:"modified_time,omitempty"`
}

type JobReport struct {
	Name       string      `json:"name"`
	Type       string      `json:"type"`
	Enabled    bool        `json:"enabled"`
	Checks     []Check     `json:"checks"`
	Candidates []Candidate `json:"candidates,omitempty"`
}

func (r *JobReport) Add(status Status, name, message, value string) {
	r.Checks = append(r.Checks, Check{
		Status:  status,
		Name:    strings.TrimSpace(name),
		Message: strings.TrimSpace(message),
		Value:   Redact(strings.TrimSpace(value)),
	})
}

func (r *JobReport) Pass(name, message, value string) { r.Add(StatusPass, name, message, value) }
func (r *JobReport) Warn(name, message, value string) { r.Add(StatusWarn, name, message, value) }
func (r *JobReport) Fail(name, message, value string) { r.Add(StatusFail, name, message, value) }
func (r *JobReport) Info(name, message, value string) { r.Add(StatusInfo, name, message, value) }

func (r *JobReport) SortCandidates() {
	sort.SliceStable(r.Candidates, func(i, j int) bool {
		if r.Candidates[i].Kind != r.Candidates[j].Kind {
			return r.Candidates[i].Kind < r.Candidates[j].Kind
		}
		if !r.Candidates[i].ModifiedTime.Equal(r.Candidates[j].ModifiedTime) {
			return r.Candidates[i].ModifiedTime.After(r.Candidates[j].ModifiedTime)
		}
		if r.Candidates[i].Value != r.Candidates[j].Value {
			return r.Candidates[i].Value < r.Candidates[j].Value
		}
		return r.Candidates[i].Source < r.Candidates[j].Source
	})
}

type Summary struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
	Info int `json:"info"`
}

type Report struct {
	GeneratedAt     time.Time   `json:"generated_at"`
	ConfigPath      string      `json:"config_path,omitempty"`
	OperatingSystem string      `json:"operating_system"`
	Hostname        string      `json:"hostname,omitempty"`
	TestConnection  bool        `json:"test_connection"`
	Jobs            []JobReport `json:"jobs"`
	Summary         Summary     `json:"summary"`
}

func (r *Report) RecalculateSummary() {
	r.Summary = Summary{}
	for _, job := range r.Jobs {
		for _, check := range job.Checks {
			switch check.Status {
			case StatusPass:
				r.Summary.Pass++
			case StatusWarn:
				r.Summary.Warn++
			case StatusFail:
				r.Summary.Fail++
			case StatusInfo:
				r.Summary.Info++
			}
		}
	}
}

func (r Report) ExitCode() int {
	if r.Summary.Fail > 0 {
		return ExitFailure
	}
	if r.Summary.Warn > 0 {
		return ExitWarnings
	}
	return ExitOK
}

type Options struct {
	ConfigPath      string
	JobName         string
	ProviderType    string
	Format          string
	Discover        bool
	IncludeDisabled bool
	TestConnection  bool
	Timeout         time.Duration
	MaxScanFileSize int64
	MaxScanMatches  int
}

func DefaultOptions() Options {
	return Options{
		Format:          "text",
		Timeout:         30 * time.Second,
		MaxScanFileSize: 20 * 1024 * 1024,
		MaxScanMatches:  500,
	}
}

type Request struct {
	Config            *Config
	Job               Job
	Options           Options
	ConnectionTimeout time.Duration
}

type Inspector interface {
	Type() string
	Inspect(ctx context.Context, request Request) JobReport
}

func NormalizeProviderType(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case "postgresql", "pg":
		return "postgres"
	case "mariadb":
		return "mysql"
	case "oracle_datapump", "oracle_data_pump", "datapump":
		return "oracle"
	case "rman", "oracle-rman":
		return "oracle_rman"
	case "powerprotect", "mssql", "sqlserver_powerprotect", "mssql-powerprotect":
		return "mssql_powerprotect"
	default:
		return normalized
	}
}

func SupportedProviderTypes() []string {
	return []string{"postgres", "mysql", "oracle", "oracle_rman", "mssql_powerprotect"}
}

func IsSupportedProvider(providerType string) bool {
	normalized := NormalizeProviderType(providerType)
	for _, supported := range SupportedProviderTypes() {
		if normalized == supported {
			return true
		}
	}
	return false
}

func UnsupportedProviderMessage(providerType string) string {
	return fmt.Sprintf("unsupported provider type %q; supported values: %s",
		providerType, strings.Join(SupportedProviderTypes(), ", "))
}

func DisplayProviderName(providerType string) string {
	switch NormalizeProviderType(providerType) {
	case "postgres":
		return "PostgreSQL"
	case "mysql":
		return "MySQL/MariaDB"
	case "oracle":
		return "Oracle Data Pump"
	case "oracle_rman":
		return "Oracle RMAN"
	case "mssql_powerprotect":
		return "Dell PowerProtect MSSQL"
	default:
		return providerType
	}
}

func ProviderSectionPaths(providerType string, fieldNames ...string) []string {
	providerType = NormalizeProviderType(providerType)
	sections := []string{providerType}
	switch providerType {
	case "postgres":
		sections = append(sections, "postgresql")
	case "oracle":
		sections = append(sections, "oracle_data_pump", "datapump")
	case "oracle_rman":
		sections = append(sections, "rman")
	case "mssql_powerprotect":
		sections = append(sections, "powerprotect", "mssql")
	}

	var paths []string
	for _, field := range fieldNames {
		paths = append(paths, field)
		for _, section := range sections {
			paths = append(paths, section+"."+field)
		}
	}
	return paths
}
