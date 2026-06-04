package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"vulpineos/internal/extensions/extensionstest"
	"vulpineos/internal/juggler"
)

func newSmokeServer(t *testing.T) (*Server, *routedTransport) {
	t.Helper()
	transport := newRoutedTransport()
	client := juggler.NewClient(transport)
	t.Cleanup(func() {
		_ = client.Close()
	})
	return NewServer(client), transport
}

func callTool(t *testing.T, s *Server, id int, name string, args interface{}) *Response {
	t.Helper()
	var rawArgs json.RawMessage
	if args != nil {
		data, err := json.Marshal(args)
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		rawArgs = data
	}
	params, err := json.Marshal(ToolCallParams{
		Name:      name,
		Arguments: rawArgs,
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	resp := s.handleRequest(&Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params:  params,
	})
	if resp == nil {
		t.Fatal("expected response")
	}
	return resp
}

func requireToolResult(t *testing.T, resp *Response) *ToolCallResult {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected rpc error: %+v", resp.Error)
	}
	result, ok := resp.Result.(*ToolCallResult)
	if !ok {
		t.Fatalf("unexpected result type %T", resp.Result)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content blocks")
	}
	return result
}

func TestMCPServerSmoke_ToolsListAndValidation(t *testing.T) {
	s, _ := newSmokeServer(t)

	listResp := s.handleRequest(&Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	})
	if listResp == nil || listResp.Error != nil {
		t.Fatalf("tools/list failed: %+v", listResp)
	}
	list, ok := listResp.Result.(ToolsListResult)
	if !ok {
		t.Fatalf("unexpected tools/list result type %T", listResp.Result)
	}

	required := map[string]bool{
		"vulpine_navigate":            false,
		"vulpine_select_option":       false,
		"vulpine_list_mobile_devices": false,
		"vulpine_click_label":         false,
		"vulpine_human_type":          false,
		"vulpine_screenshot_diff":     false,
	}
	for _, tool := range list.Tools {
		if _, ok := required[tool.Name]; ok {
			required[tool.Name] = true
		}
	}
	for name, seen := range required {
		if !seen {
			t.Fatalf("tools/list missing %s", name)
		}
	}

	resp := callTool(t, s, 2, "vulpine_select_option", map[string]interface{}{
		"sessionId": "s1",
		"selector":  "#country",
	})
	result := requireToolResult(t, resp)
	if !result.IsError {
		t.Fatal("expected tool-level validation error")
	}
	if got := result.Content[0].Text; !strings.Contains(got, "either value or text is required") {
		t.Fatalf("unexpected validation text %q", got)
	}
}

func TestMCPServerSmoke_InvalidParamsAndUnknownTool(t *testing.T) {
	s, _ := newSmokeServer(t)

	resp := s.handleRequest(&Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(`{invalid}`),
	})
	if resp == nil || resp.Error == nil {
		t.Fatalf("expected invalid params rpc error, got %+v", resp)
	}
	if resp.Error.Code != -32602 {
		t.Fatalf("rpc error code = %d want -32602", resp.Error.Code)
	}

	resp = callTool(t, s, 2, "vulpine_not_real", map[string]interface{}{})
	if resp.Error == nil {
		t.Fatalf("expected rpc error for unknown tool, got %+v", resp)
	}
	if resp.Error.Code != -32603 {
		t.Fatalf("rpc error code = %d want -32603", resp.Error.Code)
	}
}

func TestMCPServerSmoke_ExtensionUnavailableGraceful(t *testing.T) {
	withFakeMobile(t, &extensionstest.FakeMobileBridge{})
	s, _ := newSmokeServer(t)

	resp := callTool(t, s, 1, "vulpine_list_mobile_devices", map[string]interface{}{})
	result := requireToolResult(t, resp)
	if !result.IsError {
		t.Fatal("expected unavailable error result")
	}
	if got := result.Content[0].Text; !strings.Contains(got, "mobile bridge unavailable") {
		t.Fatalf("unexpected unavailable text %q", got)
	}
}

func TestMCPServerSmoke_LoopDetectorAndNavigationReset(t *testing.T) {
	withFakeMobile(t, &extensionstest.FakeMobileBridge{})
	s, transport := newSmokeServer(t)

	s.tracker.mu.Lock()
	s.tracker.contexts["s1"] = &SessionContext{
		ExecutionContextID: "ctx-1",
		FrameID:            "frame-1",
	}
	s.tracker.mu.Unlock()

	transport.on("Page.navigate", func(req *juggler.Message) *juggler.Message {
		return okResultMessage(t, map[string]interface{}{"navigationId": "nav-1"})
	})
	transport.on("Runtime.evaluate", func(req *juggler.Message) *juggler.Message {
		return okResultMessage(t, map[string]interface{}{
			"result": map[string]interface{}{
				"value": `{"readyState":"complete","bodyLen":1000,"resourceCount":2,"url":"https://example.com"}`,
			},
		})
	})

	for i := 0; i < 3; i++ {
		resp := callTool(t, s, i+1, "vulpine_list_mobile_devices", map[string]interface{}{})
		result := requireToolResult(t, resp)
		if !result.IsError {
			t.Fatalf("call %d: expected unavailable error", i+1)
		}
	}

	resp := callTool(t, s, 4, "vulpine_list_mobile_devices", map[string]interface{}{})
	result := requireToolResult(t, resp)
	if result.IsError {
		t.Fatalf("expected loop warning result, got error %q", result.Content[0].Text)
	}
	if got := result.Content[0].Text; !strings.Contains(got, "not making progress") {
		t.Fatalf("unexpected loop warning %q", got)
	}

	resp = callTool(t, s, 5, "vulpine_navigate", map[string]interface{}{
		"sessionId": "s1",
		"url":       "https://example.com",
	})
	result = requireToolResult(t, resp)
	if result.IsError {
		t.Fatalf("navigate failed: %q", result.Content[0].Text)
	}

	resp = callTool(t, s, 6, "vulpine_list_mobile_devices", map[string]interface{}{})
	result = requireToolResult(t, resp)
	if !result.IsError {
		t.Fatalf("expected unavailable error after reset, got %q", result.Content[0].Text)
	}
	if got := result.Content[0].Text; !strings.Contains(got, "mobile bridge unavailable") {
		t.Fatalf("unexpected post-reset text %q", got)
	}
}
