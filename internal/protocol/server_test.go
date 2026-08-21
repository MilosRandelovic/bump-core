package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

type failingWriter struct{}

func (failingWriter) Write(data []byte) (int, error) {
	return 0, errors.New("write failed")
}

func runProtocol(t *testing.T, request any, configure func(*Server)) []map[string]json.RawMessage {
	t.Helper()
	requestData, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	server := NewServerWithIO(strings.NewReader(string(requestData)+"\n"), &output)
	if configure != nil {
		configure(server)
	}
	if err := server.Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	decoder := json.NewDecoder(&output)
	var messages []map[string]json.RawMessage
	for decoder.More() {
		var message map[string]json.RawMessage
		if err := decoder.Decode(&message); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		messages = append(messages, message)
	}
	return messages
}

func messageType(t *testing.T, message map[string]json.RawMessage) string {
	t.Helper()
	var result string
	if err := json.Unmarshal(message["type"], &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestServerDetect(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	messages := runProtocol(t, map[string]any{
		"method": "detect",
		"id":     7,
		"params": map[string]any{"directory": directory},
	}, nil)
	if len(messages) != 2 || messageType(t, messages[0]) != "log" || messageType(t, messages[1]) != "result" {
		t.Fatalf("unexpected messages: %#v", messages)
	}

	var result DetectResult
	if err := json.Unmarshal(messages[1]["result"], &result); err != nil {
		t.Fatal(err)
	}
	if result.RegistryType != "npm" || result.FilePath != filepath.Join(directory, "package.json") {
		t.Fatalf("unexpected detect result: %#v", result)
	}
}

func TestServerCheckWiresVerboseLogsAndGlobalProgress(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "package.json")
	messages := runProtocol(t, map[string]any{
		"method": "check",
		"id":     8,
		"params": map[string]any{
			"filePath":     filePath,
			"registryType": "npm",
			"options":      map[string]any{"verbose": true, "noCache": true, "minimumAge": true},
		},
	}, func(server *Server) {
		server.parseDependencies = func(filePath string, registryType shared.RegistryType, options shared.Options, log shared.LogFunc) ([]shared.Dependency, error) {
			if !options.EnforceMinimumReleaseAge {
				return nil, fmt.Errorf("minimum age was not wired to parsing")
			}
			if log == nil {
				return nil, fmt.Errorf("parser log was not wired")
			}
			log("parser detail")
			return []shared.Dependency{{
				BaseDependency: shared.BaseDependency{Name: "example", OriginalVersion: "^1.0.0", Type: shared.Dependencies, FilePath: filePath, LineNumber: 3},
				Version:        "1.0.0",
			}}, nil
		}
		server.checkOutdated = func(ctx context.Context, dependencies []shared.Dependency, registryType shared.RegistryType, options shared.Options, workingDirectory string, progress shared.ProgressFunc, log shared.LogFunc) (*shared.CheckResult, error) {
			if !options.EnforceMinimumReleaseAge {
				return nil, fmt.Errorf("minimum age was not wired to registry checks")
			}
			progress(shared.Progress{FilePath: filePath, FileCurrent: 1, FileTotal: len(dependencies), Current: 1, Total: len(dependencies)})
			log("registry detail")
			return &shared.CheckResult{Outdated: []shared.OutdatedDependency{{
				BaseDependency: dependencies[0].BaseDependency,
				CurrentVersion: "1.0.0",
				LatestVersion:  "1.1.0",
			}}}, nil
		}
	})

	expectedTypes := []string{"log", "progress", "log", "result"}
	if len(messages) != len(expectedTypes) {
		t.Fatalf("got %d messages, expected %d: %#v", len(messages), len(expectedTypes), messages)
	}
	for index, expected := range expectedTypes {
		if got := messageType(t, messages[index]); got != expected {
			t.Fatalf("message %d type = %q, expected %q", index, got, expected)
		}
	}
	var progressMessage ProgressMessage
	progressData, err := json.Marshal(messages[1])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(progressData, &progressMessage); err != nil {
		t.Fatal(err)
	}
	if progressMessage.FilePath != filePath || progressMessage.FileCurrent != 1 || progressMessage.FileTotal != 1 || progressMessage.Current != 1 || progressMessage.Total != 1 {
		t.Fatalf("unexpected progress message: %#v", progressMessage)
	}
}

func TestServerUpdateEndToEnd(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "package.json")
	content := "{\n  \"dependencies\": {\n    \"example\": \"^1.0.0\"\n  }\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}

	messages := runProtocol(t, map[string]any{
		"method": "update",
		"id":     9,
		"params": map[string]any{
			"filePath":     filePath,
			"registryType": "npm",
			"options":      map[string]any{},
			"outdated": []map[string]any{{
				"name": "example", "type": "dependencies", "currentVersion": "1.0.0",
				"originalVersion": "^1.0.0", "latestVersion": "1.2.0", "filePath": filePath, "lineNumber": 3,
			}},
		},
	}, nil)
	if len(messages) != 1 || messageType(t, messages[0]) != "result" {
		t.Fatalf("unexpected messages: %#v", messages)
	}

	updated, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), `"example": "^1.2.0"`) {
		t.Fatalf("dependency was not updated: %s", updated)
	}
}

func TestServerRejectsUnknownDependencyType(t *testing.T) {
	messages := runProtocol(t, map[string]any{
		"method": "update",
		"id":     10,
		"params": map[string]any{
			"filePath":     "/tmp/package.json",
			"registryType": "npm",
			"options":      map[string]any{},
			"outdated": []map[string]any{{
				"name": "example", "type": "optionalDependencies", "lineNumber": 1,
			}},
		},
	}, nil)
	if len(messages) != 1 || messageType(t, messages[0]) != "error" {
		t.Fatalf("unexpected messages: %#v", messages)
	}
}

func TestServerReturnsOutputErrors(t *testing.T) {
	server := NewServerWithIO(strings.NewReader("not-json\n"), failingWriter{})
	if err := server.Run(); err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("Run() error = %v, expected output failure", err)
	}
}

func TestServerCancelsActiveCheck(t *testing.T) {
	checkRequest, err := json.Marshal(map[string]any{
		"method": "check",
		"id":     41,
		"params": map[string]any{
			"filePath":     "/tmp/package.json",
			"registryType": "npm",
			"options":      map[string]any{"noCache": true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelRequest, err := json.Marshal(map[string]any{
		"method": "cancel",
		"id":     42,
		"params": map[string]any{"id": 41},
	})
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	input := strings.NewReader(string(checkRequest) + "\n" + string(cancelRequest) + "\n")
	server := NewServerWithIO(input, &output)
	server.parseDependencies = func(filePath string, registryType shared.RegistryType, options shared.Options, log shared.LogFunc) ([]shared.Dependency, error) {
		return []shared.Dependency{{BaseDependency: shared.BaseDependency{Name: "example"}, Version: "1.0.0"}}, nil
	}
	server.checkOutdated = func(ctx context.Context, dependencies []shared.Dependency, registryType shared.RegistryType, options shared.Options, workingDirectory string, progress shared.ProgressFunc, log shared.LogFunc) (*shared.CheckResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err := server.Run(); err != nil {
		t.Fatal(err)
	}

	decoder := json.NewDecoder(&output)
	responses := make(map[int]map[string]json.RawMessage)
	for decoder.More() {
		var message map[string]json.RawMessage
		if err := decoder.Decode(&message); err != nil {
			t.Fatal(err)
		}
		var id int
		if err := json.Unmarshal(message["id"], &id); err != nil {
			t.Fatal(err)
		}
		responses[id] = message
	}
	if messageType(t, responses[41]) != "error" || !strings.Contains(string(responses[41]["error"]), "cancelled") {
		t.Fatalf("check response = %#v", responses[41])
	}
	if messageType(t, responses[42]) != "result" || !strings.Contains(string(responses[42]["result"]), `"cancelled":true`) {
		t.Fatalf("cancel response = %#v", responses[42])
	}
}

func TestServerRejectsCancelRequestWithActiveID(t *testing.T) {
	messages := runProtocol(t, map[string]any{
		"method": "cancel",
		"id":     41,
		"params": map[string]any{"id": 41},
	}, func(server *Server) {
		server.activeRequests[41] = func() {}
	})
	if len(messages) != 1 || messageType(t, messages[0]) != "error" || !strings.Contains(string(messages[0]["error"]), "already active") {
		t.Fatalf("unexpected messages: %#v", messages)
	}
}

func TestServerCancelsActiveUpdate(t *testing.T) {
	updateRequest, err := json.Marshal(map[string]any{
		"method": "update",
		"id":     51,
		"params": map[string]any{
			"filePath": "/tmp/package.json", "registryType": "npm", "options": map[string]any{}, "outdated": []any{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelRequest, err := json.Marshal(map[string]any{
		"method": "cancel",
		"id":     52,
		"params": map[string]any{"id": 51},
	})
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	server := NewServerWithIO(strings.NewReader(string(updateRequest)+"\n"+string(cancelRequest)+"\n"), &output)
	server.updateDependencies = func(ctx context.Context, filePath string, outdated []shared.OutdatedDependency, registryType shared.RegistryType, options shared.Options, workingDirectory string, log shared.LogFunc) error {
		<-ctx.Done()
		return ctx.Err()
	}
	if err := server.Run(); err != nil {
		t.Fatal(err)
	}

	decoder := json.NewDecoder(&output)
	responses := make(map[int]map[string]json.RawMessage)
	for decoder.More() {
		var message map[string]json.RawMessage
		if err := decoder.Decode(&message); err != nil {
			t.Fatal(err)
		}
		var id int
		if err := json.Unmarshal(message["id"], &id); err != nil {
			t.Fatal(err)
		}
		responses[id] = message
	}
	if messageType(t, responses[51]) != "error" || !strings.Contains(string(responses[51]["error"]), "cancelled") {
		t.Fatalf("update response = %#v", responses[51])
	}
	if messageType(t, responses[52]) != "result" || !strings.Contains(string(responses[52]["result"]), `"cancelled":true`) {
		t.Fatalf("cancel response = %#v", responses[52])
	}
}

func TestServerReportsSuccessfulUpdateWhenCancellationArrivesAfterCommit(t *testing.T) {
	updateRequest, err := json.Marshal(map[string]any{
		"method": "update",
		"id":     61,
		"params": map[string]any{
			"filePath": "/tmp/package.json", "registryType": "npm", "options": map[string]any{}, "outdated": []any{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelRequest, err := json.Marshal(map[string]any{
		"method": "cancel",
		"id":     62,
		"params": map[string]any{"id": 61},
	})
	if err != nil {
		t.Fatal(err)
	}

	inputReader, inputWriter := io.Pipe()
	var output bytes.Buffer
	server := NewServerWithIO(inputReader, &output)
	updateStarted := make(chan struct{})
	server.updateDependencies = func(ctx context.Context, filePath string, outdated []shared.OutdatedDependency, registryType shared.RegistryType, options shared.Options, workingDirectory string, log shared.LogFunc) error {
		close(updateStarted)
		<-ctx.Done()
		return nil
	}
	serverError := make(chan error, 1)
	go func() {
		serverError <- server.Run()
	}()
	if _, err := inputWriter.Write(append(updateRequest, '\n')); err != nil {
		t.Fatal(err)
	}
	<-updateStarted
	if _, err := inputWriter.Write(append(cancelRequest, '\n')); err != nil {
		t.Fatal(err)
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-serverError; err != nil {
		t.Fatal(err)
	}

	decoder := json.NewDecoder(&output)
	responses := make(map[int]map[string]json.RawMessage)
	for decoder.More() {
		var message map[string]json.RawMessage
		if err := decoder.Decode(&message); err != nil {
			t.Fatal(err)
		}
		var id int
		if err := json.Unmarshal(message["id"], &id); err != nil {
			t.Fatal(err)
		}
		responses[id] = message
	}
	if messageType(t, responses[61]) != "result" {
		t.Fatalf("update response = %#v", responses[61])
	}
	if messageType(t, responses[62]) != "result" || !strings.Contains(string(responses[62]["result"]), `"cancelled":true`) {
		t.Fatalf("cancel response = %#v", responses[62])
	}
}

func TestServerAssociatesLogsAndProgressWithRequest(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "package.json")
	messages := runProtocol(t, map[string]any{
		"method": "check",
		"id":     73,
		"params": map[string]any{
			"filePath": filePath, "registryType": "npm",
			"options": map[string]any{"verbose": true, "noCache": true},
		},
	}, func(server *Server) {
		server.parseDependencies = func(filePath string, registryType shared.RegistryType, options shared.Options, log shared.LogFunc) ([]shared.Dependency, error) {
			log("parser")
			return []shared.Dependency{{BaseDependency: shared.BaseDependency{Name: "example"}, Version: "1.0.0"}}, nil
		}
		server.checkOutdated = func(ctx context.Context, dependencies []shared.Dependency, registryType shared.RegistryType, options shared.Options, workingDirectory string, progress shared.ProgressFunc, log shared.LogFunc) (*shared.CheckResult, error) {
			progress(shared.Progress{FileCurrent: 1, FileTotal: len(dependencies), Current: 1, Total: len(dependencies)})
			return &shared.CheckResult{}, nil
		}
	})
	for _, message := range messages {
		typeName := messageType(t, message)
		if typeName != "log" && typeName != "progress" {
			continue
		}
		var id int
		if err := json.Unmarshal(message["id"], &id); err != nil {
			t.Fatal(err)
		}
		if id != 73 {
			t.Fatalf("%s message id = %d", typeName, id)
		}
	}
}

func TestServerResponsePreservesZeroRequestID(t *testing.T) {
	messages := runProtocol(t, map[string]any{
		"method": "detect",
		"id":     0,
		"params": map[string]any{"directory": t.TempDir()},
	}, func(server *Server) {
		server.detectDependency = func(directory string, log shared.LogFunc) (string, shared.RegistryType, error) {
			return filepath.Join(directory, "package.json"), shared.NPM, nil
		}
	})
	foundResult := false
	for _, message := range messages {
		if messageType(t, message) != "result" {
			continue
		}
		foundResult = true
		if _, exists := message["id"]; !exists || string(message["id"]) != "0" {
			t.Fatalf("zero request id missing from response: %#v", message)
		}
	}
	if !foundResult {
		t.Fatalf("result response missing: %#v", messages)
	}
}

func TestServerBoundsConcurrentRequestsWithoutBlockingCancellation(t *testing.T) {
	var input strings.Builder
	for id := 1; id <= maxConcurrentRequests+1; id++ {
		request, err := json.Marshal(map[string]any{
			"method": "check", "id": id,
			"params": map[string]any{"filePath": "/tmp/package.json", "registryType": "npm", "options": map[string]any{"noCache": true}},
		})
		if err != nil {
			t.Fatal(err)
		}
		input.Write(request)
		input.WriteByte('\n')
	}
	for id := 1; id <= maxConcurrentRequests; id++ {
		request, err := json.Marshal(map[string]any{"method": "cancel", "id": 1000 + id, "params": map[string]any{"id": id}})
		if err != nil {
			t.Fatal(err)
		}
		input.Write(request)
		input.WriteByte('\n')
	}

	var output bytes.Buffer
	server := NewServerWithIO(strings.NewReader(input.String()), &output)
	server.parseDependencies = func(filePath string, registryType shared.RegistryType, options shared.Options, log shared.LogFunc) ([]shared.Dependency, error) {
		return []shared.Dependency{{BaseDependency: shared.BaseDependency{Name: "example"}, Version: "1.0.0"}}, nil
	}
	server.checkOutdated = func(ctx context.Context, dependencies []shared.Dependency, registryType shared.RegistryType, options shared.Options, workingDirectory string, progress shared.ProgressFunc, log shared.LogFunc) (*shared.CheckResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err := server.Run(); err != nil {
		t.Fatal(err)
	}

	decoder := json.NewDecoder(&output)
	foundBoundError := false
	for decoder.More() {
		var message map[string]json.RawMessage
		if err := decoder.Decode(&message); err != nil {
			t.Fatal(err)
		}
		var id int
		if err := json.Unmarshal(message["id"], &id); err != nil {
			t.Fatal(err)
		}
		if id == maxConcurrentRequests+1 && messageType(t, message) == "error" {
			var code ErrorCode
			if err := json.Unmarshal(message["code"], &code); err != nil {
				t.Fatal(err)
			}
			if code != ErrorCodeRequestLimitExceeded {
				t.Fatalf("capacity error code = %q, expected %q", code, ErrorCodeRequestLimitExceeded)
			}
			foundBoundError = true
		}
	}
	if !foundBoundError {
		t.Fatalf("request above the concurrency bound was not rejected: %s", output.String())
	}
}

func TestServerConcurrentUpdatesToSameFilePreserveBothChanges(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "package.json")
	content := "{\n  \"dependencies\": {\n    \"first\": \"^1.0.0\",\n    \"second\": \"^2.0.0\"\n  }\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var input strings.Builder
	updates := []map[string]any{
		{"name": "first", "type": "dependencies", "currentVersion": "1.0.0", "originalVersion": "^1.0.0", "latestVersion": "1.1.0", "filePath": filePath, "lineNumber": 3},
		{"name": "second", "type": "dependencies", "currentVersion": "2.0.0", "originalVersion": "^2.0.0", "latestVersion": "2.1.0", "filePath": filePath, "lineNumber": 4},
	}
	for index, update := range updates {
		request, err := json.Marshal(map[string]any{
			"method": "update", "id": index + 1,
			"params": map[string]any{"filePath": filePath, "registryType": "npm", "options": map[string]any{}, "outdated": []map[string]any{update}},
		})
		if err != nil {
			t.Fatal(err)
		}
		input.Write(request)
		input.WriteByte('\n')
	}

	var output bytes.Buffer
	server := NewServerWithIO(strings.NewReader(input.String()), &output)
	if err := server.Run(); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	results := 0
	for decoder.More() {
		var message map[string]json.RawMessage
		if err := decoder.Decode(&message); err != nil {
			t.Fatal(err)
		}
		if messageType(t, message) != "result" {
			t.Fatalf("concurrent update failed: %s", output.String())
		}
		results++
	}
	if results != 2 {
		t.Fatalf("result count = %d, output = %s", results, output.String())
	}
	updated, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), `"first": "^1.1.0"`) || !strings.Contains(string(updated), `"second": "^2.1.0"`) {
		t.Fatalf("concurrent update was lost: %s", updated)
	}
}
