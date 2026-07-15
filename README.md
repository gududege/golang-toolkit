# golang-toolkit

A collection of small Go utility programs, each in its own module under a shared workspace. New tools are added as independent directories.

## Tools

| Tool                                                    | Description                                                                             |
|---------------------------------------------------------|-----------------------------------------------------------------------------------------|
| [tia-scan-calls](./tia-scan-calls/)                     | Scan Siemens TIA Portal block files, detect cross-references, output JSON call graph.   |
| [tia-tree-calls](./tia-tree-calls/)                     | Render a visual call hierarchy tree from the JSON produced by tia-scan-calls.           |
| [cli-print](./cli-print/)                               | Test/debug tool that prints all CLI input: args, env, CWD, and exit code.               |
| [dotnet-runtime-extractor](./dotnet-runtime-extractor/) | Download and extract .NET runtime from official SDK archives.                           |
| [track-repository](./track-repository/)                 | Enrich GitHub repository metadata (stars, forks, languages) via GraphQL API.            |

## Project structure

```text
golang-toolkit/
├── go.work                         # Go workspace root
├── .github/workflows/
│   └── release.yml                 # GitHub Actions — build & publish on tag push
├── tool-1/                         # Module with its own go.mod
│   ├── .goreleaser.yaml            # Per-module GoReleaser config
│   ├── README.md
│   └── main.go
├── tool-2/
│   ├── .goreleaser.yaml
│   ├── README.md
│   └── main.go
└── ...
```

Each module is a standalone `main` package with its own `go.mod` and a self-contained `.goreleaser.yaml` for cross-compilation, version injection (`-X main.version`), and UPX compression.

## Build

Requires Go 1.25+. Build from the workspace root:

```bash
go build ./tool-1
go build ./tool-2
```

Or inside a module directory:

```bash
cd tool && go build
```

Inject version (for `--version` output):

```bash
go build -ldflags="-X main.version=1.0.0" ./tool
```

## Release

On tag push (`{module}/v*`), the [release workflow](./.github/workflows/release.yml) builds the module, compresses with UPX, and publishes artifacts to GitHub Releases.

```bash
git tag tia-scan-calls/v1.0.0
git push origin tia-scan-calls/v1.0.0
```

Each module's `.goreleaser.yaml` independently controls its build, archive, and UPX compression settings.
