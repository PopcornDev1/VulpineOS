package proxy

import (
	"encoding/json"
	"testing"
)

func TestParseProxyURL_HostPort(t *testing.T) {
	p, err := ParseProxyURL("1.2.3.4:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Type != "http" {
		t.Errorf("expected type http, got %s", p.Type)
	}
	if p.Host != "1.2.3.4" {
		t.Errorf("expected host 1.2.3.4, got %s", p.Host)
	}
	if p.Port != 8080 {
		t.Errorf("expected port 8080, got %d", p.Port)
	}
}

func TestParseProxyURL_HTTP(t *testing.T) {
	p, err := ParseProxyURL("http://proxy.example.com:3128")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Type != "http" || p.Host != "proxy.example.com" || p.Port != 3128 {
		t.Errorf("unexpected result: %+v", p)
	}
}

func TestParseProxyURL_WithAuth(t *testing.T) {
	p, err := ParseProxyURL("socks5://user:p%40ss@10.0.0.1:1080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Type != "socks5" {
		t.Errorf("expected socks5, got %s", p.Type)
	}
	if p.Username != "user" {
		t.Errorf("expected username user, got %s", p.Username)
	}
	if p.Password != "p@ss" {
		t.Errorf("expected password p@ss, got %s", p.Password)
	}
}

func TestParseProxyURL_Empty(t *testing.T) {
	_, err := ParseProxyURL("")
	if err == nil {
		t.Fatal("expected error for empty string")
	}
}

func TestParseProxyURL_InvalidScheme(t *testing.T) {
	_, err := ParseProxyURL("ftp://host:80")
	if err == nil {
		t.Fatal("expected error for ftp scheme")
	}
}

func TestParseProxyURL_DefaultPorts(t *testing.T) {
	tests := []struct {
		raw      string
		wantPort int
	}{
		{"http://host", 80},
		{"https://host", 443},
		{"socks5://host", 1080},
	}
	for _, tt := range tests {
		p, err := ParseProxyURL(tt.raw)
		if err != nil {
			t.Errorf("ParseProxyURL(%q) error: %v", tt.raw, err)
			continue
		}
		if p.Port != tt.wantPort {
			t.Errorf("ParseProxyURL(%q) port = %d, want %d", tt.raw, p.Port, tt.wantPort)
		}
	}
}

func TestParseProxyList(t *testing.T) {
	text := `
# US proxies
1.2.3.4:8080
http://5.6.7.8:3128

# EU proxies
socks5://user:pass@10.0.0.1:1080

`
	proxies, err := ParseProxyList(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proxies) != 3 {
		t.Fatalf("expected 3 proxies, got %d", len(proxies))
	}
	if proxies[0].Host != "1.2.3.4" {
		t.Errorf("first proxy host = %s, want 1.2.3.4", proxies[0].Host)
	}
	if proxies[2].Type != "socks5" {
		t.Errorf("third proxy type = %s, want socks5", proxies[2].Type)
	}
}

func TestParseProxyList_Empty(t *testing.T) {
	proxies, err := ParseProxyList("# only comments\n\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proxies) != 0 {
		t.Fatalf("expected 0 proxies, got %d", len(proxies))
	}
}

func TestProxyConfig_String(t *testing.T) {
	p := ProxyConfig{Type: "http", Host: "1.2.3.4", Port: 8080}
	got := p.String()
	want := "http 1.2.3.4:8080"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestProxyConfig_URL(t *testing.T) {
	p := ProxyConfig{Type: "socks5", Host: "10.0.0.1", Port: 1080, Username: "u", Password: "p"}
	got := p.URL()
	want := "socks5://u:p@10.0.0.1:1080"
	if got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
}

func TestProxyConfig_URL_NoAuth(t *testing.T) {
	p := ProxyConfig{Type: "http", Host: "proxy.test", Port: 3128}
	got := p.URL()
	want := "http://proxy.test:3128"
	if got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
}

func TestSyncFingerprintToProxy(t *testing.T) {
	fp := `{"ua":"Mozilla/5.0","screen":"1920x1080"}`
	geo := &GeoInfo{
		IP:       "203.0.113.1",
		Timezone: "America/New_York",
		Lat:      40.7128,
		Lon:      -74.0060,
		Country:  "United States",
		City:     "New York",
		Region:   "New York",
		ISP:      "Example ISP",
	}

	result, err := SyncFingerprintToProxy(fp, geo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		t.Fatalf("parse result: %v", err)
	}

	if m["geolocation:latitude"] != 40.7128 {
		t.Errorf("lat = %v, want 40.7128", m["geolocation:latitude"])
	}
	if m["geolocation:longitude"] != -74.006 {
		t.Errorf("lon = %v, want -74.006", m["geolocation:longitude"])
	}
	if m["geolocation:accuracy"] != 50.0 {
		t.Errorf("accuracy = %v, want 50.0", m["geolocation:accuracy"])
	}
	if m["timezone"] != "America/New_York" {
		t.Errorf("timezone = %v, want America/New_York", m["timezone"])
	}
	if m["webrtc:ipv4"] != "203.0.113.1" {
		t.Errorf("webrtc:ipv4 = %v, want 203.0.113.1", m["webrtc:ipv4"])
	}
	if m["navigator.language"] != "en-US" {
		t.Errorf("navigator.language = %v, want en-US", m["navigator.language"])
	}
	// Original fields preserved
	if m["ua"] != "Mozilla/5.0" {
		t.Errorf("ua = %v, want Mozilla/5.0", m["ua"])
	}
}

func TestSyncFingerprintToProxy_NilGeo(t *testing.T) {
	_, err := SyncFingerprintToProxy(`{}`, nil)
	if err == nil {
		t.Fatal("expected error for nil geo")
	}
}
