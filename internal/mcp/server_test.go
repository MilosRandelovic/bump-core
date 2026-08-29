package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MilosRandelovic/bump-core/v2/shared"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServerChecksAndUpdatesDependencies(t *testing.T) {
	projectDirectory := t.TempDir()
	packagePath := filepath.Join(projectDirectory, "package.json")
	outdated := shared.OutdatedDependency{
		BaseDependency: shared.BaseDependency{
			Name:            "example",
			OriginalVersion: "^1.0.0",
			Type:            shared.Dependencies,
			FilePath:        packagePath,
			LineNumber:      4,
		},
		CurrentVersion: "1.0.0",
		LatestVersion:  "1.1.0",
	}
	expectedOptions := shared.Options{
		Semver:                   true,
		NoCache:                  true,
		IncludePeerDependencies:  true,
		Monorepo:                 true,
		EnforceMinimumReleaseAge: true,
	}
	updateCalled := false
	progressMessages := make(chan *mcpsdk.ProgressNotificationParams, 1)
	server := newServer(
		func(directory string, log shared.LogFunc) (string, shared.RegistryType, error) {
			if directory != projectDirectory {
				return "", 0, fmt.Errorf("directory = %q, expected %q", directory, projectDirectory)
			}
			return packagePath, shared.NPM, nil
		},
		func(filePath string, registryType shared.RegistryType, options shared.Options, log shared.LogFunc) ([]shared.Dependency, error) {
			if filePath != packagePath || registryType != shared.NPM || options != expectedOptions {
				return nil, fmt.Errorf("unexpected parse arguments: %q, %v, %+v", filePath, registryType, options)
			}
			log("workspace diagnostic\n")
			return []shared.Dependency{
				{BaseDependency: outdated.BaseDependency, Version: outdated.CurrentVersion},
				{BaseDependency: shared.BaseDependency{Name: "ignored", Type: shared.DevDependencies, FilePath: packagePath}, Version: "2.0.0"},
			}, nil
		},
		func(ctx context.Context, dependencies []shared.Dependency, registryType shared.RegistryType, options shared.Options, workingDirectory string, progressCallback shared.ProgressFunc, log shared.LogFunc) (*shared.CheckResult, error) {
			if len(dependencies) != 1 || dependencies[0].Name != "example" || registryType != shared.NPM || options != expectedOptions || workingDirectory != projectDirectory {
				return nil, fmt.Errorf("unexpected check arguments")
			}
			if progressCallback == nil {
				return nil, fmt.Errorf("progress callback was not provided")
			}
			progressCallback(shared.Progress{FilePath: packagePath, FileCurrent: 1, FileTotal: 1, Current: 1, Total: 1})
			return &shared.CheckResult{
				Outdated: []shared.OutdatedDependency{outdated},
				SemverSkipped: []shared.SemverSkipped{{
					OutdatedDependency: shared.OutdatedDependency{
						BaseDependency: shared.BaseDependency{Name: "fixed", OriginalVersion: "1.0.0", Type: shared.DevDependencies, FilePath: packagePath, LineNumber: 8},
						CurrentVersion: "1.0.0",
						LatestVersion:  "2.0.0",
					},
					Reason: shared.HardcodedVersion,
				}},
				Errors: []shared.DependencyError{{Name: "broken", Error: "registry unavailable"}},
			}, nil
		},
		func(ctx context.Context, filePath string, updates []shared.OutdatedDependency, registryType shared.RegistryType, options shared.Options, workingDirectory string, log shared.LogFunc) error {
			if filePath != packagePath || len(updates) != 1 || updates[0] != outdated || registryType != shared.NPM || options != expectedOptions || workingDirectory != projectDirectory {
				return fmt.Errorf("unexpected update arguments")
			}
			updateCalled = true
			return nil
		},
	)
	client := connectTestClientWithOptions(t, server, &mcpsdk.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, request *mcpsdk.ProgressNotificationClientRequest) {
			progressMessages <- request.Params
		},
	})

	checkParams := &mcpsdk.CallToolParams{
		Name: "check_updates",
		Arguments: map[string]any{
			"directory": projectDirectory,
			"targets":   []map[string]any{{"name": "example"}},
			"options": map[string]any{
				"semver": true, "minimumAge": true, "noCache": true,
				"includePeerDependencies": true, "monorepo": true,
			},
		},
	}
	checkParams.SetProgressToken("check-progress")
	checkResult, err := client.CallTool(context.Background(), checkParams)
	if err != nil {
		t.Fatal(err)
	}
	if checkResult.IsError {
		t.Fatalf("check_updates failed: %#v", checkResult.Content)
	}
	var checkOutput checkUpdatesOutput
	decodeStructuredContent(t, checkResult.StructuredContent, &checkOutput)
	if checkOutput.CheckID == "" || checkOutput.FilePath != packagePath || checkOutput.RegistryType != "npm" {
		t.Fatalf("unexpected check output: %+v", checkOutput)
	}
	if len(checkOutput.Outdated) != 1 || checkOutput.Outdated[0].LatestVersion != "1.1.0" {
		t.Fatalf("unexpected outdated dependencies: %+v", checkOutput.Outdated)
	}
	if len(checkOutput.SemverSkipped) != 1 || len(checkOutput.Errors) != 1 {
		t.Fatalf("unexpected partial results: %+v", checkOutput)
	}
	if len(checkOutput.Diagnostics) != 1 || checkOutput.Diagnostics[0] != "workspace diagnostic" {
		t.Fatalf("unexpected diagnostics: %#v", checkOutput.Diagnostics)
	}
	if len(checkResult.Content) != 1 {
		t.Fatal("text result did not include the structured JSON fallback")
	}
	textContent, isText := checkResult.Content[0].(*mcpsdk.TextContent)
	if !isText || !strings.Contains(textContent.Text, checkOutput.CheckID) {
		t.Fatal("text result did not include the structured JSON fallback")
	}
	select {
	case progress := <-progressMessages:
		if progress.ProgressToken != "check-progress" || progress.Progress != 1 || progress.Total != 1 {
			t.Fatalf("unexpected progress: %+v", progress)
		}
	case <-time.After(time.Second):
		t.Fatal("check_updates did not forward progress")
	}

	updateResult, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "update_dependencies",
		Arguments: map[string]any{"checkId": checkOutput.CheckID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updateResult.IsError {
		t.Fatalf("update_dependencies failed: %#v", updateResult.Content)
	}
	var updateOutput updateDependenciesOutput
	decodeStructuredContent(t, updateResult.StructuredContent, &updateOutput)
	if !updateCalled || updateOutput.Updated != 1 || len(updateOutput.Files) != 1 || updateOutput.Files[0] != packagePath {
		t.Fatalf("unexpected update output: %+v", updateOutput)
	}

	reusedResult, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "update_dependencies",
		Arguments: map[string]any{"checkId": checkOutput.CheckID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reusedResult.IsError {
		t.Fatal("update_dependencies accepted a used checkId")
	}
}

func TestServerPublishesToolContracts(t *testing.T) {
	server := NewServer()
	client := connectTestClient(t, server)
	result, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 2 {
		t.Fatalf("got %d tools, expected 2", len(result.Tools))
	}
	tools := make(map[string]*mcpsdk.Tool, len(result.Tools))
	for _, tool := range result.Tools {
		tools[tool.Name] = tool
	}
	checkTool := tools["check_updates"]
	if checkTool == nil || checkTool.Annotations == nil || !checkTool.Annotations.ReadOnlyHint || checkTool.Annotations.OpenWorldHint == nil || !*checkTool.Annotations.OpenWorldHint {
		t.Fatalf("unexpected check_updates contract: %+v", checkTool)
	}
	updateTool := tools["update_dependencies"]
	if updateTool == nil || updateTool.Annotations == nil || updateTool.Annotations.DestructiveHint == nil || !*updateTool.Annotations.DestructiveHint || updateTool.Annotations.OpenWorldHint == nil || *updateTool.Annotations.OpenWorldHint {
		t.Fatalf("unexpected update_dependencies contract: %+v", updateTool)
	}
	if checkTool.InputSchema == nil || checkTool.OutputSchema == nil || updateTool.InputSchema == nil || updateTool.OutputSchema == nil {
		t.Fatal("tool schemas were not published")
	}
}

func connectTestClient(t *testing.T, server *Server) *mcpsdk.ClientSession {
	return connectTestClientWithOptions(t, server, nil)
}

func connectTestClientWithOptions(t *testing.T, server *Server, options *mcpsdk.ClientOptions) *mcpsdk.ClientSession {
	t.Helper()
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.protocolServer.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1.0.0"}, options)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	})
	return clientSession
}

func decodeStructuredContent(t *testing.T, content any, destination any) {
	t.Helper()
	data, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, destination); err != nil {
		t.Fatal(err)
	}
}
