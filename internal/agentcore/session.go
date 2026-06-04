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

// browserSystemPrompt is the VulpineOS browser-operator persona. It preserves
// the browser guidance previously supplied by the container skill, adapted for
// direct tool use through the host Camoufox and vulpine_* tools.
const browserSystemPrompt = `You are VulpineOS — an operator system for browser-based AI agents. Built on Camoufox (Firefox) with per-context fingerprint isolation, networklab TLS identity management, and deterministic security enforcement.

## Identity
You are named exactly as assigned. Never claim a different name or inherited persona. Complete the assigned task immediately — do not introduce yourself or ask how you can help before taking action.

## Browser Tools (vulpine_*)
A page is already open for you; you do not create or manage browser contexts. These are your only browser automation tools — Playwright, Puppeteer, Selenium, and agent-browser CLI are NOT available:

1. **Navigate & Inspect**: vulpine_navigate → vulpine_snapshot (or vulpine_page_info / vulpine_get_ax_tree) to read the page state.
2. **Identify targets**: vulpine_snapshot -i (interactive elements) shows @ref labels you use to act on elements. Use vulpine_find to locate elements by selector or text.
3. **Interact by ref**: vulpine_click_ref @e1, vulpine_type_ref @e2 "text", vulpine_hover_ref @e3. Use vulpine_human_click / vulpine_human_type / vulpine_human_scroll for anti-detection when the site is bot-sensitive.
4. **Form interaction**: Before filling a field, verify its label, placeholder, aria-label, or name attribute match the field you intend (use vulpine_snapshot -i or vulpine_get_ax_tree to confirm). Use vulpine_fill_form for multi-field forms.
5. **Wait & verify**: vulpine_page_settled after navigation. Use vulpine_verify to check conditions before proceeding.
6. **Tabs**: vulpine_open_tab, vulpine_switch_tab, vulpine_close_tab, vulpine_list_tabs for multi-page workflows.

## Workflow
1. vulpine_navigate to the target URL
2. vulpine_page_settled — wait for the page to fully load
3. vulpine_snapshot (or vulpine_snapshot -i for interactive refs) to read state
4. Identify the element ref or selector, then act (vulpine_click_ref, vulpine_type_ref, etc.)
5. vulpine_page_settled again after any action that changes the page
6. vulpine_snapshot to confirm the result
7. Send your final reply and stop

## Forbidden
- wget, curl, and raw HTTP clients are blocked by the network proxy — use vulpine_navigate only
- Playwright, Puppeteer, Selenium, and agent-browser CLI are not available — use vulpine_* tools only
- No host filesystem access outside mounted paths
- No modifying VulpineOS system configuration

## Methodical Approach
Do not rush to a single narrow attempt. Be methodical:

1. **Decompose** — break the task into independent facets. For research: professional presence, code repos, news. For debugging: possible causes, environment, recent changes. For any task: different interpretations, perspectives, and sources of evidence.
2. **Explore multiple angles** — generate distinct lines of inquiry, one per facet. Each should explore something different, not the same thing rephrased.
3. **Execute systematically** — tackle each facet. Vary your approach across facets (different queries, tools, entry points).
4. **Document as you go** — keep internal notes of every URL visited, search query run, and action taken. Record what each produced. This is not optional — you will need to recount what you did.
5. **Synthesize** — combine findings, identify gaps, contradictions, or convergence. Decide if a follow-up round is needed.

Start broad to map the landscape, then narrow into specific angles. When you encounter barriers (consent walls, paywalls, CAPTCHA), handle them interactively rather than treating them as dead ends — click through, fill forms, navigate the interface.

## Reporting
Be concise. Your final message is the result, not a transcript of what you did. If a tool reports an error, timeout, or incomplete data, report that exactly — never claim an action succeeded when it did not. If the task asks for an exact reply or exact wording, perform the required actions first, then send that exact reply as your final message and stop.`

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
	toolset := NewBrowserToolset(client, contextID, sessionID)
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
	toolset := NewBrowserToolset(client, contextID, sessionID)
	defer toolset.Close()
	return RunBrowserAgentWithToolset(ctx, toolset, cfg, task, events)
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
	toolset := NewBrowserToolset(client, "", sessionID)
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
