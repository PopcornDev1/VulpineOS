package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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

// modelChain returns the ordered model list (primary first), with the provider
// prefix stripped so each provider API receives the bare model slug it expects
// (e.g. "openai/gpt-5.4" -> "gpt-5.4"; "openrouter/anthropic/claude-..." ->
// "anthropic/claude-...").
func (c Config) modelChain() []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range append([]string{c.Model}, c.FallbackModels...) {
		m = stripProviderPrefix(m, c.Provider)
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

	model := newCompleter(cfg)
	toolset := NewBrowserToolset(client, sessionID)
	defer toolset.Close()
	loop := NewLoop(model, toolset, events, LoopConfig{
		Models:        models,
		SystemPrompt:  browserSystemPrompt,
		Tools:         BrowserTools(),
		MaxIterations: cfg.MaxIterations,
	})
	return loop.Run(ctx, task, nil)
}

// RunBrowserAgentInContext runs a native agent for one task against a page
// created inside an existing (caller-owned) browser context — e.g. a pooled,
// identity-applied context from the orchestrator. It does NOT create or remove
// the context; the caller owns its lifecycle. Returns the agent's final reply.
func RunBrowserAgentInContext(ctx context.Context, client *juggler.Client, contextID string, cfg Config, task string, events Events) (string, error) {
	if client == nil {
		return "", fmt.Errorf("juggler client is required")
	}
	sessionID, err := openPageInContext(ctx, client, contextID)
	if err != nil {
		return "", fmt.Errorf("open page in context: %w", err)
	}
	return RunBrowserAgentOnSession(ctx, client, sessionID, cfg, task, events)
}

// RunBrowserAgentOnSession runs a one-off native agent turn against an already-
// open page session (tab), creating and closing a toolset for the turn. It does
// not create or close the page.
func RunBrowserAgentOnSession(ctx context.Context, client *juggler.Client, sessionID string, cfg Config, task string, events Events) (string, error) {
	if client == nil {
		return "", fmt.Errorf("juggler client is required")
	}
	if strings.TrimSpace(sessionID) == "" {
		return "", fmt.Errorf("page session is required")
	}
	toolset := NewBrowserToolset(client, sessionID)
	defer toolset.Close()
	return RunBrowserAgentWithToolset(ctx, toolset, cfg, task, events)
}

// RunBrowserAgentWithToolset runs a native agent turn using a caller-owned
// toolset (and thus a caller-owned page + MCP execution-context tracker). The
// caller keeps the toolset alive across turns so the same tab is reused and its
// execution contexts stay resolvable — the agent simply navigates that tab to
// wherever each task needs. The toolset is NOT closed here.
func RunBrowserAgentWithToolset(ctx context.Context, toolset *BrowserToolset, cfg Config, task string, events Events) (string, error) {
	if toolset == nil {
		return "", fmt.Errorf("browser toolset is required")
	}
	models := cfg.modelChain()
	if len(models) == 0 {
		return "", fmt.Errorf("no model configured")
	}
	loop := NewLoop(newCompleter(cfg), toolset, events, LoopConfig{
		Models:        models,
		SystemPrompt:  browserSystemPrompt,
		Tools:         BrowserTools(),
		MaxIterations: cfg.MaxIterations,
	})
	return loop.Run(ctx, task, nil)
}

// openPageSession opens a fresh page (tab) inside the given context and returns
// its juggler session id. Exposed for the manager's per-agent page reuse.
func openPageSession(ctx context.Context, client *juggler.Client, contextID string) (string, error) {
	return openPageInContext(ctx, client, contextID)
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

// openPageInContext creates a page inside an already-existing browser context
// (e.g. one acquired + identity-applied by the orchestrator's pool) and returns
// the new page's juggler session id. Mirrors the MCP new-context handler's
// attachedToTarget capture, but reuses the caller's context instead of making a
// fresh one.
func openPageInContext(ctx context.Context, client *juggler.Client, contextID string) (string, error) {
	if strings.TrimSpace(contextID) == "" {
		return "", fmt.Errorf("contextID is required")
	}
	sessionCh := make(chan string, 4)
	cancel := client.SubscribeWithCancel("Browser.attachedToTarget", func(_ string, params json.RawMessage) {
		var ev struct {
			SessionID  string `json:"sessionId"`
			TargetInfo struct {
				BrowserContextID string `json:"browserContextId"`
			} `json:"targetInfo"`
		}
		_ = json.Unmarshal(params, &ev)
		if ev.SessionID != "" && ev.TargetInfo.BrowserContextID == contextID {
			select {
			case sessionCh <- ev.SessionID:
			default:
			}
		}
	})
	defer cancel()

	if _, err := client.Call("", "Browser.newPage", map[string]interface{}{"browserContextId": contextID}); err != nil {
		return "", fmt.Errorf("Browser.newPage: %w", err)
	}
	select {
	case sid := <-sessionCh:
		return sid, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(15 * time.Second):
		return "", fmt.Errorf("timed out waiting for page session in context %s", contextID)
	}
}

func cleanupContext(client *juggler.Client, contextID string) {
	if client == nil || contextID == "" {
		return
	}
	_, _ = client.Call("", "Browser.removeBrowserContext", map[string]interface{}{
		"browserContextId": contextID,
	})
}
