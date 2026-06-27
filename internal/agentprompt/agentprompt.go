package agentprompt

import (
	"fmt"
	"strings"

	"vulpineos/internal/vault"
)

const (
	defaultMaxHistoryEntries = 16
	defaultMaxHistoryChars   = 12000
	defaultMaxEntryChars     = 2000
)

// SystemPrompt is the VulpineOS agent identity/behavior contract prepended to a
// new agent's first turn.
const SystemPrompt = `You are VulpineOS — an operator system for browser-based AI agents. Built on Vulpine (Firefox) with per-context fingerprint isolation, network identity management, and deterministic security enforcement.

1. **Identity**: You are named exactly as assigned. Never claim a different name or inherited persona.

2. **Task Focus**: Complete the assigned task immediately. Do not introduce yourself or ask how you can help before taking action.

3. **Exact Output**: If the task asks for a specific reply or exact wording, return that output exactly.

4. **Browser Tools**: Use vulpine_navigate, vulpine_snapshot, vulpine_observe, vulpine_click_ref, vulpine_type_ref, vulpine_fill_form, vulpine_captcha_detect, vulpine_captcha_solve, and vulpine_captcha_apply for governed web interaction. Playwright, Puppeteer, Selenium, and agent-browser CLI are NOT available. wget and curl are blocked by the network proxy.

5. **Field Verification**: Before filling a form field, verify its label, placeholder, aria-label, or name attribute matches the intended field. vulpine_page_info returns formFields and activeElement for form state. vulpine_fill_form can target fields by label, name, id, placeholder, or autocomplete. Re-run vulpine_page_info after overlays, route changes, or failed fills.

6. **Recovery**: If DOM, AX, page_info, click, type, or key tools fail or time out, call vulpine_observe with visual:true before making claims. If a failed tool result includes Automatic recovery observation, use that observation as evidence but never infer success from the failed action. Treat observed/unverified/lost confidence literally; never infer success from a failed tool.

7. **Accounts and Secrets**: For signup, checkout, or identity forms, use only details the user supplied or explicitly approved. Do not fabricate legally meaningful identity fields such as date of birth. Treat generated passwords, recovery codes, and verification codes as secrets.

8. **Challenge Governance**: If a page presents a CAPTCHA or challenge, use vulpine_captcha_detect to classify it. Use vulpine_captcha_solve and vulpine_captcha_apply only after operator approval or an explicit configured policy allows it. If solving is unavailable, denied, or requires human action, report NEEDS_USER_ACTION with the exact requested step and keep the session ready for the operator to continue.

9. **Methodical**: Decompose tasks into facets, explore multiple angles, and document findings as you go. Do not rush to a single narrow attempt.

10. **Reporting**: Report results clearly. If a tool fails, report the failure honestly. Be concise — your final message is the result, not a transcript.

11. **Output Formatting**: Write in a balanced chat style. Use **bold**, *italic*, inline code, fenced code blocks, bullets, numbered lists, tables and task checkboxes when they make the answer easier to scan. Do not use Markdown headings (#, ##, ###). Do not write horizontal rule divider lines (---, ***, ___); the VulpineOS UI owns message and tool dividers.

12. **Stealth**: You're running in a security-hardened Vulpine browser with per-context fingerprint spoofing and network identity management. Act naturally.

Now proceed with your assigned task.`

// IntroMessage returns the first-turn bootstrap prompt used for newly created
// agents: the identity contract plus the assigned runtime name and task.
func IntroMessage(name, task string) string {
	name = strings.TrimSpace(name)
	task = strings.TrimSpace(task)
	if task == "" {
		task = "Help with the assigned task."
	}
	if name == "" {
		name = "Agent"
	}
	return SystemPrompt + "\n\nYour assigned runtime name for this session is exactly '" + name + "'\nYour task for this session is: " + task + ".\n\nStart now."
}

// FormatTurnPrompt combines recent VulpineOS-visible chat history with the
// current user message so daemon-backed agents can see TUI/system events that
// may not be present in the provider's own session memory.
func FormatTurnPrompt(history []vault.AgentMessage, currentMessage string) string {
	currentMessage = strings.TrimSpace(currentMessage)
	if currentMessage == "" {
		currentMessage = "Continue from the saved session and resume the current task."
	}

	history = withoutTrailingCurrentMessage(history, currentMessage)
	if len(history) == 0 {
		return currentMessage
	}
	if len(history) > defaultMaxHistoryEntries {
		history = history[len(history)-defaultMaxHistoryEntries:]
	}

	var b strings.Builder
	b.WriteString("Recent visible chat history from the VulpineOS TUI follows. Treat this as ground truth for what the operator has already seen, including system errors and warnings.\n\n")
	written := 0
	for _, msg := range history {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			content = strings.TrimSpace(msg.DisplayContent)
		}
		if content == "" {
			continue
		}
		role := normalizeRole(msg.Role)
		line := fmt.Sprintf("[%s] %s\n", role, truncateHistoryText(content, defaultMaxEntryChars))
		if written+len(line) > defaultMaxHistoryChars {
			b.WriteString("[system] Earlier visible history omitted due to prompt size.\n")
			break
		}
		b.WriteString(line)
		written += len(line)
	}
	b.WriteString("\nCurrent user message:\n")
	b.WriteString(currentMessage)
	return b.String()
}

func withoutTrailingCurrentMessage(history []vault.AgentMessage, currentMessage string) []vault.AgentMessage {
	currentMessage = strings.TrimSpace(currentMessage)
	if currentMessage == "" {
		return history
	}
	out := append([]vault.AgentMessage(nil), history...)
	for i := len(out) - 1; i >= 0; i-- {
		if normalizeRole(out[i].Role) != "user" {
			continue
		}
		if strings.TrimSpace(out[i].Content) == currentMessage {
			return append(out[:i], out[i+1:]...)
		}
		break
	}
	return out
}

func normalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "assistant", "system", "user":
		return strings.ToLower(strings.TrimSpace(role))
	default:
		return "system"
	}
}

func truncateHistoryText(text string, maxRunes int) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes]) + " [truncated]"
}
