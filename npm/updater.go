package npm

import (
	"fmt"
	"regexp"

	"github.com/MilosRandelovic/bump-core/shared"
)

// PatternProvider implements the pattern provider for npm package.json files
type PatternProvider struct{}

// GetPattern returns the regex pattern for matching npm dependency lines
func (patternProvider *PatternProvider) GetPattern(dependency shared.OutdatedDependency) string {
	// Look for: "package-name": "old-version"
	escapedName := regexp.QuoteMeta(dependency.Name)
	return fmt.Sprintf(`("%s"\s*:\s*)"([^"]*)"`, escapedName)
}

// GetReplacement returns the replacement string for npm dependency lines
func (patternProvider *PatternProvider) GetReplacement(dependency shared.OutdatedDependency, newVersion string) string {
	return fmt.Sprintf(`${1}"%s"`, newVersion)
}

// Updater handles npm package.json updating
type Updater struct {
	patternProvider *PatternProvider
}

// NewUpdater creates a new npm updater
func NewUpdater() *Updater {
	return &Updater{
		patternProvider: &PatternProvider{},
	}
}

// GetPatternProvider returns the pattern provider for npm
func (updater *Updater) GetPatternProvider() shared.PatternProvider {
	if updater.patternProvider == nil {
		updater.patternProvider = &PatternProvider{}
	}
	return updater.patternProvider
}

// GetRegistryType returns the registry type this updater handles
func (updater *Updater) GetRegistryType() shared.RegistryType {
	return shared.Npm
}

// ValidateOptions validates options for npm updates
func (updater *Updater) ValidateOptions(options shared.Options) error {
	// npm has no special option requirements
	return nil
}

// UpdateDependencies updates dependencies in a file - thin wrapper for tests that delegates to shared logic
func (updater *Updater) UpdateDependencies(filePath string, outdated []shared.OutdatedDependency, options shared.Options) error {
	if err := updater.ValidateOptions(options); err != nil {
		return err
	}
	return shared.UpdateDependenciesInFile(filePath, outdated, updater.GetPatternProvider(), options)
}

// Ensure Updater implements the interface
var _ shared.Updater = (*Updater)(nil)
