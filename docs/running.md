# Running the Program

Follow this runbook from the repository root.

## 1. Install Go

Install Go 1.22 or newer and verify:

```bash
go version
```

## 2. Build

Linux or macOS:

```bash
go mod tidy
go build -o db-restore-automation ./cmd/db-restore-automation
```

Windows:

```powershell
go mod tidy
go build -o db-restore-automation.exe .\cmd\db-restore-automation
```

## 3. Configure YAML

Use one of:

- `config/restore-jobs.linux.yml`
- `config/restore-jobs.windows.yml`

Update:

- external tool paths under `tools`
- backup paths and file patterns
- restore target databases or schemas
- credential method fields
- safety block tokens
- schedules
- optional `alerts`

Do not store passwords in YAML.

## 4. Configure Alerts

The alert config is top-level:

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

For Slack:

```bash
export DB_RESTORE_SLACK_WEBHOOK_URL="https://hooks.slack.com/services/..."
```

For email:

```bash
export DB_RESTORE_SMTP_USERNAME="restore-alerts@company.com"
export DB_RESTORE_SMTP_PASSWORD="..."
export DB_RESTORE_EMAIL_FROM="restore-alerts@company.com"
```

On Windows, use `$env:NAME = "value"` for temporary session variables or configure persistent machine/user environment variables.

## 5. Validate

Linux:

```bash
./db-restore-automation validate --config ./config/restore-jobs.linux.yml
```

Windows:

```powershell
.\db-restore-automation.exe validate --config .\config\restore-jobs.windows.yml
```

## 6. Dry Run One Job

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

## 7. Review Logs

Check:

```text
logs/restore.log
```

External command stdout/stderr files are logged by path.

## 8. Run Restore

One job:

```bash
./db-restore-automation restore --config ./config/restore-jobs.linux.yml --job hris_postgres_restore
```

All enabled jobs:

```bash
./db-restore-automation restore --config ./config/restore-jobs.linux.yml
```

### Bounding runtime and running jobs in parallel

Add `--timeout` to cap each job, and `--concurrency` to restore several jobs at once so a slow or hung job does not block the rest:

```bash
./db-restore-automation restore \
  --config ./config/restore-jobs.linux.yml \
  --timeout 2h \
  --concurrency 2
```

`--timeout` applies only to jobs that have no `timeout:` in their config (per-job config wins). `--concurrency 1` (the default) runs jobs one at a time. Both flags apply to the manual `restore` command only — scheduled runs rely on the per-job `timeout:` field. See [configuration.md](configuration.md) and [scheduling.md](scheduling.md).

## 9. Generate Schedules

Linux:

```bash
./db-restore-automation schedule linux \
  --config ./config/restore-jobs.linux.yml \
  --root-dir /opt/db-restore-automation
```

Windows:

```powershell
.\db-restore-automation.exe schedule windows `
  --config .\config\restore-jobs.windows.yml `
  --root-dir C:\db-restore-automation
```

Install the generated commands into cron or Windows Task Scheduler.

## 10. Exit Codes

- `0`: all selected enabled jobs succeeded
- `1`: at least one selected enabled job failed
- `2`: config, argument, or job-selection error
