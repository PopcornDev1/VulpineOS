package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"vulpineos/internal/juggler"
	"vulpineos/internal/testutil"
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
	optionalParams := []string{"profile", "retry", "maxDepth", "maxNodes", "maxTextLength", "viewportOnly"}
	for _, p := range optionalParams {
		if _, ok := snap.InputSchema.Properties[p]; !ok {
			t.Errorf("vulpine_snapshot missing %q property", p)
		}
	}
	// viewportOnly should be boolean
	if snap.InputSchema.Properties["viewportOnly"].Type != "boolean" {
		t.Errorf("viewportOnly type = %q, want 'boolean'", snap.InputSchema.Properties["viewportOnly"].Type)
	}
	if snap.InputSchema.Properties["retry"].Type != "boolean" {
		t.Errorf("retry type = %q, want 'boolean'", snap.InputSchema.Properties["retry"].Type)
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

func TestHandleSnapshotProfilesAndRetry(t *testing.T) {
	resetSnapshotProfile("session-snapshot")

	transport := newScriptedJugglerTransport()
	client := juggler.NewClient(transport)
	defer client.Close()

	requests := make(chan map[string]interface{}, 2)
	go func() {
		for {
			select {
			case <-transport.closed:
				return
			case req := <-transport.outgoing:
				var params map[string]interface{}
				if err := json.Unmarshal(req.Params, &params); err == nil {
					requests <- params
				}
				transport.incoming <- &juggler.Message{
					ID:     req.ID,
					Result: json.RawMessage(`{"snapshot":{"v":1,"title":"Test","url":"https://example.test","nodes":[]},"truncated":true}`),
				}
			}
		}
	}()

	first, err := handleSnapshot(client, json.RawMessage(`{"sessionId":"session-snapshot"}`))
	if err != nil {
		t.Fatalf("first snapshot returned error: %v", err)
	}
	firstParams := <-requests
	if firstParams["profile"] != "compact" {
		t.Fatalf("first profile = %v, want compact", firstParams["profile"])
	}
	if firstParams["maxNodes"] != float64(180) {
		t.Fatalf("first maxNodes = %v, want 180", firstParams["maxNodes"])
	}

	var firstPayload map[string]interface{}
	if err := json.Unmarshal([]byte(first.Content[0].Text), &firstPayload); err != nil {
		t.Fatalf("unmarshal first payload: %v", err)
	}
	if firstPayload["profile"] != "compact" {
		t.Fatalf("annotated first profile = %v, want compact", firstPayload["profile"])
	}
	if _, ok := firstPayload["retryHint"].(string); !ok {
		t.Fatalf("expected retryHint in truncated compact payload: %#v", firstPayload)
	}

	second, err := handleSnapshot(client, json.RawMessage(`{"sessionId":"session-snapshot","retry":true}`))
	if err != nil {
		t.Fatalf("retry snapshot returned error: %v", err)
	}
	secondParams := <-requests
	if secondParams["profile"] != "expanded" {
		t.Fatalf("retry profile = %v, want expanded", secondParams["profile"])
	}
	if secondParams["maxNodes"] != float64(360) {
		t.Fatalf("retry maxNodes = %v, want 360", secondParams["maxNodes"])
	}

	var secondPayload map[string]interface{}
	if err := json.Unmarshal([]byte(second.Content[0].Text), &secondPayload); err != nil {
		t.Fatalf("unmarshal second payload: %v", err)
	}
	if secondPayload["profile"] != "expanded" {
		t.Fatalf("annotated retry profile = %v, want expanded", secondPayload["profile"])
	}
}

func TestHandleSnapshotFallsBackToAXOnOptimizedDOMProtocolError(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondError("Page.getOptimizedDOM", `Expected "<root>.executionContextId" to be |string|; found |undefined|`)
	transport.RespondJSON("Accessibility.getFullAXTree", []map[string]any{
		{"role": "document", "name": "Fallback"},
	})
	client := juggler.NewClient(transport)
	defer client.Close()

	result, err := handleSnapshot(client, json.RawMessage(`{"sessionId":"session-snapshot-fallback"}`))
	if err != nil {
		t.Fatalf("snapshot returned error: %v", err)
	}
	if result == nil || len(result.Content) == 0 {
		t.Fatalf("empty snapshot result: %#v", result)
	}
	if result.IsError {
		t.Fatalf("snapshot should fall back to AX tree, got error: %s", result.Content[0].Text)
	}
	if got := result.Content[0].Text; !strings.Contains(got, "Fallback") {
		t.Fatalf("snapshot fallback text = %q, want AX payload", got)
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

func TestHandleClickRefFallbackUsesTrackedExecutionContext(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondError("Page.resolveRef", "method not found")
	transport.RespondJSON("Accessibility.getFullAXTree", []map[string]any{
		{"ref": "@e1", "role": "button", "name": "Submit", "tag": "button"},
	})
	transport.RespondFunc("Runtime.evaluate", func(msg *juggler.Message) (json.RawMessage, *juggler.Error) {
		var params map[string]any
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			t.Fatalf("unmarshal Runtime.evaluate params: %v", err)
		}
		if params["executionContextId"] != "exec-ref" {
			return nil, &juggler.Error{Message: `Expected "<root>.executionContextId" to be |string|; found |undefined|`}
		}
		return json.RawMessage(`{"result":{"value":"{\"x\":12,\"y\":34,\"found\":true}"}}`), nil
	})
	transport.RespondJSON("Page.dispatchMouseEvent", map[string]any{})
	client := juggler.NewClient(transport)
	defer client.Close()

	tracker := NewContextTracker(client)
	defer tracker.Close()
	tracker.mu.Lock()
	tracker.contexts["session-ref"] = &SessionContext{
		ExecutionContextID: "exec-ref",
		FrameID:            "frame-ref",
	}
	tracker.mu.Unlock()

	result, err := handleToolCall(context.Background(), client, tracker, "vulpine_click_ref", json.RawMessage(`{"sessionId":"session-ref","ref":"@e1"}`))
	if err != nil {
		t.Fatalf("click_ref returned error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("click_ref should succeed with tracked context, got %#v", result)
	}
	if calls := transport.CallsByMethod("Page.dispatchMouseEvent"); len(calls) != 2 {
		t.Fatalf("mouse event calls = %d, want mousedown+mouseup", len(calls))
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

func respondRuntimeEvaluateValue(t testing.TB, transport *testutil.FakeJugglerTransport, value string) {
	t.Helper()
	transport.RespondFunc("Runtime.evaluate", func(*juggler.Message) (json.RawMessage, *juggler.Error) {
		data, err := json.Marshal(map[string]any{
			"result": map[string]any{"value": value},
		})
		if err != nil {
			t.Fatalf("marshal Runtime.evaluate response: %v", err)
		}
		return data, nil
	})
}

func TestHandleNavigateDoesNotRequireTrackedExecutionContext(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondError("Accessibility.getFullAXTree", "AX probe should not be required for navigation")
	transport.RespondJSON("Page.navigate", map[string]any{"navigationId": "nav-1"})
	respondRuntimeEvaluateValue(t, transport, `{"readyState":"complete","bodyLen":1200,"resourceCount":4,"url":"https://example.com/"}`)
	client := juggler.NewClient(transport)
	defer client.Close()

	tracker := NewContextTracker(client)
	defer tracker.Close()

	result, err := handleNavigate(client, tracker, json.RawMessage(`{"sessionId":"session-new","url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("handleNavigate returned error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("handleNavigate result = %#v, want success", result)
	}
	if calls := transport.CallsByMethod("Accessibility.getFullAXTree"); len(calls) != 0 {
		t.Fatalf("navigate made %d AX probe call(s), want none", len(calls))
	}
	navCall, ok := transport.LastCall("Page.navigate")
	if !ok {
		t.Fatal("Page.navigate was not called")
	}
	var params map[string]any
	if err := json.Unmarshal(navCall.Params, &params); err != nil {
		t.Fatalf("unmarshal navigate params: %v", err)
	}
	if params["url"] != "https://example.com" {
		t.Fatalf("navigate url = %v, want https://example.com", params["url"])
	}
	if _, ok := params["frameId"]; ok {
		t.Fatalf("navigate params included frameId without tracked frame: %#v", params)
	}
}

func TestHandleNavigateUsesKnownFrameWithoutExecutionContext(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondError("Accessibility.getFullAXTree", "AX probe should not be required for navigation")
	transport.RespondJSON("Page.navigate", map[string]any{"navigationId": "nav-1"})
	respondRuntimeEvaluateValue(t, transport, `{"readyState":"complete","bodyLen":1200,"resourceCount":4,"url":"https://example.com/known"}`)
	client := juggler.NewClient(transport)
	defer client.Close()

	tracker := NewContextTracker(client)
	defer tracker.Close()
	tracker.mu.Lock()
	tracker.contexts["session-known-frame"] = &SessionContext{FrameID: "frame-known"}
	tracker.mu.Unlock()

	result, err := handleNavigate(client, tracker, json.RawMessage(`{"sessionId":"session-known-frame","url":"https://example.com/known"}`))
	if err != nil {
		t.Fatalf("handleNavigate returned error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("handleNavigate result = %#v, want success", result)
	}
	if calls := transport.CallsByMethod("Accessibility.getFullAXTree"); len(calls) != 0 {
		t.Fatalf("navigate made %d AX probe call(s), want none", len(calls))
	}
	navCall, ok := transport.LastCall("Page.navigate")
	if !ok {
		t.Fatal("Page.navigate was not called")
	}
	var params map[string]any
	if err := json.Unmarshal(navCall.Params, &params); err != nil {
		t.Fatalf("unmarshal navigate params: %v", err)
	}
	if params["frameId"] != "frame-known" {
		t.Fatalf("navigate frameId = %v, want frame-known", params["frameId"])
	}
}

func TestHandleNavigateFallsBackToFrameProbeWhenFrameIDRequired(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	navigateCalls := 0
	transport.RespondFunc("Page.navigate", func(*juggler.Message) (json.RawMessage, *juggler.Error) {
		navigateCalls++
		if navigateCalls == 1 {
			return nil, &juggler.Error{Message: "frameId is required"}
		}
		return json.RawMessage(`{"navigationId":"nav-2"}`), nil
	})
	transport.RespondFunc("Accessibility.getFullAXTree", func(*juggler.Message) (json.RawMessage, *juggler.Error) {
		transport.InjectEvent("session-legacy", "Page.frameAttached", map[string]any{
			"frameId": "frame-legacy",
		})
		return json.RawMessage(`{}`), nil
	})
	respondRuntimeEvaluateValue(t, transport, `{"readyState":"complete","bodyLen":1200,"resourceCount":4,"url":"https://example.com/legacy"}`)
	client := juggler.NewClient(transport)
	defer client.Close()

	tracker := NewContextTracker(client)
	defer tracker.Close()

	result, err := handleNavigate(client, tracker, json.RawMessage(`{"sessionId":"session-legacy","url":"https://example.com/legacy"}`))
	if err != nil {
		t.Fatalf("handleNavigate returned error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("handleNavigate result = %#v, want success", result)
	}
	if navigateCalls != 2 {
		t.Fatalf("navigateCalls = %d, want first failure plus retry", navigateCalls)
	}
	navCall, ok := transport.LastCall("Page.navigate")
	if !ok {
		t.Fatal("Page.navigate was not called")
	}
	var params map[string]any
	if err := json.Unmarshal(navCall.Params, &params); err != nil {
		t.Fatalf("unmarshal navigate params: %v", err)
	}
	if params["frameId"] != "frame-legacy" {
		t.Fatalf("retry frameId = %v, want frame-legacy", params["frameId"])
	}
}

func TestHandleNavigateFailsWhenPageRemainsAboutBlank(t *testing.T) {
	previousTimeout := navigateVerificationTimeout
	previousInterval := navigateVerificationPollInterval
	navigateVerificationTimeout = 20 * time.Millisecond
	navigateVerificationPollInterval = time.Millisecond
	t.Cleanup(func() {
		navigateVerificationTimeout = previousTimeout
		navigateVerificationPollInterval = previousInterval
	})

	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondJSON("Page.navigate", map[string]any{"navigationId": "nav-blank"})
	respondRuntimeEvaluateValue(t, transport, `{"readyState":"complete","bodyLen":0,"resourceCount":0,"url":"about:blank"}`)
	client := juggler.NewClient(transport)
	defer client.Close()

	tracker := NewContextTracker(client)
	defer tracker.Close()

	result, err := handleNavigate(client, tracker, json.RawMessage(`{"sessionId":"session-blank","url":"https://browserleaks.com/javascript"}`))
	if err != nil {
		t.Fatalf("handleNavigate returned error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("handleNavigate result = %#v, want tool error for blank post-navigation document", result)
	}
	if len(result.Content) == 0 {
		t.Fatalf("handleNavigate returned empty error content")
	}
	got := result.Content[0].Text
	if !strings.Contains(got, "did not load usable content") || !strings.Contains(got, "about:blank") {
		t.Fatalf("handleNavigate error = %q, want blank-page diagnostic", got)
	}
}

func TestHandleNavigateResolvesExecutionContextWhenDefaultEvaluateRequiresIt(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondJSON("Page.navigate", map[string]any{"navigationId": "nav-needs-context"})
	transport.RespondFunc("Runtime.evaluate", func(msg *juggler.Message) (json.RawMessage, *juggler.Error) {
		var params map[string]any
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			t.Fatalf("unmarshal Runtime.evaluate params: %v", err)
		}
		if params["executionContextId"] == nil {
			return nil, &juggler.Error{Message: `Expected "<root>.executionContextId" to be |string|; found |undefined|`}
		}
		if params["executionContextId"] != "exec-fresh" {
			return nil, &juggler.Error{Message: fmt.Sprintf("Failed to find execution context with id = %v", params["executionContextId"])}
		}
		data, err := json.Marshal(map[string]any{
			"result": map[string]any{"value": `{"readyState":"complete","bodyLen":900,"resourceCount":3,"url":"https://example.com/context"}`},
		})
		if err != nil {
			t.Fatalf("marshal Runtime.evaluate response: %v", err)
		}
		return data, nil
	})
	transport.RespondFunc("Accessibility.getFullAXTree", func(*juggler.Message) (json.RawMessage, *juggler.Error) {
		transport.InjectEvent("session-needs-context", "Page.frameAttached", map[string]any{
			"frameId": "frame-main",
		})
		transport.InjectEvent("session-needs-context", "Runtime.executionContextCreated", map[string]any{
			"executionContextId": "exec-fresh",
			"auxData": map[string]any{
				"frameId": "frame-main",
			},
		})
		return json.RawMessage(`{}`), nil
	})
	client := juggler.NewClient(transport)
	defer client.Close()

	tracker := NewContextTracker(client)
	defer tracker.Close()

	result, err := handleNavigate(client, tracker, json.RawMessage(`{"sessionId":"session-needs-context","url":"https://example.com/context"}`))
	if err != nil {
		t.Fatalf("handleNavigate returned error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("handleNavigate result = %#v, want success after context resolve", result)
	}
}

type scriptedJugglerTransport struct {
	incoming chan *juggler.Message
	outgoing chan *juggler.Message
	closed   chan struct{}
	once     sync.Once
}

func newScriptedJugglerTransport() *scriptedJugglerTransport {
	return &scriptedJugglerTransport{
		incoming: make(chan *juggler.Message, 16),
		outgoing: make(chan *juggler.Message, 16),
		closed:   make(chan struct{}),
	}
}

func (t *scriptedJugglerTransport) Send(msg *juggler.Message) error {
	select {
	case <-t.closed:
		return fmt.Errorf("transport closed")
	case t.outgoing <- msg:
		return nil
	}
}

func (t *scriptedJugglerTransport) Receive() (*juggler.Message, error) {
	select {
	case <-t.closed:
		return nil, fmt.Errorf("transport closed")
	case msg := <-t.incoming:
		return msg, nil
	}
}

func (t *scriptedJugglerTransport) Close() error {
	t.once.Do(func() { close(t.closed) })
	return nil
}

func TestHandleNewContextFiltersAttachEventsByBrowserContext(t *testing.T) {
	transport := newScriptedJugglerTransport()
	client := juggler.NewClient(transport)
	defer client.Close()

	go func() {
		for {
			select {
			case <-transport.closed:
				return
			case req := <-transport.outgoing:
				switch req.Method {
				case "Browser.createBrowserContext":
					transport.incoming <- &juggler.Message{ID: req.ID, Result: json.RawMessage(`{"browserContextId":"ctx-new"}`)}
				case "Browser.newPage":
					transport.incoming <- &juggler.Message{
						Method: "Browser.attachedToTarget",
						Params: json.RawMessage(`{"sessionId":"session-other","targetInfo":{"browserContextId":"ctx-other"}}`),
					}
					transport.incoming <- &juggler.Message{
						Method: "Browser.attachedToTarget",
						Params: json.RawMessage(`{"sessionId":"session-new","targetInfo":{"browserContextId":"ctx-new"}}`),
					}
					transport.incoming <- &juggler.Message{ID: req.ID, Result: json.RawMessage(`{"targetId":"target-new"}`)}
				default:
					transport.incoming <- &juggler.Message{ID: req.ID, Result: json.RawMessage(`{}`)}
				}
			}
		}
	}()

	result, err := handleNewContext(client, nil)
	if err != nil {
		t.Fatalf("handleNewContext returned error: %v", err)
	}
	if result == nil || len(result.Content) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}

	var payload struct {
		ContextID string `json:"contextId"`
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal result text: %v", err)
	}
	if payload.ContextID != "ctx-new" {
		t.Fatalf("contextId = %q, want ctx-new", payload.ContextID)
	}
	if payload.SessionID != "session-new" {
		t.Fatalf("sessionId = %q, want session-new", payload.SessionID)
	}
}

func TestHandleNewContextRemovesContextOnNewPageFailure(t *testing.T) {
	transport := newScriptedJugglerTransport()
	client := juggler.NewClient(transport)
	defer client.Close()

	removed := make(chan string, 1)
	go func() {
		for {
			select {
			case <-transport.closed:
				return
			case req := <-transport.outgoing:
				switch req.Method {
				case "Browser.createBrowserContext":
					transport.incoming <- &juggler.Message{ID: req.ID, Result: json.RawMessage(`{"browserContextId":"ctx-leak"}`)}
				case "Browser.newPage":
					transport.incoming <- &juggler.Message{ID: req.ID, Error: &juggler.Error{Message: "new page failed"}}
				case "Browser.removeBrowserContext":
					var params struct {
						BrowserContextID string `json:"browserContextId"`
					}
					_ = json.Unmarshal(req.Params, &params)
					removed <- params.BrowserContextID
					transport.incoming <- &juggler.Message{ID: req.ID, Result: json.RawMessage(`{}`)}
				default:
					transport.incoming <- &juggler.Message{ID: req.ID, Result: json.RawMessage(`{}`)}
				}
			}
		}
	}()

	result, err := handleNewContext(client, nil)
	if err != nil {
		t.Fatalf("handleNewContext returned error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("handleNewContext result = %#v, want tool error", result)
	}

	select {
	case contextID := <-removed:
		if contextID != "ctx-leak" {
			t.Fatalf("removed context = %q, want ctx-leak", contextID)
		}
	case <-time.After(time.Second):
		t.Fatal("Browser.removeBrowserContext was not called")
	}
}

func TestHandleNewContextRemovesContextOnAttachTimeout(t *testing.T) {
	previousTimeout := newContextAttachTimeout
	newContextAttachTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		newContextAttachTimeout = previousTimeout
	})

	transport := newScriptedJugglerTransport()
	client := juggler.NewClient(transport)
	defer client.Close()

	removed := make(chan string, 1)
	go func() {
		for {
			select {
			case <-transport.closed:
				return
			case req := <-transport.outgoing:
				switch req.Method {
				case "Browser.createBrowserContext":
					transport.incoming <- &juggler.Message{ID: req.ID, Result: json.RawMessage(`{"browserContextId":"ctx-timeout"}`)}
				case "Browser.newPage":
					transport.incoming <- &juggler.Message{ID: req.ID, Result: json.RawMessage(`{"targetId":"target-timeout"}`)}
				case "Browser.removeBrowserContext":
					var params struct {
						BrowserContextID string `json:"browserContextId"`
					}
					_ = json.Unmarshal(req.Params, &params)
					removed <- params.BrowserContextID
					transport.incoming <- &juggler.Message{ID: req.ID, Result: json.RawMessage(`{}`)}
				default:
					transport.incoming <- &juggler.Message{ID: req.ID, Result: json.RawMessage(`{}`)}
				}
			}
		}
	}()

	result, err := handleNewContext(client, nil)
	if err != nil {
		t.Fatalf("handleNewContext returned error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("handleNewContext result = %#v, want tool error", result)
	}

	select {
	case contextID := <-removed:
		if contextID != "ctx-timeout" {
			t.Fatalf("removed context = %q, want ctx-timeout", contextID)
		}
	case <-time.After(time.Second):
		t.Fatal("Browser.removeBrowserContext was not called")
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
	_, err := handleToolCall(context.Background(), nil, nil, "vulpine_nonexistent", nil)
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
			result, err := handleToolCall(context.Background(), nil, nil, name, badJSON)
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
