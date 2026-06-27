package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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

func TestEvalJSWithTrackerResolvesAfterMissingExecutionContext(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	evalCalls := 0
	transport.RespondFunc("Runtime.evaluate", func(msg *juggler.Message) (json.RawMessage, *juggler.Error) {
		evalCalls++
		var params map[string]any
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			t.Fatalf("unmarshal Runtime.evaluate params: %v", err)
		}
		if params["executionContextId"] == nil {
			return nil, &juggler.Error{Message: `Expected "<root>.executionContextId" to be |string|; found |undefined|`}
		}
		if params["executionContextId"] != "exec-resolved" {
			t.Fatalf("executionContextId = %v, want exec-resolved", params["executionContextId"])
		}
		return json.RawMessage(`{"result":{"value":"ok"}}`), nil
	})
	transport.RespondFunc("Accessibility.getFullAXTree", func(*juggler.Message) (json.RawMessage, *juggler.Error) {
		transport.InjectEvent("session-resolve", "Page.frameAttached", map[string]any{
			"frameId": "frame-resolved",
		})
		transport.InjectEvent("session-resolve", "Runtime.executionContextCreated", map[string]any{
			"executionContextId": "exec-resolved",
			"auxData": map[string]any{
				"frameId": "frame-resolved",
			},
		})
		return json.RawMessage(`[]`), nil
	})
	client := juggler.NewClient(transport)
	defer client.Close()

	tracker := NewContextTracker(client)
	defer tracker.Close()

	got, err := evalJSWithTracker(client, tracker, "session-resolve", `"ok"`)
	if err != nil {
		t.Fatalf("evalJSWithTracker returned error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("eval result = %q, want ok", got)
	}
	if evalCalls != 1 {
		t.Fatalf("Runtime.evaluate calls = %d, want single context-bound eval", evalCalls)
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

func TestHandlePageSettledReturnsUsableAfterGraceForLongDynamicTimeout(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	evalCalls := 0
	transport.RespondFunc("Runtime.evaluate", func(*juggler.Message) (json.RawMessage, *juggler.Error) {
		evalCalls++
		state := fmt.Sprintf(`{"readyState":"complete","bodyLen":%d,"resourceCount":%d,"url":"https://detector.example/"}`, 2000+evalCalls, evalCalls)
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

	start := time.Now()
	result, err := handlePageSettled(client, tracker, json.RawMessage(`{"sessionId":"session-long-dynamic","timeout":20}`))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("handlePageSettled returned error: %v", err)
	}
	if result == nil || len(result.Content) == 0 {
		t.Fatalf("empty page_settled result: %#v", result)
	}
	if result.IsError {
		t.Fatalf("dynamic usable page should not be a tool error: %s", result.Content[0].Text)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("page_settled took %s, want early usable return", elapsed)
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

func TestHandlePageSettledRejectsFirefoxNetworkErrorPage(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondFunc("Runtime.evaluate", func(*juggler.Message) (json.RawMessage, *juggler.Error) {
		state := `{"readyState":"complete","bodyLen":5821,"resourceCount":0,"url":"https://overpoweredjs.com/","title":"Problem loading page","bodyText":"Secure Connection Failed PR_CONNECT_RESET_ERROR"}`
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

	result, err := handlePageSettled(client, tracker, json.RawMessage(`{"sessionId":"session-neterror","timeout":1}`))
	if err != nil {
		t.Fatalf("handlePageSettled returned dispatch error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("Firefox network error page should be a tool error, got %#v", result)
	}
	if got := result.Content[0].Text; !strings.Contains(got, "browser error page") || !strings.Contains(got, "PR_CONNECT_RESET_ERROR") {
		t.Fatalf("page_settled error = %q, want browser error with code", got)
	}
}

func TestHandlePageInfoRejectsFirefoxNetworkErrorPage(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondFunc("Runtime.evaluate", func(*juggler.Message) (json.RawMessage, *juggler.Error) {
		state := `{"url":"https://overpoweredjs.com/","title":"Problem loading page","readyState":"complete","bodyLen":5821,"bodyText":"Secure Connection Failed PR_CONNECT_RESET_ERROR","scrollY":0}`
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

	result, err := handleGetPageInfo(client, tracker, json.RawMessage(`{"sessionId":"session-neterror"}`))
	if err != nil {
		t.Fatalf("handleGetPageInfo returned dispatch error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("Firefox network error page should be a page_info tool error, got %#v", result)
	}
	if got := result.Content[0].Text; !strings.Contains(got, "browser error page") || !strings.Contains(got, "PR_CONNECT_RESET_ERROR") {
		t.Fatalf("page_info error = %q, want browser error with code", got)
	}
}

func TestHandlePageInfoRequestsFormIntelligence(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	var expression string
	transport.RespondFunc("Runtime.evaluate", func(msg *juggler.Message) (json.RawMessage, *juggler.Error) {
		var params map[string]any
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, &juggler.Error{Message: err.Error()}
		}
		expression, _ = params["expression"].(string)
		state := `{"url":"https://example.com/signup","title":"Signup","readyState":"complete","forms":1,"inputs":2,"buttons":1,"links":0,"formFields":[{"label":"Email","name":"email","placeholder":"you@example.com","autocomplete":"email","required":true,"validationMessage":"Please fill out this field."}],"activeElement":{"tag":"input","name":"email","label":"Email","editable":true},"bodyText":"ignored"}`
		data, err := json.Marshal(map[string]any{
			"result": map[string]any{"value": state},
		})
		if err != nil {
			return nil, &juggler.Error{Message: err.Error()}
		}
		return data, nil
	})
	client := juggler.NewClient(transport)
	defer client.Close()
	tracker := NewContextTracker(client)
	defer tracker.Close()

	result, err := handleGetPageInfo(client, tracker, json.RawMessage(`{"sessionId":"session-form-info"}`))
	if err != nil {
		t.Fatalf("handleGetPageInfo returned dispatch error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("page_info result = %#v, want success", result)
	}
	for _, want := range []string{"formFields", "label", "validationMessage", "activeElement", "shadowRoot"} {
		if !strings.Contains(expression, want) {
			t.Fatalf("page_info expression missing %q:\n%s", want, expression)
		}
	}
	text := result.Content[0].Text
	if !strings.Contains(text, `"formFields"`) || !strings.Contains(text, `"Email"`) || strings.Contains(text, "ignored") {
		t.Fatalf("page_info text missing form intelligence or leaked bodyText: %s", text)
	}
}

func TestHandleFillFormUsesFieldNameResolver(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	var expression string
	transport.RespondFunc("Runtime.evaluate", func(msg *juggler.Message) (json.RawMessage, *juggler.Error) {
		var params map[string]any
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, &juggler.Error{Message: err.Error()}
		}
		expression, _ = params["expression"].(string)
		data, err := json.Marshal(map[string]any{
			"result": map[string]any{"value": "ok"},
		})
		if err != nil {
			return nil, &juggler.Error{Message: err.Error()}
		}
		return data, nil
	})
	client := juggler.NewClient(transport)
	defer client.Close()
	tracker := NewContextTracker(client)
	defer tracker.Close()

	result, err := handleFillForm(client, tracker, json.RawMessage(`{"sessionId":"session-fill","fields":{"Email":"alice@example.com"}}`))
	if err != nil {
		t.Fatalf("handleFillForm returned dispatch error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("fill_form result = %#v, want success", result)
	}
	for _, want := range []string{"findEditableField", "placeholder", "labels", "shadowRoot"} {
		if !strings.Contains(expression, want) {
			t.Fatalf("fill_form expression missing %q:\n%s", want, expression)
		}
	}
	if got := result.Content[0].Text; !strings.Contains(got, "Filled 1/1 fields") {
		t.Fatalf("fill_form text = %q, want filled count", got)
	}
}

func TestHandleHumanTypeDispatchesKeyboardEvents(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	keyEvents := 0
	runtimeEvals := 0
	transport.RespondFunc("Page.dispatchKeyEvent", func(msg *juggler.Message) (json.RawMessage, *juggler.Error) {
		keyEvents++
		var params map[string]any
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, &juggler.Error{Message: err.Error()}
		}
		if params["type"] != "keydown" && params["type"] != "keyup" {
			return nil, &juggler.Error{Message: "unexpected key event type"}
		}
		data, err := json.Marshal(map[string]any{})
		if err != nil {
			return nil, &juggler.Error{Message: err.Error()}
		}
		return data, nil
	})
	transport.RespondFunc("Runtime.evaluate", func(msg *juggler.Message) (json.RawMessage, *juggler.Error) {
		runtimeEvals++
		data, err := json.Marshal(map[string]any{"result": map[string]any{"value": "ok"}})
		if err != nil {
			return nil, &juggler.Error{Message: err.Error()}
		}
		return data, nil
	})
	client := juggler.NewClient(transport)
	defer client.Close()
	tracker := NewContextTracker(client)
	defer tracker.Close()

	result, err := handleHumanType(client, tracker, json.RawMessage(`{"sessionId":"session-human-type","text":"12","wpm":1200}`))
	if err != nil {
		t.Fatalf("handleHumanType returned dispatch error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("human_type result = %#v, want success", result)
	}
	if keyEvents != 4 {
		t.Fatalf("keyEvents = %d, want keydown+keyup for each character", keyEvents)
	}
	if runtimeEvals != 0 {
		t.Fatalf("Runtime.evaluate calls = %d, want keyboard dispatch path without JS value mutation", runtimeEvals)
	}
}
