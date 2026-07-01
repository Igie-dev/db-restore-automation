# Scheduling

The Go CLI is not a daemon. Use cron or Windows Task Scheduler.

## Linux

Generate cron lines:

```bash
./db-restore-automation schedule linux \
  --config ./config/restore-jobs.linux.yml \
  --root-dir /opt/db-restore-automation
```

Install the generated lines into the account crontab that should run restores.

## Windows

Generate Task Scheduler commands:

```powershell
.\db-restore-automation.exe schedule windows `
  --config .\config\restore-jobs.windows.yml `
  --root-dir C:\db-restore-automation
```

Run the generated commands from an elevated PowerShell session when required by your environment.

### Frequency

`windows_frequency` is `DAILY` (default) or `MONTHLY`. For `MONTHLY`, add `day_of_month` (1–31); the generated script builds a `MSFT_TaskMonthlyTrigger`:

```yaml
schedule:
  enabled: true
  windows_time: "14:10"
  windows_frequency: "MONTHLY"
  day_of_month: 2
```

On Linux, express monthly timing directly in `linux_cron` (e.g. `10 14 2 * *`).

Scheduled jobs call the Go executable with `restore --config ... --job ...` — one task per job, with no `--timeout` or `--concurrency` flags. Two consequences:

- To bound a scheduled job's runtime, set the per-job `timeout:` field in the YAML. The CLI `--timeout` flag is not part of the generated command and never reaches scheduled runs.
- `--concurrency` does not apply: each job is its own cron entry / scheduled task. If you schedule two jobs at the same time, the OS runs them concurrently with no built-in cap — stagger their times if they share infrastructure (for example a single Data Domain appliance).
