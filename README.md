# bump-core

Core library and sidecar binary for the [Bump](https://github.com/MilosRandelovic/homebrew-bump) dependency update tool. Contains all business logic for checking and updating dependencies in `package.json` (npm) and `pubspec.yaml` (Dart/Flutter pub) projects.

## Add as a Go library dependency

Run this in the consuming Go module:

```sh
go get github.com/MilosRandelovic/bump-core/v2@latest
```

## Usage

### As a Go library

bump-core packages are importable by other Go modules:

```go
import (
	"context"
	"fmt"

	"github.com/MilosRandelovic/bump-core/v2/parser"
	"github.com/MilosRandelovic/bump-core/v2/shared"
	"github.com/MilosRandelovic/bump-core/v2/updater"
)

func checkAndUpdate(ctx context.Context, directory string) error {
	filePath, registryType, err := parser.AutoDetectDependencyFile(directory, nil)
	if err != nil {
		return fmt.Errorf("detect dependency file: %w", err)
	}

	options := shared.Options{}
	dependencies, err := parser.ParseDependencies(filePath, registryType, options)
	if err != nil {
		return fmt.Errorf("parse dependencies: %w", err)
	}

	result, err := updater.CheckOutdated(ctx, dependencies, registryType, options, directory, nil, nil)
	if err != nil {
		return fmt.Errorf("check outdated dependencies: %w", err)
	}
	if err := updater.UpdateDependencies(ctx, filePath, result.Outdated, registryType, options, directory, nil); err != nil {
		return fmt.Errorf("update dependencies: %w", err)
	}
	return nil
}
```

### As a sidecar binary

The `bump-core` binary communicates over newline-delimited JSON on stdin/stdout, enabling non-Go frontends (e.g., the [VS Code extension](https://github.com/MilosRandelovic/vscode-bump)) to use the same core logic.

```bash
# Build the binary
make build

# Check version
./bump-core --version
```

The [sidecar protocol](docs/sidecar-protocol.md) is the canonical contract for frontend implementations.

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

For the shared selector and version-policy rules, see [dependency selection](docs/sidecar-protocol.md#dependency-selection). Results include typed structured content for agents and a concise text summary for clients that only display text.

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

The persistent cache is stored as versioned JSON in `~/.bump-cache`. Concurrent checks and separate `bump` processes merge their entries during persistence rather than overwriting one another.

## Project structure

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

## Release

Set `shared.Version`, add the matching `CHANGELOG.md` section, and merge the pull request to `main`. The release workflow builds the sidecar to read its version, creates the version tag and source release, and opens the matching bump-core update pull request in `homebrew-bump`.

Before a core API removal, merge any compatible CLI preparation into `homebrew-bump` while it still pins the released core version: the core release workflow builds that repository's `main`. Merge the generated dependency-pin update only after the core release completes.

## License

This project is licensed under the terms specified in the LICENSE file.
