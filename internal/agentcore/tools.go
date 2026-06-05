package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"vulpineos/internal/juggler"
	"vulpineos/internal/mcp"
)

// browserToolAllowList is the curated set of MCP browser tools exposed to the
// model. It covers navigation, inspection, interaction, reliability, and
// human-like input — everything an agent needs to drive Camoufox — while
// excluding page/context lifecycle tools (the loop manages a single page),
// pure-image tools (most models aren't multimodal), and extension-gated tools
// (credential/audio/mobile) that are no-ops without the private build.
var browserToolAllowList = map[string]bool{
	"vulpine_navigate":        true,
	"vulpine_snapshot":        true,
	"vulpine_click":           true,
	"vulpine_type":            true,
	"vulpine_scroll":          true,
	"vulpine_get_ax_tree":     true,
	"vulpine_click_ref":       true,
	"vulpine_type_ref":        true,
	"vulpine_hover_ref":       true,
	"vulpine_wait":            true,
	"vulpine_find":            true,
	"vulpine_verify":          true,
	"vulpine_page_settled":    true,
	"vulpine_select_option":   true,
	"vulpine_fill_form":       true,
	"vulpine_page_info":       true,
	"vulpine_press_key":       true,
	"vulpine_clear_input":     true,
	"vulpine_get_form_errors": true,
	"vulpine_human_click":     true,
	"vulpine_human_type":      true,
	"vulpine_human_scroll":    true,
}

// sessionIDArg is the per-page session argument the MCP tools take. The loop
// owns a single page and injects this automatically, so it is stripped from the
// schema the model sees.
const sessionIDArg = "sessionId"

// BrowserTools returns the curated browser tools as OpenAI function schemas,
// derived from the canonical MCP definitions (mcp.ToolDefinitions) with the
// sessionId argument removed. Output is deterministically ordered.
func BrowserTools() []ToolDef {
	defs := mcp.ToolDefinitions()
	out := make([]ToolDef, 0, len(defs))
	for _, d := range defs {
		if !browserToolAllowList[d.Name] {
			continue
		}
		props := map[string]interface{}{}
		var required []string
		for name, p := range d.InputSchema.Properties {
			if name == sessionIDArg {
				continue
			}
			prop := map[string]interface{}{"type": p.Type}
			if p.Description != "" {
				prop["description"] = p.Description
			}
			props[name] = prop
		}
		for _, r := range d.InputSchema.Required {
			if r != sessionIDArg {
				required = append(required, r)
			}
		}
		sort.Strings(required)
		params := map[string]interface{}{
			"type":       "object",
			"properties": props,
		}
		if len(required) > 0 {
			params["required"] = required
		}
		out = append(out, ToolDef{
			Type: "function",
			Function: FunctionDef{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  params,
			},
		})
	}
	out = append(out, tabTools()...)
	sort.Slice(out, func(i, j int) bool { return out[i].Function.Name < out[j].Function.Name })
	return out
}

// tabTools are the agent-facing tab-management tools (one context, many tabs).
func tabTools() []ToolDef {
	strProp := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": desc}
	}
	intProp := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "integer", "description": desc}
	}
	return []ToolDef{
		{Type: "function", Function: FunctionDef{
			Name:        toolOpenTab,
			Description: "Open a new browser tab in your current context and switch to it. Optionally provide a url to load in it. Use only when you genuinely need more than one page open at once; to simply move on to another site, navigate the current tab instead.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{"url": strProp("Optional URL to open in the new tab")}},
		}},
		{Type: "function", Function: FunctionDef{
			Name:        toolSwitchTab,
			Description: "Switch the active tab to the given 1-based tab index. Subsequent browser actions apply to that tab.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{"index": intProp("1-based tab index to make active")}, "required": []string{"index"}},
		}},
		{Type: "function", Function: FunctionDef{
			Name:        toolCloseTab,
			Description: "Close a tab by 1-based index (defaults to the active tab). The last remaining tab can't be closed.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{"index": intProp("1-based tab index to close (optional; default active)")}},
		}},
		{Type: "function", Function: FunctionDef{
			Name:        toolListTabs,
			Description: "List your open tabs and their current URLs, marking the active one.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		}},
	}
}

// Tab-management tool names handled by the toolset itself (not MCP browser
// tools). They let one agent open/switch/close multiple tabs within its single
// browser context.
const (
	toolOpenTab   = "vulpine_open_tab"
	toolSwitchTab = "vulpine_switch_tab"
	toolCloseTab  = "vulpine_close_tab"
	toolListTabs  = "vulpine_list_tabs"
)

// BrowserToolset dispatches model tool calls to the live browser via the MCP
// handlers. It holds a persistent mcp.ToolExecutor so page execution contexts
// resolve across the agent's successive tool calls, and it manages one or more
// tabs (pages) within a SINGLE browser context — the agent can open additional
// tabs, switch between them, and close them, but all stay in the one context
// that belongs to the agent. Browser tool calls run against the active tab.
type BrowserToolset struct {
	executor  *mcp.ToolExecutor
	client    *juggler.Client
	contextID string // the agent's context; "" disables tab management (single page)
	loopDet   *mcp.LoopDetector

	mu     sync.Mutex
	tabs   []string // open page session ids (tabs) in this context
	active int      // index into tabs of the active tab
}

// NewBrowserToolset binds a toolset to a juggler client, the agent's browser
// context, and the initial page session. contextID may be "" when tab
// management isn't available (single-page callers). Call Close when done. It
// carries a loop detector so repeated identical, progress-less tool calls are
// nudged toward a different approach (parity with the MCP server path).
func NewBrowserToolset(client *juggler.Client, contextID, sessionID string) *BrowserToolset {
	return &BrowserToolset{
		executor:  mcp.NewToolExecutor(client),
		client:    client,
		contextID: contextID,
		loopDet:   mcp.NewLoopDetector(3),
		tabs:      []string{sessionID},
		active:    0,
	}
}

// activeSession returns the session id of the active tab.
func (t *BrowserToolset) activeSession() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active < 0 || t.active >= len(t.tabs) {
		return ""
	}
	return t.tabs[t.active]
}

// Close releases the toolset's persistent tracker subscriptions.
func (t *BrowserToolset) Close() {
	if t.executor != nil {
		t.executor.Close()
	}
}

// CloseExtraTabs closes all non-primary tabs and resets the active tab to the
// first page. The primary tab remains open across turns so the chat agent keeps
// its context and current page, while temporary multi-page work is cleaned up
// when the turn is finished.
func (t *BrowserToolset) CloseExtraTabs() error {
	if t == nil || t.client == nil {
		return nil
	}
	t.mu.Lock()
	if len(t.tabs) <= 1 {
		t.active = 0
		t.mu.Unlock()
		return nil
	}
	extra := append([]string(nil), t.tabs[1:]...)
	t.tabs = t.tabs[:1]
	t.active = 0
	t.mu.Unlock()

	var failures []string
	for _, sid := range extra {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, err := t.client.CallWithContext(ctx, sid, "Page.close", map[string]interface{}{"runBeforeUnload": false})
		cancel()
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", sid, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("close extra tabs: %s", strings.Join(failures, "; "))
	}
	return nil
}

// IsBrowserTool reports whether name is a browser tool this toolset handles.
func IsBrowserTool(name string) bool {
	return browserToolAllowList[name]
}

// Dispatch executes a browser tool call. rawArgs is the model-provided JSON
// arguments object (without sessionId). It injects the bound session, invokes
// the MCP handler, and returns the textual result. A tool-level failure (e.g.
// element not found) is returned as text with isErr=true rather than a Go
// error, so the loop can feed it back to the model; err is non-nil only for
// dispatch-level failures (unknown tool, malformed args).
func (t *BrowserToolset) Dispatch(ctx context.Context, name string, rawArgs string) (result string, isErr bool, err error) {
	// Tab-management tools are handled in-toolset (one context, many tabs).
	switch name {
	case toolOpenTab:
		return t.openTab(ctx, rawArgs)
	case toolSwitchTab:
		return t.switchTab(rawArgs)
	case toolCloseTab:
		return t.closeTab(ctx, rawArgs)
	case toolListTabs:
		return t.listTabs(ctx)
	}

	if !browserToolAllowList[name] {
		return "", false, fmt.Errorf("unknown or disallowed tool: %s", name)
	}

	session := t.activeSession()
	if session == "" {
		return "", false, fmt.Errorf("no active browser tab")
	}

	args := map[string]interface{}{}
	trimmed := strings.TrimSpace(rawArgs)
	if trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
			return "", false, fmt.Errorf("parse arguments for %s: %w", name, err)
		}
	}
	// Loop detection: if the model repeats the same tool+args with no progress,
	// nudge it toward a different approach instead of re-running the dead action.
	if t.loopDet != nil {
		if warn := t.loopDet.Check(session, name, trimmed); warn != "" {
			return warn, true, nil
		}
	}

	args[sessionIDArg] = session

	encoded, err := json.Marshal(args)
	if err != nil {
		return "", false, fmt.Errorf("encode arguments for %s: %w", name, err)
	}

	res, err := t.executor.Call(ctx, name, encoded)
	if err != nil {
		return "", false, err
	}
	text := contentText(res)
	isErr = res.IsError || looksLikeToolFailure(text)
	// Navigation moves to a new page; clear the loop history so legitimate
	// repeated actions on the new page aren't falsely flagged.
	if t.loopDet != nil && name == "vulpine_navigate" {
		t.loopDet.Reset(session)
	}
	return text, isErr, nil
}

// openTab opens a new tab (page) in the agent's context, makes it active, and
// optionally navigates it to a URL. All tabs share the one agent context.
func (t *BrowserToolset) openTab(ctx context.Context, rawArgs string) (string, bool, error) {
	if strings.TrimSpace(t.contextID) == "" {
		return "tab management unavailable for this agent (no browser context)", true, nil
	}
	var args struct {
		URL string `json:"url"`
	}
	trimmed := strings.TrimSpace(rawArgs)
	if trimmed != "" && trimmed != "null" {
		_ = json.Unmarshal([]byte(trimmed), &args)
	}
	sid, err := openPageInContext(ctx, t.client, t.contextID)
	if err != nil {
		return "", false, fmt.Errorf("open tab: %w", err)
	}
	if t.executor != nil {
		_ = t.executor.WaitForTrackerInit(sid)
	}
	t.mu.Lock()
	t.tabs = append(t.tabs, sid)
	t.active = len(t.tabs) - 1
	idx := t.active + 1
	total := len(t.tabs)
	t.mu.Unlock()

	if u := strings.TrimSpace(args.URL); u != "" {
		nav, _ := json.Marshal(map[string]interface{}{sessionIDArg: sid, "url": u})
		if res, callErr := t.executor.Call(ctx, "vulpine_navigate", nav); callErr == nil {
			return fmt.Sprintf("Opened tab %d/%d (now active) and navigated to %s. %s", idx, total, u, contentText(res)), false, nil
		}
	}
	return fmt.Sprintf("Opened tab %d/%d (now active, blank). Use vulpine_navigate to load a page.", idx, total), false, nil
}

// switchTab makes the 1-based tab index active.
func (t *BrowserToolset) switchTab(rawArgs string) (string, bool, error) {
	var args struct {
		Index int `json:"index"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(rawArgs)), &args); err != nil {
		return "", false, fmt.Errorf("parse switch_tab arguments: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if args.Index < 1 || args.Index > len(t.tabs) {
		return fmt.Sprintf("no tab %d; there are %d open tab(s)", args.Index, len(t.tabs)), true, nil
	}
	t.active = args.Index - 1
	return fmt.Sprintf("Switched to tab %d/%d", args.Index, len(t.tabs)), false, nil
}

// closeTab closes the given 1-based tab index (default: active). The last
// remaining tab cannot be closed (the agent always keeps one tab).
func (t *BrowserToolset) closeTab(ctx context.Context, rawArgs string) (string, bool, error) {
	var args struct {
		Index int `json:"index"`
	}
	trimmed := strings.TrimSpace(rawArgs)
	if trimmed != "" && trimmed != "null" {
		_ = json.Unmarshal([]byte(trimmed), &args)
	}
	t.mu.Lock()
	if len(t.tabs) <= 1 {
		t.mu.Unlock()
		return "cannot close the last remaining tab; navigate it instead", true, nil
	}
	idx := t.active
	if args.Index >= 1 && args.Index <= len(t.tabs) {
		idx = args.Index - 1
	}
	sid := t.tabs[idx]
	t.tabs = append(t.tabs[:idx], t.tabs[idx+1:]...)
	if t.active >= len(t.tabs) {
		t.active = len(t.tabs) - 1
	}
	total := len(t.tabs)
	activeIdx := t.active + 1
	t.mu.Unlock()

	_, _ = t.client.Call(sid, "Page.close", map[string]interface{}{"runBeforeUnload": false})
	return fmt.Sprintf("Closed tab %d; %d tab(s) remain (active: tab %d)", idx+1, total, activeIdx), false, nil
}

// listTabs reports the open tabs and their current URLs.
func (t *BrowserToolset) listTabs(ctx context.Context) (string, bool, error) {
	t.mu.Lock()
	tabs := append([]string(nil), t.tabs...)
	active := t.active
	t.mu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "%d open tab(s):\n", len(tabs))
	for i, sid := range tabs {
		url := ""
		errStr := ""
		info, _ := json.Marshal(map[string]interface{}{sessionIDArg: sid})
		if res, err := t.executor.Call(ctx, "vulpine_page_info", info); err == nil {
			url = extractURL(contentText(res))
			if url == "" {
				url = "(loading)"
			}
		} else {
			errStr = fmt.Sprintf(" [error: %v]", err)
		}
		marker := " "
		if i == active {
			marker = "*"
		}
		fmt.Fprintf(&b, " %s tab %d: %s%s\n", marker, i+1, url, errStr)
	}
	return strings.TrimRight(b.String(), "\n"), false, nil
}

// extractURL pulls a "url" field out of a page_info JSON result, best-effort.
func extractURL(s string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err == nil {
		if u, ok := m["url"].(string); ok {
			return u
		}
	}
	return "(unknown)"
}

// contentText flattens an MCP tool result's content blocks into a single text
// string for feeding back to the model. Image blocks are summarized rather than
// inlined.
func contentText(res *mcp.ToolCallResult) string {
	if res == nil {
		return ""
	}
	var parts []string
	for _, b := range res.Content {
		switch b.Type {
		case "text":
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		case "image":
			parts = append(parts, "[image captured]")
		}
	}
	return strings.Join(parts, "\n")
}

func looksLikeToolFailure(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "FAIL:") ||
		strings.HasPrefix(trimmed, "No elements found") ||
		strings.HasPrefix(trimmed, "SAME:") ||
		strings.Contains(trimmed, "\nErrors:")
}
