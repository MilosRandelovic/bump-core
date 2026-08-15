# Changelog

## [2.0.0]

- Initial release as a standalone library and sidecar binary
- Extracted all core logic from [homebrew-bump](https://github.com/MilosRandelovic/homebrew-bump)
- All packages exported for use as a Go module
- JSON protocol server for sidecar communication (stdin/stdout)
- npm: package.json parsing, .npmrc config, registry client, monorepo workspace support
- pub: pubspec.yaml parsing, pub-tokens.json config, registry client, hosted package support
- Semver constraint handling (^, ~, >=, compound constraints)
- Version caching with automatic expiry
- LineNumber and OriginalVersion fields for IDE integration
