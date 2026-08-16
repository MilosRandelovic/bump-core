package parser

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

// AutoDetectDependencyFile looks for package.json or pubspec.yaml in the given directory
func AutoDetectDependencyFile(directory string, log shared.LogFunc) (string, shared.RegistryType, error) {
	// Check for package.json first
	packageJson := filepath.Join(directory, "package.json")
	if _, err := os.Stat(packageJson); err == nil {
		relativePath, err := filepath.Rel(directory, packageJson)
		if err != nil {
			relativePath = packageJson
		}
		if log != nil {
			log("Found npm file: %s\n", relativePath)
		}
		return packageJson, shared.Npm, nil
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
	}

	return "", 0, fmt.Errorf("no package.json or pubspec.yaml found in directory: %s", directory)
}
