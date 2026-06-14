# db-restore-automation

Cross-platform, Go-based restore orchestration for existing database backups and Dell PowerProtect MSSQL restores.

This project is strictly **restore-only**. It never creates database backups.

## Supported Restores

- `postgres`: PostgreSQL restore with `psql` or `pg_restore`
- `mysql`: MySQL or MariaDB restore with `mysql`
- `oracle`: Oracle Data Pump restore with `impdp`
- `oracle_rman`: Oracle RMAN restore with a DBA-approved RMAN command file
- `mssql_powerprotect`: Dell PowerProtect MSSQL restore with `ddbmsqlrc.exe`

## Repository Layout

```text
cmd/db-restore-automation/   Go CLI entrypoint
internal/                    Go implementation packages
config/                      Environment-specific restore job YAML
examples/                    Sample restore job YAML
rman/                        RMAN sample command file
logs/                        Runtime logs
docs/                        Operator documentation
```

The legacy Bash and PowerShell runner implementation has been removed. Use the Go CLI for validation, restore execution, and schedule generation.

## Build

```bash
go mod tidy
go build -o db-restore-automation ./cmd/db-restore-automation
```

On Windows:

```powershell
go mod tidy
go build -o db-restore-automation.exe .\cmd\db-restore-automation
```

## Step-by-Step Run Guide

1. Install Go 1.22 or newer.

2. Build the CLI from the repository root:

   ```bash
   go mod tidy
   go build -o db-restore-automation ./cmd/db-restore-automation
   ```

   On Windows:

   ```powershell
   go mod tidy
   go build -o db-restore-automation.exe .\cmd\db-restore-automation
   ```

3. Choose the config file for the machine:

   - Linux: `config/restore-jobs.linux.yml`
   - Windows: `config/restore-jobs.windows.yml`

4. Edit tool paths, backup paths, target databases, schedules, and credentials in YAML. Do not put passwords in YAML.

5. Configure alerts in the top-level `alerts:` block if needed. It is present in both checked configs and is disabled by default.

6. Validate the config:

   ```bash
   ./db-restore-automation validate --config ./config/restore-jobs.linux.yml
   ```

7. Run a dry run for one job:

   ```bash
   ./db-restore-automation restore --config ./config/restore-jobs.linux.yml --job hris_postgres_restore --dry-run
   ```

8. Review `logs/restore.log`.

9. Run the restore:

   ```bash
   ./db-restore-automation restore --config ./config/restore-jobs.linux.yml --job hris_postgres_restore
   ```

10. Generate schedules only after manual validation and dry-run checks pass:

    ```bash
    ./db-restore-automation schedule linux --config ./config/restore-jobs.linux.yml --root-dir /opt/db-restore-automation
    ```

## Validate Config

```bash
./db-restore-automation validate --config ./config/restore-jobs.linux.yml
```

```powershell
.\db-restore-automation.exe validate --config .\config\restore-jobs.windows.yml
```

## Run Restores

Dry-run one job first:

```bash
./db-restore-automation restore \
  --config ./config/restore-jobs.linux.yml \
  --job OracleRmanTestRestore \
  --dry-run
```

Run one job:

```bash
./db-restore-automation restore \
  --config ./config/restore-jobs.linux.yml \
  --job hris_postgres_restore
```

Run all enabled jobs:

```bash
./db-restore-automation restore --config ./config/restore-jobs.linux.yml
```

The runner continues processing later selected jobs after a job failure. It exits `1` if any selected enabled job fails and `2` for config, argument, or job-selection errors.

## Scheduling

The CLI generates cron or Windows Task Scheduler commands. The operating system scheduler remains responsible for running them.

```bash
./db-restore-automation schedule linux \
  --config ./config/restore-jobs.linux.yml \
  --root-dir /opt/db-restore-automation
```

```powershell
.\db-restore-automation.exe schedule windows `
  --config .\config\restore-jobs.windows.yml `
  --root-dir C:\db-restore-automation
```

## Safety

- Restore targets come only from YAML.
- `safety.block_if_name_contains` blocks risky target/source names such as `prod`, `production`, and `live`.
- `safety.require_confirmation=true` requires interactive confirmation unless `--dry-run` is used.
- `--dry-run` logs actions without running destructive provider commands.
- Logs avoid passwords, Slack webhook URLs, SMTP passwords, wallet secrets, and lockbox secrets.

## Credentials

Do not store passwords in YAML.

- PostgreSQL: `pgpass` or `pgpass.conf`
- MySQL/MariaDB: `login_path` or `defaults_file`
- Oracle Data Pump: Oracle Wallet
- Oracle RMAN: OS authentication or Oracle Wallet
- Dell PowerProtect MSSQL: lockbox

See [docs/credentials.md](docs/credentials.md) and [docs/configuration.md](docs/configuration.md).

## Alerts

Alert configuration lives at the top level of each YAML config, beside `tools:` and `jobs:`.

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

Set `alerts.enabled: true`, enable Slack or email, then define the referenced environment variables on the machine running the CLI.
