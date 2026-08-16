package npm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

func applyTestUpdate(filePath string, outdated []shared.OutdatedDependency, updater *Updater, options shared.Options) error {
	if err := updater.ValidateOptions(options); err != nil {
		return err
	}
	prepared, err := shared.PrepareDependenciesInFile(filePath, outdated, updater.GetPatternProvider())
	if err != nil || prepared == nil {
		return err
	}
	return prepared.Apply()
}

func TestRegistryClientPreservesAbsoluteLatestOnConstraintError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		fmt.Fprint(response, `{"dist-tags":{"latest":"1.7.0"},"versions":{"1.0.0":{"version":"1.0.0"},"1.7.0":{"version":"1.7.0"}}}`)
	}))
	defer server.Close()

	client := NewRegistryClient()
	client.ConfigDirectory = t.TempDir()
	absolute, compatible, err := client.GetBothLatestVersions(context.Background(), "example", "^0.0.1", server.URL, shared.Options{}, nil)
	if !errors.Is(err, shared.ErrNoVersionsSatisfyConstraint) {
		t.Fatalf("expected constraint error, got %v", err)
	}
	if absolute != "1.7.0" || compatible != "" {
		t.Fatalf("versions = (%q, %q), expected (1.7.0, empty)", absolute, compatible)
	}
}

func TestRegistryClientMinimumAgeSelectsNewestEligibleVersion(t *testing.T) {
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(response, `{
			"dist-tags":{"latest":"2.0.0"},
			"versions":{"1.0.0":{"version":"1.0.0"},"1.5.0":{"version":"1.5.0"},"1.8.0":{"version":"1.8.0","deprecated":"unsafe"},"1.9.0-beta.1":{"version":"1.9.0-beta.1"},"2.0.0":{"version":"2.0.0"}},
			"time":{"1.0.0":%q,"1.5.0":%q,"1.8.0":%q,"1.9.0-beta.1":%q,"2.0.0":%q}
		}`, now.Add(-72*time.Hour).Format(time.RFC3339), now.Add(-25*time.Hour).Format(time.RFC3339), now.Add(-30*time.Hour).Format(time.RFC3339), now.Add(-30*time.Hour).Format(time.RFC3339), now.Add(-time.Hour).Format(time.RFC3339))
	}))
	defer server.Close()

	client := NewRegistryClient()
	client.ConfigDirectory = t.TempDir()
	client.currentTime = func() time.Time { return now }
	options := shared.Options{EnforceMinimumReleaseAge: true}

	latest, err := client.GetLatestVersionFromRegistry(context.Background(), "example", server.URL, options, nil)
	if err != nil {
		t.Fatal(err)
	}
	if latest != "1.5.0" {
		t.Fatalf("latest = %q, expected 1.5.0", latest)
	}

	absolute, compatible, err := client.GetBothLatestVersions(context.Background(), "example", "^1.0.0", server.URL, options, nil)
	if err != nil {
		t.Fatal(err)
	}
	if absolute != "1.5.0" || compatible != "1.5.0" {
		t.Fatalf("versions = (%q, %q), expected (1.5.0, 1.5.0)", absolute, compatible)
	}
}

func TestRegistryClientMinimumAgeReturnsNoCandidateWhenAllVersionsAreYoung(t *testing.T) {
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(response, `{"dist-tags":{"latest":"1.0.0"},"versions":{"1.0.0":{"version":"1.0.0"}},"time":{"1.0.0":%q}}`, now.Add(-time.Hour).Format(time.RFC3339))
	}))
	defer server.Close()

	client := NewRegistryClient()
	client.ConfigDirectory = t.TempDir()
	client.currentTime = func() time.Time { return now }
	_, err := client.GetLatestVersionFromRegistry(context.Background(), "example", server.URL, shared.Options{EnforceMinimumReleaseAge: true}, nil)
	if !errors.Is(err, shared.ErrNoVersionsMeetMinimumReleaseAge) {
		t.Fatalf("expected minimum-age error, got %v", err)
	}
}

func TestRegistryClientMinimumAgeRejectsUnverifiableVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		fmt.Fprint(response, `{"dist-tags":{"latest":"1.0.0"},"versions":{"1.0.0":{"version":"1.0.0"}},"time":{}}`)
	}))
	defer server.Close()

	client := NewRegistryClient()
	client.ConfigDirectory = t.TempDir()
	_, err := client.GetLatestVersionFromRegistry(context.Background(), "example", server.URL, shared.Options{EnforceMinimumReleaseAge: true}, nil)
	if err == nil || !strings.Contains(err.Error(), "could not verify publication times") {
		t.Fatalf("expected publication-time error, got %v", err)
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
		},
		"peerDependencies": {
			"react-dom": "^18.0.0"
		}
	}`

	err := os.WriteFile(packageJSONPath, []byte(packageJSONContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	parser := NewParser()
	dependencies, err := parser.ParseDependencies(packageJSONPath, shared.Options{})
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

func TestParseSingleLinePackageJSON(t *testing.T) {
	packagePath := filepath.Join(t.TempDir(), "package.json")
	content := `{"dependencies":{"react":"^18.0.0","lodash":"~4.17.20"},"devDependencies":{"typescript":">=5.0.0"}}`
	if err := os.WriteFile(packagePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	dependencies, err := NewParser().ParseDependencies(packagePath, shared.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(dependencies) != 3 {
		t.Fatalf("dependencies = %#v", dependencies)
	}
	for _, dependency := range dependencies {
		if dependency.LineNumber != 1 {
			t.Fatalf("%s line = %d, expected 1", dependency.Name, dependency.LineNumber)
		}
	}
}

func TestParseInvalidPackageJSONReturnsError(t *testing.T) {
	packagePath := filepath.Join(t.TempDir(), "package.json")
	if err := os.WriteFile(packagePath, []byte(`{"dependencies":{"broken":}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewParser().ParseDependencies(packagePath, shared.Options{}); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

func TestParsePeerDependencies(t *testing.T) {

	tempDir := t.TempDir()
	packageJSONPath := filepath.Join(tempDir, "package.json")

	packageJSONContent := `{
		"name": "test-package",
		"version": "1.0.0",
		"peerDependencies": {
			"react": "^18.0.0",
			"react-dom": "^18.0.0",
			"@types/react": ">=18.0.0",
			"lodash": "~4.17.20"
		}
	}`

	err := os.WriteFile(packageJSONPath, []byte(packageJSONContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	parser := NewParser()
	dependencies, err := parser.ParseDependencies(packageJSONPath, shared.Options{IncludePeerDependencies: true})
	if err != nil {
		t.Fatalf("Failed to parse package.json: %v", err)
	}

	if len(dependencies) != 4 {
		t.Errorf("Expected 4 peer dependencies, got %d", len(dependencies))
		for _, dependency := range dependencies {
			t.Logf("Found dependency: %s - %s", dependency.Name, dependency.OriginalVersion)
		}
	}

	cleanVersionMap := make(map[string]string)
	originalVersionMap := make(map[string]string)
	for _, dependency := range dependencies {
		cleanVersionMap[dependency.Name] = dependency.Version
		originalVersionMap[dependency.Name] = dependency.OriginalVersion
	}

	expectedDeps := map[string]struct {
		cleanVersion    string
		originalVersion string
	}{
		"react":        {"18.0.0", "^18.0.0"},
		"react-dom":    {"18.0.0", "^18.0.0"},
		"@types/react": {"18.0.0", ">=18.0.0"},
		"lodash":       {"4.17.20", "~4.17.20"},
	}

	for name, expected := range expectedDeps {
		if cleanVersionMap[name] != expected.cleanVersion {
			t.Errorf("Expected %s clean version '%s', got '%s'", name, expected.cleanVersion, cleanVersionMap[name])
		}
		if originalVersionMap[name] != expected.originalVersion {
			t.Errorf("Expected %s original version '%s', got '%s'", name, expected.originalVersion, originalVersionMap[name])
		}
	}
}

func TestUpdatePackageJson(t *testing.T) {

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

	parser := NewParser()
	dependencies, err := parser.ParseDependencies(packageJSONPath, shared.Options{})
	if err != nil {
		t.Fatalf("Failed to parse package.json: %v", err)
	}

	lineNumbers := make(map[string]int)
	for _, dependency := range dependencies {
		lineNumbers[dependency.Name] = dependency.LineNumber
	}

	outdated := []shared.OutdatedDependency{
		{
			BaseDependency: shared.BaseDependency{
				Name:            "react",
				OriginalVersion: "^18.0.0",
				Type:            shared.Dependencies,
				FilePath:        "",
				LineNumber:      lineNumbers["react"],
			},
			CurrentVersion: "18.0.0",
			LatestVersion:  "18.2.0",
		},
		{
			BaseDependency: shared.BaseDependency{
				Name:            "lodash",
				OriginalVersion: "~4.17.20",
				Type:            shared.Dependencies,
				FilePath:        "",
				LineNumber:      lineNumbers["lodash"],
			},
			CurrentVersion: "4.17.20",
			LatestVersion:  "4.17.21",
		},
	}

	updater := NewUpdater()
	err = applyTestUpdate(packageJSONPath, outdated, updater, shared.Options{})
	if err != nil {
		t.Fatalf("Failed to update package.json: %v", err)
	}

	updatedContent, err := os.ReadFile(packageJSONPath)
	if err != nil {
		t.Fatalf("Failed to read updated file: %v", err)
	}

	updatedStr := string(updatedContent)

	if !strings.Contains(updatedStr, `"react": "^18.2.0"`) {
		t.Errorf("React version not updated correctly, content: %s", updatedStr)
	}

	if !strings.Contains(updatedStr, `"lodash": "~4.17.21"`) {
		t.Errorf("Lodash version not updated correctly, content: %s", updatedStr)
	}

	if !strings.Contains(updatedStr, `"typescript": ">=4.9.0"`) {
		t.Errorf("TypeScript version should not have changed, content: %s", updatedStr)
	}
}

func TestUpdatePreservesAllContent(t *testing.T) {

	originalContent := `{
  "name": "my-react-app",
  "version": "0.1.0",
  "private": true,
  "description": "A comprehensive React application",
  "author": "John Doe <john@example.com>",
  "license": "MIT",
  "keywords": ["react", "frontend", "web"],
  "homepage": "https://example.com",
  "repository": {
    "type": "git",
    "url": "https://github.com/user/my-react-app.git"
  },
  "bugs": {
    "url": "https://github.com/user/my-react-app/issues"
  },
  "engines": {
    "node": ">=16.0.0",
    "npm": ">=8.0.0"
  },
  "scripts": {
    "start": "react-scripts start",
    "build": "react-scripts build",
    "test": "react-scripts test",
    "eject": "react-scripts eject",
    "lint": "eslint src/",
    "format": "prettier --write src/"
  },
  "dependencies": {
    "react": "^18.0.0",
    "react-dom": "^18.0.0",
    "axios": "^1.4.0",
    "lodash": "~4.17.20",
    "moment": ">=2.29.0"
  },
  "devDependencies": {
    "react-scripts": "5.0.1",
    "typescript": ">=4.9.0",
    "eslint": "^8.45.0",
    "prettier": "^2.8.0",
    "@types/react": "^18.0.0"
  },
  "peerDependencies": {
    "react": ">=16.8.0"
  },
  "browserslist": {
    "production": [
      ">0.2%",
      "not dead",
      "not op_mini all"
    ],
    "development": [
      "last 1 chrome version",
      "last 1 firefox version",
      "last 1 safari version"
    ]
  },
  "eslintConfig": {
    "extends": [
      "react-app",
      "react-app/jest"
    ]
  },
  "jest": {
    "collectCoverageFrom": [
      "src/**/*.{js,jsx,ts,tsx}",
      "!src/index.js"
    ]
  },
  "proxy": "http://localhost:3001",
  "custom": {
    "feature_flags": {
      "new_ui": true,
      "analytics": false
    },
    "build_config": {
      "optimization": "advanced",
      "source_maps": true
    }
  }
}`

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "package.json")

	err := os.WriteFile(testFile, []byte(originalContent), 0644)
	if err != nil {
		t.Fatal(err)
	}

	outdatedDependencies := []shared.OutdatedDependency{
		{
			BaseDependency: shared.BaseDependency{
				Name:            "react",
				OriginalVersion: "^18.0.0",
				Type:            shared.Dependencies,
				FilePath:        "",
				LineNumber:      30,
			},
			CurrentVersion: "18.0.0",
			LatestVersion:  "18.2.0",
		},
		{
			BaseDependency: shared.BaseDependency{
				Name:            "axios",
				OriginalVersion: "^1.4.0",
				Type:            shared.Dependencies,
				FilePath:        "",
				LineNumber:      32,
			},
			CurrentVersion: "1.4.0",
			LatestVersion:  "1.5.0",
		},
		{
			BaseDependency: shared.BaseDependency{
				Name:            "eslint",
				OriginalVersion: "^8.45.0",
				Type:            shared.DevDependencies,
				FilePath:        "",
				LineNumber:      39,
			},
			CurrentVersion: "8.45.0",
			LatestVersion:  "8.47.0",
		},
	}

	updater := NewUpdater()
	err = applyTestUpdate(testFile, outdatedDependencies, updater, shared.Options{})
	if err != nil {
		t.Fatal(err)
	}

	updatedContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}

	updatedStr := string(updatedContent)

	criticalContent := []string{
		`"name": "my-react-app"`,
		`"version": "0.1.0"`,
		`"private": true`,
		`"description": "A comprehensive React application"`,
		`"author": "John Doe <john@example.com>"`,
		`"license": "MIT"`,
		`"keywords": ["react", "frontend", "web"]`,
		`"homepage": "https://example.com"`,
		`"repository":`,
		`"type": "git"`,
		`"url": "https://github.com/user/my-react-app.git"`,
		`"bugs":`,
		`"engines":`,
		`"node": ">=16.0.0"`,
		`"npm": ">=8.0.0"`,
		`"scripts":`,
		`"start": "react-scripts start"`,
		`"build": "react-scripts build"`,
		`"test": "react-scripts test"`,
		`"eject": "react-scripts eject"`,
		`"lint": "eslint src/"`,
		`"format": "prettier --write src/"`,
		`"peerDependencies":`,
		`"browserslist":`,
		`"production":`,
		`">0.2%"`,
		`"not dead"`,
		`"not op_mini all"`,
		`"development":`,
		`"last 1 chrome version"`,
		`"eslintConfig":`,
		`"extends":`,
		`"react-app"`,
		`"react-app/jest"`,
		`"jest":`,
		`"collectCoverageFrom":`,
		`"src/**/*.{js,jsx,ts,tsx}"`,
		`"!src/index.js"`,
		`"proxy": "http://localhost:3001"`,
		`"custom":`,
		`"feature_flags":`,
		`"new_ui": true`,
		`"analytics": false`,
		`"build_config":`,
		`"optimization": "advanced"`,
		`"source_maps": true`,
	}

	for _, content := range criticalContent {
		if !strings.Contains(updatedStr, content) {
			t.Errorf("Critical content missing after update: %s", content)
		}
	}

	expectedUpdates := map[string]string{
		`"react": "^18.2.0"`:  "react version should be updated to 18.2.0",
		`"axios": "^1.5.0"`:   "axios version should be updated to 1.5.0",
		`"eslint": "^8.47.0"`: "eslint version should be updated to 8.47.0",
	}

	for expectedText, errorMsg := range expectedUpdates {
		if !strings.Contains(updatedStr, expectedText) {
			t.Errorf("%s, but found:\n%s", errorMsg, updatedStr)
		}
	}

	unchangedDeps := []string{
		`"react-dom": "^18.0.0"`,
		`"lodash": "~4.17.20"`,
		`"moment": ">=2.29.0"`,
		`"typescript": ">=4.9.0"`,
		`"prettier": "^2.8.0"`,
	}

	for _, unchangedDependency := range unchangedDeps {
		if !strings.Contains(updatedStr, unchangedDependency) {
			t.Errorf("Unchanged dependency missing: %s", unchangedDependency)
		}
	}

	if !strings.Contains(updatedStr, `"react": "^18.2.0"`) {
		t.Errorf("React dependency should be updated to ^18.2.0")
	}

	if !strings.Contains(updatedStr, `"react": ">=16.8.0"`) {
		t.Errorf("PeerDependencies should remain unchanged when includePeerDependencies is false")
	}
}

func TestParseScopedPackages(t *testing.T) {

	tempDir := t.TempDir()
	packageJSONPath := filepath.Join(tempDir, "package.json")

	packageJSONContent := `{
		"dependencies": {
			"react": "^18.0.0",
			"@company/private-pkg": "^1.2.3",
			"@angular/core": "^16.0.0",
			"@types/node": "^20.0.0"
		},
		"devDependencies": {
			"@company/dev-tools": "~2.1.0",
			"@babel/core": ">=7.22.0"
		},
		"peerDependencies": {
			"@angular/common": "^16.0.0"
		}
	}`

	err := os.WriteFile(packageJSONPath, []byte(packageJSONContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	parser := NewParser()
	dependencies, err := parser.ParseDependencies(packageJSONPath, shared.Options{})
	if err != nil {
		t.Fatalf("Failed to parse package.json: %v", err)
	}

	if len(dependencies) != 6 {
		t.Errorf("Expected 6 dependencies, got %d", len(dependencies))
		for _, dependency := range dependencies {
			t.Logf("Found dependency: %s - %s", dependency.Name, dependency.OriginalVersion)
		}
	}

	cleanVersionMap := make(map[string]string)
	originalVersionMap := make(map[string]string)
	for _, dependency := range dependencies {
		cleanVersionMap[dependency.Name] = dependency.Version
		originalVersionMap[dependency.Name] = dependency.OriginalVersion
	}

	expectedDeps := map[string]struct {
		cleanVersion    string
		originalVersion string
	}{
		"react":                {"18.0.0", "^18.0.0"},
		"@company/private-pkg": {"1.2.3", "^1.2.3"},
		"@angular/core":        {"16.0.0", "^16.0.0"},
		"@types/node":          {"20.0.0", "^20.0.0"},
		"@company/dev-tools":   {"2.1.0", "~2.1.0"},
		"@babel/core":          {"7.22.0", ">=7.22.0"},
	}

	for name, expected := range expectedDeps {
		if cleanVersionMap[name] != expected.cleanVersion {
			t.Errorf("Expected %s clean version '%s', got '%s'", name, expected.cleanVersion, cleanVersionMap[name])
		}
		if originalVersionMap[name] != expected.originalVersion {
			t.Errorf("Expected %s original version '%s', got '%s'", name, expected.originalVersion, originalVersionMap[name])
		}
	}
}

func TestUpdateScopedPackages(t *testing.T) {

	tempDir := t.TempDir()
	packageJSONPath := filepath.Join(tempDir, "package.json")

	packageJSONContent := `{
  "dependencies": {
    "react": "^18.0.0",
    "@company/private-pkg": "^1.2.3",
    "@angular/core": "^16.0.0"
  },
  "devDependencies": {
    "@company/dev-tools": "~2.1.0",
    "@babel/core": ">=7.22.0"
  }
}`

	err := os.WriteFile(packageJSONPath, []byte(packageJSONContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	outdated := []shared.OutdatedDependency{
		{
			BaseDependency: shared.BaseDependency{
				Name:            "@company/private-pkg",
				OriginalVersion: "^1.2.3",
				Type:            shared.Dependencies,
				FilePath:        "",
				LineNumber:      4,
			},
			CurrentVersion: "1.2.3",
			LatestVersion:  "1.3.0",
		},
		{
			BaseDependency: shared.BaseDependency{
				Name:            "@angular/core",
				OriginalVersion: "^16.0.0",
				Type:            shared.Dependencies,
				FilePath:        "",
				LineNumber:      5,
			},
			CurrentVersion: "16.0.0",
			LatestVersion:  "16.2.0",
		},
		{
			BaseDependency: shared.BaseDependency{
				Name:            "@babel/core",
				OriginalVersion: ">=7.22.0",
				Type:            shared.DevDependencies,
				FilePath:        "",
				LineNumber:      9,
			},
			CurrentVersion: "7.22.0",
			LatestVersion:  "7.22.5",
		},
	}

	updater := NewUpdater()
	err = applyTestUpdate(packageJSONPath, outdated, updater, shared.Options{})
	if err != nil {
		t.Fatalf("Failed to update package.json: %v", err)
	}

	updatedContent, err := os.ReadFile(packageJSONPath)
	if err != nil {
		t.Fatalf("Failed to read updated file: %v", err)
	}

	updatedStr := string(updatedContent)

	expectedUpdates := map[string]string{
		`"@company/private-pkg": "^1.3.0"`: "@company/private-pkg should be updated to ^1.3.0",
		`"@angular/core": "^16.2.0"`:       "@angular/core should be updated to ^16.2.0",
		`"@babel/core": ">=7.22.5"`:        "@babel/core should be updated to >=7.22.5",
	}

	for expectedText, errorMsg := range expectedUpdates {
		if !strings.Contains(updatedStr, expectedText) {
			t.Errorf("%s, but got: %s", errorMsg, updatedStr)
		}
	}

	if !strings.Contains(updatedStr, `"react": "^18.0.0"`) {
		t.Errorf("React version should not have changed")
	}
	if !strings.Contains(updatedStr, `"@company/dev-tools": "~2.1.0"`) {
		t.Errorf("@company/dev-tools version should not have changed")
	}
}

func TestParseNpmrcFile(t *testing.T) {

	tempDir := t.TempDir()
	npmrcPath := filepath.Join(tempDir, ".npmrc")

	npmrcContent := `# npm configuration
registry=https://registry.npmjs.org/
@company:registry=https://npm.company.com
@internal:registry=https://internal-registry.example.com/

# Authentication tokens
//npm.company.com/:_authToken=company_token_123
//internal-registry.example.com/:_authToken="internal_token_456"
//registry.example.com/:_authToken='quoted_token_789'

# Comments and empty lines should be ignored

; Semicolon comments too
`

	err := os.WriteFile(npmrcPath, []byte(npmrcContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test .npmrc file: %v", err)
	}

	config, err := parseNpmrcFile(npmrcPath)
	if err != nil {
		t.Fatalf("Failed to parse .npmrc file: %v", err)
	}

	expectedScopeRegistries := map[string]string{
		"@company":  "https://npm.company.com",
		"@internal": "https://internal-registry.example.com/",
	}

	for scope, expectedRegistry := range expectedScopeRegistries {
		if actualRegistry, exists := config.ScopeRegistries[scope]; !exists {
			t.Errorf("Expected scope registry for %s not found", scope)
		} else if actualRegistry != expectedRegistry {
			t.Errorf("Expected scope registry for %s to be '%s', got '%s'", scope, expectedRegistry, actualRegistry)
		}
	}

	expectedAuthTokens := map[string]string{
		"npm.company.com":               "company_token_123",
		"internal-registry.example.com": "internal_token_456",
		"registry.example.com":          "quoted_token_789",
	}

	for registry, expectedToken := range expectedAuthTokens {
		if actualToken, exists := config.AuthTokens[registry]; !exists {
			t.Errorf("Expected auth token for %s not found", registry)
		} else if actualToken != expectedToken {
			t.Errorf("Expected auth token for %s to be '%s', got '%s'", registry, expectedToken, actualToken)
		}
	}
}

func TestParseNpmrcFilesWithGlobalAndLocal(t *testing.T) {

	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	projectDir := filepath.Join(tempDir, "project")

	err := os.MkdirAll(homeDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create home directory: %v", err)
	}
	err = os.MkdirAll(projectDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create project directory: %v", err)
	}

	globalNpmrcPath := filepath.Join(homeDir, ".npmrc")
	globalNpmrcContent := `@company:registry=https://global.company.com
//global.company.com/:_authToken=global_token
//shared-registry.com/:_authToken=global_shared_token`

	err = os.WriteFile(globalNpmrcPath, []byte(globalNpmrcContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create global .npmrc file: %v", err)
	}

	localNpmrcPath := filepath.Join(projectDir, ".npmrc")
	localNpmrcContent := `@company:registry=https://local.company.com
@internal:registry=https://internal.example.com
//local.company.com/:_authToken=local_token
//shared-registry.com/:_authToken=local_shared_token`

	err = os.WriteFile(localNpmrcPath, []byte(localNpmrcContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create local .npmrc file: %v", err)
	}

	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", homeDir)

	config, err := parseNpmrcFiles(localNpmrcPath)
	if err != nil {
		t.Fatalf("Failed to parse .npmrc files: %v", err)
	}

	if config.ScopeRegistries["@company"] != "https://local.company.com" {
		t.Errorf("Expected local @company registry to override global, got '%s'", config.ScopeRegistries["@company"])
	}

	if config.ScopeRegistries["@internal"] != "https://internal.example.com" {
		t.Errorf("Expected @internal registry to be '%s', got '%s'", "https://internal.example.com", config.ScopeRegistries["@internal"])
	}

	if config.AuthTokens["shared-registry.com"] != "local_shared_token" {
		t.Errorf("Expected local shared token to override global, got '%s'", config.AuthTokens["shared-registry.com"])
	}

	if config.AuthTokens["local.company.com"] != "local_token" {
		t.Errorf("Expected local.company.com token to be 'local_token', got '%s'", config.AuthTokens["local.company.com"])
	}

	if config.AuthTokens["global.company.com"] != "global_token" {
		t.Errorf("Expected global.company.com token to be 'global_token', got '%s'", config.AuthTokens["global.company.com"])
	}
}

func TestGetRegistryForPackage(t *testing.T) {
	config := &npmConfig{
		ScopeRegistries: map[string]string{
			"@company":  "https://npm.company.com",
			"@internal": "https://internal-registry.example.com",
		},
		AuthTokens: map[string]string{
			"npm.company.com": "company_token",
		},
	}

	tests := []struct {
		packageName      string
		expectedRegistry string
	}{
		{"@company/package", "https://npm.company.com"},
		{"@internal/tool", "https://internal-registry.example.com"},
		{"@unknown/package", "https://registry.npmjs.org"},
		{"regular-package", "https://registry.npmjs.org"},
		{"@malformed", "https://registry.npmjs.org"},
	}

	for _, test := range tests {
		actualRegistry := getRegistryForPackage(test.packageName, config)
		if actualRegistry != test.expectedRegistry {
			t.Errorf("For package '%s', expected registry '%s', got '%s'",
				test.packageName, test.expectedRegistry, actualRegistry)
		}
	}
}

func TestGetAuthTokenForRegistry(t *testing.T) {
	config := &npmConfig{
		ScopeRegistries: map[string]string{},
		AuthTokens: map[string]string{
			"npm.company.com":      "company_token",
			"registry.example.com": "example_token",
			"internal.corp.com":    "internal_token",
		},
	}

	tests := []struct {
		registryURL   string
		expectedToken string
	}{
		{"https://npm.company.com", "company_token"},
		{"https://npm.company.com/", "company_token"},
		{"https://registry.example.com", "example_token"},
		{"https://internal.corp.com/npm", "internal_token"},
		{"https://unknown-registry.com", ""},
		{"http://npm.company.com", "company_token"},
	}

	for _, test := range tests {
		actualToken := getAuthTokenForRegistry(test.registryURL, config)
		if actualToken != test.expectedToken {
			t.Errorf("For registry '%s', expected token '%s', got '%s'",
				test.registryURL, test.expectedToken, actualToken)
		}
	}
}

func TestGetAuthTokenForRegistryUsesLongestPathPrefix(t *testing.T) {
	config := &npmConfig{AuthTokens: map[string]string{
		"company.jfrog.io":                                     "host-token",
		"company.jfrog.io/artifactory/api/npm":                 "npm-token",
		"company.jfrog.io/artifactory/api/npm/npm-local":       "local-token",
		"company.jfrog.io/artifactory/api/npm/npm-local-extra": "wrong-token",
	}}

	token := getAuthTokenForRegistry("https://company.jfrog.io/artifactory/api/npm/npm-local", config)
	if token != "local-token" {
		t.Fatalf("token = %q, expected longest path-scoped token", token)
	}
	token = getAuthTokenForRegistry("https://company.jfrog.io/artifactory/api/npm/other", config)
	if token != "npm-token" {
		t.Fatalf("token = %q, expected parent path token", token)
	}
}

func TestRegistryClientSendsPathScopedAuthToken(t *testing.T) {
	var receivedAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		receivedAuthorization = request.Header.Get("Authorization")
		response.Header().Set("Content-Type", "application/json")
		fmt.Fprint(response, `{"dist-tags":{"latest":"1.2.3"}}`)
	}))
	defer server.Close()

	projectDirectory := t.TempDir()
	registryURL := server.URL + "/artifactory/api/npm/npm-local"
	authKey := strings.TrimPrefix(server.URL, "http://") + "/artifactory/api/npm/npm-local"
	npmrc := "//" + authKey + "/:_authToken=path-token\n"
	if err := os.WriteFile(filepath.Join(projectDirectory, ".npmrc"), []byte(npmrc), 0o600); err != nil {
		t.Fatal(err)
	}
	client := NewRegistryClient()
	client.ConfigDirectory = projectDirectory
	latest, err := client.GetLatestVersionFromRegistry(context.Background(), "example", registryURL, shared.Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if latest != "1.2.3" || receivedAuthorization != "Bearer path-token" {
		t.Fatalf("latest = %q, authorization = %q", latest, receivedAuthorization)
	}
}

func TestRegistryClientUsesConfiguredProjectDirectory(t *testing.T) {
	projectDirectory := t.TempDir()
	emptyHome := t.TempDir()
	t.Setenv("HOME", emptyHome)

	localNpmrc := "@company:registry=https://packages.company.test/npm\n"
	if err := os.WriteFile(filepath.Join(projectDirectory, ".npmrc"), []byte(localNpmrc), 0o600); err != nil {
		t.Fatal(err)
	}

	client := NewRegistryClient()
	client.ConfigDirectory = projectDirectory
	registryURL, _, err := client.resolveRegistryURL("@company/core", "")
	if err != nil {
		t.Fatal(err)
	}
	if registryURL != "https://packages.company.test/npm" {
		t.Fatalf("registry URL = %q, expected project-local registry", registryURL)
	}
}

func TestRegistryClientCachesNPMConfiguration(t *testing.T) {
	projectDirectory := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	npmrcPath := filepath.Join(projectDirectory, ".npmrc")
	if err := os.WriteFile(npmrcPath, []byte("@company:registry=https://first.example.test/npm\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := NewRegistryClient()
	client.ConfigDirectory = projectDirectory
	first, _, err := client.resolveRegistryURL("@company/core", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(npmrcPath, []byte("@company:registry=https://second.example.test/npm\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, _, err := client.resolveRegistryURL("@company/core", "")
	if err != nil {
		t.Fatal(err)
	}
	if first != "https://first.example.test/npm" || second != first {
		t.Fatalf("registry URLs = (%q, %q), expected cached first value", first, second)
	}
}

func TestUpdateDuplicateDependenciesWithDifferentConstraints(t *testing.T) {
	packageJSONContent := `{
  "name": "test-package",
  "version": "1.0.0",
  "dependencies": {
    "react": "^18.0.0"
  },
  "devDependencies": {
    "eslint": "^8.45.0"
  },
  "peerDependencies": {
    "react": ">=16.0.0"
  }
}`

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "package.json")

	err := os.WriteFile(testFile, []byte(packageJSONContent), 0644)
	if err != nil {
		t.Fatal(err)
	}

	outdatedDependencies := []shared.OutdatedDependency{
		{
			BaseDependency: shared.BaseDependency{
				Name:            "react",
				OriginalVersion: "^18.0.0",
				Type:            shared.Dependencies,
				FilePath:        "",
				LineNumber:      5,
			},
			CurrentVersion: "18.0.0",
			LatestVersion:  "18.2.0",
		},
		{
			BaseDependency: shared.BaseDependency{
				Name:            "react",
				OriginalVersion: ">=16.0.0",
				Type:            shared.PeerDependencies,
				FilePath:        "",
				LineNumber:      11,
			},
			CurrentVersion: "16.0.0",
			LatestVersion:  "18.2.0",
		},
	}

	updater := NewUpdater()
	err = applyTestUpdate(testFile, outdatedDependencies, updater, shared.Options{IncludePeerDependencies: true})
	if err != nil {
		t.Fatal(err)
	}

	updatedContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}

	updatedStr := string(updatedContent)

	if !strings.Contains(updatedStr, `"react": "^18.2.0"`) {
		t.Errorf("Expected react in dependencies to be updated to '^18.2.0' with caret constraint preserved")
	}

	if !strings.Contains(updatedStr, `"react": ">=18.2.0"`) {
		t.Errorf("Expected react in peerDependencies to be updated to '>=18.2.0' with >= constraint preserved")
	}

	dependenciesMatch := strings.Contains(updatedStr, `"dependencies": {
    "react": "^18.2.0"
  }`)
	peerDependenciesMatch := strings.Contains(updatedStr, `"peerDependencies": {
    "react": ">=18.2.0"
  }`)

	if !dependenciesMatch {
		t.Errorf("Dependencies section not correctly updated")
	}
	if !peerDependenciesMatch {
		t.Errorf("PeerDependencies section not correctly updated")
	}
}

func TestMonorepoWorkspaceDetection(t *testing.T) {

	rootDir := t.TempDir()
	packagesDir := filepath.Join(rootDir, "packages")
	packageADir := filepath.Join(packagesDir, "package-a")
	packageBDir := filepath.Join(packagesDir, "package-b")

	if err := os.MkdirAll(packageADir, 0755); err != nil {
		t.Fatalf("Failed to create package-a directory: %v", err)
	}
	if err := os.MkdirAll(packageBDir, 0755); err != nil {
		t.Fatalf("Failed to create package-b directory: %v", err)
	}

	rootPackageJSON := `{
  "name": "root",
  "private": true,
  "workspaces": ["packages/*"],
  "dependencies": {
    "lodash": "^4.17.0"
  }
}`
	rootPath := filepath.Join(rootDir, "package.json")
	if err := os.WriteFile(rootPath, []byte(rootPackageJSON), 0644); err != nil {
		t.Fatalf("Failed to create root package.json: %v", err)
	}

	packageAJSON := `{
  "name": "package-a",
  "dependencies": {
    "react": "^17.0.0"
  }
}`
	packageAPath := filepath.Join(packageADir, "package.json")
	if err := os.WriteFile(packageAPath, []byte(packageAJSON), 0644); err != nil {
		t.Fatalf("Failed to create package-a/package.json: %v", err)
	}

	packageBJSON := `{
  "name": "package-b",
  "dependencies": {
    "axios": "^1.0.0"
  }
}`
	packageBPath := filepath.Join(packageBDir, "package.json")
	if err := os.WriteFile(packageBPath, []byte(packageBJSON), 0644); err != nil {
		t.Fatalf("Failed to create package-b/package.json: %v", err)
	}

	parser := NewParser()
	dependencies, err := parser.ParseDependencies(rootPath, shared.Options{Monorepo: true})
	if err != nil {
		t.Fatalf("Failed to parse monorepo: %v", err)
	}

	if len(dependencies) != 3 {
		t.Errorf("Expected 3 dependencies, got %d", len(dependencies))
	}

	dependencyMap := make(map[string]shared.Dependency)
	for _, dependency := range dependencies {
		dependencyMap[dependency.Name] = dependency
	}

	if lodash, ok := dependencyMap["lodash"]; ok {
		if lodash.FilePath != rootPath {
			t.Errorf("lodash FilePath = %s, want %s", lodash.FilePath, rootPath)
		}
		if lodash.Version != "4.17.0" {
			t.Errorf("lodash Version = %s, want 4.17.0", lodash.Version)
		}
	} else {
		t.Error("lodash dependency not found")
	}

	if react, ok := dependencyMap["react"]; ok {
		if react.FilePath != packageAPath {
			t.Errorf("react FilePath = %s, want %s", react.FilePath, packageAPath)
		}
		if react.Version != "17.0.0" {
			t.Errorf("react Version = %s, want 17.0.0", react.Version)
		}
	} else {
		t.Error("react dependency not found")
	}

	if axios, ok := dependencyMap["axios"]; ok {
		if axios.FilePath != packageBPath {
			t.Errorf("axios FilePath = %s, want %s", axios.FilePath, packageBPath)
		}
		if axios.Version != "1.0.0" {
			t.Errorf("axios Version = %s, want 1.0.0", axios.Version)
		}
	} else {
		t.Error("axios dependency not found")
	}
}

func TestMonorepoWithoutWorkspaces(t *testing.T) {

	tempDir := t.TempDir()
	packageJSONPath := filepath.Join(tempDir, "package.json")

	packageJSON := `{
  "name": "regular-project",
  "dependencies": {
    "lodash": "^4.17.21"
  }
}`

	if err := os.WriteFile(packageJSONPath, []byte(packageJSON), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	parser := NewParser()
	dependencies, err := parser.ParseDependencies(packageJSONPath, shared.Options{Monorepo: true})
	if err != nil {
		t.Fatalf("Failed to parse package.json: %v", err)
	}

	if len(dependencies) != 1 {
		t.Errorf("Expected 1 dependency, got %d", len(dependencies))
	}

	if dependencies[0].Name != "lodash" {
		t.Errorf("Expected lodash, got %s", dependencies[0].Name)
	}

	if dependencies[0].FilePath != packageJSONPath {
		t.Errorf("FilePath = %s, want %s", dependencies[0].FilePath, packageJSONPath)
	}
}

func TestMonorepoGlobPatterns(t *testing.T) {

	rootDir := t.TempDir()
	appsDir := filepath.Join(rootDir, "apps")
	libsDir := filepath.Join(rootDir, "libs")
	webAppDir := filepath.Join(appsDir, "web")
	utilsDir := filepath.Join(libsDir, "utils")

	if err := os.MkdirAll(webAppDir, 0755); err != nil {
		t.Fatalf("Failed to create web directory: %v", err)
	}
	if err := os.MkdirAll(utilsDir, 0755); err != nil {
		t.Fatalf("Failed to create utils directory: %v", err)
	}

	rootPackageJSON := `{
  "name": "monorepo",
  "workspaces": ["apps/*", "libs/*"],
  "dependencies": {
    "typescript": "^5.0.0"
  }
}`
	rootPath := filepath.Join(rootDir, "package.json")
	if err := os.WriteFile(rootPath, []byte(rootPackageJSON), 0644); err != nil {
		t.Fatalf("Failed to create root package.json: %v", err)
	}

	webJSON := `{
  "name": "web",
  "dependencies": {
    "react": "^18.0.0"
  }
}`
	webPath := filepath.Join(webAppDir, "package.json")
	if err := os.WriteFile(webPath, []byte(webJSON), 0644); err != nil {
		t.Fatalf("Failed to create web/package.json: %v", err)
	}

	utilsJSON := `{
  "name": "utils",
  "dependencies": {
    "lodash": "^4.17.21"
  }
}`
	utilsPath := filepath.Join(utilsDir, "package.json")
	if err := os.WriteFile(utilsPath, []byte(utilsJSON), 0644); err != nil {
		t.Fatalf("Failed to create utils/package.json: %v", err)
	}

	parser := NewParser()
	dependencies, err := parser.ParseDependencies(rootPath, shared.Options{Monorepo: true})
	if err != nil {
		t.Fatalf("Failed to parse monorepo: %v", err)
	}

	if len(dependencies) != 3 {
		t.Errorf("Expected 3 dependencies, got %d", len(dependencies))
	}

	dependencyMap := make(map[string]string)
	for _, dependency := range dependencies {
		dependencyMap[dependency.Name] = dependency.FilePath
	}

	if path, ok := dependencyMap["typescript"]; !ok || path != rootPath {
		t.Errorf("typescript not found in root or incorrect path")
	}
	if path, ok := dependencyMap["react"]; !ok || path != webPath {
		t.Errorf("react not found in web or incorrect path")
	}
	if path, ok := dependencyMap["lodash"]; !ok || path != utilsPath {
		t.Errorf("lodash not found in utils or incorrect path")
	}
}

func TestWorkspaceDependenciesSkipped(t *testing.T) {
	rootDir := t.TempDir()
	packagesDir := filepath.Join(rootDir, "packages")
	packageADir := filepath.Join(packagesDir, "package-a")
	packageBDir := filepath.Join(packagesDir, "package-b")

	if err := os.MkdirAll(packageADir, 0755); err != nil {
		t.Fatalf("Failed to create package-a directory: %v", err)
	}
	if err := os.MkdirAll(packageBDir, 0755); err != nil {
		t.Fatalf("Failed to create package-b directory: %v", err)
	}

	rootPackageJSON := `{
  "name": "monorepo-root",
  "private": true,
  "workspaces": ["packages/*"]
}`
	rootPath := filepath.Join(rootDir, "package.json")
	if err := os.WriteFile(rootPath, []byte(rootPackageJSON), 0644); err != nil {
		t.Fatalf("Failed to create root package.json: %v", err)
	}

	packageAJSON := `{
  "name": "@monorepo/package-a",
  "dependencies": {
    "lodash": "^4.17.0"
  }
}`
	packageAPath := filepath.Join(packageADir, "package.json")
	if err := os.WriteFile(packageAPath, []byte(packageAJSON), 0644); err != nil {
		t.Fatalf("Failed to create package-a/package.json: %v", err)
	}

	packageBJSON := `{
  "name": "@monorepo/package-b",
  "dependencies": {
    "@monorepo/package-a": "*",
    "axios": "^1.0.0"
  }
}`
	packageBPath := filepath.Join(packageBDir, "package.json")
	if err := os.WriteFile(packageBPath, []byte(packageBJSON), 0644); err != nil {
		t.Fatalf("Failed to create package-b/package.json: %v", err)
	}

	parser := NewParser()
	dependencies, err := parser.ParseDependencies(rootPath, shared.Options{Monorepo: true})
	if err != nil {
		t.Fatalf("Failed to parse monorepo: %v", err)
	}

	foundWorkspaceDep := false
	foundLodash := false
	foundAxios := false

	for _, dependency := range dependencies {
		if dependency.Name == "@monorepo/package-a" && dependency.Version == "*" {
			foundWorkspaceDep = true
		}
		if dependency.Name == "lodash" {
			foundLodash = true
		}
		if dependency.Name == "axios" {
			foundAxios = true
		}
	}

	if !foundWorkspaceDep {
		t.Error("Workspace dependency @monorepo/package-a with * version should be parsed")
	}
	if !foundLodash {
		t.Error("External dependency lodash should be parsed")
	}
	if !foundAxios {
		t.Error("External dependency axios should be parsed")
	}
}

func TestMonorepoWorkspaceObjectForm(t *testing.T) {
	rootDir := t.TempDir()
	packagesDir := filepath.Join(rootDir, "packages")
	packageADir := filepath.Join(packagesDir, "package-a")

	if err := os.MkdirAll(packageADir, 0755); err != nil {
		t.Fatalf("Failed to create workspace directory: %v", err)
	}

	rootPackageJSON := `{
  "name": "monorepo-root",
  "private": true,
  "workspaces": {
    "packages": ["packages/*"]
  },
  "dependencies": {
    "typescript": "^5.0.0"
  }
}`
	rootPath := filepath.Join(rootDir, "package.json")
	if err := os.WriteFile(rootPath, []byte(rootPackageJSON), 0644); err != nil {
		t.Fatalf("Failed to create root package.json: %v", err)
	}

	workspacePackageJSON := `{
  "name": "package-a",
  "dependencies": {
    "react": "^18.0.0"
  }
}`
	workspacePath := filepath.Join(packageADir, "package.json")
	if err := os.WriteFile(workspacePath, []byte(workspacePackageJSON), 0644); err != nil {
		t.Fatalf("Failed to create workspace package.json: %v", err)
	}

	parser := NewParser()
	dependencies, err := parser.ParseDependencies(rootPath, shared.Options{Monorepo: true})
	if err != nil {
		t.Fatalf("Failed to parse monorepo with object workspace form: %v", err)
	}

	if len(dependencies) != 2 {
		t.Fatalf("Expected 2 dependencies, got %d", len(dependencies))
	}

	paths := make(map[string]string)
	for _, dependency := range dependencies {
		paths[dependency.Name] = dependency.FilePath
	}

	if paths["typescript"] != rootPath {
		t.Errorf("typescript FilePath = %s, want %s", paths["typescript"], rootPath)
	}
	if paths["react"] != workspacePath {
		t.Errorf("react FilePath = %s, want %s", paths["react"], workspacePath)
	}
}

func TestMonorepoBestEffortOnInvalidWorkspacePackage(t *testing.T) {
	rootDir := t.TempDir()
	packagesDir := filepath.Join(rootDir, "packages")
	goodPackageDir := filepath.Join(packagesDir, "good")
	badPackageDir := filepath.Join(packagesDir, "bad")

	if err := os.MkdirAll(goodPackageDir, 0755); err != nil {
		t.Fatalf("Failed to create good workspace directory: %v", err)
	}
	if err := os.MkdirAll(badPackageDir, 0755); err != nil {
		t.Fatalf("Failed to create bad workspace directory: %v", err)
	}

	rootPackageJSON := `{
  "name": "monorepo-root",
  "private": true,
  "workspaces": ["packages/*"],
  "dependencies": {
    "typescript": "^5.0.0"
  }
}`
	rootPath := filepath.Join(rootDir, "package.json")
	if err := os.WriteFile(rootPath, []byte(rootPackageJSON), 0644); err != nil {
		t.Fatalf("Failed to create root package.json: %v", err)
	}

	goodPackageJSON := `{
  "name": "good",
  "dependencies": {
    "react": "^18.0.0"
  }
}`
	if err := os.WriteFile(filepath.Join(goodPackageDir, "package.json"), []byte(goodPackageJSON), 0644); err != nil {
		t.Fatalf("Failed to create good workspace package.json: %v", err)
	}

	if err := os.WriteFile(filepath.Join(badPackageDir, "package.json"), []byte(`{"dependencies": {"broken": }}`), 0644); err != nil {
		t.Fatalf("Failed to create bad workspace package.json: %v", err)
	}

	parser := NewParser()
	dependencies, err := parser.ParseDependencies(rootPath, shared.Options{Monorepo: true, Verbose: true})
	if err != nil {
		t.Fatalf("Expected best-effort parsing, got error: %v", err)
	}

	names := make(map[string]bool)
	for _, dependency := range dependencies {
		names[dependency.Name] = true
	}

	if !names["typescript"] {
		t.Error("Expected root dependency typescript to be present")
	}
	if !names["react"] {
		t.Error("Expected valid workspace dependency react to be present")
	}
}
