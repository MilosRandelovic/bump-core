package pub

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

// pubConfig holds configuration for pub registries
type pubConfig struct {
	Registries map[string]registryConfig // maps registry hostname to config
}

// registryConfig holds configuration for a specific registry
type registryConfig struct {
	URL       string
	AuthToken string
}

// parsePubConfig parses pub configuration from various sources
// This mimics how pub handles registry configuration
func parsePubConfig(log shared.LogFunc) (*pubConfig, error) {
	config := &pubConfig{
		Registries: make(map[string]registryConfig),
	}

	// Add default pub.dev registry
	config.Registries["pub.dev"] = registryConfig{
		URL: "https://pub.dev",
	}

	// Try to parse from pub-tokens.json (dart pub token add)
	if err := parsePubTokensConfig(config); err != nil {
		if log != nil {
			log("Warning: Could not load pub authentication tokens: %v\n", err)
		}
	}

	return config, nil
}

// parsePubTokensConfig reads authentication tokens from dart pub cache
func parsePubTokensConfig(config *pubConfig) error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to resolve user config directory: %w", err)
	}

	// Check for pub-tokens.json file where dart pub token add stores credentials
	pubTokensPath := filepath.Join(configDir, "dart", "pub-tokens.json")
	if _, err := os.Stat(pubTokensPath); os.IsNotExist(err) {
		return nil // File doesn't exist, that's okay
	}

	return parsePubTokensFile(pubTokensPath, config)
}

// pubTokensFile represents the structure of pub-tokens.json
type pubTokensFile struct {
	Version int               `json:"version"`
	Hosted  []pubTokensHosted `json:"hosted"`
}

// pubTokensHosted represents a hosted registry entry in pub-tokens.json
type pubTokensHosted struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// parsePubTokensFile parses the pub-tokens.json file created by dart pub token add
func parsePubTokensFile(filePath string, config *pubConfig) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read pub-tokens.json: %w", err)
	}

	var tokensFile pubTokensFile
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
			config.Registries[hostname] = registryConfig{
				URL:       hosted.URL,
				AuthToken: hosted.Token,
			}
		}
	}

	return nil
}
