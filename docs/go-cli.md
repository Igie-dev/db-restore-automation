# Go CLI

`db-restore-automation` is the supported restore runner. The old Bash and PowerShell runners have been removed.

## Commands

```bash
db-restore-automation validate --config ./config/restore-jobs.linux.yml
db-restore-automation restore --config ./config/restore-jobs.linux.yml
db-restore-automation restore --config ./config/restore-jobs.linux.yml --job OracleRmanTestRestore --dry-run
db-restore-automation schedule linux --config ./config/restore-jobs.linux.yml --root-dir /opt/db-restore-automation
```

```powershell
.\db-restore-automation.exe validate --config .\config\restore-jobs.windows.yml
.\db-restore-automation.exe restore --config .\config\restore-jobs.windows.yml --job WideWorldImportersRestore --dry-run
.\db-restore-automation.exe schedule windows --config .\config\restore-jobs.windows.yml --root-dir C:\db-restore-automation
```

## Step-by-Step

1. Install Go 1.22 or newer.

2. Build the binary:

   ```bash
   go mod tidy
   go build -o db-restore-automation ./cmd/db-restore-automation
   ```

   Windows:

   ```powershell
   go mod tidy
   go build -o db-restore-automation.exe .\cmd\db-restore-automation
   ```

3. Edit YAML:

   - Linux: `config/restore-jobs.linux.yml`
   - Windows: `config/restore-jobs.windows.yml`

4. Set up native credentials:

   - PostgreSQL pgpass
   - MySQL login path or defaults file
   - Oracle Wallet or RMAN OS auth
   - PowerProtect lockbox

5. Configure alerts if needed. The `alerts:` block is top-level, beside `tools:` and `jobs:`.

6. Validate:

   ```bash
   ./db-restore-automation validate --config ./config/restore-jobs.linux.yml
   ```

7. Dry-run one job:

   ```bash
   ./db-restore-automation restore --config ./config/restore-jobs.linux.yml --job hris_postgres_restore --dry-run
   ```

8. Review `logs/restore.log`.

9. Run the restore:

   ```bash
   ./db-restore-automation restore --config ./config/restore-jobs.linux.yml --job hris_postgres_restore
   ```

10. Generate schedule commands:

    ```bash
    ./db-restore-automation schedule linux --config ./config/restore-jobs.linux.yml --root-dir /opt/db-restore-automation
    ```

## Exit Codes

- `0`: all selected enabled jobs succeeded
- `1`: one or more selected enabled jobs failed
- `2`: config, argument, or job-selection error

## Restore Flow

1. Load YAML.
2. Validate config and safety rules.
3. Select one job with `--job`, or all jobs when omitted.
4. Skip disabled jobs.
5. Resolve the latest backup file for file-based providers.
6. Skip backup resolution for `oracle_rman` and `mssql_powerprotect`.
7. Run the selected provider, or log the provider actions when `--dry-run` is set.
8. Log result, duration, stdout/stderr capture paths, and provider logs where available.
9. Send configured alerts after each selected enabled job.

## Restore-Only Boundary

The CLI never generates database backups. Backup files, RMAN command files, Oracle directory objects, native credential stores, and PowerProtect lockboxes must already exist.
