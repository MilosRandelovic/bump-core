package shared

import (
	"context"
	"errors"
	"fmt"
)

// DependencyType identifies a dependency-file section.
type DependencyType int

// Supported dependency sections.
const (
	Dependencies DependencyType = iota
	DevDependencies
	PeerDependencies
)

// String returns the dependency-file section name, or a numeric fallback for an unknown value.
func (dependencyType DependencyType) String() string {
	switch dependencyType {
	case Dependencies:
		return "dependencies"
	case DevDependencies:
		return "devDependencies"
	case PeerDependencies:
		return "peerDependencies"
	default:
		return fmt.Sprintf("DependencyType(%d)", dependencyType)
	}
}

// RegistryType identifies a supported package ecosystem.
type RegistryType int

// Supported package registries.
const (
	NPM RegistryType = iota
	Pub
)

// String returns the registry's wire value, or a numeric fallback for an unknown value.
func (registryType RegistryType) String() string {
	switch registryType {
	case NPM:
		return "npm"
	case Pub:
		return "pub"
	default:
		return fmt.Sprintf("RegistryType(%d)", registryType)
	}
}

// SkipReason identifies why an available update was not suggested.
type SkipReason int

// Reasons a dependency update can be skipped.
const (
	HardcodedVersion SkipReason = iota
	IncompatibleWithConstraint
)

// String returns user-facing text for the skip reason, or a numeric fallback for an unknown value.
func (skipReason SkipReason) String() string {
	switch skipReason {
	case HardcodedVersion:
		return "hardcoded version"
	case IncompatibleWithConstraint:
		return "incompatible with constraint"
	default:
		return fmt.Sprintf("SkipReason(%d)", skipReason)
	}
}

// BaseDependency contains source details shared by checked and outdated dependencies.
type BaseDependency struct {
	Name            string         // Name of the package
	OriginalVersion string         // Original version with prefixes (e.g., "^1.2.3")
	Type            DependencyType // Type of dependency (dependencies, devDependencies, peerDependencies)
	FilePath        string         // Absolute path to the file where this dependency is defined
	HostedURL       string         // For hosted packages, the registry URL (empty for pub.dev/npmjs.org)
	LineNumber      int            // Line number where this dependency is defined (1-based)
}

// Dependency is a parsed package dependency.
type Dependency struct {
	BaseDependency
	Version string // Clean version for API calls (e.g., "1.2.3")
}

// OutdatedDependency is a dependency with a newer eligible version.
type OutdatedDependency struct {
	BaseDependency
	CurrentVersion string // Current version of the package
	LatestVersion  string // Latest version available
}

// SemverSkipped is an update excluded by the selected semantic-version policy.
type SemverSkipped struct {
	OutdatedDependency
	Reason SkipReason // Reason why the dependency was skipped
}

// CheckResult contains available updates, policy skips, and per-dependency failures.
type CheckResult struct {
	Outdated      []OutdatedDependency
	Errors        []DependencyError
	SemverSkipped []SemverSkipped
}

// DependencyError identifies a dependency that could not be checked.
type DependencyError struct {
	Name  string
	Error string
}

// SemverChange classifies the magnitude of a dependency update.
type SemverChange int

// Semantic-version change levels.
const (
	PatchChange SemverChange = iota
	MinorChange
	MajorChange
)

// Options controls dependency parsing, registry checks, and updates.
type Options struct {
	Semver                   bool
	NoCache                  bool
	IncludePeerDependencies  bool
	Monorepo                 bool
	EnforceMinimumReleaseAge bool
}

// Progress reports both per-file and overall dependency-check completion.
type Progress struct {
	FilePath    string
	FileCurrent int
	FileTotal   int
	Current     int
	Total       int
}

// ProgressFunc receives dependency-check progress updates.
type ProgressFunc func(progress Progress)

// LogFunc receives optional diagnostic output.
type LogFunc func(format string, args ...any)

var (
	// ErrNoVersionsSatisfyConstraint indicates that no versions match the given semver constraint.
	ErrNoVersionsSatisfyConstraint = errors.New("no versions satisfy the constraint")

	// ErrUnsupportedRegistryType indicates an unknown registry type.
	ErrUnsupportedRegistryType = errors.New("unsupported registry type")

	// ErrNoVersionsMeetMinimumReleaseAge indicates that every verifiable release is too young.
	ErrNoVersionsMeetMinimumReleaseAge = errors.New("no versions meet the minimum release age")
)

// Parser reads one ecosystem-specific dependency file.
type Parser interface {
	// ParseDependencies returns the supported dependencies found in filePath according to options.
	ParseDependencies(filePath string, options Options) ([]Dependency, error)
}

// PatternProvider describes how dependency constraints are located and replaced in a file.
type PatternProvider interface {
	// GetPattern returns a regular expression whose second capture group is the current constraint.
	GetPattern(dependency OutdatedDependency) string
	// GetReplacement returns a regexp expansion template that substitutes newVersion while preserving surrounding content.
	GetReplacement(dependency OutdatedDependency, newVersion string) string
}

// Updater supplies ecosystem-specific update rules.
type Updater interface {
	// GetPatternProvider returns the file-format rules used to locate and replace dependency constraints.
	GetPatternProvider() PatternProvider
	// ValidateOptions rejects options unsupported by the ecosystem.
	ValidateOptions(options Options) error
}

// RegistryClient resolves package versions from an ecosystem registry.
type RegistryClient interface {
	// GetLatestVersionFromRegistry returns the latest eligible version, using cache when non-nil and honoring context cancellation.
	GetLatestVersionFromRegistry(ctx context.Context, packageName, registryURL string, options Options, cache *Cache) (string, error)
	// GetBothLatestVersions returns the latest eligible version and the latest version satisfying constraint.
	// When no version satisfies the constraint, it returns the absolute latest version with ErrNoVersionsSatisfyConstraint.
	GetBothLatestVersions(ctx context.Context, packageName, constraint, registryURL string, options Options, cache *Cache) (absoluteLatest, constraintLatest string, err error)
}
