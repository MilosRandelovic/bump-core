package npm

import (
	"fmt"
	"regexp"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

// PatternProvider implements the pattern provider for npm package.json files
type PatternProvider struct{}

// GetPattern returns a regular expression whose second capture group contains the dependency constraint.
func (patternProvider *PatternProvider) GetPattern(dependency shared.OutdatedDependency) string {
	// Look for: "package-name": "old-version"
	escapedName := regexp.QuoteMeta(dependency.Name)
	return fmt.Sprintf(`("%s"\s*:\s*)"([^"]*)"`, escapedName)
}

// GetReplacement returns a regexp expansion template that replaces the constraint and preserves the JSON key and spacing.
func (patternProvider *PatternProvider) GetReplacement(dependency shared.OutdatedDependency, newVersion string) string {
	return fmt.Sprintf(`${1}"%s"`, newVersion)
}

// Updater handles npm package.json updating
type Updater struct {
	patternProvider *PatternProvider
}

// NewUpdater returns an npm updater with its package.json pattern provider initialized.
func NewUpdater() *Updater {
	return &Updater{
		patternProvider: &PatternProvider{},
	}
}

// GetPatternProvider returns the package.json pattern provider and also initializes a zero-value Updater.
func (updater *Updater) GetPatternProvider() shared.PatternProvider {
	if updater.patternProvider == nil {
		updater.patternProvider = &PatternProvider{}
	}
	return updater.patternProvider
}

// ValidateOptions accepts all currently defined update options for npm.
func (updater *Updater) ValidateOptions(options shared.Options) error {
	// npm has no special option requirements
	return nil
}

// Ensure Updater implements the interface
var _ shared.Updater = (*Updater)(nil)
