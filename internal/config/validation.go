package config

import (
	"fmt"
	"net/mail"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	jobNamePattern = regexp.MustCompile(
		`^[A-Za-z0-9_.-]+$`,
	)

	windowsTimePattern = regexp.MustCompile(
		`^([01][0-9]|2[0-3]):[0-5][0-9]$`,
	)

	environmentVariablePattern = regexp.MustCompile(
		`^[A-Za-z_][A-Za-z0-9_]*$`,
	)

	simpleTokenPattern = regexp.MustCompile(
		`^[A-Za-z0-9_.-]+$`,
	)
)

type ValidationError struct {
	Errors []string
}

func (e ValidationError) Error() string {
	if len(e.Errors) == 0 {
		return "configuration validation failed"
	}

	return strings.Join(e.Errors, "\n")
}

func Validate(cfg Config) error {
	validationErrors := make([]string, 0)

	if len(cfg.Jobs) == 0 {
		return ValidationError{
			Errors: []string{
				"config must contain a non-empty top-level jobs array",
			},
		}
	}

	seenJobNames := make(map[string]int)

	for index, job := range cfg.Jobs {
		label := strings.TrimSpace(job.Name)

		if label == "" {
			label = fmt.Sprintf("index-%d", index)

			validationErrors = append(
				validationErrors,
				fmt.Sprintf(
					"job=%q missing required field: name",
					label,
				),
			)
		} else {
			if !jobNamePattern.MatchString(label) {
				validationErrors = append(
					validationErrors,
					fmt.Sprintf(
						"job=%q name must contain only letters, numbers, underscores, hyphens, and dots",
						label,
					),
				)
			}

			normalizedName := strings.ToLower(label)

			if previousIndex, exists := seenJobNames[normalizedName]; exists {
				validationErrors = append(
					validationErrors,
					fmt.Sprintf(
						"duplicate job name: %q conflicts with jobs[%d]",
						label,
						previousIndex,
					),
				)
			} else {
				seenJobNames[normalizedName] = index
			}
		}

		if job.Enabled == nil {
			validationErrors = append(
				validationErrors,
				fmt.Sprintf(
					"job=%q missing required field: enabled",
					label,
				),
			)
		}

		jobType := job.TypeName()

		if jobType == "" {
			validationErrors = append(
				validationErrors,
				fmt.Sprintf(
					"job=%q missing required field: type",
					label,
				),
			)

			continue
		}

		if !supportedJobType(jobType) {
			validationErrors = append(
				validationErrors,
				fmt.Sprintf(
					"job=%q unsupported database type: %q",
					label,
					jobType,
				),
			)

			continue
		}

		validateSafety(
			&validationErrors,
			label,
			job.Safety,
		)

		// Disabled jobs are allowed to be incomplete templates. They must
		// still have a valid name, enabled flag, type, and safe configuration,
		// but provider-specific fields are checked only when the job is active.
		if !job.IsEnabled() {
			continue
		}

		validateSchedule(
			&validationErrors,
			label,
			job.Schedule,
		)

		switch jobType {
		case TypePostgres:
			validatePostgresJob(
				cfg,
				job,
				label,
				&validationErrors,
			)

		case TypeMySQL:
			validateMySQLJob(
				cfg,
				job,
				label,
				&validationErrors,
			)

		case TypeOracle:
			validateOracleJob(
				cfg,
				job,
				label,
				&validationErrors,
			)

		case TypeOracleRMAN:
			validateOracleRMANJob(
				cfg,
				job,
				label,
				&validationErrors,
			)

		case TypeMSSQLPowerProtect:
			validateMSSQLPowerProtectJob(
				cfg,
				job,
				label,
				&validationErrors,
			)
		}
	}

	validateAlerts(
		cfg,
		&validationErrors,
	)

	if len(validationErrors) > 0 {
		return ValidationError{
			Errors: validationErrors,
		}
	}

	return nil
}

func validatePostgresJob(
	cfg Config,
	job JobConfig,
	label string,
	validationErrors *[]string,
) {
	requireBackupSelection(
		validationErrors,
		label,
		job,
	)

	require(
		validationErrors,
		label,
		job.Target.Host,
		"target.host",
	)

	validatePort(
		validationErrors,
		label,
		job.Target.Port,
		"target.port",
	)

	require(
		validationErrors,
		label,
		job.Target.Database,
		"target.database",
	)

	require(
		validationErrors,
		label,
		job.Target.Username,
		"target.username",
	)

	require(
		validationErrors,
		label,
		job.Target.MaintenanceDatabase,
		"target.maintenance_database",
	)

	if job.CredentialMethod() != DefaultPostgresCredentialMethod {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q target.credential_method must be %q",
				label,
				DefaultPostgresCredentialMethod,
			),
		)
	}

	validateOptionalSingleLine(
		validationErrors,
		label,
		cfg.ToolPath(
			job,
			TypePostgres,
			"psql",
			"psql",
		),
		"tools.postgres.psql",
	)

	validateOptionalSingleLine(
		validationErrors,
		label,
		cfg.ToolPath(
			job,
			TypePostgres,
			"pg_restore",
			"pg_restore",
		),
		"tools.postgres.pg_restore",
	)

	validateOptionalSingleLine(
		validationErrors,
		label,
		cfg.ToolPath(
			job,
			TypePostgres,
			"dropdb",
			"dropdb",
		),
		"tools.postgres.dropdb",
	)

	validateOptionalSingleLine(
		validationErrors,
		label,
		cfg.ToolPath(
			job,
			TypePostgres,
			"createdb",
			"createdb",
		),
		"tools.postgres.createdb",
	)
}

func validateMySQLJob(
	cfg Config,
	job JobConfig,
	label string,
	validationErrors *[]string,
) {
	requireBackupSelection(
		validationErrors,
		label,
		job,
	)

	require(
		validationErrors,
		label,
		job.Target.Host,
		"target.host",
	)

	validatePort(
		validationErrors,
		label,
		job.Target.Port,
		"target.port",
	)

	require(
		validationErrors,
		label,
		job.Target.Database,
		"target.database",
	)

	require(
		validationErrors,
		label,
		job.Target.Username,
		"target.username",
	)

	switch job.CredentialMethod() {
	case "login_path":
		require(
			validationErrors,
			label,
			job.Target.LoginPath,
			"target.login_path",
		)

		if strings.TrimSpace(job.Target.DefaultsFile) != "" {
			*validationErrors = append(
				*validationErrors,
				fmt.Sprintf(
					"job=%q target.defaults_file must be empty when target.credential_method is login_path",
					label,
				),
			)
		}

	case "defaults_file":
		require(
			validationErrors,
			label,
			job.Target.DefaultsFile,
			"target.defaults_file",
		)

		if strings.TrimSpace(job.Target.LoginPath) != "" {
			*validationErrors = append(
				*validationErrors,
				fmt.Sprintf(
					"job=%q target.login_path must be empty when target.credential_method is defaults_file",
					label,
				),
			)
		}

	default:
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q target.credential_method must be login_path or defaults_file",
				label,
			),
		)
	}

	validateOptionalSingleLine(
		validationErrors,
		label,
		cfg.ToolPath(
			job,
			TypeMySQL,
			"mysql",
			"mysql",
		),
		"tools.mysql.mysql",
	)
}

func validateOracleJob(
	cfg Config,
	job JobConfig,
	label string,
	validationErrors *[]string,
) {
	requireBackupSelection(
		validationErrors,
		label,
		job,
	)

	require(
		validationErrors,
		label,
		job.Target.ConnectString,
		"target.connect_string",
	)

	require(
		validationErrors,
		label,
		job.Target.Schema,
		"target.schema",
	)

	require(
		validationErrors,
		label,
		job.Target.OracleDirectory,
		"target.oracle_directory",
	)

	if job.CredentialMethod() != DefaultOracleCredentialMethod {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q target.credential_method must be %q",
				label,
				DefaultOracleCredentialMethod,
			),
		)
	}

	validateOptionalSingleLine(
		validationErrors,
		label,
		cfg.ToolPath(
			job,
			TypeOracle,
			"impdp",
			"impdp",
		),
		"tools.oracle.impdp",
	)
}

func validateOracleRMANJob(
	cfg Config,
	job JobConfig,
	label string,
	validationErrors *[]string,
) {
	rmanTool := strings.TrimSpace(
		cfg.ToolPath(
			job,
			TypeOracleRMAN,
			"rman",
			"",
		),
	)

	if rmanTool == "" {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q tools.oracle_rman.rman must not be empty",
				label,
			),
		)
	} else {
		validateOptionalSingleLine(
			validationErrors,
			label,
			rmanTool,
			"tools.oracle_rman.rman",
		)
	}

	require(
		validationErrors,
		label,
		job.RMAN.Target,
		"rman.target",
	)

	require(
		validationErrors,
		label,
		job.RMAN.CommandFile,
		"rman.command_file",
	)

	require(
		validationErrors,
		label,
		job.RMAN.LogFile,
		"rman.log_file",
	)

	require(
		validationErrors,
		label,
		job.RMAN.OracleHome,
		"rman.oracle_home",
	)

	require(
		validationErrors,
		label,
		job.RMAN.OracleSID,
		"rman.oracle_sid",
	)

	if !oneOf(
		job.CredentialMethod(),
		DefaultOracleRMANCredentialMethod,
		"oracle_wallet",
	) {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q rman.credential_method must be os_auth or oracle_wallet",
				label,
			),
		)
	}

	if job.RestoreScope() != DefaultRMANScope {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q rman.restore_scope must be %q",
				label,
				DefaultRMANScope,
			),
		)
	}

	commandFile := strings.TrimSpace(job.RMAN.CommandFile)
	logFile := strings.TrimSpace(job.RMAN.LogFile)

	if commandFile != "" &&
		logFile != "" &&
		samePath(commandFile, logFile) {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q rman.command_file and rman.log_file must not reference the same path",
				label,
			),
		)
	}
}

func validateMSSQLPowerProtectJob(
	cfg Config,
	job JobConfig,
	label string,
	validationErrors *[]string,
) {
	ddbmsqlrc := strings.TrimSpace(
		cfg.ToolPath(
			job,
			TypeMSSQLPowerProtect,
			"ddbmsqlrc",
			"",
		),
	)

	if ddbmsqlrc == "" {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q tools.mssql_powerprotect.ddbmsqlrc must not be empty",
				label,
			),
		)
	} else {
		validateOptionalSingleLine(
			validationErrors,
			label,
			ddbmsqlrc,
			"tools.mssql_powerprotect.ddbmsqlrc",
		)
	}

	require(
		validationErrors,
		label,
		job.PowerProtect.DDHost,
		"powerprotect.dd_host",
	)

	require(
		validationErrors,
		label,
		job.PowerProtect.DDUser,
		"powerprotect.dd_user",
	)

	require(
		validationErrors,
		label,
		job.PowerProtect.DevicePath,
		"powerprotect.device_path",
	)

	require(
		validationErrors,
		label,
		job.PowerProtect.LockboxPath,
		"powerprotect.lockbox_path",
	)

	require(
		validationErrors,
		label,
		job.PowerProtect.Client,
		"powerprotect.client",
	)

	require(
		validationErrors,
		label,
		job.Source.Database,
		"source.database",
	)

	require(
		validationErrors,
		label,
		job.Target.Database,
		"target.database",
	)

	if job.CredentialMethod() != DefaultPowerProtectCredentialMethod {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q powerprotect.credential_method must be %q",
				label,
				DefaultPowerProtectCredentialMethod,
			),
		)
	}

	restoreType := job.PowerProtectRestoreType()

	if !simpleTokenPattern.MatchString(restoreType) {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q powerprotect.restore_type contains invalid characters",
				label,
			),
		)
	}

	if restoreType != DefaultPowerProtectRun {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q powerprotect.restore_type must be %q",
				label,
				DefaultPowerProtectRun,
			),
		)
	}

	if len(job.Relocate) == 0 {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q relocate must contain at least one item",
				label,
			),
		)

		return
	}

	seenLogicalNames := make(map[string]int)
	seenPhysicalPaths := make(map[string]int)

	for relocateIndex, item := range job.Relocate {
		logicalField := fmt.Sprintf(
			"relocate[%d].logical_name",
			relocateIndex,
		)

		physicalField := fmt.Sprintf(
			"relocate[%d].physical_path",
			relocateIndex,
		)

		require(
			validationErrors,
			label,
			item.LogicalName,
			logicalField,
		)

		require(
			validationErrors,
			label,
			item.PhysicalPath,
			physicalField,
		)

		logicalName := strings.TrimSpace(item.LogicalName)
		if logicalName != "" {
			logicalKey := strings.ToLower(logicalName)

			if previousIndex, exists := seenLogicalNames[logicalKey]; exists {
				*validationErrors = append(
					*validationErrors,
					fmt.Sprintf(
						"job=%q relocate[%d].logical_name duplicates relocate[%d].logical_name",
						label,
						relocateIndex,
						previousIndex,
					),
				)
			} else {
				seenLogicalNames[logicalKey] = relocateIndex
			}
		}

		physicalPath := strings.TrimSpace(item.PhysicalPath)
		if physicalPath != "" {
			physicalKey := strings.ToLower(
				filepath.Clean(physicalPath),
			)

			if previousIndex, exists := seenPhysicalPaths[physicalKey]; exists {
				*validationErrors = append(
					*validationErrors,
					fmt.Sprintf(
						"job=%q relocate[%d].physical_path duplicates relocate[%d].physical_path",
						label,
						relocateIndex,
						previousIndex,
					),
				)
			} else {
				seenPhysicalPaths[physicalKey] = relocateIndex
			}
		}
	}
}

func validateSchedule(
	validationErrors *[]string,
	label string,
	schedule ScheduleConfig,
) {
	if schedule.Enabled == nil || !*schedule.Enabled {
		return
	}

	linuxCron := strings.TrimSpace(schedule.LinuxCron)
	windowsTime := strings.TrimSpace(schedule.WindowsTime)
	windowsFrequency := strings.ToUpper(
		strings.TrimSpace(schedule.WindowsFrequency),
	)

	if linuxCron == "" && windowsTime == "" {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q schedule.enabled=true requires schedule.linux_cron or schedule.windows_time",
				label,
			),
		)

		return
	}

	if linuxCron != "" && !validFiveFieldCron(linuxCron) {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q schedule.linux_cron must be a valid five-field cron expression",
				label,
			),
		)
	}

	if windowsTime == "" {
		if windowsFrequency != "" {
			*validationErrors = append(
				*validationErrors,
				fmt.Sprintf(
					"job=%q schedule.windows_frequency requires schedule.windows_time",
					label,
				),
			)
		}

		return
	}

	if !windowsTimePattern.MatchString(windowsTime) {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q schedule.windows_time must use HH:MM 24-hour format",
				label,
			),
		)
	}

	if windowsFrequency == "" {
		windowsFrequency = "DAILY"
	}

	// The generated PowerShell scheduler currently creates daily triggers.
	// Weekly and monthly schedules require additional configuration such as
	// a day of week or day of month.
	if windowsFrequency != "DAILY" {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q schedule.windows_frequency must be DAILY",
				label,
			),
		)
	}
}

func validateSafety(
	validationErrors *[]string,
	label string,
	safety SafetyConfig,
) {
	seenTerms := make(map[string]int)

	for index, rawTerm := range safety.BlockIfNameContains {
		term := strings.TrimSpace(rawTerm)

		if term == "" {
			*validationErrors = append(
				*validationErrors,
				fmt.Sprintf(
					"job=%q safety.block_if_name_contains[%d] must not be empty",
					label,
					index,
				),
			)

			continue
		}

		if containsUnsafeControlCharacter(term) {
			*validationErrors = append(
				*validationErrors,
				fmt.Sprintf(
					"job=%q safety.block_if_name_contains[%d] must be a single-line value",
					label,
					index,
				),
			)
		}

		normalizedTerm := strings.ToLower(term)

		if previousIndex, exists := seenTerms[normalizedTerm]; exists {
			*validationErrors = append(
				*validationErrors,
				fmt.Sprintf(
					"job=%q safety.block_if_name_contains[%d] duplicates safety.block_if_name_contains[%d]",
					label,
					index,
					previousIndex,
				),
			)
		} else {
			seenTerms[normalizedTerm] = index
		}
	}
}

func requireBackupSelection(
	validationErrors *[]string,
	label string,
	job JobConfig,
) {
	require(
		validationErrors,
		label,
		job.BackupPath,
		"backup_path",
	)

	require(
		validationErrors,
		label,
		job.FilePattern,
		"file_pattern",
	)

	filePattern := strings.TrimSpace(job.FilePattern)
	if filePattern == "" {
		return
	}

	if strings.ContainsAny(filePattern, `/\`) {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q file_pattern must be a filename pattern, not a path",
				label,
			),
		)

		return
	}

	if filepath.IsAbs(filePattern) {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q file_pattern must not be an absolute path",
				label,
			),
		)

		return
	}

	if _, err := filepath.Match(
		filePattern,
		"pattern-validation-probe",
	); err != nil {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q file_pattern is invalid: %s",
				label,
				singleLine(err.Error()),
			),
		)
	}
}

func require(
	validationErrors *[]string,
	jobName string,
	value string,
	field string,
) {
	value = strings.TrimSpace(value)

	if value == "" {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q missing required field: %s",
				jobName,
				field,
			),
		)

		return
	}

	if containsUnsafeControlCharacter(value) {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q field %s must be a single-line value without null characters",
				jobName,
				field,
			),
		)
	}
}

func validateOptionalSingleLine(
	validationErrors *[]string,
	jobName string,
	value string,
	field string,
) {
	value = strings.TrimSpace(value)

	if value == "" {
		return
	}

	if containsUnsafeControlCharacter(value) {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q field %s must be a single-line value without null characters",
				jobName,
				field,
			),
		)
	}
}

func validatePort(
	validationErrors *[]string,
	jobName string,
	port int,
	field string,
) {
	if port < 1 || port > 65535 {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"job=%q %s must be between 1 and 65535",
				jobName,
				field,
			),
		)
	}
}

func validFiveFieldCron(value string) bool {
	if strings.ContainsAny(value, "\r\n\x00") {
		return false
	}

	fields := strings.Fields(value)
	if len(fields) != 5 {
		return false
	}

	ranges := [][2]int{
		{0, 59},
		{0, 23},
		{1, 31},
		{1, 12},
		{0, 7},
	}

	for index, field := range fields {
		if !validCronField(
			field,
			ranges[index][0],
			ranges[index][1],
		) {
			return false
		}
	}

	return true
}

func validCronField(
	value string,
	minimum int,
	maximum int,
) bool {
	if value == "" {
		return false
	}

	parts := strings.Split(value, ",")

	for _, part := range parts {
		if !validCronFieldPart(
			part,
			minimum,
			maximum,
		) {
			return false
		}
	}

	return true
}

func validCronFieldPart(
	value string,
	minimum int,
	maximum int,
) bool {
	if value == "" {
		return false
	}

	if strings.Count(value, "/") > 1 {
		return false
	}

	base := value

	if slashIndex := strings.IndexByte(value, '/'); slashIndex >= 0 {
		base = value[:slashIndex]
		stepText := value[slashIndex+1:]

		if base == "" || stepText == "" {
			return false
		}

		step, err := strconv.Atoi(stepText)
		if err != nil || step <= 0 {
			return false
		}
	}

	if base == "*" {
		return true
	}

	if strings.Count(base, "-") > 1 {
		return false
	}

	if dashIndex := strings.IndexByte(base, '-'); dashIndex >= 0 {
		startText := base[:dashIndex]
		endText := base[dashIndex+1:]

		start, startErr := strconv.Atoi(startText)
		end, endErr := strconv.Atoi(endText)

		if startErr != nil || endErr != nil {
			return false
		}

		return start >= minimum &&
			start <= maximum &&
			end >= minimum &&
			end <= maximum &&
			start <= end
	}

	number, err := strconv.Atoi(base)
	if err != nil {
		return false
	}

	return number >= minimum && number <= maximum
}

func supportedJobType(jobType string) bool {
	switch jobType {
	case TypePostgres,
		TypeMySQL,
		TypeOracle,
		TypeOracleRMAN,
		TypeMSSQLPowerProtect:
		return true

	default:
		return false
	}
}

func oneOf(value string, allowed ...string) bool {
	value = strings.TrimSpace(value)

	for _, candidate := range allowed {
		if strings.EqualFold(
			value,
			strings.TrimSpace(candidate),
		) {
			return true
		}
	}

	return false
}

func samePath(left string, right string) bool {
	left = filepath.Clean(strings.TrimSpace(left))
	right = filepath.Clean(strings.TrimSpace(right))

	// Match the runtime comparison in the RMAN provider: Windows paths are
	// case-insensitive, while Linux paths that differ only in case are distinct
	// files and must not be treated as the same path.
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}

	return left == right
}

func validateAlerts(
	cfg Config,
	validationErrors *[]string,
) {
	if !cfg.Alerts.Enabled {
		return
	}

	if !cfg.Alerts.Slack.Enabled &&
		!cfg.Alerts.Email.Enabled {
		*validationErrors = append(
			*validationErrors,
			"alerts.enabled=true requires Slack or email alerts to be enabled",
		)
	}

	if !cfg.Alerts.NotifyOn.Success &&
		!cfg.Alerts.NotifyOn.Failure &&
		!cfg.Alerts.NotifyOn.DryRun {
		*validationErrors = append(
			*validationErrors,
			"alerts.notify_on must enable success, failure, or dry_run",
		)
	}

	if cfg.Alerts.Slack.Enabled {
		validateEnvironmentVariableName(
			validationErrors,
			cfg.Alerts.Slack.WebhookURLEnv,
			"alerts.slack.webhook_url_env",
		)
	}

	if !cfg.Alerts.Email.Enabled {
		return
	}

	if strings.TrimSpace(cfg.Alerts.Email.SMTPHost) == "" {
		*validationErrors = append(
			*validationErrors,
			"alerts.email.smtp_host must not be empty when email alerts are enabled",
		)
	} else if containsUnsafeControlCharacter(
		cfg.Alerts.Email.SMTPHost,
	) {
		*validationErrors = append(
			*validationErrors,
			"alerts.email.smtp_host must be a single-line value",
		)
	}

	if cfg.Alerts.Email.SMTPPort < 1 ||
		cfg.Alerts.Email.SMTPPort > 65535 {
		*validationErrors = append(
			*validationErrors,
			"alerts.email.smtp_port must be between 1 and 65535",
		)
	}

	validateEnvironmentVariableName(
		validationErrors,
		cfg.Alerts.Email.UsernameEnv,
		"alerts.email.username_env",
	)

	validateEnvironmentVariableName(
		validationErrors,
		cfg.Alerts.Email.PasswordEnv,
		"alerts.email.password_env",
	)

	validateEnvironmentVariableName(
		validationErrors,
		cfg.Alerts.Email.FromEnv,
		"alerts.email.from_env",
	)

	if len(cfg.Alerts.Email.To) == 0 {
		*validationErrors = append(
			*validationErrors,
			"alerts.email.to must contain at least one recipient when email alerts are enabled",
		)

		return
	}

	seenRecipients := make(map[string]int)

	for index, rawRecipient := range cfg.Alerts.Email.To {
		recipient := strings.TrimSpace(rawRecipient)

		if recipient == "" {
			*validationErrors = append(
				*validationErrors,
				fmt.Sprintf(
					"alerts.email.to[%d] must not be empty",
					index,
				),
			)

			continue
		}

		if containsUnsafeControlCharacter(recipient) {
			*validationErrors = append(
				*validationErrors,
				fmt.Sprintf(
					"alerts.email.to[%d] must be a single-line value",
					index,
				),
			)

			continue
		}

		address, err := mail.ParseAddress(recipient)
		if err != nil || strings.TrimSpace(address.Address) == "" {
			*validationErrors = append(
				*validationErrors,
				fmt.Sprintf(
					"alerts.email.to[%d] is not a valid email address",
					index,
				),
			)

			continue
		}

		normalizedAddress := strings.ToLower(
			strings.TrimSpace(address.Address),
		)

		if previousIndex, exists := seenRecipients[normalizedAddress]; exists {
			*validationErrors = append(
				*validationErrors,
				fmt.Sprintf(
					"alerts.email.to[%d] duplicates alerts.email.to[%d]",
					index,
					previousIndex,
				),
			)
		} else {
			seenRecipients[normalizedAddress] = index
		}
	}
}

func validateEnvironmentVariableName(
	validationErrors *[]string,
	value string,
	field string,
) {
	value = strings.TrimSpace(value)

	if value == "" {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"%s must not be empty when its alert channel is enabled",
				field,
			),
		)

		return
	}

	if !environmentVariablePattern.MatchString(value) {
		*validationErrors = append(
			*validationErrors,
			fmt.Sprintf(
				"%s must contain a valid environment variable name",
				field,
			),
		)
	}
}

func containsUnsafeControlCharacter(value string) bool {
	return strings.ContainsAny(
		value,
		"\r\n\x00",
	)
}

func singleLine(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")

	return value
}

func hasForbiddenPasswordField(
	node *yaml.Node,
	path string,
) bool {
	return hasForbiddenPasswordFieldVisited(
		node,
		path,
		make(map[*yaml.Node]bool),
	)
}

func hasForbiddenPasswordFieldVisited(
	node *yaml.Node,
	path string,
	visited map[*yaml.Node]bool,
) bool {
	if node == nil {
		return false
	}

	if visited[node] {
		return false
	}

	visited[node] = true

	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if hasForbiddenPasswordFieldVisited(
				child,
				path,
				visited,
			) {
				return true
			}
		}

	case yaml.MappingNode:
		for index := 0; index+1 < len(node.Content); index += 2 {
			keyNode := node.Content[index]
			valueNode := node.Content[index+1]

			key := normalizeYAMLKey(keyNode.Value)
			childPath := key

			if path != "" {
				childPath = path + "." + key
			}

			if forbiddenPasswordKey(key) {
				return true
			}

			if hasForbiddenPasswordFieldVisited(
				valueNode,
				childPath,
				visited,
			) {
				return true
			}
		}

	case yaml.SequenceNode:
		for _, child := range node.Content {
			if hasForbiddenPasswordFieldVisited(
				child,
				path,
				visited,
			) {
				return true
			}
		}

	case yaml.AliasNode:
		if hasForbiddenPasswordFieldVisited(
			node.Alias,
			path,
			visited,
		) {
			return true
		}
	}

	return false
}

func forbiddenPasswordKey(key string) bool {
	key = normalizeYAMLKey(key)

	// These fields contain the name of an environment variable, not the
	// password itself, and are intentionally allowed.
	if strings.HasSuffix(key, "_env") ||
		strings.HasSuffix(key, "_env_name") ||
		strings.HasSuffix(key, "_environment_variable") {
		return false
	}

	switch key {
	case "password",
		"passwd",
		"pwd",
		"passphrase",
		"db_password",
		"database_password",
		"postgres_password",
		"postgresql_password",
		"mysql_password",
		"oracle_password",
		"mssql_password",
		"sqlserver_password",
		"smtp_password":
		return true
	}

	return strings.HasSuffix(key, "_password") ||
		strings.HasSuffix(key, "_passwd") ||
		strings.HasSuffix(key, "_pwd") ||
		strings.HasSuffix(key, "_passphrase")
}

func normalizeYAMLKey(value string) string {
	value = strings.ToLower(
		strings.TrimSpace(value),
	)

	replacer := strings.NewReplacer(
		"-",
		"_",
		".",
		"_",
		" ",
		"_",
	)

	return replacer.Replace(value)
}
