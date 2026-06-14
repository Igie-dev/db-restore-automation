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

Scheduled jobs call the Go executable with `restore --config ... --job ...`.
