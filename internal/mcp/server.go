package mcp

import (
	"bufio"
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

// Server is an MCP server that bridges OpenClaw to VulpineOS via stdio.
type Server struct {
	client *juggler.Client
	reader *bufio.Reader
	writer io.Writer
}

// NewServer creates an MCP server connected to the given Juggler client.
func NewServer(client *juggler.Client) *Server {
	return &Server{
		client: client,
		reader: bufio.NewReader(os.Stdin),
		writer: os.Stdout,
	}
}

// Run starts the MCP server loop, reading from stdin and writing to stdout.
func (s *Server) Run() error {
	log.SetOutput(os.Stderr) // Keep logs on stderr, MCP uses stdout

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

	result, err := handleToolCall(s.client, params.Name, params.Arguments)
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
