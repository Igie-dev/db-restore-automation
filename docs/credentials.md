# Credentials

Do not store passwords in YAML, logs, command-line arguments, or scripts.

The Go CLI preserves the existing credential model:

- PostgreSQL: `pgpass`
- MySQL/MariaDB: `login_path` or `defaults_file`
- Oracle Data Pump: `oracle_wallet`
- Oracle RMAN: `os_auth` or `oracle_wallet`
- Dell PowerProtect MSSQL: `lockbox`

## PostgreSQL

Set up `.pgpass` or `pgpass.conf` for the account that runs the CLI. The CLI passes host, port, username, and database to PostgreSQL tools, but never a password.

## MySQL or MariaDB

Use either:

- `target.login_path`
- `target.defaults_file`

When `defaults_file` is used, the CLI passes it as `--defaults-extra-file`.

## Oracle

Oracle Data Pump uses wallet-style connect strings such as `/@ACCOUNTING_TEST`.

Oracle RMAN supports OS authentication with `target: "/"` or wallet-based target/catalog strings supplied by the DBA.

## Dell PowerProtect MSSQL

PowerProtect restores use a configured lockbox path. The CLI validates that the lockbox directory exists before invoking `ddbmsqlrc.exe`.
