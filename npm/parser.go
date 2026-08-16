package npm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

// Parser handles npm package.json parsing
type Parser struct {
	Log shared.LogFunc
}

type packageManifest struct {
	Workspaces       json.RawMessage   `json:"workspaces"`
	Dependencies     map[string]string `json:"dependencies"`
	DevDependencies  map[string]string `json:"devDependencies"`
	PeerDependencies map[string]string `json:"peerDependencies"`
}

// NewParser returns an npm parser. Set Parser.Log to receive optional diagnostics.
func NewParser() *Parser {
	return &Parser{}
}

// ParseDependencies reads package.json dependencies selected by options.
// In monorepo mode it also parses workspace manifests and logs malformed or missing workspace packages when Log is set.
func (parser *Parser) ParseDependencies(filePath string, options shared.Options) ([]shared.Dependency, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var packageData packageManifest
	if err := json.Unmarshal(data, &packageData); err != nil {
		return nil, fmt.Errorf("failed to parse package.json: %w", err)
	}

	if options.Monorepo {
		workspacePatterns, err := extractWorkspacePatterns(packageData.Workspaces)
		if err != nil {
			parser.log("Warning: invalid workspaces format in %s: %v\n", filePath, err)
			return parser.parseManifest(filePath, data, packageData, options), nil
		}

		if len(workspacePatterns) > 0 {
			return parser.parseWorkspaces(filePath, data, packageData, workspacePatterns, options)
		}
	}

	return parser.parseManifest(filePath, data, packageData, options), nil
}

func (parser *Parser) log(format string, args ...any) {
	if parser.Log != nil {
		parser.Log(format, args...)
	}
}

func (parser *Parser) parseFile(filePath string, data []byte, options shared.Options) ([]shared.Dependency, error) {
	var manifest packageManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse package.json: %w", err)
	}
	return parser.parseManifest(filePath, data, manifest, options), nil
}

func (parser *Parser) parseManifest(filePath string, data []byte, manifest packageManifest, options shared.Options) []shared.Dependency {
	// Parse line by line to track line numbers and extract dependencies
	lines := strings.Split(string(data), "\n")
	var dependencies []shared.Dependency

	// Track which section we're in
	var currentSection shared.DependencyType
	var inSection bool

	for lineNumber, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Check if we're entering a dependency section
		if strings.Contains(trimmedLine, `"dependencies"`) && strings.Contains(trimmedLine, `:`) {
			currentSection = shared.Dependencies
			inSection = true
			continue
		} else if strings.Contains(trimmedLine, `"devDependencies"`) && strings.Contains(trimmedLine, `:`) {
			currentSection = shared.DevDependencies
			inSection = true
			continue
		} else if strings.Contains(trimmedLine, `"peerDependencies"`) && strings.Contains(trimmedLine, `:`) {
			if options.IncludePeerDependencies {
				currentSection = shared.PeerDependencies
				inSection = true
			}
			continue
		}

		// Check if we're leaving a section (closing brace or comma)
		if inSection && (trimmedLine == "}" || trimmedLine == "},") {
			inSection = false
			continue
		}

		// If we're in a section, look for dependency definitions
		if inSection {
			// Look for lines like: "package-name": "version",
			if strings.Contains(trimmedLine, `"`) && strings.Contains(trimmedLine, `:`) {
				// Parse the dependency name and version
				parts := strings.SplitN(trimmedLine, ":", 2)
				if len(parts) == 2 {
					// Extract package name (remove quotes and whitespace)
					nameString := strings.TrimSpace(parts[0])
					nameString = strings.Trim(nameString, `"`)

					// Extract version (remove quotes, whitespace, and trailing comma)
					versionString := strings.TrimSpace(parts[1])
					versionString = strings.Trim(versionString, `",`)
					versionString = strings.Trim(versionString, `"`)

					// Basic validation - skip empty names or versions
					if nameString != "" && versionString != "" {
						dependencies = append(dependencies, shared.Dependency{
							BaseDependency: shared.BaseDependency{
								Name:            nameString,
								OriginalVersion: versionString,
								Type:            currentSection,
								FilePath:        filePath,
								LineNumber:      lineNumber + 1, // Convert to 1-based
							},
							Version: shared.CleanVersion(versionString),
						})
					}
				}
			}
		}
	}

	dependencies = filterManifestDependencies(dependencies, manifest, options)
	return appendMissingManifestDependencies(dependencies, filePath, data, manifest, options)
}

func (parser *Parser) parseWorkspaces(rootPath string, rootData []byte, rootManifest packageManifest, patterns []string, options shared.Options) ([]shared.Dependency, error) {
	rootDir := filepath.Dir(rootPath)
	all := []shared.Dependency{}

	root := parser.parseManifest(rootPath, rootData, rootManifest, options)
	all = append(all, root...)

	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "!") {
			parser.log("Warning: workspace negation pattern %q is not supported and was skipped\n", pattern)
			continue
		}

		matches, err := filepath.Glob(filepath.Join(rootDir, pattern))
		if err != nil {
			parser.log("Warning: invalid workspace glob pattern %q: %v\n", pattern, err)
			continue
		}
		if len(matches) == 0 {
			parser.log("Warning: workspace pattern %q matched no directories\n", pattern)
			continue
		}

		sort.Strings(matches)
		for _, match := range matches {
			if info, err := os.Stat(match); err == nil && info.IsDir() {
				packagePath := filepath.Join(match, "package.json")
				if _, err := os.Stat(packagePath); err == nil {
					workspaceData, err := os.ReadFile(packagePath)
					if err != nil {
						parser.log("Warning: failed to read workspace package %s: %v\n", packagePath, err)
					} else if fileDependencies, err := parser.parseFile(packagePath, workspaceData, options); err == nil {
						all = append(all, fileDependencies...)
					} else {
						parser.log("Warning: failed to parse workspace package %s: %v\n", packagePath, err)
					}
				} else {
					parser.log("Warning: workspace directory %s has no package.json\n", match)
				}
			}
		}
	}

	return all, nil
}

func filterManifestDependencies(dependencies []shared.Dependency, manifest packageManifest, options shared.Options) []shared.Dependency {
	filtered := dependencies[:0]
	for _, dependency := range dependencies {
		versions := manifestVersions(manifest, dependency.Type, options)
		if version, exists := versions[dependency.Name]; exists && version == dependency.OriginalVersion {
			filtered = append(filtered, dependency)
		}
	}
	return filtered
}

func appendMissingManifestDependencies(dependencies []shared.Dependency, filePath string, data []byte, manifest packageManifest, options shared.Options) []shared.Dependency {
	type positionedDependency struct {
		dependency shared.Dependency
		position   int
	}

	seen := make(map[string]struct{}, len(dependencies))
	for _, dependency := range dependencies {
		seen[fmt.Sprintf("%d:%s", dependency.Type, dependency.Name)] = struct{}{}
	}

	var missing []positionedDependency
	sections := []struct {
		dependencyType shared.DependencyType
		versions       map[string]string
	}{
		{shared.Dependencies, manifest.Dependencies},
		{shared.DevDependencies, manifest.DevDependencies},
	}
	if options.IncludePeerDependencies {
		sections = append(sections, struct {
			dependencyType shared.DependencyType
			versions       map[string]string
		}{shared.PeerDependencies, manifest.PeerDependencies})
	}

	for _, section := range sections {
		for name, version := range section.versions {
			key := fmt.Sprintf("%d:%s", section.dependencyType, name)
			if _, exists := seen[key]; exists {
				continue
			}

			encodedName := strconv.Quote(name)
			encodedVersion := strconv.Quote(version)
			pattern := regexp.MustCompile(regexp.QuoteMeta(encodedName) + `\s*:\s*` + regexp.QuoteMeta(encodedVersion))
			location := pattern.FindIndex(data)
			if location == nil {
				continue
			}
			missing = append(missing, positionedDependency{
				position: location[0],
				dependency: shared.Dependency{
					BaseDependency: shared.BaseDependency{
						Name:            name,
						OriginalVersion: version,
						Type:            section.dependencyType,
						FilePath:        filePath,
						LineNumber:      bytes.Count(data[:location[0]], []byte("\n")) + 1,
					},
					Version: shared.CleanVersion(version),
				},
			})
		}
	}

	sort.Slice(missing, func(first, second int) bool {
		if missing[first].position != missing[second].position {
			return missing[first].position < missing[second].position
		}
		if missing[first].dependency.Type != missing[second].dependency.Type {
			return missing[first].dependency.Type < missing[second].dependency.Type
		}
		return missing[first].dependency.Name < missing[second].dependency.Name
	})
	for _, positioned := range missing {
		dependencies = append(dependencies, positioned.dependency)
	}
	return dependencies
}

func manifestVersions(manifest packageManifest, dependencyType shared.DependencyType, options shared.Options) map[string]string {
	switch dependencyType {
	case shared.Dependencies:
		return manifest.Dependencies
	case shared.DevDependencies:
		return manifest.DevDependencies
	case shared.PeerDependencies:
		if options.IncludePeerDependencies {
			return manifest.PeerDependencies
		}
	}
	return nil
}

func extractWorkspacePatterns(workspacesRaw json.RawMessage) ([]string, error) {
	if len(workspacesRaw) == 0 || string(workspacesRaw) == "null" {
		return nil, nil
	}

	var asArray []string
	if err := json.Unmarshal(workspacesRaw, &asArray); err == nil {
		return asArray, nil
	}

	var asObject struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(workspacesRaw, &asObject); err == nil {
		return asObject.Packages, nil
	}

	return nil, fmt.Errorf("expected an array of strings or object with packages field")
}

// Ensure Parser implements the interface
var _ shared.Parser = (*Parser)(nil)
