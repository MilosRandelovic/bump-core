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
	configOnce      sync.Once
	config          *NpmConfig
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

// NpmPackageInfo represents the response from npm registry
type NpmPackageInfo struct {
	DistTags map[string]string `json:"dist-tags"`
	Versions map[string]struct {
		Version    string `json:"version"`
		Deprecated any    `json:"deprecated,omitempty"`
	} `json:"versions"`
}

// NewRegistryClient creates a new npm registry client
func NewRegistryClient() *RegistryClient {
	return &RegistryClient{}
}

// GetLatestVersionFromRegistry fetches the latest version from a specific registry
func (client *RegistryClient) GetLatestVersionFromRegistry(ctx context.Context, packageName, registryURL string, _ shared.Options, cache *shared.Cache) (string, error) {
	targetRegistryURL, npmrcConfig, err := client.resolveRegistryURL(packageName, registryURL)
	if err != nil {
		return "", err
	}

	// Check cache first if enabled
	if cache != nil {
		key := shared.GenerateCacheKey(packageName, "npm", targetRegistryURL, "", "*")
		if entry, ok := cache.Get(key); ok {
			client.log(ctx, "Cache hit: %s\n", packageName)
			return entry.AbsoluteLatest, nil
		}
	}

	body, err := client.fetchPackageInfo(ctx, packageName, targetRegistryURL, npmrcConfig)
	if err != nil {
		return "", err
	}

	var packageInfo NpmPackageInfo
	if err := json.Unmarshal(body, &packageInfo); err != nil {
		return "", fmt.Errorf("failed to parse npm response: %w", err)
	}

	if latest, ok := packageInfo.DistTags["latest"]; ok {
		// Cache the result if cache is enabled
		if cache != nil {
			entry := shared.CacheEntry{
				PackageName:      packageName,
				Type:             "npm",
				Registry:         targetRegistryURL,
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

	return "", fmt.Errorf("no latest version found for %s", packageName)
}

// GetBothLatestVersions fetches both the absolute latest version and the latest version satisfying a constraint
func (client *RegistryClient) GetBothLatestVersions(ctx context.Context, packageName, constraint, registryURL string, _ shared.Options, cache *shared.Cache) (string, string, error) {
	targetRegistryURL, npmrcConfig, err := client.resolveRegistryURL(packageName, registryURL)
	if err != nil {
		return "", "", err
	}

	// Check cache first if enabled
	if cache != nil {
		key := shared.GenerateCacheKey(packageName, "npm", targetRegistryURL, "", constraint)
		if entry, ok := cache.Get(key); ok {
			client.log(ctx, "Cache hit: %s\n", packageName)
			return entry.AbsoluteLatest, entry.ConstraintLatest, nil
		}
	}

	body, err := client.fetchPackageInfo(ctx, packageName, targetRegistryURL, npmrcConfig)
	if err != nil {
		return "", "", err
	}

	var packageInfo NpmPackageInfo
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

	absoluteLatest, constraintLatest, err := shared.FindBothLatestVersions(versions, constraint)
	if err != nil {
		return absoluteLatest, constraintLatest, err
	}

	// Cache the result if cache is enabled
	if cache != nil {
		entry := shared.CacheEntry{
			PackageName:      packageName,
			Type:             "npm",
			Registry:         targetRegistryURL,
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

func (client *RegistryClient) resolveRegistryURL(packageName, registryURL string) (string, *NpmConfig, error) {
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
func (client *RegistryClient) fetchPackageInfo(ctx context.Context, packageName, targetRegistryURL string, npmrcConfig *NpmConfig) ([]byte, error) {
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

// GetRegistryType returns the registry type this client handles
func (client *RegistryClient) GetRegistryType() shared.RegistryType {
	return shared.Npm
}

// Ensure RegistryClient implements the interface
var _ shared.RegistryClient = (*RegistryClient)(nil)
