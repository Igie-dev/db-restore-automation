# Configuration

The CLI reads YAML restore job files from `config/` or any path passed with `--config`.

Top-level sections:

- `tools`: external executable paths
- `alerts`: optional Slack and email notification settings
- `jobs`: restore jobs

Supported job types:

- `postgres`
- `mysql`
- `oracle`
- `oracle_rman`
- `mssql_powerprotect`

## Validation Rules

Every job requires:

- `name`
- `enabled`
- `type`

Job names must match:

```text
^[A-Za-z0-9_.-]+$
```

File-based jobs require `backup_path` and `file_pattern`. RMAN and PowerProtect jobs do not use normal backup file lookup.

### Optional per-job fields

- `timeout`: a wall-clock ceiling for the job, written as a Go duration string (`"2h"`, `"90m"`, `"1h30m"`). When the restore exceeds it, the provider process is killed and the job is marked failed. Omit it for no limit. Set it comfortably *above* the longest legitimate restore — a value below the real duration kills a valid restore mid-flight (for `mssql_powerprotect` this can leave the target database in a `RESTORING` state). Validation rejects a non-positive or unparsable value. A per-job `timeout` overrides the `--timeout` CLI default, and it is the only runtime bound that applies to scheduled jobs, which do not receive CLI flags.

```yaml
jobs:
  - name: WideWorldImportersRestore
    enabled: true
    type: mssql_powerprotect
    timeout: "6h"
    # ...
```

### Schedule fields

The `schedule` block drives the generated cron / Task Scheduler entries:

- `enabled`: include the job in generated schedules.
- `linux_cron`: a five-field cron expression for Linux (`schedule linux`). Express day-of-month directly here — e.g. `10 14 2 * *` runs at 14:10 on the 2nd.
- `windows_time`: `HH:MM` 24-hour start time (Windows).
- `windows_frequency`: `DAILY` (default) or `MONTHLY`.
- `day_of_month`: required when `windows_frequency: MONTHLY`; the day of the month (1–31) the task runs. Only valid with `MONTHLY`. Days 29–31 do not fire in months that lack them.

```yaml
schedule:
  enabled: true
  windows_time: "14:10"
  windows_frequency: "MONTHLY"
  day_of_month: 2
```

A `MONTHLY` Windows schedule generates a `MSFT_TaskMonthlyTrigger` (via CIM) because `New-ScheduledTaskTrigger` has no monthly option; `DAILY` uses `New-ScheduledTaskTrigger -Daily`.

## Provider Notes

PostgreSQL jobs use `pgpass` and restore `.sql`, `.dump`, or `.backup` files.

MySQL jobs use `login_path` or `defaults_file` and restore `.sql` or `.sql.gz` files. Gzip decompression is handled by Go and streamed to `mysql`.

Oracle Data Pump jobs use Oracle Wallet and `.dmp` files. The dump file name is passed to `impdp`; the file must exist in the server-side Oracle directory object path.

Oracle RMAN jobs use `rman.command_file`. The command file must be reviewed and approved by an Oracle DBA.

Dell PowerProtect MSSQL jobs use `ddbmsqlrc.exe`, lockbox credentials, source/target database fields, and at least one relocation mapping.

## Alerts

`alerts` is optional and lives at the top level of the YAML file, beside `tools:` and `jobs:`.

The checked configs include this block disabled by default. Secrets are read from environment variables named in YAML, not from YAML values.

```yaml
alerts:
  enabled: false
  notify_on:
    success: true
    failure: true
    dry_run: false
  slack:
    enabled: true
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

To enable Slack:

1. Set `alerts.enabled: true`.
2. Set `alerts.slack.enabled: true`.
3. Set the environment variable named by `alerts.slack.webhook_url_env`.

Example:

```bash
export DB_RESTORE_SLACK_WEBHOOK_URL="https://hooks.slack.com/services/..."
```

To enable email:

1. Set `alerts.enabled: true`.
2. Set `alerts.email.enabled: true`.
3. Set the environment variables named by `username_env`, `password_env`, and `from_env`.
