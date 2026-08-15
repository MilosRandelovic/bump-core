package pub

import (
	"fmt"
	"os"
	"strings"

	"github.com/MilosRandelovic/bump-core/shared"
)

// Parser handles Dart pubspec.yaml parsing
type Parser struct {
	Log shared.LogFunc
}

func (parser *Parser) log(format string, args ...any) {
	if parser.Log != nil {
		parser.Log(format, args...)
	}
}

// NewParser creates a new Dart parser
func NewParser() *Parser {
	return &Parser{}
}

// ParseDependencies parses a pubspec.yaml file and extracts dependencies
func (parser *Parser) ParseDependencies(filePath string, options shared.Options) ([]shared.Dependency, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	var dependencies []shared.Dependency

	// Track which section we're in
	var currentSection shared.DependencyType
	var inSection bool
	var currentPackage *packageInfo

	for lineNumber, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		indent := getIndentation(line)

		// Check if we're entering a dependency section
		if strings.HasPrefix(trimmedLine, "dependencies:") {
			// Finalize any pending package from previous section
			if currentPackage != nil {
				if dependency := currentPackage.toDependency(currentSection, filePath); dependency != nil {
					dependencies = append(dependencies, *dependency)
				}
			}
			currentSection = shared.Dependencies
			inSection = true
			currentPackage = nil
			continue
		} else if strings.HasPrefix(trimmedLine, "dev_dependencies:") {
			// Finalize any pending package from previous section
			if currentPackage != nil {
				if dependency := currentPackage.toDependency(currentSection, filePath); dependency != nil {
					dependencies = append(dependencies, *dependency)
				}
			}
			currentSection = shared.DevDependencies
			inSection = true
			currentPackage = nil
			continue
		}

		// Check if we're leaving a section (non-indented line that's not a comment)
		if inSection && len(line) > 0 && line[0] != ' ' && line[0] != '\t' && trimmedLine != "" && !strings.HasPrefix(trimmedLine, "#") {
			// Finalize any pending package
			if currentPackage != nil {
				if dependency := currentPackage.toDependency(currentSection, filePath); dependency != nil {
					dependencies = append(dependencies, *dependency)
				}
				currentPackage = nil
			}
			inSection = false
		}

		// If we're in a section, look for dependency definitions
		if inSection && len(line) > 0 && (line[0] == ' ' || line[0] == '\t') && !strings.HasPrefix(trimmedLine, "#") && strings.Contains(trimmedLine, ":") {
			parts := strings.SplitN(trimmedLine, ":", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])

				// Check if this is a top-level package name (2 spaces indentation)
				if indent == 2 {
					// Finalize previous package if any
					if currentPackage != nil {
						if dependency := currentPackage.toDependency(currentSection, filePath); dependency != nil {
							dependencies = append(dependencies, *dependency)
						}
					}

					// Start new package
					currentPackage = &packageInfo{
						name:       key,
						lineNumber: lineNumber + 1,
					}

					if value != "" {
						// Simple dependency (name: version)
						currentPackage.version = cleanQuotes(value)
					}
				} else if indent >= 4 {
					// This is a sub-property of the current package (4+ spaces indentation)
					if currentPackage != nil {
						switch key {
						case "version":
							currentPackage.version = cleanQuotes(value)
							currentPackage.versionLineNumber = lineNumber + 1
						case "hosted":
							currentPackage.inHostedBlock = value == ""
							if value != "" {
								currentPackage.hostedURL = cleanQuotes(value)
							}
						case "url":
							if currentPackage.inHostedBlock && indent >= 6 {
								currentPackage.hostedURL = cleanQuotes(value)
							}
						case "sdk":
							currentPackage.sdk = value
						}
					}
				}
			}
		}
	}

	// Finalize any pending package
	if currentPackage != nil {
		if dependency := currentPackage.toDependency(currentSection, filePath); dependency != nil {
			dependencies = append(dependencies, *dependency)
		}
	}

	return dependencies, nil
}

// packageInfo holds information about a package being parsed
type packageInfo struct {
	name              string
	version           string
	hostedURL         string
	inHostedBlock     bool
	sdk               string
	lineNumber        int
	versionLineNumber int
}

// toDependency converts packageInfo to shared.Dependency if it should be included
func (info *packageInfo) toDependency(section shared.DependencyType, filePath string) *shared.Dependency {
	// Skip SDK dependencies
	if info.sdk != "" {
		return nil
	}

	if !shouldIncludeDependency(info.name, info.version) {
		return nil
	}

	// Use version line number if available, otherwise use package line number
	effectiveLineNumber := info.lineNumber
	if info.versionLineNumber > 0 {
		effectiveLineNumber = info.versionLineNumber
	}

	dependency := &shared.Dependency{
		BaseDependency: shared.BaseDependency{
			Name:            info.name,
			OriginalVersion: info.version,
			Type:            section,
			FilePath:        filePath,
			LineNumber:      effectiveLineNumber,
		},
		Version: shared.CleanVersion(info.version),
	}

	// Set hosted URL for non-pub.dev hosted packages
	if info.hostedURL != "" && !strings.Contains(info.hostedURL, "pub.dev") {
		dependency.HostedURL = info.hostedURL
	}

	return dependency
}

// cleanQuotes removes surrounding quotes from a string
func cleanQuotes(s string) string {
	s = strings.TrimSpace(s)
	if (strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`)) ||
		(strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`)) {
		return s[1 : len(s)-1]
	}
	return s
}

func getIndentation(line string) int {
	indent := 0
	for _, character := range line {
		if character == ' ' {
			indent++
			continue
		}
		if character == '\t' {
			indent += 2
			continue
		}
		break
	}
	return indent
}

// shouldIncludeDependency checks if a dependency should be included
func shouldIncludeDependency(name, version string) bool {
	// Skip flutter SDK dependency
	if name == "flutter" {
		return false
	}

	// Skip if no version
	if version == "" {
		return false
	}

	// Skip 'any' versions, SDK dependencies, path, git dependencies
	if version == "any" || strings.HasPrefix(version, "sdk:") || strings.HasPrefix(version, "path:") || strings.HasPrefix(version, "git:") {
		return false
	}

	return true
}

// GetRegistryType returns the registry type this parser handles
func (parser *Parser) GetRegistryType() shared.RegistryType {
	return shared.Pub
}

// Ensure Parser implements the interface
var _ shared.Parser = (*Parser)(nil)
