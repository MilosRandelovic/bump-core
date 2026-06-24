package protocol

import "github.com/MilosRandelovic/bump-core/shared"

// Request represents an incoming JSON-RPC-like request over stdin
type Request struct {
	Method string      `json:"method"`
	ID     int         `json:"id"`
	Params interface{} `json:"params,omitempty"`
}

// Response represents a JSON response sent over stdout
type Response struct {
	ID     int         `json:"id,omitempty"`
	Type   string      `json:"type"`
	Result interface{} `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
}

// LogMessage is an out-of-band log message sent during processing
type LogMessage struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ProgressMessage is an out-of-band progress update sent during processing
type ProgressMessage struct {
	Type    string `json:"type"`
	Current int    `json:"current"`
	Total   int    `json:"total"`
}

// DetectParams are the parameters for the "detect" method
type DetectParams struct {
	Directory string `json:"directory"`
}

// CheckParams are the parameters for the "check" method
type CheckParams struct {
	FilePath     string        `json:"filePath"`
	RegistryType string        `json:"registryType"`
	Options      OptionsParams `json:"options"`
}

// UpdateParams are the parameters for the "update" method
type UpdateParams struct {
	FilePath     string                   `json:"filePath"`
	RegistryType string                   `json:"registryType"`
	Options      OptionsParams            `json:"options"`
	Outdated     []OutdatedDependencyInfo `json:"outdated"`
}

// OptionsParams maps to shared.Options
type OptionsParams struct {
	Verbose                 bool `json:"verbose"`
	Update                  bool `json:"update"`
	Semver                  bool `json:"semver"`
	NoCache                 bool `json:"noCache"`
	IncludePeerDependencies bool `json:"includePeerDependencies"`
	Monorepo                bool `json:"monorepo"`
}

// OutdatedDependencyInfo is the JSON representation of an outdated dependency
type OutdatedDependencyInfo struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	CurrentVersion  string `json:"currentVersion"`
	OriginalVersion string `json:"originalVersion"`
	LatestVersion   string `json:"latestVersion"`
	FilePath        string `json:"filePath"`
	HostedURL       string `json:"hostedUrl,omitempty"`
	LineNumber      int    `json:"lineNumber"`
}

// CheckResult is the JSON result of the "check" method
type CheckResult struct {
	Outdated      []OutdatedDependencyInfo `json:"outdated"`
	SemverSkipped []SemverSkippedInfo      `json:"semverSkipped"`
	Errors        []DependencyErrorInfo    `json:"errors"`
}

// SemverSkippedInfo is the JSON representation of a semver-skipped dependency
type SemverSkippedInfo struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	CurrentVersion  string `json:"currentVersion"`
	OriginalVersion string `json:"originalVersion"`
	LatestVersion   string `json:"latestVersion"`
	Reason          string `json:"reason"`
	LineNumber      int    `json:"lineNumber"`
}

// DependencyErrorInfo is the JSON representation of a dependency error
type DependencyErrorInfo struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

// DetectResult is the JSON result of the "detect" method
type DetectResult struct {
	FilePath     string `json:"filePath"`
	RegistryType string `json:"registryType"`
}

// UpdateResult is the JSON result of the "update" method
type UpdateResult struct {
	Updated int `json:"updated"`
}

// ToOptions converts OptionsParams to shared.Options
func (o OptionsParams) ToOptions() shared.Options {
	return shared.Options{
		Verbose:                 o.Verbose,
		Update:                  o.Update,
		Semver:                  o.Semver,
		NoCache:                 o.NoCache,
		IncludePeerDependencies: o.IncludePeerDependencies,
		Monorepo:                o.Monorepo,
	}
}

// FromCheckResult converts a shared.CheckResult to a protocol CheckResult
func FromCheckResult(checkResult *shared.CheckResult) *CheckResult {
	result := &CheckResult{
		Outdated:      make([]OutdatedDependencyInfo, 0, len(checkResult.Outdated)),
		SemverSkipped: make([]SemverSkippedInfo, 0, len(checkResult.SemverSkipped)),
		Errors:        make([]DependencyErrorInfo, 0, len(checkResult.Errors)),
	}

	for _, outdated := range checkResult.Outdated {
		result.Outdated = append(result.Outdated, OutdatedDependencyInfo{
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
		result.SemverSkipped = append(result.SemverSkipped, SemverSkippedInfo{
			Name:            skipped.Name,
			Type:            skipped.Type.String(),
			CurrentVersion:  skipped.CurrentVersion,
			OriginalVersion: skipped.OriginalVersion,
			LatestVersion:   skipped.LatestVersion,
			Reason:          skipped.Reason.String(),
			LineNumber:      skipped.LineNumber,
		})
	}

	for _, dependencyError := range checkResult.Errors {
		result.Errors = append(result.Errors, DependencyErrorInfo{
			Name:  dependencyError.Name,
			Error: dependencyError.Error,
		})
	}

	return result
}

// ToOutdatedDependencies converts protocol OutdatedDependencyInfo slice to shared.OutdatedDependency slice
func ToOutdatedDependencies(infos []OutdatedDependencyInfo) []shared.OutdatedDependency {
	result := make([]shared.OutdatedDependency, 0, len(infos))
	for _, info := range infos {
		dependencyType := shared.Dependencies
		switch info.Type {
		case "devDependencies":
			dependencyType = shared.DevDependencies
		case "peerDependencies":
			dependencyType = shared.PeerDependencies
		}
		result = append(result, shared.OutdatedDependency{
			BaseDependency: shared.BaseDependency{
				Name:      info.Name,
				Type:      dependencyType,
				FilePath:  info.FilePath,
				HostedURL: info.HostedURL,
			},
			CurrentVersion: info.CurrentVersion,
			LatestVersion:  info.LatestVersion,
		})
	}
	return result
}

// ParseRegistryType converts a string to shared.RegistryType
func ParseRegistryType(s string) (shared.RegistryType, error) {
	switch s {
	case "npm":
		return shared.Npm, nil
	case "pub":
		return shared.Pub, nil
	default:
		return 0, shared.ErrUnsupportedRegistryType
	}
}
