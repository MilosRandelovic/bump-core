package shared

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Version is the single source of truth for the bump version across all repos
const Version = "2.1.0"

var (
	versionPrefixCaptureRegex = regexp.MustCompile(`^([\^~>=<]+)`)
	versionTokenRegex         = regexp.MustCompile(`[vV]?\d+(?:\.\d+){0,2}(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?`)
)

// CleanVersion extracts the first version from a version or constraint string.
// For example, both "^1.2.3" and ">=1.2.3 <2.0.0" return "1.2.3".
func CleanVersion(version string) string {
	if match := versionTokenRegex.FindString(version); match != "" {
		return strings.TrimPrefix(strings.TrimPrefix(match, "v"), "V")
	}
	return versionPrefixCaptureRegex.ReplaceAllString(version, "")
}

// UpdateVersionConstraint replaces the first version token while preserving the
// original constraint operators and any remaining range clauses.
func UpdateVersionConstraint(constraint, newVersion string) string {
	location := matchingConstraintVersionLocation(constraint, newVersion)
	if location == nil {
		location = versionTokenRegex.FindStringIndex(constraint)
	}
	if location == nil {
		return GetVersionPrefix(constraint) + newVersion
	}
	return constraint[:location[0]] + newVersion + constraint[location[1]:]
}

func matchingConstraintVersionLocation(constraint, version string) []int {
	parsedVersion, err := semver.NewVersion(version)
	if err != nil {
		return nil
	}

	offset := 0
	for _, clause := range strings.Split(constraint, "||") {
		trimmedClause := strings.TrimSpace(clause)
		clauseConstraint, err := semver.NewConstraint(trimmedClause)
		if err == nil && clauseConstraint.Check(parsedVersion) {
			location := versionTokenRegex.FindStringIndex(clause)
			if location != nil {
				return []int{offset + location[0], offset + location[1]}
			}
		}
		offset += len(clause) + len("||")
	}
	return nil
}

// HasSemanticPrefix reports whether every version clause starts with a supported semantic-version operator.
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

// GetVersionPrefix returns the leading supported constraint operator, or an empty string for an exact version.
func GetVersionPrefix(version string) string {
	matches := versionPrefixCaptureRegex.FindStringSubmatch(version)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// FindBothLatestVersions returns the newest valid version and the newest version satisfying constraint.
// Invalid versions are ignored; prereleases are considered only when the constraint references one.
// If no version satisfies constraint, absoluteLatest is still returned with ErrNoVersionsSatisfyConstraint.
func FindBothLatestVersions(versions []string, constraint string) (absoluteLatest string, constraintLatest string, err error) {
	if len(versions) == 0 {
		return "", "", fmt.Errorf("no versions provided")
	}

	// Use the first version token as the current/reference version. A constraint
	// is not itself a version, so stripping only its leading operator breaks
	// compound ranges such as ">=1.0.0 <2.0.0".
	currentVersion := CleanVersion(constraint)
	// CleanVersion selects the reference version without treating a compound range as a version.
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
	for _, versionToken := range versionTokenRegex.FindAllString(constraint, -1) {
		parsedToken, parseErr := semver.NewVersion(strings.TrimPrefix(strings.TrimPrefix(versionToken, "v"), "V"))
		if parseErr == nil && parsedToken.Prerelease() != "" {
			includePrerelease = true
			break
		}
	}

	// Find absolute latest (stable or prerelease depending on current version)
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
	effectiveConstraint := constraint
	if GetVersionPrefix(constraint) == "" {
		// No prefix means exact version, we want newer versions
		effectiveConstraint = ">" + currentVersion
	}

	parsedConstraint, err := semver.NewConstraint(effectiveConstraint)
	if err != nil {
		return absoluteLatest, "", fmt.Errorf("invalid constraint: %s", effectiveConstraint)
	}

	// Set IncludePrerelease based on current version
	parsedConstraint.IncludePrerelease = includePrerelease

	// Find latest satisfying constraint (iterate from end since collection is sorted)
	for i := len(collection) - 1; i >= 0; i-- {
		if parsedConstraint.Check(collection[i]) {
			constraintLatest = versionMap[collection[i].String()]
			break
		}
	}

	if constraintLatest == "" {
		return absoluteLatest, "", fmt.Errorf("%w: %s", ErrNoVersionsSatisfyConstraint, constraint)
	}

	return absoluteLatest, constraintLatest, nil
}

// GetSemverChange classifies an upgrade as major, minor, or patch.
// Invalid versions, equal versions, and downgrades fall back to PatchChange.
func GetSemverChange(currentVer, latestVer string) SemverChange {
	current, currentErr := semver.NewVersion(CleanVersion(currentVer))
	latest, latestErr := semver.NewVersion(CleanVersion(latestVer))

	if currentErr != nil || latestErr != nil {
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
