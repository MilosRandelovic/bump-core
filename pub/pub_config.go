package pub

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

type pubConfig struct {
	Registries map[string]registryConfig
}

type registryConfig struct {
	URL       string
	AuthToken string
}

func parsePubConfig(log shared.LogFunc) (*pubConfig, error) {
	config := &pubConfig{
		Registries: make(map[string]registryConfig),
	}

	config.Registries["pub.dev"] = registryConfig{
		URL: "https://pub.dev",
	}

	if err := parsePubTokensConfig(config); err != nil {
		if log != nil {
			log("Warning: Could not load pub authentication tokens: %v\n", err)
		}
	}

	return config, nil
}

func parsePubTokensConfig(config *pubConfig) error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to resolve user config directory: %w", err)
	}

	pubTokensPath := filepath.Join(configDir, "dart", "pub-tokens.json")
	if _, err := os.Stat(pubTokensPath); os.IsNotExist(err) {
		return nil
	}

	return parsePubTokensFile(pubTokensPath, config)
}

type pubTokensFile struct {
	Version int               `json:"version"`
	Hosted  []pubTokensHosted `json:"hosted"`
}

type pubTokensHosted struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func parsePubTokensFile(filePath string, config *pubConfig) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read pub-tokens.json: %w", err)
	}

	var tokensFile pubTokensFile
	if err := json.Unmarshal(data, &tokensFile); err != nil {
		return fmt.Errorf("failed to parse pub-tokens.json: %w", err)
	}

	for _, hosted := range tokensFile.Hosted {
		hostname := shared.ExtractHostname(hosted.URL)
		if existingConfig, exists := config.Registries[hostname]; exists {
			existingConfig.AuthToken = hosted.Token
			config.Registries[hostname] = existingConfig
		} else {

			config.Registries[hostname] = registryConfig{
				URL:       hosted.URL,
				AuthToken: hosted.Token,
			}
		}
	}

	return nil
}
