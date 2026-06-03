package mcp

import (
	"testing"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		max   int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc..."},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.max)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
		}
	}
}

func TestScreenshotTracker_SetGet(t *testing.T) {
	tracker := NewScreenshotTracker()

	// Empty
	if got := tracker.Get("session1"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}

	// Set and get
	tracker.Set("session1", "base64data1")
	if got := tracker.Get("session1"); got != "base64data1" {
		t.Errorf("expected 'base64data1', got %q", got)
	}

	// Different sessions are isolated
	tracker.Set("session2", "base64data2")
	if got := tracker.Get("session1"); got != "base64data1" {
		t.Errorf("session1 should still be 'base64data1', got %q", got)
	}
	if got := tracker.Get("session2"); got != "base64data2" {
		t.Errorf("session2 should be 'base64data2', got %q", got)
	}

	// Overwrite
	tracker.Set("session1", "updated")
	if got := tracker.Get("session1"); got != "updated" {
		t.Errorf("expected 'updated', got %q", got)
	}
}

func TestNewScreenshotTracker(t *testing.T) {
	tracker := NewScreenshotTracker()
	if tracker == nil {
		t.Fatal("NewScreenshotTracker returned nil")
	}
	if tracker.screenshots == nil {
		t.Fatal("screenshots map is nil")
	}
}

func TestToolsCount(t *testing.T) {
	defs := tools()
	// 12 original + 8 new = 20
	if len(defs) < 20 {
		t.Errorf("expected at least 20 tools, got %d", len(defs))
	}
}

func TestToolsHaveRequiredFields(t *testing.T) {
	for _, tool := range tools() {
		if tool.Name == "" {
			t.Error("tool has empty name")
		}
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
	}
}

func TestNewToolNames(t *testing.T) {
	defs := tools()
	expected := []string{
		"vulpine_wait",
		"vulpine_find",
		"vulpine_verify",
		"vulpine_screenshot_diff",
		"vulpine_page_settled",
		"vulpine_select_option",
		"vulpine_fill_form",
		"vulpine_page_info",
	}
	nameSet := make(map[string]bool)
	for _, d := range defs {
		nameSet[d.Name] = true
	}
	for _, name := range expected {
		if !nameSet[name] {
			t.Errorf("missing expected tool: %s", name)
		}
	}
}

func TestEvalJSHelper_NotPanicsOnNilClient(t *testing.T) {
	// evalJS should return error, not panic, when client is nil
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("evalJS panicked: %v", r)
		}
	}()
	_, err := evalJS(nil, "session", "1+1")
	if err == nil {
		t.Error("expected error with nil client")
	}
}
