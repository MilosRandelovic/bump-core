package updater

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

type concurrentLogRegistry struct {
	active    atomic.Int32
	maxActive atomic.Int32
	delays    map[string]time.Duration
}

func TestValidateOptionsRejectsUnsupportedRegistry(t *testing.T) {
	err := ValidateOptions(shared.RegistryType(99), shared.Options{})
	if !errors.Is(err, shared.ErrUnsupportedRegistryType) {
		t.Fatalf("expected unsupported-registry error, got %v", err)
	}
}

type cancellationRegistry struct {
	started chan struct{}
	count   atomic.Int32
}

func (registry *cancellationRegistry) GetLatestVersionFromRegistry(ctx context.Context, packageName string, registryURL string, options shared.Options, cache *shared.Cache) (string, error) {
	registry.count.Add(1)
	registry.started <- struct{}{}
	<-ctx.Done()
	return "", ctx.Err()
}

func (registry *cancellationRegistry) GetBothLatestVersions(ctx context.Context, packageName string, constraint string, registryURL string, options shared.Options, cache *shared.Cache) (absoluteLatest string, constraintLatest string, err error) {
	latest, err := registry.GetLatestVersionFromRegistry(ctx, packageName, registryURL, options, cache)
	return latest, latest, err
}

func (registry *concurrentLogRegistry) GetLatestVersionFromRegistry(ctx context.Context, packageName string, registryURL string, options shared.Options, cache *shared.Cache) (string, error) {
	active := registry.active.Add(1)
	defer registry.active.Add(-1)
	for {
		maxActive := registry.maxActive.Load()
		if active <= maxActive || registry.maxActive.CompareAndSwap(maxActive, active) {
			break
		}
	}
	select {
	case <-time.After(registry.delays[packageName]):
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if log := shared.LogFromContext(ctx); log != nil {
		log("registry detail: %s\n", packageName)
	}
	return "2.0.0", nil
}

func (registry *concurrentLogRegistry) GetBothLatestVersions(ctx context.Context, packageName string, constraint string, registryURL string, options shared.Options, cache *shared.Cache) (absoluteLatest string, constraintLatest string, err error) {
	latest, err := registry.GetLatestVersionFromRegistry(ctx, packageName, registryURL, options, cache)
	return latest, latest, err
}

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
			name:               "compound range constraint",
			versions:           []string{"1.21.0", "1.30.0", "1.99.0", "2.0.0"},
			constraint:         ">=1.21.0 <2.0.0",
			expectedAbsolute:   "2.0.0",
			expectedConstraint: "1.99.0",
			description:        "should evaluate the full range while using its first version as the current reference",
		},
		{
			name:               "or constraint",
			versions:           []string{"1.0.0", "1.9.0", "2.0.0", "2.5.0", "3.0.0"},
			constraint:         "^1.0.0 || ^2.0.0",
			expectedAbsolute:   "3.0.0",
			expectedConstraint: "2.5.0",
			description:        "should support OR constraints without confusing the constraint for a version",
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
		{">=1.0.0 1.5.0", false},
	}

	for _, test := range tests {
		result := shared.HasSemanticPrefix(test.input)
		if result != test.expected {
			t.Errorf("HasSemanticPrefix(%s) = %v, expected %v", test.input, result, test.expected)
		}
	}
}

type mockRegistryClient struct {
	packageVersions map[string][]string
}

type fixedRegistryClient struct {
	absoluteLatest   string
	constraintLatest string
	err              error
}

func (client *fixedRegistryClient) GetLatestVersionFromRegistry(ctx context.Context, packageName string, registryURL string, options shared.Options, cache *shared.Cache) (string, error) {
	return client.absoluteLatest, client.err
}

func (client *fixedRegistryClient) GetBothLatestVersions(ctx context.Context, packageName string, constraint string, registryURL string, options shared.Options, cache *shared.Cache) (absoluteLatest string, constraintLatest string, err error) {
	return client.absoluteLatest, client.constraintLatest, client.err
}

func TestMinimumAgeNeverSuggestsDowngrade(t *testing.T) {
	dependency := shared.Dependency{
		BaseDependency: shared.BaseDependency{Name: "example", OriginalVersion: "^2.0.0", Type: shared.Dependencies},
		Version:        "2.0.0",
	}
	registry := &fixedRegistryClient{absoluteLatest: "1.5.0", constraintLatest: "1.5.0"}
	var result checkResult
	checkSingleDependency(context.Background(), dependency, registry, shared.Options{EnforceMinimumReleaseAge: true}, nil, &result, nil)
	if len(result.outdated) != 0 || len(result.semverSkipped) != 0 || len(result.errors) != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestMinimumAgeWithNoEligibleVersionsIsNotAnError(t *testing.T) {
	dependency := shared.Dependency{
		BaseDependency: shared.BaseDependency{Name: "example", OriginalVersion: "1.0.0", Type: shared.Dependencies},
		Version:        "1.0.0",
	}
	registry := &fixedRegistryClient{err: shared.ErrNoVersionsMeetMinimumReleaseAge}
	var result checkResult
	checkSingleDependency(context.Background(), dependency, registry, shared.Options{EnforceMinimumReleaseAge: true}, nil, &result, nil)
	if len(result.outdated) != 0 || len(result.semverSkipped) != 0 || len(result.errors) != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func (mockClient *mockRegistryClient) GetLatestVersionFromRegistry(ctx context.Context, packageName string, registryURL string, options shared.Options, cache *shared.Cache) (string, error) {
	versions := mockClient.packageVersions[packageName]
	if len(versions) == 0 {
		return "", fmt.Errorf("package not found")
	}
	return versions[len(versions)-1], nil
}

func (mockClient *mockRegistryClient) GetBothLatestVersions(ctx context.Context, packageName string, constraint string, registryURL string, options shared.Options, cache *shared.Cache) (absoluteLatest string, constraintLatest string, err error) {
	versions := mockClient.packageVersions[packageName]
	if len(versions) == 0 {
		return "", "", fmt.Errorf("package not found")
	}
	return shared.FindBothLatestVersions(versions, constraint)
}

func TestCheckForUpdatesIntegration(t *testing.T) {

	mockRegistry := &mockRegistryClient{
		packageVersions: map[string][]string{
			"@types/node": {"22.15.0", "22.16.0", "22.17.0", "24.0.0-alpha", "24.0.0-beta", "24.1.0"},
			"typescript":  {"5.8.0", "5.8.3", "5.9.0", "5.9.2"},
		},
	}

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

	shouldSkip := absolute != constraint
	if !shouldSkip {
		t.Errorf("Expected to skip major version, but absolute == constraint")
	}
}

func TestConstraintMatchesNoVersions(t *testing.T) {

	mockRegistry := &mockRegistryClient{
		packageVersions: map[string][]string{
			"core": {"1.0.0", "1.1.0", "1.7.0"},
		},
	}

	absoluteLatest, constraintLatest, err := mockRegistry.GetBothLatestVersions(context.Background(), "core", "^0.0.1", "", shared.Options{}, nil)
	if err == nil {
		t.Fatal("Expected error for incompatible constraint, got nil")
	}

	if !errors.Is(err, shared.ErrNoVersionsSatisfyConstraint) {
		t.Errorf("Expected ErrNoVersionsSatisfyConstraint error, got: %v", err)
	}

	if absoluteLatest != "1.7.0" {
		t.Errorf("Expected absolute latest '1.7.0' even with constraint error, got '%s'", absoluteLatest)
	}

	if constraintLatest != "" {
		t.Errorf("Expected empty constraint latest when no versions satisfy, got '%s'", constraintLatest)
	}
}

func TestWorkspaceDependenciesSkipped(t *testing.T) {

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
				Name:            "@monorepo/package-b",
				OriginalVersion: "workspace:^",
				Type:            shared.Dependencies,
				FilePath:        "/test/package.json",
				LineNumber:      3,
			},
			Version: "workspace:^",
		},
		{
			BaseDependency: shared.BaseDependency{
				Name:            "axios",
				OriginalVersion: "^1.0.0",
				Type:            shared.Dependencies,
				FilePath:        "/test/package.json",
				LineNumber:      4,
			},
			Version: "1.6.0",
		},
	}

	mockRegistry := &mockRegistryClient{
		packageVersions: map[string][]string{
			"lodash": {"4.17.21", "4.18.0"},
			"axios":  {"1.6.0", "1.7.0"},
		},
	}

	var progress [][2]int
	result, err := checkOutdatedWithRegistryClient(
		context.Background(),
		dependencies,
		mockRegistry,
		shared.Options{},
		"/test",
		func(update shared.Progress) { progress = append(progress, [2]int{update.Current, update.Total}) },
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("CheckOutdated failed: %v", err)
	}

	for _, dependency := range result.Outdated {
		if dependency.Name == "@monorepo/package-a" {
			t.Error("Workspace dependency @monorepo/package-a should be skipped, but found in outdated list")
		}
	}

	for _, dependencyError := range result.Errors {
		if dependencyError.Name == "@monorepo/package-a" {
			t.Error("Workspace dependency @monorepo/package-a should be skipped, but found in errors list")
		}
	}

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

	expectedProgress := [][2]int{{1, 4}, {2, 4}, {3, 4}, {4, 4}}
	if !reflect.DeepEqual(progress, expectedProgress) {
		t.Fatalf("progress = %#v, expected %#v", progress, expectedProgress)
	}
}

func TestProductionCheckClassifiesConstraintMismatch(t *testing.T) {
	dependency := shared.Dependency{
		BaseDependency: shared.BaseDependency{
			Name:            "core",
			OriginalVersion: "^0.0.1",
			Type:            shared.Dependencies,
			FilePath:        "/test/package.json",
			LineNumber:      3,
		},
		Version: "0.0.1",
	}
	registry := &mockRegistryClient{packageVersions: map[string][]string{
		"core": {"1.0.0", "1.7.0"},
	}}

	result, err := checkOutdatedWithRegistryClient(
		context.Background(),
		[]shared.Dependency{dependency},
		registry,
		shared.Options{Semver: true},
		"/test",
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) != 0 || len(result.Outdated) != 0 || len(result.SemverSkipped) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.SemverSkipped[0].Reason != shared.IncompatibleWithConstraint || result.SemverSkipped[0].LatestVersion != "1.7.0" {
		t.Fatalf("unexpected skipped result: %#v", result.SemverSkipped[0])
	}
}

func TestCheckOutdatedEndToEndWithNPMRegistry(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		fmt.Fprint(response, `{"dist-tags":{"latest":"1.2.0"},"versions":{"1.0.0":{"version":"1.0.0"},"1.2.0":{"version":"1.2.0"}}}`)
	}))
	defer registry.Close()

	filePath := filepath.Join(t.TempDir(), "package.json")
	dependency := shared.Dependency{
		BaseDependency: shared.BaseDependency{
			Name:            "example",
			OriginalVersion: "1.0.0",
			Type:            shared.Dependencies,
			FilePath:        filePath,
			HostedURL:       registry.URL,
			LineNumber:      3,
		},
		Version: "1.0.0",
	}
	var progress [][2]int
	var logs strings.Builder
	result, err := CheckOutdated(
		context.Background(),
		[]shared.Dependency{dependency},
		shared.NPM,
		shared.Options{NoCache: true},
		"",
		func(update shared.Progress) { progress = append(progress, [2]int{update.Current, update.Total}) },
		func(format string, args ...any) { fmt.Fprintf(&logs, format, args...) },
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Outdated) != 1 || result.Outdated[0].LatestVersion != "1.2.0" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !reflect.DeepEqual(progress, [][2]int{{1, 1}}) {
		t.Fatalf("unexpected progress: %#v", progress)
	}
	if !strings.Contains(logs.String(), "Checking npm package: example") {
		t.Fatalf("registry logging was not wired: %q", logs.String())
	}
}

func TestUpdateDependenciesValidatesEveryFileBeforeWriting(t *testing.T) {
	directory := t.TempDir()
	rootPath := filepath.Join(directory, "package.json")
	workspaceDirectory := filepath.Join(directory, "packages", "app")
	if err := os.MkdirAll(workspaceDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(workspaceDirectory, "package.json")

	rootContent := "{\n  \"dependencies\": {\n    \"root-package\": \"^1.0.0\"\n  }\n}\n"
	workspaceContent := "{\n  \"dependencies\": {\n    \"workspace-package\": \"^1.1.0\"\n  }\n}\n"
	if err := os.WriteFile(rootPath, []byte(rootContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspacePath, []byte(workspaceContent), 0o644); err != nil {
		t.Fatal(err)
	}

	outdated := []shared.OutdatedDependency{
		{
			BaseDependency: shared.BaseDependency{Name: "root-package", OriginalVersion: "^1.0.0", Type: shared.Dependencies, FilePath: rootPath, LineNumber: 3},
			CurrentVersion: "1.0.0",
			LatestVersion:  "1.2.0",
		},
		{
			BaseDependency: shared.BaseDependency{Name: "workspace-package", OriginalVersion: "^1.0.0", Type: shared.Dependencies, FilePath: workspacePath, LineNumber: 3},
			CurrentVersion: "1.0.0",
			LatestVersion:  "1.2.0",
		},
	}

	err := UpdateDependencies(context.Background(), rootPath, outdated, shared.NPM, shared.Options{}, directory, nil)
	if err == nil || !strings.Contains(err.Error(), "changed on line") {
		t.Fatalf("expected stale workspace error, got %v", err)
	}
	rootAfter, readErr := os.ReadFile(rootPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(rootAfter) != rootContent {
		t.Fatal("root file was modified before every target passed validation")
	}
}

func TestUpdateDependenciesRejectsUnknownDependencyTypeBeforeWriting(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "package.json")
	original := []byte("{\n  \"dependencies\": {\n    \"example\": \"1.0.0\"\n  }\n}\n")
	if err := os.WriteFile(filePath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	err := UpdateDependencies(context.Background(), filePath, []shared.OutdatedDependency{{
		BaseDependency: shared.BaseDependency{
			Name:            "example",
			OriginalVersion: "1.0.0",
			Type:            shared.DependencyType(99),
			FilePath:        filePath,
			LineNumber:      3,
		},
		CurrentVersion: "1.0.0",
		LatestVersion:  "1.1.0",
	}}, shared.NPM, shared.Options{}, filepath.Dir(filePath), func(format string, args ...any) {})
	if err == nil || !strings.Contains(err.Error(), "unsupported dependency type") {
		t.Fatalf("expected dependency-type error, got %v", err)
	}

	actual, readErr := os.ReadFile(filePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(actual, original) {
		t.Fatalf("file changed despite validation failure: %s", actual)
	}
}

func TestUpdateDependenciesHonoursCancellationBeforeWriting(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "package.json")
	content := "{\n  \"dependencies\": {\n    \"example\": \"^1.0.0\"\n  }\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := UpdateDependencies(ctx, filePath, []shared.OutdatedDependency{{
		BaseDependency: shared.BaseDependency{Name: "example", OriginalVersion: "^1.0.0", Type: shared.Dependencies, FilePath: filePath, LineNumber: 3},
		CurrentVersion: "1.0.0",
		LatestVersion:  "1.1.0",
	}}, shared.NPM, shared.Options{}, filepath.Dir(filePath), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, expected cancellation", err)
	}
	updated, readErr := os.ReadFile(filePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(updated) != content {
		t.Fatalf("cancelled update changed file: %s", updated)
	}
}

func TestVerboseChecksStayConcurrentAndFlushLogsInDependencyOrder(t *testing.T) {
	dependencies := make([]shared.Dependency, 6)
	delays := make(map[string]time.Duration, len(dependencies))
	for index := range dependencies {
		name := fmt.Sprintf("package-%d", index)
		dependencies[index] = shared.Dependency{
			BaseDependency: shared.BaseDependency{Name: name, OriginalVersion: "1.0.0", Type: shared.Dependencies, FilePath: "/project/package.json", LineNumber: index + 1},
			Version:        "1.0.0",
		}
		delays[name] = time.Duration(len(dependencies)-index) * 5 * time.Millisecond
	}
	registry := &concurrentLogRegistry{delays: delays}
	var logs strings.Builder
	result, err := checkOutdatedWithRegistryClient(
		context.Background(), dependencies, registry, shared.Options{}, "/project", nil,
		func(format string, args ...any) { fmt.Fprintf(&logs, format, args...) }, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Outdated) != len(dependencies) {
		t.Fatalf("outdated = %#v", result.Outdated)
	}
	if registry.maxActive.Load() < 2 {
		t.Fatalf("maximum concurrent checks = %d", registry.maxActive.Load())
	}
	previousPosition := -1
	for index := range dependencies {
		position := strings.Index(logs.String(), "registry detail: "+dependencies[index].Name)
		if position <= previousPosition {
			t.Fatalf("logs are not dependency-ordered: %q", logs.String())
		}
		previousPosition = position
	}
}

func TestCheckOutdatedPreservesInputOrderAcrossFiles(t *testing.T) {
	dependencies := []shared.Dependency{
		{BaseDependency: shared.BaseDependency{Name: "first", OriginalVersion: "1.0.0", Type: shared.Dependencies, FilePath: "/project/packages/app/package.json"}, Version: "1.0.0"},
		{BaseDependency: shared.BaseDependency{Name: "second", OriginalVersion: "1.0.0", Type: shared.Dependencies, FilePath: "/project/package.json"}, Version: "1.0.0"},
		{BaseDependency: shared.BaseDependency{Name: "third", OriginalVersion: "1.0.0", Type: shared.Dependencies, FilePath: "/project/packages/app/package.json"}, Version: "1.0.0"},
	}
	registry := &concurrentLogRegistry{delays: map[string]time.Duration{
		"first": 3 * time.Millisecond, "second": 2 * time.Millisecond, "third": time.Millisecond,
	}}
	var logs strings.Builder
	var progressUpdates []shared.Progress
	result, err := checkOutdatedWithRegistryClient(
		context.Background(), dependencies, registry, shared.Options{}, "/project",
		func(progress shared.Progress) { progressUpdates = append(progressUpdates, progress) },
		func(format string, args ...any) { fmt.Fprintf(&logs, format, args...) }, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	actualNames := make([]string, 0, len(result.Outdated))
	for _, dependency := range result.Outdated {
		actualNames = append(actualNames, dependency.Name)
	}
	if !reflect.DeepEqual(actualNames, []string{"first", "second", "third"}) {
		t.Fatalf("outdated order = %#v", actualNames)
	}
	previousPosition := -1
	for _, dependency := range dependencies {
		position := strings.Index(logs.String(), "registry detail: "+dependency.Name)
		if position <= previousPosition {
			t.Fatalf("logs are not input-ordered: %q", logs.String())
		}
		previousPosition = position
	}
	if len(progressUpdates) != len(dependencies) {
		t.Fatalf("progress updates = %#v", progressUpdates)
	}
	fileProgress := make(map[string][]int)
	for _, progress := range progressUpdates {
		fileProgress[progress.FilePath] = append(fileProgress[progress.FilePath], progress.FileCurrent)
		if progress.FileTotal != len(groupedDependenciesForFile(dependencies, progress.FilePath)) {
			t.Fatalf("file total for %s = %d", progress.FilePath, progress.FileTotal)
		}
	}
	if !reflect.DeepEqual(fileProgress["/project/package.json"], []int{1}) || !reflect.DeepEqual(fileProgress["/project/packages/app/package.json"], []int{1, 2}) {
		t.Fatalf("per-file progress = %#v", fileProgress)
	}
}

func groupedDependenciesForFile(dependencies []shared.Dependency, filePath string) []shared.Dependency {
	var grouped []shared.Dependency
	for _, dependency := range dependencies {
		if dependency.FilePath == filePath {
			grouped = append(grouped, dependency)
		}
	}
	return grouped
}

func TestCheckOutdatedStopsStartingWorkAfterCancellation(t *testing.T) {
	dependencies := make([]shared.Dependency, 20)
	for index := range dependencies {
		dependencies[index] = shared.Dependency{
			BaseDependency: shared.BaseDependency{Name: fmt.Sprintf("package-%d", index), OriginalVersion: "1.0.0", Type: shared.Dependencies, FilePath: "/project/package.json"},
			Version:        "1.0.0",
		}
	}
	registry := &cancellationRegistry{started: make(chan struct{}, len(dependencies))}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := checkOutdatedWithRegistryClient(ctx, dependencies, registry, shared.Options{}, "/project", nil, nil, nil)
		result <- err
	}()

	<-registry.started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, expected cancellation", err)
	}
	if started := registry.count.Load(); started > maxConcurrentRegistryChecks {
		t.Fatalf("started %d checks after cancellation; maximum initial workers is %d", started, maxConcurrentRegistryChecks)
	}
}

func TestConcurrentUpdatesToDifferentLinesDoNotLoseChanges(t *testing.T) {
	for attempt := 0; attempt < 25; attempt++ {
		filePath := filepath.Join(t.TempDir(), "package.json")
		content := "{\n  \"dependencies\": {\n    \"first\": \"^1.0.0\",\n    \"second\": \"^2.0.0\"\n  }\n}\n"
		if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		updates := []shared.OutdatedDependency{
			{BaseDependency: shared.BaseDependency{Name: "first", OriginalVersion: "^1.0.0", Type: shared.Dependencies, FilePath: filePath, LineNumber: 3}, CurrentVersion: "1.0.0", LatestVersion: "1.1.0"},
			{BaseDependency: shared.BaseDependency{Name: "second", OriginalVersion: "^2.0.0", Type: shared.Dependencies, FilePath: filePath, LineNumber: 4}, CurrentVersion: "2.0.0", LatestVersion: "2.1.0"},
		}

		start := make(chan struct{})
		errors := make(chan error, len(updates))
		var workers sync.WaitGroup
		for _, update := range updates {
			update := update
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				errors <- UpdateDependencies(context.Background(), filePath, []shared.OutdatedDependency{update}, shared.NPM, shared.Options{}, filepath.Dir(filePath), nil)
			}()
		}
		close(start)
		workers.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatalf("attempt %d: update error: %v", attempt, err)
			}
		}
		updated, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(updated), `"first": "^1.1.0"`) || !strings.Contains(string(updated), `"second": "^2.1.0"`) {
			t.Fatalf("attempt %d lost an update: %s", attempt, updated)
		}
	}
}

func TestUpdateMinifiedDuplicateDependencyAcrossSections(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "package.json")
	content := `{"dependencies":{"react":"^18.0.0"},"devDependencies":{"react":">=16.0.0"}}`
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	outdated := []shared.OutdatedDependency{
		{BaseDependency: shared.BaseDependency{Name: "react", OriginalVersion: "^18.0.0", Type: shared.Dependencies, FilePath: filePath, LineNumber: 1}, CurrentVersion: "18.0.0", LatestVersion: "18.2.0"},
		{BaseDependency: shared.BaseDependency{Name: "react", OriginalVersion: ">=16.0.0", Type: shared.DevDependencies, FilePath: filePath, LineNumber: 1}, CurrentVersion: "16.0.0", LatestVersion: "18.2.0"},
	}
	if err := UpdateDependencies(context.Background(), filePath, outdated, shared.NPM, shared.Options{}, filepath.Dir(filePath), nil); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"dependencies":{"react":"^18.2.0"},"devDependencies":{"react":">=18.2.0"}}`
	if string(updated) != expected {
		t.Fatalf("updated manifest = %s, expected %s", updated, expected)
	}
}
