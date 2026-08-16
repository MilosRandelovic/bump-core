package npm

import (
	"bufio"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type npmConfig struct {
	ScopeRegistries map[string]string
	AuthTokens      map[string]string
}

func parseNpmrcFiles(localPath string) (*npmConfig, error) {
	config := &npmConfig{
		ScopeRegistries: make(map[string]string),
		AuthTokens:      make(map[string]string),
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve home directory: %w", err)
	}
	globalNpmrcPath := filepath.Join(homeDir, ".npmrc")
	globalConfig, err := parseNpmrcFile(globalNpmrcPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse global .npmrc: %w", err)
	}

	maps.Copy(config.ScopeRegistries, globalConfig.ScopeRegistries)
	maps.Copy(config.AuthTokens, globalConfig.AuthTokens)

	localConfig, err := parseNpmrcFile(localPath)
	if err != nil {

		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to parse local .npmrc: %w", err)
		}
	} else {

		maps.Copy(config.ScopeRegistries, localConfig.ScopeRegistries)

		maps.Copy(config.AuthTokens, localConfig.AuthTokens)
	}

	return config, nil
}

func parseNpmrcFile(filePath string) (*npmConfig, error) {
	config := &npmConfig{
		ScopeRegistries: make(map[string]string),
		AuthTokens:      make(map[string]string),
	}

	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {

			return config, nil
		}
		return nil, fmt.Errorf("failed to open .npmrc file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.Contains(line, ":registry=") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				registry := strings.TrimSpace(parts[1])

				if strings.HasSuffix(key, ":registry") {
					scope := strings.TrimSuffix(key, ":registry")
					config.ScopeRegistries[scope] = registry
				}
			}
		}

		if strings.Contains(line, ":_authToken=") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				token := strings.Trim(strings.TrimSpace(parts[1]), "\"'")

				if strings.HasSuffix(key, ":_authToken") {
					registry := strings.TrimSuffix(key, ":_authToken")
					registry = strings.TrimPrefix(registry, "//")
					registry = strings.TrimSuffix(registry, "/")
					config.AuthTokens[registry] = token
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading .npmrc file: %w", err)
	}

	return config, nil
}

func getRegistryForPackage(packageName string, npmrcConfig *npmConfig) string {

	if strings.HasPrefix(packageName, "@") {
		if index := strings.Index(packageName[1:], "/"); index != -1 {
			scope := packageName[:index+1]
			if registry, exists := npmrcConfig.ScopeRegistries[scope]; exists {
				return registry
			}
		}
	}

	return "https://registry.npmjs.org"
}

func getAuthTokenForRegistry(registryURL string, npmrcConfig *npmConfig) string {
	target := normalizeRegistryAuthKey(registryURL)
	longestMatch := ""
	matchedToken := ""
	for configuredRegistry, token := range npmrcConfig.AuthTokens {
		candidate := normalizeRegistryAuthKey(configuredRegistry)
		if candidate == "" {
			continue
		}
		if target != candidate && !strings.HasPrefix(target, candidate+"/") {
			continue
		}
		if len(candidate) > len(longestMatch) {
			longestMatch = candidate
			matchedToken = token
		}
	}
	return matchedToken
}

func normalizeRegistryAuthKey(registry string) string {
	registry = strings.TrimSpace(registry)
	if registry == "" {
		return ""
	}
	if strings.HasPrefix(registry, "//") {
		registry = "https:" + registry
	} else if !strings.Contains(registry, "://") {
		registry = "https://" + registry
	}
	parsed, err := url.Parse(registry)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Host) + strings.TrimRight(parsed.EscapedPath(), "/")
}
