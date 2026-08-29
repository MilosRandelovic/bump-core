package dependency

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

// Selector identifies dependencies by package name, dependency-file section, file path, or any combination of those fields.
// Multiple selectors are combined as a union, while fields within one selector must all match.
type Selector struct {
	Name     string `json:"name,omitempty" jsonschema:"Exact package name to select."`
	Type     string `json:"type,omitempty" jsonschema:"Dependency-file section: dependencies, devDependencies, or peerDependencies."`
	FilePath string `json:"filePath,omitempty" jsonschema:"Absolute file path or path relative to the checked project directory."`
}

type preparedSelector struct {
	selector       Selector
	dependencyType *shared.DependencyType
	filePath       string
	matched        bool
}

// Filter returns dependencies matched by selectors in their original order.
// An empty selector list returns all dependencies, and invalid or unmatched selectors return an error.
func Filter(dependencies []shared.Dependency, selectors []Selector, baseDirectory string) ([]shared.Dependency, error) {
	if len(selectors) == 0 {
		return dependencies, nil
	}

	prepared := make([]preparedSelector, 0, len(selectors))
	for index, selector := range selectors {
		preparedTarget, err := prepareSelector(selector, baseDirectory)
		if err != nil {
			return nil, fmt.Errorf("invalid target %d: %w", index+1, err)
		}
		prepared = append(prepared, preparedTarget)
	}

	selected := make([]shared.Dependency, 0, len(dependencies))
	for _, dependency := range dependencies {
		matched := false
		for index := range prepared {
			if selectorMatches(dependency, prepared[index], baseDirectory) {
				prepared[index].matched = true
				matched = true
			}
		}
		if matched {
			selected = append(selected, dependency)
		}
	}

	for index, selector := range prepared {
		if !selector.matched {
			return nil, fmt.Errorf("target %d matched no dependencies", index+1)
		}
	}
	return selected, nil
}

func prepareSelector(selector Selector, baseDirectory string) (preparedSelector, error) {
	selector.Name = strings.TrimSpace(selector.Name)
	selector.Type = strings.TrimSpace(selector.Type)
	selector.FilePath = strings.TrimSpace(selector.FilePath)
	if selector.Name == "" && selector.Type == "" && selector.FilePath == "" {
		return preparedSelector{}, fmt.Errorf("at least one of name, type, or filePath is required")
	}

	prepared := preparedSelector{selector: selector}
	if selector.Type != "" {
		dependencyType, err := parseDependencyType(selector.Type)
		if err != nil {
			return preparedSelector{}, err
		}
		prepared.dependencyType = &dependencyType
	}
	if selector.FilePath != "" {
		filePath, err := normalizePath(selector.FilePath, baseDirectory)
		if err != nil {
			return preparedSelector{}, fmt.Errorf("failed to resolve filePath: %w", err)
		}
		prepared.filePath = filePath
	}
	return prepared, nil
}

func selectorMatches(dependency shared.Dependency, selector preparedSelector, baseDirectory string) bool {
	if selector.selector.Name != "" && dependency.Name != selector.selector.Name {
		return false
	}
	if selector.dependencyType != nil && dependency.Type != *selector.dependencyType {
		return false
	}
	if selector.filePath != "" {
		dependencyPath, err := normalizePath(dependency.FilePath, baseDirectory)
		if err != nil || dependencyPath != selector.filePath {
			return false
		}
	}
	return true
}

func normalizePath(filePath, baseDirectory string) (string, error) {
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(baseDirectory, filePath)
	}
	return filepath.Abs(filePath)
}

func parseDependencyType(value string) (shared.DependencyType, error) {
	switch value {
	case "dependencies":
		return shared.Dependencies, nil
	case "devDependencies":
		return shared.DevDependencies, nil
	case "peerDependencies":
		return shared.PeerDependencies, nil
	default:
		return 0, fmt.Errorf("unsupported dependency type %q", value)
	}
}
