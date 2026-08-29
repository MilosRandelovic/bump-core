package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/MilosRandelovic/bump-core/v2/internal/dependency"
	"github.com/MilosRandelovic/bump-core/v2/parser"
	"github.com/MilosRandelovic/bump-core/v2/shared"
	"github.com/MilosRandelovic/bump-core/v2/updater"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type detectDependencyFunc func(
	directory string,
	log shared.LogFunc,
) (filePath string, registryType shared.RegistryType, err error)

type parseDependenciesFunc func(
	filePath string,
	registryType shared.RegistryType,
	options shared.Options,
	log shared.LogFunc,
) (dependencies []shared.Dependency, err error)

type checkOutdatedFunc func(
	ctx context.Context,
	dependencies []shared.Dependency,
	registryType shared.RegistryType,
	options shared.Options,
	workingDirectory string,
	progressCallback shared.ProgressFunc,
	log shared.LogFunc,
) (checkResult *shared.CheckResult, err error)

type updateDependenciesFunc func(
	ctx context.Context,
	filePath string,
	outdated []shared.OutdatedDependency,
	registryType shared.RegistryType,
	options shared.Options,
	workingDirectory string,
	log shared.LogFunc,
) error

type checkedUpdates struct {
	filePath         string
	registryType     shared.RegistryType
	options          shared.Options
	workingDirectory string
	outdated         []shared.OutdatedDependency
}

// Server exposes bump dependency checks and updates as MCP tools.
type Server struct {
	protocolServer     *mcpsdk.Server
	checksMutex        sync.Mutex
	nextCheckID        uint64
	checks             map[string]checkedUpdates
	detectDependency   detectDependencyFunc
	parseDependencies  parseDependenciesFunc
	checkOutdated      checkOutdatedFunc
	updateDependencies updateDependenciesFunc
}

// NewServer returns an MCP server configured with bump's dependency tools.
func NewServer() *Server {
	return newServer(
		parser.AutoDetectDependencyFile,
		parser.ParseDependenciesWithLog,
		updater.CheckOutdated,
		updater.UpdateDependencies,
	)
}

func newServer(detectDependency detectDependencyFunc, parseDependencies parseDependenciesFunc, checkOutdated checkOutdatedFunc, updateDependencies updateDependenciesFunc) *Server {
	server := &Server{
		checks:             make(map[string]checkedUpdates),
		detectDependency:   detectDependency,
		parseDependencies:  parseDependencies,
		checkOutdated:      checkOutdated,
		updateDependencies: updateDependencies,
	}
	server.protocolServer = mcpsdk.NewServer(
		&mcpsdk.Implementation{
			Name:        "bump",
			Title:       "Bump",
			Description: "Checks and updates npm and Pub dependencies.",
			Version:     shared.Version,
			WebsiteURL:  "https://github.com/MilosRandelovic/homebrew-bump",
		},
		&mcpsdk.ServerOptions{
			Instructions: "Call check_updates before update_dependencies. update_dependencies only accepts a checkId returned by this server and applies that exact checked set.",
			Capabilities: &mcpsdk.ServerCapabilities{},
		},
	)
	server.registerTools()
	return server
}

func (server *Server) registerTools() {
	openWorld := true
	closedWorld := false
	destructive := true
	mcpsdk.AddTool(server.protocolServer, &mcpsdk.Tool{
		Name:        "check_updates",
		Title:       "Check dependency updates",
		Description: "Detect package.json or pubspec.yaml in a project, query its registries, and return available dependency updates. The returned checkId identifies the exact checked set for update_dependencies.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  &openWorld,
		},
	}, server.handleCheckUpdates)
	mcpsdk.AddTool(server.protocolServer, &mcpsdk.Tool{
		Name:        "update_dependencies",
		Title:       "Update dependencies",
		Description: "Apply the exact dependency updates previously returned by check_updates. The checkId is single-use, and file changes since the check are rejected safely.",
		Annotations: &mcpsdk.ToolAnnotations{
			DestructiveHint: &destructive,
			OpenWorldHint:   &closedWorld,
		},
	}, server.handleUpdateDependencies)
}

// Run serves MCP requests over standard input and standard output until the client disconnects or ctx is cancelled.
func (server *Server) Run(ctx context.Context) error {
	return server.protocolServer.Run(ctx, &mcpsdk.StdioTransport{})
}

func (server *Server) handleCheckUpdates(ctx context.Context, request *mcpsdk.CallToolRequest, input checkUpdatesInput) (*mcpsdk.CallToolResult, checkUpdatesOutput, error) {
	workingDirectory, err := resolveDirectory(input.Directory)
	if err != nil {
		return nil, checkUpdatesOutput{}, err
	}

	var diagnostics []string
	log := func(format string, args ...any) {
		message := strings.TrimSpace(fmt.Sprintf(format, args...))
		if message != "" {
			diagnostics = append(diagnostics, message)
		}
	}
	filePath, registryType, err := server.detectDependency(workingDirectory, nil)
	if err != nil {
		return nil, checkUpdatesOutput{}, err
	}
	options := input.Options.sharedOptions()
	dependencies, err := server.parseDependencies(filePath, registryType, options, log)
	if err != nil {
		return nil, checkUpdatesOutput{}, fmt.Errorf("failed to parse dependencies: %w", err)
	}
	dependencies, err = dependency.Filter(dependencies, input.Targets, workingDirectory)
	if err != nil {
		return nil, checkUpdatesOutput{}, fmt.Errorf("invalid check targets: %w", err)
	}

	progressCallback := mcpProgressCallback(ctx, request)
	checkResult, err := server.checkOutdated(ctx, dependencies, registryType, options, workingDirectory, progressCallback, nil)
	if err != nil {
		return nil, checkUpdatesOutput{}, fmt.Errorf("failed to check dependencies: %w", err)
	}
	checkID := server.storeCheckedUpdates(checkedUpdates{
		filePath:         filePath,
		registryType:     registryType,
		options:          options,
		workingDirectory: workingDirectory,
		outdated:         append([]shared.OutdatedDependency(nil), checkResult.Outdated...),
	})
	output := newCheckUpdatesOutput(checkID, filePath, registryType, checkResult, diagnostics)
	return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: textWithStructuredJSON(checkSummary(output), output)}}}, output, nil
}

func (server *Server) handleUpdateDependencies(ctx context.Context, _ *mcpsdk.CallToolRequest, input updateDependenciesInput) (*mcpsdk.CallToolResult, updateDependenciesOutput, error) {
	if strings.TrimSpace(input.CheckID) == "" {
		return nil, updateDependenciesOutput{}, fmt.Errorf("checkId is required")
	}
	checked, exists := server.takeCheckedUpdates(input.CheckID)
	if !exists {
		return nil, updateDependenciesOutput{}, fmt.Errorf("unknown or already used checkId %q; call check_updates again", input.CheckID)
	}
	if err := server.updateDependencies(ctx, checked.filePath, checked.outdated, checked.registryType, checked.options, checked.workingDirectory, nil); err != nil {
		return nil, updateDependenciesOutput{}, fmt.Errorf("failed to update dependencies: %w", err)
	}
	output := updateDependenciesOutput{Updated: len(checked.outdated), Files: updatedFiles(checked.outdated, checked.filePath)}
	return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: textWithStructuredJSON(updateSummary(output), output)}}}, output, nil
}

func resolveDirectory(directory string) (string, error) {
	if strings.TrimSpace(directory) == "" {
		return "", fmt.Errorf("directory is required")
	}
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("failed to resolve directory: %w", err)
	}
	info, err := os.Stat(absoluteDirectory)
	if err != nil {
		return "", fmt.Errorf("failed to inspect directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("directory is not a directory: %s", absoluteDirectory)
	}
	return absoluteDirectory, nil
}

func mcpProgressCallback(ctx context.Context, request *mcpsdk.CallToolRequest) shared.ProgressFunc {
	if request == nil || request.Session == nil || request.Params == nil {
		return nil
	}
	progressToken := request.Params.GetProgressToken()
	if progressToken == nil {
		return nil
	}
	return func(progress shared.Progress) {
		_ = request.Session.NotifyProgress(ctx, &mcpsdk.ProgressNotificationParams{
			ProgressToken: progressToken,
			Progress:      float64(progress.Current),
			Total:         float64(progress.Total),
			Message:       fmt.Sprintf("Checking %s (%d/%d)", filepath.Base(progress.FilePath), progress.FileCurrent, progress.FileTotal),
		})
	}
}

func (server *Server) storeCheckedUpdates(checked checkedUpdates) string {
	server.checksMutex.Lock()
	defer server.checksMutex.Unlock()
	server.nextCheckID++
	checkID := fmt.Sprintf("check-%d", server.nextCheckID)
	server.checks[checkID] = checked
	return checkID
}

func (server *Server) takeCheckedUpdates(checkID string) (checkedUpdates, bool) {
	server.checksMutex.Lock()
	defer server.checksMutex.Unlock()
	checked, exists := server.checks[checkID]
	if exists {
		delete(server.checks, checkID)
	}
	return checked, exists
}
