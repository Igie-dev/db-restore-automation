package config

import (
	"strings"
	"time"
)

const (
	TypePostgres          = "postgres"
	TypeMySQL             = "mysql"
	TypeOracle            = "oracle"
	TypeOracleRMAN        = "oracle_rman"
	TypeMSSQLPowerProtect = "mssql_powerprotect"

	DefaultRMANScope       = "full_database"
	DefaultPowerProtectRun = "normal"

	DefaultPostgresCredentialMethod    = "pgpass"
	DefaultMySQLCredentialMethod       = "defaults_file"
	DefaultOracleCredentialMethod      = "oracle_wallet"
	DefaultOracleRMANCredentialMethod  = "os_auth"
	DefaultPowerProtectCredentialMethod = "lockbox"
)

type Config struct {
	Tools  ToolsConfig  `yaml:"tools"`
	Alerts AlertsConfig `yaml:"alerts"`
	Jobs   []JobConfig  `yaml:"jobs"`
}

type ToolsConfig struct {
	Postgres          PostgresToolsConfig          `yaml:"postgres"`
	MySQL             MySQLToolsConfig             `yaml:"mysql"`
	Oracle            OracleToolsConfig            `yaml:"oracle"`
	OracleRMAN        OracleRMANToolsConfig        `yaml:"oracle_rman"`
	MSSQLPowerProtect MSSQLPowerProtectToolsConfig `yaml:"mssql_powerprotect"`
}

type PostgresToolsConfig struct {
	PSQL      string `yaml:"psql"`
	PGRestore string `yaml:"pg_restore"`
	DropDB    string `yaml:"dropdb"`
	CreateDB  string `yaml:"createdb"`
}

type MySQLToolsConfig struct {
	MySQL string `yaml:"mysql"`
}

type OracleToolsConfig struct {
	ImpDP string `yaml:"impdp"`
}

type OracleRMANToolsConfig struct {
	RMAN string `yaml:"rman"`
}

type MSSQLPowerProtectToolsConfig struct {
	DDBMSSQLRC string `yaml:"ddbmsqlrc"`
}

type AlertsConfig struct {
	Enabled  bool           `yaml:"enabled"`
	NotifyOn NotifyOnConfig `yaml:"notify_on"`
	Slack    SlackConfig    `yaml:"slack"`
	Email    EmailConfig    `yaml:"email"`
}

type NotifyOnConfig struct {
	Success bool `yaml:"success"`
	Failure bool `yaml:"failure"`
	DryRun  bool `yaml:"dry_run"`
}

type SlackConfig struct {
	Enabled       bool   `yaml:"enabled"`
	WebhookURLEnv string `yaml:"webhook_url_env"`
}

type EmailConfig struct {
	Enabled     bool     `yaml:"enabled"`
	SMTPHost    string   `yaml:"smtp_host"`
	SMTPPort    int      `yaml:"smtp_port"`
	UsernameEnv string   `yaml:"username_env"`
	PasswordEnv string   `yaml:"password_env"`
	FromEnv     string   `yaml:"from_env"`
	To          []string `yaml:"to"`
}

type JobConfig struct {
	Name         string             `yaml:"name"`
	Enabled      *bool              `yaml:"enabled"`
	Type         string             `yaml:"type"`
	BackupPath   string             `yaml:"backup_path"`
	FilePattern  string             `yaml:"file_pattern"`
	Timeout      string             `yaml:"timeout"`
	Schedule     ScheduleConfig     `yaml:"schedule"`
	Target       TargetConfig       `yaml:"target"`
	Source       SourceConfig       `yaml:"source"`
	PowerProtect PowerProtectConfig `yaml:"powerprotect"`
	RMAN         RMANConfig         `yaml:"rman"`
	Relocate     []RelocateConfig   `yaml:"relocate"`
	Safety       SafetyConfig       `yaml:"safety"`
	Tools        ToolsConfig        `yaml:"tools"`
}

type ScheduleConfig struct {
	Enabled          *bool  `yaml:"enabled"`
	LinuxCron        string `yaml:"linux_cron"`
	WindowsTime      string `yaml:"windows_time"`
	WindowsFrequency string `yaml:"windows_frequency"`
	// DayOfMonth is the day (1-31) a MONTHLY Windows schedule runs on. It is
	// ignored for DAILY schedules and for Linux, which uses linux_cron.
	DayOfMonth int `yaml:"day_of_month"`
}

type TargetConfig struct {
	Host                string `yaml:"host"`
	Port                int    `yaml:"port"`
	Database            string `yaml:"database"`
	Username            string `yaml:"username"`
	CredentialMethod    string `yaml:"credential_method"`
	MaintenanceDatabase string `yaml:"maintenance_database"`
	ConnectString       string `yaml:"connect_string"`
	Schema              string `yaml:"schema"`
	OracleDirectory     string `yaml:"oracle_directory"`
	LoginPath           string `yaml:"login_path"`
	DefaultsFile        string `yaml:"defaults_file"`
}

type SourceConfig struct {
	Database string `yaml:"database"`
}

type PowerProtectConfig struct {
	DDHost               string `yaml:"dd_host"`
	DDUser               string `yaml:"dd_user"`
	DevicePath           string `yaml:"device_path"`
	LockboxPath          string `yaml:"lockbox_path"`
	Client               string `yaml:"client"`
	SkipClientResolution *bool  `yaml:"skip_client_resolution"`
	RestoreType          string `yaml:"restore_type"`
	CredentialMethod     string `yaml:"credential_method"`
}

type RMANConfig struct {
	Target           string `yaml:"target"`
	Catalog          string `yaml:"catalog"`
	CommandFile      string `yaml:"command_file"`
	LogFile          string `yaml:"log_file"`
	CredentialMethod string `yaml:"credential_method"`
	OracleHome       string `yaml:"oracle_home"`
	OracleSID        string `yaml:"oracle_sid"`
	RestoreScope     string `yaml:"restore_scope"`
}

type RelocateConfig struct {
	LogicalName  string `yaml:"logical_name"`
	PhysicalPath string `yaml:"physical_path"`
}

type SafetyConfig struct {
	RequireConfirmation *bool    `yaml:"require_confirmation"`
	BlockIfNameContains []string `yaml:"block_if_name_contains"`
}

func (j JobConfig) TypeName() string {
	return normalizeToken(j.Type)
}

func (j JobConfig) IsEnabled() bool {
	return j.Enabled != nil && *j.Enabled
}

// JobTimeout returns the parsed per-job timeout and true when a timeout is
// configured. Returns 0 and false when the timeout field is empty.
func (j JobConfig) JobTimeout() (time.Duration, bool) {
	raw := strings.TrimSpace(j.Timeout)
	if raw == "" {
		return 0, false
	}

	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, false
	}

	return d, true
}

func (j JobConfig) ScheduleEnabled() bool {
	return j.Schedule.Enabled != nil && *j.Schedule.Enabled
}

func (j JobConfig) UsesBackupFile() bool {
	switch j.TypeName() {
	case TypePostgres, TypeMySQL, TypeOracle:
		return true

	case TypeOracleRMAN, TypeMSSQLPowerProtect:
		return false

	default:
		// Unknown job types must not cause the application to inspect
		// arbitrary backup paths before configuration validation rejects them.
		return false
	}
}

func (j JobConfig) CredentialMethod() string {
	switch j.TypeName() {
	case TypePostgres:
		return normalizeTokenWithDefault(
			j.Target.CredentialMethod,
			DefaultPostgresCredentialMethod,
		)

	case TypeMySQL:
		return normalizeTokenWithDefault(
			j.Target.CredentialMethod,
			DefaultMySQLCredentialMethod,
		)

	case TypeOracle:
		return normalizeTokenWithDefault(
			j.Target.CredentialMethod,
			DefaultOracleCredentialMethod,
		)

	case TypeOracleRMAN:
		return normalizeTokenWithDefault(
			j.RMAN.CredentialMethod,
			DefaultOracleRMANCredentialMethod,
		)

	case TypeMSSQLPowerProtect:
		return normalizeTokenWithDefault(
			j.PowerProtect.CredentialMethod,
			DefaultPowerProtectCredentialMethod,
		)

	default:
		return ""
	}
}

func (j JobConfig) RestoreScope() string {
	return normalizeTokenWithDefault(
		j.RMAN.RestoreScope,
		DefaultRMANScope,
	)
}

func (j JobConfig) PowerProtectRestoreType() string {
	return normalizeTokenWithDefault(
		j.PowerProtect.RestoreType,
		DefaultPowerProtectRun,
	)
}

func (j JobConfig) PowerProtectSkipClientResolution() bool {
	// Skipping client-name resolution weakens validation and must therefore
	// only happen when it is explicitly enabled in the configuration.
	return j.PowerProtect.SkipClientResolution != nil &&
		*j.PowerProtect.SkipClientResolution
}

func (j JobConfig) RequiresConfirmation() bool {
	// Destructive restores should require confirmation unless the
	// configuration explicitly disables it.
	if j.Safety.RequireConfirmation == nil {
		return true
	}

	return *j.Safety.RequireConfirmation
}

func (j JobConfig) SourceText() string {
	switch j.TypeName() {
	case TypeMSSQLPowerProtect:
		return strings.TrimSpace(j.Source.Database)

	case TypeOracleRMAN:
		return strings.TrimSpace(j.RMAN.Target)

	case TypePostgres, TypeMySQL, TypeOracle:
		return "backup_file"

	default:
		return ""
	}
}

func (j JobConfig) TargetText() string {
	switch j.TypeName() {
	case TypePostgres, TypeMySQL, TypeMSSQLPowerProtect:
		return strings.TrimSpace(j.Target.Database)

	case TypeOracle:
		return joinNonEmpty(
			j.Target.Schema,
			j.Target.ConnectString,
		)

	case TypeOracleRMAN:
		return strings.TrimSpace(j.RMAN.Target)

	default:
		return ""
	}
}

func (c Config) ToolPath(
	job JobConfig,
	category string,
	name string,
	fallback string,
) string {
	if value := strings.TrimSpace(
		toolPath(job.Tools, category, name),
	); value != "" {
		return value
	}

	if value := strings.TrimSpace(
		toolPath(c.Tools, category, name),
	); value != "" {
		return value
	}

	return strings.TrimSpace(fallback)
}

func toolPath(
	tools ToolsConfig,
	category string,
	name string,
) string {
	category = normalizeToken(category)
	name = normalizeToken(name)

	switch category {
	case TypePostgres:
		switch name {
		case "psql":
			return strings.TrimSpace(tools.Postgres.PSQL)

		case "pg_restore":
			return strings.TrimSpace(tools.Postgres.PGRestore)

		case "dropdb":
			return strings.TrimSpace(tools.Postgres.DropDB)

		case "createdb":
			return strings.TrimSpace(tools.Postgres.CreateDB)
		}

	case TypeMySQL:
		if name == "mysql" {
			return strings.TrimSpace(tools.MySQL.MySQL)
		}

	case TypeOracle:
		if name == "impdp" {
			return strings.TrimSpace(tools.Oracle.ImpDP)
		}

	case TypeOracleRMAN:
		if name == "rman" {
			return strings.TrimSpace(tools.OracleRMAN.RMAN)
		}

	case TypeMSSQLPowerProtect:
		if name == "ddbmsqlrc" {
			return strings.TrimSpace(
				tools.MSSQLPowerProtect.DDBMSSQLRC,
			)
		}
	}

	return ""
}

func normalizeToken(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeTokenWithDefault(
	value string,
	fallback string,
) string {
	value = normalizeToken(value)
	if value != "" {
		return value
	}

	return normalizeToken(fallback)
}

func joinNonEmpty(values ...string) string {
	parts := make([]string, 0, len(values))

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, value)
		}
	}

	return strings.Join(parts, " ")
}

