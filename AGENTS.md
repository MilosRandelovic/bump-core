# Copilot Rules for Bump Core

## Project Context

This is the core library and sidecar binary for the "bump" dependency update tool. It extracts the business logic from homebrew-bump into a standalone Go module that communicates over a JSON protocol on stdin/stdout. This enables frontends (VS Code extension, CLI, etc.) to use the same core logic.

The binary reads newline-delimited JSON requests from stdin and writes JSON responses to stdout.

## Architecture Principles

- Keep npm/ and pub/ packages separate - no cross-dependencies
- Place common functionality in shared/
- Follow single responsibility principle per package
- Use shared.Options struct for passing configuration flags
- NO direct terminal output (fmt.Print, os.Stdout) in internal packages — use shared.LogFunc callbacks
- All communication with the outside world goes through the protocol package

## Protocol

### Request format

```json
{"method": "detect", "id": 1, "params": {"directory": "/path/to/project"}}
{"method": "check", "id": 2, "params": {"filePath": "/path/to/package.json", "registryType": "npm", "options": {}}}
{"method": "update", "id": 3, "params": {"filePath": "/path/to/package.json", "registryType": "npm", "options": {}, "outdated": [...]}}
```

### Response types

- `{"type": "result", "id": 1, "result": {...}}` — success response
- `{"type": "error", "id": 1, "error": "message"}` — error response
- `{"type": "log", "message": "..."}` — out-of-band log message
- `{"type": "progress", "current": 3, "total": 10}` — progress update

## Code Patterns to Follow

### LogFunc Pattern

- Functions that previously used output.VerbosePrintf now accept `log shared.LogFunc`
- Always check `if log != nil` before calling
- Structs (Parser, RegistryClient) have a `Log shared.LogFunc` field with a `log()` helper method

### Options Pattern

- ALL functions that accept configuration flags MUST use the shared.Options struct
- Do NOT pass individual boolean parameters

### Error Handling

- Categorize constraint mismatches as `semverSkipped`, not `errors`
- Wrap errors with context: `fmt.Errorf("context: %w", err)`

## File Structure

```text
cmd/bump-core/        # Binary entry point (sidecar)
shared/               # Common types, version utilities, interfaces, LogFunc
parser/               # Auto-detection and delegation
updater/              # Core update checking logic
npm/                  # npm ecosystem (package.json, .npmrc, npm registry)
pub/                  # Dart/Flutter pub ecosystem (pubspec.yaml, pub-tokens.json, pub registry)
internal/
└── protocol/         # JSON protocol server (sidecar-specific, not importable)
```

## Building

```sh
make build     # Build the binary
make test      # Run tests
```
