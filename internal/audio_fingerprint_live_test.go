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

// evalNumber evaluates expr in the page's latest execution context and returns
// the numeric result (Runtime.evaluate has no awaitPromise, so async work is
// driven via the kick-off-then-poll pattern below).
func evalNumber(t *testing.T, client *juggler.Client, sessionID, expr string) float64 {
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
			Value float64 `json:"value"`
		} `json:"result"`
	}
	json.Unmarshal(res, &e)
	return e.Result.Value
}

// audioRenderKick starts an OfflineAudioContext render (the classic audio
// fingerprint) asynchronously, stashing the summed output on window. Reading the
// rendered buffer via getChannelData exercises AudioBuffer::RestoreJSChannelData,
// where the per-context audio-fingerprint transformation is applied.
const audioRenderKick = `
window.__afpDone = false; window.__afpVal = -1;
(async () => {
  const ctx = new OfflineAudioContext(1, 44100, 44100);
  const osc = ctx.createOscillator();
  osc.type = 'triangle'; osc.frequency.value = 10000;
  const comp = ctx.createDynamicsCompressor();
  comp.threshold.value = -50; comp.knee.value = 40; comp.ratio.value = 12;
  comp.attack.value = 0; comp.release.value = 0.25;
  osc.connect(comp); comp.connect(ctx.destination); osc.start(0);
  const r = await ctx.startRendering();
  const d = r.getChannelData(0);
  let s = 0; for (let i = 4500; i < 5000; i++) s += Math.abs(d[i]);
  window.__afpVal = s; window.__afpDone = true;
})().catch(() => { window.__afpVal = -2; window.__afpDone = true; });
0;`

func audioFP(t *testing.T, client *juggler.Client, sessionID string) float64 {
	t.Helper()
	evalNumber(t, client, sessionID, audioRenderKick)
	for i := 0; i < 100; i++ {
		if evalNumber(t, client, sessionID, "window.__afpDone ? 1 : 0") == 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	v := evalNumber(t, client, sessionID, "window.__afpVal")
	if v == -2 {
		t.Fatal("audio render threw in-page")
	}
	return v
}

// TestLiveAudioFingerprint runtime-verifies the production audio-fingerprint
// path: Browser.setContextFingerprint writes a per-context
// roverfox.s.audioFingerprintSeed_<userContextId> pref, the page-visible setter
// remains hidden, and WebAudio output is deterministic and seed-dependent.
func TestLiveAudioFingerprint(t *testing.T) {
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
		if c.UserContextID == 0 {
			t.Fatal("Browser.createBrowserContext returned userContextId=0; per-context audio seeding is unavailable")
		}
		return c
	}

	baseCtx := mkctx()
	seedCtx := mkctx()
	if baseCtx.UserContextID == seedCtx.UserContextID {
		t.Fatalf("two contexts share userContextId %d", baseCtx.UserContextID)
	}

	const seed = 777777
	if _, err := client.Call("", "Browser.setContextFingerprint", mustJSON(map[string]interface{}{
		"prefs": []map[string]interface{}{{
			"name":  fmt.Sprintf("roverfox.s.audioFingerprintSeed_%d", seedCtx.UserContextID),
			"value": strconv.Itoa(seed),
		}},
	})); err != nil {
		t.Fatalf("setContextFingerprint(ctx=%d): %v", seedCtx.UserContextID, err)
	}

	baseSessionID := newPageInContext(t, client, baseCtx.BrowserContextID)
	seedSessionID := newPageInContext(t, client, seedCtx.BrowserContextID)
	// about:blank is sufficient for OfflineAudioContext; wait for content-process
	// init and execution-context tracking.
	time.Sleep(1 * time.Second)
	if _, ok := latestContext.Load(baseSessionID); !ok {
		t.Fatal("no execution context for baseline context")
	}
	if _, ok := latestContext.Load(seedSessionID); !ok {
		t.Fatal("no execution context for seeded context")
	}

	if evalNumber(t, client, baseSessionID, "typeof window.setAudioFingerprintSeed === 'undefined' ? 1 : 0") != 1 {
		t.Fatal("window.setAudioFingerprintSeed is page-visible; expected ChromeOnly production path")
	}

	base1 := audioFP(t, client, baseSessionID)
	base2 := audioFP(t, client, baseSessionID)
	if base1 != base2 {
		t.Errorf("baseline not deterministic: %v vs %v", base1, base2)
	}

	s1 := audioFP(t, client, seedSessionID)
	s2 := audioFP(t, client, seedSessionID)
	t.Logf("baseline(ctx=%d)=%v ; seeded(ctx=%d seed=%d)=%v (rerun %v)",
		baseCtx.UserContextID, base1, seedCtx.UserContextID, seed, s1, s2)

	if s1 != s2 {
		t.Errorf("seeded render not deterministic: %v vs %v", s1, s2)
	}
	if s1 == base1 {
		t.Errorf("seed had NO effect: baseline and seeded both produced %v — transformation not applied on the OfflineAudioContext getChannelData path", base1)
	} else {
		t.Logf("transformation applied: seed shifted fingerprint by %v", s1-base1)
	}
}
