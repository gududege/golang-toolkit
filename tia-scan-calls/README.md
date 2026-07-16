# tia-scan-calls

Scan Siemens TIA Portal block files and output a cross-reference call graph as JSON.

## Usage

```bash
tia-scan-calls [flags]
```

Scan custom directories and write to a specific file:

```bash
tia-scan-calls -p udts -p blocks/Function -o result.json
```

## Flags

| Short | Long            | Description                                         |
|-------|-----------------|-----------------------------------------------------|
| `-p`  | `--scan-path`   | Directories to scan (can be specified multiple times) |
| `-o`  | `--output-file` | Path to the output JSON file                        |
| `-v`  | `--version`     | Show version                                        |
| `-h`  | `--help`        | Show this help message                              |

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

| Type | Description                  |
|------|------------------------------|
| OB   | Organization block           |
| FB   | Function block               |
| FC   | Function                     |
| DB   | Data block (with VAR/STRUCT) |
| DI   | Instance data block          |
| UDT  | User-defined type            |
| UNKNOWN | Unrecognized block type   |

## Version

```bash
tia-scan-calls -v
```

The version is injected at build time:

```bash
go build -ldflags="-X main.version=1.0.0" ./tia-scan-calls
```
