package pub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/MilosRandelovic/bump-core/shared"
)

// RegistryClient handles pub.dev and private registry operations
type RegistryClient struct {
	Log shared.LogFunc
}

func (client *RegistryClient) log(format string, args ...any) {
	if client.Log != nil {
		client.Log(format, args...)
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
func (client *RegistryClient) GetLatestVersionFromRegistry(packageName, registryURL string, options shared.Options, cache *shared.Cache) (string, error) {
	targetRegistry, err := client.resolveRegistry(registryURL)
	if err != nil {
		return "", err
	}

	// Check cache first if enabled
	if cache != nil {
		key := shared.GenerateCacheKey(packageName, "pub", targetRegistry.URL, "", "*")
		if entry, ok := cache.Get(key); ok {
			client.log("Cache hit: %s\n", packageName)
			return entry.AbsoluteLatest, nil
		}
	}

	body, err := client.fetchPackageInfo(packageName, targetRegistry, options)
	if err != nil {
		return "", err
	}

	var packageInfo PubDevPackageInfo
	if err := json.Unmarshal(body, &packageInfo); err != nil {
		return "", fmt.Errorf("failed to parse pub.dev response: %w", err)
	}

	// Cache the result if cache is enabled
	if cache != nil {
		entry := shared.CacheEntry{
			PackageName:      packageName,
			Type:             "pub",
			Registry:         targetRegistry.URL,
			CurrentVersion:   "",
			Constraint:       "*",
			AbsoluteLatest:   packageInfo.Latest.Version,
			ConstraintLatest: packageInfo.Latest.Version,
			Expiry:           time.Now().Add(10 * time.Minute),
		}
		cache.Set(entry)
	}
	return packageInfo.Latest.Version, nil
}

// GetBothLatestVersions fetches both the absolute latest version and the latest version satisfying a constraint
func (client *RegistryClient) GetBothLatestVersions(packageName, constraint, registryURL string, options shared.Options, cache *shared.Cache) (string, string, error) {
	targetRegistry, err := client.resolveRegistry(registryURL)
	if err != nil {
		return "", "", err
	}

	// Check cache first if enabled
	if cache != nil {
		key := shared.GenerateCacheKey(packageName, "pub", targetRegistry.URL, "", constraint)
		if entry, ok := cache.Get(key); ok {
			client.log("Cache hit: %s\n", packageName)
			return entry.AbsoluteLatest, entry.ConstraintLatest, nil
		}
	}

	body, err := client.fetchPackageInfo(packageName, targetRegistry, options)
	if err != nil {
		return "", "", err
	}

	var packageInfo struct {
		Versions []struct {
			Version string `json:"version"`
		} `json:"versions"`
	}

	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&packageInfo); err != nil {
		return "", "", fmt.Errorf("error decoding response: %w", err)
	}

	// Extract version strings
	var versions []string
	for _, versionInfo := range packageInfo.Versions {
		versions = append(versions, versionInfo.Version)
	}

	absoluteLatest, constraintLatest, err := shared.FindBothLatestVersions(versions, constraint)
	if err != nil {
		return "", "", err
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

func (client *RegistryClient) resolveRegistry(registryURL string) (RegistryConfig, error) {
	config, err := parsePubConfig()
	if err != nil {
		return RegistryConfig{}, fmt.Errorf("failed to parse pub config: %w", err)
	}

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
func (client *RegistryClient) fetchPackageInfo(packageName string, targetRegistry RegistryConfig, options shared.Options) ([]byte, error) {
	url := fmt.Sprintf("%s/api/packages/%s", strings.TrimRight(targetRegistry.URL, "/"), packageName)

	client.log("Checking pub package: %s (registry: %s)\n", packageName, targetRegistry.URL)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add authentication if available for this registry
	if targetRegistry.AuthToken != "" {
		request.Header.Set("Authorization", "Bearer "+targetRegistry.AuthToken)
		client.log("Using authentication for registry: %s\n", targetRegistry.URL)
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
