package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

// RegistryClient handles npm registry operations
type RegistryClient struct {
	Log             shared.LogFunc
	ConfigDirectory string
	currentTime     func() time.Time
	configOnce      sync.Once
	config          *npmConfig
	configErr       error
}

func (client *RegistryClient) log(ctx context.Context, format string, args ...any) {
	log := shared.LogFromContext(ctx)
	if log == nil {
		log = client.Log
	}
	if log != nil {
		log(format, args...)
	}
}

// npmPackageInfo represents the response from npm registry
type npmPackageInfo struct {
	DistTags map[string]string `json:"dist-tags"`
	Time     map[string]string `json:"time"`
	Versions map[string]struct {
		Version    string `json:"version"`
		Deprecated any    `json:"deprecated,omitempty"`
	} `json:"versions"`
}

// NewRegistryClient returns an npm registry client that lazily loads .npmrc configuration.
// Set ConfigDirectory before the first request to resolve project configuration, and set Log for diagnostics.
func NewRegistryClient() *RegistryClient {
	return &RegistryClient{}
}

// GetLatestVersionFromRegistry returns the package's latest eligible npm dist-tag version.
// It resolves scoped registries and authentication from .npmrc, honors minimum-age filtering, and uses cache when non-nil.
func (client *RegistryClient) GetLatestVersionFromRegistry(ctx context.Context, packageName, registryURL string, options shared.Options, cache *shared.Cache) (string, error) {
	targetRegistryURL, npmrcConfig, err := client.resolveRegistryURL(packageName, registryURL)
	if err != nil {
		return "", err
	}

	// Check cache first if enabled
	if cache != nil {
		key := shared.GenerateCacheKey(packageName, "npm", targetRegistryURL, "", "*", options)
		if entry, ok := cache.Get(key); ok {
			client.log(ctx, "Cache hit: %s\n", packageName)
			return entry.AbsoluteLatest, nil
		}
	}

	body, err := client.fetchPackageInfo(ctx, packageName, targetRegistryURL, npmrcConfig)
	if err != nil {
		return "", err
	}

	var packageInfo npmPackageInfo
	if err := json.Unmarshal(body, &packageInfo); err != nil {
		return "", fmt.Errorf("failed to parse npm response: %w", err)
	}

	latest, ok := packageInfo.DistTags["latest"]
	var nextEligibility time.Time
	if options.EnforceMinimumReleaseAge {
		if !ok || strings.TrimSpace(latest) == "" {
			return "", fmt.Errorf("no latest version found for %s", packageName)
		}
		eligibleVersions, eligibility, filterErr := client.filterVersionsByMinimumReleaseAge(ctx, packageName, packageInfo)
		if filterErr != nil {
			return "", filterErr
		}
		_, latest, filterErr = shared.FindBothLatestVersions(eligibleVersions, "<="+latest)
		if filterErr != nil {
			return "", filterErr
		}
		nextEligibility = eligibility
		ok = true
	}

	if ok {

		// Cache the result if cache is enabled
		if cache != nil {
			now := client.now()
			entry := shared.CacheEntry{
				PackageName:      packageName,
				Type:             "npm",
				Registry:         targetRegistryURL,
				CurrentVersion:   "",
				Constraint:       "*",
				MinimumAge:       options.EnforceMinimumReleaseAge,
				AbsoluteLatest:   latest,
				ConstraintLatest: latest,
				Expiry:           shared.CacheExpiry(now, nextEligibility),
			}
			cache.Set(entry)
		}
		return latest, nil
	}

	return "", fmt.Errorf("no latest version found for %s", packageName)
}

// GetBothLatestVersions returns the latest eligible non-deprecated npm version and the latest one satisfying constraint.
// It preserves the absolute result when returning ErrNoVersionsSatisfyConstraint and uses cache when non-nil.
func (client *RegistryClient) GetBothLatestVersions(ctx context.Context, packageName, constraint, registryURL string, options shared.Options, cache *shared.Cache) (absoluteLatest string, constraintLatest string, err error) {
	targetRegistryURL, npmrcConfig, err := client.resolveRegistryURL(packageName, registryURL)
	if err != nil {
		return "", "", err
	}

	// Check cache first if enabled
	if cache != nil {
		key := shared.GenerateCacheKey(packageName, "npm", targetRegistryURL, "", constraint, options)
		if entry, ok := cache.Get(key); ok {
			client.log(ctx, "Cache hit: %s\n", packageName)
			return entry.AbsoluteLatest, entry.ConstraintLatest, nil
		}
	}

	body, err := client.fetchPackageInfo(ctx, packageName, targetRegistryURL, npmrcConfig)
	if err != nil {
		return "", "", err
	}

	var packageInfo npmPackageInfo
	if err := json.Unmarshal(body, &packageInfo); err != nil {
		return "", "", fmt.Errorf("failed to parse npm response: %w", err)
	}

	// Get all non-deprecated versions
	versions := make([]string, 0, len(packageInfo.Versions))
	for version, versionInfo := range packageInfo.Versions {

		// Include only non-deprecated versions (deprecated field is null/missing for non-deprecated)
		if versionInfo.Deprecated == nil || versionInfo.Deprecated == "" {
			versions = append(versions, version)
		}
	}
	var nextEligibility time.Time
	if options.EnforceMinimumReleaseAge {
		versions, nextEligibility, err = client.filterVersionsByMinimumReleaseAge(ctx, packageName, packageInfo)
		if err != nil {
			return "", "", err
		}
	}

	absoluteLatest, constraintLatest, err = shared.FindBothLatestVersions(versions, constraint)
	if err != nil {
		return absoluteLatest, constraintLatest, err
	}

	// Cache the result if cache is enabled
	if cache != nil {
		now := client.now()
		entry := shared.CacheEntry{
			PackageName:      packageName,
			Type:             "npm",
			Registry:         targetRegistryURL,
			CurrentVersion:   "",
			Constraint:       constraint,
			MinimumAge:       options.EnforceMinimumReleaseAge,
			AbsoluteLatest:   absoluteLatest,
			ConstraintLatest: constraintLatest,
			Expiry:           shared.CacheExpiry(now, nextEligibility),
		}
		cache.Set(entry)
	}

	return absoluteLatest, constraintLatest, nil
}

func (client *RegistryClient) filterVersionsByMinimumReleaseAge(ctx context.Context, packageName string, packageInfo npmPackageInfo) ([]string, time.Time, error) {
	publishedVersions := make([]shared.PublishedVersion, 0, len(packageInfo.Versions))
	for version, versionInfo := range packageInfo.Versions {
		if versionInfo.Deprecated != nil && versionInfo.Deprecated != "" {
			continue
		}
		publishedVersions = append(publishedVersions, shared.PublishedVersion{
			Version:     version,
			PublishedAt: packageInfo.Time[version],
		})
	}

	filtered := shared.FilterVersionsByMinimumReleaseAge(publishedVersions, client.now())
	for _, version := range filtered.TooYoung {
		client.log(ctx, "Ignoring %s %s because it is not more than 24 hours old\n", packageName, version)
	}
	if len(filtered.Unverified) > 0 {
		client.log(ctx, "Ignoring %d version(s) of %s with unverifiable publication times\n", len(filtered.Unverified), packageName)
	}
	if len(filtered.Versions) == 0 {
		if len(filtered.Unverified) > 0 {
			return nil, time.Time{}, fmt.Errorf("could not verify publication times for %s", packageName)
		}
		return nil, filtered.NextEligibility, shared.ErrNoVersionsMeetMinimumReleaseAge
	}
	return filtered.Versions, filtered.NextEligibility, nil
}

func (client *RegistryClient) now() time.Time {
	if client.currentTime != nil {
		return client.currentTime()
	}
	return time.Now()
}

func (client *RegistryClient) resolveRegistryURL(packageName, registryURL string) (string, *npmConfig, error) {
	configDirectory := client.ConfigDirectory
	if configDirectory == "" {
		currentWorkingDir, err := os.Getwd()
		if err != nil {
			return "", nil, fmt.Errorf("failed to get current directory: %w", err)
		}
		configDirectory = currentWorkingDir
	}

	client.configOnce.Do(func() {
		npmrcPath := filepath.Join(configDirectory, ".npmrc")
		client.config, client.configErr = parseNpmrcFiles(npmrcPath)
		if client.configErr != nil {
			client.configErr = fmt.Errorf("failed to parse .npmrc: %w", client.configErr)
		}
	})
	if client.configErr != nil {
		return "", nil, client.configErr
	}
	npmrcConfig := client.config

	if registryURL != "" {
		return registryURL, npmrcConfig, nil
	}

	return getRegistryForPackage(packageName, npmrcConfig), npmrcConfig, nil
}

// fetchPackageInfo is a shared method to fetch package information from registries
func (client *RegistryClient) fetchPackageInfo(ctx context.Context, packageName, targetRegistryURL string, npmrcConfig *npmConfig) ([]byte, error) {
	url := fmt.Sprintf("%s/%s", strings.TrimRight(targetRegistryURL, "/"), packageName)

	client.log(ctx, "Checking npm package: %s (registry: %s)\n", packageName, targetRegistryURL)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	request, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add authentication if available for this registry
	if authToken := getAuthTokenForRegistry(targetRegistryURL, npmrcConfig); authToken != "" {
		request.Header.Set("Authorization", "Bearer "+authToken)
		client.log(ctx, "Using authentication for registry: %s\n", targetRegistryURL)
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch package info: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned status %d for %s", response.StatusCode, packageName)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return body, nil
}

// Ensure RegistryClient implements the interface
var _ shared.RegistryClient = (*RegistryClient)(nil)
