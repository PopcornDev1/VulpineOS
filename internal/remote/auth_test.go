package remote

import (
	"net/http"
	"net/url"
	"testing"
)

func TestNewAuthenticator(t *testing.T) {
	a := NewAuthenticator("test-key")
	if a == nil {
		t.Fatal("expected non-nil authenticator")
	}
	if a.apiKey != "test-key" {
		t.Errorf("apiKey = %q, want %q", a.apiKey, "test-key")
	}
}

func TestValidateEmptyKeyAllowsAll(t *testing.T) {
	a := NewAuthenticator("")
	req := &http.Request{
		Header: http.Header{},
		URL:    &url.URL{},
	}
	if !a.Validate(req) {
		t.Error("empty API key should allow all requests")
	}
}

func TestValidateBearerHeaderCorrectKey(t *testing.T) {
	a := NewAuthenticator("secret-key-123")
	req := &http.Request{
		Header: http.Header{
			"Authorization": []string{"Bearer secret-key-123"},
		},
		URL: &url.URL{},
	}
	if !a.Validate(req) {
		t.Error("correct Bearer token should be accepted")
	}
}

func TestValidateBearerHeaderWrongKey(t *testing.T) {
	a := NewAuthenticator("secret-key-123")
	req := &http.Request{
		Header: http.Header{
			"Authorization": []string{"Bearer wrong-key"},
		},
		URL: &url.URL{},
	}
	if a.Validate(req) {
		t.Error("wrong Bearer token should be rejected")
	}
}

func TestValidateBearerHeaderEmpty(t *testing.T) {
	a := NewAuthenticator("secret-key-123")
	req := &http.Request{
		Header: http.Header{},
		URL:    &url.URL{},
	}
	if a.Validate(req) {
		t.Error("missing auth should be rejected when key is configured")
	}
}

func TestValidateQueryTokenCorrectKey(t *testing.T) {
	a := NewAuthenticator("secret-key-123")
	req := &http.Request{
		Header: http.Header{},
		URL:    &url.URL{RawQuery: "token=secret-key-123"},
	}
	if !a.Validate(req) {
		t.Error("correct query token should be accepted")
	}
}

func TestValidateQueryTokenWrongKey(t *testing.T) {
	a := NewAuthenticator("secret-key-123")
	req := &http.Request{
		Header: http.Header{},
		URL:    &url.URL{RawQuery: "token=wrong-key"},
	}
	if a.Validate(req) {
		t.Error("wrong query token should be rejected")
	}
}

func TestValidateBearerTakesPrecedence(t *testing.T) {
	a := NewAuthenticator("correct-key")
	// Correct bearer, wrong query param — should pass via bearer
	req := &http.Request{
		Header: http.Header{
			"Authorization": []string{"Bearer correct-key"},
		},
		URL: &url.URL{RawQuery: "token=wrong-key"},
	}
	if !a.Validate(req) {
		t.Error("correct Bearer should pass even with wrong query param")
	}
}

func TestValidateQueryFallback(t *testing.T) {
	a := NewAuthenticator("correct-key")
	// Wrong bearer, correct query param — should pass via query
	req := &http.Request{
		Header: http.Header{
			"Authorization": []string{"Bearer wrong-key"},
		},
		URL: &url.URL{RawQuery: "token=correct-key"},
	}
	if !a.Validate(req) {
		t.Error("correct query token should pass when Bearer is wrong")
	}
}

func TestValidateMalformedAuthHeader(t *testing.T) {
	a := NewAuthenticator("key")
	tests := []struct {
		name   string
		header string
	}{
		{"empty", ""},
		{"no_bearer_prefix", "key"},
		{"basic_auth", "Basic dXNlcjpwYXNz"},
		{"bearer_only", "Bearer"},
		{"short", "Bear k"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &http.Request{
				Header: http.Header{
					"Authorization": []string{tt.header},
				},
				URL: &url.URL{},
			}
			if a.Validate(req) {
				t.Errorf("malformed auth header %q should be rejected", tt.header)
			}
		})
	}
}

func TestValidateConstantTimeComparison(t *testing.T) {
	// This is a structural test — we verify that different-length keys
	// are still rejected (constant-time compare pads or returns 0 for length mismatch).
	a := NewAuthenticator("short")
	req := &http.Request{
		Header: http.Header{
			"Authorization": []string{"Bearer a-much-longer-key-that-differs"},
		},
		URL: &url.URL{},
	}
	if a.Validate(req) {
		t.Error("different-length key should be rejected")
	}
}
