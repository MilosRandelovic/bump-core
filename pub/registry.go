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
	Log        shared.LogFunc
	configOnce sync.Once
	config     *PubConfig
	configErr  error
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

// PubDevPackageInfo represents the response from pub.dev API
type PubDevPackageInfo struct {
	Latest struct {
		Version string `json:"version"`
	} `json:"latest"`
}

// NewRegistryClient creates a new pub registry client
func NewRegistryClient() *RegistryClient {
	return &RegistryClient{}
}

// GetLatestVersionFromRegistry fetches the latest version from a specific registry
func (client *RegistryClient) GetLatestVersionFromRegistry(ctx context.Context, packageName, registryURL string, _ shared.Options, cache *shared.Cache) (string, error) {
	targetRegistry, err := client.resolveRegistry(ctx, registryURL)
	if err != nil {
		return "", err
	}

	// Check cache first if enabled
	if cache != nil {
		key := shared.GenerateCacheKey(packageName, "pub", targetRegistry.URL, "", "*")
		if entry, ok := cache.Get(key); ok {
			client.log(ctx, "Cache hit: %s\n", packageName)
			return entry.AbsoluteLatest, nil
		}
	}

	body, err := client.fetchPackageInfo(ctx, packageName, targetRegistry)
	if err != nil {
		return "", err
	}

	var packageInfo PubDevPackageInfo
	if err := json.Unmarshal(body, &packageInfo); err != nil {
		return "", fmt.Errorf("failed to parse pub.dev response: %w", err)
	}
	latest := strings.TrimSpace(packageInfo.Latest.Version)
	if latest == "" {
		return "", fmt.Errorf("no latest version found for %s", packageName)
	}

	// Cache the result if cache is enabled
	if cache != nil {
		entry := shared.CacheEntry{
			PackageName:      packageName,
			Type:             "pub",
			Registry:         targetRegistry.URL,
			CurrentVersion:   "",
			Constraint:       "*",
			AbsoluteLatest:   latest,
			ConstraintLatest: latest,
			Expiry:           time.Now().Add(10 * time.Minute),
		}
		cache.Set(entry)
	}
	return latest, nil
}

// GetBothLatestVersions fetches both the absolute latest version and the latest version satisfying a constraint
func (client *RegistryClient) GetBothLatestVersions(ctx context.Context, packageName, constraint, registryURL string, _ shared.Options, cache *shared.Cache) (string, string, error) {
	targetRegistry, err := client.resolveRegistry(ctx, registryURL)
	if err != nil {
		return "", "", err
	}

	// Check cache first if enabled
	if cache != nil {
		key := shared.GenerateCacheKey(packageName, "pub", targetRegistry.URL, "", constraint)
		if entry, ok := cache.Get(key); ok {
			client.log(ctx, "Cache hit: %s\n", packageName)
			return entry.AbsoluteLatest, entry.ConstraintLatest, nil
		}
	}

	body, err := client.fetchPackageInfo(ctx, packageName, targetRegistry)
	if err != nil {
		return "", "", err
	}

	var packageInfo struct {
		Versions []struct {
			Version string `json:"version"`
		} `json:"versions"`
	}

	if err := json.Unmarshal(body, &packageInfo); err != nil {
		return "", "", fmt.Errorf("error decoding response: %w", err)
	}

	// Extract version strings
	var versions []string
	for _, versionInfo := range packageInfo.Versions {
		versions = append(versions, versionInfo.Version)
	}

	absoluteLatest, constraintLatest, err := shared.FindBothLatestVersions(versions, constraint)
	if err != nil {
		return absoluteLatest, constraintLatest, err
	}

	// Cache the result if cache is enabled
	if cache != nil {
		entry := shared.CacheEntry{
			PackageName:      packageName,
			Type:             "pub",
			Registry:         targetRegistry.URL,
			CurrentVersion:   "",
			Constraint:       constraint,
			AbsoluteLatest:   absoluteLatest,
			ConstraintLatest: constraintLatest,
			Expiry:           time.Now().Add(10 * time.Minute),
		}
		cache.Set(entry)
	}

	return absoluteLatest, constraintLatest, nil
}

func (client *RegistryClient) resolveRegistry(ctx context.Context, registryURL string) (RegistryConfig, error) {
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
		return RegistryConfig{}, client.configErr
	}
	config := client.config

	if registryURL != "" {
		hostname := shared.ExtractHostname(registryURL)
		if registryConfig, exists := config.Registries[hostname]; exists {
			return registryConfig, nil
		}
		return RegistryConfig{URL: registryURL}, nil
	}

	if pubDevConfig, exists := config.Registries["pub.dev"]; exists {
		return pubDevConfig, nil
	}

	return RegistryConfig{URL: "https://pub.dev"}, nil
}

// fetchPackageInfo is a shared method to fetch package information from registries
func (client *RegistryClient) fetchPackageInfo(ctx context.Context, packageName string, targetRegistry RegistryConfig) ([]byte, error) {
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

// GetRegistryType returns the registry type this client handles
func (client *RegistryClient) GetRegistryType() shared.RegistryType {
	return shared.Pub
}

// Ensure RegistryClient implements the interface
var _ shared.RegistryClient = (*RegistryClient)(nil)
