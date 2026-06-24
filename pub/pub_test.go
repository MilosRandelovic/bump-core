package pub

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MilosRandelovic/bump-core/shared"
)

func TestParsePubspecYaml(t *testing.T) {
	// Create a temporary pubspec.yaml file
	tempDir := t.TempDir()
	pubspecPath := filepath.Join(tempDir, "pubspec.yaml")

	pubspecContent := `name: test_package
dependencies:
  flutter:
    sdk: flutter
  http: ^0.13.5
  path: ^1.8.0
  intl: any
  private_package:
    hosted: https://private.registry.com/pub
    version: ^0.0.1
  pubdev_hosted:
    hosted: https://pub.dev
    version: ^1.0.0
dev_dependencies:
  flutter_test:
    sdk: flutter
  test: ">=1.21.0 <2.0.0"
`

	err := os.WriteFile(pubspecPath, []byte(pubspecContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	parser := NewParser()
	dependencies, err := parser.ParseDependencies(pubspecPath, shared.Options{})
	if err != nil {
		t.Fatalf("Failed to parse pubspec.yaml: %v", err)
	}

	// Should exclude flutter SDK dependency and 'any' versions
	// Should include: http, path, pubdev_hosted, private_package, test = 5 dependencies
	if len(dependencies) != 5 {
		t.Errorf("Expected 5 dependencies, got %d", len(dependencies))
		for _, dependency := range dependencies {
			t.Logf("Found dependency: %s - %s (hosted: %s)", dependency.Name, dependency.OriginalVersion, dependency.HostedURL)
		}
	}

	// Check specific dependencies
	cleanVersionMap := make(map[string]string)
	originalVersionMap := make(map[string]string)
	hostedURLMap := make(map[string]string)
	for _, dependency := range dependencies {
		cleanVersionMap[dependency.Name] = dependency.Version
		originalVersionMap[dependency.Name] = dependency.OriginalVersion
		hostedURLMap[dependency.Name] = dependency.HostedURL
	}

	// Check clean versions (without prefixes)
	if cleanVersionMap["http"] != "0.13.5" {
		t.Errorf("Expected http clean version '0.13.5', got '%s'", cleanVersionMap["http"])
	}

	if cleanVersionMap["path"] != "1.8.0" {
		t.Errorf("Expected path clean version '1.8.0', got '%s'", cleanVersionMap["path"])
	}

	// Check original versions (with prefixes)
	if originalVersionMap["http"] != "^0.13.5" {
		t.Errorf("Expected http original version '^0.13.5', got '%s'", originalVersionMap["http"])
	}

	if originalVersionMap["path"] != "^1.8.0" {
		t.Errorf("Expected path original version '^1.8.0', got '%s'", originalVersionMap["path"])
	}

	if originalVersionMap["test"] != ">=1.21.0 <2.0.0" {
		t.Errorf("Expected test original version '>=1.21.0 <2.0.0', got '%s'", originalVersionMap["test"])
	}

	// Check that pub.dev hosted packages are included
	if originalVersionMap["pubdev_hosted"] != "^1.0.0" {
		t.Errorf("Expected pubdev_hosted original version '^1.0.0', got '%s'", originalVersionMap["pubdev_hosted"])
	}

	// Check that 'any' versions are excluded
	if _, exists := originalVersionMap["intl"]; exists {
		t.Errorf("Expected intl ('any' version) to be excluded, but it was found with version '%s'", originalVersionMap["intl"])
	}

	// Check that private hosted packages are included with correct hosted URL
	if originalVersionMap["private_package"] != "^0.0.1" {
		t.Errorf("Expected private_package original version '^0.0.1', got '%s'", originalVersionMap["private_package"])
	}
	if hostedURLMap["private_package"] != "https://private.registry.com/pub" {
		t.Errorf("Expected private_package hosted URL 'https://private.registry.com/pub', got '%s'", hostedURLMap["private_package"])
	}

	// Check that pub.dev packages have empty hosted URL
	if hostedURLMap["http"] != "" {
		t.Errorf("Expected http to have empty hosted URL, got '%s'", hostedURLMap["http"])
	}
}

func TestUpdatePubspecYaml(t *testing.T) {
	// Create a temporary pubspec.yaml file
	tempDir := t.TempDir()
	pubspecPath := filepath.Join(tempDir, "pubspec.yaml")

	pubspecContent := `name: test_package
dependencies:
  flutter:
    sdk: flutter
  http: ^0.13.5
  path: ^1.8.0
dev_dependencies:
  flutter_test:
    sdk: flutter
  test: ">=1.21.0 <2.0.0"
`

	err := os.WriteFile(pubspecPath, []byte(pubspecContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Mock outdated dependencies
	outdated := []shared.OutdatedDependency{
		{
			BaseDependency: shared.BaseDependency{
				Name:            "http",
				OriginalVersion: "^0.13.5",
				Type:            shared.Dependencies,
				FilePath:        "",
				LineNumber:      5,
			},
			CurrentVersion: "0.13.5",
			LatestVersion:  "0.13.6",
		},
		{
			BaseDependency: shared.BaseDependency{
				Name:            "path",
				OriginalVersion: "^1.8.0",
				Type:            shared.Dependencies,
				FilePath:        "",
				LineNumber:      6,
			},
			CurrentVersion: "1.8.0",
			LatestVersion:  "1.8.3",
		},
	}

	updaterInstance := NewUpdater()
	err = updaterInstance.UpdateDependencies(pubspecPath, outdated, shared.Options{})
	if err != nil {
		t.Fatalf("Failed to update pubspec.yaml: %v", err)
	}

	// Read and verify the updated file
	updatedContent, err := os.ReadFile(pubspecPath)
	if err != nil {
		t.Fatalf("Failed to read updated file: %v", err)
	}

	updatedStr := string(updatedContent)

	// Check that versions were updated correctly with prefixes preserved
	if !strings.Contains(updatedStr, "http: ^0.13.6") {
		t.Errorf("http version not updated correctly, content: %s", updatedStr)
	}

	if !strings.Contains(updatedStr, "path: ^1.8.3") {
		t.Errorf("path version not updated correctly, content: %s", updatedStr)
	}

	// test should remain unchanged
	if !strings.Contains(updatedStr, `test: '>=1.21.0 <2.0.0'`) && !strings.Contains(updatedStr, `test: ">=1.21.0 <2.0.0"`) {
		t.Errorf("test version should not have changed, content: %s", updatedStr)
	}
}

func TestGetFileType(t *testing.T) {
	parser := NewParser()
	if parser.GetRegistryType() != shared.Pub {
		t.Errorf("Expected registry type Pub, got '%s'", parser.GetRegistryType().String())
	}

	updater := NewUpdater()
	if updater.GetRegistryType() != shared.Pub {
		t.Errorf("Expected registry type Pub, got '%s'", updater.GetRegistryType().String())
	}

	registry := NewRegistryClient()
	if registry.GetRegistryType() != shared.Pub {
		t.Errorf("Expected registry type Pub, got '%s'", registry.GetRegistryType().String())
	}
}

func TestParsePubspecYamlEdgeCases(t *testing.T) {
	// Test various edge cases for pubspec parsing
	tempDir := t.TempDir()
	pubspecPath := filepath.Join(tempDir, "pubspec.yaml")

	pubspecContent := `name: test_package
dependencies:
  # Regular dependencies
  http: ^0.13.5
  intl: any

  # SDK dependencies (should be skipped)
  flutter:
    sdk: flutter
  flutter_localizations:
    sdk: flutter

  # Private hosted packages (should be skipped)
  private_pkg:
    hosted: "https://private-registry.example.com"
    version: "1.0.0"

  # Public hosted packages (should be included)
  pubdev_hosted:
    hosted: https://pub.dev
    version: ^1.0.0

  # Git dependencies (should be skipped)
  git_pkg:
    git:
      url: https://github.com/example/repo.git
      ref: main

  # Path dependencies (should be skipped)
  local_pkg:
    path: ../local_package
`

	err := os.WriteFile(pubspecPath, []byte(pubspecContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	parser := NewParser()
	dependencies, err := parser.ParseDependencies(pubspecPath, shared.Options{})
	if err != nil {
		t.Fatalf("Failed to parse pubspec.yaml: %v", err)
	}

	// Should include: http, pubdev_hosted, private_pkg = 3 dependencies ('any' versions are filtered out)
	expectedDeps := []string{"http", "pubdev_hosted", "private_pkg"}
	if len(dependencies) != len(expectedDeps) {
		t.Errorf("Expected %d dependencies, got %d", len(expectedDeps), len(dependencies))
		for _, dependency := range dependencies {
			t.Logf("Found dependency: %s - %s (hosted: %s)", dependency.Name, dependency.OriginalVersion, dependency.HostedURL)
		}
	}

	// Create map for easier testing
	dependencyMap := make(map[string]shared.Dependency)
	for _, dependency := range dependencies {
		dependencyMap[dependency.Name] = dependency
	}

	// Verify each expected dependency
	for _, expectedName := range expectedDeps {
		if _, exists := dependencyMap[expectedName]; !exists {
			t.Errorf("Expected dependency '%s' not found", expectedName)
		}
	}

	// Check specific version handling for pubdev_hosted
	if dependency, exists := dependencyMap["pubdev_hosted"]; exists {
		if dependency.OriginalVersion != "^1.0.0" {
			t.Errorf("Expected pubdev_hosted original version '^1.0.0', got '%s'", dependency.OriginalVersion)
		}
		if dependency.Version != "1.0.0" {
			t.Errorf("Expected pubdev_hosted cleaned version '1.0.0', got '%s'", dependency.Version)
		}
		if dependency.HostedURL != "" {
			t.Errorf("Expected pubdev_hosted to have empty hosted URL, got '%s'", dependency.HostedURL)
		}
	}

	// Check private hosted package
	if dependency, exists := dependencyMap["private_pkg"]; exists {
		if dependency.OriginalVersion != "1.0.0" {
			t.Errorf("Expected private_pkg original version '1.0.0', got '%s'", dependency.OriginalVersion)
		}
		if dependency.HostedURL != "https://private-registry.example.com" {
			t.Errorf("Expected private_pkg hosted URL 'https://private-registry.example.com', got '%s'", dependency.HostedURL)
		}
	}

	// Verify excluded dependencies (including 'any' versions)
	excludedDeps := []string{"flutter", "flutter_localizations", "git_pkg", "local_pkg", "intl"}
	for _, excludedName := range excludedDeps {
		if _, exists := dependencyMap[excludedName]; exists {
			t.Errorf("Dependency '%s' should have been excluded but was found", excludedName)
		}
	}
}

func TestUpdatePreservesAllContent(t *testing.T) {
	// Realistic pubspec.yaml content with comments, metadata, assets, and dependencies
	originalContent := `name: my_flutter_app
description: A new Flutter application.
version: 1.0.0+1

environment:
  sdk: '>=3.1.0 <4.0.0'
  flutter: ">=3.13.0"

dependencies:
  flutter:
    sdk: flutter

  # HTTP client
  http: ^0.13.0

  # State management
  provider: ^6.0.0

  # Utilities
  collection: ^1.17.0

dev_dependencies:
  flutter_test:
    sdk: flutter

  # Code generation
  build_runner: ^2.4.0

  # Linting
  flutter_lints: ^2.0.0

flutter:
  uses-material-design: true

  # Assets
  assets:
    - assets/images/
    - assets/icons/

  # Fonts
  fonts:
    - family: CustomFont
      fonts:
        - asset: fonts/CustomFont-Regular.ttf
        - asset: fonts/CustomFont-Bold.ttf
          weight: 700

# Custom configuration
custom_config:
  feature_flags:
    new_ui: true
    analytics: false`

	// Create a temporary test file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "pubspec.yaml")

	// Write the original content
	err := os.WriteFile(testFile, []byte(originalContent), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Mock dependencies for update
	outdatedDependencies := []shared.OutdatedDependency{
		{
			BaseDependency: shared.BaseDependency{
				Name:            "http",
				OriginalVersion: "^0.13.0",
				Type:            shared.Dependencies,
				FilePath:        "",
				LineNumber:      14,
			},
			CurrentVersion: "0.13.0",
			LatestVersion:  "0.13.5",
		},
		{
			BaseDependency: shared.BaseDependency{
				Name:            "provider",
				OriginalVersion: "^6.0.0",
				Type:            shared.Dependencies,
				FilePath:        "",
				LineNumber:      17,
			},
			CurrentVersion: "6.0.0",
			LatestVersion:  "6.1.2",
		},
		{
			BaseDependency: shared.BaseDependency{
				Name:            "build_runner",
				OriginalVersion: "^2.4.0",
				Type:            shared.DevDependencies,
				FilePath:        "",
				LineNumber:      27,
			},
			CurrentVersion: "2.4.0",
			LatestVersion:  "2.4.7",
		},
	}

	// Update the dependencies
	updater := NewUpdater()
	err = updater.UpdateDependencies(testFile, outdatedDependencies, shared.Options{})
	if err != nil {
		t.Fatal(err)
	}

	// Read the updated content
	updatedContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}

	updatedStr := string(updatedContent)

	// Verify that critical non-dependency content is preserved
	criticalContent := []string{
		"name: my_flutter_app",
		"description: A new Flutter application.",
		"version: 1.0.0+1",
		"environment:",
		"sdk: '>=3.1.0 <4.0.0'",
		"flutter: \">=3.13.0\"",
		"# HTTP client",
		"# State management",
		"# Utilities",
		"# Code generation",
		"# Linting",
		"flutter:",
		"uses-material-design: true",
		"# Assets",
		"assets:",
		"- assets/images/",
		"- assets/icons/",
		"# Fonts",
		"fonts:",
		"- family: CustomFont",
		"fonts:",
		"- asset: fonts/CustomFont-Regular.ttf",
		"- asset: fonts/CustomFont-Bold.ttf",
		"weight: 700",
		"# Custom configuration",
		"custom_config:",
		"feature_flags:",
		"new_ui: true",
		"analytics: false",
	}

	for _, content := range criticalContent {
		if !strings.Contains(updatedStr, content) {
			t.Errorf("Critical content missing after update: %s", content)
		}
	}

	// Verify that dependencies were actually updated
	expectedUpdates := map[string]string{
		"http: ^0.13.5":        "http version should be updated to 0.13.5",
		"provider: ^6.1.2":     "provider version should be updated to 6.1.2",
		"build_runner: ^2.4.7": "build_runner version should be updated to 2.4.7",
	}

	for expectedText, errorMsg := range expectedUpdates {
		if !strings.Contains(updatedStr, expectedText) {
			t.Errorf("%s, but found:\n%s", errorMsg, updatedStr)
		}
	}

	// Verify that unchanged dependencies remain unchanged
	unchangedDeps := []string{
		"collection: ^1.17.0",
		"flutter_lints: ^2.0.0",
	}

	for _, dependency := range unchangedDeps {
		if !strings.Contains(updatedStr, dependency) {
			t.Errorf("Unchanged dependency missing: %s", dependency)
		}
	}
}

func TestUpdateHostedPackages(t *testing.T) {
	// Create a pubspec.yaml with hosted packages
	tempDir := t.TempDir()
	pubspecPath := filepath.Join(tempDir, "pubspec.yaml")

	pubspecContent := `name: test_app
dependencies:
  flutter:
    sdk: flutter

  # Regular pub.dev dependency
  http: ^0.13.0

  # Private hosted package
  company_core:
    hosted: https://packages.company.com/pub
    version: ^1.0.0

  # Another private hosted package
  internal_tools:
    hosted: https://internal-registry.example.com/pub/
    version: ~2.5.0

dev_dependencies:
  flutter_test:
    sdk: flutter

  # Private dev dependency
  company_test_utils:
    hosted: https://packages.company.com/pub
    version: ^0.3.0`

	err := os.WriteFile(pubspecPath, []byte(pubspecContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Mock outdated hosted dependencies
	outdated := []shared.OutdatedDependency{
		{
			BaseDependency: shared.BaseDependency{
				Name:            "company_core",
				OriginalVersion: "^1.0.0",
				Type:            shared.Dependencies,
				FilePath:        "",
				HostedURL:       "https://packages.company.com/pub",
				LineNumber:      12,
			},
			CurrentVersion: "1.0.0",
			LatestVersion:  "1.2.0",
		},
		{
			BaseDependency: shared.BaseDependency{
				Name:            "internal_tools",
				OriginalVersion: "~2.5.0",
				Type:            shared.Dependencies,
				FilePath:        "",
				HostedURL:       "https://internal-registry.example.com/pub/",
				LineNumber:      17,
			},
			CurrentVersion: "2.5.0",
			LatestVersion:  "2.6.1",
		},
		{
			BaseDependency: shared.BaseDependency{
				Name:            "http",
				OriginalVersion: "^0.13.0",
				Type:            shared.Dependencies,
				FilePath:        "",
				LineNumber:      7,
			},
			CurrentVersion: "0.13.0",
			LatestVersion:  "0.13.5",
		},
	}

	updaterInstance := NewUpdater()
	err = updaterInstance.UpdateDependencies(pubspecPath, outdated, shared.Options{})
	if err != nil {
		t.Fatalf("Failed to update pubspec.yaml: %v", err)
	}

	// Read and verify the updated file
	updatedContent, err := os.ReadFile(pubspecPath)
	if err != nil {
		t.Fatalf("Failed to read updated file: %v", err)
	}

	updatedStr := string(updatedContent)

	// Verify that versions were updated correctly
	if !strings.Contains(updatedStr, "http: ^0.13.5") {
		t.Errorf("http version not updated correctly")
	}

	// Check hosted package updates - these should update the version field, not the hosted field
	if !strings.Contains(updatedStr, "version: ^1.2.0") {
		t.Errorf("company_core version not updated correctly")
	}
	if !strings.Contains(updatedStr, "version: ~2.6.1") {
		t.Errorf("internal_tools version not updated correctly")
	}

	// Verify hosted URLs are preserved
	if !strings.Contains(updatedStr, "hosted: https://packages.company.com/pub") {
		t.Errorf("company_core hosted URL not preserved")
	}
	if !strings.Contains(updatedStr, "hosted: https://internal-registry.example.com/pub/") {
		t.Errorf("internal_tools hosted URL not preserved")
	}

	// Verify unchanged dependency
	if !strings.Contains(updatedStr, "company_test_utils:") {
		t.Errorf("company_test_utils should remain unchanged")
	}
}

func TestParsePubTokensFile(t *testing.T) {
	// Create a temporary pub-tokens.json file
	tempDir := t.TempDir()
	pubTokensPath := filepath.Join(tempDir, "pub-tokens.json")

	pubTokensContent := `{
  "version": 1,
  "hosted": [
    {
      "url": "https://packages.company.com/pub/",
      "token": "company_token_123"
    },
    {
      "url": "https://internal-registry.example.com/pub",
      "token": "internal_token_456"
    }
  ]
}`

	err := os.WriteFile(pubTokensPath, []byte(pubTokensContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test pub-tokens.json file: %v", err)
	}

	// Create config to parse into
	config := &PubConfig{
		Registries: make(map[string]RegistryConfig),
	}

	// Add default pub.dev registry
	config.Registries["pub.dev"] = RegistryConfig{
		URL: "https://pub.dev",
	}

	err = parsePubTokensFile(pubTokensPath, config)
	if err != nil {
		t.Fatalf("Failed to parse pub-tokens.json file: %v", err)
	}

	// Test that registries were added correctly
	expectedRegistries := map[string]struct {
		url   string
		token string
	}{
		"packages.company.com": {
			url:   "https://packages.company.com/pub/",
			token: "company_token_123",
		},
		"internal-registry.example.com": {
			url:   "https://internal-registry.example.com/pub",
			token: "internal_token_456",
		},
		"pub.dev": {
			url:   "https://pub.dev",
			token: "",
		},
	}

	if len(config.Registries) != len(expectedRegistries) {
		t.Errorf("Expected %d registries, got %d", len(expectedRegistries), len(config.Registries))
	}

	for hostname, expected := range expectedRegistries {
		if registry, exists := config.Registries[hostname]; !exists {
			t.Errorf("Expected registry for hostname '%s' not found", hostname)
		} else {
			if registry.URL != expected.url {
				t.Errorf("Expected URL for %s to be '%s', got '%s'", hostname, expected.url, registry.URL)
			}
			if registry.AuthToken != expected.token {
				t.Errorf("Expected token for %s to be '%s', got '%s'", hostname, expected.token, registry.AuthToken)
			}
		}
	}
}

func TestPubConfigIntegration(t *testing.T) {
	// This test verifies the full integration of parsing pub configuration
	// Create temporary directories to simulate the real environment
	tempDir := t.TempDir()

	// Create fake home directory structure
	homeDir := filepath.Join(tempDir, "home")
	dartDir := filepath.Join(homeDir, "Library", "Application Support", "dart")
	err := os.MkdirAll(dartDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create dart directory: %v", err)
	}

	// Create pub-tokens.json
	pubTokensPath := filepath.Join(dartDir, "pub-tokens.json")
	pubTokensContent := `{
  "version": 1,
  "hosted": [
    {
      "url": "https://packages.company.com/pub",
      "token": "company_token_abc"
    }
  ]
}`

	err = os.WriteFile(pubTokensPath, []byte(pubTokensContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create pub-tokens.json: %v", err)
	}

	// Save and set temporary HOME
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", homeDir)

	// Test the full configuration parsing
	config, err := parsePubConfig()
	if err != nil {
		t.Fatalf("Failed to parse pub config: %v", err)
	}

	// Verify default pub.dev registry is present
	if registry, exists := config.Registries["pub.dev"]; !exists {
		t.Errorf("Default pub.dev registry not found")
	} else if registry.URL != "https://pub.dev" {
		t.Errorf("Expected pub.dev URL to be 'https://pub.dev', got '%s'", registry.URL)
	}

	// Verify pub-tokens.json registries
	expectedRegistries := map[string]struct {
		url   string
		token string
	}{
		"packages.company.com": {
			url:   "https://packages.company.com/pub",
			token: "company_token_abc",
		},
	}

	for hostname, expected := range expectedRegistries {
		if registry, exists := config.Registries[hostname]; !exists {
			t.Errorf("Registry for hostname '%s' from pub-tokens.json not found", hostname)
		} else {
			if registry.URL != expected.url {
				t.Errorf("Expected URL for %s to be '%s', got '%s'", hostname, expected.url, registry.URL)
			}
			if registry.AuthToken != expected.token {
				t.Errorf("Expected token for %s to be '%s', got '%s'", hostname, expected.token, registry.AuthToken)
			}
		}
	}
}

func TestUpdaterWithInvertedSectionOrder(t *testing.T) {
	// Test pubspec.yaml with dev_dependencies before dependencies
	pubspecContent := `name: test_package
version: 1.0.0

dev_dependencies:
  test: ^1.21.0
  mockito: ^5.3.0

dependencies:
  http: ^0.13.5
  path: ^1.8.0

flutter:
  uses-material-design: true`

	// Create temporary files for testing
	tempDir := t.TempDir()
	pubspecPath1 := filepath.Join(tempDir, "pubspec1.yaml")
	pubspecPath2 := filepath.Join(tempDir, "pubspec2.yaml")

	// Write content to both files
	err := os.WriteFile(pubspecPath1, []byte(pubspecContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	err = os.WriteFile(pubspecPath2, []byte(pubspecContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	updater := &Updater{}

	// Test updating a dependency in the dependencies section
	outdatedDependencies := []shared.OutdatedDependency{
		{
			BaseDependency: shared.BaseDependency{
				Name:            "http",
				OriginalVersion: "^0.13.5",
				Type:            shared.Dependencies,
				FilePath:        "",
				LineNumber:      9,
			},
			CurrentVersion: "0.13.5",
			LatestVersion:  "0.13.6",
		},
	}

	err = updater.UpdateDependencies(pubspecPath1, outdatedDependencies, shared.Options{})
	if err != nil {
		t.Fatalf("Failed to update dependencies: %v", err)
	}

	// Read and verify the updated file
	updated, err := os.ReadFile(pubspecPath1)
	if err != nil {
		t.Fatalf("Failed to read updated file: %v", err)
	}
	updatedStr := string(updated)

	if !strings.Contains(updatedStr, "http: ^0.13.6") {
		t.Errorf("Expected http dependency to be updated to ^0.13.6")
	}

	// Verify the old version is not present
	if strings.Contains(updatedStr, "http: ^0.13.5") {
		t.Errorf("Old http version ^0.13.5 should not be present")
	}

	// Test updating a dev dependency
	devDependencies := []shared.OutdatedDependency{
		{
			BaseDependency: shared.BaseDependency{
				Name:            "test",
				OriginalVersion: "^1.21.0",
				Type:            shared.DevDependencies,
				FilePath:        "",
				LineNumber:      5,
			},
			CurrentVersion: "1.21.0",
			LatestVersion:  "1.22.0",
		},
	}

	updaterInstance := NewUpdater()
	err = updaterInstance.UpdateDependencies(pubspecPath2, devDependencies, shared.Options{})
	if err != nil {
		t.Fatalf("Failed to update dev dependencies: %v", err)
	}

	// Read and verify the updated file
	updated2, err := os.ReadFile(pubspecPath2)
	if err != nil {
		t.Fatalf("Failed to read updated file: %v", err)
	}
	updated2Str := string(updated2)

	if !strings.Contains(updated2Str, "test: ^1.22.0") {
		t.Errorf("Expected test dev dependency to be updated to ^1.22.0")
	}

	// Verify the old version is not present
	if strings.Contains(updated2Str, "test: ^1.21.0") {
		t.Errorf("Old test version ^1.21.0 should not be present")
	}

	// Verify that updating dependencies section doesn't affect dev_dependencies section
	if !strings.Contains(updatedStr, "test: ^1.21.0") {
		t.Errorf("test dev dependency should remain unchanged when updating dependencies")
	}

	// Verify that updating dev_dependencies section doesn't affect dependencies section
	if !strings.Contains(updated2Str, "http: ^0.13.5") {
		t.Errorf("http dependency should remain unchanged when updating dev dependencies")
	}
}

func TestHostedURLTrailingSlashDoesNotProduceDoubleSlash(t *testing.T) {
	tempDir := t.TempDir()
	pubspecPath := filepath.Join(tempDir, "pubspec.yaml")

	pubspecContent := `name: test_package
dependencies:
  trailing_slash_pkg:
    hosted: https://registry.example.com/pub/
    version: ^1.0.0
  no_trailing_slash_pkg:
    hosted: https://registry.example.com/pub
    version: ^2.0.0
`

	err := os.WriteFile(pubspecPath, []byte(pubspecContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	parser := NewParser()
	dependencies, err := parser.ParseDependencies(pubspecPath, shared.Options{})
	if err != nil {
		t.Fatalf("Failed to parse pubspec.yaml: %v", err)
	}

	if len(dependencies) != 2 {
		t.Fatalf("Expected 2 dependencies, got %d", len(dependencies))
	}

	for _, dependency := range dependencies {
		registryURL := dependency.HostedURL
		// Simulate the same URL construction used in registry.go
		url := fmt.Sprintf("%s/api/packages/%s", strings.TrimRight(registryURL, "/"), dependency.Name)
		if strings.Contains(url, "//api") {
			t.Errorf("URL for %s contains double slash: %s", dependency.Name, url)
		}
		expectedURL := fmt.Sprintf("https://registry.example.com/pub/api/packages/%s", dependency.Name)
		if url != expectedURL {
			t.Errorf("Expected URL %s, got %s", expectedURL, url)
		}
	}
}

func TestParseHostedDependencyBothForms(t *testing.T) {
	tempDir := t.TempDir()
	pubspecPath := filepath.Join(tempDir, "pubspec.yaml")

	pubspecContent := `name: hosted_forms_test
dependencies:
  scalar_hosted_pkg:
    hosted: https://registry.example.com/pub
    version: ^1.0.0
  map_hosted_pkg:
    hosted:
      name: map_hosted_pkg
      url: https://registry.example.com/pub/
    version: ^2.0.0
`

	err := os.WriteFile(pubspecPath, []byte(pubspecContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	parser := NewParser()
	dependencies, err := parser.ParseDependencies(pubspecPath, shared.Options{})
	if err != nil {
		t.Fatalf("Failed to parse pubspec.yaml: %v", err)
	}

	if len(dependencies) != 2 {
		t.Fatalf("Expected 2 dependencies, got %d", len(dependencies))
	}

	dependencyMap := make(map[string]shared.Dependency)
	for _, dependency := range dependencies {
		dependencyMap[dependency.Name] = dependency
	}

	scalarDependency, scalarExists := dependencyMap["scalar_hosted_pkg"]
	if !scalarExists {
		t.Fatalf("scalar_hosted_pkg not found")
	}
	if scalarDependency.HostedURL != "https://registry.example.com/pub" {
		t.Errorf("Expected scalar hosted URL https://registry.example.com/pub, got %s", scalarDependency.HostedURL)
	}

	mapDependency, mapExists := dependencyMap["map_hosted_pkg"]
	if !mapExists {
		t.Fatalf("map_hosted_pkg not found")
	}
	if mapDependency.HostedURL != "https://registry.example.com/pub/" {
		t.Errorf("Expected map hosted URL https://registry.example.com/pub/, got %s", mapDependency.HostedURL)
	}
}
