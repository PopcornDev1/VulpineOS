package agentcore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"vulpineos/internal/juggler"
	"vulpineos/internal/testutil"
)

func TestBrowserToolsStripSessionIDAndCurate(t *testing.T) {
	tools := BrowserTools()
	if len(tools) == 0 {
		t.Fatal("expected browser tools")
	}

	byName := map[string]ToolDef{}
	for _, td := range tools {
		if td.Type != "function" {
			t.Errorf("tool %s type = %q, want function", td.Function.Name, td.Type)
		}
		byName[td.Function.Name] = td

		// sessionId must never be exposed to the model.
		props, _ := td.Function.Parameters["properties"].(map[string]interface{})
		if _, ok := props[sessionIDArg]; ok {
			t.Errorf("tool %s still exposes %s in properties", td.Function.Name, sessionIDArg)
		}
		if req, ok := td.Function.Parameters["required"].([]string); ok {
			for _, r := range req {
				if r == sessionIDArg {
					t.Errorf("tool %s still requires %s", td.Function.Name, sessionIDArg)
				}
			}
		}
	}

	// A representative core tool must be present and keep its non-session args.
	nav, ok := byName["vulpine_navigate"]
	if !ok {
		t.Fatal("vulpine_navigate missing from exposed tools")
	}
	props := nav.Function.Parameters["properties"].(map[string]interface{})
	if _, ok := props["url"]; !ok {
		t.Error("vulpine_navigate should still expose url")
	}
	req, _ := nav.Function.Parameters["required"].([]string)
	foundURL := false
	for _, r := range req {
		if r == "url" {
			foundURL = true
		}
	}
	if !foundURL {
		t.Error("vulpine_navigate should still require url")
	}

	// Lifecycle/image/extension tools must be excluded.
	for _, excluded := range []string{"vulpine_new_context", "vulpine_close_context", "vulpine_screenshot", "vulpine_annotated_screenshot"} {
		if _, ok := byName[excluded]; ok {
			t.Errorf("tool %s should be excluded from the native toolset", excluded)
		}
	}

	if !IsBrowserTool("vulpine_navigate") || IsBrowserTool("vulpine_new_context") {
		t.Error("IsBrowserTool allow-list mismatch")
	}
}

func TestBrowserToolsetOpenTabWaitsForTrackerBeforeNavigate(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondJSON("Accessibility.getFullAXTree", []map[string]any{})
	transport.RespondFunc("Browser.newPage", func(*juggler.Message) (json.RawMessage, *juggler.Error) {
		go func() {
			transport.InjectEvent("", "Browser.attachedToTarget", map[string]any{
				"sessionId": "session-tab",
				"targetInfo": map[string]any{
					"browserContextId": "ctx-agent",
				},
			})
			time.Sleep(100 * time.Millisecond)
			transport.InjectEvent("session-tab", "Page.frameAttached", map[string]any{
				"frameId": "frame-tab",
			})
			transport.InjectEvent("session-tab", "Runtime.executionContextCreated", map[string]any{
				"executionContextId": "exec-tab",
				"auxData": map[string]any{
					"frameId": "frame-tab",
				},
			})
		}()
		return json.RawMessage(`{}`), nil
	})
	transport.RespondJSON("Page.navigate", map[string]any{"navigationId": "nav-tab"})
	transport.RespondJSON("Runtime.evaluate", map[string]any{
		"result": map[string]any{"value": `{"readyState":"complete","bodyLen":100,"resourceCount":1,"url":"https://example.test/"}`},
	})
	client := juggler.NewClient(transport)
	defer client.Close()

	ts := NewBrowserToolset(client, "ctx-agent", "session-primary")
	defer ts.Close()

	result, isErr, err := ts.openTab(context.Background(), `{"url":"https://example.test/"}`)
	if err != nil {
		t.Fatalf("openTab returned error: %v", err)
	}
	if isErr {
		t.Fatalf("openTab returned tool error: %s", result)
	}
	navCalls := transport.CallsByMethod("Page.navigate")
	if len(navCalls) == 0 {
		t.Fatal("Page.navigate was not called")
	}
	var params map[string]any
	if err := json.Unmarshal(navCalls[0].Params, &params); err != nil {
		t.Fatalf("unmarshal first navigate params: %v", err)
	}
	if params["frameId"] != "frame-tab" {
		t.Fatalf("first navigate frameId = %v, want frame-tab", params["frameId"])
	}
}

func TestBrowserToolsetLoopWarningIsErrorClass(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondJSON("Accessibility.getFullAXTree", []map[string]any{})
	transport.RespondJSON("Runtime.evaluate", map[string]any{
		"result": map[string]any{"value": `{"url":"https://example.test","title":"Example","readyState":"complete"}`},
	})
	client := juggler.NewClient(transport)
	defer client.Close()

	ts := NewBrowserToolset(client, "ctx-agent", "session-loop")
	defer ts.Close()

	var (
		result string
		isErr  bool
		err    error
	)
	for i := 0; i < 4; i++ {
		result, isErr, err = ts.Dispatch(context.Background(), "vulpine_page_info", `{}`)
		if err != nil {
			t.Fatalf("Dispatch call %d returned error: %v", i+1, err)
		}
	}
	if !isErr {
		t.Fatalf("loop warning isErr = false, want true (result: %q)", result)
	}
	if !strings.Contains(result, "not making progress") {
		t.Fatalf("loop warning result = %q, want no-progress warning", result)
	}
}

func TestBrowserToolsetClassifiesVerifyFailAsError(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondJSON("Accessibility.getFullAXTree", []map[string]any{})
	transport.RespondJSON("Runtime.evaluate", map[string]any{
		"result": map[string]any{"value": "Still loading"},
	})
	client := juggler.NewClient(transport)
	defer client.Close()

	ts := NewBrowserToolset(client, "ctx-agent", "session-verify")
	defer ts.Close()

	result, isErr, err := ts.Dispatch(context.Background(), "vulpine_verify", `{"check":"text","selector":"#status","expected":"Done"}`)
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if !strings.HasPrefix(result, "FAIL:") {
		t.Fatalf("verify result = %q, want FAIL", result)
	}
	if !isErr {
		t.Fatalf("verify FAIL result isErr = false, want true")
	}
}

func TestBrowserToolsetCloseExtraTabsKeepsPrimaryTab(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	client := juggler.NewClient(transport)
	defer client.Close()

	ts := NewBrowserToolset(client, "ctx-agent", "session-1")
	ts.tabs = []string{"session-1", "session-2", "session-3"}
	ts.active = 2

	if err := ts.CloseExtraTabs(); err != nil {
		t.Fatalf("CloseExtraTabs failed: %v", err)
	}

	if len(ts.tabs) != 1 || ts.tabs[0] != "session-1" {
		t.Fatalf("tabs after cleanup = %#v, want primary session only", ts.tabs)
	}
	if ts.active != 0 {
		t.Fatalf("active tab = %d, want 0", ts.active)
	}

	calls := transport.CallsByMethod("Page.close")
	if len(calls) != 2 {
		t.Fatalf("Page.close calls = %d, want 2", len(calls))
	}
	for i, call := range calls {
		wantSession := []string{"session-2", "session-3"}[i]
		if call.SessionID != wantSession {
			t.Fatalf("Page.close call %d session = %q, want %q", i, call.SessionID, wantSession)
		}
		var params struct {
			RunBeforeUnload bool `json:"runBeforeUnload"`
		}
		if err := json.Unmarshal(call.Params, &params); err != nil {
			t.Fatalf("unmarshal close params: %v", err)
		}
		if params.RunBeforeUnload {
			t.Fatal("CloseExtraTabs should skip beforeunload prompts")
		}
	}
}
