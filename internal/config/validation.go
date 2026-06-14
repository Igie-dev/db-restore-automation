package config

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var jobNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var windowsTimePattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

type ValidationError struct {
	Errors []string
}

func (e ValidationError) Error() string {
	return strings.Join(e.Errors, "\n")
}

func Validate(cfg Config) error {
	var errors []string

	if len(cfg.Jobs) == 0 {
		return ValidationError{Errors: []string{"config must contain a non-empty top-level jobs array"}}
	}

	seen := map[string]bool{}
	for i, job := range cfg.Jobs {
		label := job.Name
		if strings.TrimSpace(label) == "" {
			label = fmt.Sprintf("index-%d", i)
			errors = append(errors, "job missing required field: name")
		} else if !jobNamePattern.MatchString(label) {
			errors = append(errors, fmt.Sprintf("job=%s name must contain only letters, numbers, underscores, hyphens, and dots", label))
		} else if seen[label] {
			errors = append(errors, fmt.Sprintf("duplicate job name: %s", label))
		}
		seen[label] = true

		if job.Enabled == nil {
			errors = append(errors, fmt.Sprintf("job=%s missing required field: enabled", label))
		}

		if job.Schedule.Enabled != nil && *job.Schedule.Enabled {
			if strings.TrimSpace(job.Schedule.LinuxCron) != "" && !validFiveFieldCron(job.Schedule.LinuxCron) {
				errors = append(errors, fmt.Sprintf("job=%s schedule.linux_cron must be a standard five-field cron expression", label))
			}
			if strings.TrimSpace(job.Schedule.WindowsTime) == "" {
				errors = append(errors, fmt.Sprintf("job=%s missing required field when schedule.enabled=true: schedule.windows_time", label))
			} else if !windowsTimePattern.MatchString(job.Schedule.WindowsTime) {
				errors = append(errors, fmt.Sprintf("job=%s schedule.windows_time must use HH:MM 24-hour format", label))
			}
			frequency := strings.ToUpper(strings.TrimSpace(job.Schedule.WindowsFrequency))
			if frequency == "" {
				frequency = "DAILY"
			}
			if !oneOf(frequency, "ONCE", "DAILY", "WEEKLY", "MONTHLY") {
				errors = append(errors, fmt.Sprintf("job=%s schedule.windows_frequency must be one of: ONCE, DAILY, WEEKLY, MONTHLY", label))
			}
		}

		if strings.TrimSpace(job.Type) == "" {
			errors = append(errors, fmt.Sprintf("job=%s missing required field: type", label))
			continue
		}

		switch job.TypeName() {
		case TypePostgres:
			require(&errors, label, job.BackupPath, "backup_path")
			require(&errors, label, job.FilePattern, "file_pattern")
			require(&errors, label, job.Target.Host, "target.host")
			if job.Target.Port == 0 {
				errors = append(errors, fmt.Sprintf("job=%s missing required field: target.port", label))
			}
			require(&errors, label, job.Target.Database, "target.database")
			require(&errors, label, job.Target.Username, "target.username")
			require(&errors, label, job.Target.CredentialMethod, "target.credential_method")
			require(&errors, label, job.Target.MaintenanceDatabase, "target.maintenance_database")
			if job.CredentialMethod() != "pgpass" {
				errors = append(errors, fmt.Sprintf("job=%s target.credential_method must be pgpass", label))
			}

		case TypeMySQL:
			require(&errors, label, job.BackupPath, "backup_path")
			require(&errors, label, job.FilePattern, "file_pattern")
			require(&errors, label, job.Target.Database, "target.database")
			require(&errors, label, job.Target.CredentialMethod, "target.credential_method")
			switch job.CredentialMethod() {
			case "login_path":
				require(&errors, label, job.Target.LoginPath, "target.login_path")
			case "defaults_file":
				require(&errors, label, job.Target.DefaultsFile, "target.defaults_file")
			default:
				errors = append(errors, fmt.Sprintf("job=%s target.credential_method must be login_path or defaults_file", label))
			}

		case TypeOracle:
			require(&errors, label, job.BackupPath, "backup_path")
			require(&errors, label, job.FilePattern, "file_pattern")
			require(&errors, label, job.Target.ConnectString, "target.connect_string")
			require(&errors, label, job.Target.Schema, "target.schema")
			require(&errors, label, job.Target.OracleDirectory, "target.oracle_directory")
			require(&errors, label, job.Target.CredentialMethod, "target.credential_method")
			if job.CredentialMethod() != "oracle_wallet" {
				errors = append(errors, fmt.Sprintf("job=%s target.credential_method must be oracle_wallet", label))
			}

		case TypeOracleRMAN:
			if strings.TrimSpace(cfg.ToolPath(job, "oracle_rman", "rman", "")) == "" {
				errors = append(errors, "tools.oracle_rman.rman must not be empty")
			}
			require(&errors, label, job.RMAN.Target, "rman.target")
			require(&errors, label, job.RMAN.CommandFile, "rman.command_file")
			require(&errors, label, job.RMAN.LogFile, "rman.log_file")
			require(&errors, label, job.RMAN.CredentialMethod, "rman.credential_method")
			if !oneOf(job.CredentialMethod(), "os_auth", "oracle_wallet") {
				errors = append(errors, fmt.Sprintf("job=%s rman.credential_method must be os_auth or oracle_wallet", label))
			}

		case TypeMSSQLPowerProtect:
			if strings.TrimSpace(cfg.ToolPath(job, "mssql_powerprotect", "ddbmsqlrc", "")) == "" {
				errors = append(errors, "tools.mssql_powerprotect.ddbmsqlrc must not be empty")
			}
			require(&errors, label, job.PowerProtect.DDHost, "powerprotect.dd_host")
			require(&errors, label, job.PowerProtect.DDUser, "powerprotect.dd_user")
			require(&errors, label, job.PowerProtect.DevicePath, "powerprotect.device_path")
			require(&errors, label, job.PowerProtect.LockboxPath, "powerprotect.lockbox_path")
			require(&errors, label, job.PowerProtect.Client, "powerprotect.client")
			require(&errors, label, job.PowerProtect.CredentialMethod, "powerprotect.credential_method")
			require(&errors, label, job.Source.Database, "source.database")
			require(&errors, label, job.Target.Database, "target.database")
			if job.CredentialMethod() != "lockbox" {
				errors = append(errors, fmt.Sprintf("job=%s powerprotect.credential_method must be lockbox", label))
			}
			if len(job.Relocate) == 0 {
				errors = append(errors, fmt.Sprintf("job=%s relocate must contain at least one item", label))
			}
			for relocateIndex, item := range job.Relocate {
				require(&errors, label, item.LogicalName, fmt.Sprintf("relocate[%d].logical_name", relocateIndex))
				require(&errors, label, item.PhysicalPath, fmt.Sprintf("relocate[%d].physical_path", relocateIndex))
			}

		default:
			errors = append(errors, fmt.Sprintf("job=%s unsupported database type: %s", label, job.TypeName()))
		}
	}

	validateAlerts(cfg, &errors)

	if len(errors) > 0 {
		return ValidationError{Errors: errors}
	}
	return nil
}

func require(errors *[]string, jobName, value, field string) {
	if strings.TrimSpace(value) == "" {
		*errors = append(*errors, fmt.Sprintf("job=%s missing required field: %s", jobName, field))
	}
}

func validFiveFieldCron(value string) bool {
	if strings.ContainsAny(value, "\r\n") {
		return false
	}
	return len(strings.Fields(value)) == 5
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func validateAlerts(cfg Config, errors *[]string) {
	if !cfg.Alerts.Enabled {
		return
	}
	if cfg.Alerts.Slack.Enabled {
		if strings.TrimSpace(cfg.Alerts.Slack.WebhookURLEnv) == "" {
			*errors = append(*errors, "alerts.slack.webhook_url_env must not be empty when Slack alerts are enabled")
		}
	}
	if cfg.Alerts.Email.Enabled {
		if strings.TrimSpace(cfg.Alerts.Email.SMTPHost) == "" {
			*errors = append(*errors, "alerts.email.smtp_host must not be empty when email alerts are enabled")
		}
		if cfg.Alerts.Email.SMTPPort == 0 {
			*errors = append(*errors, "alerts.email.smtp_port must not be empty when email alerts are enabled")
		}
		if strings.TrimSpace(cfg.Alerts.Email.UsernameEnv) == "" {
			*errors = append(*errors, "alerts.email.username_env must not be empty when email alerts are enabled")
		}
		if strings.TrimSpace(cfg.Alerts.Email.PasswordEnv) == "" {
			*errors = append(*errors, "alerts.email.password_env must not be empty when email alerts are enabled")
		}
		if strings.TrimSpace(cfg.Alerts.Email.FromEnv) == "" {
			*errors = append(*errors, "alerts.email.from_env must not be empty when email alerts are enabled")
		}
		if len(cfg.Alerts.Email.To) == 0 {
			*errors = append(*errors, "alerts.email.to must contain at least one recipient when email alerts are enabled")
		}
	}
}

func hasForbiddenPasswordField(node *yaml.Node, path string) bool {
	if node == nil {
		return false
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return hasForbiddenPasswordField(node.Content[0], path)
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := strings.ToLower(strings.TrimSpace(node.Content[i].Value))
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			switch key {
			case "password", "db_password", "postgres_password", "mysql_password", "oracle_password", "mssql_password":
				return true
			}
			if childPath == "target.password" || childPath == "powerprotect.password" {
				return true
			}
			if hasForbiddenPasswordField(node.Content[i+1], childPath) {
				return true
			}
		}
	}
	if node.Kind == yaml.SequenceNode {
		for _, child := range node.Content {
			if hasForbiddenPasswordField(child, path) {
				return true
			}
		}
	}
	return false
}
