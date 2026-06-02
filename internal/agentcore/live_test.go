package agentcore

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"vulpineos/internal/config"
	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
)

// liveLogEvents prints the agent's activity during a live run.
type liveLogEvents struct{ t *testing.T }

func (e liveLogEvents) OnTextDelta(string)     {}
func (e liveLogEvents) OnAssistant(s string)   { e.t.Logf("[assistant] %s", truncate(s, 200)) }
func (e liveLogEvents) OnToolCall(n, a string) { e.t.Logf("[tool-call] %s %s", n, truncate(a, 120)) }
func (e liveLogEvents) OnToolResult(n, r string, er bool) {
	e.t.Logf("[tool-result] %s err=%v %s", n, er, truncate(r, 120))
}
func (e liveLogEvents) OnStatus(s string)  { e.t.Logf("[status] %s", s) }
func (e liveLogEvents) OnUsage(u Usage)    { e.t.Logf("[usage] total=%d", u.TotalTokens) }
func (e liveLogEvents) OnWarning(w string) { e.t.Logf("[warning] %s", w) }

// startLiveKernel boots headless Camoufox and enables the browser, mirroring the
// integration harness. Gated by VULPINEOS_RUN_LIVE + VULPINE_AGENTCORE_LIVE.
func startLiveKernel(t *testing.T) (*kernel.Kernel, *juggler.Client) {
	t.Helper()
	if os.Getenv("VULPINEOS_RUN_LIVE") == "" || os.Getenv("VULPINE_AGENTCORE_LIVE") == "" {
		t.Skip("set VULPINEOS_RUN_LIVE=1 and VULPINE_AGENTCORE_LIVE=1 to run the native agent live test")
	}
	binary := strings.TrimSpace(os.Getenv("CAMOUFOX_BINARY"))
	if binary == "" {
		t.Skip("set CAMOUFOX_BINARY to the camoufox-bin path")
	}
	// Camoufox (anti-detect) may only initialize Juggler properly headful; set
	// VULPINE_AGENTCORE_HEADFUL=1 (under Xvfb) to launch with a display.
	headless := os.Getenv("VULPINE_AGENTCORE_HEADFUL") == ""
	k := kernel.New()
	if err := k.Start(kernel.Config{BinaryPath: binary, Headless: headless}); err != nil {
		t.Fatalf("start kernel: %v", err)
	}
	client := k.Client()
	if _, err := client.Call("", "Browser.enable", map[string]interface{}{"attachToDefaultContext": true}); err != nil {
		k.Stop()
		t.Fatalf("Browser.enable: %v", err)
	}
	return k, client
}

func liveConfig(t *testing.T) Config {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		t.Skip("no APIKey configured in ~/.vulpineos/config.json")
	}
	return Config{
		Provider: cfg.Provider,
		Model:    cfg.Model,
		APIKey:   cfg.APIKey,
		// Live free models that exist as of testing; the loop falls through on 429.
		FallbackModels: []string{
			"openrouter/z-ai/glm-4.5-air:free",
			"openrouter/nvidia/nemotron-nano-9b-v2:free",
		},
		MaxIterations: 12,
	}
}

// TestLive_NativeAgent_TextReply proves the native runtime end-to-end with a
// minimal model requirement (no tool-calling): boot Camoufox, open a page, run
// the loop, get a text reply. This validates page setup + model client + loop
// against the real browser and provider.
func TestLive_NativeAgent_TextReply(t *testing.T) {
	k, client := startLiveKernel(t)
	defer k.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	start := time.Now()
	final, err := RunBrowserAgent(ctx, client, liveConfig(t), "Reply with exactly: NATIVE_OK", liveLogEvents{t})
	t.Logf("NATIVE spawn->reply: %s", time.Since(start).Round(time.Millisecond))
	if err != nil {
		t.Fatalf("RunBrowserAgent: %v", err)
	}
	if !strings.Contains(final, "NATIVE_OK") {
		t.Fatalf("reply %q did not contain NATIVE_OK", final)
	}
}

// TestLive_NativeAgent_BrowserAction exercises a real browser tool call: open a
// page and report a fact from it. Requires a model that supports function
// calling. Gated additionally by VULPINE_AGENTCORE_BROWSER=1.
func TestLive_NativeAgent_BrowserAction(t *testing.T) {
	if os.Getenv("VULPINE_AGENTCORE_BROWSER") == "" {
		t.Skip("set VULPINE_AGENTCORE_BROWSER=1 to run the browser-action live test (needs a tool-calling model)")
	}
	k, client := startLiveKernel(t)
	defer k.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	start := time.Now()
	task := "Use the browser to open https://example.com, then reply with the exact text of the page's main <h1> heading and nothing else."
	final, err := RunBrowserAgent(ctx, client, liveConfig(t), task, liveLogEvents{t})
	t.Logf("NATIVE browser spawn->action: %s", time.Since(start).Round(time.Millisecond))
	if err != nil {
		t.Fatalf("RunBrowserAgent: %v", err)
	}
	if !strings.Contains(strings.ToLower(final), "example domain") {
		t.Fatalf("reply %q did not contain the expected heading 'Example Domain'", final)
	}
}
