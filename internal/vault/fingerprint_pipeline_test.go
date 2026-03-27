package vault

import (
	"encoding/json"
	"testing"
)

func TestFullBrowserForgePipeline(t *testing.T) {
	fp, err := GenerateFingerprint("test-full-pipeline")
	if err != nil {
		t.Fatalf("GenerateFingerprint: %v", err)
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(fp), &data); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Must have a user agent
	ua, ok := data["navigator.userAgent"].(string)
	if !ok || ua == "" {
		t.Fatal("missing navigator.userAgent")
	}
	t.Logf("UA: %s", ua[:min(80, len(ua))])

	// Must have screen dimensions
	if _, ok := data["screen.width"]; !ok {
		t.Error("missing screen.width")
	}
	if _, ok := data["screen.height"]; !ok {
		t.Error("missing screen.height")
	}

	// Must have platform
	if _, ok := data["navigator.platform"]; !ok {
		t.Error("missing navigator.platform")
	}

	// Should have WebGL data (from camoufox.webgl.sample_webgl)
	hasWebGL := false
	for k := range data {
		if len(k) > 5 && k[:5] == "webGl" {
			hasWebGL = true
			break
		}
	}
	if hasWebGL {
		t.Logf("WebGL: present (%d total keys)", len(data))
	} else {
		t.Log("WebGL: not present (BrowserForge may not include it)")
	}

	// Should have noise seeds for canvas/audio
	if seed, ok := data["canvas:seed"]; ok {
		t.Logf("Canvas seed: %v", seed)
	}
	if seed, ok := data["audio:seed"]; ok {
		t.Logf("Audio seed: %v", seed)
	}

	t.Logf("Total fingerprint properties: %d", len(data))

	// FingerprintData parsing should work
	summary := FingerprintSummary(fp)
	if summary == "" {
		t.Error("FingerprintSummary returned empty")
	}
	t.Logf("Summary: %s", summary)
}

func TestFingerprintDeterministic(t *testing.T) {
	// Same seed should produce same fallback fingerprint
	fp1, err := generateFallback("seed-abc", "mac")
	if err != nil {
		t.Fatal(err)
	}
	fp2, err := generateFallback("seed-abc", "mac")
	if err != nil {
		t.Fatal(err)
	}
	if fp1 != fp2 {
		t.Error("same seed should produce same fingerprint")
	}

	// Different length seeds should produce different fingerprints
	fp3, err := generateFallback("a-much-longer-seed-string", "mac")
	if err != nil {
		t.Fatal(err)
	}
	if fp1 == fp3 {
		t.Error("different seeds should produce different fingerprints")
	}
}

func TestFingerprintOSConsistency(t *testing.T) {
	// Mac fingerprint should have Mac-like values
	fp, err := generateFallback("test", "mac")
	if err != nil {
		t.Fatal(err)
	}
	var data FingerprintData
	json.Unmarshal([]byte(fp), &data)

	if data.Platform != "MacIntel" {
		t.Errorf("Mac platform should be MacIntel, got %s", data.Platform)
	}
	if data.UserAgent == "" {
		t.Error("UserAgent is empty")
	}

	// Windows fingerprint
	fp, err = generateFallback("test", "win")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(fp), &data)
	if data.Platform != "Win32" {
		t.Errorf("Windows platform should be Win32, got %s", data.Platform)
	}

	// Linux fingerprint
	fp, err = generateFallback("test", "lin")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(fp), &data)
	if data.Platform != "Linux x86_64" {
		t.Errorf("Linux platform should be Linux x86_64, got %s", data.Platform)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
