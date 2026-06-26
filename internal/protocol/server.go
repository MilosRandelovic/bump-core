package protocol

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/MilosRandelovic/bump-core/parser"
	"github.com/MilosRandelovic/bump-core/shared"
	"github.com/MilosRandelovic/bump-core/updater"
)

// Server handles JSON protocol communication over stdin/stdout
type Server struct {
	reader  *bufio.Scanner
	encoder *json.Encoder
}

// NewServer creates a new protocol server using stdin/stdout
func NewServer() *Server {
	return NewServerWithIO(os.Stdin, os.Stdout)
}

// NewServerWithIO creates a new protocol server with custom I/O streams
func NewServerWithIO(input io.Reader, output io.Writer) *Server {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	return &Server{
		reader:  scanner,
		encoder: json.NewEncoder(output),
	}
}

// Run starts the server loop, reading requests from stdin and writing responses to stdout
func (s *Server) Run() error {
	for s.reader.Scan() {
		line := s.reader.Bytes()
		if len(line) == 0 {
			continue
		}

		var request Request
		if err := json.Unmarshal(line, &request); err != nil {
			s.sendError(0, fmt.Sprintf("invalid JSON: %v", err))
			continue
		}

		s.handleRequest(&request)
	}

	return s.reader.Err()
}

func (s *Server) handleRequest(request *Request) {
	switch request.Method {
	case "detect":
		s.handleDetect(request)
	case "check":
		s.handleCheck(request)
	case "update":
		s.handleUpdate(request)
	default:
		s.sendError(request.ID, fmt.Sprintf("unknown method: %s", request.Method))
	}
}

func (s *Server) handleDetect(request *Request) {
	var params DetectParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		s.sendError(request.ID, fmt.Sprintf("invalid detect params: %v", err))
		return
	}

	if params.Directory == "" {
		s.sendError(request.ID, "directory is required")
		return
	}

	logFunc := s.makeLogFunc()

	filePath, registryType, err := parser.AutoDetectDependencyFile(params.Directory, logFunc)
	if err != nil {
		s.sendError(request.ID, err.Error())
		return
	}

	s.sendResult(request.ID, &DetectResult{
		FilePath:     filePath,
		RegistryType: registryType.String(),
	})
}

func (s *Server) handleCheck(request *Request) {
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

	options := params.Options.ToOptions()
	logFunc := s.makeLogFunc()

	dependencies, err := parser.ParseDependencies(params.FilePath, registryType, options)
	if err != nil {
		s.sendError(request.ID, fmt.Sprintf("parse error: %v", err))
		return
	}

	progressCallback := func(current, total int) {
		s.sendProgress(current, total)
	}

	checkResult, err := updater.CheckOutdated(context.Background(), dependencies, registryType, options, "", progressCallback, logFunc)
	if err != nil {
		s.sendError(request.ID, fmt.Sprintf("check error: %v", err))
		return
	}

	s.sendResult(request.ID, FromCheckResult(checkResult))
}

func (s *Server) handleUpdate(request *Request) {
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

	options := params.Options.ToOptions()
	logFunc := s.makeLogFunc()
	outdated := ToOutdatedDependencies(params.Outdated)

	if err := updater.UpdateDependencies(params.FilePath, outdated, registryType, options, "", logFunc); err != nil {
		s.sendError(request.ID, fmt.Sprintf("update error: %v", err))
		return
	}

	s.sendResult(request.ID, &UpdateResult{Updated: len(outdated)})
}

func (s *Server) makeLogFunc() shared.LogFunc {
	return func(format string, args ...any) {
		message := fmt.Sprintf(format, args...)
		s.sendLog(message)
	}
}

func (s *Server) sendResult(id int, result interface{}) {
	s.encoder.Encode(&Response{
		ID:     id,
		Type:   "result",
		Result: result,
	})
}

func (s *Server) sendError(id int, errorMessage string) {
	s.encoder.Encode(&Response{
		ID:    id,
		Type:  "error",
		Error: errorMessage,
	})
}

func (s *Server) sendLog(message string) {
	s.encoder.Encode(&LogMessage{
		Type:    "log",
		Message: message,
	})
}

func (s *Server) sendProgress(current, total int) {
	s.encoder.Encode(&ProgressMessage{
		Type:    "progress",
		Current: current,
		Total:   total,
	})
}
