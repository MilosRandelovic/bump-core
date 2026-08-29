package shared

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// PreparedFileUpdate is a validated dependency file edit that has not yet been written.
type PreparedFileUpdate struct {
	filePath string
	content  []byte
	mode     fs.FileMode
}

// PrepareDependenciesInFile validates and renders every requested edit without modifying the file.
// It resolves symlink targets, preserves file mode, rejects hard links and stale source locations, and returns nil for no edits.
func PrepareDependenciesInFile(filePath string, outdated []OutdatedDependency, patternProvider PatternProvider) (*PreparedFileUpdate, error) {
	// If no dependencies to update, return early
	if len(outdated) == 0 {
		return nil, nil
	}

	writePath := filePath
	linkInfo, err := os.Lstat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		writePath, err = filepath.EvalSymlinks(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve dependency file symlink: %w", err)
		}
	}
	info, err := os.Stat(writePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	if fileLinkCount(info) > 1 {
		return nil, fmt.Errorf("refusing to atomically replace hard-linked dependency file %s", filePath)
	}
	data, err := os.ReadFile(writePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Split content into lines
	lines := strings.Split(string(data), "\n")

	// Update each dependency by modifying its specific line
	for _, dependency := range outdated {
		if dependency.LineNumber < 1 || dependency.LineNumber > len(lines) {
			return nil, fmt.Errorf("invalid line number %d for dependency %s", dependency.LineNumber, dependency.Name)
		}

		lineIndex := dependency.LineNumber - 1 // Convert to 0-based index
		line := lines[lineIndex]

		// Get the pattern from the provider
		pattern := patternProvider.GetPattern(dependency)
		versionRegex, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid update pattern for dependency %s: %w", dependency.Name, err)
		}
		allMatches := versionRegex.FindAllStringSubmatchIndex(line, -1)
		if len(allMatches) == 0 {
			return nil, fmt.Errorf("could not find %s on line %d for updating", dependency.Name, dependency.LineNumber)
		}

		selectedMatch := allMatches[0]
		if dependency.OriginalVersion != "" {
			selectedMatch = nil
			for _, match := range allMatches {
				if len(match) >= 6 && match[4] >= 0 && line[match[4]:match[5]] == dependency.OriginalVersion {
					selectedMatch = match
					break
				}
			}
			if selectedMatch == nil {
				foundVersion := ""
				if first := allMatches[0]; len(first) >= 6 && first[4] >= 0 {
					foundVersion = line[first[4]:first[5]]
				}
				return nil, fmt.Errorf("dependency %s changed on line %d: expected version %q, found %q", dependency.Name, dependency.LineNumber, dependency.OriginalVersion, foundVersion)
			}
		}
		if len(selectedMatch) < 6 || selectedMatch[4] < 0 {
			return nil, fmt.Errorf("invalid update pattern for dependency %s", dependency.Name)
		}
		oldVersion := line[selectedMatch[4]:selectedMatch[5]]
		newVersion := UpdateVersionConstraint(oldVersion, dependency.LatestVersion)
		latestVersion, err := semver.NewVersion(dependency.LatestVersion)
		if err != nil {
			return nil, fmt.Errorf("invalid latest version %q for dependency %s", dependency.LatestVersion, dependency.Name)
		}
		updatedConstraint, err := semver.NewConstraint(newVersion)
		if err != nil {
			return nil, fmt.Errorf("updating dependency %s would produce invalid constraint %q: %w", dependency.Name, newVersion, err)
		}
		if !updatedConstraint.Check(latestVersion) {
			return nil, fmt.Errorf("latest version %s does not satisfy updated constraint %q for dependency %s", dependency.LatestVersion, newVersion, dependency.Name)
		}

		// Get the replacement string from the provider
		replacement := patternProvider.GetReplacement(dependency, newVersion)
		expandedReplacement := versionRegex.ExpandString(nil, replacement, line, selectedMatch)
		newLine := line[:selectedMatch[0]] + string(expandedReplacement) + line[selectedMatch[1]:]
		lines[lineIndex] = newLine
	}

	return &PreparedFileUpdate{
		filePath: writePath,
		content:  []byte(strings.Join(lines, "\n")),
		mode:     info.Mode(),
	}, nil
}

func fileLinkCount(info fs.FileInfo) uint64 {
	value := reflect.ValueOf(info.Sys())
	if !value.IsValid() {
		return 1
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 1
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return 1
	}
	field := value.FieldByName("Nlink")
	if !field.IsValid() {
		return 1
	}
	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if links := field.Uint(); links > 0 {
			return links
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if links := field.Int(); links > 0 {
			return uint64(links)
		}
	}
	return 1
}

// Apply atomically replaces the prepared target while preserving its file mode.
// Validation is not repeated, so callers must hold any transaction lock from preparation through Apply.
func (update *PreparedFileUpdate) Apply() error {
	temporaryFile, err := os.CreateTemp(filepath.Dir(update.filePath), "."+filepath.Base(update.filePath)+".bump-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary update file: %w", err)
	}
	temporaryPath := temporaryFile.Name()
	defer os.Remove(temporaryPath)

	if err := temporaryFile.Chmod(update.mode); err != nil {
		temporaryFile.Close()
		return fmt.Errorf("failed to preserve file permissions: %w", err)
	}
	if _, err := temporaryFile.Write(update.content); err != nil {
		temporaryFile.Close()
		return fmt.Errorf("failed to write temporary update file: %w", err)
	}
	if err := temporaryFile.Sync(); err != nil {
		temporaryFile.Close()
		return fmt.Errorf("failed to sync temporary update file: %w", err)
	}
	if err := temporaryFile.Close(); err != nil {
		return fmt.Errorf("failed to close temporary update file: %w", err)
	}
	if err := os.Rename(temporaryPath, update.filePath); err != nil {
		return fmt.Errorf("failed to replace dependency file: %w", err)
	}

	return nil
}
