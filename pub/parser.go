package pub

import (
	"fmt"
	"os"
	"strings"

	"github.com/MilosRandelovic/bump-core/v2/shared"
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

// NewParser returns a Pub parser. Set Parser.Log to receive optional diagnostics.
func NewParser() *Parser {
	return &Parser{}
}

// ParseDependencies reads registry-backed dependencies and dev dependencies from a pubspec.yaml file.
// SDK, path, Git, and unconstrained dependencies are excluded.
func (parser *Parser) ParseDependencies(filePath string, options shared.Options) ([]shared.Dependency, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	var dependencies []shared.Dependency

	var currentSection shared.DependencyType
	var inSection bool
	var currentPackage *packageInfo
	sectionIndent := -1
	packageIndent := -1
	sectionEntries := 0

	finalizePackage := func() {
		if currentPackage == nil {
			return
		}
		if dependency := currentPackage.toDependency(currentSection, filePath); dependency != nil {
			dependencies = append(dependencies, *dependency)
		}
		currentPackage = nil
	}
	finalizeSection := func() {
		finalizePackage()
		if inSection && sectionEntries == 0 {
			parser.log("Warning: %s section contains no dependency entries\n", currentSection.String())
		}
		inSection = false
		sectionIndent = -1
		packageIndent = -1
		sectionEntries = 0
	}

	for lineNumber, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		indent := getIndentation(line)
		sectionLine := strings.TrimSpace(stripInlineComment(trimmedLine))

		if sectionLine == "dependencies:" {
			finalizeSection()
			currentSection = shared.Dependencies
			inSection = true
			sectionIndent = indent
			continue
		} else if sectionLine == "dev_dependencies:" {
			finalizeSection()
			currentSection = shared.DevDependencies
			inSection = true
			sectionIndent = indent
			continue
		}

		if !inSection || trimmedLine == "" || strings.HasPrefix(trimmedLine, "#") {
			continue
		}

		if indent <= sectionIndent {
			finalizeSection()
			continue
		}

		parts := strings.SplitN(trimmedLine, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		if packageIndent == -1 || indent < packageIndent {
			packageIndent = indent
		}
		if indent == packageIndent {
			finalizePackage()
			sectionEntries++
			currentPackage = &packageInfo{name: key, lineNumber: lineNumber + 1}
			if value != "" {
				currentPackage.version = cleanQuotes(value)
			}
			continue
		}

		if currentPackage != nil && indent > packageIndent {
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
				if currentPackage.inHostedBlock {
					currentPackage.hostedURL = cleanQuotes(value)
				}
			case "sdk":
				currentPackage.sdk = value
			}
		}
	}

	finalizeSection()

	return dependencies, nil
}

type packageInfo struct {
	name              string
	version           string
	hostedURL         string
	inHostedBlock     bool
	sdk               string
	lineNumber        int
	versionLineNumber int
}

func (info *packageInfo) toDependency(section shared.DependencyType, filePath string) *shared.Dependency {

	if info.sdk != "" {
		return nil
	}

	if !shouldIncludeDependency(info.name, info.version) {
		return nil
	}

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

	if info.hostedURL != "" && !strings.Contains(info.hostedURL, "pub.dev") {
		dependency.HostedURL = info.hostedURL
	}

	return dependency
}

func cleanQuotes(s string) string {
	s = strings.TrimSpace(stripInlineComment(s))
	if (strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`)) ||
		(strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`)) {
		return s[1 : len(s)-1]
	}
	return s
}

func stripInlineComment(value string) string {
	var quote byte
	for index := 0; index < len(value); index++ {
		character := value[index]
		if quote != 0 {
			if character == quote && (index == 0 || value[index-1] != '\\') {
				quote = 0
			}
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			continue
		}
		if character == '#' && (index == 0 || value[index-1] == ' ' || value[index-1] == '\t') {
			return strings.TrimSpace(value[:index])
		}
	}
	return value
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

func shouldIncludeDependency(name, version string) bool {

	if name == "flutter" {
		return false
	}

	if version == "" {
		return false
	}

	if version == "any" || strings.HasPrefix(version, "sdk:") || strings.HasPrefix(version, "path:") || strings.HasPrefix(version, "git:") {
		return false
	}

	return true
}

var _ shared.Parser = (*Parser)(nil)
