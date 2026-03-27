package security

import (
	"strings"
	"testing"
)

func TestGenerateObserverScript(t *testing.T) {
	script := GenerateObserverScript()

	if script == "" {
		t.Fatal("expected non-empty observer script")
	}

	// Should set up the installed flag
	if !strings.Contains(script, "__vulpineObserverInstalled") {
		t.Error("expected __vulpineObserverInstalled guard")
	}

	// Should initialize alerts array
	if !strings.Contains(script, "__vulpineAlerts") {
		t.Error("expected __vulpineAlerts array")
	}

	// Should create MutationObserver
	if !strings.Contains(script, "MutationObserver") {
		t.Error("expected MutationObserver")
	}

	// Should check for SCRIPT tags
	if !strings.Contains(script, "SCRIPT") {
		t.Error("expected check for SCRIPT tag")
	}

	// Should check for hidden elements
	if !strings.Contains(script, "display") {
		t.Error("expected display check")
	}
	if !strings.Contains(script, "visibility") {
		t.Error("expected visibility check")
	}
	if !strings.Contains(script, "opacity") {
		t.Error("expected opacity check")
	}

	// Should check off-screen
	if !strings.Contains(script, "getBoundingClientRect") {
		t.Error("expected bounding rect check")
	}

	// Should detect alert types
	if !strings.Contains(script, "hidden_text") {
		t.Error("expected hidden_text alert type")
	}
	if !strings.Contains(script, "new_script") {
		t.Error("expected new_script alert type")
	}
	if !strings.Contains(script, "dynamic_element") {
		t.Error("expected dynamic_element alert type")
	}

	// Should observe with childList and subtree
	if !strings.Contains(script, "childList") {
		t.Error("expected childList option")
	}
	if !strings.Contains(script, "subtree") {
		t.Error("expected subtree option")
	}
}

func TestNewMutationMonitor(t *testing.T) {
	m := NewMutationMonitor()
	if m == nil {
		t.Fatal("expected non-nil monitor")
	}
	if m.Alerts() == nil {
		t.Fatal("expected non-nil alerts channel")
	}
}

func TestMutationMonitor_AlertsChannel(t *testing.T) {
	m := NewMutationMonitor()

	// Push an alert directly for testing
	alert := MutationAlert{
		AgentID:  "agent-1",
		Type:     "new_script",
		Selector: "script",
		Content:  "alert('xss')",
	}

	// Non-blocking send
	select {
	case m.alerts <- alert:
	default:
		t.Fatal("failed to send alert to channel")
	}

	// Read back
	select {
	case got := <-m.Alerts():
		if got.AgentID != "agent-1" {
			t.Errorf("expected agent-1, got %s", got.AgentID)
		}
		if got.Type != "new_script" {
			t.Errorf("expected new_script, got %s", got.Type)
		}
	default:
		t.Fatal("expected alert in channel")
	}
}

func TestGenerateObserverScript_IsSelfContained(t *testing.T) {
	script := GenerateObserverScript()
	// Should be an IIFE
	if !strings.HasPrefix(script, "(function()") {
		t.Error("expected script to be an IIFE")
	}
	// Should end with invocation
	trimmed := strings.TrimSpace(script)
	if !strings.HasSuffix(trimmed, "})();") {
		t.Error("expected script to end with IIFE invocation")
	}
}

func TestGenerateObserverScript_Truncation(t *testing.T) {
	script := GenerateObserverScript()
	// Should have truncation function
	if !strings.Contains(script, "truncate") {
		t.Error("expected truncate function for content limiting")
	}
}
