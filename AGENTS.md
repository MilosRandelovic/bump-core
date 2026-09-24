# bump-core

## Role

`bump-core` owns the shared Go library, newline-delimited JSON sidecar, and stdio MCP server for the Bump dependency-update tool. Go consumers import `github.com/MilosRandelovic/bump-core/v2`; non-Go clients run `cmd/bump-core`; coding agents run `cmd/bump-mcp`.

## Package boundaries

```text
cmd/bump-core/       Sidecar entry point
cmd/bump-mcp/        MCP server entry point
internal/dependency/ Shared sidecar and MCP dependency selection
internal/mcp/        MCP tools and server
internal/protocol/   Sidecar protocol
parser/              Dependency-file detection and parser delegation
updater/             Registry checks and update orchestration
npm/                 package.json, .npmrc, and npm registry support
pub/                 pubspec.yaml, Pub configuration, and Pub registry support
shared/              Cross-ecosystem contracts and utilities
```

Dependencies point from entry points to internal frontends, then to `parser` and `updater`, then to ecosystem packages and `shared`. Keep `npm` and `pub` independent, keep frontend behavior under `internal`, and move a helper to `shared` only when more than one package genuinely owns the need.

Registry HTTP access belongs in the ecosystem registry clients. Dependency discovery reads belong in parsers, configuration reads belong in ecosystem configuration loaders, and dependency-file prepare/apply reads and writes and cache persistence belong in `shared`.

Library packages never print. Optional diagnostics use `shared.LogFunc`; the sidecar and MCP transports reserve stdout for protocol messages and may send fatal transport errors to stderr.

## Go conventions

- Functions that accept dependency behavior take `shared.Options`; frontend-only behavior such as CLI output and sidecar verbosity stays in the frontend.
- Name every callable parameter, including function types and callbacks. Keep conventional abbreviations such as `ctx`, `err`, `ok`, `id`, `max`, `min`, `args`, `config`, and `info`; expand names whose meaning is not immediate in their scope.
- Put `context.Context` first on registry checks, update operations, and transport lifecycles, and propagate cancellation through HTTP requests and owned goroutines.
- Keep public API comments useful as contracts. Comments elsewhere explain safety, ordering, external constraints, or behavior the code cannot express; they do not narrate statements or record changes.
- Wrap operational errors with lower-case context and `%w` so callers can classify the cause.
- Constraint mismatches belong in `SemverSkipped`, not `Errors`.

## Safety invariants

- `CheckOutdated` runs at most six registry checks per file, preserves dependency order in results and diagnostics, reports per-file and overall progress, and stops scheduling work after cancellation.
- Registry clients cap response bodies before decoding and never contact a live registry from tests.
- The persistent cache is versioned strict JSON at `~/.bump-cache`. Cache keys are structured JSON, never delimiter-composed strings, and include package name, dependency type, registry, current version, constraint, and minimum-age policy when enabled. Minimum-age results expire when the next release becomes eligible if that occurs before the normal expiry.
- Cache saves take process-local and operating-system locks, observe request cancellation while waiting for the operating-system lock, reload and merge the current file, remove expired entries, and atomically replace it with mode `0600`. Unsupported cache versions fail without being overwritten; other invalid cache data may be replaced by fresh results.
- `updater.UpdateDependencies` locks canonical target paths across goroutines and processes for the complete prepare/apply transaction and acquires multi-file locks in deterministic order.
- Prepare and validate every target before writing the first file. Validate the source location, original and replacement versions, and resulting constraint; changed input fails safely.
- Each file replacement is atomic and preserves symlink targets, permissions, and unrelated content. Hard-linked files are rejected because atomic replacement cannot preserve their topology.
- Multi-file updates are deliberately not transactional: if a later apply fails, earlier files remain updated.
- `Options.EnforceMinimumReleaseAge` permits only npm and Pub releases published more than 24 hours ago, never downgrades the current version, and is not configurable.

## Sidecar contract

Each request, response, log message, and progress event occupies one JSON line. Owned JSON schemas reject unknown fields and trailing values. Request IDs are required integers; zero is valid and appears in every correlated message.

The sidecar supports `detect`, `check`, `update`, and `cancel`. `check` selects one version policy and optional dependency targets. A target intersects its package name, dependency type, and file path fields; multiple targets form a union; omitted targets select all parsed dependencies; invalid or unmatched targets fail. `update` applies the supplied checked updates.

Regular requests run concurrently up to an exact limit of eight. Additional work is rejected immediately with the machine-readable `request_limit_exceeded` code so a client can retry; cancellation stays available and does not consume a slot. Duplicate active IDs fail, and every started request has cancellation and wait ownership. The encoder serializes all response, log, and progress writes so concurrent requests cannot interleave JSON lines. Check cancellation before starting registry work and before dependency-update prepare/apply operations so canceled requests stop before new work begins.

## MCP contract

`check_updates` accepts a project directory, dependency options, and optional targets, then returns structured updates, skipped dependencies, errors, diagnostics, and a single-use opaque `checkId`. It supports absolute latest, semver-compatible latest, fixed minimum-age latest, and the combined policy.

`update_dependencies` accepts only a `checkId` and applies the exact checked update set. Never accept model-supplied replacement versions. Changed files fail through the same stale-input validation used by the library.

Keep MCP version policy and target selection identical to the sidecar. Forward the tool context through checks and updates, and forward progress when the client supplies a progress token.

## Release

`shared.Version` is the release source of truth. Every version needs an exact `## [x.y.z]` section in `CHANGELOG.md` before merge, and major versions must follow Go module import-path rules.

The release workflow runs on pushes to `main` and does not repeat formatting, vet, or tests because required, up-to-date PR CI has already checked the merged tree. It builds the sidecar to read its version, rejects an existing tag, creates the tag and GitHub source release, and opens a dependency-update pull request in `homebrew-bump`. Workflow actions use their latest supported major tags.

The Homebrew formula builds `bump-mcp` from the released module version, so the bump-core release must complete before the generated `homebrew-bump` update is merged.

Validate workflow edits locally with `make workflow-lint`; Actionlint is intentionally not part of CI because CI should validate the product rather than validate its own workflow syntax.

## Validation

Run the same commands as CI:

```sh
go mod download
test -z "$(gofmt -l .)"
go vet ./...
go test ./...
go test -race ./...
make build
./bump-core --version
```
