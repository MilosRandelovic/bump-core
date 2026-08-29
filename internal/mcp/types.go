package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/MilosRandelovic/bump-core/v2/internal/dependency"
	"github.com/MilosRandelovic/bump-core/v2/shared"
)

type toolOptions struct {
	Semver                  bool `json:"semver,omitempty" jsonschema:"Return the latest version allowed by each dependency's semantic-version constraint instead of its absolute latest version."`
	MinimumAge              bool `json:"minimumAge,omitempty" jsonschema:"Restrict candidates to releases published more than 24 hours ago. May be combined with semver."`
	NoCache                 bool `json:"noCache,omitempty" jsonschema:"Bypass cached registry responses."`
	IncludePeerDependencies bool `json:"includePeerDependencies,omitempty" jsonschema:"Include npm peer dependencies."`
	Monorepo                bool `json:"monorepo,omitempty" jsonschema:"Include npm workspace package manifests."`
}

func (options toolOptions) sharedOptions() shared.Options {
	return shared.Options{
		Semver:                   options.Semver,
		NoCache:                  options.NoCache,
		IncludePeerDependencies:  options.IncludePeerDependencies,
		Monorepo:                 options.Monorepo,
		EnforceMinimumReleaseAge: options.MinimumAge,
	}
}

type checkUpdatesInput struct {
	Directory string                `json:"directory" jsonschema:"Absolute path to the project directory containing package.json or pubspec.yaml."`
	Options   toolOptions           `json:"options,omitempty" jsonschema:"Dependency check options. Omit semver and minimumAge for absolute latest versions."`
	Targets   []dependency.Selector `json:"targets,omitempty" jsonschema:"Optional dependency selectors. Omit to check the entire detected file or workspace."`
}

type updateDependenciesInput struct {
	CheckID string `json:"checkId" jsonschema:"The checkId returned by check_updates for the exact updates to apply."`
}

type dependencyUpdate struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	CurrentVersion  string `json:"currentVersion"`
	OriginalVersion string `json:"originalVersion"`
	LatestVersion   string `json:"latestVersion"`
	FilePath        string `json:"filePath"`
	HostedURL       string `json:"hostedUrl,omitempty"`
	LineNumber      int    `json:"lineNumber"`
}

type skippedDependency struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	CurrentVersion  string `json:"currentVersion"`
	OriginalVersion string `json:"originalVersion"`
	LatestVersion   string `json:"latestVersion"`
	Reason          string `json:"reason"`
	LineNumber      int    `json:"lineNumber"`
}

type dependencyError struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

type checkUpdatesOutput struct {
	CheckID       string              `json:"checkId"`
	FilePath      string              `json:"filePath"`
	RegistryType  string              `json:"registryType"`
	Outdated      []dependencyUpdate  `json:"outdated"`
	SemverSkipped []skippedDependency `json:"semverSkipped"`
	Errors        []dependencyError   `json:"errors"`
	Diagnostics   []string            `json:"diagnostics"`
}

type updateDependenciesOutput struct {
	Updated int      `json:"updated"`
	Files   []string `json:"files"`
}

func newCheckUpdatesOutput(checkID, filePath string, registryType shared.RegistryType, checkResult *shared.CheckResult, diagnostics []string) checkUpdatesOutput {
	output := checkUpdatesOutput{
		CheckID:       checkID,
		FilePath:      filePath,
		RegistryType:  registryType.String(),
		Outdated:      make([]dependencyUpdate, 0, len(checkResult.Outdated)),
		SemverSkipped: make([]skippedDependency, 0, len(checkResult.SemverSkipped)),
		Errors:        make([]dependencyError, 0, len(checkResult.Errors)),
		Diagnostics:   diagnostics,
	}
	if output.Diagnostics == nil {
		output.Diagnostics = []string{}
	}

	for _, outdated := range checkResult.Outdated {
		output.Outdated = append(output.Outdated, dependencyUpdate{
			Name:            outdated.Name,
			Type:            outdated.Type.String(),
			CurrentVersion:  outdated.CurrentVersion,
			OriginalVersion: outdated.OriginalVersion,
			LatestVersion:   outdated.LatestVersion,
			FilePath:        outdated.FilePath,
			HostedURL:       outdated.HostedURL,
			LineNumber:      outdated.LineNumber,
		})
	}

	for _, skipped := range checkResult.SemverSkipped {
		output.SemverSkipped = append(output.SemverSkipped, skippedDependency{
			Name:            skipped.Name,
			Type:            skipped.Type.String(),
			CurrentVersion:  skipped.CurrentVersion,
			OriginalVersion: skipped.OriginalVersion,
			LatestVersion:   skipped.LatestVersion,
			Reason:          skipped.Reason.String(),
			LineNumber:      skipped.LineNumber,
		})
	}

	for _, checkError := range checkResult.Errors {
		output.Errors = append(output.Errors, dependencyError{Name: checkError.Name, Error: checkError.Error})
	}

	return output
}

func checkSummary(output checkUpdatesOutput) string {
	var summary string
	switch len(output.Outdated) {
	case 0:
		summary = fmt.Sprintf("No dependency updates found in %s.", output.FilePath)
	case 1:
		update := output.Outdated[0]
		summary = fmt.Sprintf("Found 1 dependency update in %s: %s %s -> %s.", output.FilePath, update.Name, update.CurrentVersion, update.LatestVersion)
	default:
		summary = fmt.Sprintf("Found %d dependency updates in %s.", len(output.Outdated), output.FilePath)
	}
	if len(output.SemverSkipped) > 0 {
		summary += fmt.Sprintf(" %d skipped by semantic-version constraints.", len(output.SemverSkipped))
	}
	if len(output.Errors) > 0 {
		summary += fmt.Sprintf(" %d dependency checks failed.", len(output.Errors))
	}
	return summary
}

func updatedFiles(outdated []shared.OutdatedDependency, defaultFilePath string) []string {
	files := make([]string, 0)
	seen := make(map[string]struct{})
	for _, dependency := range outdated {
		filePath := dependency.FilePath
		if filePath == "" {
			filePath = defaultFilePath
		}
		if _, exists := seen[filePath]; exists {
			continue
		}
		seen[filePath] = struct{}{}
		files = append(files, filePath)
	}
	shared.SortFilesByDepth(files)
	return files
}

func updateSummary(output updateDependenciesOutput) string {
	if output.Updated == 0 {
		return "No dependency files required changes."
	}
	if output.Updated == 1 && len(output.Files) == 1 {
		return "Updated 1 dependency in 1 file."
	}
	return fmt.Sprintf("Updated %d dependencies across %d files.", output.Updated, len(output.Files))
}

func textWithStructuredJSON(summary string, output any) string {
	encoded, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return summary
	}
	return summary + "\n\n" + string(encoded)
}
