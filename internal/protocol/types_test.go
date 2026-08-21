package protocol

import (
	"testing"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

func TestOptionsParamsToOptions(t *testing.T) {
	tests := []struct {
		name     string
		params   OptionsParams
		expected shared.Options
	}{
		{
			name:     "verbose",
			params:   OptionsParams{Verbose: true},
			expected: shared.Options{Verbose: true},
		},
		{
			name:     "update",
			params:   OptionsParams{Update: true},
			expected: shared.Options{Update: true},
		},
		{
			name:     "semver",
			params:   OptionsParams{Semver: true},
			expected: shared.Options{Semver: true},
		},
		{
			name:     "no cache",
			params:   OptionsParams{NoCache: true},
			expected: shared.Options{NoCache: true},
		},
		{
			name:     "include peer dependencies",
			params:   OptionsParams{IncludePeerDependencies: true},
			expected: shared.Options{IncludePeerDependencies: true},
		},
		{
			name:     "monorepo",
			params:   OptionsParams{Monorepo: true},
			expected: shared.Options{Monorepo: true},
		},
		{
			name:     "minimum age",
			params:   OptionsParams{MinimumAge: true},
			expected: shared.Options{EnforceMinimumReleaseAge: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := test.params.ToOptions(); actual != test.expected {
				t.Fatalf("ToOptions() = %+v, want %+v", actual, test.expected)
			}
		})
	}
}
