# Changelog

## [2.1.0]

- Add a fixed 24-hour minimum release age for npm and Pub version suggestions
- Keep age-filtered registry results separate in the cache and expire them when the next release becomes eligible
- Expose minimum-age checks through the sidecar protocol without allowing version downgrades
- Preserve dependency input order, stop scheduling work after cancellation, and report per-file and overall progress through library and sidecar APIs
- Make dependency updates context-aware, serialize them across processes, and reject duplicate active IDs for cancel requests
- Reject invalid dependency types and update patterns without panicking or modifying files
- Use idiomatic Go API names and keep registry configuration details internal to their ecosystem packages

## [2.0.0]

- Fix compound and disjunctive semver constraint evaluation and updates
- Preserve absolute latest versions when no release satisfies a constraint
- Replace the delimiter-based cache with versioned, atomic JSON persistence
- Validate all dependency edits before writing and reject stale source locations
- Resolve npm and Dart registry configuration relative to platform/project paths
- Add bounded parallel registry checks with global progress reporting
- Harden npm and pub parsing for compact JSON and inline YAML comments
- Add direct protocol and production updater integration coverage
- Preserve quoted pub constraints and support arbitrary consistent YAML indentation
- Support path-scoped npm authentication and cache registry configuration per check
- Reject empty pub registry versions and surface malformed token configuration
- Keep verbose registry checks concurrent while emitting dependency logs in stable order
- Add request-scoped protocol progress, concurrent requests, and cancellation
- Preserve dependency-file symlinks and reject unsafe hard-linked replacements
- Validate client-supplied versions before applying dependency constraints
- Fail releases with stale or undocumented versions
- Serialize overlapping update transactions so concurrent requests cannot lose edits
- Merge concurrent cache saves and bound active sidecar requests with machine-readable capacity errors without blocking cancellation
- Preserve request ID zero and update duplicate dependencies in minified npm manifests
- Remove unused single-file update and cache compatibility helpers
- Publish the Go module under the required `/v2` import path
- Initial release as a standalone library and sidecar binary
- Extracted all core logic from [homebrew-bump](https://github.com/MilosRandelovic/homebrew-bump)
- All packages exported for use as a Go module
- JSON protocol server for sidecar communication (stdin/stdout)
- npm: package.json parsing, .npmrc config, registry client, monorepo workspace support
- pub: pubspec.yaml parsing, pub-tokens.json config, registry client, hosted package support
- Semver constraint handling (^, ~, >=, compound constraints)
- Version caching with automatic expiry
- LineNumber and OriginalVersion fields for IDE integration
