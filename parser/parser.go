package parser

import (
	"fmt"

	"github.com/MilosRandelovic/bump-core/npm"
	"github.com/MilosRandelovic/bump-core/pub"
	"github.com/MilosRandelovic/bump-core/shared"
)

// ParseDependencies parses dependencies from a file based on its type
func ParseDependencies(filePath string, registryType shared.RegistryType, options shared.Options) ([]shared.Dependency, error) {
	parser, err := getParser(registryType)
	if err != nil {
		return nil, err
	}
	return parser.ParseDependencies(filePath, options)
}

// getParser returns the appropriate parser for the given file type
func getParser(registryType shared.RegistryType) (shared.Parser, error) {
	switch registryType {
	case shared.Npm:
		return npm.NewParser(), nil
	case shared.Pub:
		return pub.NewParser(), nil
	default:
		return nil, fmt.Errorf("unsupported registry type: %s", registryType)
	}
}
