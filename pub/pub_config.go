package pub

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MilosRandelovic/bump-core/shared"
)

// PubConfig holds configuration for pub registries
type PubConfig struct {
	Registries map[string]RegistryConfig // maps registry hostname to config
}

// RegistryConfig holds configuration for a specific registry
type RegistryConfig struct {
	URL       string
	AuthToken string
}

// parsePubConfig parses pub configuration from various sources
// This mimics how pub handles registry configuration
func parsePubConfig() (*PubConfig, error) {
	config := &PubConfig{
		Registries: make(map[string]RegistryConfig),
	}

	// Add default pub.dev registry
	config.Registries["pub.dev"] = RegistryConfig{
		URL: "https://pub.dev",
	}

	// Try to parse from pub-tokens.json (dart pub token add)
	if err := parsePubTokensConfig(config); err != nil {
		// Silently ignore errors - pub-tokens.json is optional
	}

	return config, nil
}

// parsePubTokensConfig reads authentication tokens from dart pub cache
func parsePubTokensConfig(config *PubConfig) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Check for pub-tokens.json file where dart pub token add stores credentials
	pubTokensPath := filepath.Join(homeDir, "Library", "Application Support", "dart", "pub-tokens.json")
	if _, err := os.Stat(pubTokensPath); os.IsNotExist(err) {
		return nil // File doesn't exist, that's okay
	}

	return parsePubTokensFile(pubTokensPath, config)
}

// PubTokensFile represents the structure of pub-tokens.json
type PubTokensFile struct {
	Version int               `json:"version"`
	Hosted  []PubTokensHosted `json:"hosted"`
}

// PubTokensHosted represents a hosted registry entry in pub-tokens.json
type PubTokensHosted struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// parsePubTokensFile parses the pub-tokens.json file created by dart pub token add
func parsePubTokensFile(filePath string, config *PubConfig) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read pub-tokens.json: %w", err)
	}

	var tokensFile PubTokensFile
	if err := json.Unmarshal(data, &tokensFile); err != nil {
		return fmt.Errorf("failed to parse pub-tokens.json: %w", err)
	}

	// Add tokens to registry configurations
	for _, hosted := range tokensFile.Hosted {
		hostname := shared.ExtractHostname(hosted.URL)
		if existingConfig, exists := config.Registries[hostname]; exists {
			existingConfig.AuthToken = hosted.Token
			config.Registries[hostname] = existingConfig
		} else {
			// Create new registry config with token
			config.Registries[hostname] = RegistryConfig{
				URL:       hosted.URL,
				AuthToken: hosted.Token,
			}
		}
	}

	return nil
}
