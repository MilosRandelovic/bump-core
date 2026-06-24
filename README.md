# bump-core

Core library and sidecar binary for the [Bump](https://github.com/MilosRandelovic/homebrew-bump) dependency update tool. Contains all business logic for checking and updating dependencies in `package.json` (npm) and `pubspec.yaml` (Dart/Flutter pub) projects.

## Usage

### As a Go library

bump-core packages are importable by other Go modules:

```go
import (
    "github.com/MilosRandelovic/bump-core/parser"
    "github.com/MilosRandelovic/bump-core/updater"
    "github.com/MilosRandelovic/bump-core/shared"
)

// Auto-detect dependency file
filePath, registryType, err := parser.AutoDetectDependencyFile(directory, nil)

// Parse dependencies
dependencies, err := parser.ParseDependencies(filePath, registryType, options)

// Check for outdated dependencies (empty workingDirectory falls back to the process CWD)
result, err := updater.CheckOutdated(dependencies, registryType, options, "", nil, nil)

// Update dependency files
err = updater.UpdateDependencies(filePath, result.Outdated, registryType, options, "", nil)
```

### As a sidecar binary

The `bump-core` binary communicates over newline-delimited JSON on stdin/stdout, enabling non-Go frontends (e.g., the [VS Code extension](https://github.com/MilosRandelovic/vscode-bump)) to use the same core logic.

```bash
# Build the binary
make build

# Check version
./bump-core --version
```

#### Protocol

Send JSON requests (one per line) to stdin, receive JSON responses on stdout:

```json
{"method": "detect", "id": 1, "params": {"directory": "/path/to/project"}}
{"method": "check", "id": 2, "params": {"filePath": "/path/to/package.json", "registryType": "npm", "options": {}}}
{"method": "update", "id": 3, "params": {"filePath": "/path/to/package.json", "registryType": "npm", "options": {}, "outdated": [...]}}
```

Response types:

- `{"type": "result", "id": 1, "result": {...}}` — success
- `{"type": "error", "id": 1, "error": "message"}` — error
- `{"type": "log", "message": "..."}` — out-of-band log message
- `{"type": "progress", "current": 3, "total": 10}` — progress update

## Features

- Check and update npm dependencies (`package.json`)
- Check and update Dart/Flutter pub dependencies (`pubspec.yaml`)
- Semver constraint awareness (`^`, `~`, `>=`, compound constraints)
- Private registry and hosted package support
- Monorepo/workspace detection for npm
- Version caching for repeated checks
- Pre-release version filtering

## Project Structure

```txt
cmd/bump-core/        # Sidecar binary entry point
shared/               # Common types, version utilities, interfaces, LogFunc
parser/               # Auto-detection and delegation
updater/              # Core update checking logic
npm/                  # npm ecosystem (package.json, .npmrc, npm registry)
pub/                  # Dart/Flutter pub ecosystem (pubspec.yaml, pub registry)
internal/
└── protocol/         # JSON protocol server (sidecar-specific, not importable)
```

## Building

```bash
make build     # Build the sidecar binary
make test      # Run tests
make clean     # Remove build artifacts
```

## Frontends

- [homebrew-bump](https://github.com/MilosRandelovic/homebrew-bump) — CLI tool (imports bump-core as a Go module)
- [vscode-bump](https://github.com/MilosRandelovic/vscode-bump) — VS Code extension (communicates with the sidecar binary)

## License

This project is licensed under the terms specified in the LICENSE file.
