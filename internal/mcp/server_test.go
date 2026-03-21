package mcp

import (
	"testing"
)

func TestToolDefinitions(t *testing.T) {
	toolList := tools()
	if len(toolList) != 9 {
		t.Errorf("expected 9 tools, got %d", len(toolList))
	}

	expectedNames := map[string]bool{
		"vulpine_navigate":      false,
		"vulpine_snapshot":      false,
		"vulpine_click":         false,
		"vulpine_type":          false,
		"vulpine_screenshot":    false,
		"vulpine_scroll":        false,
		"vulpine_new_context":   false,
		"vulpine_close_context": false,
		"vulpine_get_ax_tree":   false,
	}

	for _, tool := range toolList {
		if tool.Name == "" {
			t.Error("tool missing name")
			continue
		}
		if tool.Description == "" {
			t.Errorf("tool %s missing description", tool.Name)
		}
		if tool.InputSchema.Type != "object" {
			t.Errorf("tool %s schema type = %q, want 'object'", tool.Name, tool.InputSchema.Type)
		}

		if _, ok := expectedNames[tool.Name]; !ok {
			t.Errorf("unexpected tool name: %s", tool.Name)
		} else {
			expectedNames[tool.Name] = true
		}
	}

	for name, found := range expectedNames {
		if !found {
			t.Errorf("expected tool %q not found in tool list", name)
		}
	}
}

func TestToolDefinitionsRequiredFields(t *testing.T) {
	toolList := tools()

	// Tools that require sessionId
	sessionTools := map[string]bool{
		"vulpine_navigate":   true,
		"vulpine_snapshot":   true,
		"vulpine_click":      true,
		"vulpine_type":       true,
		"vulpine_screenshot": true,
		"vulpine_scroll":     true,
		"vulpine_get_ax_tree": true,
	}

	for _, tool := range toolList {
		if sessionTools[tool.Name] {
			found := false
			for _, req := range tool.InputSchema.Required {
				if req == "sessionId" {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("tool %s should require sessionId", tool.Name)
			}
		}
	}

	// vulpine_click requires x and y
	for _, tool := range toolList {
		if tool.Name == "vulpine_click" {
			reqSet := map[string]bool{}
			for _, r := range tool.InputSchema.Required {
				reqSet[r] = true
			}
			if !reqSet["x"] || !reqSet["y"] {
				t.Errorf("vulpine_click should require x and y, got required=%v", tool.InputSchema.Required)
			}
		}

		// vulpine_navigate requires url
		if tool.Name == "vulpine_navigate" {
			reqSet := map[string]bool{}
			for _, r := range tool.InputSchema.Required {
				reqSet[r] = true
			}
			if !reqSet["url"] {
				t.Error("vulpine_navigate should require url")
			}
		}

		// vulpine_close_context requires contextId
		if tool.Name == "vulpine_close_context" {
			reqSet := map[string]bool{}
			for _, r := range tool.InputSchema.Required {
				reqSet[r] = true
			}
			if !reqSet["contextId"] {
				t.Error("vulpine_close_context should require contextId")
			}
		}
	}
}

func TestHandleToolCallUnknown(t *testing.T) {
	_, err := handleToolCall(nil, "nonexistent_tool", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestInitializeResponse(t *testing.T) {
	s := &Server{}
	req := &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	}

	resp := s.handleRequest(req)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.ID != 1 {
		t.Errorf("response ID = %v, want 1", resp.ID)
	}
	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}

	result, ok := resp.Result.(InitializeResult)
	if !ok {
		t.Fatalf("result is not InitializeResult, got %T", resp.Result)
	}
	if result.ProtocolVersion != mcpVersion {
		t.Errorf("protocolVersion = %q, want %q", result.ProtocolVersion, mcpVersion)
	}
	if result.ServerInfo.Name != serverName {
		t.Errorf("serverInfo.name = %q, want %q", result.ServerInfo.Name, serverName)
	}
	if result.ServerInfo.Version != serverVersion {
		t.Errorf("serverInfo.version = %q, want %q", result.ServerInfo.Version, serverVersion)
	}
	if result.Capabilities.Tools == nil {
		t.Error("expected non-nil tools capability")
	}
}

func TestToolsListResponse(t *testing.T) {
	s := &Server{}
	req := &Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	}

	resp := s.handleRequest(req)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}

	result, ok := resp.Result.(ToolsListResult)
	if !ok {
		t.Fatalf("result is not ToolsListResult, got %T", resp.Result)
	}
	if len(result.Tools) != 9 {
		t.Errorf("expected 9 tools, got %d", len(result.Tools))
	}
}

func TestPingResponse(t *testing.T) {
	s := &Server{}
	req := &Request{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "ping",
	}

	resp := s.handleRequest(req)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}
}

func TestUnknownMethodResponse(t *testing.T) {
	s := &Server{}
	req := &Request{
		JSONRPC: "2.0",
		ID:      4,
		Method:  "unknown/method",
	}

	resp := s.handleRequest(req)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("error code = %d, want -32601", resp.Error.Code)
	}
}

func TestNotificationNoResponse(t *testing.T) {
	s := &Server{}
	req := &Request{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}

	resp := s.handleRequest(req)
	if resp != nil {
		t.Error("expected nil response for notification")
	}
}
