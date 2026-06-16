package oracle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"db-restore-automation/internal/inspect/common"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_$#]*$`)

type Inspector struct{}

func (Inspector) Type() string { return "oracle" }

func (Inspector) Inspect(ctx context.Context, request common.Request) common.JobReport {
	job := request.Job
	report := common.JobReport{Name: job.Name, Type: "oracle", Enabled: job.Enabled}

	configuredImpdp := request.Config.Tool("oracle", "impdp")
	configuredSQLPlus := request.Config.Tool("oracle", "sqlplus")
	common.InspectExecutable(ctx, &report, "impdp", configuredImpdp, "impdp", "-version")
	sqlplus := ""
	if configuredSQLPlus != "" || request.Options.TestConnection {
		sqlplus = common.InspectOptionalExecutable(ctx, &report, "sqlplus", configuredSQLPlus, "sqlplus", "-version")
	}

	oracleHome := firstNonEmpty(job.Value(common.ProviderSectionPaths("oracle", "oracle_home")...), os.Getenv("ORACLE_HOME"))
	if oracleHome == "" {
		report.Warn("ORACLE_HOME", "environment variable and job value are not configured", "")
	} else {
		common.InspectDirectory(&report, "ORACLE_HOME", oracleHome, false, false)
	}

	tnsAdmin := firstNonEmpty(job.Value(common.ProviderSectionPaths("oracle", "tns_admin")...), os.Getenv("TNS_ADMIN"))
	if tnsAdmin == "" && oracleHome != "" {
		tnsAdmin = filepath.Join(oracleHome, "network", "admin")
	}
	if common.InspectDirectory(&report, "TNS_ADMIN", tnsAdmin, false, false) {
		common.InspectFile(&report, "tnsnames.ora", filepath.Join(tnsAdmin, "tnsnames.ora"), false)
		common.InspectFile(&report, "sqlnet.ora", filepath.Join(tnsAdmin, "sqlnet.ora"), false)
	}

	credentialMethod := strings.ToLower(job.Value(common.ProviderSectionPaths("oracle", "credential_method")...))
	if credentialMethod == "" {
		credentialMethod = "oracle_wallet"
	}
	if credentialMethod != "oracle_wallet" {
		report.Fail("credential method", "only oracle_wallet is supported for Oracle Data Pump", credentialMethod)
	} else {
		report.Pass("credential method", "Oracle Wallet authentication configured", credentialMethod)
	}

	walletPath := request.Config.ResolvePath(job.Value(common.ProviderSectionPaths("oracle", "wallet_path", "wallet_directory")...))
	if walletPath == "" {
		walletPath = common.FirstExistingPath(
			filepath.Join(tnsAdmin, "wallet"),
			filepath.Join(oracleHome, "network", "admin", "wallet"),
		)
	}
	common.InspectDirectory(&report, "Oracle Wallet", walletPath, !request.Options.Discover, false)

	backupDirectory := request.Config.ResolvePath(job.Value(common.ProviderSectionPaths("oracle",
		"backup_path", "backup_directory", "backup.dir", "backup.path")...))
	filePattern := job.Value(common.ProviderSectionPaths("oracle", "file_pattern", "backup_pattern", "backup.pattern")...)
	if common.InspectDirectory(&report, "backup directory", backupDirectory, !request.Options.Discover, false) {
		common.InspectLatestBackup(&report, backupDirectory, filePattern, []string{".dmp"})
	}

	schema := job.Value(common.ProviderSectionPaths("oracle", "schema", "target_schema")...)
	directoryObject := job.Value(common.ProviderSectionPaths("oracle", "directory", "directory_object")...)
	serviceName := job.Value(common.ProviderSectionPaths("oracle", "service", "service_name", "connect_identifier", "target")...)
	logFile := job.Value(common.ProviderSectionPaths("oracle", "log_file", "logfile")...)
	if logFile == "" {
		report.Warn("Data Pump log file", "log_file is not configured", "")
	} else {
		report.Pass("Data Pump log file", "server-side Data Pump log filename is configured", logFile)
	}

	if !request.Options.Discover {
		common.InspectName(&report, "target schema", schema, identifierPattern, true)
		common.InspectName(&report, "Oracle DIRECTORY object", directoryObject, identifierPattern, true)
		report.Info("Oracle DIRECTORY object verification", "the server-side DIRECTORY object cannot be confirmed from the local filesystem without a database connection", directoryObject)
		if serviceName == "" {
			report.Fail("Oracle service", "service name or connect identifier is not configured", "")
		} else {
			report.Pass("Oracle service", "connect identifier is configured", serviceName)
		}
	}

	if request.Options.TestConnection {
		if sqlplus == "" || serviceName == "" {
			report.Warn("Oracle query", "sqlplus or service name is unavailable; SELECT 1 was not run", "")
		} else {
			connect := "/@" + serviceName
			script := "SET HEADING OFF FEEDBACK OFF PAGESIZE 0 VERIFY OFF ECHO OFF\nSELECT 1 FROM DUAL;\nEXIT;\n"
			output, err := common.RunReadOnlyCommand(ctx, sqlplus, []string{"-L", connect}, nil, script)
			if err != nil || !containsStandaloneOne(output) {
				report.Fail("Oracle query", fmt.Sprintf("read-only SELECT 1 FROM DUAL failed: %v", err), common.CompactOutput(output, 500))
			} else {
				report.Pass("Oracle query", "read-only SELECT 1 FROM DUAL succeeded", "1")
			}
		}
	} else {
		report.Info("connection test", "skipped; use --test-connection to run an Oracle SELECT 1 check", "")
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

func containsStandaloneOne(output string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r", ""), "\n") {
		if strings.TrimSpace(line) == "1" {
			return true
		}
	}
	return false
}
