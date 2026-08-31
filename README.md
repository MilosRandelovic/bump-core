# bump-core

Core library and sidecar binary for the [Bump](https://github.com/MilosRandelovic/homebrew-bump) dependency update tool. Contains all business logic for checking and updating dependencies in `package.json` (npm) and `pubspec.yaml` (Dart/Flutter pub) projects.

## Usage

### As a Go library

bump-core packages are importable by other Go modules:

```go
import (
	"context"

	"github.com/MilosRandelovic/bump-core/v2/parser"
	"github.com/MilosRandelovic/bump-core/v2/shared"
	"github.com/MilosRandelovic/bump-core/v2/updater"
)

filePath, registryType, err := parser.AutoDetectDependencyFile(directory, nil)

dependencies, err := parser.ParseDependencies(filePath, registryType, options)

// Check for outdated dependencies (empty workingDirectory is derived from dependency paths, then falls back to the process CWD)
result, err := updater.CheckOutdated(context.Background(), dependencies, registryType, options, "", nil, nil)

err = updater.UpdateDependencies(context.Background(), filePath, result.Outdated, registryType, options, "", nil)
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
{"method": "check", "id": 3, "params": {"filePath": "/path/to/package.json", "registryType": "npm", "options": {"minimumAge": true}}}
{"method": "check", "id": 4, "params": {"filePath": "/path/to/package.json", "registryType": "npm", "options": {"semver": true}, "targets": [{"name": "react"}, {"type": "devDependencies", "filePath": "packages/app/package.json"}]}}
{"method": "update", "id": 5, "params": {"filePath": "/path/to/package.json", "registryType": "npm", "options": {}, "outdated": [...]}}
{"method": "cancel", "id": 6, "params": {"id": 2}}
```

Each check uses one version policy for its selected dependencies. Omit both flags for absolute latest versions, set `options.semver` for constraint-compatible latest versions, set `options.minimumAge` for latest versions published more than 24 hours ago, or set both flags to combine the semver and minimum-age restrictions. The age is fixed, and filtering never downgrades the current version.

Omit `targets` to check every parsed dependency. A target can select an exact package `name`, dependency `type` (`dependencies`, `devDependencies`, or `peerDependencies`), `filePath`, or a combination of those fields. Fields within one target are combined, while multiple targets form a union. File paths may be absolute or relative to the checked project directory. Invalid targets and targets that match nothing return an error rather than silently checking the wrong scope.

Response types:

- `{"type": "result", "id": 1, "result": {...}}` — success
- `{"type": "error", "id": 1, "error": "message"}` — error
- `{"type": "error", "id": 9, "code": "request_limit_exceeded", "error": "too many active requests (maximum 8)"}` — retryable capacity error
- `{"type": "log", "id": 2, "message": "..."}` — request-scoped log message
- `{"type": "progress", "id": 2, "filePath": "/path/to/package.json", "fileCurrent": 3, "fileTotal": 5, "current": 8, "total": 10}` — request-scoped per-file and overall progress update

Requests may run concurrently. Send `cancel` with the target request ID to stop an active check; the cancel result reports whether that request was still active. The sidecar accepts up to eight active requests and rejects additional work with the retryable `request_limit_exceeded` code until a slot is available, while cancellation requests remain available.

### As an MCP server

The `bump-mcp` stdio server lets coding agents check and update dependencies using the same bump-core logic. Build it and register it with an MCP client:

```bash
make build
codex mcp add bump -- /absolute/path/to/bump-mcp
claude mcp add bump -- /absolute/path/to/bump-mcp
```

It exposes two tools:

- `check_updates` detects `package.json` or `pubspec.yaml` in a project directory, checks the requested dependency targets using the requested version policy, and returns available updates, skipped dependencies, errors, and an opaque `checkId`.
- `update_dependencies` accepts that single-use `checkId` and applies the exact checked update set. It rejects changed dependency files using bump-core's existing stale-input validation.

The check tool supports absolute latest, semantic-version-compatible latest, fixed 24-hour minimum-age latest, and combined semver-plus-minimum-age queries. Each query can target a package, dependency type, file, combination, or union using the same selector contract as the sidecar protocol. It also supports cache bypass, npm peer dependencies, and npm workspaces. Results include typed structured content for agents and a concise text summary for clients that only display text.

## Features

- Check and update npm dependencies (`package.json`)
- Check and update Dart/Flutter pub dependencies (`pubspec.yaml`)
- Semver constraint awareness (`^`, `~`, `>=`, compound constraints)
- Private registry and hosted package support
- Monorepo/workspace detection for npm
- Version caching for repeated checks
- Pre-release version filtering
- Optional fixed 24-hour minimum release age for npm and Pub suggestions
- MCP tools for agent-driven dependency checks and updates
- Bounded parallel registry checks with deterministic results
- Stale-safe dependency updates with validation across all targets and atomic replacement per file

The persistent cache is stored as versioned JSON in `~/.bump-cache`. Concurrent checks within one process merge their entries during persistence rather than overwriting one another.

## Project Structure

```txt
cmd/bump-core/          # Sidecar binary entry point
cmd/bump-mcp/           # MCP server entry point
shared/                 # Common types, version utilities, interfaces, LogFunc
parser/                 # Auto-detection and delegation
updater/                # Core update checking logic
npm/                    # npm ecosystem (package.json, .npmrc, npm registry)
pub/                    # Dart/Flutter pub ecosystem (pubspec.yaml, pub registry)
internal/
├── dependency/          # Shared MCP and sidecar target selection
├── mcp/                 # MCP tools and stdio server
└── protocol/            # JSON sidecar protocol server
```

## Building

```bash
make build     # Build the sidecar and MCP binaries
make test      # Run tests
make clean     # Remove build artifacts
```

## Frontends

- [homebrew-bump](https://github.com/MilosRandelovic/homebrew-bump) — CLI tool (imports bump-core as a Go module)
- [vscode-bump](https://github.com/MilosRandelovic/vscode-bump) — VS Code extension (communicates with the sidecar binary)

## License

This project is licensed under the terms specified in the LICENSE file.
