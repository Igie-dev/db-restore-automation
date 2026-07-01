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

## Restore Hangs

A stuck provider process — for example `ddbmsqlrc` waiting on a Data Domain, or `rman` blocked on the Oracle host — otherwise runs forever. Bound each job with a wall-clock timeout:

- Per job: set `timeout:` in the YAML (e.g. `"6h"`). This is the only bound that applies to scheduled runs.
- Manual runs: pass `--timeout <duration>` as a default for jobs without their own `timeout:`.

When the timeout fires, the provider process is killed and the job is logged as `result=failure reason=restore_failed`. Set the value *above* the longest legitimate restore — too low a value kills a valid restore mid-flight (for `mssql_powerprotect` this can leave the database in a `RESTORING` state).

When running every job in one invocation, add `--concurrency <n>` so one slow or hung job does not block the others. Each job also logs a heartbeat every 5 minutes while a provider command runs, so a slow-but-progressing job can be told apart from a dead one.

## Dry Run

Use `--dry-run` before real restores:

```bash
./db-restore-automation restore --config ./config/restore-jobs.linux.yml --job hris_postgres_restore --dry-run
```

Dry runs skip destructive provider execution and confirmation prompts.
