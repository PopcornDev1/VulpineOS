package agentprompt

import (
	"strings"
	"testing"
	"time"

	"vulpineos/internal/vault"
)

func TestFormatTurnPromptIncludesVisibleHistoryAndCurrentMessage(t *testing.T) {
	history := []vault.AgentMessage{
		{Role: "user", Content: "please diagnose the browser", Timestamp: time.Unix(1, 0)},
		{Role: "system", Content: "Error: disk I/O error", Timestamp: time.Unix(2, 0)},
		{Role: "assistant", Content: "Let me start probing the browser environment.", Timestamp: time.Unix(3, 0)},
		{Role: "user", Content: "why did we get the error?", Timestamp: time.Unix(4, 0)},
	}

	got := FormatTurnPrompt(history, "why did we get the error?")

	for _, want := range []string{
		"Recent visible chat history",
		"[system] Error: disk I/O error",
		"[assistant] Let me start probing the browser environment.",
		"Current user message:",
		"why did we get the error?",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "why did we get the error?") != 1 {
		t.Fatalf("current message should appear once, got prompt:\n%s", got)
	}
}

func TestFormatTurnPromptCapsLongHistoryEntries(t *testing.T) {
	got := FormatTurnPrompt([]vault.AgentMessage{
		{Role: "assistant", Content: strings.Repeat("a", 5000)},
	}, "next")

	if strings.Contains(got, strings.Repeat("a", 4500)) {
		t.Fatalf("prompt did not cap long history entry")
	}
	if !strings.Contains(got, "[truncated]") {
		t.Fatalf("prompt should mark truncation:\n%s", got)
	}
	if !strings.Contains(got, "Current user message:\nnext") {
		t.Fatalf("prompt missing current message:\n%s", got)
	}
}
