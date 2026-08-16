package updater

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Masterminds/semver/v3"
	"github.com/MilosRandelovic/bump-core/v2/npm"
	"github.com/MilosRandelovic/bump-core/v2/pub"
	"github.com/MilosRandelovic/bump-core/v2/shared"
)

// checkResult accumulates results from checking individual dependencies
type checkResult struct {
	outdated      []shared.OutdatedDependency
	errors        []shared.DependencyError
	semverSkipped []shared.SemverSkipped
}

const maxConcurrentRegistryChecks = 6

type dependencyCheckOutput struct {
	index  int
	result checkResult
	logs   []string
}

// CheckOutdated checks which dependencies have newer versions available
func CheckOutdated(ctx context.Context, dependencies []shared.Dependency, registryType shared.RegistryType, options shared.Options, workingDirectory string, progressCallback func(int, int), log shared.LogFunc) (*shared.CheckResult, error) {
	// Validate options against the registry's rules before doing any work
	if err := ValidateOptions(registryType, options); err != nil {
		return nil, err
	}

	workingDirectory = resolveWorkingDirectory(dependencies, workingDirectory)

	// Initialize cache if not disabled
	var cache *shared.Cache
	if !options.NoCache {
		var cacheErr error
		cache, cacheErr = shared.NewCacheWithError()
		if cacheErr != nil && log != nil {
			log("Warning: Could not load cache: %v\n", cacheErr)
		}
	}

	// Get the appropriate registry client after resolving the project directory,
	// so ecosystem configuration is read relative to the checked project.
	registryClient, err := getRegistryClient(registryType, workingDirectory, log)
	if err != nil {
		return nil, err
	}
	return checkOutdatedWithRegistryClient(ctx, dependencies, registryClient, options, workingDirectory, progressCallback, log, cache)
}

func resolveWorkingDirectory(dependencies []shared.Dependency, workingDirectory string) string {
	if workingDirectory != "" {
		return workingDirectory
	}

	filePaths := make([]string, 0, len(dependencies))
	seen := make(map[string]struct{}, len(dependencies))
	for _, dependency := range dependencies {
		if dependency.FilePath == "" {
			continue
		}
		if _, exists := seen[dependency.FilePath]; exists {
			continue
		}
		seen[dependency.FilePath] = struct{}{}
		filePaths = append(filePaths, dependency.FilePath)
	}
	shared.SortFilesByDepth(filePaths)
	if len(filePaths) > 0 {
		return filepath.Dir(filePaths[0])
	}

	workingDirectory, _ = os.Getwd()
	return workingDirectory
}

func checkOutdatedWithRegistryClient(ctx context.Context, dependencies []shared.Dependency, registryClient shared.RegistryClient, options shared.Options, workingDirectory string, progressCallback func(int, int), log shared.LogFunc, cache *shared.Cache) (*shared.CheckResult, error) {
	grouped := make(map[string][]shared.Dependency)
	for _, dependency := range dependencies {
		grouped[dependency.FilePath] = append(grouped[dependency.FilePath], dependency)
	}

	filePaths := make([]string, 0, len(grouped))
	for file := range grouped {
		filePaths = append(filePaths, file)
	}
	shared.SortFilesByDepth(filePaths)

	var result checkResult
	processedDependencies := 0
	totalDependencies := len(dependencies)

	for _, file := range filePaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fileDependencies := grouped[file]
		displayPath := file
		if relativePath, err := filepath.Rel(workingDirectory, file); err == nil {
			displayPath = relativePath
		}
		dependencyWord := "dependencies"
		if len(fileDependencies) == 1 {
			dependencyWord = "dependency"
		}

		if log != nil {
			log("Checking %s (%d %s)\n", displayPath, len(fileDependencies), dependencyWord)
		}

		outputs := checkFileDependencies(ctx, fileDependencies, registryClient, options, cache, func(output dependencyCheckOutput) {
			processedDependencies++
			if progressCallback != nil {
				progressCallback(processedDependencies, totalDependencies)
			}
		})
		for _, output := range outputs {
			if log != nil {
				for _, message := range output.logs {
					log("%s", message)
				}
			}
			result.outdated = append(result.outdated, output.result.outdated...)
			result.errors = append(result.errors, output.result.errors...)
			result.semverSkipped = append(result.semverSkipped, output.result.semverSkipped...)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	// Save cache if it was used
	if cache != nil {
		cache.CleanExpiredEntries()
		if err := cache.SaveEntries(); err != nil {
			if log != nil {
				log("Warning: Could not save cache: %v\n", err)
			}
		}
	}

	return &shared.CheckResult{
		Outdated:      result.outdated,
		Errors:        result.errors,
		SemverSkipped: result.semverSkipped,
	}, nil
}

func checkFileDependencies(ctx context.Context, dependencies []shared.Dependency, registryClient shared.RegistryClient, options shared.Options, cache *shared.Cache, onComplete func(dependencyCheckOutput)) []dependencyCheckOutput {
	if len(dependencies) == 0 {
		return nil
	}

	workerCount := min(len(dependencies), maxConcurrentRegistryChecks)
	jobs := make(chan int)
	completed := make(chan dependencyCheckOutput, len(dependencies))
	var workers sync.WaitGroup

	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				var result checkResult
				var logs []string
				captureLog := func(format string, args ...any) {
					logs = append(logs, fmt.Sprintf(format, args...))
				}
				dependencyContext := shared.ContextWithLog(ctx, captureLog)
				checkSingleDependency(dependencyContext, dependencies[index], registryClient, options, cache, &result, captureLog)
				completed <- dependencyCheckOutput{index: index, result: result, logs: logs}
			}
		}()
	}

	go func() {
		for index := range dependencies {
			jobs <- index
		}
		close(jobs)
		workers.Wait()
		close(completed)
	}()

	outputs := make([]dependencyCheckOutput, len(dependencies))
	for output := range completed {
		outputs[output.index] = output
		if onComplete != nil {
			onComplete(output)
		}
	}
	return outputs
}

// checkSingleDependency checks a single dependency for updates and appends results
func checkSingleDependency(ctx context.Context, dependency shared.Dependency, registryClient shared.RegistryClient, options shared.Options, cache *shared.Cache, result *checkResult, log shared.LogFunc) {
	// Skip complex dependencies (git, path, workspace, etc.)
	if isNonRegistryDependency(dependency) {
		if log != nil {
			log("Skipping complex dependency: %s (%s)\n", dependency.Name, dependency.Version)
		}
		return
	}

	// If semver flag is enabled and it's a hardcoded version (no prefix), skip it
	if options.Semver && shared.GetVersionPrefix(dependency.OriginalVersion) == "" {
		if log != nil {
			log("Skipping hardcoded version: %s (%s)\n", dependency.Name, dependency.OriginalVersion)
		}
		result.semverSkipped = append(result.semverSkipped, shared.SemverSkipped{
			OutdatedDependency: shared.OutdatedDependency{
				BaseDependency: dependency.BaseDependency,
				CurrentVersion: dependency.Version,
				LatestVersion:  "",
			},
			Reason: shared.HardcodedVersion,
		})
		return
	}

	absoluteLatest, constraintLatest, err := fetchLatestVersions(ctx, dependency, registryClient, options, cache)
	if err != nil {
		// If constraint error, use the absolute latest already returned for semver skipped
		if errors.Is(err, shared.ErrNoVersionsSatisfyConstraint) && absoluteLatest != "" {
			result.semverSkipped = append(result.semverSkipped, shared.SemverSkipped{
				OutdatedDependency: shared.OutdatedDependency{
					BaseDependency: dependency.BaseDependency,
					CurrentVersion: dependency.Version,
					LatestVersion:  absoluteLatest,
				},
				Reason: shared.IncompatibleWithConstraint,
			})
			return
		}
		// If constraint error for hardcoded pre-release, treat as up-to-date
		if errors.Is(err, shared.ErrNoVersionsSatisfyConstraint) {
			if log != nil {
				log("No newer versions found for pre-release: %s (%s)\n", dependency.Name, dependency.OriginalVersion)
			}
			return
		}
		if log != nil {
			log("Error checking %s: %v\n", dependency.Name, err)
		}
		result.errors = append(result.errors, shared.DependencyError{
			Name:  dependency.Name,
			Error: err.Error(),
		})
		return
	}

	currentVersion := dependency.Version

	// Check if there's an update available
	if currentVersion != constraintLatest && constraintLatest != "" {
		result.outdated = append(result.outdated, shared.OutdatedDependency{
			BaseDependency: dependency.BaseDependency,
			CurrentVersion: currentVersion,
			LatestVersion:  constraintLatest,
		})
	}

	// Add to semverSkipped if the absolute latest differs from the constraint-compatible latest
	if absoluteLatest != constraintLatest && absoluteLatest != "" {
		result.semverSkipped = append(result.semverSkipped, shared.SemverSkipped{
			OutdatedDependency: shared.OutdatedDependency{
				BaseDependency: dependency.BaseDependency,
				CurrentVersion: currentVersion,
				LatestVersion:  absoluteLatest,
			},
			Reason: shared.IncompatibleWithConstraint,
		})
	}
}

func isNonRegistryDependency(dependency shared.Dependency) bool {
	if dependency.Version == "complex" || dependency.Version == "*" {
		return true
	}
	version := strings.ToLower(strings.TrimSpace(dependency.OriginalVersion))
	for _, prefix := range []string{"git:", "git+", "github:", "http:", "https:", "file:", "link:", "path:", "workspace:", "npm:"} {
		if strings.HasPrefix(version, prefix) {
			return true
		}
	}
	return false
}

// fetchLatestVersions determines the appropriate strategy and fetches version info
func fetchLatestVersions(ctx context.Context, dependency shared.Dependency, registryClient shared.RegistryClient, options shared.Options, cache *shared.Cache) (absoluteLatest, constraintLatest string, err error) {
	// If semver flag is enabled and we have a prefixed version, get both versions in one call
	if options.Semver && shared.HasSemanticPrefix(dependency.OriginalVersion) {
		return registryClient.GetBothLatestVersions(ctx, dependency.Name, dependency.OriginalVersion, dependency.HostedURL, options, cache)
	}

	// Check if current version is pre-release to determine which method to use
	currentSemver, parseErr := semver.NewVersion(dependency.Version)
	if parseErr == nil && currentSemver.Prerelease() != "" {
		return registryClient.GetBothLatestVersions(ctx, dependency.Name, dependency.OriginalVersion, dependency.HostedURL, options, cache)
	}

	// Use absolute latest version fetching for stable versions (non-semver cases)
	latest, err := registryClient.GetLatestVersionFromRegistry(ctx, dependency.Name, dependency.HostedURL, options, cache)
	if err != nil {
		return "", "", err
	}
	return latest, latest, nil
}

// UpdateDependencies validates every target before applying atomic per-file
// replacements. Overlapping calls are serialized for their complete transaction.
func UpdateDependencies(filePath string, outdated []shared.OutdatedDependency, registryType shared.RegistryType, options shared.Options, workingDirectory string, log shared.LogFunc) error {
	byFile := make(map[string][]shared.OutdatedDependency)
	for _, dependency := range outdated {
		path := dependency.FilePath
		if path == "" {
			path = filePath
		}
		byFile[path] = append(byFile[path], dependency)
	}

	updater, err := getUpdater(registryType)
	if err != nil {
		return err
	}

	// Validate options before updating
	if err := updater.ValidateOptions(options); err != nil {
		return err
	}

	patternProvider := updater.GetPatternProvider()

	// Sort file paths for deterministic ordering
	filePaths := make([]string, 0, len(byFile))
	for path := range byFile {
		filePaths = append(filePaths, path)
	}
	shared.SortFilesByDepth(filePaths)
	unlockFiles := lockDependencyFiles(filePaths)
	defer unlockFiles()

	showFilenames := len(filePaths) > 1
	preparedUpdates := make(map[string]*shared.PreparedFileUpdate, len(filePaths))

	for _, path := range filePaths {
		dependencies := byFile[path]
		prepared, err := shared.PrepareDependenciesInFile(path, dependencies, patternProvider)
		if err != nil {
			return err
		}
		if prepared != nil {
			preparedUpdates[path] = prepared
		}
	}

	for _, path := range filePaths {
		dependencies := byFile[path]
		prepared := preparedUpdates[path]
		if prepared == nil {
			continue
		}
		if err := prepared.Apply(); err != nil {
			return err
		}
		if log != nil {
			log("\n")
			if showFilenames {
				displayPath := path
				effectiveWorkingDirectory := workingDirectory
				if effectiveWorkingDirectory == "" {
					effectiveWorkingDirectory, _ = os.Getwd()
				}
				if relativePath, err := filepath.Rel(effectiveWorkingDirectory, path); err == nil {
					displayPath = relativePath
				}
				log("%s:\n", displayPath)
			}
			for _, dependency := range dependencies {
				log("  Updated %s (%s): %s -> %s\n",
					dependency.Name,
					dependency.Type.String(),
					dependency.CurrentVersion,
					dependency.LatestVersion)
			}
		}
	}

	return nil
}

// getRegistryClient returns the appropriate registry client for the given registry type
func getRegistryClient(registryType shared.RegistryType, workingDirectory string, log shared.LogFunc) (shared.RegistryClient, error) {
	switch registryType {
	case shared.Npm:
		client := npm.NewRegistryClient()
		client.Log = log
		client.ConfigDirectory = workingDirectory
		return client, nil
	case shared.Pub:
		client := pub.NewRegistryClient()
		client.Log = log
		return client, nil
	default:
		return nil, fmt.Errorf("unsupported registry type: %s", registryType)
	}
}

// ValidateOptions validates the given options against the rules for the registry type.
// It is the single source of truth for registry-specific option validation and is used
// by both CheckOutdated and UpdateDependencies, and can be called by frontends for early validation.
func ValidateOptions(registryType shared.RegistryType, options shared.Options) error {
	registryUpdater, err := getUpdater(registryType)
	if err != nil {
		return err
	}
	return registryUpdater.ValidateOptions(options)
}

// getUpdater returns the appropriate updater for the given registry type
func getUpdater(registryType shared.RegistryType) (shared.Updater, error) {
	switch registryType {
	case shared.Npm:
		return npm.NewUpdater(), nil
	case shared.Pub:
		return pub.NewUpdater(), nil
	default:
		return nil, fmt.Errorf("unsupported registry type: %s", registryType)
	}
}
