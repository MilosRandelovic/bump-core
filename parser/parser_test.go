package parser

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

func TestParseDependenciesRejectsUnsupportedRegistry(t *testing.T) {
	_, err := ParseDependencies("package.json", shared.RegistryType(99), shared.Options{})
	if !errors.Is(err, shared.ErrUnsupportedRegistryType) {
		t.Fatalf("expected unsupported-registry error, got %v", err)
	}
}

func TestParsePackageJson(t *testing.T) {

	tempDir := t.TempDir()
	packageJSONPath := filepath.Join(tempDir, "package.json")

	packageJSONContent := `{
		"dependencies": {
			"react": "^18.0.0",
			"lodash": "~4.17.20"
		},
		"devDependencies": {
			"typescript": ">=4.9.0"
		}
	}`

	err := os.WriteFile(packageJSONPath, []byte(packageJSONContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	dependencies, err := ParseDependencies(packageJSONPath, shared.NPM, shared.Options{})
	if err != nil {
		t.Fatalf("Failed to parse package.json: %v", err)
	}

	if len(dependencies) != 3 {
		t.Errorf("Expected 3 dependencies, got %d", len(dependencies))
	}

	cleanVersionMap := make(map[string]string)
	originalVersionMap := make(map[string]string)
	for _, dependency := range dependencies {
		cleanVersionMap[dependency.Name] = dependency.Version
		originalVersionMap[dependency.Name] = dependency.OriginalVersion
	}

	if cleanVersionMap["react"] != "18.0.0" {
		t.Errorf("Expected react clean version '18.0.0', got '%s'", cleanVersionMap["react"])
	}

	if cleanVersionMap["lodash"] != "4.17.20" {
		t.Errorf("Expected lodash clean version '4.17.20', got '%s'", cleanVersionMap["lodash"])
	}

	if cleanVersionMap["typescript"] != "4.9.0" {
		t.Errorf("Expected typescript clean version '4.9.0', got '%s'", cleanVersionMap["typescript"])
	}

	if originalVersionMap["react"] != "^18.0.0" {
		t.Errorf("Expected react original version '^18.0.0', got '%s'", originalVersionMap["react"])
	}

	if originalVersionMap["lodash"] != "~4.17.20" {
		t.Errorf("Expected lodash original version '~4.17.20', got '%s'", originalVersionMap["lodash"])
	}

	if originalVersionMap["typescript"] != ">=4.9.0" {
		t.Errorf("Expected typescript original version '>=4.9.0', got '%s'", originalVersionMap["typescript"])
	}
}

func TestParsePubspecYaml(t *testing.T) {

	tempDir := t.TempDir()
	pubspecPath := filepath.Join(tempDir, "pubspec.yaml")

	pubspecContent := `name: test_app
version: 1.0.0

dependencies:
  flutter:
    sdk: flutter
  http: ^0.13.0
  shared_preferences: ^2.0.0

dev_dependencies:
  flutter_test:
    sdk: flutter
  mockito: ^5.3.0
`

	err := os.WriteFile(pubspecPath, []byte(pubspecContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	dependencies, err := ParseDependencies(pubspecPath, shared.Pub, shared.Options{})
	if err != nil {
		t.Fatalf("Failed to parse pubspec.yaml: %v", err)
	}

	if len(dependencies) != 3 {
		t.Errorf("Expected 3 dependencies, got %d", len(dependencies))
		for i, dependency := range dependencies {
			t.Logf("  %d: %s = %s", i, dependency.Name, dependency.Version)
		}
	}

	cleanVersionMap := make(map[string]string)
	originalVersionMap := make(map[string]string)
	for _, dependency := range dependencies {
		cleanVersionMap[dependency.Name] = dependency.Version
		originalVersionMap[dependency.Name] = dependency.OriginalVersion
	}

	if cleanVersionMap["http"] != "0.13.0" {
		t.Errorf("Expected http clean version '0.13.0', got '%s'", cleanVersionMap["http"])
	}

	if cleanVersionMap["shared_preferences"] != "2.0.0" {
		t.Errorf("Expected shared_preferences clean version '2.0.0', got '%s'", cleanVersionMap["shared_preferences"])
	}

	if cleanVersionMap["mockito"] != "5.3.0" {
		t.Errorf("Expected mockito clean version '5.3.0', got '%s'", cleanVersionMap["mockito"])
	}

	if originalVersionMap["http"] != "^0.13.0" {
		t.Errorf("Expected http original version '^0.13.0', got '%s'", originalVersionMap["http"])
	}

	if originalVersionMap["shared_preferences"] != "^2.0.0" {
		t.Errorf("Expected shared_preferences original version '^2.0.0', got '%s'", originalVersionMap["shared_preferences"])
	}

	if originalVersionMap["mockito"] != "^5.3.0" {
		t.Errorf("Expected mockito original version '^5.3.0', got '%s'", originalVersionMap["mockito"])
	}

	if shared.CleanVersion("^1.0.0") != "1.0.0" {
		t.Errorf("shared.CleanVersion function not working correctly")
	}
}
