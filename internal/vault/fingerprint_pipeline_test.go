package vault

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateFingerprintPublicFallback(t *testing.T) {
	fp, err := GenerateFingerprint("test-public-profile")
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
	if strings.Contains(ua, "Camoufox") {
		t.Fatalf("fallback user agent exposes Camoufox: %s", ua)
	}
	if !strings.Contains(ua, "Firefox/146.0") || !strings.Contains(ua, "rv:146.0") {
		t.Fatalf("fallback user agent is not aligned with bundled Firefox 146: %s", ua)
	}
	if langs, ok := data["navigator.languages"].([]interface{}); !ok || len(langs) < 2 {
		t.Fatalf("navigator.languages = %#v, want array with primary and base language", data["navigator.languages"])
	}
	if timezone, ok := data["timezone"].(string); !ok || timezone == "" {
		t.Fatalf("timezone = %#v, want non-empty timezone", data["timezone"])
	}

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

	// Different seeds should produce different fingerprints even when lengths match.
	fp3, err := generateFallback("seed-def", "mac")
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
	if strings.Contains(data.UserAgent, "Camoufox") || !strings.Contains(data.UserAgent, "Firefox/146.0") {
		t.Errorf("Mac user agent = %q, want Firefox 146 without Camoufox branding", data.UserAgent)
	}
	if data.Timezone == "" {
		t.Error("Mac timezone is empty")
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
