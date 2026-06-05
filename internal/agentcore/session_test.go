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
