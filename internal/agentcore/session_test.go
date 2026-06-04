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
