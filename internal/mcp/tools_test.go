package mcp

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestToolSchemaProperties(t *testing.T) {
	toolList := tools()
	toolMap := make(map[string]ToolDefinition)
	for _, tool := range toolList {
		toolMap[tool.Name] = tool
	}

	// vulpine_navigate should have url and sessionId properties
	nav := toolMap["vulpine_navigate"]
	if _, ok := nav.InputSchema.Properties["url"]; !ok {
		t.Error("vulpine_navigate missing 'url' property")
	}
	if nav.InputSchema.Properties["url"].Type != "string" {
		t.Errorf("vulpine_navigate url type = %q, want 'string'", nav.InputSchema.Properties["url"].Type)
	}

	// vulpine_snapshot should have optional params
	snap := toolMap["vulpine_snapshot"]
	optionalParams := []string{"maxDepth", "maxNodes", "maxTextLength", "viewportOnly"}
	for _, p := range optionalParams {
		if _, ok := snap.InputSchema.Properties[p]; !ok {
			t.Errorf("vulpine_snapshot missing %q property", p)
		}
	}
	// viewportOnly should be boolean
	if snap.InputSchema.Properties["viewportOnly"].Type != "boolean" {
		t.Errorf("viewportOnly type = %q, want 'boolean'", snap.InputSchema.Properties["viewportOnly"].Type)
	}

	// vulpine_click should have x, y as number type
	click := toolMap["vulpine_click"]
	for _, coord := range []string{"x", "y"} {
		prop, ok := click.InputSchema.Properties[coord]
		if !ok {
			t.Errorf("vulpine_click missing %q property", coord)
		} else if prop.Type != "number" {
			t.Errorf("vulpine_click %s type = %q, want 'number'", coord, prop.Type)
		}
	}

	// vulpine_scroll deltaY should be number
	scroll := toolMap["vulpine_scroll"]
	if scroll.InputSchema.Properties["deltaY"].Type != "number" {
		t.Errorf("vulpine_scroll deltaY type = %q, want 'number'", scroll.InputSchema.Properties["deltaY"].Type)
	}

	// vulpine_type should have text property
	typ := toolMap["vulpine_type"]
	if _, ok := typ.InputSchema.Properties["text"]; !ok {
		t.Error("vulpine_type missing 'text' property")
	}

	// vulpine_new_context should have no required fields
	newCtx := toolMap["vulpine_new_context"]
	if len(newCtx.InputSchema.Required) != 0 {
		t.Errorf("vulpine_new_context required = %v, want empty", newCtx.InputSchema.Required)
	}

	// vulpine_close_context should have contextId property
	closeCtx := toolMap["vulpine_close_context"]
	if _, ok := closeCtx.InputSchema.Properties["contextId"]; !ok {
		t.Error("vulpine_close_context missing 'contextId' property")
	}
}

func TestToolSchemaRefTools(t *testing.T) {
	toolList := tools()
	toolMap := make(map[string]ToolDefinition)
	for _, tool := range toolList {
		toolMap[tool.Name] = tool
	}

	// vulpine_click_ref should require sessionId and ref
	clickRef := toolMap["vulpine_click_ref"]
	reqSet := make(map[string]bool)
	for _, r := range clickRef.InputSchema.Required {
		reqSet[r] = true
	}
	if !reqSet["sessionId"] || !reqSet["ref"] {
		t.Errorf("vulpine_click_ref required = %v, want sessionId and ref", clickRef.InputSchema.Required)
	}

	// vulpine_type_ref should require sessionId, ref, and text
	typeRef := toolMap["vulpine_type_ref"]
	reqSet = make(map[string]bool)
	for _, r := range typeRef.InputSchema.Required {
		reqSet[r] = true
	}
	if !reqSet["sessionId"] || !reqSet["ref"] || !reqSet["text"] {
		t.Errorf("vulpine_type_ref required = %v, want sessionId, ref, text", typeRef.InputSchema.Required)
	}

	// vulpine_hover_ref should require sessionId and ref
	hoverRef := toolMap["vulpine_hover_ref"]
	reqSet = make(map[string]bool)
	for _, r := range hoverRef.InputSchema.Required {
		reqSet[r] = true
	}
	if !reqSet["sessionId"] || !reqSet["ref"] {
		t.Errorf("vulpine_hover_ref required = %v, want sessionId and ref", hoverRef.InputSchema.Required)
	}
}

func TestTextResult(t *testing.T) {
	r := textResult("hello world")
	if r == nil {
		t.Fatal("textResult returned nil")
	}
	if len(r.Content) != 1 {
		t.Fatalf("content length = %d, want 1", len(r.Content))
	}
	if r.Content[0].Type != "text" {
		t.Errorf("content type = %q, want 'text'", r.Content[0].Type)
	}
	if r.Content[0].Text != "hello world" {
		t.Errorf("content text = %q, want 'hello world'", r.Content[0].Text)
	}
	if r.IsError {
		t.Error("textResult should not be an error")
	}
}

func TestErrorResult(t *testing.T) {
	err := fmt.Errorf("something went wrong")
	r := errorResult(err)
	if r == nil {
		t.Fatal("errorResult returned nil")
	}
	if !r.IsError {
		t.Error("errorResult should have IsError=true")
	}
	if len(r.Content) != 1 {
		t.Fatalf("content length = %d, want 1", len(r.Content))
	}
	if r.Content[0].Text != "something went wrong" {
		t.Errorf("content text = %q, want 'something went wrong'", r.Content[0].Text)
	}
}

func TestHandleToolCallUnknownTool(t *testing.T) {
	_, err := handleToolCall(nil, nil, "vulpine_nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestHandleToolCallBadJSON(t *testing.T) {
	// Tools that parse JSON args should return error results for invalid JSON
	badJSON := json.RawMessage(`{invalid}`)
	toolNames := []string{
		"vulpine_navigate", "vulpine_snapshot", "vulpine_click",
		"vulpine_type", "vulpine_screenshot", "vulpine_scroll",
		"vulpine_close_context", "vulpine_get_ax_tree",
		"vulpine_click_ref", "vulpine_type_ref", "vulpine_hover_ref",
	}
	for _, name := range toolNames {
		t.Run(name, func(t *testing.T) {
			result, err := handleToolCall(nil, nil, name, badJSON)
			// Should return a result (not a Go error) with IsError=true
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if !result.IsError {
				t.Error("expected IsError=true for bad JSON")
			}
		})
	}
}

func TestToolCallResultJSON(t *testing.T) {
	r := &ToolCallResult{
		Content: []ContentBlock{{Type: "text", Text: "test"}},
		IsError: false,
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	// isError should be omitted when false
	var m map[string]interface{}
	json.Unmarshal(data, &m)
	if _, ok := m["isError"]; ok {
		t.Error("isError should be omitted when false (omitempty)")
	}
}

func TestToolCallResultWithErrorJSON(t *testing.T) {
	r := &ToolCallResult{
		Content: []ContentBlock{{Type: "text", Text: "error msg"}},
		IsError: true,
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var m map[string]interface{}
	json.Unmarshal(data, &m)
	if _, ok := m["isError"]; !ok {
		t.Error("isError should be present when true")
	}
}

