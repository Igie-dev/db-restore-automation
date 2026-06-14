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
