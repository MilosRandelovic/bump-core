package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

func TestTypedResponsesPreserveWireFormat(t *testing.T) {
	tests := []struct {
		name string
		send func(server *Server)
		want string
	}{
		{"detect", func(server *Server) {
			sendResult(server, 0, &DetectResult{FilePath: "/project/package.json", RegistryType: "npm"})
		}, `{"id":0,"type":"result","result":{"filePath":"/project/package.json","registryType":"npm"}}`},
		{"check", func(server *Server) {
			sendResult(server, 0, &CheckResult{Outdated: []OutdatedDependencyInfo{}, SemverSkipped: []SemverSkippedInfo{}, Errors: []DependencyErrorInfo{}})
		}, `{"id":0,"type":"result","result":{"outdated":[],"semverSkipped":[],"errors":[]}}`},
		{"update", func(server *Server) {
			sendResult(server, 0, &UpdateResult{Updated: 0})
		}, `{"id":0,"type":"result","result":{"updated":0}}`},
		{"cancel", func(server *Server) {
			sendResult(server, 0, &CancelResult{Cancelled: false})
		}, `{"id":0,"type":"result","result":{"cancelled":false}}`},
		{"error", func(server *Server) {
			server.sendError(0, "invalid request")
		}, `{"id":0,"type":"error","error":"invalid request"}`},
		{"coded error", func(server *Server) {
			server.sendCodedError(0, ErrorCodeRequestLimitExceeded, "too many active requests")
		}, `{"id":0,"type":"error","code":"request_limit_exceeded","error":"too many active requests"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			server := NewServerWithIO(strings.NewReader(""), &output)
			test.send(server)
			if got := output.String(); got != test.want+"\n" {
				t.Fatalf("response = %s, want %s", got, test.want)
			}
		})
	}
}

func TestResultPayloadRejectsUnsupportedTypes(t *testing.T) {
	packageDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		body string
	}{
		{"response string", `_ = Response[string]{Result: "unsupported"}`},
		{"response any", `_ = Response[any]{Result: &UpdateResult{}}`},
		{"send string", `sendResult((*Server)(nil), 0, "unsupported")`},
		{"send any", `var result any = &UpdateResult{}; sendResult((*Server)(nil), 0, result)`},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			probePath := filepath.Join(directory, "probe_test.go")
			probe := "package protocol\nfunc unsupportedResultProbe() { " + test.body + " }\n"
			if err := os.WriteFile(probePath, []byte(probe), 0600); err != nil {
				t.Fatal(err)
			}
			overlay, err := json.Marshal(struct {
				Replace map[string]string
			}{Replace: map[string]string{filepath.Join(packageDirectory, "result_constraint_probe_test.go"): probePath}})
			if err != nil {
				t.Fatal(err)
			}
			overlayPath := filepath.Join(directory, "overlay.json")
			if err := os.WriteFile(overlayPath, overlay, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			command := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "test", "-vet=off", "-run=^$", "-overlay="+overlayPath, ".")
			command.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "GOPROXY=off", "GOSUMDB=off", "GOFLAGS=")
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "does not satisfy resultPayload") {
				t.Fatalf("compiler must reject unsupported payload: error = %v, output = %s", err, output)
			}
		})
	}
}

func TestOptionsParamsToOptions(t *testing.T) {
	params := OptionsParams{
		Verbose:                 true,
		Semver:                  true,
		NoCache:                 true,
		IncludePeerDependencies: true,
		Monorepo:                true,
		MinimumAge:              true,
	}
	expected := shared.Options{
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
