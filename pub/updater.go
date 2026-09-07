package pub

import (
	"fmt"
	"regexp"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

// PatternProvider locates and replaces dependency constraints in pubspec.yaml files.
type PatternProvider struct{}

// GetPattern returns a regular expression whose second capture group contains the dependency constraint.
func (patternProvider *PatternProvider) GetPattern(dependency shared.OutdatedDependency) string {
	// Group 2 must contain only the constraint because shared update validation
	// compares it with OriginalVersion. Groups 1, 3, and 4 preserve the quote
	// style, trailing whitespace, and inline comment respectively.
	if dependency.HostedURL != "" {
		return `(\s*version\s*:\s*["']?)([^"'#]*[^"'#\s])(["']?)(\s*(?:#.*)?)$`
	}
	escapedName := regexp.QuoteMeta(dependency.Name)
	return fmt.Sprintf(`(\s*%s\s*:\s*["']?)([^"'#]*[^"'#\s])(["']?)(\s*(?:#.*)?)$`, escapedName)
}

// GetReplacement returns a regexp expansion template that preserves quotes, whitespace, and inline comments.
func (patternProvider *PatternProvider) GetReplacement(dependency shared.OutdatedDependency, newVersion string) string {
	return fmt.Sprintf(`${1}%s${3}${4}`, newVersion)
}

// Updater supplies Pub-specific dependency update rules.
type Updater struct {
	patternProvider *PatternProvider
}

// NewUpdater returns a Pub updater with its pubspec pattern provider initialized.
func NewUpdater() *Updater {
	return &Updater{
		patternProvider: &PatternProvider{},
	}
}

// GetPatternProvider returns the pubspec pattern provider and also initializes a zero-value Updater.
func (updater *Updater) GetPatternProvider() shared.PatternProvider {
	if updater.patternProvider == nil {
		updater.patternProvider = &PatternProvider{}
	}
	return updater.patternProvider
}

// ValidateOptions rejects npm-only peer-dependency and monorepo options.
func (updater *Updater) ValidateOptions(options shared.Options) error {
	if options.IncludePeerDependencies {
		return fmt.Errorf("peer dependencies are not supported by pub")
	}
	if options.Monorepo {
		return fmt.Errorf("monorepo mode is only supported for npm projects")
	}
	return nil
}

var _ shared.Updater = (*Updater)(nil)
