//go:build !race

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"vulpineos/internal/agentmsg"
	"vulpineos/internal/config"
	"vulpineos/internal/foxbridge"
	"vulpineos/internal/juggler"
	"vulpineos/internal/nanoclaw"
)

func TestLiveScopedSessionSoak(t *testing.T) {
	if strings.TrimSpace(os.Getenv("VULPINEOS_RUN_SOAK")) == "" || strings.TrimSpace(os.Getenv("VULPINEOS_RUN_LIVE")) == "" {
		t.Skip("set VULPINEOS_RUN_SOAK=1 and VULPINEOS_RUN_LIVE=1 to run the scoped-session soak harness")
	}

	iterations := 3
	if raw := os.Getenv("VULPINEOS_SOAK_ITERATIONS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			t.Fatalf("invalid VULPINEOS_SOAK_ITERATIONS %q", raw)
		}
		iterations = n
	}

	mgr := nanoclaw.NewManager("")
	if !mgr.NanoClawInstalled() {
		t.Skip("NanoClaw not installed")
	}
	defer mgr.Dispose()

	cfg, err := config.Load()
	if err != nil || !cfg.SetupComplete {
		t.Skip("VulpineOS not configured — run setup wizard first")
	}
	if err := cfg.GenerateNanoClawConfig("", cfg.BinaryPath); err != nil {
		t.Fatalf("GenerateNanoClawConfig: %v", err)
	}

	k, client := startLiveKernel(t)
	defer k.Stop()

	tracker := NewContextTracker(client)
	detachedCh := make(chan string, 64)
	client.Subscribe("Browser.detachedFromTarget", func(_ string, params json.RawMessage) {
		var ev struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(params, &ev)
		if ev.SessionID != "" {
			select {
			case detachedCh <- ev.SessionID:
			default:
			}
		}
	})

	server := startScopedSoakServer(t)
	defer server.Close()

	for i := 0; i < iterations; i++ {
		t.Run(fmt.Sprintf("iteration-%02d", i+1), func(t *testing.T) {
			started := time.Now()
			contextID := createBrowserContext(t, client)
			token := fmt.Sprintf("soak-%02d-%d", i+1, time.Now().UnixNano())

			if _, err := client.Call("", "Browser.setCookies", mustArgs(map[string]interface{}{
				"browserContextId": contextID,
				"cookies": []map[string]interface{}{
					{
						"name":   "vulpine_scope",
						"value":  token,
						"domain": "127.0.0.1",
						"path":   "/",
					},
				},
			})); err != nil {
				t.Fatalf("Browser.setCookies: %v", err)
			}

			scopedFoxbridge, err := foxbridge.StartEmbeddedScoped(client, 0, contextID)
			if err != nil {
				t.Fatalf("StartEmbeddedScoped: %v", err)
			}
			defer scopedFoxbridge.Stop()

			scopedConfig, cleanupConfig, err := nanoclaw.PrepareScopedConfig(config.NanoClawConfigPath(), scopedFoxbridge.CDPURL())
			if err != nil {
				t.Fatalf("PrepareScopedConfig: %v", err)
			}
			defer cleanupConfig()

			agentID := fmt.Sprintf("soak-nanoclaw-%02d", i+1)
			sessionName := fmt.Sprintf("vulpine-soak-%02d", i+1)
			task := fmt.Sprintf("Use the browser to open %s/cookie?iter=%d and reply exactly COOKIE:%s. Do not explain.", server.URL, i+1, token)
			if _, err := mgr.SpawnWithSession(agentID, task, sessionName, scopedConfig); err != nil {
				t.Fatalf("SpawnWithSession: %v", err)
			}

			waitForAssistantContains(t, mgr.ConversationChan(), agentID, "COOKIE:"+token, 180*time.Second)

			sessionID := newPageSessionInContext(t, client, contextID)

			url1 := fmt.Sprintf("%s/mcp?iter=%d&step=1", server.URL, i+1)
			nav1Res, nav1Err := handleToolCall(context.Background(), client, tracker, "vulpine_navigate", mustArgs(map[string]interface{}{
				"sessionId": sessionID,
				"url":       url1,
			}))
			nav1 := toolText(t, nav1Res, nav1Err)
			if !strings.Contains(nav1, url1) {
				t.Fatalf("navigate 1 text = %q", nav1)
			}

			wait1Res, wait1Err := handleToolCall(context.Background(), client, tracker, "vulpine_wait", mustArgs(map[string]interface{}{
				"sessionId": sessionID,
				"condition": "urlContains",
				"text":      fmt.Sprintf("step=1"),
				"timeout":   5,
			}))
			wait1 := toolText(t, wait1Res, wait1Err)
			if !strings.Contains(wait1, "Condition met") {
				t.Fatalf("wait 1 text = %q", wait1)
			}

			url2 := fmt.Sprintf("%s/mcp?iter=%d&step=2", server.URL, i+1)
			nav2Res, nav2Err := handleToolCall(context.Background(), client, tracker, "vulpine_navigate", mustArgs(map[string]interface{}{
				"sessionId": sessionID,
				"url":       url2,
			}))
			nav2 := toolText(t, nav2Res, nav2Err)
			if !strings.Contains(nav2, url2) {
				t.Fatalf("navigate 2 text = %q", nav2)
			}

			pageInfoRes, pageInfoErr := handleToolCall(context.Background(), client, tracker, "vulpine_page_info", mustArgs(map[string]interface{}{
				"sessionId": sessionID,
			}))
			pageInfo := toolText(t, pageInfoRes, pageInfoErr)
			if !strings.Contains(pageInfo, fmt.Sprintf(`"title":"MCP Soak %d"`, i+1)) {
				t.Fatalf("page info missing updated title: %s", pageInfo)
			}

			fillRes, fillErr := handleToolCall(context.Background(), client, tracker, "vulpine_fill_form", mustArgs(map[string]interface{}{
				"sessionId": sessionID,
				"fields": map[string]string{
					"#name":  fmt.Sprintf("Agent %d", i+1),
					"#email": fmt.Sprintf("agent-%d@example.com", i+1),
				},
			}))
			fill := toolText(t, fillRes, fillErr)
			if !strings.Contains(fill, "Filled 2/2 fields") {
				t.Fatalf("fill text = %q", fill)
			}

			x, y := elementCenter(t, client, tracker, sessionID, "#submit-btn")
			clickRes, clickErr := handleToolCall(context.Background(), client, tracker, "vulpine_human_click", mustArgs(map[string]interface{}{
				"sessionId": sessionID,
				"x":         x,
				"y":         y,
				"speed":     "normal",
			}))
			click := toolText(t, clickRes, clickErr)
			if !strings.Contains(click, "Human-clicked") {
				t.Fatalf("click text = %q", click)
			}

			verifyRes, verifyErr := handleToolCall(context.Background(), client, tracker, "vulpine_verify", mustArgs(map[string]interface{}{
				"sessionId": sessionID,
				"check":     "text",
				"selector":  "#status",
				"expected":  fmt.Sprintf("submitted-%d", i+1),
			}))
			verify := toolText(t, verifyRes, verifyErr)
			if !strings.Contains(verify, "PASS:") {
				t.Fatalf("verify text = %q", verify)
			}

			if _, err := client.Call("", "Browser.removeBrowserContext", mustArgs(map[string]interface{}{
				"browserContextId": contextID,
			})); err != nil {
				t.Fatalf("Browser.removeBrowserContext: %v", err)
			}

			waitForDetachedSession(t, detachedCh, sessionID, 15*time.Second)
			t.Logf("SOAK_RESULT iteration=%d duration_ms=%d cleanup_session=%s status=ok", i+1, time.Since(started).Milliseconds(), sessionID)
		})
	}
}

func startScopedSoakServer(t *testing.T) *httptest.Server {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cookie":
			value := "MISSING"
			if cookie, err := r.Cookie("vulpine_scope"); err == nil && cookie.Value != "" {
				value = cookie.Value
			}
			fmt.Fprintf(w, "<!doctype html><html><head><title>Cookie %s</title></head><body>COOKIE:%s</body></html>", r.URL.Query().Get("iter"), value)
		case "/mcp":
			iter := r.URL.Query().Get("iter")
			if iter == "" {
				iter = "0"
			}
			step := r.URL.Query().Get("step")
			if step == "" {
				step = "0"
			}
			fmt.Fprintf(w, `<!doctype html>
<html>
<head><title>MCP Soak %s</title></head>
<body>
  <h1>MCP Soak %s</h1>
  <div id="step">step-%s</div>
  <form id="soak-form">
    <input id="name" name="name" placeholder="Name">
    <input id="email" name="email" type="email" placeholder="Email">
    <button id="submit-btn" type="submit">Submit</button>
  </form>
  <div id="status">ready</div>
  <script>
    document.querySelector('#soak-form').addEventListener('submit', (event) => {
      event.preventDefault();
      document.querySelector('#status').textContent = 'submitted-%s';
    });
  </script>
</body>
</html>`, iter, iter, step, iter)
		default:
			http.NotFound(w, r)
		}
	}))
	server.Listener = listener
	server.Start()
	return server
}

func createBrowserContext(t *testing.T, client *juggler.Client) string {
	t.Helper()

	result, err := client.Call("", "Browser.createBrowserContext", mustArgs(map[string]interface{}{
		"removeOnDetach": true,
	}))
	if err != nil {
		t.Fatalf("Browser.createBrowserContext: %v", err)
	}
	var payload struct {
		BrowserContextID string `json:"browserContextId"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("unmarshal Browser.createBrowserContext: %v", err)
	}
	if payload.BrowserContextID == "" {
		t.Fatal("Browser.createBrowserContext returned empty browserContextId")
	}
	return payload.BrowserContextID
}

func newPageSessionInContext(t *testing.T, client *juggler.Client, contextID string) string {
	t.Helper()

	sessionCh := make(chan string, 1)
	client.Subscribe("Browser.attachedToTarget", func(_ string, params json.RawMessage) {
		var ev struct {
			SessionID  string `json:"sessionId"`
			TargetInfo struct {
				BrowserContextID string `json:"browserContextId"`
			} `json:"targetInfo"`
		}
		_ = json.Unmarshal(params, &ev)
		if ev.SessionID != "" && ev.TargetInfo.BrowserContextID == contextID {
			select {
			case sessionCh <- ev.SessionID:
			default:
			}
		}
	})

	if _, err := client.Call("", "Browser.newPage", mustArgs(map[string]interface{}{
		"browserContextId": contextID,
	})); err != nil {
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

func elementCenter(t *testing.T, client *juggler.Client, tracker *ContextTracker, sessionID, selector string) (float64, float64) {
	t.Helper()

	result, err := evalJS(client, tracker, sessionID, fmt.Sprintf(`(() => {
		const el = document.querySelector(%q);
		if (!el) return JSON.stringify({found: false});
		const rect = el.getBoundingClientRect();
		return JSON.stringify({
			found: true,
			x: rect.left + (rect.width / 2),
			y: rect.top + (rect.height / 2)
		});
	})()`, selector))
	if err != nil {
		t.Fatalf("evalJS elementCenter: %v", err)
	}
	var payload struct {
		Found bool    `json:"found"`
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("unmarshal element center: %v", err)
	}
	if !payload.Found {
		t.Fatalf("selector %q not found", selector)
	}
	return payload.X, payload.Y
}

func waitForAssistantContains(t *testing.T, convCh <-chan agentmsg.ConversationMsg, agentID, want string, timeout time.Duration) {
	t.Helper()

	deadline := time.After(timeout)
	for {
		select {
		case msg, ok := <-convCh:
			if !ok {
				t.Fatal("conversation channel closed")
			}
			if msg.AgentID == agentID && msg.Role == "assistant" && strings.Contains(msg.Content, want) {
				return
			}
		case <-deadline:
			t.Fatalf("assistant response containing %q not received within %s", want, timeout)
		}
	}
}

func waitForDetachedSession(t *testing.T, detachedCh <-chan string, sessionID string, timeout time.Duration) {
	t.Helper()

	deadline := time.After(timeout)
	for {
		select {
		case detached := <-detachedCh:
			if detached == sessionID {
				return
			}
		case <-deadline:
			t.Fatalf("session %s was not detached within %s", sessionID, timeout)
		}
	}
}
