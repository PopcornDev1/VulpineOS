package agentcore

import (
	"strings"
	"testing"
)

func TestDefaultReliabilityGauntletAvoidsManualUserGates(t *testing.T) {
	scenarios := DefaultReliabilityGauntlet()
	if len(scenarios) < 5 {
		t.Fatalf("scenario count = %d, want at least 5", len(scenarios))
	}
	for _, scenario := range scenarios {
		if strings.TrimSpace(scenario.Name) == "" || strings.TrimSpace(scenario.Task) == "" || strings.TrimSpace(scenario.ExpectedAnswer) == "" {
			t.Fatalf("scenario has empty name/task: %#v", scenario)
		}
		if scenario.RequiresUserInput {
			t.Fatalf("%s requires user input; default gauntlet must stay unattended", scenario.Name)
		}
		if scenario.External {
			t.Fatalf("%s is external; default gauntlet must use local/controlled fixtures", scenario.Name)
		}
		text := strings.ToLower(scenario.Name + " " + scenario.Task + " " + scenario.Fixture)
		for _, blocked := range []string{"verification code", "captcha", "payment", "paywall", "create reddit", "sign up for"} {
			if strings.Contains(text, blocked) {
				t.Fatalf("%s contains manual-gate marker %q: %s", scenario.Name, blocked, text)
			}
		}
	}
}

func TestReliabilityGauntletExpectedSignals(t *testing.T) {
	var hasShadowForm, hasDynamicPage, hasRecovery, hasChallengeMock bool
	for _, scenario := range DefaultReliabilityGauntlet() {
		for _, signal := range scenario.ExpectedSignals {
			switch signal {
			case "form-intelligence":
				hasShadowForm = true
			case "dynamic-usable":
				hasDynamicPage = true
			case "auto-observe":
				hasRecovery = true
			case "mock-challenge-handoff":
				hasChallengeMock = true
			}
		}
	}
	if !hasShadowForm || !hasDynamicPage || !hasRecovery || !hasChallengeMock {
		t.Fatalf("expected signal coverage shadow=%v dynamic=%v recovery=%v challenge=%v", hasShadowForm, hasDynamicPage, hasRecovery, hasChallengeMock)
	}
}
