package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"vulpineos/internal/juggler"
)

const (
	mcpVersion    = "2024-11-05"
	serverName    = "vulpineos"
	serverVersion = "0.1.0"
)

// Server is an MCP server that bridges tool calls to VulpineOS via stdio.
type Server struct {
	client      *juggler.Client
	tracker     *ContextTracker
	screenshots *ScreenshotTracker
	loops       *LoopDetector
	cancels     []func()
	reader      *bufio.Reader
	writer      io.Writer
}

// NewServer creates an MCP server connected to the given Juggler client.
func NewServer(client *juggler.Client) *Server {
	s := &Server{
		client:      client,
		tracker:     NewContextTracker(client),
		screenshots: NewScreenshotTracker(),
		loops:       NewLoopDetector(3),
		reader:      bufio.NewReader(os.Stdin),
		writer:      os.Stdout,
	}
	s.cancels = append(s.cancels, client.SubscribeWithCancel("Browser.detachedFromTarget", func(_ string, params json.RawMessage) {
		var ev struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(params, &ev); err == nil && ev.SessionID != "" {
			s.cleanupSession(ev.SessionID)
		}
	}))
	return s
}

func (s *Server) Close() {
	for _, cancel := range s.cancels {
		cancel()
	}
	s.cancels = nil
	if s.tracker != nil {
		s.tracker.Close()
	}
}

func (s *Server) cleanupSession(sessionID string) {
	if s.screenshots != nil {
		s.screenshots.Delete(sessionID)
	}
	resetSnapshotProfile(sessionID)
	if s.tracker != nil {
		s.tracker.RemoveSession(sessionID)
	}
}

// Run starts the MCP server loop, reading from stdin and writing to stdout.
func (s *Server) Run() error {
	log.SetOutput(os.Stderr) // Keep logs on stderr, MCP uses stdout
	defer s.Close()

	for {
		line, err := s.reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read stdin: %w", err)
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			log.Printf("mcp: invalid JSON-RPC: %v", err)
			continue
		}

		resp := s.handleRequest(&req)
		if resp != nil {
			data, _ := json.Marshal(resp)
			data = append(data, '\n')
			s.writer.Write(data)
		}
	}
}

func (s *Server) handleRequest(req *Request) *Response {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "notifications/initialized":
		// Client acknowledges init — no response needed
		return nil
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	case "ping":
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]string{}}
	default:
		log.Printf("mcp: unknown method: %s", req.Method)
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32601, Message: "Method not found: " + req.Method},
		}
	}
}

func (s *Server) handleInitialize(req *Request) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: InitializeResult{
			ProtocolVersion: mcpVersion,
			ServerInfo: ServerInfo{
				Name:    serverName,
				Version: serverVersion,
			},
			Capabilities: Capabilities{
				Tools: &ToolsCapability{},
			},
		},
	}
}

func (s *Server) handleToolsList(req *Request) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolsListResult{Tools: tools()},
	}
}

func (s *Server) handleToolsCall(req *Request) *Response {
	var params ToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32602, Message: "Invalid params: " + err.Error()},
		}
	}

	// Check for action loops before executing
	if warning := s.loops.Check("default", params.Name, string(params.Arguments)); warning != "" {
		log.Printf("mcp: loop detected: %s", params.Name)
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  textResult(warning),
		}
	}

	// Reset loop history on navigation (new page = fresh context)
	if params.Name == "vulpine_navigate" {
		s.loops.Reset("default")
	}

	result, err := handleToolCallFull(context.Background(), s.client, s.tracker, s.screenshots, params.Name, params.Arguments)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32603, Message: err.Error()},
		}
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	}
}
