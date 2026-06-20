//go:build !race

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
)

func findBrowserBinary() string {
	if binary := strings.TrimSpace(os.Getenv("VULPINE_BROWSER_BINARY")); binary != "" {
		return binary
	}
	if binary := strings.TrimSpace(os.Getenv("CAMOUFOX_BINARY")); binary != "" {
		return binary
	}
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, "Downloads", "Vulpine.app", "Contents", "MacOS", "vulpine"),
		filepath.Join(home, ".vulpineos", "browser", "vulpine"),
		"/usr/local/bin/vulpine",
		filepath.Join(home, "Downloads", "Camoufox.app", "Contents", "MacOS", "camoufox"),
		filepath.Join(home, ".camoufox", "camoufox"),
		"/usr/local/bin/camoufox",
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	binary, _ := kernel.ResolveBinaryPath("")
	return binary
}

func startLiveKernel(t *testing.T) (*kernel.Kernel, *juggler.Client) {
	t.Helper()

	if strings.TrimSpace(os.Getenv("VULPINEOS_RUN_LIVE")) == "" {
		t.Skip("set VULPINEOS_RUN_LIVE=1 to run live VulpineOS MCP integration tests")
	}

	binary := findBrowserBinary()
	if binary == "" {
		t.Skip("Vulpine browser binary not found")
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		k := kernel.New()
		if err := k.Start(kernel.Config{
			BinaryPath: binary,
			Headless:   true,
		}); err != nil {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}

		client := k.Client()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := client.Call("", "Browser.enable", map[string]interface{}{
				"attachToDefaultContext": true,
			}); err == nil {
				return k, client
			} else {
				lastErr = err
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		k.Stop()
		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf("Browser.enable: %v", lastErr)
	return nil, nil
}

func newPageSession(t *testing.T, client *juggler.Client) string {
	t.Helper()

	sessionCh := make(chan string, 1)
	client.Subscribe("Browser.attachedToTarget", func(_ string, params json.RawMessage) {
		var ev struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(params, &ev)
		if ev.SessionID != "" {
			select {
			case sessionCh <- ev.SessionID:
			default:
			}
		}
	})

	if _, err := client.Call("", "Browser.newPage", nil); err != nil {
		t.Fatalf("Browser.newPage: %v", err)
	}

	select {
	case sid := <-sessionCh:
		return sid
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for page session")
		return ""
	}
}

func mustArgs(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func toolText(t *testing.T, res *ToolCallResult, err error) string {
	t.Helper()
	if err != nil {
		t.Fatalf("tool call failed: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("tool call returned no content")
	}
	if res.IsError {
		t.Fatalf("tool call returned error: %s", res.Content[0].Text)
	}
	return res.Content[0].Text
}

func TestLiveBrowser_AgentToolsUseExecutionContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
<head><title>Audit Page</title></head>
<body>
  <h1>Audit Heading</h1>
  <form id="audit-form">
    <input id="name" name="name" placeholder="Name">
    <input id="email" name="email" type="email" required placeholder="Email">
    <input id="required-field" name="requiredField" required placeholder="Required field">
    <button id="submit-btn" type="submit">Submit</button>
  </form>
  <div id="status">ready</div>
  <script>
    document.querySelector('#audit-form').addEventListener('submit', (e) => {
      e.preventDefault();
      document.querySelector('#status').textContent = 'submitted';
    });
  </script>
</body>
</html>`))
	}))
	defer server.Close()

	k, client := startLiveKernel(t)
	defer k.Stop()

	tracker := NewContextTracker(client)
	sid := newPageSession(t, client)

	navRes, navErr := handleToolCall(context.Background(), client, tracker, "vulpine_navigate", mustArgs(map[string]interface{}{
		"sessionId": sid,
		"url":       server.URL,
	}))
	navText := toolText(t, navRes, navErr)
	if !strings.Contains(navText, server.URL) {
		t.Fatalf("navigate text = %q", navText)
	}

	waitRes, waitErr := handleToolCall(context.Background(), client, tracker, "vulpine_wait", mustArgs(map[string]interface{}{
		"sessionId": sid,
		"condition": "urlContains",
		"text":      server.URL,
		"timeout":   5,
	}))
	waitText := toolText(t, waitRes, waitErr)
	if !strings.Contains(waitText, "Condition met") {
		t.Fatalf("wait text = %q", waitText)
	}

	settledRes, settledErr := handleToolCall(context.Background(), client, tracker, "vulpine_page_settled", mustArgs(map[string]interface{}{
		"sessionId": sid,
		"timeout":   5,
	}))
	settledText := toolText(t, settledRes, settledErr)
	if !strings.Contains(settledText, "Page settled:") {
		t.Fatalf("settled text = %q", settledText)
	}

	pageInfoRes, pageInfoErr := handleToolCall(context.Background(), client, tracker, "vulpine_page_info", mustArgs(map[string]interface{}{
		"sessionId": sid,
	}))
	pageInfoText := toolText(t, pageInfoRes, pageInfoErr)
	if !strings.Contains(pageInfoText, `"title":"Audit Page"`) {
		t.Fatalf("page info missing title: %s", pageInfoText)
	}

	fillRes, fillErr := handleToolCall(context.Background(), client, tracker, "vulpine_fill_form", mustArgs(map[string]interface{}{
		"sessionId": sid,
		"fields": map[string]string{
			"#name":  "Alice",
			"#email": "alice@example.com",
		},
	}))
	fillText := toolText(t, fillRes, fillErr)
	if !strings.Contains(fillText, "Filled 2/2 fields") {
		t.Fatalf("fill text = %q", fillText)
	}

	verifyRes, verifyErr := handleToolCall(context.Background(), client, tracker, "vulpine_verify", mustArgs(map[string]interface{}{
		"sessionId": sid,
		"check":     "value",
		"selector":  "#email",
		"expected":  "alice@example.com",
	}))
	verifyText := toolText(t, verifyRes, verifyErr)
	if !strings.Contains(verifyText, "PASS:") {
		t.Fatalf("verify text = %q", verifyText)
	}

	errorsRes, errorsErr := handleToolCall(context.Background(), client, tracker, "vulpine_get_form_errors", mustArgs(map[string]interface{}{
		"sessionId": sid,
		"selector":  "#audit-form",
	}))
	errorsText := toolText(t, errorsRes, errorsErr)
	if !strings.Contains(errorsText, `"count":1`) {
		t.Fatalf("form errors = %q", errorsText)
	}
}

func TestLiveBrowser_AnnotatedScreenshotClickLabel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
<head><title>Label Demo</title></head>
<body>
  <button id="press" type="button">Press Me</button>
  <div id="status">ready</div>
  <script>
    document.querySelector('#press').addEventListener('click', () => {
      document.querySelector('#status').textContent = 'clicked';
    });
  </script>
</body>
</html>`))
	}))
	defer server.Close()

	k, client := startLiveKernel(t)
	defer k.Stop()

	tracker := NewContextTracker(client)
	sid := newPageSession(t, client)

	navRes, navErr := handleToolCall(context.Background(), client, tracker, "vulpine_navigate", mustArgs(map[string]interface{}{
		"sessionId": sid,
		"url":       server.URL,
	}))
	_ = toolText(t, navRes, navErr)
	time.Sleep(2 * time.Second)

	settledRes, settledErr := handleToolCall(context.Background(), client, tracker, "vulpine_page_settled", mustArgs(map[string]interface{}{
		"sessionId": sid,
		"timeout":   5,
	}))
	_ = toolText(t, settledRes, settledErr)

	shotArgs, _ := json.Marshal(map[string]interface{}{
		"sessionId":   sid,
		"maxElements": 10,
	})
	shotRes, ok := handleExtensionTool(context.Background(), client, "vulpine_annotated_screenshot", shotArgs)
	if !ok {
		t.Fatal("vulpine_annotated_screenshot not dispatched")
	}
	if shotRes == nil || shotRes.IsError {
		t.Fatalf("annotated screenshot failed: %+v", shotRes)
	}
	if len(shotRes.Content) < 2 {
		t.Fatalf("annotated screenshot content = %+v, want image + text", shotRes.Content)
	}

	var elements []map[string]interface{}
	if err := json.Unmarshal([]byte(shotRes.Content[1].Text), &elements); err != nil {
		t.Fatalf("decode annotated screenshot elements: %v", err)
	}

	var label string
	for i, element := range elements {
		text, _ := element["text"].(string)
		if !strings.Contains(text, "Press Me") {
			continue
		}
		label, _ = element["label"].(string)
		if label == "" {
			label = fmt.Sprintf("@%d", i+1)
		}
		break
	}
	if label == "" {
		t.Fatalf("missing label for Press Me button: %s", shotRes.Content[1].Text)
	}
	if _, ok := globalLabels.Get(sid, label); !ok {
		t.Fatalf("label %q was not stored in label index", label)
	}

	clickArgs, _ := json.Marshal(map[string]interface{}{
		"session_id": sid,
		"label":      label,
	})
	clickRes, ok := handleExtensionTool(context.Background(), client, "vulpine_click_label", clickArgs)
	if !ok {
		t.Fatal("vulpine_click_label not dispatched")
	}
	if clickRes == nil || clickRes.IsError {
		t.Fatalf("click label failed: %+v", clickRes)
	}

	verifyRes, verifyErr := handleToolCall(context.Background(), client, tracker, "vulpine_verify", mustArgs(map[string]interface{}{
		"sessionId": sid,
		"check":     "text",
		"selector":  "#status",
		"expected":  "clicked",
	}))
	verifyText := toolText(t, verifyRes, verifyErr)
	if !strings.Contains(verifyText, "PASS:") {
		t.Fatalf("verify text = %q", verifyText)
	}

	resetRes, resetErr := handleToolCall(context.Background(), client, tracker, "vulpine_navigate", mustArgs(map[string]interface{}{
		"sessionId": sid,
		"url":       "about:blank",
	}))
	_ = toolText(t, resetRes, resetErr)
	if _, ok := globalLabels.Get(sid, label); ok {
		t.Fatalf("label %q should be cleared after navigation", label)
	}
}
