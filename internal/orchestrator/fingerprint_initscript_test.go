package orchestrator

import (
	"testing"

	"vulpineos/internal/vault"
)

func prefValue(prefs []map[string]interface{}, name string) (string, bool) {
	for _, p := range prefs {
		if p["name"] == name {
			v, _ := p["value"].(string)
			return v, true
		}
	}
	return "", false
}

func TestBuildContextFingerprintPrefsEmptyWhenNoPerContextFields(t *testing.T) {
	// Only stock-override fields present (UA/platform/screen) -> no roverfox prefs.
	fp := vault.FingerprintData{UserAgent: "Mozilla/5.0", Platform: "MacIntel", ScreenWidth: 1920, ScreenHeight: 1080}
	if got := buildContextFingerprintPrefs(fp, 7); got != nil {
		t.Fatalf("expected nil, got: %v", got)
	}
}

func TestBuildContextFingerprintPrefsKeysAndUcid(t *testing.T) {
	fp := vault.FingerprintData{
		WebGLVendor:         "Apple",
		WebGLRenderer:       "Apple M1",
		AudioSeed:           123456,
		FontSpacingSeed:     7891011,
		OsCPU:               "Intel Mac OS X 10.15",
		HardwareConcurrency: 8,
		WebRTCIPv4:          "203.0.113.45",
	}
	prefs := buildContextFingerprintPrefs(fp, 6)
	if len(prefs) == 0 {
		t.Fatal("expected prefs")
	}
	// Exact roverfox key contract, keyed by userContextId, values as strings.
	want := map[string]string{
		"roverfox.s.webgl_vendor_6":          "Apple",
		"roverfox.s.webgl_renderer_6":        "Apple M1",
		"roverfox.s.audioFingerprintSeed_6":  "123456",
		"roverfox.s.seed_6":                  "7891011",
		"roverfox.s.nav_oscpu_6":             "Intel Mac OS X 10.15",
		"roverfox.s.nav_hwc_6":               "8",
		"roverfox.s.webrtc_ipv4_6":           "203.0.113.45",
	}
	for name, val := range want {
		got, ok := prefValue(prefs, name)
		if !ok {
			t.Errorf("missing pref %q", name)
			continue
		}
		if got != val {
			t.Errorf("pref %q = %q, want %q", name, got, val)
		}
	}
	// All pref names must stay in the roverfox namespace (the handler rejects others).
	for _, p := range prefs {
		n, _ := p["name"].(string)
		if len(n) < len("roverfox.s.") || n[:len("roverfox.s.")] != "roverfox.s." {
			t.Errorf("pref name escapes roverfox namespace: %q", n)
		}
	}
}

func TestBuildContextFingerprintPrefsDistinctPerUcid(t *testing.T) {
	fp := vault.FingerprintData{AudioSeed: 42}
	a := buildContextFingerprintPrefs(fp, 6)
	b := buildContextFingerprintPrefs(fp, 7)
	an, _ := a[0]["name"].(string)
	bn, _ := b[0]["name"].(string)
	if an == bn {
		t.Fatalf("pref names not keyed per userContextId: %q == %q", an, bn)
	}
}
