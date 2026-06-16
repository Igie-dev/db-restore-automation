package postgres

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"db-restore-automation/internal/inspect/common"
)

var databaseNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

type Inspector struct{}

func (Inspector) Type() string { return "postgres" }

func (Inspector) Inspect(ctx context.Context, request common.Request) common.JobReport {
	job := request.Job
	report := common.JobReport{Name: job.Name, Type: "postgres", Enabled: job.Enabled}

	configuredPsql := request.Config.Tool("postgres", "psql")
	configuredPgRestore := request.Config.Tool("postgres", "pg_restore", "pgrestore")
	configuredDropDB := request.Config.Tool("postgres", "dropdb")
	configuredCreateDB := request.Config.Tool("postgres", "createdb")

	psql := common.InspectExecutable(ctx, &report, "psql", configuredPsql, "psql", "--version")
	common.InspectExecutable(ctx, &report, "pg_restore", configuredPgRestore, "pg_restore", "--version")
	common.InspectExecutable(ctx, &report, "dropdb", configuredDropDB, "dropdb", "--version")
	common.InspectExecutable(ctx, &report, "createdb", configuredCreateDB, "createdb", "--version")

	backupDirectory := request.Config.ResolvePath(job.Value(common.ProviderSectionPaths("postgres",
		"backup_path", "backup_directory", "backup.dir", "backup.path")...))
	filePattern := job.Value(common.ProviderSectionPaths("postgres", "file_pattern", "backup_pattern", "backup.pattern")...)
	if common.InspectDirectory(&report, "backup directory", backupDirectory, !request.Options.Discover, false) {
		common.InspectLatestBackup(&report, backupDirectory, filePattern, []string{".sql", ".sql.gz", ".dump", ".backup"})
	}

	credentialMethod := strings.ToLower(job.Value(common.ProviderSectionPaths("postgres", "credential_method")...))
	if credentialMethod == "" {
		credentialMethod = "pgpass"
	}
	if credentialMethod != "pgpass" {
		report.Fail("credential method", "only pgpass is supported for PostgreSQL inspection", credentialMethod)
	} else {
		report.Pass("credential method", "pgpass authentication configured", credentialMethod)
	}

	pgpass := job.Value(common.ProviderSectionPaths("postgres", "pgpass_file", "pgpass_path")...)
	if pgpass == "" {
		pgpass = common.DefaultPgpassPath()
	} else {
		pgpass = request.Config.ResolvePath(pgpass)
	}
	common.InspectFile(&report, "pgpass", pgpass, !request.Options.Discover)

	host := job.Value(common.ProviderSectionPaths("postgres", "host", "target_host")...)
	port := job.IntValue(5432, common.ProviderSectionPaths("postgres", "port", "target_port")...)
	user := job.Value(common.ProviderSectionPaths("postgres", "user", "username")...)
	targetDatabase := job.Value(common.ProviderSectionPaths("postgres", "target_database", "database", "db_name")...)
	maintenanceDatabase := job.Value(common.ProviderSectionPaths("postgres", "maintenance_database", "maintenance_db")...)
	if maintenanceDatabase == "" {
		maintenanceDatabase = "postgres"
	}
	owner := job.Value(common.ProviderSectionPaths("postgres", "owner", "database_owner")...)

	if request.Options.Discover {
		report.Info("configured host", "discovery mode does not require a host", host)
	} else {
		common.InspectName(&report, "target database", targetDatabase, databaseNamePattern, true)
		if owner != "" {
			common.InspectName(&report, "database owner", owner, databaseNamePattern, false)
		}
		if user == "" {
			report.Fail("database user", "user is not configured", "")
		} else {
			report.Pass("database user", "user is configured", user)
		}
		report.Info("maintenance database", "database used for administrative operations", maintenanceDatabase)
	}

	if request.Options.TestConnection {
		if common.InspectTCP(ctx, &report, "PostgreSQL TCP connectivity", host, port) && psql != "" && user != "" {
			args := []string{
				"--no-password",
				"--host", host,
				"--port", fmt.Sprint(port),
				"--username", user,
				"--dbname", maintenanceDatabase,
				"--tuples-only",
				"--no-align",
				"--command", "SELECT 1;",
			}
			output, err := common.RunReadOnlyCommand(ctx, psql, args, nil, "")
			if err != nil || strings.TrimSpace(output) != "1" {
				report.Fail("PostgreSQL query", fmt.Sprintf("read-only SELECT 1 failed: %v", err), common.CompactOutput(output, 500))
			} else {
				report.Pass("PostgreSQL query", "read-only SELECT 1 succeeded", "1")
			}
		}
	} else {
		report.Info("connection test", "skipped; use --test-connection to run TCP and SELECT 1 checks", "")
	}

	return report
}
