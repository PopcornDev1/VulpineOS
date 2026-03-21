package proxy

import (
	"encoding/json"
	"testing"
)

func TestSyncFingerprintToProxy_PreservesExistingFields(t *testing.T) {
	fpJSON := `{"navigator.userAgent":"Mozilla/5.0","screen.width":1920,"existing_field":"keep_me"}`
	geo := &GeoInfo{
		IP:       "203.0.113.42",
		Timezone: "America/New_York",
		Lat:      40.7128,
		Lon:      -74.0060,
		Country:  "United States",
		City:     "New York",
		Region:   "New York",
		ISP:      "Example ISP",
	}

	result, err := SyncFingerprintToProxy(fpJSON, geo)
	if err != nil {
		t.Fatalf("SyncFingerprintToProxy: %v", err)
	}

	var fp map[string]interface{}
	if err := json.Unmarshal([]byte(result), &fp); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}

	// Verify all geo fields injected
	checks := map[string]interface{}{
		"geolocation:latitude":  40.7128,
		"geolocation:longitude": -74.006,
		"geolocation:accuracy":  50.0,
		"timezone":              "America/New_York",
		"webrtc:ipv4":           "203.0.113.42",
		"navigator.language":    "en-US",
	}
	for key, want := range checks {
		if fp[key] != want {
			t.Errorf("%s = %v, want %v", key, fp[key], want)
		}
	}

	// Verify existing fields are preserved
	if fp["navigator.userAgent"] != "Mozilla/5.0" {
		t.Errorf("existing navigator.userAgent was lost")
	}
	if fp["screen.width"] != float64(1920) {
		t.Errorf("existing screen.width was lost")
	}
	if fp["existing_field"] != "keep_me" {
		t.Errorf("existing_field was lost")
	}
}

func TestSyncFingerprintToProxy_EmptyFingerprint(t *testing.T) {
	geo := &GeoInfo{
		IP:       "198.51.100.1",
		Timezone: "Europe/Berlin",
		Lat:      52.52,
		Lon:      13.405,
		Country:  "Germany",
	}

	result, err := SyncFingerprintToProxy("{}", geo)
	if err != nil {
		t.Fatalf("SyncFingerprintToProxy with empty fp: %v", err)
	}

	var fp map[string]interface{}
	if err := json.Unmarshal([]byte(result), &fp); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}

	if fp["geolocation:latitude"] != 52.52 {
		t.Errorf("latitude = %v, want 52.52", fp["geolocation:latitude"])
	}
	if fp["timezone"] != "Europe/Berlin" {
		t.Errorf("timezone = %v, want Europe/Berlin", fp["timezone"])
	}
	if fp["navigator.language"] != "de-DE" {
		t.Errorf("navigator.language = %v, want de-DE (for Germany)", fp["navigator.language"])
	}
	if fp["webrtc:ipv4"] != "198.51.100.1" {
		t.Errorf("webrtc:ipv4 = %v, want 198.51.100.1", fp["webrtc:ipv4"])
	}
}

func TestSyncFingerprintToProxy_UnknownCountryDefaultsEnUS(t *testing.T) {
	geo := &GeoInfo{
		IP:       "10.0.0.1",
		Timezone: "Pacific/Fiji",
		Country:  "Fiji",
	}

	result, err := SyncFingerprintToProxy("{}", geo)
	if err != nil {
		t.Fatal(err)
	}

	var fp map[string]interface{}
	json.Unmarshal([]byte(result), &fp)

	if fp["navigator.language"] != "en-US" {
		t.Errorf("navigator.language = %v, want en-US (fallback for unmapped country)", fp["navigator.language"])
	}
}

func TestLocaleForCountry(t *testing.T) {
	tests := []struct {
		country string
		want    string
	}{
		{"United States", "en-US"},
		{"Germany", "de-DE"},
		{"Japan", "ja-JP"},
		{"united states", "en-US"}, // case-insensitive
		{"GERMANY", "de-DE"},       // case-insensitive
		{"Unknown Land", "en-US"},  // fallback
		{"Brazil", "pt-BR"},
		{"South Korea", "ko-KR"},
		{"", "en-US"}, // empty string fallback
	}

	for _, tt := range tests {
		got := localeForCountry(tt.country)
		if got != tt.want {
			t.Errorf("localeForCountry(%q) = %q, want %q", tt.country, got, tt.want)
		}
	}
}

func TestSyncFingerprintToProxy_InvalidJSON(t *testing.T) {
	geo := &GeoInfo{IP: "1.2.3.4", Timezone: "UTC", Country: "US"}
	_, err := SyncFingerprintToProxy("not valid json", geo)
	if err == nil {
		t.Fatal("expected error for invalid JSON fingerprint")
	}
}
