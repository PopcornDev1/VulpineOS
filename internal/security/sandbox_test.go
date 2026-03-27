package security

import (
	"strings"
	"testing"
)

func TestNewSandbox_DefaultAPIs(t *testing.T) {
	s := NewSandbox()
	apis := s.BlockedAPIs()
	expected := []string{"fetch", "XMLHttpRequest", "WebSocket", "navigator.sendBeacon"}
	if len(apis) != len(expected) {
		t.Fatalf("expected %d blocked APIs, got %d", len(expected), len(apis))
	}
	for i, api := range expected {
		if apis[i] != api {
			t.Errorf("expected API %q at index %d, got %q", api, i, apis[i])
		}
	}
}

func TestBlockAPI_NoDuplicates(t *testing.T) {
	s := NewSandbox()
	s.BlockAPI("fetch") // already exists
	apis := s.BlockedAPIs()
	count := 0
	for _, a := range apis {
		if a == "fetch" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 'fetch', got %d", count)
	}
}

func TestBlockAPI_Custom(t *testing.T) {
	s := NewSandbox()
	s.BlockAPI("eval")
	apis := s.BlockedAPIs()
	found := false
	for _, a := range apis {
		if a == "eval" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'eval' in blocked APIs")
	}
}

func TestWrapExpression(t *testing.T) {
	s := NewSandbox()
	wrapped := s.WrapExpression("document.title")

	// Should contain IIFE wrapper
	if !strings.HasPrefix(wrapped, "(function() {") {
		t.Error("expected IIFE prefix")
	}
	if !strings.HasSuffix(wrapped, "})()") {
		t.Error("expected IIFE suffix")
	}

	// Should save and restore blocked APIs
	for _, api := range defaultBlockedAPIs {
		safeName := safeVarName(api)
		if !strings.Contains(wrapped, "_save_"+safeName) {
			t.Errorf("expected save variable for %s", api)
		}
	}

	// Should block fetch
	if !strings.Contains(wrapped, "fetch = undefined") {
		t.Error("expected fetch to be set to undefined")
	}

	// Should block XMLHttpRequest
	if !strings.Contains(wrapped, "XMLHttpRequest = undefined") {
		t.Error("expected XMLHttpRequest to be set to undefined")
	}

	// Should block WebSocket
	if !strings.Contains(wrapped, "WebSocket = undefined") {
		t.Error("expected WebSocket to be set to undefined")
	}

	// Should handle navigator.sendBeacon as property
	if !strings.Contains(wrapped, "navigator.sendBeacon = function()") {
		t.Error("expected navigator.sendBeacon to be blocked as function")
	}

	// Should contain the expression
	if !strings.Contains(wrapped, "return (document.title)") {
		t.Error("expected expression in return statement")
	}

	// Should restore in finally block
	if !strings.Contains(wrapped, "finally {") {
		t.Error("expected finally block for restoration")
	}
	if !strings.Contains(wrapped, "fetch = _save_fetch") {
		t.Error("expected fetch restoration in finally")
	}
}

func TestWrapFunction(t *testing.T) {
	s := NewSandbox()
	wrapped := s.WrapFunction("function foo() { return 42; }")

	// Should not have "return" before the function
	if strings.Contains(wrapped, "return (function foo") {
		t.Error("function wrapping should not use return (expr)")
	}

	// Should contain the function declaration
	if !strings.Contains(wrapped, "function foo() { return 42; }") {
		t.Error("expected function declaration in wrapper")
	}
}

func TestWrapExpression_CustomBlockedAPI(t *testing.T) {
	s := NewSandbox()
	s.BlockAPI("eval")
	wrapped := s.WrapExpression("1+1")

	if !strings.Contains(wrapped, "eval = undefined") {
		t.Error("expected custom blocked API 'eval' in wrapper")
	}
	if !strings.Contains(wrapped, "_save_eval") {
		t.Error("expected save variable for custom blocked API")
	}
}

func TestWrapExpression_SandboxErrorMessage(t *testing.T) {
	s := NewSandbox()
	wrapped := s.WrapExpression("1")
	// navigator.sendBeacon should throw with our error message
	if !strings.Contains(wrapped, "blocked by VulpineOS sandbox") {
		t.Error("expected VulpineOS sandbox error message for property-based API blocking")
	}
}
