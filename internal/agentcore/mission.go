package agentcore

import "fmt"

const defaultMissionMaxTurns = 60

// Mission is the declarative task document the lead agent writes when
// delegating work to a sub-agent. It is structured (not natural-language)
// so the sub-agent spends zero tokens parsing intent.
type Mission struct {
	AgentID     string   `json:"agent_id,omitempty"`    // target sub-agent ID, or empty for auto-select
	RoleSeed    string   `json:"role_seed,omitempty"`   // role identity for this sub-agent
	Objective   string   `json:"objective"`             // what to accomplish (concise)
	Context     string   `json:"context,omitempty"`     // relevant background information (compact)
	Constraints []string `json:"constraints,omitempty"` // rules and boundaries
	OutputSpec  string   `json:"output_spec,omitempty"` // expected output format
	MaxTurns    int      `json:"max_turns,omitempty"`   // maximum iterations for this mission
	Priority    int      `json:"priority,omitempty"`    // scheduling priority (reserved)
}

// ComposeSubAgentPrompt assembles the full system prompt for a sub-agent
// from the mission fields. Returns a single string suitable for use as
// the system message in a LoopConfig.
// Empty fields are replaced with safe defaults to avoid degenerate prompts.
func ComposeSubAgentPrompt(m Mission) string {
	maxTurns := m.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMissionMaxTurns
	}

	roleSeed := m.RoleSeed
	if roleSeed == "" {
		roleSeed = "You are a sub-agent."
	}
	objective := m.Objective
	if objective == "" {
		objective = "Complete the assigned task."
	}

	prompt := BaseSubAgentPrompt + "\n\n" + roleSeed
	prompt += "\n\nYour current mission: " + objective
	if m.Context != "" {
		prompt += "\n\n" + m.Context
	}
	if len(m.Constraints) > 0 {
		prompt += "\n\nConstraints:"
		for _, c := range m.Constraints {
			prompt += "\n- " + c
		}
	}
	if m.OutputSpec != "" {
		prompt += "\n\nWhen you have completed the mission, return your findings in this format: " + m.OutputSpec
	}
	prompt += fmt.Sprintf("\n\nMaximum turns: %d", maxTurns)

	return prompt
}
