package pub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

// RegistryClient handles pub.dev and private registry operations
type RegistryClient struct {
	Log         shared.LogFunc
	currentTime func() time.Time
	configOnce  sync.Once
	config      *pubConfig
	configErr   error
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

// pubDevPackageInfo represents the response from pub.dev API
type pubDevPackageInfo struct {
	Latest struct {
		Version string `json:"version"`
	} `json:"latest"`
	Versions []struct {
		Version   string `json:"version"`
		Published string `json:"published"`
	} `json:"versions"`
}

// NewRegistryClient returns a Pub registry client that lazily loads configured authentication tokens.
// Set Log before the first request to receive diagnostics.
func NewRegistryClient() *RegistryClient {
	return &RegistryClient{}
}

// GetLatestVersionFromRegistry returns the package's latest eligible version from Pub or a hosted registry.
// It applies configured authentication, honors minimum-age filtering, and uses cache when non-nil.
func (client *RegistryClient) GetLatestVersionFromRegistry(ctx context.Context, packageName, registryURL string, options shared.Options, cache *shared.Cache) (string, error) {
	targetRegistry, err := client.resolveRegistry(ctx, registryURL)
	if err != nil {
		return "", err
	}

	// Check cache first if enabled
	if cache != nil {
		key := shared.GenerateCacheKey(packageName, "pub", targetRegistry.URL, "", "*", options)
		if entry, ok := cache.Get(key); ok {
			client.log(ctx, "Cache hit: %s\n", packageName)
			return entry.AbsoluteLatest, nil
		}
	}

	body, err := client.fetchPackageInfo(ctx, packageName, targetRegistry)
	if err != nil {
		return "", err
	}

	var packageInfo pubDevPackageInfo
	if err := json.Unmarshal(body, &packageInfo); err != nil {
		return "", fmt.Errorf("failed to parse pub.dev response: %w", err)
	}
	latest := strings.TrimSpace(packageInfo.Latest.Version)
	if latest == "" {
		return "", fmt.Errorf("no latest version found for %s", packageName)
	}
	var nextEligibility time.Time
	if options.EnforceMinimumReleaseAge {
		eligibleVersions, eligibility, filterErr := client.filterVersionsByMinimumReleaseAge(ctx, packageName, packageInfo)
		if filterErr != nil {
			return "", filterErr
		}
		_, latest, filterErr = shared.FindBothLatestVersions(eligibleVersions, "<="+latest)
		if filterErr != nil {
			return "", filterErr
		}
		nextEligibility = eligibility
	}

	// Cache the result if cache is enabled
	if cache != nil {
		now := client.now()
		entry := shared.CacheEntry{
			PackageName:      packageName,
			Type:             "pub",
			Registry:         targetRegistry.URL,
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

// GetBothLatestVersions returns the latest eligible Pub version and the latest one satisfying constraint.
// It preserves the absolute result when returning ErrNoVersionsSatisfyConstraint and uses cache when non-nil.
func (client *RegistryClient) GetBothLatestVersions(ctx context.Context, packageName, constraint, registryURL string, options shared.Options, cache *shared.Cache) (absoluteLatest string, constraintLatest string, err error) {
	targetRegistry, err := client.resolveRegistry(ctx, registryURL)
	if err != nil {
		return "", "", err
	}

	// Check cache first if enabled
	if cache != nil {
		key := shared.GenerateCacheKey(packageName, "pub", targetRegistry.URL, "", constraint, options)
		if entry, ok := cache.Get(key); ok {
			client.log(ctx, "Cache hit: %s\n", packageName)
			return entry.AbsoluteLatest, entry.ConstraintLatest, nil
		}
	}

	body, err := client.fetchPackageInfo(ctx, packageName, targetRegistry)
	if err != nil {
		return "", "", err
	}

	var packageInfo pubDevPackageInfo

	if err := json.Unmarshal(body, &packageInfo); err != nil {
		return "", "", fmt.Errorf("error decoding response: %w", err)
	}

	// Extract version strings
	var versions []string
	for _, versionInfo := range packageInfo.Versions {
		versions = append(versions, versionInfo.Version)
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
			Type:             "pub",
			Registry:         targetRegistry.URL,
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

func (client *RegistryClient) filterVersionsByMinimumReleaseAge(ctx context.Context, packageName string, packageInfo pubDevPackageInfo) ([]string, time.Time, error) {
	publishedVersions := make([]shared.PublishedVersion, 0, len(packageInfo.Versions))
	for _, versionInfo := range packageInfo.Versions {
		publishedVersions = append(publishedVersions, shared.PublishedVersion{
			Version:     versionInfo.Version,
			PublishedAt: versionInfo.Published,
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

func (client *RegistryClient) resolveRegistry(ctx context.Context, registryURL string) (registryConfig, error) {
	client.configOnce.Do(func() {
		log := shared.LogFromContext(ctx)
		if log == nil {
			log = client.Log
		}
		client.config, client.configErr = parsePubConfig(log)
		if client.configErr != nil {
			client.configErr = fmt.Errorf("failed to parse pub config: %w", client.configErr)
		}
	})
	if client.configErr != nil {
		return registryConfig{}, client.configErr
	}
	config := client.config

	if registryURL != "" {
		hostname := shared.ExtractHostname(registryURL)
		if registryConfig, exists := config.Registries[hostname]; exists {
			return registryConfig, nil
		}
		return registryConfig{URL: registryURL}, nil
	}

	if pubDevConfig, exists := config.Registries["pub.dev"]; exists {
		return pubDevConfig, nil
	}

	return registryConfig{URL: "https://pub.dev"}, nil
}

// fetchPackageInfo is a shared method to fetch package information from registries
func (client *RegistryClient) fetchPackageInfo(ctx context.Context, packageName string, targetRegistry registryConfig) ([]byte, error) {
	url := fmt.Sprintf("%s/api/packages/%s", strings.TrimRight(targetRegistry.URL, "/"), packageName)

	client.log(ctx, "Checking pub package: %s (registry: %s)\n", packageName, targetRegistry.URL)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	request, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add authentication if available for this registry
	if targetRegistry.AuthToken != "" {
		request.Header.Set("Authorization", "Bearer "+targetRegistry.AuthToken)
		client.log(ctx, "Using authentication for registry: %s\n", targetRegistry.URL)
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
