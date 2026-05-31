package internal

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
)

func evalStr(t *testing.T, client *juggler.Client, sessionID, expr string) string {
	t.Helper()
	ctxID, ok := latestContext.Load(sessionID)
	if !ok {
		t.Fatalf("no execution context for session %s", sessionID)
	}
	res, err := client.Call(sessionID, "Runtime.evaluate", mustJSON(map[string]interface{}{
		"expression":         expr,
		"returnByValue":      true,
		"executionContextId": ctxID.(string),
	}))
	if err != nil {
		t.Fatalf("Runtime.evaluate(%.40q): %v", expr, err)
	}
	var e struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	json.Unmarshal(res, &e)
	return e.Result.Value
}

// TestLiveDragEventWithData runtime-verifies the closed-source drag feature
// (nsDOMWindowUtils.dispatchDragEventWithData) end-to-end through the juggler
// Page.dispatchDragEventWithData command: it dispatches a synthetic 'drop' with
// a DataTransfer and confirms the page's drop handler fires with the data.
func TestLiveDragEventWithData(t *testing.T) {
	binary := skipIfNoLiveBrowser(t)

	k := kernel.New()
	if err := k.Start(kernel.Config{BinaryPath: binary, Headless: true}); err != nil {
		t.Fatalf("kernel.Start: %v", err)
	}
	defer k.Stop()
	client := k.Client()
	setupContextTracking(client)

	if _, err := client.Call("", "Browser.enable", mustJSON(map[string]interface{}{
		"attachToDefaultContext": true,
	})); err != nil {
		t.Fatalf("Browser.enable: %v", err)
	}

	sessionID, frameID := createPageWithFrame(t, client)

	// A page with a full-viewport drop target that records the dropped text.
	page := `<html><body>` +
		`<div id="t" style="position:fixed;top:0;left:0;width:100%;height:100%">drop here</div>` +
		`<script>` +
		`window.__drop = null;` +
		`document.addEventListener('dragover', e => e.preventDefault());` +
		`document.addEventListener('drop', e => { window.__drop = e.dataTransfer ? e.dataTransfer.getData('text/plain') : 'NO_DATATRANSFER'; });` +
		`</script></body></html>`
	url := "data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte(page))

	if _, err := client.Call(sessionID, "Page.navigate", mustJSON(map[string]interface{}{
		"url":     url,
		"frameId": frameID,
	})); err != nil {
		t.Fatalf("Page.navigate: %v", err)
	}
	time.Sleep(2 * time.Second) // load + handler registration + fresh execution context

	// Dispatch a synthetic drop carrying data via the new juggler command.
	if _, err := client.Call(sessionID, "Page.dispatchDragEventWithData", mustJSON(map[string]interface{}{
		"type":     "drop",
		"x":        50.0,
		"y":        50.0,
		"data":     "hello-drag-payload",
		"dataType": "text/plain",
	})); err != nil {
		t.Fatalf("Page.dispatchDragEventWithData: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	got := evalStr(t, client, sessionID, "window.__drop")
	t.Logf("page drop handler received: %q", got)
	if got != "hello-drag-payload" {
		t.Errorf("synthetic drag did not deliver data to the page drop handler: got %q, want %q", got, "hello-drag-payload")
	}
}
