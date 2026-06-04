package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"vulpineos/internal/juggler"
	"vulpineos/internal/testutil"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		max   int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc..."},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.max)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
		}
	}
}

func TestScreenshotTracker_SetGet(t *testing.T) {
	tracker := NewScreenshotTracker()

	// Empty
	if got := tracker.Get("session1"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}

	// Set and get
	tracker.Set("session1", "base64data1")
	if got := tracker.Get("session1"); got != "base64data1" {
		t.Errorf("expected 'base64data1', got %q", got)
	}

	// Different sessions are isolated
	tracker.Set("session2", "base64data2")
	if got := tracker.Get("session1"); got != "base64data1" {
		t.Errorf("session1 should still be 'base64data1', got %q", got)
	}
	if got := tracker.Get("session2"); got != "base64data2" {
		t.Errorf("session2 should be 'base64data2', got %q", got)
	}

	// Overwrite
	tracker.Set("session1", "updated")
	if got := tracker.Get("session1"); got != "updated" {
		t.Errorf("expected 'updated', got %q", got)
	}
}

func TestNewScreenshotTracker(t *testing.T) {
	tracker := NewScreenshotTracker()
	if tracker == nil {
		t.Fatal("NewScreenshotTracker returned nil")
	}
	if tracker.screenshots == nil {
		t.Fatal("screenshots map is nil")
	}
}

func TestToolsCount(t *testing.T) {
	defs := tools()
	// 12 original + 8 new = 20
	if len(defs) < 20 {
		t.Errorf("expected at least 20 tools, got %d", len(defs))
	}
}

func TestToolsHaveRequiredFields(t *testing.T) {
	for _, tool := range tools() {
		if tool.Name == "" {
			t.Error("tool has empty name")
		}
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
	}
}

func TestNewToolNames(t *testing.T) {
	defs := tools()
	expected := []string{
		"vulpine_wait",
		"vulpine_find",
		"vulpine_verify",
		"vulpine_screenshot_diff",
		"vulpine_page_settled",
		"vulpine_select_option",
		"vulpine_fill_form",
		"vulpine_page_info",
	}
	nameSet := make(map[string]bool)
	for _, d := range defs {
		nameSet[d.Name] = true
	}
	for _, name := range expected {
		if !nameSet[name] {
			t.Errorf("missing expected tool: %s", name)
		}
	}
}

func TestEvalJSHelper_NotPanicsOnNilClient(t *testing.T) {
	// evalJS should return error, not panic, when client is nil
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("evalJS panicked: %v", r)
		}
	}()
	_, err := evalJS(nil, "session", "1+1")
	if err == nil {
		t.Error("expected error with nil client")
	}
}

func TestHandleWaitPollsUntilTextAppears(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	evalCalls := 0
	transport.RespondFunc("Runtime.evaluate", func(*juggler.Message) (json.RawMessage, *juggler.Error) {
		evalCalls++
		text := "loading"
		if evalCalls >= 3 {
			text = "loading\nReady now"
		}
		data, err := json.Marshal(map[string]any{
			"result": map[string]any{"value": text},
		})
		if err != nil {
			t.Fatalf("marshal eval result: %v", err)
		}
		return data, nil
	})
	client := juggler.NewClient(transport)
	defer client.Close()

	tracker := NewContextTracker(client)
	defer tracker.Close()

	result, err := handleWait(client, tracker, json.RawMessage(`{"sessionId":"session-wait","condition":"text","text":"Ready now","timeout":2}`))
	if err != nil {
		t.Fatalf("handleWait returned error: %v", err)
	}
	if result == nil || len(result.Content) == 0 {
		t.Fatalf("empty wait result: %#v", result)
	}
	if result.IsError {
		t.Fatalf("wait should not fail on first missing text: %s", result.Content[0].Text)
	}
	if evalCalls < 3 {
		t.Fatalf("evalCalls = %d, want polling until text appears", evalCalls)
	}
	if got := result.Content[0].Text; !strings.Contains(got, "Condition met") {
		t.Fatalf("wait text = %q, want condition met", got)
	}
}

func TestHandleWaitUsesTrackedExecutionContext(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondJSON("Runtime.evaluate", map[string]any{
		"result": map[string]any{"value": "https://example.com/dashboard"},
	})
	client := juggler.NewClient(transport)
	defer client.Close()

	tracker := NewContextTracker(client)
	defer tracker.Close()
	tracker.mu.Lock()
	tracker.contexts["session-eval"] = &SessionContext{
		ExecutionContextID: "exec-current",
		FrameID:            "frame-current",
	}
	tracker.mu.Unlock()

	result, err := handleWait(client, tracker, json.RawMessage(`{"sessionId":"session-eval","condition":"urlContains","text":"dashboard","timeout":1}`))
	if err != nil {
		t.Fatalf("handleWait returned error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("handleWait result = %#v, want success", result)
	}

	call, ok := transport.LastCall("Runtime.evaluate")
	if !ok {
		t.Fatal("Runtime.evaluate was not called")
	}
	var params map[string]any
	if err := json.Unmarshal(call.Params, &params); err != nil {
		t.Fatalf("unmarshal eval params: %v", err)
	}
	if params["executionContextId"] != "exec-current" {
		t.Fatalf("executionContextId = %v, want exec-current", params["executionContextId"])
	}
}

func TestHandlePageSettledReturnsUsableForDynamicPage(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	evalCalls := 0
	transport.RespondFunc("Runtime.evaluate", func(*juggler.Message) (json.RawMessage, *juggler.Error) {
		evalCalls++
		state := fmt.Sprintf(`{"readyState":"complete","bodyLen":%d,"resourceCount":%d,"url":"https://app.example/dashboard"}`, 1000+evalCalls, evalCalls)
		data, err := json.Marshal(map[string]any{
			"result": map[string]any{"value": state},
		})
		if err != nil {
			t.Fatalf("marshal eval result: %v", err)
		}
		return data, nil
	})
	client := juggler.NewClient(transport)
	defer client.Close()

	tracker := NewContextTracker(client)
	defer tracker.Close()

	result, err := handlePageSettled(client, tracker, json.RawMessage(`{"sessionId":"session-dynamic","timeout":1}`))
	if err != nil {
		t.Fatalf("handlePageSettled returned error: %v", err)
	}
	if result == nil || len(result.Content) == 0 {
		t.Fatalf("empty page_settled result: %#v", result)
	}
	if result.IsError {
		t.Fatalf("dynamic usable page should not be a tool error: %s", result.Content[0].Text)
	}
	if got := result.Content[0].Text; !strings.Contains(got, "Page usable:") || !strings.Contains(got, "still changing") {
		t.Fatalf("page_settled text = %q, want usable dynamic-page message", got)
	}
}

func TestHandlePageSettledIgnoresResourceChurnWhenDOMStable(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	evalCalls := 0
	transport.RespondFunc("Runtime.evaluate", func(*juggler.Message) (json.RawMessage, *juggler.Error) {
		evalCalls++
		state := fmt.Sprintf(`{"readyState":"complete","bodyLen":1200,"resourceCount":%d,"url":"https://app.example/dashboard"}`, evalCalls)
		data, err := json.Marshal(map[string]any{
			"result": map[string]any{"value": state},
		})
		if err != nil {
			t.Fatalf("marshal eval result: %v", err)
		}
		return data, nil
	})
	client := juggler.NewClient(transport)
	defer client.Close()

	tracker := NewContextTracker(client)
	defer tracker.Close()

	result, err := handlePageSettled(client, tracker, json.RawMessage(`{"sessionId":"session-churn","timeout":2}`))
	if err != nil {
		t.Fatalf("handlePageSettled returned error: %v", err)
	}
	if result == nil || len(result.Content) == 0 {
		t.Fatalf("empty page_settled result: %#v", result)
	}
	if result.IsError {
		t.Fatalf("stable DOM with resource churn should not be a tool error: %s", result.Content[0].Text)
	}
	if got := result.Content[0].Text; !strings.Contains(got, "Page settled:") {
		t.Fatalf("page_settled text = %q, want settled", got)
	}
}
