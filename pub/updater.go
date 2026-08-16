package pub

import (
	"fmt"
	"regexp"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

// PatternProvider implements the pattern provider for pub pubspec.yaml files
type PatternProvider struct{}

// GetPattern returns the regex pattern for matching pub dependency lines
func (patternProvider *PatternProvider) GetPattern(dependency shared.OutdatedDependency) string {
	// Group 2 must contain only the constraint because shared update validation
	// compares it with OriginalVersion. Groups 1, 3, and 4 preserve the quote
	// style, trailing whitespace, and inline comment respectively.
	// For hosted packages, match the version line
	// For simple packages, match the package name line
	if dependency.HostedURL != "" {
		return `(\s*version\s*:\s*["']?)([^"'#]*[^"'#\s])(["']?)(\s*(?:#.*)?)$`
	}
	escapedName := regexp.QuoteMeta(dependency.Name)
	return fmt.Sprintf(`(\s*%s\s*:\s*["']?)([^"'#]*[^"'#\s])(["']?)(\s*(?:#.*)?)$`, escapedName)
}

// GetReplacement returns the replacement string for pub dependency lines
func (patternProvider *PatternProvider) GetReplacement(dependency shared.OutdatedDependency, newVersion string) string {
	return fmt.Sprintf(`${1}%s${3}${4}`, newVersion)
}

// Updater handles Dart pubspec.yaml updating
type Updater struct {
	patternProvider *PatternProvider
}

// NewUpdater creates a new Dart updater
func NewUpdater() *Updater {
	return &Updater{
		patternProvider: &PatternProvider{},
	}
}

// GetPatternProvider returns the pattern provider for pub
func (updater *Updater) GetPatternProvider() shared.PatternProvider {
	if updater.patternProvider == nil {
		updater.patternProvider = &PatternProvider{}
	}
	return updater.patternProvider
}

// GetRegistryType returns the registry type this updater handles
func (updater *Updater) GetRegistryType() shared.RegistryType {
	return shared.Pub
}

// ValidateOptions validates options for pub updates
func (updater *Updater) ValidateOptions(options shared.Options) error {
	// Pub ecosystem doesn't support peer dependencies
	if options.IncludePeerDependencies {
		return fmt.Errorf("peer dependencies are not supported by pub")
	}
	// Monorepo mode is an npm-only concept
	if options.Monorepo {
		return fmt.Errorf("monorepo mode is only supported for npm projects")
	}
	return nil
}

// Ensure Updater implements the interface
var _ shared.Updater = (*Updater)(nil)
