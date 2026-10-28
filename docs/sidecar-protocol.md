# Sidecar protocol

This is the canonical wire contract for `bump-core` and its non-Go frontends. The implementation and wire types live in `internal/protocol`; `vscode-bump/src/types.ts` is a consumer. This protocol is distinct from the JSON-RPC transport used by the `bump-mcp` command.

## Transport and validation

Run `bump-core` with no arguments and exchange newline-delimited JSON over stdin and stdout. Each request, response, diagnostic, or progress event occupies one line. Diagnostics outside the protocol go to stderr. Input lines must fit the server's one-MiB scanner buffer; an oversized line terminates the input loop with an error.

Every request has a `method`, an integer `id`, and method-specific `params`. IDs are required, zero is valid, and every correlated message includes the request ID. A malformed envelope or missing ID produces an error with ID zero because it has no usable correlation ID. Request envelopes, parameter objects, options, and nested update or target objects reject unknown fields; trailing JSON values or malformed suffixes are rejected too. Error prose is diagnostic, not a stable machine interface.

```json
{"method":"detect","id":1,"params":{"directory":"/project"}}
{"method":"check","id":2,"params":{"filePath":"/project/package.json","registryType":"npm","options":{"semver":true},"targets":[{"name":"react"}]}}
{"method":"cancel","id":3,"params":{"id":2}}
```

## Methods

| Method | Parameters | Successful result |
| --- | --- | --- |
| `detect` | Non-empty `directory` | `filePath` and `registryType` |
| `check` | Non-empty `filePath`, `registryType`, optional `options` and `targets` | `outdated`, `semverSkipped`, and `errors` arrays |
| `update` | Non-empty `filePath`, `registryType`, optional `options`, and `outdated` entries from a check | `updated`, the number of entries supplied to the update operation |
| `cancel` | `id`, the active request to cancel | `cancelled`, whether that request was active |

`registryType` is `npm` or `pub`. Unsupported methods, registry values, dependency types, or selectors return errors. The update method applies checked replacement versions; its request method, not an option flag, expresses update intent. Dependency-file validation and replacement guarantees are defined by the [update safety invariants](../AGENTS.md#safety-invariants).

## Dependency selection

Each check applies one policy to all selected dependencies. Omit both policy flags for absolute latest, set `options.semver` for constraint-compatible latest, set `options.minimumAge` for latest versions published more than 24 hours ago, or set both to combine the restrictions. The minimum age is fixed, and filtering never downgrades the installed version.

Omit `targets` to check every parsed dependency. Each target may contain `name`, `type`, and `filePath`. Fields within a target intersect; multiple targets form a union. Names are exact package names. Types are `dependencies`, `devDependencies`, or `peerDependencies`. File selectors may be absolute or relative to the checked project directory. Invalid selectors and selectors matching no parsed dependency fail rather than silently broadening the check.

| Option | Meaning |
| --- | --- |
| `semver` | Respect compatible version constraints and report fixed or unsupported constraints as skipped |
| `minimumAge` | Enforce the fixed release-age policy |
| `noCache` | Bypass persistent registry lookup caching |
| `includePeerDependencies` | Include npm peer dependencies |
| `monorepo` | Include npm workspace packages |
| `verbose` | Emit request-scoped diagnostics |
| `update` | Ignored compatibility field; use the `update` method |

Omitted option flags are false. npm-only peer and workspace options are rejected for Pub. MCP uses the same selector and version-policy semantics, though its update tool takes a checked-result ID rather than this sidecar's update payload.

## Results and events

| `type` | Fields in addition to `id` |
| --- | --- |
| `result` | `result`, the method-specific object |
| `error` | `error`, diagnostic text; optional `code` for a machine-readable error |
| `log` | `message` |
| `progress` | `filePath`, `fileCurrent`, `fileTotal`, `current`, `total` |

Check result `outdated` entries carry `name`, `type`, `currentVersion`, `originalVersion`, `latestVersion`, `filePath`, `lineNumber`, and optional `hostedUrl`. Updates send those entries back unchanged, using the original file location and constraint for stale-input validation.

`semverSkipped` entries carry `name`, `type`, `currentVersion`, `originalVersion`, `latestVersion`, `reason`, and `lineNumber`. Dependency errors carry `name` and `error`; callers must not interpret a failed dependency check as an up-to-date result. Progress reports both the current file and overall dependency-check counters.

## Concurrency and cancellation

Regular requests may finish out of order and are limited to eight active workers. An extra request is rejected immediately with `code: "request_limit_exceeded"`; clients may queue or retry it after a slot becomes available and must not string-match the error prose. Cancellation is handled outside that limit, so an overloaded client can still cancel active work. A request ID already active is rejected; clients should allocate a distinct ID for a cancellation request as well as naming its target ID in `params`.

```json
{"type":"error","id":9,"code":"request_limit_exceeded","error":"too many active requests (maximum 8)"}
```

Cancel signals cooperative cancellation, not rollback or a promise that the target has finished. The target emits its own correlated result or error. Request workers are owned and awaited by the server; output encoding serializes concurrent responses and events so their JSON lines cannot interleave. On normal input EOF the server drains active workers before returning. Input read failures cancel active work before waiting for it; output failures are recorded and surfaced by the server's run loop.
