package protocol

import (
	"encoding/json"
	"fmt"

	"github.com/MilosRandelovic/bump-core/v2/internal/dependency"
	"github.com/MilosRandelovic/bump-core/v2/shared"
)

// RequestMethod identifies a supported sidecar operation.
type RequestMethod string

// Supported request methods.
const (
	RequestMethodDetect RequestMethod = "detect"
	RequestMethodCheck  RequestMethod = "check"
	RequestMethodUpdate RequestMethod = "update"
	RequestMethodCancel RequestMethod = "cancel"
)

// Request represents an incoming JSON-RPC-like request over stdin
type Request struct {
	Method RequestMethod   `json:"method"`
	ID     int             `json:"id"`
	Params json.RawMessage `json:"params,omitempty"`
}

// ErrorCode is a stable machine-readable protocol error identifier.
type ErrorCode string

const (
	// ErrorCodeRequestLimitExceeded indicates that the client may retry after an active request completes.
	ErrorCodeRequestLimitExceeded ErrorCode = "request_limit_exceeded"
)

// Response represents a JSON response sent over stdout
type Response struct {
	ID     int         `json:"id"`
	Type   string      `json:"type"`
	Result interface{} `json:"result,omitempty"`
	Code   ErrorCode   `json:"code,omitempty"`
	Error  string      `json:"error,omitempty"`
}

// LogMessage is an out-of-band log message sent during processing
type LogMessage struct {
	Type    string `json:"type"`
	ID      int    `json:"id"`
	Message string `json:"message"`
}

// ProgressMessage is an out-of-band progress update sent during processing
type ProgressMessage struct {
	Type        string `json:"type"`
	ID          int    `json:"id"`
	FilePath    string `json:"filePath"`
	FileCurrent int    `json:"fileCurrent"`
	FileTotal   int    `json:"fileTotal"`
	Current     int    `json:"current"`
	Total       int    `json:"total"`
}

// CancelParams identifies an active request to cancel.
type CancelParams struct {
	ID *int `json:"id"`
}

// DetectParams are the parameters for the "detect" method
type DetectParams struct {
	Directory string `json:"directory"`
}

// CheckParams are the parameters for the "check" method
type CheckParams struct {
	FilePath     string                `json:"filePath"`
	RegistryType string                `json:"registryType"`
	Options      OptionsParams         `json:"options"`
	Targets      []dependency.Selector `json:"targets,omitempty"`
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
	MinimumAge              bool `json:"minimumAge"`
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

// CancelResult reports whether the target request was active.
type CancelResult struct {
	Cancelled bool `json:"cancelled"`
}

// ToOptions maps every wire-level option to its shared library equivalent.
func (o OptionsParams) ToOptions() shared.Options {
	return shared.Options{
		Verbose:                  o.Verbose,
		Update:                   o.Update,
		Semver:                   o.Semver,
		NoCache:                  o.NoCache,
		IncludePeerDependencies:  o.IncludePeerDependencies,
		Monorepo:                 o.Monorepo,
		EnforceMinimumReleaseAge: o.MinimumAge,
	}
}

// FromCheckResult converts library results to their JSON protocol representation without reordering them.
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

// ToOutdatedDependencies converts wire-level updates to library dependencies and rejects unknown dependency types.
func ToOutdatedDependencies(infos []OutdatedDependencyInfo) ([]shared.OutdatedDependency, error) {
	result := make([]shared.OutdatedDependency, 0, len(infos))
	for _, info := range infos {
		var dependencyType shared.DependencyType
		switch info.Type {
		case "dependencies":
			dependencyType = shared.Dependencies
		case "devDependencies":
			dependencyType = shared.DevDependencies
		case "peerDependencies":
			dependencyType = shared.PeerDependencies
		default:
			return nil, fmt.Errorf("unsupported dependency type %q for %s", info.Type, info.Name)
		}
		result = append(result, shared.OutdatedDependency{
			BaseDependency: shared.BaseDependency{
				Name:            info.Name,
				OriginalVersion: info.OriginalVersion,
				Type:            dependencyType,
				FilePath:        info.FilePath,
				HostedURL:       info.HostedURL,
				LineNumber:      info.LineNumber,
			},
			CurrentVersion: info.CurrentVersion,
			LatestVersion:  info.LatestVersion,
		})
	}
	return result, nil
}

// ParseRegistryType converts the supported wire values "npm" and "pub" to shared.RegistryType.
func ParseRegistryType(s string) (shared.RegistryType, error) {
	switch s {
	case "npm":
		return shared.NPM, nil
	case "pub":
		return shared.Pub, nil
	default:
		return 0, shared.ErrUnsupportedRegistryType
	}
}
