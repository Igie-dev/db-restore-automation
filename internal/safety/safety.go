package safety

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"db-restore-automation/internal/config"
	"db-restore-automation/internal/logging"
)

type Checker struct {
	Logger *logging.Logger
}

func (c Checker) Validate(job config.JobConfig) error {
	for _, token := range job.Safety.BlockIfNameContains {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		tokenLower := strings.ToLower(token)
		for _, value := range Values(job) {
			if strings.Contains(strings.ToLower(value), tokenLower) {
				if c.Logger != nil {
					c.Logger.Error(fmt.Sprintf("job=%s safety=blocked blocked_token=%s matched_value=%s", job.Name, token, value))
				}
				return fmt.Errorf("safety blocked token %q matched %q", token, value)
			}
		}
	}
	return nil
}

func (c Checker) Confirm(job config.JobConfig, dryRun bool) error {
	if dryRun {
		if c.Logger != nil {
			c.Logger.Info(fmt.Sprintf("job=%s confirmation=skipped reason=dry_run", job.Name))
		}
		return nil
	}
	required := false
	if job.Safety.RequireConfirmation != nil {
		required = *job.Safety.RequireConfirmation
	} else {
		required = envTrue(os.Getenv("REQUIRE_CONFIRMATION"))
	}
	if !required {
		return nil
	}
	if !interactive() {
		if c.Logger != nil {
			c.Logger.Error(fmt.Sprintf("job=%s confirmation=failed reason=non_interactive_session", job.Name))
		}
		return fmt.Errorf("confirmation required but stdin is not interactive")
	}
	fmt.Fprintf(os.Stderr, "Type job name to restore [%s]: ", job.Name)
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		if c.Logger != nil {
			c.Logger.Error(fmt.Sprintf("job=%s confirmation=failed reason=read_failed", job.Name))
		}
		return err
	}
	answer = strings.TrimSpace(answer)
	if answer != job.Name && !strings.EqualFold(answer, "yes") {
		if c.Logger != nil {
			c.Logger.Error(fmt.Sprintf("job=%s confirmation=failed reason=input_mismatch", job.Name))
		}
		return fmt.Errorf("confirmation input did not match job name")
	}
	if c.Logger != nil {
		c.Logger.Info(fmt.Sprintf("job=%s confirmation=passed", job.Name))
	}
	return nil
}

func Values(job config.JobConfig) []string {
	values := []string{job.Name}
	add := func(value string) {
		if strings.TrimSpace(value) != "" {
			values = append(values, value)
		}
	}
	switch job.TypeName() {
	case config.TypePostgres:
		add(job.Target.Database)
		add(job.Target.Host)
	case config.TypeMySQL:
		add(job.Target.Database)
		add(job.Target.Host)
	case config.TypeOracle:
		add(job.Target.Schema)
		add(job.Target.ConnectString)
		add(job.Target.OracleDirectory)
	case config.TypeOracleRMAN:
		add(job.RMAN.Target)
		add(job.RMAN.Catalog)
		add(job.RMAN.OracleSID)
		add(job.RMAN.OracleHome)
	case config.TypeMSSQLPowerProtect:
		add(job.Source.Database)
		add(job.Target.Database)
		add(job.PowerProtect.Client)
		add(job.PowerProtect.DDHost)
	default:
		add(job.TargetText())
	}
	return values
}

func interactive() bool {
	info, err := os.Stdin.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

func envTrue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "y":
		return true
	default:
		return false
	}
}
