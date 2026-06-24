package shared

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// UpdateDependenciesInFile contains the shared logic for updating dependencies in a file
func UpdateDependenciesInFile(filePath string, outdated []OutdatedDependency, patternProvider PatternProvider, options Options) error {
	// If no dependencies to update, return early
	if len(outdated) == 0 {
		return nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Split content into lines
	lines := strings.Split(string(data), "\n")

	// Update each dependency by modifying its specific line
	for _, dependency := range outdated {
		if dependency.LineNumber < 1 || dependency.LineNumber > len(lines) {
			return fmt.Errorf("invalid line number %d for dependency %s", dependency.LineNumber, dependency.Name)
		}

		lineIndex := dependency.LineNumber - 1 // Convert to 0-based index
		line := lines[lineIndex]

		// Get the pattern from the provider
		pattern := patternProvider.GetPattern(dependency)
		versionRegex := regexp.MustCompile(pattern)
		matches := versionRegex.FindStringSubmatch(line)

		if len(matches) < 3 {
			return fmt.Errorf("could not find %s on line %d for updating", dependency.Name, dependency.LineNumber)
		}

		oldVersion := matches[2]
		prefix := GetVersionPrefix(oldVersion)
		newVersion := prefix + dependency.LatestVersion

		// Get the replacement string from the provider
		replacement := patternProvider.GetReplacement(dependency, newVersion)
		newLine := versionRegex.ReplaceAllString(line, replacement)
		lines[lineIndex] = newLine
	}

	// Join lines back together and write to file
	content := strings.Join(lines, "\n")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}
