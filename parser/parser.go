package parser

import (
	"fmt"

	"github.com/MilosRandelovic/bump-core/v2/npm"
	"github.com/MilosRandelovic/bump-core/v2/pub"
	"github.com/MilosRandelovic/bump-core/v2/shared"
)

// ParseDependencies delegates filePath to the parser for registryType without emitting diagnostics.
func ParseDependencies(filePath string, registryType shared.RegistryType, options shared.Options) ([]shared.Dependency, error) {
	return ParseDependenciesWithLog(filePath, registryType, options, nil)
}

// ParseDependenciesWithLog delegates filePath to the parser for registryType and forwards optional diagnostics to log.
func ParseDependenciesWithLog(filePath string, registryType shared.RegistryType, options shared.Options, log shared.LogFunc) ([]shared.Dependency, error) {
	parser, err := getParser(registryType, log)
	if err != nil {
		return nil, err
	}
	return parser.ParseDependencies(filePath, options)
}

func getParser(registryType shared.RegistryType, log shared.LogFunc) (shared.Parser, error) {
	switch registryType {
	case shared.NPM:
		parser := npm.NewParser()
		parser.Log = log
		return parser, nil
	case shared.Pub:
		parser := pub.NewParser()
		parser.Log = log
		return parser, nil
	default:
		return nil, fmt.Errorf("%w: %d", shared.ErrUnsupportedRegistryType, registryType)
	}
}
