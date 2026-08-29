# Agent Guidelines for bump-core

## Repository Scope

`bump-core` is the shared Go library and sidecar binary for the `bump` dependency-update tool. The module path is:

```text
github.com/MilosRandelovic/bump-core/v2
```

Go consumers such as `homebrew-bump` import the public packages directly. Other clients can run `cmd/bump-core` and communicate with it using newline-delimited JSON over stdin/stdout.

## Package Boundaries

```text
cmd/bump-core/        Sidecar entry point
cmd/bump-mcp/         MCP server entry point
internal/mcp/         MCP tool implementation
internal/protocol/    Sidecar protocol implementation
internal/dependency/    Shared MCP and sidecar dependency-target selection
parser/               Dependency-file detection and parser delegation
updater/              Registry checks and dependency-update orchestration
npm/                  package.json, .npmrc, and npm registry support
pub/                  pubspec.yaml, pub configuration, and pub registry support
shared/               Shared types, cache, version helpers, file updates, and logging
```

- Keep `npm` and `pub` independent; ecosystem-specific behavior belongs in its ecosystem package.
- Put cross-ecosystem types and helpers in `shared`.
- Keep sidecar-only behavior under `internal/protocol`.
- Keep MCP-only behavior under `internal/mcp`.
- Registry HTTP access belongs in the npm/pub registry clients. Filesystem access belongs in parsers, configuration loaders, cache persistence, and file updates.
- Library packages must not print to stdout or stderr. Use `shared.LogFunc` for optional diagnostics. The sidecar protocol owns stdout; `cmd/bump-core` may use stderr for a fatal server error and stdout for `--version`.

## Public API Conventions

- Functions that accept configuration use `shared.Options`; do not add parallel boolean parameters.
- Name every parameter in callable signatures, including function types and injected callbacks. Never use bare positional type lists that force readers to infer what repeated types such as `string` represent.
- Long-running registry checks accept `context.Context` and must propagate cancellation through HTTP requests and worker orchestration.
- Dependency-check progress uses `shared.ProgressFunc`; it reports per-file and overall counts while results and verbose logs retain input order.
- Optional diagnostics use `shared.LogFunc`. Check for nil before calling it.
- Parser and registry-client structs expose a `Log` field and internal `log` helper. Concurrent registry operations can carry an operation-specific logger through `shared.ContextWithLog`/`shared.LogFromContext`.
- `parser.ParseDependencies` is the quiet convenience API; `parser.ParseDependenciesWithLog` is used when diagnostics are required.
- Constraint mismatches belong in `SemverSkipped`, not `Errors`.
- `Options.EnforceMinimumReleaseAge` only permits npm and Pub suggestions published more than 24 hours ago. It must never cause a downgrade, and the age is not configurable.
- Wrap operational errors with context using `fmt.Errorf("context: %w", err)`.
- Use concise, idiomatic Go names. Keep conventional or immediately obvious abbreviations such as `ctx`, `err`, `ok`, `i`, `t`, `max`, `min`, `args`, `config`, and `info`; expand abbreviations whose meaning is unclear from their scope and domain.
- Do not add comments merely to narrate the code or document what changed. Keep comments for public API contracts and non-obvious intent.

## Sidecar Protocol

Each input and output message occupies one JSON line. Request IDs are integers; zero is valid and must not be omitted from any correlated message.

### Requests

```json
{"method":"detect","id":1,"params":{"directory":"/path/to/project"}}
{"method":"check","id":2,"params":{"filePath":"/path/to/package.json","registryType":"npm","options":{}}}
{"method":"check","id":3,"params":{"filePath":"/path/to/package.json","registryType":"npm","options":{"semver":true},"targets":[{"name":"react"}]}}
{"method":"update","id":4,"params":{"filePath":"/path/to/package.json","registryType":"npm","options":{},"outdated":[]}}
{"method":"cancel","id":5,"params":{"id":2}}
```

### Responses and events

```json
{"type":"result","id":1,"result":{}}
{"type":"error","id":1,"error":"message"}
{"type":"error","id":9,"code":"request_limit_exceeded","error":"too many active requests (maximum 8)"}
{"type":"log","id":2,"message":"message"}
{"type":"progress","id":2,"filePath":"/path/to/package.json","fileCurrent":3,"fileTotal":5,"current":8,"total":10}
```

- `detect`, `check`, and `update` requests run concurrently.
- At most eight regular requests may be active. Requests above the limit are rejected immediately with `request_limit_exceeded`; they are not queued.
- `cancel` is handled by the reader loop and does not consume a request slot.
- A duplicate active request ID is rejected.
- Cancellation is checked before starting registry work and before dependency-update prepare/apply operations.
- All writes go through the server's synchronized encoder. Never write unrelated content to sidecar stdout.
- Keep logs, progress, cancellation, and terminal responses correlated with the originating request ID.
- A `check` request may select dependencies by exact package name, dependency type, file path, or any combination. Fields within a target are intersected; multiple targets form a union; omitted targets select every parsed dependency. Reject invalid and unmatched targets.
- The request options choose one version policy for the selected targets: neither `semver` nor `minimumAge` means absolute latest; either flag applies its restriction; both flags combine them.

## MCP Server

- `bump-mcp` is a stdio MCP server built with the official Go SDK.
- `check_updates` accepts a project directory, dependency options, and optional targets, detects the supported dependency file, and returns structured updates, skipped dependencies, errors, diagnostics, and a `checkId`.
- `update_dependencies` accepts a single-use `checkId` and applies only the exact result stored by the corresponding check. Never accept model-supplied replacement versions.
- Keep MCP version-policy and target-selection behavior identical to the sidecar protocol.
- Preserve MCP cancellation by passing the tool context through registry checks and updates.
- Forward dependency-check progress when the client supplies an MCP progress token.
- MCP stdout is reserved for protocol messages. Fatal startup or transport errors go to stderr.

## Registry Check Concurrency

- `CheckOutdated` checks up to six dependencies concurrently within each file.
- Preserve input ordering in returned results and verbose logs even though registry calls complete out of order.
- Progress reports completed dependencies and must remain safe under concurrent workers.
- Check context cancellation before starting more work and after each file.

## Cache Contract

- The persistent cache is versioned JSON stored at `~/.bump-cache`.
- Cache keys include package name, dependency type, registry, current version, and constraint. Do not replace the structured JSON key with a delimiter-based key.
- Minimum-age and unfiltered registry lookups use distinct cache identities. An age-filtered entry expires when the next release becomes eligible if that is sooner than the normal cache lifetime.
- A save reloads and merges the current on-disk entries, removes expired entries, and atomically replaces the file with mode `0600`.
- Non-JSON cache data is not parsed or carried forward. It is replaced by fresh cache data on a successful save.
- An unsupported JSON cache version must return an error and must not be overwritten.
- Cache persistence is serialized in-process. Dependency-file transactions additionally use operating-system file locks so separate bump processes cannot overwrite one another's updates.

## Dependency Update Safety

- `updater.UpdateDependencies` groups updates by file and locks canonical target paths across goroutines and processes for the entire prepare/apply transaction. Acquire multi-file locks in a deterministic order.
- Prepare and validate every target before writing the first file.
- Validate the dependency line, original version, replacement version, and resulting semantic-version constraint. Treat changed source locations as stale input and fail safely.
- Each file is replaced atomically using a same-directory temporary file and rename. A multi-file update is not transactional: if a later apply fails, earlier files remain updated.
- Resolve symlinks and replace their targets so the symlink itself is preserved.
- Reject hard-linked dependency files rather than breaking their link topology.
- Preserve file permissions and the source file's unrelated formatting/content.

## Versioning and Releases

- `shared.Version` is the single source of truth for the release version.
- The Homebrew formula installs `bump-mcp` alongside `bump` by building the command from the pinned bump-core module version.
- Every release version must have an exact `## [x.y.z]` section in `CHANGELOG.md` before merging to `main`.
- Major-version import paths must follow Go module rules; v2 imports include `/v2`.
- The release workflow runs on pushes to `main`, rejects an existing tag, creates the tag and GitHub release, and then opens a dependency-update pull request in `homebrew-bump`.
- Workflow actions use current major-version tags such as `@v6`; keep them on the latest supported major.

## Verification

Run the checks appropriate to the change, and run the full set before release:

```sh
gofmt -w <changed-go-files>
go test ./...
go test -race ./...
go vet ./...
make build
./bump-core --version
```

For workflow edits, also validate every file under `.github/workflows` with Actionlint.
