package internal

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
)

// newPageInContext opens a page in a specific browser context and returns its
// session id, triggering content-process init so an execution context is
// tracked (mirrors createPageWithFrame but context-scoped).
func newPageInContext(t *testing.T, client *juggler.Client, browserContextID string) string {
	t.Helper()
	sessionCh := make(chan string, 8)
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
	if _, err := client.Call("", "Browser.newPage", mustJSON(map[string]interface{}{
		"browserContextId": browserContextID,
	})); err != nil {
		t.Fatalf("Browser.newPage(ctx=%s): %v", browserContextID, err)
	}
	var sessionID string
	select {
	case sessionID = <-sessionCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for page session")
	}
	// Trigger content-process init so the main-frame execution context is created
	// and recorded by setupContextTracking (consumed by evalNumber/audioFP).
	client.Call(sessionID, "Accessibility.getFullAXTree", mustJSON(map[string]interface{}{}))
	return sessionID
}

// TestLivePerContextFingerprintIsolation runtime-verifies the per-context
// isolation slice end-to-end:
//   - D.4: Browser.createBrowserContext returns a numeric userContextId that is
//     non-zero and distinct per context.
//   - B/C: Browser.setContextFingerprint writes the per-context
//     roverfox.s.audioFingerprintSeed_<userContextId> prefs production uses, so
//     two contexts seeded differently produce DIFFERENT audio fingerprints.
func TestLivePerContextFingerprintIsolation(t *testing.T) {
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

	type ctxInfo struct {
		BrowserContextID string `json:"browserContextId"`
		UserContextID    uint32 `json:"userContextId"`
	}
	mkctx := func() ctxInfo {
		res, err := client.Call("", "Browser.createBrowserContext", mustJSON(map[string]interface{}{
			"removeOnDetach": true,
		}))
		if err != nil {
			t.Fatalf("createBrowserContext: %v", err)
		}
		var c ctxInfo
		if err := json.Unmarshal(res, &c); err != nil {
			t.Fatalf("unmarshal ctx: %v", err)
		}
		return c
	}

	a := mkctx()
	b := mkctx()
	t.Logf("ctxA userContextId=%d  ctxB userContextId=%d", a.UserContextID, b.UserContextID)

	// D.4 assertions.
	if a.UserContextID == 0 || b.UserContextID == 0 {
		t.Fatalf("userContextId not returned (A=%d B=%d) — D.4 not built into this binary", a.UserContextID, b.UserContextID)
	}
	if a.UserContextID == b.UserContextID {
		t.Fatalf("two contexts share userContextId %d — not isolated", a.UserContextID)
	}

	// C delivery: seed audio per-context via the privileged roverfox pref path —
	// the same path production uses (orchestrator buildContextFingerprintPrefs)
	// and that AudioFingerprintManager::GetSeed reads. The older content-world
	// window.setAudioFingerprintSeed setter is now [ChromeOnly], so init-script
	// delivery from content no longer reaches it.
	const seedA, seedB = 111111, 222222
	setSeed := func(c ctxInfo, seed int) {
		if _, err := client.Call("", "Browser.setContextFingerprint", mustJSON(map[string]interface{}{
			"prefs": []map[string]interface{}{{
				"name":  fmt.Sprintf("roverfox.s.audioFingerprintSeed_%d", c.UserContextID),
				"value": strconv.Itoa(seed),
			}},
		})); err != nil {
			t.Fatalf("setContextFingerprint(ctx=%d): %v", c.UserContextID, err)
		}
	}
	setSeed(a, seedA)
	setSeed(b, seedB)

	sessA := newPageInContext(t, client, a.BrowserContextID)
	sessB := newPageInContext(t, client, b.BrowserContextID)
	time.Sleep(1 * time.Second)

	if _, ok := latestContext.Load(sessA); !ok {
		t.Fatal("no execution context for ctxA page")
	}
	if _, ok := latestContext.Load(sessB); !ok {
		t.Fatal("no execution context for ctxB page")
	}

	fpA := audioFP(t, client, sessA)
	fpB := audioFP(t, client, sessB)
	t.Logf("audioFP ctxA(seed=%d)=%v  ctxB(seed=%d)=%v", seedA, fpA, seedB, fpB)

	if fpA == fpB {
		t.Fatalf("per-context audio fingerprints identical (%v) — pref delivery is NOT isolating contexts", fpA)
	}
	t.Logf("per-context audio isolation confirmed via pref delivery: Δfp=%v", fpA-fpB)
}
