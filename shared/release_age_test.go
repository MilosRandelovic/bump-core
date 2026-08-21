package shared

import (
	"reflect"
	"testing"
	"time"
)

func TestFilterVersionsByMinimumReleaseAge(t *testing.T) {
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	result := FilterVersionsByMinimumReleaseAge([]PublishedVersion{
		{Version: "1.0.0", PublishedAt: "2026-08-15T10:00:00.123Z"},
		{Version: "1.1.0", PublishedAt: now.Add(-MinimumReleaseAge).Format(time.RFC3339)},
		{Version: "1.2.0", PublishedAt: now.Add(-time.Hour).Format(time.RFC3339)},
		{Version: "1.3.0", PublishedAt: "not-a-time"},
	}, now)

	if !reflect.DeepEqual(result.Versions, []string{"1.0.0"}) {
		t.Fatalf("eligible versions = %#v", result.Versions)
	}
	if !reflect.DeepEqual(result.TooYoung, []string{"1.1.0", "1.2.0"}) {
		t.Fatalf("young versions = %#v", result.TooYoung)
	}
	if !reflect.DeepEqual(result.Unverified, []string{"1.3.0"}) {
		t.Fatalf("unverified versions = %#v", result.Unverified)
	}
	expectedEligibility := now.Add(time.Nanosecond)
	if !result.NextEligibility.Equal(expectedEligibility) {
		t.Fatalf("next eligibility = %s, expected %s", result.NextEligibility, expectedEligibility)
	}
}

func TestGenerateCacheKeyUsesOnlyCacheRelevantOptions(t *testing.T) {
	unfiltered := GenerateCacheKey("example", "npm", "https://registry.npmjs.org", "", "*", Options{})
	unfilteredVerbose := GenerateCacheKey("example", "npm", "https://registry.npmjs.org", "", "*", Options{Verbose: true})
	filtered := GenerateCacheKey("example", "npm", "https://registry.npmjs.org", "", "*", Options{EnforceMinimumReleaseAge: true})
	if unfiltered != unfilteredVerbose {
		t.Fatal("options unrelated to registry results must not change cache keys")
	}
	if unfiltered == filtered {
		t.Fatal("minimum-age and unfiltered cache keys must differ")
	}
}
