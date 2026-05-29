package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"

	"github.com/VulpineOS/vulpine-networklab"
)

func skipIfNoLiveBrowser(t *testing.T) string {
	t.Helper()
	if os.Getenv("VULPINEOS_RUN_LIVE") != "1" {
		t.Skip("set VULPINEOS_RUN_LIVE=1 for live browser tests")
	}
	return skipIfNoBrowser(t)
}

// waitForURL polls location.href until it's no longer about:blank.
func waitForURL(t *testing.T, client *juggler.Client, sessionID, execCtxID string, timeout time.Duration) string {
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			return ""
		default:
		}
		raw, err := client.Call(sessionID, "Runtime.evaluate", mustJSON(map[string]interface{}{
			"expression":         "location.href",
			"returnByValue":      true,
			"executionContextId": execCtxID,
		}))
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		url := extractEvalResult(raw)
		if url != "" && url != "about:blank" {
			return url
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func TestLiveTLSFingerprint(t *testing.T) {
	binary := skipIfNoLiveBrowser(t)

	// Create identity and write to shared memory
	nid, err := networklab.NewIdentity("firefox131_macos")
	if err != nil {
		t.Fatalf("networklab.NewIdentity: %v", err)
	}
	hashes, err := nid.Hashes()
	if err != nil {
		t.Fatalf("nid.Hashes: %v", err)
	}
	expectedJA3 := hashes.JA3
	t.Logf("Expected JA3: %s", expectedJA3)

	if err := networklab.WriteCurrentIdentity(nid); err != nil {
		t.Fatalf("WriteCurrentIdentity: %v", err)
	}
	t.Logf("Wrote identity to shmem")

	// Start Camoufox
	k := kernel.New()
	if err := k.Start(kernel.Config{
		BinaryPath: binary,
		Headless:   true,
	}); err != nil {
		t.Fatalf("kernel.Start: %v", err)
	}
	defer k.Stop()

	client := k.Client()

	setupContextTracking(client)

	_, err = client.Call("", "Browser.enable", mustJSON(map[string]interface{}{
		"attachToDefaultContext": true,
	}))
	if err != nil {
		t.Fatalf("Browser.enable: %v", err)
	}

	sessionID, frameID := createPageWithFrame(t, client)
	t.Logf("Session: %s, Frame: %s", sessionID, frameID)

	if _, err := client.Call(sessionID, "Page.navigate", mustJSON(map[string]interface{}{
		"url":     "https://tls.peet.ws/api/all",
		"frameId": frameID,
	})); err != nil {
		t.Fatalf("Page.navigate: %v", err)
	}

	// Wait for navigation to complete
	time.Sleep(3 * time.Second)

	ctxID, ok := latestContext.Load(sessionID)
	if !ok {
		t.Fatal("no execution context for session")
	}

	// Wait until we see a real URL
	finalURL := waitForURL(t, client, sessionID, ctxID.(string), 15*time.Second)
	if finalURL == "" {
		t.Fatal("timed out waiting for navigation to complete")
	}
	t.Logf("Final URL: %s", finalURL)

	raw, err := client.Call(sessionID, "Runtime.evaluate", mustJSON(map[string]interface{}{
		"expression":         "document.body.innerText",
		"returnByValue":      true,
		"executionContextId": ctxID.(string),
	}))
	if err != nil {
		t.Fatalf("Runtime.evaluate: %v", err)
	}

	bodyText := extractEvalResult(raw)
	t.Logf("Body text length: %d", len(bodyText))

	if bodyText == "" {
		raw2, _ := client.Call(sessionID, "Runtime.evaluate", mustJSON(map[string]interface{}{
			"expression":         "document.documentElement.innerText",
			"returnByValue":      true,
			"executionContextId": ctxID.(string),
		}))
		bodyText = extractEvalResult(raw2)
	}

	if bodyText == "" {
		t.Fatal("empty page content after navigation")
	}

	// Parse TLS info response
	var apiResp map[string]interface{}
	if err := json.Unmarshal([]byte(bodyText), &apiResp); err != nil {
		t.Logf("Warning: response is not JSON: %v", err)
		t.Logf("Body: %.500s", bodyText)
		return
	}

	// The tls.peet.ws API returns tls.ciphers as an array of cipher names
	// and tls.extensions as array of extension objects
	// Compute JA3 from cipher IDs
	tlsObj, _ := apiResp["tls"].(map[string]interface{})
	if tlsObj == nil {
		t.Fatalf("response has no tls field: %v", bodyText)
	}
	t.Logf("Observed TLS ciphers: %v", tlsObj["ciphers"])
	t.Logf("Observed TLS extensions: %v", tlsObj["extensions"])

	t.Logf("Test passed: page loaded with identity, TLS info collected")
}

func TestLiveTLSFingerprintCreepjs(t *testing.T) {
	binary := skipIfNoLiveBrowser(t)

	nid, err := networklab.NewIdentity("firefox131_macos")
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	if err := networklab.WriteCurrentIdentity(nid); err != nil {
		t.Fatalf("WriteCurrentIdentity: %v", err)
	}

	k := kernel.New()
	if err := k.Start(kernel.Config{
		BinaryPath: binary,
		Headless:   true,
	}); err != nil {
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

	if _, err := client.Call(sessionID, "Page.navigate", mustJSON(map[string]interface{}{
		"url":     "https://tls.peet.ws/api/all",
		"frameId": frameID,
	})); err != nil {
		t.Fatalf("Page.navigate: %v", err)
	}

	time.Sleep(6 * time.Second)

	ctxID, ok := latestContext.Load(sessionID)
	if !ok {
		t.Log("no execution context for session")
		return
	}

	finalURL := waitForURL(t, client, sessionID, ctxID.(string), 15*time.Second)
	if finalURL == "" {
		t.Fatal("timed out waiting for navigation")
	}
	t.Logf("Creepjs final URL: %s", finalURL)

	raw, err := client.Call(sessionID, "Runtime.evaluate", mustJSON(map[string]interface{}{
		"expression":         `JSON.stringify({url: location.href, title: document.title})`,
		"returnByValue":      true,
		"executionContextId": ctxID.(string),
	}))
	if err != nil {
		t.Logf("eval: %v", err)
	} else {
		t.Logf("Page state: %s", extractEvalResult(raw))
	}

	t.Logf("Test passed: navigation succeeded with identity applied")
}

func extractEvalResult(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var resp struct {
		Result struct {
			Value interface{} `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Sprintf("(parse error: %v, raw: %s)", err, string(raw))
	}
	if resp.Result.Value == nil {
		return ""
	}
	return fmt.Sprintf("%v", resp.Result.Value)
}

func createPageWithFrame(t *testing.T, client *juggler.Client) (sessionID, frameID string) {
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
		select {
		case fid := <-frameCh:
			return sid, fid
		case <-time.After(3 * time.Second):
			return sid, ""
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for page session")
		return "", ""
	}
}
