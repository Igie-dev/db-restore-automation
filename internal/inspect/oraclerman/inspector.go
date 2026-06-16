package oraclerman

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"db-restore-automation/internal/inspect/common"
)

type Inspector struct{}

func (Inspector) Type() string { return "oracle_rman" }

func (Inspector) Inspect(ctx context.Context, request common.Request) common.JobReport {
	job := request.Job
	report := common.JobReport{Name: job.Name, Type: "oracle_rman", Enabled: job.Enabled}

	configuredRMAN := request.Config.Tool("oracle_rman", "rman")
	common.InspectExecutable(ctx, &report, "rman", configuredRMAN, "rman", "-version")

	oracleHome := firstNonEmpty(job.Value(common.ProviderSectionPaths("oracle_rman", "oracle_home")...), os.Getenv("ORACLE_HOME"))
	if oracleHome == "" {
		report.Warn("ORACLE_HOME", "environment variable and job value are not configured", "")
	} else {
		common.InspectDirectory(&report, "ORACLE_HOME", oracleHome, false, false)
	}

	oracleSID := firstNonEmpty(job.Value(common.ProviderSectionPaths("oracle_rman", "oracle_sid")...), os.Getenv("ORACLE_SID"))
	if oracleSID == "" {
		report.Warn("ORACLE_SID", "environment variable and job value are not configured", "")
	} else {
		report.Pass("ORACLE_SID", "Oracle SID is configured", oracleSID)
	}

	tnsAdmin := firstNonEmpty(job.Value(common.ProviderSectionPaths("oracle_rman", "tns_admin")...), os.Getenv("TNS_ADMIN"))
	if tnsAdmin == "" && oracleHome != "" {
		tnsAdmin = filepath.Join(oracleHome, "network", "admin")
	}
	common.InspectDirectory(&report, "TNS_ADMIN", tnsAdmin, false, false)

	credentialMethod := strings.ToLower(job.Value(common.ProviderSectionPaths("oracle_rman", "credential_method")...))
	if credentialMethod == "" {
		credentialMethod = "os_auth"
	}
	switch credentialMethod {
	case "os_auth":
		report.Pass("credential method", "operating-system authentication configured", credentialMethod)
	case "oracle_wallet":
		report.Pass("credential method", "Oracle Wallet authentication configured", credentialMethod)
		walletPath := request.Config.ResolvePath(job.Value(common.ProviderSectionPaths("oracle_rman", "wallet_path", "wallet_directory")...))
		common.InspectDirectory(&report, "Oracle Wallet", walletPath, !request.Options.Discover, false)
	default:
		report.Fail("credential method", "supported values are os_auth and oracle_wallet", credentialMethod)
	}

	target := job.Value(common.ProviderSectionPaths("oracle_rman", "target")...)
	if target == "" {
		report.Fail("RMAN target", "target is not configured", "")
	} else {
		report.Pass("RMAN target", "target is configured", target)
	}

	commandFile := request.Config.ResolvePath(job.Value(common.ProviderSectionPaths("oracle_rman", "command_file", "cmdfile")...))
	common.InspectFile(&report, "RMAN command file", commandFile, !request.Options.Discover)

	logFile := request.Config.ResolvePath(job.Value(common.ProviderSectionPaths("oracle_rman", "log_file", "log")...))
	if logFile == "" {
		report.Warn("RMAN log file", "log file is not configured", "")
	} else {
		common.InspectDirectory(&report, "RMAN log directory", filepath.Dir(logFile), true, true)
		report.Info("RMAN log file", "configured output file", logFile)
	}

	workingDirectory := request.Config.ResolvePath(job.Value(common.ProviderSectionPaths("oracle_rman", "working_directory", "work_dir")...))
	if workingDirectory == "" && commandFile != "" {
		workingDirectory = filepath.Dir(commandFile)
	}
	common.InspectDirectory(&report, "RMAN working directory", workingDirectory, !request.Options.Discover, false)

	restoreScope := strings.ToLower(job.Value(common.ProviderSectionPaths("oracle_rman", "restore_scope")...))
	if restoreScope == "" {
		restoreScope = "full_database"
	}
	if restoreScope != "full_database" {
		report.Fail("restore scope", "only full_database is currently supported", restoreScope)
	} else {
		report.Pass("restore scope", "supported restore scope", restoreScope)
	}

	if request.Options.TestConnection {
		report.Warn("RMAN connection test", "not executed automatically because RMAN behavior depends on the DBA-approved target and environment; use the existing dry-run/validation flow", "")
	} else {
		report.Info("connection test", "skipped; RMAN inspection is intentionally offline", "")
	}

	return report
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(os.ExpandEnv(value))
		}
	}
	return ""
}
