package parser

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

// AutoDetectDependencyFile returns the first supported dependency file in directory.
// package.json takes precedence over pubspec.yaml; log receives the detected relative path when non-nil.
func AutoDetectDependencyFile(directory string, log shared.LogFunc) (string, shared.RegistryType, error) {

	// Check for package.json first
	packageJSON := filepath.Join(directory, "package.json")
	if _, err := os.Stat(packageJSON); err == nil {
		relativePath, err := filepath.Rel(directory, packageJSON)
		if err != nil {
			relativePath = packageJSON
		}
		if log != nil {
			log("Found npm file: %s\n", relativePath)
		}
		return packageJSON, shared.NPM, nil
	} else if !os.IsNotExist(err) {
		return "", 0, fmt.Errorf("failed to inspect package.json: %w", err)
	}

	// Check for pubspec.yaml
	pubspecYaml := filepath.Join(directory, "pubspec.yaml")
	if _, err := os.Stat(pubspecYaml); err == nil {
		relativePath, err := filepath.Rel(directory, pubspecYaml)
		if err != nil {
			relativePath = pubspecYaml
		}
		if log != nil {
			log("Found pub file: %s\n", relativePath)
		}
		return pubspecYaml, shared.Pub, nil
	} else if !os.IsNotExist(err) {
		return "", 0, fmt.Errorf("failed to inspect pubspec.yaml: %w", err)
	}

	return "", 0, fmt.Errorf("no package.json or pubspec.yaml found in directory: %s", directory)
}
