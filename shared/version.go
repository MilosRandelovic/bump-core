package shared

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Version is the single source of truth for the bump version across all repos
const Version = "2.0.0"

var versionPrefixCaptureRegex = regexp.MustCompile(`^([\^~>=<]+)`)

// CleanVersion removes prefix characters (^, ~, >=, etc.) from version strings
func CleanVersion(version string) string {
	return versionPrefixCaptureRegex.ReplaceAllString(version, "")
}

// HasSemanticPrefix checks if version has semantic versioning prefix
func HasSemanticPrefix(version string) bool {
	if version == "" {
		return false
	}

	// Common semantic versioning prefixes
	prefixes := []string{"^", "~", ">=", ">", "<=", "<"}
	hasPrefix := false
	for _, prefix := range prefixes {
		if strings.HasPrefix(version, prefix) {
			hasPrefix = true
			break
		}
	}

	// If it doesn't start with a semantic prefix, it's not semantic
	if !hasPrefix {
		return false
	}

	// Check if it contains mixed semantic and non-semantic parts
	// Split by spaces and check if there are multiple parts
	parts := strings.Fields(version)
	if len(parts) > 1 {
		// For multiple parts, all parts should have semantic prefixes or be range operators
		for _, part := range parts {
			// Skip range operators
			if part == "&&" || part == "||" {
				continue
			}

			partHasPrefix := false
			for _, prefix := range prefixes {
				if strings.HasPrefix(part, prefix) {
					partHasPrefix = true
					break
				}
			}

			// If any part doesn't have a semantic prefix, it's mixed
			if !partHasPrefix {
				return false
			}
		}
	}

	return true
}

// GetVersionPrefix returns the prefix of a version string (^, ~, >=, etc.)
func GetVersionPrefix(version string) string {
	matches := versionPrefixCaptureRegex.FindStringSubmatch(version)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// FindBothLatestVersions finds both the absolute latest version and the latest version satisfying a constraint
// Returns (absoluteLatest, constraintLatest, error)
func FindBothLatestVersions(versions []string, constraint string) (string, string, error) {
	if len(versions) == 0 {
		return "", "", fmt.Errorf("no versions provided")
	}

	// Check if the current version (from constraint) is a pre-release
	currentVersion := CleanVersion(constraint)
	currentSemver, err := semver.NewVersion(currentVersion)
	if err != nil {
		return "", "", fmt.Errorf("invalid current version: %s", currentVersion)
	}

	// Parse versions using semver and build map from semver string to original
	var collection semver.Collection
	versionMap := make(map[string]string) // semver string -> original string

	for _, versionString := range versions {
		parsedVersion, err := semver.NewVersion(versionString)
		if err != nil {
			// Skip invalid versions
			continue
		}
		collection = append(collection, parsedVersion)
		versionMap[parsedVersion.String()] = versionString
	}

	if len(collection) == 0 {
		return "", "", fmt.Errorf("no valid semver versions found")
	}

	// Sort versions using semver.Collection's built-in sort
	sort.Sort(collection)

	// Determine if we should include prereleases based on current version
	includePrerelease := currentSemver.Prerelease() != ""

	// Find absolute latest (stable or prerelease depending on current version)
	var absoluteLatest string
	for i := len(collection) - 1; i >= 0; i-- {
		if includePrerelease || collection[i].Prerelease() == "" {
			absoluteLatest = versionMap[collection[i].String()]
			break
		}
	}

	if absoluteLatest == "" {
		if includePrerelease {
			return "", "", fmt.Errorf("no versions available")
		}
		return "", "", fmt.Errorf("no stable versions available")
	}

	// Parse the constraint
	constraintStr := constraint
	if GetVersionPrefix(constraint) == "" {
		// No prefix means exact version, we want newer versions
		constraintStr = ">" + currentVersion
	}

	constraintObj, err := semver.NewConstraint(constraintStr)
	if err != nil {
		return absoluteLatest, "", fmt.Errorf("invalid constraint: %s", constraintStr)
	}

	// Set IncludePrerelease based on current version
	constraintObj.IncludePrerelease = includePrerelease

	// Find latest satisfying constraint (iterate from end since collection is sorted)
	var constraintLatest string
	for i := len(collection) - 1; i >= 0; i-- {
		if constraintObj.Check(collection[i]) {
			constraintLatest = versionMap[collection[i].String()]
			break
		}
	}

	if constraintLatest == "" {
		return absoluteLatest, "", fmt.Errorf("%w: %s", ErrNoVersionsSatisfyConstraint, constraint)
	}

	return absoluteLatest, constraintLatest, nil
}

// GetSemverChange determines the type of version change (major, minor, patch)
func GetSemverChange(currentVer, latestVer string) SemverChange {
	current, err1 := semver.NewVersion(CleanVersion(currentVer))
	latest, err2 := semver.NewVersion(CleanVersion(latestVer))

	if err1 != nil || err2 != nil {
		return PatchChange
	}

	// If the latest version is less than or equal to current, default to patch
	if !latest.GreaterThan(current) {
		return PatchChange
	}

	if latest.Major() != current.Major() {
		return MajorChange
	} else if latest.Minor() != current.Minor() {
		return MinorChange
	} else if latest.Patch() != current.Patch() {
		return PatchChange
	}

	return PatchChange
}
