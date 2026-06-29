package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
		if _, ok := props["session_id"]; ok {
			t.Errorf("tool %s still exposes injected session_id in properties", td.Function.Name)
		}
		if req, ok := td.Function.Parameters["required"].([]string); ok {
			for _, r := range req {
				if r == sessionIDArg || r == "session_id" {
					t.Errorf("tool %s still requires injected session arg %s", td.Function.Name, r)
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

	for _, recoveryTool := range []string{"vulpine_scroll_into_view", "vulpine_element_status", "vulpine_observe"} {
		if _, ok := byName[recoveryTool]; !ok {
			t.Errorf("%s missing from exposed tools", recoveryTool)
		}
		if !IsBrowserTool(recoveryTool) {
			t.Errorf("IsBrowserTool(%q) = false, want true", recoveryTool)
		}
	}

	for _, recoveryTool := range []string{"vulpine_annotated_screenshot", "vulpine_click_label"} {
		if _, ok := byName[recoveryTool]; !ok {
			t.Errorf("%s missing from exposed recovery tools", recoveryTool)
		}
		if !IsBrowserTool(recoveryTool) {
			t.Errorf("IsBrowserTool(%q) = false, want true", recoveryTool)
		}
	}

	for _, challengeTool := range []string{"vulpine_captcha_detect", "vulpine_captcha_solve", "vulpine_captcha_apply"} {
		if _, ok := byName[challengeTool]; !ok {
			t.Errorf("%s missing from exposed challenge tools", challengeTool)
		}
		if !IsBrowserTool(challengeTool) {
			t.Errorf("IsBrowserTool(%q) = false, want true", challengeTool)
		}
	}

	// Lifecycle/raw-image tools must be excluded.
	for _, excluded := range []string{"vulpine_new_context", "vulpine_close_context", "vulpine_screenshot"} {
		if _, ok := byName[excluded]; ok {
			t.Errorf("tool %s should be excluded from the native toolset", excluded)
		}
	}

	if !IsBrowserTool("vulpine_navigate") || IsBrowserTool("vulpine_new_context") {
		t.Error("IsBrowserTool allow-list mismatch")
	}
}

func TestBrowserToolsetDispatchesAnnotatedScreenshotFallback(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondJSON("Page.getAnnotatedScreenshot", map[string]any{
		"image": "aGVsbG8=",
		"elements": []map[string]any{{
			"label":    "@1",
			"role":     "button",
			"name":     "Continue",
			"objectId": "obj-1",
			"frameId":  "frame-1",
		}},
	})
	client := juggler.NewClient(transport)
	defer client.Close()

	ts := NewBrowserToolset(client, "ctx-agent", "session-shot")
	defer ts.Close()

	result, isErr, err := ts.Dispatch(context.Background(), "vulpine_annotated_screenshot", `{"maxElements":20}`)
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if isErr {
		t.Fatalf("Dispatch returned tool error: %s", result)
	}
	if !strings.Contains(result, "[image captured]") || !strings.Contains(result, `"Continue"`) || strings.Contains(result, "aGVsbG8=") {
		t.Fatalf("annotated screenshot result = %q", result)
	}
	call, ok := transport.LastCall("Page.getAnnotatedScreenshot")
	if !ok {
		t.Fatal("Page.getAnnotatedScreenshot was not called")
	}
	if call.SessionID != "session-shot" {
		t.Fatalf("annotated screenshot session = %q, want session-shot", call.SessionID)
	}
}

func TestBrowserToolsetDispatchesClickLabelWithInjectedSession(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondJSON("Page.getAnnotatedScreenshot", map[string]any{
		"image": "aGVsbG8=",
		"elements": []map[string]any{{
			"label":    "@1",
			"role":     "button",
			"name":     "Continue",
			"objectId": "obj-1",
			"frameId":  "frame-1",
		}},
	})
	transport.RespondJSON("Page.scrollIntoViewIfNeeded", map[string]any{})
	transport.RespondJSON("Page.getContentQuads", map[string]any{
		"quads": []map[string]any{{
			"p1": map[string]any{"x": 10, "y": 20},
			"p2": map[string]any{"x": 30, "y": 20},
			"p3": map[string]any{"x": 30, "y": 40},
			"p4": map[string]any{"x": 10, "y": 40},
		}},
	})
	transport.RespondJSON("Page.dispatchMouseEvent", map[string]any{})
	client := juggler.NewClient(transport)
	defer client.Close()

	ts := NewBrowserToolset(client, "ctx-agent", "session-click-label")
	defer ts.Close()

	if result, isErr, err := ts.Dispatch(context.Background(), "vulpine_annotated_screenshot", `{}`); err != nil || isErr {
		t.Fatalf("annotated screenshot result=%q isErr=%v err=%v", result, isErr, err)
	}
	result, isErr, err := ts.Dispatch(context.Background(), "vulpine_click_label", `{"label":"@1"}`)
	if err != nil {
		t.Fatalf("click_label dispatch error: %v", err)
	}
	if isErr {
		t.Fatalf("click_label returned tool error: %s", result)
	}
	if !strings.Contains(result, "clicked label @1") {
		t.Fatalf("click_label result = %q", result)
	}
	for _, method := range []string{"Page.scrollIntoViewIfNeeded", "Page.getContentQuads", "Page.dispatchMouseEvent"} {
		calls := transport.CallsByMethod(method)
		if len(calls) == 0 {
			t.Fatalf("%s was not called", method)
		}
		for _, call := range calls {
			if call.SessionID != "session-click-label" {
				t.Fatalf("%s session = %q, want session-click-label", method, call.SessionID)
			}
		}
	}
}

func TestBrowserToolsetFindNoElementsIsNotToolFailure(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondJSON("Runtime.evaluate", map[string]any{
		"result": map[string]any{"value": `[]`},
	})
	client := juggler.NewClient(transport)
	defer client.Close()

	ts := NewBrowserToolset(client, "ctx-agent", "session-find")
	defer ts.Close()

	result, isErr, err := ts.Dispatch(context.Background(), "vulpine_find", `{"query":"Definitely Missing"}`)
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if isErr {
		t.Fatalf("find no-elements isErr = true, result=%q", result)
	}
	if !strings.Contains(result, `No elements found matching "Definitely Missing"`) {
		t.Fatalf("find result = %q", result)
	}
}

func TestBrowserToolsetCompactsAnnotatedScreenshotResult(t *testing.T) {
	elements := make([]map[string]any, 120)
	for i := range elements {
		elements[i] = map[string]any{
			"label":    fmt.Sprintf("@%d", i+1),
			"role":     "button",
			"name":     strings.Repeat("Long label ", 30),
			"objectId": fmt.Sprintf("obj-%d", i+1),
			"frameId":  "frame-1",
		}
	}
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondJSON("Page.getAnnotatedScreenshot", map[string]any{
		"image":    "aGVsbG8=",
		"elements": elements,
	})
	client := juggler.NewClient(transport)
	defer client.Close()

	ts := NewBrowserToolset(client, "ctx-agent", "session-large-shot")
	defer ts.Close()

	result, isErr, err := ts.Dispatch(context.Background(), "vulpine_annotated_screenshot", `{}`)
	if err != nil || isErr {
		t.Fatalf("Dispatch result=%q isErr=%v err=%v", result, isErr, err)
	}
	if len(result) > 4200 {
		t.Fatalf("annotated screenshot result length = %d, want compacted <= 4200", len(result))
	}
	if !strings.Contains(result, "[image captured]") || !strings.Contains(result, "@1") {
		t.Fatalf("compacted result lost useful context: %q", result)
	}
}

func TestAgentToolsExposeWorkspaceFileTools(t *testing.T) {
	tools := BrowserTools()
	byName := map[string]ToolDef{}
	for _, td := range tools {
		byName[td.Function.Name] = td
	}
	for _, name := range []string{toolListFiles, toolReadFile, toolWriteFile} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("%s missing from exposed tools", name)
		}
	}
	write := byName[toolWriteFile]
	req, _ := write.Function.Parameters["required"].([]string)
	if !containsString(req, "path") || !containsString(req, "content") {
		t.Fatalf("%s required = %#v, want path and content", toolWriteFile, req)
	}
}

func TestBrowserToolsetWorkspaceFileTools(t *testing.T) {
	workspace := t.TempDir()
	ts := &BrowserToolset{workspace: workspace}

	result, isErr, err := ts.Dispatch(context.Background(), toolWriteFile, `{"path":"notes/demo.txt","content":"hello"}`)
	if err != nil || isErr {
		t.Fatalf("write result=%q isErr=%v err=%v", result, isErr, err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "notes", "demo.txt"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("written content = %q, want hello", data)
	}

	result, isErr, err = ts.Dispatch(context.Background(), toolReadFile, `{"path":"notes/demo.txt"}`)
	if err != nil || isErr || result != "hello" {
		t.Fatalf("read result=%q isErr=%v err=%v, want hello", result, isErr, err)
	}

	result, isErr, err = ts.Dispatch(context.Background(), toolListFiles, `{"path":"notes"}`)
	if err != nil || isErr || !strings.Contains(result, "demo.txt") {
		t.Fatalf("list result=%q isErr=%v err=%v, want demo.txt", result, isErr, err)
	}
}

func TestBrowserToolsetWorkspaceFileToolsRejectEscapes(t *testing.T) {
	ts := &BrowserToolset{workspace: t.TempDir()}

	for _, tc := range []struct {
		name string
		args string
	}{
		{name: toolReadFile, args: `{"path":"../secret.txt"}`},
		{name: toolWriteFile, args: `{"path":"/tmp/secret.txt","content":"nope"}`},
	} {
		result, isErr, err := ts.Dispatch(context.Background(), tc.name, tc.args)
		if err != nil {
			t.Fatalf("%s dispatch error: %v", tc.name, err)
		}
		if !isErr {
			t.Fatalf("%s isErr = false, result=%q", tc.name, result)
		}
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
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

func TestAgentToolsExposeDelegationTools(t *testing.T) {
	tools := BrowserTools()
	byName := map[string]ToolDef{}
	for _, td := range tools {
		byName[td.Function.Name] = td
	}
	for _, name := range []string{toolDelegateAgent, toolSteerAgent, toolAgentStatus, toolReleaseAgent, toolGetAgentResult} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("%s missing from exposed tools", name)
		}
	}
}

func TestDelegateAgentToolAdvertisesActualDefaultMaxTurns(t *testing.T) {
	tools := BrowserTools()
	var delegate ToolDef
	for _, td := range tools {
		if td.Function.Name == toolDelegateAgent {
			delegate = td
			break
		}
	}
	if delegate.Function.Name == "" {
		t.Fatal("delegate tool missing")
	}
	props, ok := delegate.Function.Parameters["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("delegate properties = %#v", delegate.Function.Parameters["properties"])
	}
	maxTurns, ok := props["max_turns"].(map[string]interface{})
	if !ok {
		t.Fatalf("max_turns property = %#v", props["max_turns"])
	}
	description, _ := maxTurns["description"].(string)
	want := fmt.Sprintf("default %d", defaultMissionMaxTurns)
	if !strings.Contains(description, want) {
		t.Fatalf("max_turns description = %q, want %q", description, want)
	}
}

func TestDelegationToolsErrorWhenNoManager(t *testing.T) {
	ts := &BrowserToolset{}

	result, isErr, err := ts.Dispatch(context.Background(), toolDelegateAgent, `{"objective":"test"}`)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !isErr {
		t.Fatal("expected isErr=true when no delegate manager")
	}
	if !strings.Contains(result, "delegation manager not available") {
		t.Errorf("result = %q, want delegation manager not available", result)
	}
}

type mockDelegationManager struct {
	DelegateFunc      func(Mission) (string, error)
	SteerAgentFunc    func(string, string) error
	AgentStatusFunc   func(string) (string, error)
	AgentResultFunc   func(string) (string, error)
	ReleaseAgentFunc  func(string) error
	AgentSnapshotFunc func(string) (string, error)
}

func (m *mockDelegationManager) Delegate(mission Mission) (string, error) {
	if m.DelegateFunc != nil {
		return m.DelegateFunc(mission)
	}
	return "sub-1", nil
}

func (m *mockDelegationManager) SteerAgent(agentID, message string) error {
	if m.SteerAgentFunc != nil {
		return m.SteerAgentFunc(agentID, message)
	}
	return nil
}

func (m *mockDelegationManager) AgentStatus(agentID string) (string, error) {
	if m.AgentStatusFunc != nil {
		return m.AgentStatusFunc(agentID)
	}
	return "running", nil
}

func (m *mockDelegationManager) AgentResult(agentID string) (string, error) {
	if m.AgentResultFunc != nil {
		return m.AgentResultFunc(agentID)
	}
	return "result output", nil
}

func (m *mockDelegationManager) ReleaseAgent(agentID string) error {
	if m.ReleaseAgentFunc != nil {
		return m.ReleaseAgentFunc(agentID)
	}
	return nil
}

func (m *mockDelegationManager) AgentSnapshot(agentID string) (string, error) {
	if m.AgentSnapshotFunc != nil {
		return m.AgentSnapshotFunc(agentID)
	}
	return `{"status":"running","phase":"processing"}`, nil
}

func TestDelegateAgentTool(t *testing.T) {
	ts := &BrowserToolset{}
	ts.SetDelegateManager(&mockDelegationManager{
		DelegateFunc: func(m Mission) (string, error) {
			if m.Objective != "test objective" {
				t.Errorf("objective = %q, want 'test objective'", m.Objective)
			}
			if m.RoleSeed != "reviewer" {
				t.Errorf("role_seed = %q, want 'reviewer'", m.RoleSeed)
			}
			return "sub-agent-42", nil
		},
	})

	result, isErr, err := ts.Dispatch(context.Background(), toolDelegateAgent,
		`{"objective":"test objective","role_seed":"reviewer","max_turns":5}`)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
	if !strings.Contains(result, "sub-agent-42") {
		t.Errorf("result = %q, want sub-agent-42", result)
	}
}

func TestSteerAgentTool(t *testing.T) {
	ts := &BrowserToolset{}
	var capturedID, capturedMsg string
	ts.SetDelegateManager(&mockDelegationManager{
		SteerAgentFunc: func(agentID, message string) error {
			capturedID = agentID
			capturedMsg = message
			return nil
		},
	})

	result, isErr, err := ts.Dispatch(context.Background(), toolSteerAgent,
		`{"agent_id":"sub-1","message":"focus on performance"}`)
	if err != nil || isErr {
		t.Fatalf("dispatch: result=%q isErr=%v err=%v", result, isErr, err)
	}
	if capturedID != "sub-1" {
		t.Errorf("agent_id = %q, want sub-1", capturedID)
	}
	if capturedMsg != "focus on performance" {
		t.Errorf("message = %q, want 'focus on performance'", capturedMsg)
	}
	if !strings.Contains(result, "Steering message sent") {
		t.Errorf("result = %q, want 'Steering message sent'", result)
	}
}

func TestAgentStatusTool(t *testing.T) {
	ts := &BrowserToolset{}
	ts.SetDelegateManager(&mockDelegationManager{
		AgentStatusFunc: func(id string) (string, error) {
			if id != "sub-1" {
				t.Errorf("agent_id = %q, want sub-1", id)
			}
			return "running", nil
		},
	})

	result, isErr, err := ts.Dispatch(context.Background(), toolAgentStatus,
		`{"agent_id":"sub-1"}`)
	if err != nil || isErr {
		t.Fatalf("dispatch: result=%q isErr=%v err=%v", result, isErr, err)
	}
	if result != "running" {
		t.Errorf("result = %q, want 'running'", result)
	}
}

func TestReleaseAgentTool(t *testing.T) {
	ts := &BrowserToolset{}
	var releasedID string
	ts.SetDelegateManager(&mockDelegationManager{
		ReleaseAgentFunc: func(id string) error {
			releasedID = id
			return nil
		},
	})

	result, isErr, err := ts.Dispatch(context.Background(), toolReleaseAgent,
		`{"agent_id":"sub-1"}`)
	if err != nil || isErr {
		t.Fatalf("dispatch: result=%q isErr=%v err=%v", result, isErr, err)
	}
	if releasedID != "sub-1" {
		t.Errorf("released agent = %q, want sub-1", releasedID)
	}
	if !strings.Contains(result, "Sub-agent released") {
		t.Errorf("result = %q, want 'Sub-agent released'", result)
	}
}

func TestAgentResultTool(t *testing.T) {
	ts := &BrowserToolset{}
	ts.SetDelegateManager(&mockDelegationManager{
		AgentResultFunc: func(id string) (string, error) {
			if id != "sub-1" {
				t.Errorf("agent_id = %q, want sub-1", id)
			}
			return "completed analysis: all tests pass", nil
		},
	})

	result, isErr, err := ts.Dispatch(context.Background(), toolGetAgentResult,
		`{"agent_id":"sub-1"}`)
	if err != nil || isErr {
		t.Fatalf("dispatch: result=%q isErr=%v err=%v", result, isErr, err)
	}
	if result != "completed analysis: all tests pass" {
		t.Errorf("result = %q, want 'completed analysis: all tests pass'", result)
	}

	// Error path: agent not found
	ts.SetDelegateManager(&mockDelegationManager{
		AgentResultFunc: func(id string) (string, error) {
			return "", fmt.Errorf("agent %s not found", id)
		},
	})
	result, isErr, err = ts.Dispatch(context.Background(), toolGetAgentResult,
		`{"agent_id":"nonexistent"}`)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !isErr {
		t.Fatal("expected isErr=true for nonexistent agent")
	}
	if !strings.Contains(result, "not found") {
		t.Errorf("result = %q, want 'not found'", result)
	}

	// Error path: empty result
	ts.SetDelegateManager(&mockDelegationManager{
		AgentResultFunc: func(id string) (string, error) {
			return "", nil
		},
	})
	result, isErr, err = ts.Dispatch(context.Background(), toolGetAgentResult,
		`{"agent_id":"sub-empty"}`)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
	if result != "(agent produced no output)" {
		t.Errorf("result = %q, want '(agent produced no output)'", result)
	}
}

func TestDelegationToolMissingRequiredArgs(t *testing.T) {
	ts := &BrowserToolset{}
	ts.SetDelegateManager(&mockDelegationManager{})

	// Steer with no agent_id
	result, isErr, err := ts.Dispatch(context.Background(), toolSteerAgent, `{"message":"hi"}`)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !isErr {
		t.Fatal("expected isErr=true for missing agent_id")
	}
	if !strings.Contains(result, "agent_id is required") {
		t.Errorf("result = %q, want 'agent_id is required'", result)
	}

	// Status with no agent_id
	result, isErr, err = ts.Dispatch(context.Background(), toolAgentStatus, `{}`)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !isErr {
		t.Fatal("expected isErr=true for missing agent_id")
	}

	// Release with no agent_id
	result, isErr, err = ts.Dispatch(context.Background(), toolReleaseAgent, `{}`)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !isErr {
		t.Fatal("expected isErr=true for missing agent_id")
	}

	// Get result with no agent_id
	result, isErr, err = ts.Dispatch(context.Background(), toolGetAgentResult, `{}`)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !isErr {
		t.Fatal("expected isErr=true for missing agent_id")
	}

	// Delegate with no objective (still dispatches, Mission has safe defaults)
	result, isErr, err = ts.Dispatch(context.Background(), toolDelegateAgent, `{}`)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
}
