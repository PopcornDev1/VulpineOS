package agentcore

import (
	"strings"
	"testing"
)

func TestBrowserSystemPromptIncludesChatFormattingPolicy(t *testing.T) {
	for _, want := range []string{
		"Output Formatting",
		"Do not use Markdown headings",
		"Do not write horizontal rule divider lines",
		"tables and task checkboxes",
	} {
		if !strings.Contains(browserSystemPrompt, want) {
			t.Fatalf("browserSystemPrompt missing formatting rule %q:\n%s", want, browserSystemPrompt)
		}
	}
}

func TestBrowserSystemPromptDoesNotReferenceUnsupportedSnapshotFlag(t *testing.T) {
	if strings.Contains(browserSystemPrompt, "vulpine_snapshot -i") {
		t.Fatalf("browserSystemPrompt references unsupported vulpine_snapshot -i flag:\n%s", browserSystemPrompt)
	}
}

func TestAgentPromptsContainExpectedDirectives(t *testing.T) {
	for _, want := range []string{
		"lead agent",
		"plan strategically",
		"delegate specialized work",
		"clarification reflex",
		"Plan-then-execute",
		"Sub-Agent System",
		"autonomous LLM instances",
		"vulpine_delegate_agent",
		"vulpine_steer_agent",
		"vulpine_agent_status",
		"vulpine_get_agent_result",
		"vulpine_get_agent_snapshot",
		"vulpine_release_agent",
	} {
		if !strings.Contains(LeadAgentPrompt, want) {
			t.Errorf("LeadAgentPrompt missing: %q", want)
		}
	}

	for _, want := range []string{
		"VulpineOS",
		"Vulpine",
		"vulpine_navigate",
		"vulpine_snapshot",
	} {
		if !strings.Contains(BaseSubAgentPrompt, want) {
			t.Errorf("BaseSubAgentPrompt missing: %q", want)
		}
	}
	for _, unwanted := range []string{"lead agent", "delegate"} {
		if strings.Contains(BaseSubAgentPrompt, unwanted) {
			t.Errorf("BaseSubAgentPrompt should not contain %q", unwanted)
		}
	}
}

func TestBrowserSystemPromptIncludesFileWorkspaceTools(t *testing.T) {
	for _, want := range []string{
		"File Workspace Tools",
		"vulpine_write_file",
		"If the operator asks whether you can write files, answer yes",
		"absolute paths and .. traversal are rejected",
	} {
		if !strings.Contains(browserSystemPrompt, want) {
			t.Fatalf("browserSystemPrompt missing file workspace rule %q:\n%s", want, browserSystemPrompt)
		}
	}
}

func TestBrowserSystemPromptBoundsBroadWebsiteChecks(t *testing.T) {
	for _, want := range []string{
		"Bounded Website Checks",
		"do not expand to extra detector or benchmark sites",
		"Do not revisit a URL after you already captured usable page state",
		"If one targeted wait times out, inspect the current snapshot once and continue",
	} {
		if !strings.Contains(browserSystemPrompt, want) {
			t.Fatalf("browserSystemPrompt missing bounded website rule %q:\n%s", want, browserSystemPrompt)
		}
	}
}
