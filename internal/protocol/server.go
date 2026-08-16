package protocol

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/MilosRandelovic/bump-core/v2/parser"
	"github.com/MilosRandelovic/bump-core/v2/shared"
	"github.com/MilosRandelovic/bump-core/v2/updater"
)

const maxConcurrentRequests = 8

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

// Server processes newline-delimited JSON requests and emits correlated results, logs, and progress events.
type Server struct {
	reader             *bufio.Scanner
	encoder            *json.Encoder
	writeMutex         sync.Mutex
	writeErr           error
	activeMutex        sync.Mutex
	activeRequests     map[int]context.CancelFunc
	requestWorkers     sync.WaitGroup
	requestSlots       chan struct{}
	detectDependency   detectDependencyFunc
	parseDependencies  parseDependenciesFunc
	checkOutdated      checkOutdatedFunc
	updateDependencies updateDependenciesFunc
}

// NewServer returns a protocol server connected to standard input and standard output.
func NewServer() *Server {
	return NewServerWithIO(os.Stdin, os.Stdout)
}

// NewServerWithIO returns a protocol server that reads requests from input and writes responses to output.
// Input lines may be up to one MiB, and output writes are serialized across concurrent requests.
func NewServerWithIO(input io.Reader, output io.Writer) *Server {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	return &Server{
		reader:           scanner,
		encoder:          json.NewEncoder(output),
		activeRequests:   make(map[int]context.CancelFunc),
		requestSlots:     make(chan struct{}, maxConcurrentRequests),
		detectDependency: parser.AutoDetectDependencyFile,
		parseDependencies: func(filePath string, registryType shared.RegistryType, options shared.Options, log shared.LogFunc) ([]shared.Dependency, error) {
			return parser.ParseDependenciesWithLog(filePath, registryType, options, log)
		},
		checkOutdated:      updater.CheckOutdated,
		updateDependencies: updater.UpdateDependencies,
	}
}

// Run processes requests until input closes or a read or write fails.
// It waits for active request workers before returning and propagates the terminal I/O error.
func (s *Server) Run() error {
	for s.reader.Scan() {
		line := s.reader.Bytes()
		if len(line) == 0 {
			continue
		}

		var request Request
		if err := json.Unmarshal(line, &request); err != nil {
			s.sendError(0, fmt.Sprintf("invalid JSON: %v", err))
			if err := s.currentWriteError(); err != nil {
				s.cancelAllRequests()
				s.requestWorkers.Wait()
				return err
			}
			continue
		}

		if request.Method == RequestMethodCancel {
			if s.isRequestActive(request.ID) {
				s.sendError(request.ID, fmt.Sprintf("request id %d is already active", request.ID))
				continue
			}
			s.handleCancel(&request)
			if err := s.currentWriteError(); err != nil {
				s.cancelAllRequests()
				s.requestWorkers.Wait()
				return err
			}
			continue
		}

		ctx, cancel, registered := s.registerRequest(request.ID)
		if !registered {
			s.sendError(request.ID, fmt.Sprintf("request id %d is already active", request.ID))
			continue
		}
		if !s.acquireRequestSlot() {
			cancel()
			s.unregisterRequest(request.ID)
			s.sendCodedError(request.ID, ErrorCodeRequestLimitExceeded, fmt.Sprintf("too many active requests (maximum %d)", maxConcurrentRequests))
			continue
		}
		s.requestWorkers.Add(1)
		go func(request Request) {
			defer s.requestWorkers.Done()
			defer s.releaseRequestSlot()
			defer s.unregisterRequest(request.ID)
			defer cancel()
			s.handleRequest(ctx, &request)
		}(request)
	}

	if err := s.reader.Err(); err != nil {
		s.cancelAllRequests()
		s.requestWorkers.Wait()
		return fmt.Errorf("failed to read protocol input: %w", err)
	}
	s.requestWorkers.Wait()
	return s.currentWriteError()
}

func (s *Server) acquireRequestSlot() bool {
	select {
	case s.requestSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseRequestSlot() {
	<-s.requestSlots
}

func (s *Server) handleRequest(ctx context.Context, request *Request) {
	switch request.Method {
	case RequestMethodDetect:
		s.handleDetect(ctx, request)
	case RequestMethodCheck:
		s.handleCheck(ctx, request)
	case RequestMethodUpdate:
		s.handleUpdate(ctx, request)
	default:
		s.sendError(request.ID, fmt.Sprintf("unknown method: %s", request.Method))
	}
}

func (s *Server) handleDetect(ctx context.Context, request *Request) {
	var params DetectParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		s.sendError(request.ID, fmt.Sprintf("invalid detect params: %v", err))
		return
	}

	if params.Directory == "" {
		s.sendError(request.ID, "directory is required")
		return
	}

	if err := ctx.Err(); err != nil {
		s.sendError(request.ID, "detect cancelled")
		return
	}
	logFunc := s.makeLogFunc(request.ID)

	filePath, registryType, err := s.detectDependency(params.Directory, logFunc)
	if err != nil {
		s.sendError(request.ID, err.Error())
		return
	}
	if ctx.Err() != nil {
		s.sendError(request.ID, "detect cancelled")
		return
	}

	s.sendResult(request.ID, &DetectResult{
		FilePath:     filePath,
		RegistryType: registryType.String(),
	})
}

func (s *Server) handleCheck(ctx context.Context, request *Request) {
	var params CheckParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		s.sendError(request.ID, fmt.Sprintf("invalid check params: %v", err))
		return
	}

	registryType, err := ParseRegistryType(params.RegistryType)
	if err != nil {
		s.sendError(request.ID, fmt.Sprintf("invalid registry type: %s", params.RegistryType))
		return
	}
	if params.FilePath == "" {
		s.sendError(request.ID, "filePath is required")
		return
	}

	options := params.Options.ToOptions()
	var logFunc shared.LogFunc
	if options.Verbose {
		logFunc = s.makeLogFunc(request.ID)
	}

	dependencies, err := s.parseDependencies(params.FilePath, registryType, options, logFunc)
	if err != nil {
		s.sendError(request.ID, fmt.Sprintf("parse error: %v", err))
		return
	}

	progressCallback := func(progress shared.Progress) {
		s.sendProgress(request.ID, progress)
	}

	checkResult, err := s.checkOutdated(ctx, dependencies, registryType, options, "", progressCallback, logFunc)
	if err != nil {
		if ctx.Err() != nil {
			s.sendError(request.ID, "check cancelled")
			return
		}
		s.sendError(request.ID, fmt.Sprintf("check error: %v", err))
		return
	}
	if ctx.Err() != nil {
		s.sendError(request.ID, "check cancelled")
		return
	}

	s.sendResult(request.ID, FromCheckResult(checkResult))
}

func (s *Server) handleUpdate(ctx context.Context, request *Request) {
	var params UpdateParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		s.sendError(request.ID, fmt.Sprintf("invalid update params: %v", err))
		return
	}

	registryType, err := ParseRegistryType(params.RegistryType)
	if err != nil {
		s.sendError(request.ID, fmt.Sprintf("invalid registry type: %s", params.RegistryType))
		return
	}
	if params.FilePath == "" {
		s.sendError(request.ID, "filePath is required")
		return
	}

	options := params.Options.ToOptions()
	var logFunc shared.LogFunc
	if options.Verbose {
		logFunc = s.makeLogFunc(request.ID)
	}
	outdated, err := ToOutdatedDependencies(params.Outdated)
	if err != nil {
		s.sendError(request.ID, fmt.Sprintf("invalid outdated dependencies: %v", err))
		return
	}
	if err := ctx.Err(); err != nil {
		s.sendError(request.ID, "update cancelled")
		return
	}

	if err := s.updateDependencies(ctx, params.FilePath, outdated, registryType, options, "", logFunc); err != nil {
		if ctx.Err() != nil {
			s.sendError(request.ID, "update cancelled")
			return
		}
		s.sendError(request.ID, fmt.Sprintf("update error: %v", err))
		return
	}
	s.sendResult(request.ID, &UpdateResult{Updated: len(outdated)})
}

func (s *Server) makeLogFunc(id int) shared.LogFunc {
	return func(format string, args ...any) {
		message := fmt.Sprintf(format, args...)
		s.sendLog(id, message)
	}
}

func (s *Server) sendResult(id int, result interface{}) {
	s.encode(&Response{
		ID:     id,
		Type:   "result",
		Result: result,
	})
}

func (s *Server) sendError(id int, errorMessage string) {
	s.sendCodedError(id, "", errorMessage)
}

func (s *Server) sendCodedError(id int, code ErrorCode, errorMessage string) {
	s.encode(&Response{
		ID:    id,
		Type:  "error",
		Code:  code,
		Error: errorMessage,
	})
}

func (s *Server) sendLog(id int, message string) {
	s.encode(&LogMessage{
		Type:    "log",
		ID:      id,
		Message: message,
	})
}

func (s *Server) sendProgress(id int, progress shared.Progress) {
	s.encode(&ProgressMessage{
		Type:        "progress",
		ID:          id,
		FilePath:    progress.FilePath,
		FileCurrent: progress.FileCurrent,
		FileTotal:   progress.FileTotal,
		Current:     progress.Current,
		Total:       progress.Total,
	})
}

func (s *Server) encode(message any) {
	s.writeMutex.Lock()
	defer s.writeMutex.Unlock()
	if s.writeErr != nil {
		return
	}
	if err := s.encoder.Encode(message); err != nil {
		s.writeErr = fmt.Errorf("failed to write protocol response: %w", err)
	}
}

func (s *Server) currentWriteError() error {
	s.writeMutex.Lock()
	defer s.writeMutex.Unlock()
	return s.writeErr
}

func (s *Server) registerRequest(id int) (context.Context, context.CancelFunc, bool) {
	s.activeMutex.Lock()
	defer s.activeMutex.Unlock()
	if _, exists := s.activeRequests[id]; exists {
		return nil, nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.activeRequests[id] = cancel
	return ctx, cancel, true
}

func (s *Server) isRequestActive(id int) bool {
	s.activeMutex.Lock()
	defer s.activeMutex.Unlock()
	_, exists := s.activeRequests[id]
	return exists
}

func (s *Server) unregisterRequest(id int) {
	s.activeMutex.Lock()
	delete(s.activeRequests, id)
	s.activeMutex.Unlock()
}

func (s *Server) cancelRequest(id int) bool {
	s.activeMutex.Lock()
	cancel, exists := s.activeRequests[id]
	s.activeMutex.Unlock()
	if exists {
		cancel()
	}
	return exists
}

func (s *Server) cancelAllRequests() {
	s.activeMutex.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.activeRequests))
	for _, cancel := range s.activeRequests {
		cancels = append(cancels, cancel)
	}
	s.activeMutex.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (s *Server) handleCancel(request *Request) {
	var params CancelParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		s.sendError(request.ID, fmt.Sprintf("invalid cancel params: %v", err))
		return
	}
	if params.ID == nil {
		s.sendError(request.ID, "cancel target id is required")
		return
	}
	s.sendResult(request.ID, &CancelResult{Cancelled: s.cancelRequest(*params.ID)})
}
