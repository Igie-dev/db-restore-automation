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

Oracle RMAN accepts only connect strings that can never prompt for a password:

- `credential_method: os_auth` requires `target: "/"` (bequeath connection as the OS user running the CLI).
- `credential_method: oracle_wallet` requires `target: "/@<tns_alias>"`.
- `rman.catalog`, when configured, must always be a wallet string (`/@<tns_alias>`) regardless of the credential method.

Anything else — `user@alias`, `user/password@alias` — is rejected at `validate` time and again by the provider before RMAN is executed.

## Dell PowerProtect MSSQL

PowerProtect restores use a configured lockbox path. The CLI validates that the lockbox directory exists before invoking `ddbmsqlrc.exe`.
