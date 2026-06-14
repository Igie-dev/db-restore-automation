# Dell PowerProtect MSSQL

`mssql_powerprotect` jobs restore MSSQL databases through Dell PowerProtect and `ddbmsqlrc.exe`.

Required YAML fields:

- `tools.mssql_powerprotect.ddbmsqlrc`
- `powerprotect.dd_host`
- `powerprotect.dd_user`
- `powerprotect.device_path`
- `powerprotect.lockbox_path`
- `powerprotect.client`
- `powerprotect.credential_method: lockbox`
- `source.database`
- `target.database`
- at least one `relocate` entry

The relocation map is built as:

```text
'LogicalName1'='C:\Path\File1.mdf','LogicalName2'='C:\Path\File2.ldf'
```

The CLI invokes `ddbmsqlrc.exe` directly with argument arrays. It does not use `cmd.exe`.

PowerProtect jobs skip normal `backup_path` and `file_pattern` lookup.
