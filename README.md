# db-restore-automation

Cross-platform, Go-based restore orchestration for existing database backups and Dell PowerProtect MSSQL restores.

This project is strictly **restore-only**. It never creates database backups.

## Supported Restore Providers

| Type | Provider | Supported input |
|---|---|---|
| `postgres` | PostgreSQL `psql` / `pg_restore` | `.sql`, `.sql.gz`, `.dump`, `.backup` |
| `mysql` | MySQL or MariaDB `mysql` | `.sql`, `.sql.gz` |
| `oracle` | Oracle Data Pump `impdp` | `.dmp` |
| `oracle_rman` | Oracle RMAN | DBA-approved RMAN command file |
| `mssql_powerprotect` | Dell PowerProtect `ddbmsqlrc.exe` | PowerProtect-managed backup |

Only the tools and files required by **enabled jobs** must be installed on a machine.

## Repository Layout

```text
cmd/db-restore-automation/   Go CLI entrypoint
internal/                    Go implementation packages
config/                      Environment-specific restore job YAML files
examples/                    Sample restore job YAML files
rman/                        RMAN command files, when RMAN is used
logs/                        Main runtime logs
docs/                        Operator documentation
```

The legacy Bash and PowerShell restore runners have been removed.

The Go CLI is used for:

- Configuration validation
- Dry-run validation
- Manual restore execution
- Linux cron generation
- Windows Task Scheduler script generation
- Slack and email notifications

## Build Requirements

Install the Go version declared in `go.mod`, or a newer compatible version.

The target machine does **not** need Go after the executable has been built.

Restore tools must still be installed for enabled providers:

- PostgreSQL: `psql`, `pg_restore`, `dropdb`, and `createdb`
- MySQL/MariaDB: `mysql`
- Oracle Data Pump: `impdp`
- Oracle RMAN: `rman`
- Dell PowerProtect MSSQL: `ddbmsqlrc.exe`

The Windows or Linux account running the CLI or scheduled task must have access to:

- Restore tool executables
- Backup files and backup directories
- Target database servers
- Credential stores
- Oracle Wallets, when used
- Dell PowerProtect lockboxes, when used
- Application and provider log directories

## Build the CLI

Run the build command from the repository root, where `go.mod` exists.

### Linux

```bash
go mod tidy
go build -o db-restore-automation ./cmd/db-restore-automation
```

### Windows PowerShell

Use forward slashes in the Go package path:

```powershell
go mod tidy
go build -o db-restore-automation.exe ./cmd/db-restore-automation
```

Verify the executable:

```powershell
.\db-restore-automation.exe --help
```

## Windows Deployment

For a Windows installation without RMAN jobs, the minimum application files are:

```text
C:\db-restore\
├── db-restore-automation.exe
├── config\
│   └── restore-jobs.windows.yml
├── logs\
└── install-db-restore-tasks.ps1
```

The generated task-installation script is required only when installing or updating scheduled tasks.

You do not need to deploy:

```text
cmd\
internal\
*.go
go.mod
go.sum
docs\
examples\
rman\
```

The external tools, lockbox, credential files, and database connectivity are separate machine dependencies and must match the paths in the YAML configuration.

## CLI Commands

```text
db-restore-automation interactive

db-restore-automation validate --config <config-file>

db-restore-automation restore \
  --config <config-file> \
  [--job <job-name>] \
  [--dry-run] \
  [--timeout <duration>] \
  [--concurrency <n>]

db-restore-automation schedule linux \
  --config <config-file> \
  --root-dir <root-directory>

db-restore-automation schedule windows \
  --config <config-file> \
  --root-dir <root-directory>
```

### Interactive mode

For ad-hoc operation you do not need to remember flags. Run the guided menu:

```text
db-restore-automation interactive
```

Running the executable with no arguments from a terminal launches the same menu
automatically. (When stdin is redirected — scripts, pipes, cron — the tool
prints usage instead of waiting for input, so automation is unaffected.)

The menu walks you through each action and lets you pick a restore job from a
titled list instead of typing its name:

```text
Available jobs:
   1) hris_postgres_restore      [postgres          ] enabled
   2) inventory_mysql_restore    [mysql             ] disabled
   3) accounting_oracle_restore  [oracle            ] disabled
   4) sales_rman_restore         [oracle_rman       ] disabled
   A) all enabled jobs           (run every enabled job)
   0) Back
```

Restores default to a dry run and ask for confirmation before any real change.
Interactive mode only collects your choices; it then runs the same
`validate`, `restore`, and `schedule` logic as the flag-based commands, so
safety checks and confirmation rules are identical.

Display command help:

```powershell
.\db-restore-automation.exe restore --help
```

```powershell
.\db-restore-automation.exe schedule windows --help
```

## Recommended Windows Workflow

Run the following commands from the deployed application directory:

```powershell
cd C:\db-restore
```

### 1. Validate the configuration

```powershell
.\db-restore-automation.exe validate `
  --config .\config\restore-jobs.windows.yml
```

Do not continue until validation succeeds.

### 2. Dry-run one job

```powershell
.\db-restore-automation.exe restore `
  --config .\config\restore-jobs.windows.yml `
  --job AdventureWorksRestore `
  --dry-run
```

Another example:

```powershell
.\db-restore-automation.exe restore `
  --config .\config\restore-jobs.windows.yml `
  --job WideWorldImportersRestore `
  --dry-run
```

A dry run does not execute destructive provider commands, but it can still validate:

- Configuration values
- Safety rules
- Backup-file selection
- Backup-file readability
- Backup extensions
- Gzip contents
- Credential configuration
- Tool paths represented in configuration
- Oracle command files and directories
- Provider command arguments

A dry run can therefore fail when configuration, backup, or credential-related requirements are missing.

## Manual Restore

A manual restore runs immediately from PowerShell and does not wait for Task Scheduler.

### Important before a manual restore

If the same job already has a scheduled task, disable that task temporarily. This prevents a scheduled run from starting while the manual restore is still running.

```powershell
Disable-ScheduledTask `
  -TaskName "DB Restore - AdventureWorksRestore"
```

### Run one job manually

```powershell
cd C:\db-restore

.\db-restore-automation.exe restore `
  --config .\config\restore-jobs.windows.yml `
  --job AdventureWorksRestore
```

Run the other job manually:

```powershell
.\db-restore-automation.exe restore `
  --config .\config\restore-jobs.windows.yml `
  --job WideWorldImportersRestore
```

When `safety.require_confirmation` is `true`, type the exact job name when prompted.

For unattended scheduled jobs, the YAML must explicitly contain:

```yaml
safety:
  require_confirmation: false
```

### Run all enabled jobs manually

Omit `--job`:

```powershell
.\db-restore-automation.exe restore `
  --config .\config\restore-jobs.windows.yml
```

The CLI continues with later selected jobs after an individual job failure. It stops starting new jobs when the process is cancelled.

By default all jobs run sequentially — one job must finish (or hit its timeout) before the next starts. To restore several at once so a slow or hung job does not block the rest, add `--concurrency <n>` (a bounded worker pool). Add `--timeout <duration>` to cap any job that lacks its own `timeout:` field:

```powershell
.\db-restore-automation.exe restore `
  --config .\config\restore-jobs.windows.yml `
  --timeout 6h `
  --concurrency 2
```

Running all jobs can be dangerous when jobs target the same database server or share restore resources. Prefer running and verifying one job at a time, and keep `--concurrency` low when jobs share infrastructure such as a single Data Domain appliance.

### Monitor the application log

Open another PowerShell window:

```powershell
Get-Content C:\db-restore\logs\restore.log -Wait
```

Show the latest 150 lines:

```powershell
Get-Content C:\db-restore\logs\restore.log -Tail 150
```

Command stdout and stderr are written to temporary files. Their paths are recorded in `restore.log`.

### Re-enable the scheduled task

After the manual test completes:

```powershell
Enable-ScheduledTask `
  -TaskName "DB Restore - AdventureWorksRestore"
```

## Manually Trigger a Scheduled Task

To test the exact Task Scheduler configuration instead of running the CLI directly:

```powershell
Start-ScheduledTask `
  -TaskName "DB Restore - AdventureWorksRestore"
```

Check its result:

```powershell
Get-ScheduledTaskInfo `
  -TaskName "DB Restore - AdventureWorksRestore" |
  Select-Object LastRunTime, LastTaskResult, NextRunTime
```

A successful CLI execution normally produces:

```text
LastTaskResult : 0
```

A nonzero value means the executable returned an error. Review:

```powershell
Get-Content C:\db-restore\logs\restore.log -Tail 150
```

## Generate and Install Windows Scheduled Tasks

Generate the script using the **actual deployed root directory**:

```powershell
cd C:\db-restore

.\db-restore-automation.exe schedule windows `
  --config .\config\restore-jobs.windows.yml `
  --root-dir C:\db-restore |
  Set-Content .\install-db-restore-tasks.ps1 -Encoding UTF8
```

Review the generated paths:

```powershell
Select-String `
  -Path .\install-db-restore-tasks.ps1 `
  -Pattern "exePath|configPath|workingDirectory|taskName|taskTrigger"
```

Expected application paths:

```text
C:\db-restore\db-restore-automation.exe
C:\db-restore\config\restore-jobs.windows.yml
C:\db-restore
```

Install or update the tasks:

```powershell
.\install-db-restore-tasks.ps1
```

Verify them:

```powershell
Get-ScheduledTask -TaskName "DB Restore - *" |
  Select-Object TaskName, State,
    @{Name="User"; Expression={$_.Principal.UserId}},
    @{Name="LogonType"; Expression={$_.Principal.LogonType}}
```

Check task run information:

```powershell
Get-ScheduledTaskInfo `
  -TaskName "DB Restore - AdventureWorksRestore"

Get-ScheduledTaskInfo `
  -TaskName "DB Restore - WideWorldImportersRestore"
```

Windows schedules currently support:

```yaml
windows_frequency: "DAILY"
```

Example:

```yaml
schedule:
  enabled: true
  linux_cron: ""
  windows_time: "15:00"
  windows_frequency: "DAILY"
```

Generated tasks:

- Use the configured application root as the working directory
- Use absolute executable and configuration paths
- Start missed tasks when the computer becomes available
- Prevent overlapping instances of the **same task**
- Replace an existing task with the same generated name

Two different scheduled tasks can still overlap. Do not schedule separate restore jobs only a few minutes apart unless concurrent execution has been tested and approved.

## When to Rebuild or Regenerate

### YAML-only changes

You do **not** need to rebuild the executable when changing:

- Source client
- Source database
- Target database
- Data Domain values
- Device path
- Relocation paths
- Backup directory or pattern
- Safety rules
- Alert settings
- Credential-method configuration

The executable reads the YAML every time it starts.

After a YAML-only change:

```powershell
.\db-restore-automation.exe validate `
  --config .\config\restore-jobs.windows.yml
```

Then run a dry run or supervised manual restore.

### Go source-code changes

Rebuild when any `.go` file changes:

```powershell
go build -o db-restore-automation.exe ./cmd/db-restore-automation
```

Stop or disable scheduled tasks before replacing an executable that might currently be running.

Copy the newly built executable to:

```text
C:\db-restore\db-restore-automation.exe
```

### Schedule changes

Regenerate and reinstall scheduled tasks when changing:

- Job name
- Schedule time
- Schedule frequency
- Whether scheduling is enabled
- Application root directory
- Config file path

Changing provider values in YAML does not require task regeneration when the job name and paths remain unchanged.

## Configuration Validation

The YAML loader checks:

- Exactly one YAML document
- A mapping/object at the root
- Known configuration fields only
- Maximum configuration-file size
- No direct password fields
- A nonempty top-level `jobs` collection
- Unique job names, case-insensitively
- Supported provider types
- Required provider-specific values for enabled jobs
- Valid ports, schedules, credential methods, and alert settings

Disabled jobs may remain incomplete templates. Their name, type, enabled flag, and safety configuration must still be valid.

## Exit Codes

| Exit code | Meaning |
|---|---|
| `0` | Command completed successfully |
| `1` | Restore failure, safety failure, validation failure, or cancellation |
| `2` | CLI usage, configuration loading, job-selection, or schedule-generation error |

## Safety

Every enabled job is checked before provider execution.

### Blocked-name rules

`safety.block_if_name_contains` is matched case-insensitively against target-oriented values, including:

- Job name
- Target database identifiers
- Target host or connect string where applicable
- RMAN target and Oracle SID
- MSSQL target database

Example:

```yaml
safety:
  require_confirmation: false
  block_if_name_contains:
    - "prod"
    - "production"
    - "live"
```

A matching token blocks the restore before the provider command runs.

### Interactive confirmation

Destructive restores require confirmation by default.

For an approved unattended scheduled job:

```yaml
safety:
  require_confirmation: false
```

When confirmation is required:

- Dry-run skips the prompt
- Manual execution requires the exact job name
- Generic values such as `yes` are not accepted
- Non-interactive scheduled execution fails safely

This environment variable can force confirmation globally:

```text
REQUIRE_CONFIRMATION=true
```

Setting it to `false` does not disable confirmation required by a job.

## Credentials

Never store passwords directly in YAML.

### PostgreSQL

Use:

- `.pgpass` on Linux
- `pgpass.conf` on Windows

The Windows credential file normally belongs to the account running the task:

```text
%APPDATA%\postgresql\pgpass.conf
```

### MySQL and MariaDB

Use:

- MySQL login path
- MySQL defaults file

### Oracle Data Pump

Use Oracle Wallet.

### Oracle RMAN

Use:

- Operating-system authentication
- Oracle Wallet

### Dell PowerProtect MSSQL

Use the Dell PowerProtect lockbox.

The scheduled task must run under an account permitted to access the lockbox and PowerProtect client installation.

## PostgreSQL Restore Behavior

Supported formats:

- `.sql`
- `.sql.gz`
- `.dump`
- `.backup`

Before destructive execution, the provider validates and prepares the backup. For custom-format backups, it runs a `pg_restore --list` preflight.

The restore sequence is:

1. Terminate active target-database sessions
2. Drop the target database
3. Recreate the target database
4. Restore the selected backup

## MySQL and MariaDB Restore Behavior

Supported formats:

- `.sql`
- `.sql.gz`

The provider opens and validates the backup before dropping the target database.

The restore sequence is:

1. Validate the backup stream
2. Drop the target database
3. Recreate the target database
4. Stream SQL into the target database

Supported credential methods:

- `login_path`
- `defaults_file`

## Oracle Data Pump Restore Behavior

Oracle Data Pump uses `impdp` and accepts `.dmp` files.

Only the dump filename is passed to `impdp`. The dump file must be available in the server-side filesystem location represented by the configured Oracle DIRECTORY object.

## Oracle RMAN Restore Behavior

RMAN executes a DBA-approved command file.

Supported credential methods and the connect strings they require:

| `credential_method` | `rman.target`     | `rman.catalog` (optional) |
|---------------------|-------------------|---------------------------|
| `os_auth`           | exactly `/`       | `/@<tns_alias>`           |
| `oracle_wallet`     | `/@<tns_alias>`   | `/@<tns_alias>`           |

Any other connect-string form (for example `user@alias` or
`user/password@alias`) is rejected by `validate` and again at restore time.
RMAN reads passwords from stdin, which the automation never provides, so a
prompting connect string could only fail at run time with a confusing EOF
error.

Currently supported scope:

```yaml
restore_scope: "full_database"
```

RMAN command files must not contain passwords.

Machines with no enabled RMAN jobs do not need the repository `rman` folder.

### Command files and the controlfile bootstrap

The repository `rman/` folder contains:

- `restore-full-database.sample.rman` — the empty skeleton a DBA fills in.
- `restore-hobs2pro-powerprotect.rman` — a complete full-database restore
  through Dell PowerProtect DD Boost (`SBT_TAPE` channels) showing every
  value in context.

A full restore always starts by restoring the controlfile, because the
controlfile is RMAN's own index of all backups — until it is back, RMAN has
nothing to consult. Autobackup pieces have a predictable name,
`c-<DBID>-<YYYYMMDD>-<sequence>`, so with `SET DBID` followed by
`RESTORE CONTROLFILE FROM AUTOBACKUP` RMAN finds the newest piece itself and
no per-run manual value is needed. After `ALTER DATABASE MOUNT`,
`RESTORE DATABASE` and `RECOVER DATABASE` select the latest usable backups
automatically from the records inside the restored controlfile, and the
database is opened with `RESETLOGS`.

### Example RMAN job

```yaml
tools:
  oracle_rman:
    # A bare "rman" is resolved against <oracle_home>/bin automatically (see
    # below). Provide an absolute path here only to override that resolution.
    rman: "rman"

jobs:
  - name: sales_rman_restore
    enabled: true
    type: oracle_rman

    schedule:
      enabled: true
      linux_cron: "0 2 * * *"
      windows_time: "02:00"
      windows_frequency: "DAILY"

    rman:
      target: "/"                       # OS authentication; use "/@TNS_ALIAS" for a wallet
      command_file: "/opt/db-restore/rman/restore_sales.rman"
      log_file: "/opt/db-restore/logs/sales_rman.log"
      credential_method: "os_auth"
      oracle_home: "/u01/app/oracle/product/19c/dbhome_1"
      oracle_sid: "SALESTST"
      restore_scope: "full_database"

    safety:
      require_confirmation: false
      block_if_name_contains:
        - "prod"
        - "production"
        - "live"
```

The `target` and `command_file` / `log_file` paths must not contain inline
passwords. `command_file` and `log_file` must resolve to different paths.

A dry run (`--dry-run`) validates the command file, `oracle_home`, and log
target without executing RMAN, and logs a warning when the `rman` binary
cannot be found on the host — so a dry run on the Oracle server is a real
preflight.

For unattended (cron) execution, set `safety.require_confirmation: false` on
the job: confirmation prompts cannot be answered without a terminal, and the
generated crontab marks jobs that would block on one.

### Executable and library resolution (Linux)

The provider exports `ORACLE_HOME`, `ORACLE_SID`, and `PATH` (with
`<oracle_home>/bin` prepended) to the RMAN process. On Linux it also prepends
`<oracle_home>/lib` to `LD_LIBRARY_PATH` so the dynamically linked `rman` binary
can load the Oracle client libraries.

When `tools.oracle_rman.rman` is a bare command name (no path separator), the
binary is located at `<oracle_home>/bin/rman` (`rman.exe` on Windows) when that
file exists, and otherwise falls back to a `PATH` lookup. This means a correctly
configured `oracle_home` is enough to run RMAN even when `rman` is not on the
service account's `PATH`. Provide an absolute path in `tools.oracle_rman.rman`
to bypass this resolution entirely.

## Dell PowerProtect MSSQL Restore Behavior

PowerProtect restores use `ddbmsqlrc.exe`.

Required job information includes:

- Data Domain host
- Data Domain user
- Device path
- Lockbox path
- Source client
- Source database
- Target database
- Restore type
- Relocation mappings

Example:

```yaml
powerprotect:
  dd_host: "192.168.20.251"
  dd_user: "sql-ppdm-user"
  device_path: "/sql-ppdm-user/device-path"
  lockbox_path: "C:\\Program Files\\DPSAPPS\\common\\lockbox"
  client: "actual-source-sql-server.company.local"
  skip_client_resolution: true
  restore_type: "normal"
  credential_method: "lockbox"
```

The `client` value must match the source client identity used when the backup was created, especially when `skip_client_resolution` is enabled.

Each logical SQL Server file must have a unique destination:

```yaml
relocate:
  - logical_name: "AdventureWorks"
    physical_path: "C:\\Program Files\\Microsoft SQL Server\\MSSQL15.MSSQLSERVER\\MSSQL\\DATA\\AdventureWorks.mdf"

  - logical_name: "AdventureWorks_log"
    physical_path: "C:\\Program Files\\Microsoft SQL Server\\MSSQL15.MSSQLSERVER\\MSSQL\\DATA\\AdventureWorks_log.ldf"
```

### `No full backup was found`

When the provider connects successfully but reports:

```text
No full backup was found!
XBSA object not found.
```

the CLI and scheduler have already started correctly. Recheck the PowerProtect backup-selection values:

- Source client
- Source database
- SQL instance identity
- Data Domain host
- Data Domain user
- Device path
- Backup workflow and restore arguments

After correcting YAML values, validate and run the job manually. A YAML-only correction does not require rebuilding the executable.

## Alerts

Alert configuration is defined beside `tools:` and `jobs:`.

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

Dry-run alerts are controlled independently by `notify_on.dry_run`.

Slack webhook URLs and SMTP passwords must be stored in environment variables, not YAML.

Email delivery requires STARTTLS and SMTP authentication.

## Linux Scheduling

Generate cron output:

```bash
./db-restore-automation schedule linux \
  --config ./config/restore-jobs.linux.yml \
  --root-dir /opt/db-restore-automation
```

Save and review it:

```bash
./db-restore-automation schedule linux \
  --config ./config/restore-jobs.linux.yml \
  --root-dir /opt/db-restore-automation \
  > generated-restore.cron

cat generated-restore.cron
```

Install it only after review:

```bash
crontab generated-restore.cron
```

## Operational Checklist

Before enabling an unattended restore:

1. Validate the configuration.
2. Run a dry run.
3. Verify the selected backup or PowerProtect source.
4. Verify the destination database and relocation paths.
5. Verify credentials using the scheduled-task account.
6. Disable the scheduled task temporarily.
7. Run one supervised manual restore.
8. Review the main and provider logs.
9. Verify the restored database.
10. Generate or update the schedule.
11. Enable the scheduled task.
12. Test alert delivery.
13. Periodically test the restore process.

Never enable unattended restores until the target, credentials, backup selection, relocation paths, safety rules, and restore behavior have been manually verified.
