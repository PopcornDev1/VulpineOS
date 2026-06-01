package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"vulpineos/internal/juggler"
	"vulpineos/internal/mcp"
)

// Config selects the model provider for a native agent run.
type Config struct {
	Provider       string   // VulpineOS provider id (e.g. "openrouter")
	Model          string   // primary model (e.g. "openrouter/z-ai/glm-4.5-air:free")
	APIKey         string   // provider API key
	FallbackModels []string // tried in order after the primary on rate limits
	BaseURL        string   // optional override; derived from Provider when empty
	MaxIterations  int      // optional loop bound
}

// modelChain returns the ordered model list (primary first), with the
// "openrouter/" prefix stripped since the OpenRouter API expects the bare slug.
func (c Config) modelChain() []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range append([]string{c.Model}, c.FallbackModels...) {
		m = strings.TrimPrefix(strings.TrimSpace(m), "openrouter/")
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// browserSystemPrompt is the VulpineOS browser-operator persona. It mirrors the
// identity the NanoClaw container skill provided, adapted for direct tool use:
// the agent drives the host Camoufox via the vulpine_* tools (no agent-browser
// CLI, no container).
const browserSystemPrompt = `You are a VulpineOS browser agent. You operate a real Camoufox (Firefox) browser directly through the provided tools to carry out the user's task.

Rules:
- Use the browser tools to navigate, read the page (vulpine_snapshot / vulpine_get_ax_tree), and interact (click, type, select, press keys). A page is already open for you; you do not create or manage browser contexts.
- After each action, check the result. If a tool reports an error, timeout, or incomplete data, report that exactly — never claim an action succeeded when it did not.
- Take the exact action the task requires, then stop. Do not keep inspecting once the page state proves the task is done.
- When the task asks for an exact reply or exact wording, perform the required actions first, then send that exact reply as your final message and stop.
- Be concise. Your final message is the result, not a transcript of what you did.`

// RunBrowserAgent runs a native agent for one task against the host Camoufox.
// It opens a fresh browser context+page, drives it with the model loop via the
// vulpine_* tools, and returns the agent's final reply. The temporary context
// is removed on return. events may be nil.
//
// This is the standalone entrypoint (used by the live E2E and as the basis for
// the orchestrator-integrated path, which supplies its own pooled context).
func RunBrowserAgent(ctx context.Context, client *juggler.Client, cfg Config, task string, events Events) (string, error) {
	if client == nil {
		return "", fmt.Errorf("juggler client is required")
	}
	models := cfg.modelChain()
	if len(models) == 0 {
		return "", fmt.Errorf("no model configured")
	}

	contextID, sessionID, err := openPage(ctx, client)
	if err != nil {
		return "", fmt.Errorf("open page: %w", err)
	}
	defer cleanupContext(client, contextID)

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = BaseURLForProvider(cfg.Provider)
	}
	model := NewModelClient(baseURL, cfg.APIKey)
	toolset := NewBrowserToolset(client, sessionID)
	loop := NewLoop(model, toolset, events, LoopConfig{
		Models:        models,
		SystemPrompt:  browserSystemPrompt,
		Tools:         BrowserTools(),
		MaxIterations: cfg.MaxIterations,
	})
	return loop.Run(ctx, task, nil)
}

// openPage creates a fresh browser context with one page and returns the
// context id + the page's juggler session id, via the canonical MCP handler.
func openPage(ctx context.Context, client *juggler.Client) (contextID, sessionID string, err error) {
	res, err := mcp.HandleToolCallDirectCtx(ctx, client, "vulpine_new_context", json.RawMessage(`{}`))
	if err != nil {
		return "", "", err
	}
	if res == nil || res.IsError {
		return "", "", fmt.Errorf("new_context failed: %s", contentText(res))
	}
	var out struct {
		ContextID string `json:"contextId"`
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal([]byte(contentText(res)), &out); err != nil {
		return "", "", fmt.Errorf("parse new_context result: %w", err)
	}
	if out.SessionID == "" {
		return "", "", fmt.Errorf("new_context returned empty sessionId")
	}
	return out.ContextID, out.SessionID, nil
}

func cleanupContext(client *juggler.Client, contextID string) {
	if client == nil || contextID == "" {
		return
	}
	_, _ = client.Call("", "Browser.removeBrowserContext", map[string]interface{}{
		"browserContextId": contextID,
	})
}
