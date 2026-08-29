package shared

import (
	"sort"
	"time"
)

// MinimumReleaseAge is the fixed age a release must exceed when age filtering is enabled.
const MinimumReleaseAge = 24 * time.Hour

// PublishedVersion associates a package version with its registry publication timestamp.
type PublishedVersion struct {
	Version     string
	PublishedAt string
}

// ReleaseAgeFilterResult contains eligible versions and metadata needed for safe caching.
type ReleaseAgeFilterResult struct {
	Versions        []string
	TooYoung        []string
	Unverified      []string
	NextEligibility time.Time
}

// FilterVersionsByMinimumReleaseAge classifies releases using the fixed minimum age.
// Invalid timestamps are unverified rather than eligible, and NextEligibility is the earliest time a young release becomes usable.
func FilterVersionsByMinimumReleaseAge(versions []PublishedVersion, now time.Time) ReleaseAgeFilterResult {
	result := ReleaseAgeFilterResult{
		Versions:   make([]string, 0, len(versions)),
		TooYoung:   make([]string, 0),
		Unverified: make([]string, 0),
	}
	cutoff := now.Add(-MinimumReleaseAge)

	for _, version := range versions {
		publishedAt, err := time.Parse(time.RFC3339, version.PublishedAt)
		if err != nil {
			result.Unverified = append(result.Unverified, version.Version)
			continue
		}
		if publishedAt.Before(cutoff) {
			result.Versions = append(result.Versions, version.Version)
			continue
		}

		eligibility := publishedAt.Add(MinimumReleaseAge).Add(time.Nanosecond)
		result.TooYoung = append(result.TooYoung, version.Version)
		if result.NextEligibility.IsZero() || eligibility.Before(result.NextEligibility) {
			result.NextEligibility = eligibility
		}
	}
	sort.Strings(result.Versions)
	sort.Strings(result.TooYoung)
	sort.Strings(result.Unverified)

	return result
}
