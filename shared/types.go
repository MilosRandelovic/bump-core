package shared

import (
	"context"
	"errors"
	"fmt"
)

// DependencyType represents the type of dependency
type DependencyType int

const (
	Dependencies DependencyType = iota
	DevDependencies
	PeerDependencies
)

// String returns the string representation of DependencyType
func (dependencyType DependencyType) String() string {
	switch dependencyType {
	case Dependencies:
		return "dependencies"
	case DevDependencies:
		return "devDependencies"
	case PeerDependencies:
		return "peerDependencies"
	default:
		panic(fmt.Sprintf("unknown DependencyType: %d", dependencyType))
	}
}

// RegistryType represents the type of package registry
type RegistryType int

const (
	Npm RegistryType = iota
	Pub
)

// String returns the string representation of RegistryType
func (registryType RegistryType) String() string {
	switch registryType {
	case Npm:
		return "npm"
	case Pub:
		return "pub"
	default:
		panic(fmt.Sprintf("unknown RegistryType: %d", registryType))
	}
}

// SkipReason represents the reason a dependency was skipped
type SkipReason int

const (
	HardcodedVersion SkipReason = iota
	IncompatibleWithConstraint
)

// String returns the string representation of SkipReason
func (skipReason SkipReason) String() string {
	switch skipReason {
	case HardcodedVersion:
		return "hardcoded version"
	case IncompatibleWithConstraint:
		return "incompatible with constraint"
	default:
		panic(fmt.Sprintf("unknown SkipReason: %d", skipReason))
	}
}

// BaseDependency contains the core fields shared by all dependency types
type BaseDependency struct {
	Name            string         // Name of the package
	OriginalVersion string         // Original version with prefixes (e.g., "^1.2.3")
	Type            DependencyType // Type of dependency (dependencies, devDependencies, peerDependencies)
	FilePath        string         // Absolute path to the file where this dependency is defined
	HostedURL       string         // For hosted packages, the registry URL (empty for pub.dev/npmjs.org)
	LineNumber      int            // Line number where this dependency is defined (1-based)
}

// Dependency represents a package dependency
type Dependency struct {
	BaseDependency
	Version string // Clean version for API calls (e.g., "1.2.3")
}

// OutdatedDependency represents a dependency that has a newer version available
type OutdatedDependency struct {
	BaseDependency
	CurrentVersion string // Current version of the package
	LatestVersion  string // Latest version available
}

// SemverSkipped represents a dependency that was skipped due to semver constraints
type SemverSkipped struct {
	OutdatedDependency
	Reason SkipReason // Reason why the dependency was skipped
}

// CheckResult contains the results of checking dependencies
type CheckResult struct {
	Outdated      []OutdatedDependency
	Errors        []DependencyError
	SemverSkipped []SemverSkipped
}

// DependencyError represents an error that occurred while checking a dependency
type DependencyError struct {
	Name  string
	Error string
}

// SemverChange represents the type of version change
type SemverChange int

const (
	PatchChange SemverChange = iota
	MinorChange
	MajorChange
)

// Options contains all configuration flags for the application
type Options struct {
	Verbose                 bool
	Update                  bool
	Semver                  bool
	NoCache                 bool
	IncludePeerDependencies bool
	Monorepo                bool
}

// LogFunc is a callback for verbose logging
type LogFunc func(format string, args ...any)

// Custom error types for better error handling
var (
	// ErrNoVersionsSatisfyConstraint indicates that no versions match the given semver constraint
	ErrNoVersionsSatisfyConstraint = errors.New("no versions satisfy the constraint")

	// ErrUnsupportedRegistryType indicates an unknown registry type string
	ErrUnsupportedRegistryType = errors.New("unsupported registry type")
)

// Parser interface defines the contract for parsing dependencies from files
type Parser interface {
	ParseDependencies(filePath string, options Options) ([]Dependency, error)
	GetRegistryType() RegistryType
}

// PatternProvider defines how to generate regex patterns for different file formats
type PatternProvider interface {
	GetPattern(dependency OutdatedDependency) string
	GetReplacement(dependency OutdatedDependency, newVersion string) string
}

// Updater interface defines the contract for updating dependencies in files
type Updater interface {
	GetPatternProvider() PatternProvider
	GetRegistryType() RegistryType
	ValidateOptions(options Options) error
}

// RegistryClient interface defines the contract for fetching package information
type RegistryClient interface {
	GetLatestVersionFromRegistry(ctx context.Context, packageName, registryURL string, options Options, cache *Cache) (string, error)
	GetBothLatestVersions(ctx context.Context, packageName, constraint, registryURL string, options Options, cache *Cache) (absoluteLatest, constraintLatest string, err error)
	GetRegistryType() RegistryType
}
