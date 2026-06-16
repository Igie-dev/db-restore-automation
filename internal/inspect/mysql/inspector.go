package mysql

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"db-restore-automation/internal/inspect/common"
)

var databaseNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

type Inspector struct{}

func (Inspector) Type() string { return "mysql" }

func (Inspector) Inspect(ctx context.Context, request common.Request) common.JobReport {
	job := request.Job
	report := common.JobReport{Name: job.Name, Type: "mysql", Enabled: job.Enabled}

	configuredMySQL := request.Config.Tool("mysql", "mysql")
	configuredEditor := request.Config.Tool("mysql", "mysql_config_editor", "config_editor")
	mysqlPath := common.InspectExecutable(ctx, &report, "mysql", configuredMySQL, "mysql", "--version")
	configEditor := common.InspectOptionalExecutable(ctx, &report, "mysql_config_editor", configuredEditor, "mysql_config_editor", "--version")

	backupDirectory := request.Config.ResolvePath(job.Value(common.ProviderSectionPaths("mysql",
		"backup_path", "backup_directory", "backup.dir", "backup.path")...))
	filePattern := job.Value(common.ProviderSectionPaths("mysql", "file_pattern", "backup_pattern", "backup.pattern")...)
	if common.InspectDirectory(&report, "backup directory", backupDirectory, !request.Options.Discover, false) {
		common.InspectLatestBackup(&report, backupDirectory, filePattern, []string{".sql", ".sql.gz"})
	}

	host := job.Value(common.ProviderSectionPaths("mysql", "host", "target_host")...)
	port := job.IntValue(3306, common.ProviderSectionPaths("mysql", "port", "target_port")...)
	targetDatabase := job.Value(common.ProviderSectionPaths("mysql", "target_database", "database", "db_name")...)
	credentialMethod := strings.ToLower(job.Value(common.ProviderSectionPaths("mysql", "credential_method")...))
	loginPath := job.Value(common.ProviderSectionPaths("mysql", "login_path")...)
	defaultsFile := request.Config.ResolvePath(job.Value(common.ProviderSectionPaths("mysql", "defaults_file", "option_file")...))
	if credentialMethod == "" {
		credentialMethod = "login_path"
	}

	if !request.Options.Discover {
		common.InspectName(&report, "target database", targetDatabase, databaseNamePattern, true)
		switch credentialMethod {
		case "login_path":
			if loginPath == "" {
				report.Fail("MySQL login path", "login_path is not configured", "")
			} else {
				report.Pass("MySQL login path", "login path is configured", loginPath)
				if configEditor != "" {
					output, err := common.RunReadOnlyCommand(ctx, configEditor, []string{"print", "--all"}, nil, "")
					if err != nil {
						report.Warn("MySQL login paths", fmt.Sprintf("unable to list login paths: %v", err), common.CompactOutput(output, 500))
					} else if strings.Contains(output, "["+loginPath+"]") {
						report.Pass("MySQL login paths", "configured login path exists", loginPath)
					} else {
						report.Fail("MySQL login paths", "configured login path was not found", loginPath)
					}
				}
			}
		case "defaults_file":
			common.InspectFile(&report, "MySQL defaults file", defaultsFile, true)
		default:
			report.Fail("credential method", "supported values are login_path and defaults_file", credentialMethod)
		}
	}

	if request.Options.TestConnection {
		if common.InspectTCP(ctx, &report, "MySQL TCP connectivity", host, port) && mysqlPath != "" {
			args := []string{}
			if credentialMethod == "defaults_file" && defaultsFile != "" {
				args = append(args, "--defaults-extra-file="+defaultsFile)
			} else if loginPath != "" {
				args = append(args, "--login-path="+loginPath)
			}
			if host != "" {
				args = append(args, "--host="+host)
			}
			args = append(args,
				fmt.Sprintf("--port=%d", port),
				"--batch",
				"--skip-column-names",
				"--execute=SELECT 1;",
			)
			output, err := common.RunReadOnlyCommand(ctx, mysqlPath, args, nil, "")
			if err != nil || strings.TrimSpace(output) != "1" {
				report.Fail("MySQL query", fmt.Sprintf("read-only SELECT 1 failed: %v", err), common.CompactOutput(output, 500))
			} else {
				report.Pass("MySQL query", "read-only SELECT 1 succeeded", "1")
			}
		}
	} else {
		report.Info("connection test", "skipped; use --test-connection to run TCP and SELECT 1 checks", "")
	}

	return report
}
