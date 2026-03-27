package security

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultCSPConfig(t *testing.T) {
	cfg := DefaultCSPConfig()
	if !cfg.BlockInlineScripts {
		t.Error("expected BlockInlineScripts to be true by default")
	}
	if cfg.BlockExternalScripts {
		t.Error("expected BlockExternalScripts to be false by default")
	}
	if cfg.BlockDataURIs {
		t.Error("expected BlockDataURIs to be false by default")
	}
}

func TestGenerateCSP_Default(t *testing.T) {
	csp := GenerateCSP(DefaultCSPConfig())
	if !strings.Contains(csp, "'self'") {
		t.Error("expected CSP to contain 'self'")
	}
	if strings.Contains(csp, "'unsafe-inline'") {
		t.Error("default CSP should not contain 'unsafe-inline'")
	}
	if !strings.Contains(csp, "https:") {
		t.Error("default CSP should allow https: for external scripts")
	}
}

func TestGenerateCSP_AllowInline(t *testing.T) {
	cfg := CSPConfig{
		BlockInlineScripts: false,
	}
	csp := GenerateCSP(cfg)
	if !strings.Contains(csp, "'unsafe-inline'") {
		t.Error("expected 'unsafe-inline' when BlockInlineScripts is false")
	}
}

func TestGenerateCSP_BlockExternal(t *testing.T) {
	cfg := CSPConfig{
		BlockInlineScripts:   true,
		BlockExternalScripts: true,
	}
	csp := GenerateCSP(cfg)
	if strings.Contains(csp, "https:") {
		t.Error("expected no https: when blocking external scripts")
	}
	if !strings.Contains(csp, "'self'") {
		t.Error("expected 'self' in script-src")
	}
}

func TestGenerateCSP_BlockDataURIs(t *testing.T) {
	cfg := CSPConfig{
		BlockDataURIs: true,
	}
	csp := GenerateCSP(cfg)
	if !strings.Contains(csp, "default-src") {
		t.Error("expected default-src directive when blocking data URIs")
	}
	if !strings.Contains(csp, "img-src") {
		t.Error("expected img-src directive when blocking data URIs")
	}
}

func TestGenerateCSP_CustomDirectives(t *testing.T) {
	cfg := CSPConfig{
		CustomDirectives: "frame-ancestors 'none'",
	}
	csp := GenerateCSP(cfg)
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("expected custom directive in CSP, got: %s", csp)
	}
}

func TestInjectCSPRaw(t *testing.T) {
	cfg := DefaultCSPConfig()
	raw, err := InjectCSPRaw("ctx-123", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var params struct {
		BrowserContextID string              `json:"browserContextId"`
		Headers          []map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("failed to unmarshal params: %v", err)
	}

	if params.BrowserContextID != "ctx-123" {
		t.Errorf("expected context ID ctx-123, got %s", params.BrowserContextID)
	}
	if len(params.Headers) != 1 {
		t.Fatalf("expected 1 header, got %d", len(params.Headers))
	}
	if params.Headers[0]["name"] != "Content-Security-Policy" {
		t.Errorf("expected CSP header name, got %s", params.Headers[0]["name"])
	}
	if !strings.Contains(params.Headers[0]["value"], "'self'") {
		t.Errorf("expected CSP value to contain 'self', got %s", params.Headers[0]["value"])
	}
}
