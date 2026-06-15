# db-restore-automation

Cross-platform, Go-based restore orchestration for existing database backups and Dell PowerProtect MSSQL restores.

This project is strictly **restore-only**. It never creates database backups.

## Supported Restore Providers

| Type                 | Provider                          | Supported input                       |
| -------------------- | --------------------------------- | ------------------------------------- |
| `postgres`           | PostgreSQL `psql` / `pg_restore`  | `.sql`, `.sql.gz`, `.dump`, `.backup` |
| `mysql`              | MySQL or MariaDB `mysql`          | `.sql`, `.sql.gz`                     |
| `oracle`             | Oracle Data Pump `impdp`          | `.dmp`                                |
| `oracle_rman`        | Oracle RMAN                       | DBA-approved RMAN command file        |
| `mssql_powerprotect` | Dell PowerProtect `ddbmsqlrc.exe` | PowerProtect-managed backup           |

## Repository Layout

```text
cmd/db-restore-automation/   Go CLI entrypoint
internal/                    Go implementation packages
config/                      Environment-specific restore job YAML files
examples/                    Sample restore job YAML files
rman/                        RMAN sample command files
logs/                        Main runtime logs
docs/                        Operator documentation
```

The legacy Bash and PowerShell restore runners have been removed.

Use the Go CLI for:

* Configuration validation
* Restore execution
* Dry-run validation
* Linux cron generation
* Windows Task Scheduler script generation
* Slack and email notifications

## Requirements

Install the Go version declared in `go.mod`, or a newer compatible release.

Restore tools must also be installed for the providers used on the machine:

* PostgreSQL: `psql`, `pg_restore`, `dropdb`, and `createdb`
* MySQL/MariaDB: `mysql`
* Oracle Data Pump: `impdp`
* Oracle RMAN: `rman`
* Dell PowerProtect MSSQL: `ddbmsqlrc.exe`

The operating-system account running the CLI or scheduled task must have access to:

* The configured backup files
* Database credential stores
* Oracle Wallets
* Dell PowerProtect lockboxes
* Restore tool executables
* Target database servers
* Log directories

## Build

Run commands from the repository root.

### Linux

```bash
go mod tidy
go build -o db-restore-automation ./cmd/db-restore-automation
```

### Windows PowerShell

```powershell
go mod tidy
go build -o db-restore-automation.exe .\cmd\db-restore-automation
```

## CLI Commands

```text
db-restore-automation validate --config <config-file>

db-restore-automation restore \
  --config <config-file> \
  [--job <job-name>] \
  [--dry-run]

db-restore-automation schedule linux \
  --config <config-file> \
  --root-dir <root-directory>

db-restore-automation schedule windows \
  --config <config-file> \
  --root-dir <root-directory>
```

Use `--help` to display command-specific options:

```bash
./db-restore-automation restore --help
```

```powershell
.\db-restore-automation.exe schedule windows --help
```

## Step-by-Step Run Guide

### 1. Build the CLI

Linux:

```bash
go build -o db-restore-automation ./cmd/db-restore-automation
```

Windows:

```powershell
go build -o db-restore-automation.exe .\cmd\db-restore-automation
```

### 2. Select the machine configuration

Use the appropriate configuration file:

* Linux: `config/restore-jobs.linux.yml`
* Windows: `config/restore-jobs.windows.yml`

### 3. Configure restore jobs

Update:

* Tool executable paths
* Backup directories
* Backup filename patterns
* Target databases
* Credential methods
* Restore schedules
* Safety rules
* Optional alerts

Do not put database, SMTP, Slack, wallet, or lockbox secrets directly in YAML.

### 4. Validate the configuration

Linux:

```bash
./db-restore-automation validate \
  --config ./config/restore-jobs.linux.yml
```

Windows:

```powershell
.\db-restore-automation.exe validate `
  --config .\config\restore-jobs.windows.yml
```

### 5. Run a dry run

Linux:

```bash
./db-restore-automation restore \
  --config ./config/restore-jobs.linux.yml \
  --job hris_postgres_restore \
  --dry-run
```

Windows:

```powershell
.\db-restore-automation.exe restore `
  --config .\config\restore-jobs.windows.yml `
  --job WideWorldImportersRestore `
  --dry-run
```

A dry run does not execute destructive provider commands.

However, it still performs applicable validation, including:

* Configuration validation
* Safety-rule validation
* Backup-file selection
* Backup-file readability checks
* Backup extension validation
* Gzip validation and temporary decompression when applicable
* Credential configuration validation
* Oracle command-file and directory validation
* Restore argument generation

A dry run may therefore fail when a required file, directory, or configuration value is missing.

### 6. Review logs

The default main log file is:

```text
logs/restore.log
```

The path is resolved relative to the application root used by the logger.

Override it with the `LOG_FILE` environment variable:

Linux:

```bash
export LOG_FILE=/var/log/db-restore-automation/restore.log
```

Windows PowerShell:

```powershell
$env:LOG_FILE = "C:\db-restore-automation\logs\restore.log"
```

Each provider command also writes stdout and stderr to temporary files. Their paths are recorded in the main log.

### 7. Run the restore

Linux:

```bash
./db-restore-automation restore \
  --config ./config/restore-jobs.linux.yml \
  --job hris_postgres_restore
```

Windows:

```powershell
.\db-restore-automation.exe restore `
  --config .\config\restore-jobs.windows.yml `
  --job WideWorldImportersRestore
```

### 8. Generate schedules

Generate schedules only after configuration validation and manual dry-run checks pass.

The schedule commands print generated scheduler content to stdout. They do not directly install the schedule.

## Configuration Validation

The YAML loader enforces:

* Exactly one YAML document
* A mapping/object at the root
* Known configuration fields only
* A configured maximum file size
* No direct password fields
* No empty top-level `jobs` array
* Unique job names, case-insensitively
* Supported restore provider types
* Required provider-specific values for enabled jobs
* Valid ports, schedules, credential methods, and alert settings

Disabled jobs may remain incomplete templates. Their name, type, enabled flag, and safety configuration must still be valid.

## Running Restores

### Restore one job

```bash
./db-restore-automation restore \
  --config ./config/restore-jobs.linux.yml \
  --job hris_postgres_restore
```

Job names are matched case-insensitively.

When a selected job is disabled, it is logged as skipped and no restore is performed.

### Restore all enabled jobs

```bash
./db-restore-automation restore \
  --config ./config/restore-jobs.linux.yml
```

The runner continues processing later selected jobs after an individual job failure.

Remaining jobs are not started after the operation context is cancelled, such as by Ctrl+C or `SIGTERM`.

## Exit Codes

| Exit code | Meaning                                                                                                                   |
| --------- | ------------------------------------------------------------------------------------------------------------------------- |
| `0`       | Command completed successfully                                                                                            |
| `1`       | Restore failure, safety failure, cancellation, or failed `validate` result                                                |
| `2`       | CLI usage error, unreadable or malformed configuration, invalid job selection, or schedule-generation configuration error |

For the `restore` and `schedule` commands, configuration rejection returns exit code `2`.

For the `validate` command, a successfully loaded configuration that fails semantic or safety validation returns exit code `1`.

## PostgreSQL Restores

Supported backup formats:

* `.sql`
* `.sql.gz`
* `.dump`
* `.backup`

Plain SQL and compressed SQL files are restored using `psql`.

Custom-format backups are restored using `pg_restore`.

Before destructive actions, the provider:

* Validates the backup file
* Decompresses `.sql.gz` files to a temporary SQL file
* Validates configured executable paths
* Runs a `pg_restore --list` preflight for custom-format archives
* Confirms that the target and maintenance databases are different

The restore sequence is:

1. Terminate active target-database sessions
2. Drop the target database
3. Recreate the target database
4. Restore the selected backup

PostgreSQL credentials must be supplied using `pgpass` or `pgpass.conf`.

## MySQL and MariaDB Restores

Supported backup formats:

* `.sql`
* `.sql.gz`

The provider validates and opens the backup before dropping the target database.

The restore sequence is:

1. Validate the backup stream
2. Drop the target database if it exists
3. Recreate the target database
4. Stream the SQL into the target database

Supported credential methods:

* `login_path`
* `defaults_file`

Example login-path creation:

```bash
mysql_config_editor set \
  --login-path=inventory_test \
  --host=localhost \
  --user=inventory_restore \
  --password
```

Do not put the MySQL password in YAML.

## Oracle Data Pump Restores

Oracle Data Pump uses `impdp` and accepts `.dmp` files.

The selected local backup file is validated before execution. Only its filename is passed to `impdp`.

The same dump filename must already be available in the filesystem path represented by the configured Oracle DIRECTORY object on the Oracle database server.

For example:

```yaml
target:
  connect_string: "/@ACCOUNTING_TEST"
  schema: "ACCOUNTING_TEST"
  oracle_directory: "ORCH_DUMP_DIR"
  credential_method: "oracle_wallet"
```

The Oracle DIRECTORY object `ORCH_DUMP_DIR` must point to the server-side directory containing the selected dump filename.

Oracle Data Pump credentials must use Oracle Wallet authentication.

## Oracle RMAN Restores

Oracle RMAN executes a DBA-approved command file.

Required configuration includes:

* RMAN executable
* Target connection
* Command file
* Log file
* `ORACLE_HOME`
* `ORACLE_SID`
* Credential method
* Restore scope

Supported credential methods:

* `os_auth`
* `oracle_wallet`

The currently supported restore scope is:

```yaml
restore_scope: "full_database"
```

RMAN command files must not contain credentials.

Example:

```yaml
rman:
  target: "/"
  catalog: ""
  command_file: "/opt/db-restore-automation/rman/restore-full-database.sample.rman"
  log_file: "/opt/db-restore-automation/logs/oracle-rman-restore.log"
  credential_method: "os_auth"
  oracle_home: "/u01/app/oracle/product/19c/dbhome_1"
  oracle_sid: "ORCLTEST"
  restore_scope: "full_database"
```

The RMAN provider captures and logs the tail of the current RMAN log after execution.

## Dell PowerProtect MSSQL Restores

Dell PowerProtect restores use `ddbmsqlrc.exe`.

Required configuration includes:

* Data Domain host
* Data Domain user
* Device path
* Lockbox path
* Source client
* Source database
* Target database
* Relocation mappings
* Lockbox credential method

The currently supported restore type is:

```yaml
restore_type: "normal"
```

Example:

```yaml
powerprotect:
  dd_host: "192.168.20.251"
  dd_user: "sql-ppdm-user"
  device_path: "/sql-ppdm-user/sample-device-path"
  lockbox_path: "C:\\Program Files\\DPSAPPS\\common\\lockbox"
  client: "source-sql-server.domain.local"
  skip_client_resolution: true
  restore_type: "normal"
  credential_method: "lockbox"
```

Each SQL Server logical file must have a unique destination:

```yaml
relocate:
  - logical_name: "AdventureWorks"
    physical_path: "C:\\Program Files\\Microsoft SQL Server\\MSSQL15.MSSQLSERVER\\MSSQL\\DATA\\AdventureWorks_Test.mdf"

  - logical_name: "AdventureWorks_log"
    physical_path: "C:\\Program Files\\Microsoft SQL Server\\MSSQL15.MSSQLSERVER\\MSSQL\\DATA\\AdventureWorks_Test_log.ldf"
```

The PowerProtect lockbox directory must be available to the account running the restore.

## Scheduling

### Linux cron generation

Generate cron entries:

```bash
./db-restore-automation schedule linux \
  --config ./config/restore-jobs.linux.yml \
  --root-dir /opt/db-restore-automation
```

Save the generated output for review:

```bash
./db-restore-automation schedule linux \
  --config ./config/restore-jobs.linux.yml \
  --root-dir /opt/db-restore-automation \
  > generated-restore.cron
```

Review the file:

```bash
cat generated-restore.cron
```

Install it for the current operating-system account:

```bash
crontab generated-restore.cron
```

Linux schedules use standard five-field cron expressions:

```yaml
schedule:
  enabled: true
  linux_cron: "0 3 * * *"
  windows_time: ""
  windows_frequency: ""
```

The generated cron command:

* Uses absolute application and configuration paths
* Creates the configured log directory
* Writes job output to a per-job cron log
* Escapes percent signs used by cron
* Skips enabled jobs with an empty `linux_cron`

### Windows Task Scheduler generation

Generate the PowerShell registration script:

```powershell
.\db-restore-automation.exe schedule windows `
  --config .\config\restore-jobs.windows.yml `
  --root-dir C:\db-restore-automation |
  Out-File .\install-db-restore-tasks.ps1 -Encoding utf8
```

Review the generated script:

```powershell
Get-Content .\install-db-restore-tasks.ps1
```

Run it using the Windows account that owns the required credential stores:

```powershell
.\install-db-restore-tasks.ps1
```

Windows schedules currently support only:

```yaml
windows_frequency: "DAILY"
```

Example:

```yaml
schedule:
  enabled: true
  linux_cron: ""
  windows_time: "03:00"
  windows_frequency: "DAILY"
```

Generated Windows tasks:

* Use the configured application root as the working directory
* Use absolute executable and configuration paths
* Start missed tasks when the machine becomes available
* Prevent overlapping instances of the same task
* Replace an existing task with the same generated task name

## Safety

Every enabled job is checked before execution.

### Blocked-name rules

The `safety.block_if_name_contains` list is matched case-insensitively against:

* The job name
* Target-oriented database identifiers
* Target host or connect string where applicable
* RMAN target and Oracle SID
* MSSQL target database

PowerProtect source database, source client, Data Domain host, and backup device path are intentionally not treated as destructive targets. This permits production backups to be restored into approved non-production databases.

Example:

```yaml
safety:
  require_confirmation: false
  block_if_name_contains:
    - "prod"
    - "production"
    - "live"
```

A matching blocked token stops the job before provider execution.

### Interactive confirmation

Destructive restores require confirmation by default.

To disable confirmation for an approved unattended job, explicitly configure:

```yaml
safety:
  require_confirmation: false
```

When confirmation is required:

* `--dry-run` skips the prompt
* Interactive execution requires typing the exact job name
* Generic answers such as `yes` are not accepted
* Non-interactive execution fails safely

The environment variable below can force confirmation globally:

```text
REQUIRE_CONFIRMATION=true
```

Setting the environment variable to `false` does not override a job that requires confirmation.

## Credentials

Do not store passwords in YAML.

### PostgreSQL

Use:

* `.pgpass` on Linux
* `pgpass.conf` on Windows

The Windows default location is commonly:

```text
%APPDATA%\postgresql\pgpass.conf
```

### MySQL and MariaDB

Use:

* MySQL login path
* MySQL defaults file

Ensure the credential file is accessible to the scheduled-task account.

### Oracle Data Pump

Use Oracle Wallet.

### Oracle RMAN

Use:

* Operating-system authentication
* Oracle Wallet

### Dell PowerProtect MSSQL

Use a Dell PowerProtect lockbox.

See:

* [Credential configuration](docs/credentials.md)
* [Restore configuration reference](docs/configuration.md)

## Alerts

Alert configuration is defined at the top level of the YAML file beside `tools:` and `jobs:`.

```yaml
alerts:
  enabled: false

  notify_on:
    success: true
    failure: true
    dry_run: false

  slack:
    enabled: false
    webhook_url_env: "DB_RESTORE_SLACK_WEBHOOK_URL"

  email:
    enabled: false
    smtp_host: "smtp.office365.com"
    smtp_port: 587
    username_env: "DB_RESTORE_SMTP_USERNAME"
    password_env: "DB_RESTORE_SMTP_PASSWORD"
    from_env: "DB_RESTORE_EMAIL_FROM"
    to:
      - "it-team@company.com"
```

To enable alerts:

1. Set `alerts.enabled: true`.
2. Enable Slack, email, or both.
3. Configure at least one `notify_on` event.
4. Define the referenced environment variables for the execution account.

### Alert routing

Normal restore results use:

* `notify_on.success`
* `notify_on.failure`

Dry-run alerts are controlled independently by:

```yaml
notify_on:
  dry_run: true
```

A dry-run alert does not also require `success: true` or `failure: true`.

### Slack

Slack notifications require an HTTPS webhook URL stored in the configured environment variable.

Linux:

```bash
export DB_RESTORE_SLACK_WEBHOOK_URL="https://hooks.slack.com/services/..."
```

Windows PowerShell:

```powershell
$env:DB_RESTORE_SLACK_WEBHOOK_URL = "https://hooks.slack.com/services/..."
```

Slack webhook redirects are not followed.

### Email

Email alerts require:

* SMTP server with STARTTLS
* SMTP authentication
* Username environment variable
* Password environment variable
* Sender environment variable
* At least one valid recipient

Linux:

```bash
export DB_RESTORE_SMTP_USERNAME="restore-alerts@company.com"
export DB_RESTORE_SMTP_PASSWORD="..."
export DB_RESTORE_EMAIL_FROM="restore-alerts@company.com"
```

Windows PowerShell:

```powershell
$env:DB_RESTORE_SMTP_USERNAME = "restore-alerts@company.com"
$env:DB_RESTORE_SMTP_PASSWORD = "..."
$env:DB_RESTORE_EMAIL_FROM = "restore-alerts@company.com"
```

The notifier refuses to send SMTP credentials or messages when the server does not advertise STARTTLS.

Each notifier runs independently. A Slack failure does not prevent an email notification, and an email failure does not prevent a Slack notification.

## Operational Recommendations

Before scheduling a restore job:

1. Validate the configuration.
2. Run a dry run.
3. Verify the selected backup file.
4. Verify the target database.
5. Verify credential-store access using the scheduler account.
6. Run one supervised manual restore.
7. Review the main log and provider logs.
8. Generate and review the operating-system schedule.
9. Enable alerts for failures.
10. Test recovery procedures regularly.

Never enable unattended restores against a target until its safety rules, credentials, backup selection, relocation paths, and restore behavior have been manually verified.
