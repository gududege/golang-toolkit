# scan-calls

Scan Siemens TIA Portal block files and output a cross-reference call graph as JSON.

## Usage

```bash
scan-calls [flags]
```

Scan the default directories (`udts/`, `blocks/`) and write `block_calls.json`:

```bash
scan-calls
```

Scan custom directories and write to a specific file:

```bash
scan-calls -p udts,blocks/Function -o result.json
```

## Flags

| Short | Long              | Default           | Description                              |
|-------|-------------------|-------------------|------------------------------------------|
| `-o`  | `--output-file`   | `block_calls.json`| Path to the output JSON file             |
| `-p`  | `--scan-paths`    | `udts,blocks`     | Comma-separated directories to scan      |
| `-v`  | `--version`       |                   | Show version                             |
| `-h`  | `--help`          |                   | Show this help message                   |

## Output

The output is a JSON array where each element represents a block:

```json
{
  "name": "FB_Init",
  "type": "FB",
  "use": [
    { "name": "FC_Util", "type": "FC" }
  ]
}
```

### Block types

| Type | Description                        |
|------|------------------------------------|
| OB   | Organization block                 |
| FB   | Function block                     |
| FC   | Function                           |
| DB   | Data block (with VAR/STRUCT)       |
| DI   | Instance data block                |
| UDT  | User-defined type                  |

## Version

```bash
scan-calls -v
```

The version is injected at build time:

```bash
go build -ldflags="-X main.version=1.0.0" ./scan-calls
```
