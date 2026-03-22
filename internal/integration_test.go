package internal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"vulpineos/internal/config"
	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
	"vulpineos/internal/mcp"
	"vulpineos/internal/openclaw"
)

// findCamoufox locates the Camoufox binary for integration tests.
func findCamoufox() string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, "Downloads", "Camoufox.app", "Contents", "MacOS", "camoufox"),
		filepath.Join(home, ".camoufox", "camoufox"),
		"/usr/local/bin/camoufox",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// skipIfNoBrowser skips the test if Camoufox is not available.
func skipIfNoBrowser(t *testing.T) string {
	binary := findCamoufox()
	if binary == "" {
		t.Skip("Camoufox binary not found — skipping integration test")
	}
	return binary
}

// startKernel launches Camoufox in headless mode and returns the kernel + client.
func startKernel(t *testing.T) (*kernel.Kernel, *juggler.Client) {
	binary := skipIfNoBrowser(t)

	k := kernel.New()
	err := k.Start(kernel.Config{
		BinaryPath: binary,
		Headless:   true,
	})
	if err != nil {
		t.Fatalf("failed to start kernel: %v", err)
	}

	client := k.Client()
	// Track execution contexts before enabling Browser (which triggers initial page events)
	setupContextTracking(client)

	_, err = client.Call("", "Browser.enable", mustJSON(map[string]interface{}{
		"attachToDefaultContext": true,
	}))
	if err != nil {
		k.Stop()
		t.Fatalf("Browser.enable failed: %v", err)
	}

	return k, client
}

func mustJSON(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// pageSession holds the session ID and frame ID for a created page.
type pageSession struct {
	SessionID string
	FrameID   string
}

// createPage creates a new page and returns session + frame IDs.
func createPage(t *testing.T, client *juggler.Client) string {
	t.Helper()

	sessionCh := make(chan string, 4)
	frameCh := make(chan string, 4)

	client.Subscribe("Browser.attachedToTarget", func(_ string, params json.RawMessage) {
		var ev struct {
			SessionID string `json:"sessionId"`
		}
		json.Unmarshal(params, &ev)
		if ev.SessionID != "" {
			select {
			case sessionCh <- ev.SessionID:
			default:
			}
		}
	})

	client.Subscribe("Page.frameAttached", func(sid string, params json.RawMessage) {
		var ev struct {
			FrameID string `json:"frameId"`
		}
		json.Unmarshal(params, &ev)
		if ev.FrameID != "" {
			select {
			case frameCh <- ev.FrameID:
			default:
			}
		}
	})

	_, err := client.Call("", "Browser.newPage", nil)
	if err != nil {
		t.Fatalf("Browser.newPage failed: %v", err)
	}

	select {
	case sid := <-sessionCh:
		// Also wait briefly for frame
		select {
		case <-frameCh:
		case <-time.After(3 * time.Second):
		}
		return sid
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for page session")
		return ""
	}
}

// navigateTo navigates a page and waits for load.
// Captures execution context and frame IDs from events before navigating.
func navigateTo(t *testing.T, client *juggler.Client, sessionID, url string) {
	t.Helper()

	// Capture execution context ID from events
	ctxCh := make(chan string, 4)
	frameCh := make(chan string, 4)
	client.Subscribe("Runtime.executionContextCreated", func(sid string, params json.RawMessage) {
		if sid == sessionID {
			var ev struct {
				ExecutionContextID string `json:"executionContextId"`
				AuxData            struct {
					FrameID string `json:"frameId"`
				} `json:"auxData"`
			}
			json.Unmarshal(params, &ev)
			if ev.ExecutionContextID != "" {
				select {
				case ctxCh <- ev.ExecutionContextID:
				default:
				}
			}
			if ev.AuxData.FrameID != "" {
				select {
				case frameCh <- ev.AuxData.FrameID:
				default:
				}
			}
		}
	})

	// Trigger content process init
	client.Call(sessionID, "Accessibility.getFullAXTree", mustJSON(map[string]interface{}{}))

	// Wait for execution context
	var execCtxID string
	var frameID string
	select {
	case execCtxID = <-ctxCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for execution context")
	}
	select {
	case frameID = <-frameCh:
	case <-time.After(2 * time.Second):
		// May not get frame from context event, try navigate anyway
	}

	t.Logf("Got execCtx=%s frame=%s", execCtxID, frameID)

	// Navigate using Page.navigate with frameId, or evaluate with contextId
	if frameID != "" {
		_, err := client.Call(sessionID, "Page.navigate", mustJSON(map[string]interface{}{
			"url":     url,
			"frameId": frameID,
		}))
		if err != nil {
			t.Fatalf("Page.navigate failed: %v", err)
		}
	} else {
		_, err := client.Call(sessionID, "Runtime.evaluate", mustJSON(map[string]interface{}{
			"expression":         "window.location.href = " + string(mustJSON(url)),
			"returnByValue":      true,
			"executionContextId": execCtxID,
		}))
		if err != nil {
			t.Fatalf("navigate via evaluate failed: %v", err)
		}
	}
	time.Sleep(3 * time.Second)
}

// latestContextID tracks the latest execution context per session.
var latestContext sync.Map // sessionID → executionContextId (string)

// setupContextTracking subscribes to context events to track the latest context per session.
// Must be called once per kernel before creating pages.
func setupContextTracking(client *juggler.Client) {
	client.Subscribe("Runtime.executionContextCreated", func(sid string, params json.RawMessage) {
		var ev struct {
			ExecutionContextID string `json:"executionContextId"`
		}
		json.Unmarshal(params, &ev)
		if ev.ExecutionContextID != "" && sid != "" {
			latestContext.Store(sid, ev.ExecutionContextID)
		}
	})
}

// callEval evaluates a JS expression using the tracked execution context.
func callEval(t *testing.T, client *juggler.Client, sessionID, expression string) (json.RawMessage, error) {
	t.Helper()

	ctxID, ok := latestContext.Load(sessionID)
	if !ok {
		t.Fatalf("no execution context for session %s — was setupContextTracking called?", sessionID)
	}

	return client.Call(sessionID, "Runtime.evaluate", mustJSON(map[string]interface{}{
		"expression":         expression,
		"returnByValue":      true,
		"executionContextId": ctxID.(string),
	}))
}

// === KERNEL & JUGGLER TESTS ===

func TestIntegration_KernelStarts(t *testing.T) {
	k, client := startKernel(t)
	defer k.Stop()

	// Verify kernel is running
	if k.PID() == 0 {
		t.Fatal("kernel PID is 0")
	}

	// Verify we can call Browser.getInfo
	result, err := client.Call("", "Browser.getInfo", nil)
	if err != nil {
		t.Fatalf("Browser.getInfo failed: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("Browser.getInfo returned empty result")
	}
	t.Logf("Browser.getInfo: %s", string(result))
}

func TestIntegration_CreatePageAndNavigate(t *testing.T) {
	k, client := startKernel(t)
	defer k.Stop()

	// Subscribe to attachedToTarget to get sessionID
	sessionCh := make(chan string, 1)
	client.Subscribe("Browser.attachedToTarget", func(_ string, params json.RawMessage) {
		var ev struct {
			SessionID string `json:"sessionId"`
		}
		json.Unmarshal(params, &ev)
		if ev.SessionID != "" {
			select {
			case sessionCh <- ev.SessionID:
			default:
			}
		}
	})

	time.Sleep(2 * time.Second)

	// Create a new page
	result, err := client.Call("", "Browser.newPage", nil)
	if err != nil {
		t.Fatalf("Browser.newPage failed: %v", err)
	}
	t.Logf("Browser.newPage result: %s", string(result))

	// Wait for session from event
	var sessionID string
	select {
	case sessionID = <-sessionCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for attachedToTarget event")
	}
	t.Logf("Got session: %s", sessionID)

	navigateTo(t, client, sessionID, "https://example.com")

	// Need a fresh execution context after navigation
	time.Sleep(2 * time.Second)

	// Use evaluate with the latest execution context
	// The navigate helper triggers a new context — capture it
	evalResult, err := callEval(t, client, sessionID, "document.title")
	if err != nil {
		t.Fatalf("Runtime.evaluate failed: %v", err)
	}

	var eval struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	json.Unmarshal(evalResult, &eval)

	if eval.Result.Value == "" {
		t.Fatalf("document.title is empty, got raw: %s", string(evalResult))
	}
	t.Logf("Page title: %s", eval.Result.Value)
	if !strings.Contains(strings.ToLower(eval.Result.Value), "example") {
		t.Errorf("expected title containing 'example', got %q", eval.Result.Value)
	}
}

func TestIntegration_Screenshot(t *testing.T) {
	k, client := startKernel(t)
	defer k.Stop()
	time.Sleep(2 * time.Second)

	sid := createPage(t, client)
	navigateTo(t, client, sid, "https://example.com")

	ssResult, err := client.Call(sid, "Page.screenshot", mustJSON(map[string]interface{}{
		"mimeType": "image/png",
		"clip":     map[string]interface{}{"x": 0, "y": 0, "width": 800, "height": 600},
	}))
	if err != nil {
		t.Fatalf("Page.screenshot failed: %v", err)
	}

	var ss struct {
		Data string `json:"data"`
	}
	json.Unmarshal(ssResult, &ss)

	if len(ss.Data) < 100 {
		t.Fatalf("screenshot data too small (%d bytes)", len(ss.Data))
	}
	t.Logf("Screenshot: %d bytes of base64 data", len(ss.Data))
}

func TestIntegration_MouseClick(t *testing.T) {
	k, client := startKernel(t)
	defer k.Stop()
	time.Sleep(2 * time.Second)

	sid := createPage(t, client)
	navigateTo(t, client, sid, "https://example.com")

	_, err := client.Call(sid, "Page.dispatchMouseEvent", mustJSON(map[string]interface{}{
		"type":       "mousedown",
		"x":          100,
		"y":          100,
		"button":     0,
		"clickCount": 1,
		"modifiers":  0,
	}))
	if err != nil {
		t.Fatalf("mousedown failed: %v", err)
	}

	_, err = client.Call(sid, "Page.dispatchMouseEvent", mustJSON(map[string]interface{}{
		"type":       "mouseup",
		"x":          100,
		"y":          100,
		"button":     0,
		"clickCount": 1,
		"modifiers":  0,
	}))
	if err != nil {
		t.Fatalf("mouseup failed: %v", err)
	}
	t.Log("Mouse click dispatched successfully")
}

func TestIntegration_KeyboardType(t *testing.T) {
	k, client := startKernel(t)
	defer k.Stop()
	time.Sleep(2 * time.Second)

	sid := createPage(t, client)
	navigateTo(t, client, sid, "data:text/html,<input id='test' autofocus>")

	_, err := client.Call(sid, "Page.insertText", mustJSON(map[string]interface{}{
		"text": "hello world",
	}))
	if err != nil {
		t.Fatalf("insertText failed: %v", err)
	}

	// Verify the value
	evalResult, err := callEval(t, client, sid, "document.getElementById('test').value")

	var eval struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	json.Unmarshal(evalResult, &eval)

	if eval.Result.Value != "hello world" {
		t.Errorf("expected 'hello world', got %q", eval.Result.Value)
	}
	t.Logf("Typed text: %q", eval.Result.Value)
}

func TestIntegration_OptimizedDOM(t *testing.T) {
	k, client := startKernel(t)
	defer k.Stop()
	time.Sleep(2 * time.Second)

	sid := createPage(t, client)
	navigateTo(t, client, sid, "https://example.com")

	domResult, err := client.Call(sid, "Page.getOptimizedDOM", mustJSON(map[string]interface{}{
		"maxDepth": 5,
		"maxNodes": 100,
	}))
	if err != nil {
		t.Fatalf("Page.getOptimizedDOM failed: %v", err)
	}

	var dom struct {
		Snapshot struct {
			V     int             `json:"v"`
			Title string          `json:"title"`
			URL   string          `json:"url"`
			Nodes json.RawMessage `json:"nodes"`
		} `json:"snapshot"`
		Truncated bool `json:"truncated"`
	}
	json.Unmarshal(domResult, &dom)

	if dom.Snapshot.V != 1 {
		t.Errorf("expected version 1, got %d", dom.Snapshot.V)
	}
	if dom.Snapshot.Title == "" {
		t.Error("snapshot title is empty")
	}
	if len(dom.Snapshot.Nodes) < 10 {
		t.Errorf("snapshot nodes too small: %s", string(dom.Snapshot.Nodes))
	}
	t.Logf("Optimized DOM: title=%q, url=%q, nodes=%d bytes, truncated=%v",
		dom.Snapshot.Title, dom.Snapshot.URL, len(dom.Snapshot.Nodes), dom.Truncated)
}

func TestIntegration_AccessibilityTree(t *testing.T) {
	k, client := startKernel(t)
	defer k.Stop()
	time.Sleep(2 * time.Second)

	sid := createPage(t, client)
	navigateTo(t, client, sid, "https://example.com")

	axResult, err := client.Call(sid, "Accessibility.getFullAXTree", mustJSON(map[string]interface{}{}))
	if err != nil {
		t.Fatalf("Accessibility.getFullAXTree failed: %v", err)
	}

	if len(axResult) < 50 {
		t.Errorf("AX tree too small: %s", string(axResult))
	}
	t.Logf("Accessibility tree: %d bytes", len(axResult))
}

func TestIntegration_ActionLock(t *testing.T) {
	k, client := startKernel(t)
	defer k.Stop()
	time.Sleep(2 * time.Second)

	sid := createPage(t, client)
	navigateTo(t, client, sid, "https://example.com")

	_, err := client.Call(sid, "Page.setActionLock", mustJSON(map[string]interface{}{
		"enabled": true,
	}))
	if err != nil {
		t.Fatalf("Page.setActionLock(true) failed: %v", err)
	}
	t.Log("Action lock engaged")

	// Unlock immediately — don't try to evaluate while locked (JS is frozen, would hang)
	_, err = client.Call(sid, "Page.setActionLock", mustJSON(map[string]interface{}{
		"enabled": false,
	}))
	if err != nil {
		t.Fatalf("Page.setActionLock(false) failed: %v", err)
	}
	t.Log("Action lock released")

	// Verify JS works again after unlock
	evalResult, evalErr := callEval(t, client, sid, "document.title")
	if evalErr != nil {
		t.Fatalf("evaluate after unlock failed: %v", evalErr)
	}
	t.Logf("After unlock, title: %s", string(evalResult))
}

func TestIntegration_ElementRefs(t *testing.T) {
	k, client := startKernel(t)
	defer k.Stop()
	time.Sleep(2 * time.Second)

	sid := createPage(t, client)
	navigateTo(t, client, sid, "data:text/html,<button id='btn'>Click Me</button><input id='inp' placeholder='Type here'>")

	domResult, err := client.Call(sid, "Page.getOptimizedDOM", mustJSON(map[string]interface{}{}))
	if err != nil {
		t.Fatalf("getOptimizedDOM failed: %v", err)
	}

	domStr := string(domResult)
	if !strings.Contains(domStr, "@") {
		t.Error("optimized DOM does not contain element refs (@0, @1, etc.)")
	}
	t.Logf("DOM with refs: %s", domStr[:min(len(domStr), 500)])

	// Try resolving a ref
	resolveResult, err := client.Call(sid, "Page.resolveRef", mustJSON(map[string]interface{}{
		"ref": "@0",
	}))
	if err != nil {
		t.Skipf("Page.resolveRef not available (rebuild Camoufox with VulpineOS patches): %v", err)
	}

	var resolved struct {
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
		Found bool    `json:"found"`
	}
	json.Unmarshal(resolveResult, &resolved)

	if !resolved.Found {
		t.Error("resolveRef(@0) returned found=false")
	}
	t.Logf("Ref @0 resolved to x=%.1f y=%.1f", resolved.X, resolved.Y)
}

// === MCP TOOLS TESTS ===

func TestIntegration_MCPSnapshot(t *testing.T) {
	k, client := startKernel(t)
	defer k.Stop()
	time.Sleep(2 * time.Second)

	sid := createPage(t, client)
	navigateTo(t, client, sid, "https://example.com")

	args := mustJSON(map[string]interface{}{
		"sessionId": sid,
		"maxNodes":  50,
	})

	toolResult, err := mcp.HandleToolCallDirect(client, "vulpine_snapshot", args)
	if err != nil {
		t.Fatalf("vulpine_snapshot failed: %v", err)
	}
	if toolResult == nil || toolResult.IsError {
		t.Fatalf("vulpine_snapshot returned error: %+v", toolResult)
	}
	if len(toolResult.Content) == 0 || toolResult.Content[0].Text == "" {
		t.Fatal("vulpine_snapshot returned empty content")
	}
	t.Logf("Snapshot: %s", toolResult.Content[0].Text[:min(len(toolResult.Content[0].Text), 300)])
}

func TestIntegration_MCPNavigateAndClick(t *testing.T) {
	k, client := startKernel(t)
	defer k.Stop()
	time.Sleep(2 * time.Second)

	sid := createPage(t, client)

	navArgs := mustJSON(map[string]interface{}{
		"sessionId": sid,
		"url":       "https://example.com",
	})
	navResult, err := mcp.HandleToolCallDirect(client, "vulpine_navigate", navArgs)
	if err != nil {
		t.Fatalf("vulpine_navigate failed: %v", err)
	}
	if navResult.IsError {
		t.Fatalf("vulpine_navigate error: %s", navResult.Content[0].Text)
	}
	t.Log("Navigate via MCP: OK")

	time.Sleep(3 * time.Second)

	// Click via MCP tool
	clickArgs := mustJSON(map[string]interface{}{
		"sessionId": sid,
		"x":         200,
		"y":         200,
	})
	clickResult, err := mcp.HandleToolCallDirect(client, "vulpine_click", clickArgs)
	if err != nil {
		t.Fatalf("vulpine_click failed: %v", err)
	}
	if clickResult.IsError {
		t.Fatalf("vulpine_click error: %s", clickResult.Content[0].Text)
	}
	t.Log("Click via MCP: OK")
}

// === OPENCLAW AGENT TESTS ===

func TestIntegration_OpenClawInstalled(t *testing.T) {
	mgr := openclaw.NewManager("")
	if !mgr.OpenClawInstalled() {
		t.Skip("OpenClaw not installed — skipping")
	}
	t.Log("OpenClaw binary found")
}

func TestIntegration_AgentSpawnAndRespond(t *testing.T) {
	mgr := openclaw.NewManager("")
	if !mgr.OpenClawInstalled() {
		t.Skip("OpenClaw not installed")
	}

	// Check if config exists
	cfg, err := config.Load()
	if err != nil || !cfg.SetupComplete {
		t.Skip("VulpineOS not configured — run setup wizard first")
	}

	agentID := "test-integration"
	sessionName := "vulpine-test-integration"

	_, err = mgr.SpawnWithSession(agentID, "Say exactly: INTEGRATION_TEST_OK", sessionName, config.OpenClawConfigPath())
	if err != nil {
		t.Fatalf("SpawnWithSession failed: %v", err)
	}

	// Wait for response with timeout
	convCh := mgr.ConversationChan()
	timeout := time.After(60 * time.Second)
	gotResponse := false

	for !gotResponse {
		select {
		case msg, ok := <-convCh:
			if !ok {
				t.Fatal("conversation channel closed")
			}
			if msg.AgentID == agentID && msg.Role == "assistant" {
				t.Logf("Agent response: %s", msg.Content[:min(len(msg.Content), 200)])
				gotResponse = true
			}
		case <-timeout:
			t.Fatal("agent did not respond within 60 seconds")
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
