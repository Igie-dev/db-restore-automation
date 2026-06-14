package config

import "strings"

const (
	TypePostgres           = "postgres"
	TypeMySQL              = "mysql"
	TypeOracle             = "oracle"
	TypeOracleRMAN         = "oracle_rman"
	TypeMSSQLPowerProtect  = "mssql_powerprotect"
	DefaultRMANScope       = "full_database"
	DefaultPowerProtectRun = "normal"
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
	Enabled  bool              `yaml:"enabled"`
	NotifyOn NotifyOnConfig    `yaml:"notify_on"`
	Slack    SlackConfig       `yaml:"slack"`
	Email    EmailConfig       `yaml:"email"`
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
	Name        string              `yaml:"name"`
	Enabled     *bool               `yaml:"enabled"`
	Type        string              `yaml:"type"`
	BackupPath  string              `yaml:"backup_path"`
	FilePattern string              `yaml:"file_pattern"`
	Schedule    ScheduleConfig      `yaml:"schedule"`
	Target      TargetConfig        `yaml:"target"`
	Source      SourceConfig        `yaml:"source"`
	PowerProtect PowerProtectConfig `yaml:"powerprotect"`
	RMAN        RMANConfig          `yaml:"rman"`
	Relocate    []RelocateConfig    `yaml:"relocate"`
	Safety      SafetyConfig        `yaml:"safety"`
	Tools       ToolsConfig         `yaml:"tools"`
}

type ScheduleConfig struct {
	Enabled          *bool  `yaml:"enabled"`
	LinuxCron        string `yaml:"linux_cron"`
	WindowsTime      string `yaml:"windows_time"`
	WindowsFrequency string `yaml:"windows_frequency"`
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
	return strings.ToLower(strings.TrimSpace(j.Type))
}

func (j JobConfig) IsEnabled() bool {
	return j.Enabled != nil && *j.Enabled
}

func (j JobConfig) ScheduleEnabled() bool {
	return j.Schedule.Enabled != nil && *j.Schedule.Enabled
}

func (j JobConfig) UsesBackupFile() bool {
	switch j.TypeName() {
	case TypeOracleRMAN, TypeMSSQLPowerProtect:
		return false
	default:
		return true
	}
}

func (j JobConfig) CredentialMethod() string {
	switch j.TypeName() {
	case TypePostgres:
		return defaultString(j.Target.CredentialMethod, "pgpass")
	case TypeMySQL:
		return defaultString(j.Target.CredentialMethod, "defaults_file")
	case TypeOracle:
		return defaultString(j.Target.CredentialMethod, "oracle_wallet")
	case TypeOracleRMAN:
		return defaultString(j.RMAN.CredentialMethod, "os_auth")
	case TypeMSSQLPowerProtect:
		return defaultString(j.PowerProtect.CredentialMethod, "lockbox")
	default:
		return ""
	}
}

func (j JobConfig) RestoreScope() string {
	return defaultString(j.RMAN.RestoreScope, DefaultRMANScope)
}

func (j JobConfig) PowerProtectRestoreType() string {
	return defaultString(j.PowerProtect.RestoreType, DefaultPowerProtectRun)
}

func (j JobConfig) PowerProtectSkipClientResolution() bool {
	if j.PowerProtect.SkipClientResolution == nil {
		return true
	}
	return *j.PowerProtect.SkipClientResolution
}

func (j JobConfig) SourceText() string {
	switch j.TypeName() {
	case TypeMSSQLPowerProtect:
		return j.Source.Database
	case TypeOracleRMAN:
		return j.RMAN.Target
	default:
		return "backup_file"
	}
}

func (j JobConfig) TargetText() string {
	switch j.TypeName() {
	case TypePostgres, TypeMySQL, TypeMSSQLPowerProtect:
		return j.Target.Database
	case TypeOracle:
		return strings.TrimSpace(j.Target.Schema + " " + j.Target.ConnectString)
	case TypeOracleRMAN:
		return j.RMAN.Target
	default:
		return ""
	}
}

func (c Config) ToolPath(job JobConfig, category, name, fallback string) string {
	if v := toolPath(job.Tools, category, name); strings.TrimSpace(v) != "" {
		return v
	}
	if v := toolPath(c.Tools, category, name); strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func toolPath(tools ToolsConfig, category, name string) string {
	switch category {
	case "postgres":
		switch name {
		case "psql":
			return tools.Postgres.PSQL
		case "pg_restore":
			return tools.Postgres.PGRestore
		case "dropdb":
			return tools.Postgres.DropDB
		case "createdb":
			return tools.Postgres.CreateDB
		}
	case "mysql":
		if name == "mysql" {
			return tools.MySQL.MySQL
		}
	case "oracle":
		if name == "impdp" {
			return tools.Oracle.ImpDP
		}
	case "oracle_rman":
		if name == "rman" {
			return tools.OracleRMAN.RMAN
		}
	case "mssql_powerprotect":
		if name == "ddbmsqlrc" {
			return tools.MSSQLPowerProtect.DDBMSSQLRC
		}
	}
	return ""
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
