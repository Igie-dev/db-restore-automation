# Troubleshooting

## Build Tool Missing

Install Go and verify:

```bash
go version
gofmt -w cmd internal
go test ./...
```

## Config Validation Fails

Run:

```bash
./db-restore-automation validate --config ./config/restore-jobs.linux.yml
```

Check for missing required fields, unsupported credential methods, invalid job names, or safety tokens that match a target/source name.

## Restore Fails

Check `logs/restore.log` first. External command stdout and stderr are captured into temp files and their paths are logged.

For RMAN jobs, also check the configured `rman.log_file`.

## Dry Run

Use `--dry-run` before real restores:

```bash
./db-restore-automation restore --config ./config/restore-jobs.linux.yml --job hris_postgres_restore --dry-run
```

Dry runs skip destructive provider execution and confirmation prompts.
