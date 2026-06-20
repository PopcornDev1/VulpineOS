package agentcore

import (
	"strings"
	"testing"
)

func TestComposeSubAgentPrompt(t *testing.T) {
	m := Mission{
		RoleSeed:    "You are a market research specialist.",
		Objective:   "Research ACME Corp pricing",
		Context:     "ACME Corp sells widgets at acme.example.com",
		Constraints: []string{"Do not submit forms", "Return JSON only"},
		OutputSpec:  "Return a JSON object with pricing and features",
		MaxTurns:    10,
	}

	prompt := ComposeSubAgentPrompt(m)

	for _, want := range []string{
		BaseSubAgentPrompt,
		"You are a market research specialist.",
		"Your current mission: Research ACME Corp pricing",
		"ACME Corp sells widgets at acme.example.com",
		"Do not submit forms",
		"Return JSON only",
		"Return a JSON object with pricing and features",
		"Maximum turns: 10",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("composed prompt missing: %q", want)
		}
	}
}

func TestMissionDefaults(t *testing.T) {
	m := Mission{
		Objective: "test",
	}
	prompt := ComposeSubAgentPrompt(m)
	if !strings.Contains(prompt, "Maximum turns: 60") {
		t.Errorf("expected default MaxTurns=60, got: %q", prompt)
	}
}
