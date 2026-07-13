# dotnet-runtime-extractor

A Go tool to extract .NET runtime components from official SDK archives.

## Features

- Download .NET SDK from official Microsoft releases
- Extract runtime, host fxr, and dotnet.exe to target directory
- Support fuzzy version (e.g., `10`) or exact version (e.g., `10.0.5`)
- Skip download if local file hash matches (SHA512 verification)
- Configurable runtime extraction (aspnet, desktop, all)

## Installation

```bash
go build -o dotnet-runtime-extractor.exe main.go
```

## Usage

```bash
# Download and extract all runtimes (default)
dotnet-runtime-extractor.exe -release-version 10

# Download and extract only ASP.NET Core runtime
dotnet-runtime-extractor.exe -release-version 10 -runtime aspnet

# Download and extract only Desktop runtime
dotnet-runtime-extractor.exe -release-version 10 -runtime desktop

# Extract from local SDK archive
dotnet-runtime-extractor.exe -file dotnet-sdk-10.0.5-win-x64.zip

# Specify target directory
dotnet-runtime-extractor.exe -release-version 10 -target ./my-runtime

# Cross-platform download
dotnet-runtime-extractor.exe -release-version 10 -os linux -arch x64
```

## Options

| Flag | Description | Default |
|------|-------------|---------|
| `-release-version` | .NET release version (e.g., 10, 10.0, 10.0.5) | - |
| `-file` | Path to local SDK archive | - |
| `-runtime` | Runtime type: aspnet, desktop, all | all |
| `-os` | Target platform: windows, linux, osx | windows |
| `-arch` | Target architecture: x64, arm64 | amd64 |
| `-target` | Extraction target directory | runtime |

## Output Structure

```
target/
├── dotnet.exe                          # Main dotnet executable
├── dotnet-sdk-*.zip                    # Downloaded SDK archive
├── host/
│   └── fxr/
│       └── 10.0.x/
│           └── hostfxr.dll             # Host FX Resolver
└── shared/
    ├── Microsoft.NETCore.App/          # .NET Runtime (always included)
    ├── Microsoft.AspNetCore.App/       # ASP.NET Core Runtime (if --runtime aspnet or all)
    └── Microsoft.WindowsDesktop.App/   # Windows Desktop Runtime (if --runtime desktop or all)
```

## License

MIT
