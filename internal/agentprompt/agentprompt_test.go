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

func TestSystemPromptIncludesChatFormattingPolicy(t *testing.T) {
	for _, want := range []string{
		"Output Formatting",
		"Do not use Markdown headings",
		"Do not write horizontal rule divider lines",
		"tables and task checkboxes",
	} {
		if !strings.Contains(SystemPrompt, want) {
			t.Fatalf("SystemPrompt missing formatting rule %q:\n%s", want, SystemPrompt)
		}
	}
}

func TestSystemPromptIncludesObservationRecoveryAndSecretPolicy(t *testing.T) {
	for _, want := range []string{
		"vulpine_observe with visual:true",
		"observed/unverified/lost confidence",
		"never infer success from a failed tool",
		"Automatic recovery observation",
		"Do not fabricate legally meaningful identity fields such as date of birth",
		"Treat generated passwords, recovery codes, and verification codes as secrets",
	} {
		if !strings.Contains(SystemPrompt, want) {
			t.Fatalf("SystemPrompt missing recovery/secret rule %q:\n%s", want, SystemPrompt)
		}
	}
}

func TestSystemPromptIncludesFormIntelligenceAndChallengePolicy(t *testing.T) {
	for _, want := range []string{
		"vulpine_page_info returns formFields and activeElement",
		"vulpine_fill_form can target fields by label, name, id, placeholder, or autocomplete",
		"Re-run vulpine_page_info after overlays, route changes, or failed fills",
		"vulpine_captcha_detect",
		"vulpine_captcha_solve",
		"vulpine_captcha_apply",
		"only after operator approval or an explicit configured policy allows it",
		"NEEDS_USER_ACTION",
	} {
		if !strings.Contains(SystemPrompt, want) {
			t.Fatalf("SystemPrompt missing form/challenge rule %q:\n%s", want, SystemPrompt)
		}
	}
}
