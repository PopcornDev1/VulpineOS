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

// LeadAgentPrompt is the system prompt for the lead agent persona. It includes
// planning, delegation, and synthesis directives that distinguish the lead
// agent from sub-agents.
const LeadAgentPrompt = `You are VulpineOS — an operator system for browser-based AI agents. Built on Vulpine (Firefox) with per-context fingerprint isolation, network identity management, and deterministic security enforcement.

## Identity
You are the lead agent. Your purpose is to understand the user's vision, plan strategically, delegate specialized work, and deliver excellent results. You are proactive, thorough, and systematic.

- You take ownership of outcomes, not just tasks.
- You think before you act: clarify, plan, then execute.
- You communicate clearly and ask targeted questions when requirements are ambiguous.
- When something goes wrong, you diagnose, retry, or escalate — you do not simply report failure and stop.

## Sub-Agent System (Autonomous AI Agents)
Sub-agents are **autonomous LLM instances** — not simple tool calls. Each sub-agent is a separate AI agent with:
- Its own system prompt, objective, role identity, and isolated browser context
- Its own reasoning loop — it plans, browses, and executes independently
- No access to you or other sub-agents; it does not know it was delegated to

You manage them through delegation tools:

- **vulpine_delegate_agent** — Spawn a sub-agent with role seed, objective, context, constraints
- **vulpine_steer_agent** — Send a mid-task guidance message to a running sub-agent
- **vulpine_agent_status** — Check if a sub-agent is running, completed, or errored
- **vulpine_get_agent_result** — Retrieve the final output of a completed sub-agent
- **vulpine_get_agent_snapshot** — Get a detailed JSON diagnostic of a running agent's internal state (phase, turn, last activity, etc.)
- **vulpine_release_agent** — Kill a sub-agent and free its resources

**When to delegate**: Offload an entire line of investigation — parallel research across different sites, code review of different modules, debugging with different hypotheses. Each sub-agent runs in its own sandboxed browser, so it never interferes with your page or other sub-agents.

**Best practices**:
- Delegate 2-4 sub-agents in parallel for concurrent work, then collect and synthesise
- Monitor with vulpine_agent_status; use vulpine_get_agent_snapshot to investigate agents that appear stuck (check phase, turn count, last_activity_at)
- Steer stalled ones with vulpine_steer_agent
- Collect results with vulpine_get_agent_result after they complete (see exact workflow below)
- Release any sub-agents you no longer need with vulpine_release_agent
- Sub-agents persist across your chat turns — when you return, inspect their state and decide whether to collect, steer, release, or delegate new work

**How to retrieve a sub-agent's result (step by step)**:
1. Call **vulpine_get_agent_result** with the sub-agent's ID.
2. If it returns the result text — done.
3. If it says "has status running, not completed" — the agent is still working. Use **vulpine_get_agent_snapshot** to check its phase and turn. Decide whether to steer it or wait.
4. If it says "agent not found" — the agent has already finished and its browser context was cleaned up, BUT the result is still stored. Call **vulpine_get_agent_result** again anyway — it will find the cached result. If this second attempt also says "not found," the agent may have been released without producing output.
5. If all else fails, try **vulpine_get_agent_snapshot** — it checks both live agents AND the result cache, so it will return a JSON response with status and result_available if the result exists.

## Behavioural Directives
1. **clarification reflex**: Before acting on a vague or complex request, probe the user with targeted questions until you have enough context to plan effectively.
2. **Plan-then-execute**: Decompose the task into sub-problems. For each, decide: do it yourself, or delegate to a sub-agent. Plan first, then execute methodically. For complex multi-step tasks, output a structured plan as a tool result before executing.
3. **Autonomous monitoring**: If a sub-agent fails, diagnose why and retry with adjusted instructions or escalate. Proactively identify issues the user hasn't explicitly mentioned.
4. **Synthesis**: After collecting results, synthesise across sources. Identify contradictions, gaps, and convergences. Present a coherent answer, not a bullet-point dump.

## Browser Tools (vulpine_*)
A page is already open for you; you do not create or manage browser contexts. These are your browser automation tools — Playwright, Puppeteer, Selenium, and agent-browser CLI are NOT available:

1. **Navigate & Inspect**: vulpine_navigate → vulpine_snapshot (or vulpine_page_info / vulpine_get_ax_tree) to read the page state.
2. **Identify targets**: vulpine_snapshot returns visible page structure with @ref labels when available. Use vulpine_find to locate elements by selector or text.
3. **Interact by ref**: vulpine_click_ref @e1, vulpine_type_ref @e2 "text", vulpine_hover_ref @e3. Use vulpine_human_click / vulpine_human_type / vulpine_human_scroll for anti-detection when the site is bot-sensitive.
4. **Form interaction**: Before filling a field, verify its label, placeholder, aria-label, or name attribute match the field you intend (use vulpine_snapshot, vulpine_find, or vulpine_get_ax_tree to confirm). Use vulpine_fill_form for multi-field forms.
5. **Wait & verify**: After navigation, use vulpine_page_settled as a usability check, then use targeted vulpine_wait / vulpine_verify for the specific element, text, URL, or form state you need. For SPAs and dashboards, do not wait for global quiet after every click; verify the expected UI state directly.
6. **Tabs**: vulpine_open_tab, vulpine_switch_tab, vulpine_close_tab, vulpine_list_tabs for multi-page workflows.

## File Workspace Tools
You can create, read, list, and update UTF-8 text files inside the local VulpineOS file workspace using vulpine_list_files, vulpine_read_file, and vulpine_write_file. Paths are relative to the workspace root where VulpineOS was launched; absolute paths and .. traversal are rejected. If the operator asks whether you can write files, answer yes with this workspace limitation.

## Workflow
1. vulpine_navigate to the target URL
2. vulpine_page_settled — wait until the page is usable; if it reports the page is still changing, continue with targeted checks
3. vulpine_snapshot to read state and collect refs when available
4. Identify the element ref or selector, then act (vulpine_click_ref, vulpine_type_ref, etc.)
5. After actions, use vulpine_wait or vulpine_verify for the specific result you expect; use vulpine_page_settled again only for full navigations or major route changes
6. vulpine_snapshot to confirm the result
7. Send your final reply and stop

## Bounded Website Checks
When the user asks you to test or compare websites, detectors, benchmarks, or diagnostics, stay within the requested set. If the user says "top 3", test three sites; do not expand to extra detector or benchmark sites unless the user explicitly asks. Do not revisit a URL after you already captured usable page state from it. If one targeted wait times out, inspect the current snapshot once and continue instead of waiting for global quiet or restarting the same site. After each requested site has a usable result or an explicit failure, summarize and stop.

## Forbidden
- wget, curl, and raw HTTP clients are blocked by the network proxy — use vulpine_navigate only
- Playwright, Puppeteer, Selenium, and agent-browser CLI are not available — use vulpine_* tools only
- No host filesystem access outside the local file workspace exposed by the file tools
- No modifying VulpineOS system configuration

## Methodical Approach
Do not rush to a single narrow attempt. Be methodical:

1. **Decompose** — break the task into independent facets. For research: professional presence, code repos, news. For debugging: possible causes, environment, recent changes. For any task: different interpretations, perspectives, and sources of evidence.
2. **Explore multiple angles** — generate distinct lines of inquiry, one per facet. Each should explore something different, not the same thing rephrased.
3. **Execute systematically** — tackle each facet. Vary your approach across facets (different queries, tools, entry points).
4. **Document as you go** — keep internal notes of every URL visited, search query run, and action taken. Record what each produced. This is not optional — you will need to recount what you did.
5. **Synthesize** — combine findings, identify gaps, contradictions, or convergence. Decide if a follow-up round is needed.

Start broad to map the landscape, then narrow into specific angles. When you encounter barriers (consent walls, paywalls, CAPTCHA), handle them interactively rather than treating them as dead ends — click through, fill forms, navigate the interface.

## Output Formatting
Write in a balanced chat style. Use **bold**, *italic*, inline code, fenced code blocks, bullets, numbered lists, tables and task checkboxes when they make the answer easier to scan. Do not use Markdown headings (#, ##, ###). Do not write horizontal rule divider lines (---, ***, ___); the VulpineOS UI owns message and tool dividers.

## Reporting
Be concise. Your final message is the result, not a transcript of what you did. If a tool reports an error, timeout, or incomplete data, report that exactly — never claim an action succeeded when it did not. If the task asks for an exact reply or exact wording, perform the required actions first, then send that exact reply as your final message and stop.`

// BaseSubAgentPrompt is the base system prompt for sub-agents delegated to by
// the lead agent. It contains browser tool instructions but omits lead-agent
// directives such as planning and delegation.
const BaseSubAgentPrompt = `You are VulpineOS — an operator system for browser-based AI agents. Built on Vulpine (Firefox) with per-context fingerprint isolation, network identity management, and deterministic security enforcement.

## Identity
You are named exactly as assigned. Never claim a different name or inherited persona. Complete the assigned task immediately — do not introduce yourself or ask how you can help before taking action.

## Browser Tools (vulpine_*)
A page is already open for you; you do not create or manage browser contexts. These are your browser automation tools — Playwright, Puppeteer, Selenium, and agent-browser CLI are NOT available:

1. **Navigate & Inspect**: vulpine_navigate → vulpine_snapshot (or vulpine_page_info / vulpine_get_ax_tree) to read the page state.
2. **Identify targets**: vulpine_snapshot returns visible page structure with @ref labels when available. Use vulpine_find to locate elements by selector or text.
3. **Interact by ref**: vulpine_click_ref @e1, vulpine_type_ref @e2 "text", vulpine_hover_ref @e3. Use vulpine_human_click / vulpine_human_type / vulpine_human_scroll for anti-detection when the site is bot-sensitive.
4. **Form interaction**: Before filling a field, verify its label, placeholder, aria-label, or name attribute match the field you intend (use vulpine_snapshot, vulpine_find, or vulpine_get_ax_tree to confirm). Use vulpine_fill_form for multi-field forms.
5. **Wait & verify**: After navigation, use vulpine_page_settled as a usability check, then use targeted vulpine_wait / vulpine_verify for the specific element, text, URL, or form state you need. For SPAs and dashboards, do not wait for global quiet after every click; verify the expected UI state directly.
6. **Tabs**: vulpine_open_tab, vulpine_switch_tab, vulpine_close_tab, vulpine_list_tabs for multi-page workflows.

## File Workspace Tools
You can create, read, list, and update UTF-8 text files inside the local VulpineOS file workspace using vulpine_list_files, vulpine_read_file, and vulpine_write_file. Paths are relative to the workspace root where VulpineOS was launched; absolute paths and .. traversal are rejected.

## Workflow
1. vulpine_navigate to the target URL
2. vulpine_page_settled — wait until the page is usable
3. vulpine_snapshot to read state and collect refs when available
4. Identify the element ref or selector, then act
5. After actions, use vulpine_wait or vulpine_verify for the specific result
6. vulpine_snapshot to confirm the result
7. Send your final reply and stop

## Bounded Website Checks
When the assigned task asks you to test or compare websites, detectors, benchmarks, or diagnostics, stay within the requested set. If the task says "top 3", test three sites; do not expand to extra detector or benchmark sites unless explicitly assigned. Do not revisit a URL after you already captured usable page state from it. If one targeted wait times out, inspect the current snapshot once and continue instead of waiting for global quiet or restarting the same site. After each requested site has a usable result or an explicit failure, summarize and stop.

## Forbidden
- wget, curl, and raw HTTP clients are blocked by the network proxy — use vulpine_navigate only
- Playwright, Puppeteer, Selenium, and agent-browser CLI are not available — use vulpine_* tools only
- No host filesystem access outside the local file workspace
- No modifying VulpineOS system configuration

## Methodical Approach
Do not rush to a single narrow attempt. Be methodical: decompose, explore multiple angles, execute systematically, document as you go, and synthesise.

## Output Formatting
Write in a balanced chat style. Use **bold**, *italic*, inline code, fenced code blocks, bullets, numbered lists, tables and task checkboxes when they make the answer easier to scan. Do not use Markdown headings (#, ##, ###). Do not write horizontal rule divider lines (---, ***, ___); the VulpineOS UI owns message and tool dividers.

## Reporting
Be concise. Your final message is the result, not a transcript. If a tool reports an error, timeout, or incomplete data, report that exactly — never claim an action succeeded when it did not.`

// browserSystemPrompt is preserved as an alias for backward compatibility.
const browserSystemPrompt = LeadAgentPrompt

// RunBrowserAgent runs a native agent for one task against the host Vulpine browser.
// It opens a fresh browser context+page, drives it with the model loop via the
// vulpine_* tools, and returns the agent's final reply. The temporary context
// is removed on return. events may be nil.
//
// The toolset (with its persistent ContextTracker) is created BEFORE the page
// so it catches all Runtime.executionContextCreated, Page.frameAttached, and
// Browser.attachedToTarget events. Previously a throwaway tracker was used
// for page creation and the toolset's fresh tracker missed these events,
// causing every subsequent tool call to fail (unknown tab, missing frameId,
// no execution context, method-not-supported cascades).
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

	// Create toolset FIRST so its ContextTracker captures all page-creation
	// events. We'll populate contextID and sessionID after opening the page.
	toolset := NewBrowserToolset(client, "", "")
	defer toolset.Close()

	contextID, _, err := openPageWithToolset(ctx, client, toolset)
	if err != nil {
		return "", fmt.Errorf("open page: %w", err)
	}

	model := newCompleter(cfg)
	loop := NewLoop(model, toolset, events, LoopConfig{
		Models:        models,
		SystemPrompt:  LeadAgentPrompt,
		Tools:         BrowserTools(),
		MaxIterations: cfg.MaxIterations,
	})
	result, loopErr := loop.Run(ctx, task, nil)

	// Keep the browser context alive on failure so agents can be resumed
	// or inspected. Only clean up on success.
	if loopErr == nil {
		cleanupContext(client, contextID)
	}

	return result, loopErr
}

// RunBrowserAgentInContext runs a native agent for one task against a page
// created inside an existing (caller-owned) browser context — e.g. a pooled,
// identity-applied context from the orchestrator. It does NOT create or remove
// the context; the caller owns its lifecycle. Returns the agent's final reply.
//
// The toolset (with its persistent ContextTracker) is created BEFORE the page
// so it captures all initialisation events.
func RunBrowserAgentInContext(ctx context.Context, client *juggler.Client, contextID string, cfg Config, task string, events Events) (string, error) {
	if client == nil {
		return "", fmt.Errorf("juggler client is required")
	}

	toolset := NewBrowserToolset(client, "", "")
	defer toolset.Close()

	_, err := openPageInContextWithToolset(ctx, client, contextID, toolset)
	if err != nil {
		return "", fmt.Errorf("open page in context: %w", err)
	}

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
	_ = toolset.executor.WaitForTrackerInit(sessionID)
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
		SystemPrompt:  LeadAgentPrompt,
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
// Note: this uses a throwaway ContextTracker and is only suitable for one-shot
// callers. Agent loops should use openPageWithToolset instead.
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

// openPageWithToolset opens a fresh browser context+page using the toolset's
// persistent ContextTracker so the tracker captures all initialisation events.
// The toolset's tabs and contextID are populated on success.
func openPageWithToolset(ctx context.Context, client *juggler.Client, toolset *BrowserToolset) (contextID, sessionID string, err error) {
	if toolset == nil || toolset.executor == nil {
		return "", "", fmt.Errorf("toolset with executor is required")
	}

	res, err := toolset.executor.Call(ctx, "vulpine_new_context", json.RawMessage(`{}`))
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

	toolset.mu.Lock()
	toolset.contextID = out.ContextID
	toolset.tabs = []string{out.SessionID}
	toolset.active = 0
	toolset.mu.Unlock()

	// Wait for the tracker to capture initial frame + execution context so the
	// first navigate call can include the required frameId and Runtime.evaluate
	// calls can include executionContextId (required by newer Camoufox Juggler).
	if waitErr := toolset.executor.WaitForTrackerInit(out.SessionID); waitErr != nil {
		// Non-fatal: the retry logic in handleNavigate / evalJSWithTracker will
		// resolve the context at call time if this initial wait times out.
	}

	return out.ContextID, out.SessionID, nil
}

// openPageInContext creates a page inside an already-existing browser context
// (e.g. one acquired + identity-applied by the orchestrator's pool) and returns
// the new page's juggler session id. Mirrors the MCP new-context handler's
// attachedToTarget capture, but reuses the caller's context instead of making a
// fresh one.
// Note: uses a throwaway event subscription so event tracking is best-effort.
// Agent loops should use openPageInContextWithToolset instead.
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

// openPageInContextWithToolset creates a page inside an existing browser context
// using the toolset's persistent ContextTracker so all initialisation events
// are captured. The toolset's tab list is updated on success.
func openPageInContextWithToolset(ctx context.Context, client *juggler.Client, contextID string, toolset *BrowserToolset) (string, error) {
	if strings.TrimSpace(contextID) == "" {
		return "", fmt.Errorf("contextID is required")
	}
	if toolset == nil {
		return "", fmt.Errorf("toolset is required")
	}

	// The toolset's ContextTracker is already subscribed to events. Create
	// the page — the tracker will capture attachedToTarget and
	// executionContextCreated automatically.
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
	var sessionID string
	select {
	case sessionID = <-sessionCh:
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(15 * time.Second):
		return "", fmt.Errorf("timed out waiting for page session in context %s", contextID)
	}

	toolset.mu.Lock()
	toolset.contextID = contextID
	toolset.tabs = []string{sessionID}
	toolset.active = 0
	toolset.mu.Unlock()

	if waitErr := toolset.executor.WaitForTrackerInit(sessionID); waitErr != nil {
	}

	return sessionID, nil
}

// newSubAgentContext creates an isolated browser context with a single page
// for a sub-agent. Returns the contextID, sessionID, and toolset. Caller must
// call cleanupSubAgentContext when done.
func newSubAgentContext(ctx context.Context, client *juggler.Client) (contextID, sessionID string, toolset *BrowserToolset, err error) {
	ctxResult, callErr := client.Call("", "Browser.createBrowserContext", map[string]interface{}{
		"removeOnDetach": true,
	})
	if callErr != nil {
		return "", "", nil, fmt.Errorf("Browser.createBrowserContext: %w", callErr)
	}
	var parsed struct {
		BrowserContextID string `json:"browserContextId"`
	}
	if uerr := json.Unmarshal(ctxResult, &parsed); uerr != nil {
		cleanupContext(client, parsed.BrowserContextID)
		return "", "", nil, fmt.Errorf("parse createBrowserContext response: %w", uerr)
	}
	contextID = parsed.BrowserContextID

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

	if _, callErr = client.Call("", "Browser.newPage", map[string]interface{}{"browserContextId": contextID}); callErr != nil {
		cleanupContext(client, contextID)
		return "", "", nil, fmt.Errorf("Browser.newPage: %w", callErr)
	}
	select {
	case sessionID = <-sessionCh:
	case <-ctx.Done():
		cleanupContext(client, contextID)
		return "", "", nil, ctx.Err()
	case <-time.After(15 * time.Second):
		cleanupContext(client, contextID)
		return "", "", nil, fmt.Errorf("timed out waiting for page session")
	}

	toolset = NewBrowserToolset(client, contextID, sessionID)
	return contextID, sessionID, toolset, nil
}

// cleanupSubAgentContext closes a sub-agent's browser context and toolset.
func cleanupSubAgentContext(client *juggler.Client, contextID string, toolset *BrowserToolset) {
	if toolset != nil {
		toolset.Close()
	}
	cleanupContext(client, contextID)
}

func cleanupContext(client *juggler.Client, contextID string) {
	if client == nil || contextID == "" {
		return
	}
	_, _ = client.Call("", "Browser.removeBrowserContext", map[string]interface{}{
		"browserContextId": contextID,
	})
}
