package safety

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
)

const (
	requireConfirmationEnv = "REQUIRE_CONFIRMATION"
	maxConfirmationLength  = 4096
)

type Checker struct {
	Logger *logging.Logger
}

type safetyValue struct {
	Field string
	Value string
}

func (c Checker) Validate(job config.JobConfig) error {
	jobName := strings.TrimSpace(job.Name)

	for tokenIndex, rawToken := range job.Safety.BlockIfNameContains {
		token := strings.TrimSpace(rawToken)
		if token == "" {
			continue
		}

		if strings.ContainsRune(token, '\x00') ||
			strings.ContainsAny(token, "\r\n") {
			return fmt.Errorf(
				"job=%q safety.block_if_name_contains[%d] must be a single-line value without null characters",
				jobName,
				tokenIndex,
			)
		}

		normalizedToken := strings.ToLower(token)

		for _, candidate := range safetyValues(job) {
			normalizedValue := strings.ToLower(
				strings.TrimSpace(candidate.Value),
			)

			if normalizedValue == "" {
				continue
			}

			if !strings.Contains(
				normalizedValue,
				normalizedToken,
			) {
				continue
			}

			c.logError(fmt.Sprintf(
				"job=%s safety=blocked blocked_token=%s matched_field=%s",
				jobName,
				token,
				candidate.Field,
			))

			return fmt.Errorf(
				"safety blocked job %q because token %q matched %s",
				jobName,
				token,
				candidate.Field,
			)
		}
	}

	return nil
}

func (c Checker) Confirm(
	job config.JobConfig,
	dryRun bool,
) error {
	jobName := strings.TrimSpace(job.Name)

	if jobName == "" {
		return fmt.Errorf(
			"confirmation cannot be performed because job name is empty",
		)
	}

	if dryRun {
		c.logInfo(fmt.Sprintf(
			"job=%s confirmation=skipped reason=dry_run",
			jobName,
		))

		return nil
	}

	required, err := confirmationRequired(job)
	if err != nil {
		c.logError(fmt.Sprintf(
			"job=%s confirmation=failed reason=invalid_configuration error=%s",
			jobName,
			err.Error(),
		))

		return err
	}

	if !required {
		c.logInfo(fmt.Sprintf(
			"job=%s confirmation=skipped reason=not_required",
			jobName,
		))

		return nil
	}

	if !interactive() {
		c.logError(fmt.Sprintf(
			"job=%s confirmation=failed reason=non_interactive_session",
			jobName,
		))

		return fmt.Errorf(
			"confirmation is required for job %q, but stdin is not interactive; explicitly set safety.require_confirmation=false for approved unattended execution",
			jobName,
		)
	}

	if _, err := fmt.Fprintf(
		os.Stderr,
		"Type the exact job name to restore [%s]: ",
		jobName,
	); err != nil {
		c.logError(fmt.Sprintf(
			"job=%s confirmation=failed reason=prompt_write_failed error=%s",
			jobName,
			err.Error(),
		))

		return fmt.Errorf(
			"write confirmation prompt: %w",
			err,
		)
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(
		make([]byte, 256),
		maxConfirmationLength,
	)

	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			c.logError(fmt.Sprintf(
				"job=%s confirmation=failed reason=read_failed error=%s",
				jobName,
				err.Error(),
			))

			return fmt.Errorf(
				"read confirmation input: %w",
				err,
			)
		}

		c.logError(fmt.Sprintf(
			"job=%s confirmation=failed reason=input_closed",
			jobName,
		))

		return fmt.Errorf(
			"confirmation input was closed before a value was entered",
		)
	}

	answer := strings.TrimSpace(scanner.Text())

	// Do not accept generic values such as "yes". Requiring the exact job
	// name reduces the chance of approving the wrong destructive restore.
	if answer != jobName {
		c.logError(fmt.Sprintf(
			"job=%s confirmation=failed reason=input_mismatch",
			jobName,
		))

		return fmt.Errorf(
			"confirmation input did not exactly match job name %q",
			jobName,
		)
	}

	c.logInfo(fmt.Sprintf(
		"job=%s confirmation=passed",
		jobName,
	))

	return nil
}

func confirmationRequired(
	job config.JobConfig,
) (bool, error) {
	// Destructive restores require confirmation by default unless the job
	// explicitly sets safety.require_confirmation=false.
	required := true

	if job.Safety.RequireConfirmation != nil {
		required = *job.Safety.RequireConfirmation
	}

	rawEnvironmentValue, exists := os.LookupEnv(
		requireConfirmationEnv,
	)

	if !exists ||
		strings.TrimSpace(rawEnvironmentValue) == "" {
		return required, nil
	}

	environmentValue, err := parseEnvironmentBoolean(
		rawEnvironmentValue,
	)
	if err != nil {
		return false, fmt.Errorf(
			"environment variable %s has invalid boolean value %q",
			requireConfirmationEnv,
			rawEnvironmentValue,
		)
	}

	// The environment variable may force confirmation globally, but it must
	// never disable confirmation required by a job configuration.
	if environmentValue {
		return true, nil
	}

	return required, nil
}

// Values returns the non-empty target-oriented values inspected by the safety
// checker. It remains exported for callers that display or test safety scope.
func Values(job config.JobConfig) []string {
	candidates := safetyValues(job)

	values := make(
		[]string,
		0,
		len(candidates),
	)

	seen := make(map[string]struct{})

	for _, candidate := range candidates {
		value := strings.TrimSpace(candidate.Value)
		if value == "" {
			continue
		}

		normalized := strings.ToLower(value)
		if _, exists := seen[normalized]; exists {
			continue
		}

		seen[normalized] = struct{}{}
		values = append(values, value)
	}

	return values
}

func safetyValues(
	job config.JobConfig,
) []safetyValue {
	values := []safetyValue{
		{
			Field: "job.name",
			Value: job.Name,
		},
	}

	add := func(
		field string,
		value string,
	) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}

		values = append(
			values,
			safetyValue{
				Field: field,
				Value: value,
			},
		)
	}

	switch job.TypeName() {
	case config.TypePostgres:
		add(
			"target.database",
			job.Target.Database,
		)
		add(
			"target.host",
			job.Target.Host,
		)
		add(
			"target.maintenance_database",
			job.Target.MaintenanceDatabase,
		)

	case config.TypeMySQL:
		add(
			"target.database",
			job.Target.Database,
		)
		add(
			"target.host",
			job.Target.Host,
		)

	case config.TypeOracle:
		add(
			"target.schema",
			job.Target.Schema,
		)
		add(
			"target.connect_string",
			job.Target.ConnectString,
		)

	case config.TypeOracleRMAN:
		add(
			"rman.target",
			job.RMAN.Target,
		)
		add(
			"rman.oracle_sid",
			job.RMAN.OracleSID,
		)

		// Do not inspect ORACLE_HOME. A block token such as "prod" would
		// incorrectly match the standard directory name "product".
		//
		// Do not inspect the RMAN catalog either because it is not the
		// destructive restore target.

	case config.TypeMSSQLPowerProtect:
		add(
			"target.database",
			job.Target.Database,
		)

		// source.database, client, dd_host, and device_path identify the
		// backup source. Production backups are commonly restored into test
		// targets, so blocking on source values would reject valid restores.

	default:
		add(
			"target",
			job.TargetText(),
		)
	}

	return deduplicateSafetyValues(values)
}

func deduplicateSafetyValues(
	values []safetyValue,
) []safetyValue {
	result := make(
		[]safetyValue,
		0,
		len(values),
	)

	seen := make(map[string]struct{})

	for _, candidate := range values {
		field := strings.TrimSpace(candidate.Field)
		value := strings.TrimSpace(candidate.Value)

		if field == "" || value == "" {
			continue
		}

		key := strings.ToLower(field) +
			"\x00" +
			strings.ToLower(value)

		if _, exists := seen[key]; exists {
			continue
		}

		seen[key] = struct{}{}

		result = append(
			result,
			safetyValue{
				Field: field,
				Value: value,
			},
		)
	}

	return result
}

func interactive() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}

func parseEnvironmentBoolean(
	value string,
) (bool, error) {
	switch strings.ToLower(
		strings.TrimSpace(value),
	) {
	case "true", "1", "yes", "y", "on":
		return true, nil

	case "false", "0", "no", "n", "off":
		return false, nil

	default:
		return false, fmt.Errorf(
			"invalid boolean value %q",
			value,
		)
	}
}

func envTrue(value string) bool {
	parsed, err := parseEnvironmentBoolean(value)
	return err == nil && parsed
}

func (c Checker) logInfo(message string) {
	if c.Logger != nil {
		c.Logger.Info(message)
	}
}

func (c Checker) logError(message string) {
	if c.Logger != nil {
		c.Logger.Error(message)
	}
}
