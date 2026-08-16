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

type indexedDependency struct {
	index      int
	dependency shared.Dependency
}

// CheckOutdated checks dependencies concurrently while preserving input order in the result.
// It honors context cancellation, reports per-file and overall progress, derives workingDirectory when empty, and persists cache unless disabled.
func CheckOutdated(ctx context.Context, dependencies []shared.Dependency, registryType shared.RegistryType, options shared.Options, workingDirectory string, progressCallback shared.ProgressFunc, log shared.LogFunc) (*shared.CheckResult, error) {

	// Validate options against the registry's rules before doing any work
	if err := ValidateOptions(registryType, options); err != nil {
		return nil, err
	}

	workingDirectory, err := resolveWorkingDirectory(dependencies, workingDirectory)
	if err != nil {
		return nil, err
	}

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

func resolveWorkingDirectory(dependencies []shared.Dependency, workingDirectory string) (string, error) {
	if workingDirectory != "" {
		return workingDirectory, nil
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
		return filepath.Dir(filePaths[0]), nil
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to resolve working directory: %w", err)
	}
	return workingDirectory, nil
}

func checkOutdatedWithRegistryClient(ctx context.Context, dependencies []shared.Dependency, registryClient shared.RegistryClient, options shared.Options, workingDirectory string, progressCallback shared.ProgressFunc, log shared.LogFunc, cache *shared.Cache) (*shared.CheckResult, error) {
	grouped := make(map[string][]indexedDependency)
	for index, dependency := range dependencies {
		grouped[dependency.FilePath] = append(grouped[dependency.FilePath], indexedDependency{index: index, dependency: dependency})
	}

	filePaths := make([]string, 0, len(grouped))
	for file := range grouped {
		filePaths = append(filePaths, file)
	}
	shared.SortFilesByDepth(filePaths)

	var result checkResult
	processedDependencies := 0
	totalDependencies := len(dependencies)
	outputsByIndex := make([]dependencyCheckOutput, len(dependencies))

	for _, file := range filePaths {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("dependency checks cancelled before checking %s: %w", file, err)
		}
		fileDependencies := grouped[file]
		processedFileDependencies := 0
		outputs, err := checkFileDependencies(ctx, fileDependencies, registryClient, options, cache, func(output dependencyCheckOutput) {
			processedFileDependencies++
			processedDependencies++
			if progressCallback != nil {
				progressCallback(shared.Progress{
					FilePath:    file,
					FileCurrent: processedFileDependencies,
					FileTotal:   len(fileDependencies),
					Current:     processedDependencies,
					Total:       totalDependencies,
				})
			}
		})
		if err != nil {
			return nil, err
		}
		for _, output := range outputs {
			outputsByIndex[output.index] = output
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("dependency checks cancelled after checking %s: %w", file, err)
		}
	}

	loggedFiles := make(map[string]struct{}, len(grouped))
	for index, dependency := range dependencies {
		if log != nil {
			if _, logged := loggedFiles[dependency.FilePath]; !logged {
				displayPath := dependency.FilePath
				if relativePath, err := filepath.Rel(workingDirectory, dependency.FilePath); err == nil {
					displayPath = relativePath
				}
				dependencyCount := len(grouped[dependency.FilePath])
				dependencyWord := "dependencies"
				if dependencyCount == 1 {
					dependencyWord = "dependency"
				}
				log("Checking %s (%d %s)\n", displayPath, dependencyCount, dependencyWord)
				loggedFiles[dependency.FilePath] = struct{}{}
			}
			for _, message := range outputsByIndex[index].logs {
				log("%s", message)
			}
		}
		output := outputsByIndex[index]
		result.outdated = append(result.outdated, output.result.outdated...)
		result.errors = append(result.errors, output.result.errors...)
		result.semverSkipped = append(result.semverSkipped, output.result.semverSkipped...)
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

func checkFileDependencies(ctx context.Context, dependencies []indexedDependency, registryClient shared.RegistryClient, options shared.Options, cache *shared.Cache, onComplete func(output dependencyCheckOutput)) ([]dependencyCheckOutput, error) {
	if len(dependencies) == 0 {
		return nil, nil
	}

	workerCount := min(len(dependencies), maxConcurrentRegistryChecks)
	jobs := make(chan indexedDependency)
	completed := make(chan dependencyCheckOutput, len(dependencies))
	var workers sync.WaitGroup

	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				var indexed indexedDependency
				var ok bool
				select {
				case <-ctx.Done():
					return
				case indexed, ok = <-jobs:
					if !ok {
						return
					}
				}
				if ctx.Err() != nil {
					return
				}
				var result checkResult
				var logs []string
				captureLog := func(format string, args ...any) {
					logs = append(logs, fmt.Sprintf(format, args...))
				}
				dependencyContext := shared.ContextWithLog(ctx, captureLog)
				checkSingleDependency(dependencyContext, indexed.dependency, registryClient, options, cache, &result, captureLog)
				completed <- dependencyCheckOutput{index: indexed.index, result: result, logs: logs}
			}
		}()
	}

	go func() {
		for _, dependency := range dependencies {
			select {
			case <-ctx.Done():
				close(jobs)
				workers.Wait()
				close(completed)
				return
			case jobs <- dependency:
			}
		}
		close(jobs)
		workers.Wait()
		close(completed)
	}()

	outputs := make([]dependencyCheckOutput, 0, len(dependencies))
	for output := range completed {
		outputs = append(outputs, output)
		if onComplete != nil {
			onComplete(output)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("dependency checks cancelled: %w", err)
	}
	return outputs, nil
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
		if errors.Is(err, shared.ErrNoVersionsMeetMinimumReleaseAge) {
			if log != nil {
				log("No versions of %s are more than 24 hours old\n", dependency.Name)
			}
			return
		}

		// If constraint error, use the absolute latest already returned for semver skipped
		if errors.Is(err, shared.ErrNoVersionsSatisfyConstraint) && absoluteLatest != "" && isNewerVersion(dependency.Version, absoluteLatest) {
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
	if constraintLatest != "" && isNewerVersion(currentVersion, constraintLatest) {
		result.outdated = append(result.outdated, shared.OutdatedDependency{
			BaseDependency: dependency.BaseDependency,
			CurrentVersion: currentVersion,
			LatestVersion:  constraintLatest,
		})
	}

	// Add to semverSkipped if the absolute latest differs from the constraint-compatible latest
	if absoluteLatest != constraintLatest && absoluteLatest != "" && isNewerVersion(currentVersion, absoluteLatest) {
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

func isNewerVersion(currentVersion, candidateVersion string) bool {
	current, currentErr := semver.NewVersion(currentVersion)
	candidate, candidateErr := semver.NewVersion(candidateVersion)
	return currentErr == nil && candidateErr == nil && candidate.GreaterThan(current)
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

// UpdateDependencies validates every target before applying atomic per-file replacements.
// Overlapping calls are serialized across goroutines and processes for their complete transaction, and context cancellation is checked before each prepare and apply.
// A multi-file update is not transactional: an apply failure can leave earlier files updated.
func UpdateDependencies(ctx context.Context, filePath string, outdated []shared.OutdatedDependency, registryType shared.RegistryType, options shared.Options, workingDirectory string, log shared.LogFunc) error {
	if len(outdated) == 0 {
		return nil
	}
	if workingDirectory == "" {
		var err error
		workingDirectory, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to resolve working directory: %w", err)
		}
	}
	byFile := make(map[string][]shared.OutdatedDependency)
	for _, dependency := range outdated {
		if !isSupportedDependencyType(dependency.Type) {
			return fmt.Errorf("unsupported dependency type: %d", dependency.Type)
		}
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
	unlockFiles, err := lockDependencyFiles(ctx, filePaths)
	if err != nil {
		return err
	}
	defer unlockFiles()

	showFilenames := len(filePaths) > 1
	preparedUpdates := make(map[string]*shared.PreparedFileUpdate, len(filePaths))

	for _, path := range filePaths {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("dependency update cancelled before preparing %s: %w", path, err)
		}
		dependencies := byFile[path]
		prepared, err := shared.PrepareDependenciesInFile(path, dependencies, patternProvider)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("dependency update cancelled after preparing %s: %w", path, err)
		}
		if prepared != nil {
			preparedUpdates[path] = prepared
		}
	}

	for _, path := range filePaths {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("dependency update cancelled before applying %s: %w", path, err)
		}
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
				if relativePath, err := filepath.Rel(workingDirectory, path); err == nil {
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
	case shared.NPM:
		client := npm.NewRegistryClient()
		client.Log = log
		client.ConfigDirectory = workingDirectory
		return client, nil
	case shared.Pub:
		client := pub.NewRegistryClient()
		client.Log = log
		return client, nil
	default:
		return nil, fmt.Errorf("%w: %d", shared.ErrUnsupportedRegistryType, registryType)
	}
}

// ValidateOptions rejects options unsupported by registryType.
// It is the same validation used by CheckOutdated and UpdateDependencies and can be called by frontends before starting work.
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
	case shared.NPM:
		return npm.NewUpdater(), nil
	case shared.Pub:
		return pub.NewUpdater(), nil
	default:
		return nil, fmt.Errorf("%w: %d", shared.ErrUnsupportedRegistryType, registryType)
	}
}

func isSupportedDependencyType(dependencyType shared.DependencyType) bool {
	switch dependencyType {
	case shared.Dependencies, shared.DevDependencies, shared.PeerDependencies:
		return true
	default:
		return false
	}
}
