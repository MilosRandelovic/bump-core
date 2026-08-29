package protocol

import (
	"testing"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

func TestOptionsParamsToOptions(t *testing.T) {
	params := OptionsParams{
		Verbose:                 true,
		Update:                  true,
		Semver:                  true,
		NoCache:                 true,
		IncludePeerDependencies: true,
		Monorepo:                true,
		MinimumAge:              true,
	}
	expected := shared.Options{
		Verbose:                  true,
		Update:                   true,
		Semver:                   true,
		NoCache:                  true,
		IncludePeerDependencies:  true,
		Monorepo:                 true,
		EnforceMinimumReleaseAge: true,
	}

	if actual := params.ToOptions(); actual != expected {
		t.Fatalf("ToOptions() = %+v, want %+v", actual, expected)
	}
}
