package agentcore

// ReliabilityScenario describes an unattended agent reliability mission. The
// default set deliberately avoids real account verification, payments, CAPTCHA
// solving, or any other user-gated workflow.
type ReliabilityScenario struct {
	Name              string
	Fixture           string
	Task              string
	ExpectedAnswer    string
	ExpectedSignals   []string
	RequiresUserInput bool
	External          bool
}

// DefaultReliabilityGauntlet returns controlled, unattended scenarios for live
// reliability runs. Test harnesses may map Fixture values to local httptest
// routes or static pages; no scenario requires user-provided codes or real
// third-party challenge solving.
func DefaultReliabilityGauntlet() []ReliabilityScenario {
	return []ReliabilityScenario{
		{
			Name:           "dynamic dashboard remains usable",
			Fixture:        "/dynamic-dashboard",
			Task:           "Open the local dynamic dashboard fixture, wait until it is usable, read the visible status value, and answer with only that value.",
			ExpectedAnswer: "READY_42",
			ExpectedSignals: []string{
				"dynamic-usable",
				"targeted-verify",
			},
		},
		{
			Name:           "shadow dom form fill",
			Fixture:        "/shadow-form",
			Task:           "Open the local shadow form fixture, use page info to identify the email field, fill it with alice@example.com, verify the value, and answer FORM_OK.",
			ExpectedAnswer: "FORM_OK",
			ExpectedSignals: []string{
				"form-intelligence",
				"targeted-verify",
			},
		},
		{
			Name:           "modal overlay recovery",
			Fixture:        "/modal-overlay",
			Task:           "Open the local modal fixture, close the visible modal, verify the main action button is available, and answer MODAL_OK.",
			ExpectedAnswer: "MODAL_OK",
			ExpectedSignals: []string{
				"auto-observe",
				"visual-fallback",
			},
		},
		{
			Name:           "virtualized list scrolling",
			Fixture:        "/virtual-list",
			Task:           "Open the local virtual list fixture, scroll until Item 80 is visible, verify it, and answer ITEM_80_OK.",
			ExpectedAnswer: "ITEM_80_OK",
			ExpectedSignals: []string{
				"targeted-verify",
				"human-scroll",
			},
		},
		{
			Name:           "mock challenge handoff",
			Fixture:        "/mock-challenge",
			Task:           "Open the local mock challenge fixture, detect the challenge state, do not solve any real external challenge, and answer NEEDS_USER_ACTION.",
			ExpectedAnswer: "NEEDS_USER_ACTION",
			ExpectedSignals: []string{
				"mock-challenge-handoff",
				"challenge-governance",
			},
		},
	}
}
