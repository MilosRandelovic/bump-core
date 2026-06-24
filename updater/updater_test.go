package updater

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MilosRandelovic/bump-core/shared"
)

func TestFindBothLatestVersions(t *testing.T) {
	tests := []struct {
		name                  string
		versions              []string
		constraint            string
		expectedAbsolute      string
		expectedConstraint    string
		expectConstraintError bool
		description           string
	}{
		{
			name:               "caret constraint with newer major available",
			versions:           []string{"22.15.0", "22.16.0", "22.17.0", "24.0.0", "24.1.0"},
			constraint:         "^22.16.0",
			expectedAbsolute:   "24.1.0",
			expectedConstraint: "22.17.0",
			description:        "should find 24.1.0 as absolute latest and 22.17.0 as constraint-satisfying latest",
		},
		{
			name:               "tilde constraint",
			versions:           []string{"1.2.0", "1.2.5", "1.3.0", "2.0.0"},
			constraint:         "~1.2.3",
			expectedAbsolute:   "2.0.0",
			expectedConstraint: "1.2.5",
			description:        "should find 2.0.0 as absolute latest and 1.2.5 as constraint-satisfying latest",
		},
		{
			name:                  "no compatible versions",
			versions:              []string{"1.0.0", "1.1.0", "1.2.0"},
			constraint:            "^2.0.0",
			expectedAbsolute:      "1.2.0",
			expectConstraintError: true,
			description:           "should find absolute latest but error for constraint",
		},
		{
			name:               "absolute and constraint latest are same",
			versions:           []string{"1.0.0", "1.1.0", "1.2.0"},
			constraint:         "^1.0.0",
			expectedAbsolute:   "1.2.0",
			expectedConstraint: "1.2.0",
			description:        "should find same version for both when constraint allows latest",
		},
		{
			name:               "pre-release versions should be filtered out",
			versions:           []string{"22.15.0", "22.16.0", "22.17.0", "24.0.0", "24.1.0-alpha", "24.1.0-beta", "24.2.0-rc"},
			constraint:         "^22.16.0",
			expectedAbsolute:   "24.0.0",
			expectedConstraint: "22.17.0",
			description:        "should filter out alpha/beta/rc versions from both absolute and constraint results",
		},
		{
			name:                  "only pre-release versions available should error",
			versions:              []string{"1.0.0-alpha", "1.0.0-beta", "1.1.0-rc"},
			constraint:            "^1.0.0",
			expectedAbsolute:      "",
			expectedConstraint:    "",
			expectConstraintError: true,
			description:           "should error when only pre-release versions are available",
		},
		{
			name:               "mixed stable and pre-release with constraint match",
			versions:           []string{"1.0.0", "1.1.0-alpha", "1.1.0", "1.2.0-beta", "2.0.0-alpha", "2.0.0"},
			constraint:         "^1.0.0",
			expectedAbsolute:   "2.0.0",
			expectedConstraint: "1.1.0",
			description:        "should find stable versions ignoring pre-releases",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			absolute, constraint, err := shared.FindBothLatestVersions(test.versions, test.constraint)

			if test.expectConstraintError {
				if err == nil {
					t.Errorf("%s: expected error for constraint but got none", test.description)
				}
				if test.expectedAbsolute != "" && absolute != test.expectedAbsolute {
					t.Errorf("%s: absolute latest = %s, expected %s", test.description, absolute, test.expectedAbsolute)
				}
				return
			}

			if err != nil {
				t.Errorf("%s: unexpected error: %v", test.description, err)
				return
			}

			if absolute != test.expectedAbsolute {
				t.Errorf("%s: absolute latest = %s, expected %s", test.description, absolute, test.expectedAbsolute)
			}

			if constraint != test.expectedConstraint {
				t.Errorf("%s: constraint latest = %s, expected %s", test.description, constraint, test.expectedConstraint)
			}
		})
	}
}

func TestHasSemanticPrefix(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"^1.0.0", true},
		{"~2.3.4", true},
		{">=3.0.0", true},
		{">1.0.0", true},
		{"<2.0.0", true},
		{"<=2.0.0", true},
		{">=1.0.0 <2.0.0", true},
		{">1.0.0 <=2.0.0", true},
		{">=1.2.3 <1.3.0", true},
		{"1.5.0", false},
		{"", false},
		{">=1.0.0 1.5.0", false}, // Mix of semantic and non-semantic
	}

	for _, test := range tests {
		result := shared.HasSemanticPrefix(test.input)
		if result != test.expected {
			t.Errorf("HasSemanticPrefix(%s) = %v, expected %v", test.input, result, test.expected)
		}
	}
}

// MockRegistryClient for testing
type MockRegistryClient struct {
	packageVersions map[string][]string
}

func (mockClient *MockRegistryClient) GetLatestVersionFromRegistry(packageName, registryURL string, options shared.Options, cache *shared.Cache) (string, error) {
	versions := mockClient.packageVersions[packageName]
	if len(versions) == 0 {
		return "", fmt.Errorf("package not found")
	}
	return versions[len(versions)-1], nil
}

func (mockClient *MockRegistryClient) GetBothLatestVersions(packageName, constraint, registryURL string, options shared.Options, cache *shared.Cache) (string, string, error) {
	versions := mockClient.packageVersions[packageName]
	if len(versions) == 0 {
		return "", "", fmt.Errorf("package not found")
	}
	return shared.FindBothLatestVersions(versions, constraint)
}

func (mockClient *MockRegistryClient) GetRegistryType() shared.RegistryType {
	return shared.Npm // Mock defaults to Npm type
}

func TestCheckForUpdatesIntegration(t *testing.T) {
	// Mock registry client with pre-release versions
	mockRegistry := &MockRegistryClient{
		packageVersions: map[string][]string{
			"@types/node": {"22.15.0", "22.16.0", "22.17.0", "24.0.0-alpha", "24.0.0-beta", "24.1.0"},
			"typescript":  {"5.8.0", "5.8.3", "5.9.0", "5.9.2"},
		},
	}

	// Test that pre-release versions are filtered
	absolute, constraint, err := shared.FindBothLatestVersions(
		mockRegistry.packageVersions["@types/node"],
		"^22.16.0",
	)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if absolute != "24.1.0" {
		t.Errorf("Expected absolute latest 24.1.0, got %s", absolute)
	}

	if constraint != "22.17.0" {
		t.Errorf("Expected constraint latest 22.17.0, got %s", constraint)
	}

	// Test that we would report semver skipped when absolute != constraint
	shouldSkip := absolute != constraint
	if !shouldSkip {
		t.Errorf("Expected to skip major version, but absolute == constraint")
	}
}

func TestConstraintMatchesNoVersions(t *testing.T) {
	// Test that when constraint matches no available versions,
	// it goes to semverSkipped instead of errors
	mockRegistry := &MockRegistryClient{
		packageVersions: map[string][]string{
			"core": {"1.0.0", "1.1.0", "1.7.0"}, // Available versions: all 1.x
		},
	}

	// Test the scenario directly using the shared function
	absoluteLatest, constraintLatest, err := mockRegistry.GetBothLatestVersions("core", "^0.0.1", "", shared.Options{}, nil)
	if err == nil {
		t.Fatal("Expected error for incompatible constraint, got nil")
	}

	if !errors.Is(err, shared.ErrNoVersionsSatisfyConstraint) {
		t.Errorf("Expected ErrNoVersionsSatisfyConstraint error, got: %v", err)
	}

	// Verify that even with the error, absoluteLatest is still returned
	if absoluteLatest != "1.7.0" {
		t.Errorf("Expected absolute latest '1.7.0' even with constraint error, got '%s'", absoluteLatest)
	}

	// Verify constraintLatest is empty when no versions satisfy constraint
	if constraintLatest != "" {
		t.Errorf("Expected empty constraint latest when no versions satisfy, got '%s'", constraintLatest)
	}
}

func TestWorkspaceDependenciesSkipped(t *testing.T) {
	// Test that workspace dependencies with * version are skipped
	dependencies := []shared.Dependency{
		{
			BaseDependency: shared.BaseDependency{
				Name:            "lodash",
				OriginalVersion: "^4.17.0",
				Type:            shared.Dependencies,
				FilePath:        "/test/package.json",
				LineNumber:      1,
			},
			Version: "4.17.21",
		},
		{
			BaseDependency: shared.BaseDependency{
				Name:            "@monorepo/package-a",
				OriginalVersion: "*",
				Type:            shared.Dependencies,
				FilePath:        "/test/package.json",
				LineNumber:      2,
			},
			Version: "*",
		},
		{
			BaseDependency: shared.BaseDependency{
				Name:            "axios",
				OriginalVersion: "^1.0.0",
				Type:            shared.Dependencies,
				FilePath:        "/test/package.json",
				LineNumber:      3,
			},
			Version: "1.6.0",
		},
	}

	mockRegistry := &MockRegistryClient{
		packageVersions: map[string][]string{
			"lodash": {"4.17.21", "4.18.0"},
			"axios":  {"1.6.0", "1.7.0"},
		},
	}

	result, err := checkOutdatedWithMockRegistry(dependencies, mockRegistry, shared.Options{})
	if err != nil {
		t.Fatalf("CheckOutdated failed: %v", err)
	}

	// Verify workspace dependency was skipped (not in outdated or errors)
	for _, dependency := range result.Outdated {
		if dependency.Name == "@monorepo/package-a" {
			t.Error("Workspace dependency @monorepo/package-a should be skipped, but found in outdated list")
		}
	}

	for _, errDep := range result.Errors {
		if errDep.Name == "@monorepo/package-a" {
			t.Error("Workspace dependency @monorepo/package-a should be skipped, but found in errors list")
		}
	}

	// Verify external dependencies were processed
	foundLodash := false
	foundAxios := false
	for _, dependency := range result.Outdated {
		if dependency.Name == "lodash" {
			foundLodash = true
		}
		if dependency.Name == "axios" {
			foundAxios = true
		}
	}

	if !foundLodash {
		t.Error("External dependency lodash should be checked for updates")
	}
	if !foundAxios {
		t.Error("External dependency axios should be checked for updates")
	}
}

func checkOutdatedWithMockRegistry(dependencies []shared.Dependency, mockRegistry *MockRegistryClient, options shared.Options) (*shared.CheckResult, error) {
	var outdated []shared.OutdatedDependency
	var errors []shared.DependencyError

	for _, dependency := range dependencies {
		// Skip complex dependencies
		if strings.HasPrefix(dependency.Version, "git:") || strings.HasPrefix(dependency.Version, "path:") || dependency.Version == "complex" || dependency.Version == "*" {
			continue
		}

		latestVersion, err := mockRegistry.GetLatestVersionFromRegistry(dependency.Name, "", options, nil)
		if err != nil {
			errors = append(errors, shared.DependencyError{
				Name:  dependency.Name,
				Error: err.Error(),
			})
			continue
		}

		if dependency.Version != latestVersion {
			outdated = append(outdated, shared.OutdatedDependency{
				BaseDependency: shared.BaseDependency{
					Name:            dependency.Name,
					OriginalVersion: dependency.OriginalVersion,
					Type:            dependency.Type,
					FilePath:        dependency.FilePath,
					LineNumber:      dependency.LineNumber,
				},
				CurrentVersion: dependency.Version,
				LatestVersion:  latestVersion,
			})
		}
	}

	return &shared.CheckResult{
		Outdated: outdated,
		Errors:   errors,
	}, nil
}
