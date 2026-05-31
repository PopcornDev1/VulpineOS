package internal

import (
	"encoding/json"
	"fmt"
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

// TestLiveAudioFingerprint runtime-verifies the audio-fingerprint feature:
// window.setAudioFingerprintSeed is exposed, applies a SEED-DEPENDENT transform
// to WebAudio output, and is DETERMINISTIC for a given seed.
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

	sessionID, _ := createPageWithFrame(t, client)
	// createPageWithFrame already triggered content-process init and an execution
	// context (captured by setupContextTracking). about:blank is sufficient for
	// OfflineAudioContext + setAudioFingerprintSeed — no navigation needed.
	time.Sleep(1 * time.Second)
	if _, ok := latestContext.Load(sessionID); !ok {
		t.Fatal("no execution context after createPageWithFrame")
	}

	if evalNumber(t, client, sessionID, "typeof window.setAudioFingerprintSeed === 'function' ? 1 : 0") != 1 {
		t.Fatal("window.setAudioFingerprintSeed is not exposed — audio-fingerprint feature not built/enabled")
	}
	t.Log("setAudioFingerprintSeed exposed ✓")

	// Baseline first: no seed set this session. NOTE: the WebIDL setter
	// self-disables after the first call (SetSeed -> DisableFunction by design),
	// so we measure baseline, then set the seed exactly ONCE, then re-measure.
	base1 := audioFP(t, client, sessionID)
	base2 := audioFP(t, client, sessionID)
	if base1 != base2 {
		t.Errorf("baseline not deterministic: %v vs %v", base1, base2)
	}

	evalNumber(t, client, sessionID, fmt.Sprintf("window.setAudioFingerprintSeed(%d); 0", 777777))

	s1 := audioFP(t, client, sessionID)
	s2 := audioFP(t, client, sessionID)
	t.Logf("baseline=%v ; seeded(777777)=%v (rerun %v)", base1, s1, s2)

	if s1 != s2 {
		t.Errorf("seeded render not deterministic: %v vs %v", s1, s2)
	}
	if s1 == base1 {
		t.Errorf("seed had NO effect: baseline and seeded both produced %v — transformation not applied on the OfflineAudioContext getChannelData path", base1)
	} else {
		t.Logf("transformation applied: seed shifted fingerprint by %v", s1-base1)
	}
}
