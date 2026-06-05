package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"vulpineos/internal/juggler"
	"vulpineos/internal/tokenopt"
)

var (
	toolsOnce   sync.Once
	toolsCached []ToolDefinition
)

var newContextAttachTimeout = 10 * time.Second
var navigateVerificationTimeout = 5 * time.Second
var navigateVerificationPollInterval = 250 * time.Millisecond

// tools returns the list of VulpineOS browser tools available via MCP.
// The result is computed exactly once per process and cached. Callers
// must treat the returned slice as read-only; mutating it will affect
// every subsequent tools/list response.
func tools() []ToolDefinition {
	toolsOnce.Do(func() {
		base := baseTools()
		base = append(base, humanTools()...)
		toolsCached = append(base, extensionTools()...)
	})
	return toolsCached
}

// ToolDefinitions returns the full set of browser tool definitions exposed via
// MCP. It is the canonical source the native agent runtime (internal/agentcore)
// converts into model function schemas, so the two never drift. The returned
// slice is read-only.
func ToolDefinitions() []ToolDefinition {
	return tools()
}

// baseTools returns the core browser tool definitions.
func baseTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "vulpine_navigate",
			Description: "Navigate the browser to a URL",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"url":       {Type: "string", Description: "The URL to navigate to"},
					"sessionId": {Type: "string", Description: "Target page session ID (from vulpine_new_context)"},
				},
				Required: []string{"url", "sessionId"},
			},
		},
		{
			Name:        "vulpine_snapshot",
			Description: "Get a token-optimized semantic snapshot of the page content for LLM processing. Default profile is compact. If a target is missing from a truncated snapshot, retry with retry:true or profile:\"expanded\"/\"full\" before giving up.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId":     {Type: "string", Description: "Target page session ID"},
					"profile":       {Type: "string", Description: "Snapshot profile: compact, expanded, or full. Explicit max* values override this."},
					"retry":         {Type: "boolean", Description: "Use the next larger profile after a truncated snapshot for this session (compact -> expanded -> full)."},
					"maxDepth":      {Type: "number", Description: "Max tree depth (default compact: 10)"},
					"maxNodes":      {Type: "number", Description: "Max nodes to return (defaults to the selected profile)"},
					"maxTextLength": {Type: "number", Description: "Max text per node (defaults to the selected profile)"},
					"viewportOnly":  {Type: "boolean", Description: "Only return elements visible in the viewport (default false)"},
				},
				Required: []string{"sessionId"},
			},
		},
		{
			Name:        "vulpine_click",
			Description: "Click at specific coordinates on the page",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"x":         {Type: "number", Description: "X coordinate"},
					"y":         {Type: "number", Description: "Y coordinate"},
				},
				Required: []string{"sessionId", "x", "y"},
			},
		},
		{
			Name:        "vulpine_type",
			Description: "Type text into the currently focused element",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"text":      {Type: "string", Description: "Text to type"},
				},
				Required: []string{"sessionId", "text"},
			},
		},
		{
			Name:        "vulpine_screenshot",
			Description: "Take a screenshot of the current page",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
				},
				Required: []string{"sessionId"},
			},
		},
		{
			Name:        "vulpine_scroll",
			Description: "Scroll the page by a given amount",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"deltaY":    {Type: "number", Description: "Vertical scroll amount in pixels (positive = down)"},
				},
				Required: []string{"sessionId", "deltaY"},
			},
		},
		{
			Name:        "vulpine_new_context",
			Description: "Create a new isolated browser context with a fresh page. Returns the sessionId and contextId for subsequent operations.",
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]Property{},
			},
		},
		{
			Name:        "vulpine_close_context",
			Description: "Close a browser context and all its pages",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"contextId": {Type: "string", Description: "Browser context ID to close"},
				},
				Required: []string{"contextId"},
			},
		},
		{
			Name:        "vulpine_get_ax_tree",
			Description: "Get the full accessibility tree of the page (injection-proof filtered)",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
				},
				Required: []string{"sessionId"},
			},
		},
		{
			Name:        "vulpine_click_ref",
			Description: "Click an element by its snapshot-scoped ref from the optimized DOM snapshot (e.g. @7:0, @7:1). Use vulpine_snapshot first to get refs.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"ref":       {Type: "string", Description: "Snapshot-scoped element reference from snapshot (e.g. \"@7:0\", \"@7:1\")"},
				},
				Required: []string{"sessionId", "ref"},
			},
		},
		{
			Name:        "vulpine_type_ref",
			Description: "Focus an element by its ref from the optimized DOM snapshot and type text into it.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"ref":       {Type: "string", Description: "Snapshot-scoped element reference from snapshot (e.g. \"@7:0\", \"@7:1\")"},
					"text":      {Type: "string", Description: "Text to type into the element"},
				},
				Required: []string{"sessionId", "ref", "text"},
			},
		},
		{
			Name:        "vulpine_hover_ref",
			Description: "Hover over an element by its ref from the optimized DOM snapshot.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"ref":       {Type: "string", Description: "Snapshot-scoped element reference from snapshot (e.g. \"@7:0\", \"@7:1\")"},
				},
				Required: []string{"sessionId", "ref"},
			},
		},
		// --- Agent reliability tools ---
		{
			Name:        "vulpine_wait",
			Description: "Wait for a condition to be met on the page. Use this BEFORE taking actions to ensure the page is ready. Conditions: 'element' (CSS selector visible), 'text' (body contains text), 'networkIdle' (no pending requests), 'domStable' (DOM stopped changing), 'urlContains' (URL contains string).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"condition": {Type: "string", Description: "Condition type: element, text, networkIdle, domStable, urlContains"},
					"selector":  {Type: "string", Description: "CSS selector (for 'element' condition)"},
					"text":      {Type: "string", Description: "Text to match (for 'text' and 'urlContains' conditions)"},
					"timeout":   {Type: "number", Description: "Timeout in seconds (default 10, max 30)"},
				},
				Required: []string{"sessionId", "condition"},
			},
		},
		{
			Name:        "vulpine_find",
			Description: "Search for interactive elements by text content, aria-label, or placeholder. Returns matching elements with their position and role. Use this to locate elements when you don't have a ref from the snapshot.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId":  {Type: "string", Description: "Target page session ID"},
					"query":      {Type: "string", Description: "Text to search for (case-insensitive, matches text, aria-label, placeholder, title)"},
					"role":       {Type: "string", Description: "Optional: filter by element role (button, link, input, select, etc.)"},
					"maxResults": {Type: "number", Description: "Max results to return (default 5)"},
				},
				Required: []string{"sessionId", "query"},
			},
		},
		{
			Name:        "vulpine_verify",
			Description: "Verify element state after an action. Use this to confirm your action had the intended effect. Returns PASS or FAIL. Checks: 'exists', 'visible', 'checked', 'value', 'text', 'url', 'title'.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"check":     {Type: "string", Description: "What to check: exists, visible, checked, value, text, url, title"},
					"selector":  {Type: "string", Description: "CSS selector (for element checks)"},
					"expected":  {Type: "string", Description: "Expected value (for value, text, url, title checks)"},
				},
				Required: []string{"sessionId", "check"},
			},
		},
		{
			Name:        "vulpine_screenshot_diff",
			Description: "Take a screenshot checkpoint. Compares with the previous checkpoint for this session to detect if the page changed visually. Returns SAME or CHANGED. Use before and after actions to verify they had an effect.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"label":     {Type: "string", Description: "Label for this checkpoint (e.g. 'before_click', 'after_submit')"},
				},
				Required: []string{"sessionId"},
			},
		},
		{
			Name:        "vulpine_page_settled",
			Description: "Wait until the page is usable and preferably stable. Dynamic SPAs may return a usable warning instead of failing when they keep polling or updating. Timeout default: 30s.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"timeout":   {Type: "number", Description: "Timeout in seconds (default 30)"},
				},
				Required: []string{"sessionId"},
			},
		},
		{
			Name:        "vulpine_select_option",
			Description: "Select an option from a dropdown/select element. Specify either the option value or visible text.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"selector":  {Type: "string", Description: "CSS selector for the <select> element"},
					"value":     {Type: "string", Description: "Option value to select"},
					"text":      {Type: "string", Description: "Option visible text to select (alternative to value)"},
				},
				Required: []string{"sessionId", "selector"},
			},
		},
		{
			Name:        "vulpine_fill_form",
			Description: "Fill multiple form fields at once. Pass a map of CSS selectors to values. Triggers input and change events on each field.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"fields":    {Type: "object", Description: "Map of CSS selector → value to fill"},
				},
				Required: []string{"sessionId", "fields"},
			},
		},
		{
			Name:        "vulpine_page_info",
			Description: "Get comprehensive page state: URL, title, scroll position, number of forms/inputs/buttons/links, whether you can scroll further, and whether modals are open. Use this to understand the current page before deciding what to do.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
				},
				Required: []string{"sessionId"},
			},
		},
		{
			Name:        "vulpine_press_key",
			Description: "Press a keyboard key or shortcut. Supports: Enter, Tab, Escape, Backspace, Delete, ArrowUp/Down/Left/Right, Home, End, PageUp/Down, Space. Modifiers: ctrl, shift, alt, meta (e.g. \"ctrl+shift\").",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"key":       {Type: "string", Description: "Key name (Enter, Tab, Escape, Backspace, ArrowDown, etc.)"},
					"modifiers": {Type: "string", Description: "Optional modifiers: ctrl, shift, alt, meta, or combinations like ctrl+shift"},
				},
				Required: []string{"sessionId", "key"},
			},
		},
		{
			Name:        "vulpine_clear_input",
			Description: "Clear the text in an input field. Optionally specify a CSS selector to focus the element first, then selects all text and deletes it.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"selector":  {Type: "string", Description: "Optional CSS selector to focus before clearing"},
				},
				Required: []string{"sessionId"},
			},
		},
		{
			Name:        "vulpine_get_form_errors",
			Description: "Extract form validation error messages from the page. Checks HTML5 validation, common error CSS classes (.error, .is-invalid, [aria-invalid]), and aria-describedby messages.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"selector":  {Type: "string", Description: "CSS selector for the form (default: \"form\")"},
				},
				Required: []string{"sessionId"},
			},
		},
	}
}

// humanTools returns tool definitions for varied interaction tools.
func humanTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "vulpine_human_click",
			Description: "Move the pointer to coordinates with timed variation and click.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"x":         {Type: "number", Description: "X coordinate to click"},
					"y":         {Type: "number", Description: "Y coordinate to click"},
					"speed":     {Type: "string", Description: "Movement speed: slow, normal, fast (default: normal)"},
				},
				Required: []string{"sessionId", "x", "y"},
			},
		},
		{
			Name:        "vulpine_human_type",
			Description: "Type text with realistic human cadence. Variable inter-key intervals, occasional pauses.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"text":      {Type: "string", Description: "Text to type"},
					"wpm":       {Type: "number", Description: "Words per minute (default: 60)"},
				},
				Required: []string{"sessionId", "text"},
			},
		},
		{
			Name:        "vulpine_human_scroll",
			Description: "Scroll with realistic inertial decay.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId": {Type: "string", Description: "Target page session ID"},
					"deltaY":    {Type: "number", Description: "Total scroll amount in pixels (positive = down)"},
				},
				Required: []string{"sessionId", "deltaY"},
			},
		},
	}
}

// ToolExecutor dispatches browser tool calls against a persistent
// ContextTracker (and screenshot tracker) for the lifetime of an agent session.
// A long-lived tracker is required so that page execution contexts and frames —
// discovered from Juggler events as the page navigates — resolve across
// successive calls. The per-call HandleToolCallDirect* helpers create a
// throwaway tracker and therefore cannot resolve execution contexts for a
// multi-step agent; native agents must use a ToolExecutor instead.
type ToolExecutor struct {
	client      *juggler.Client
	tracker     *ContextTracker
	screenshots *ScreenshotTracker
}

// NewToolExecutor creates a session-scoped executor. Call Close when the agent
// session ends to drop the tracker's event subscriptions.
func NewToolExecutor(client *juggler.Client) *ToolExecutor {
	return &ToolExecutor{
		client:      client,
		tracker:     NewContextTracker(client),
		screenshots: NewScreenshotTracker(),
	}
}

// Call dispatches a tool against the persistent trackers.
func (e *ToolExecutor) Call(ctx context.Context, name string, args json.RawMessage) (*ToolCallResult, error) {
	return handleToolCallFull(ctx, e.client, e.tracker, e.screenshots, name, args)
}

// Close releases the executor's tracker subscriptions.
func (e *ToolExecutor) Close() {
	if e.tracker != nil {
		e.tracker.Close()
	}
}

// WaitForTrackerInit blocks until the tracker has captured frame and execution
// context for the given session. Returns immediately if already tracked. This
// ensures the first navigate call can include frameId and first evalJS call
// can include executionContextId — both required by newer Camoufox Juggler.
func (e *ToolExecutor) WaitForTrackerInit(sessionID string) error {
	if e.tracker == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := e.tracker.ResolveCtx(ctx, sessionID)
	return err
}

// Tracker exposes the tracker for direct use by agentcore session setup.
func (e *ToolExecutor) Tracker() *ContextTracker {
	return e.tracker
}

// HandleToolCallDirect dispatches a tool call directly (for testing).
func HandleToolCallDirect(client *juggler.Client, name string, args json.RawMessage) (*ToolCallResult, error) {
	return HandleToolCallDirectCtx(context.Background(), client, name, args)
}

// HandleToolCallDirectCtx dispatches a tool call directly with an
// explicit context, for tests and callers that want to pass through a
// per-call deadline or marker value into extension handlers.
func HandleToolCallDirectCtx(ctx context.Context, client *juggler.Client, name string, args json.RawMessage) (*ToolCallResult, error) {
	tracker := NewContextTracker(client)
	defer tracker.Close()
	return handleToolCall(ctx, client, tracker, name, args)
}

// handleToolCall dispatches a tool call to the appropriate handler.
func handleToolCall(ctx context.Context, client *juggler.Client, tracker *ContextTracker, name string, args json.RawMessage) (*ToolCallResult, error) {
	return handleToolCallFull(ctx, client, tracker, nil, name, args)
}

func handleToolCallFull(ctx context.Context, client *juggler.Client, tracker *ContextTracker, screenshots *ScreenshotTracker, name string, args json.RawMessage) (*ToolCallResult, error) {
	if res, ok := handleExtensionTool(ctx, client, name, args); ok {
		return res, nil
	}
	switch name {
	// Core browser tools
	case "vulpine_navigate":
		return handleNavigate(client, tracker, args)
	case "vulpine_snapshot":
		return handleSnapshot(client, args)
	case "vulpine_click":
		return handleClick(client, args)
	case "vulpine_type":
		return handleType(client, args)
	case "vulpine_screenshot":
		return handleScreenshot(client, args)
	case "vulpine_scroll":
		return handleScroll(client, tracker, args)
	case "vulpine_new_context":
		return handleNewContext(client, args)
	case "vulpine_close_context":
		return handleCloseContext(client, tracker, screenshots, args)
	case "vulpine_get_ax_tree":
		return handleGetAXTree(client, args)
	case "vulpine_click_ref":
		return handleClickRef(client, tracker, args)
	case "vulpine_type_ref":
		return handleTypeRef(client, tracker, args)
	case "vulpine_hover_ref":
		return handleHoverRef(client, tracker, args)

	// Agent reliability tools
	case "vulpine_wait":
		return handleWait(client, tracker, args)
	case "vulpine_find":
		return handleFind(client, tracker, args)
	case "vulpine_verify":
		return handleVerify(client, tracker, args)
	case "vulpine_screenshot_diff":
		if screenshots == nil {
			screenshots = NewScreenshotTracker()
		}
		return handleScreenshotDiff(client, screenshots, args)
	case "vulpine_page_settled":
		return handlePageSettled(client, tracker, args)
	case "vulpine_select_option":
		return handleSelectOption(client, tracker, args)
	case "vulpine_fill_form":
		return handleFillForm(client, tracker, args)
	case "vulpine_page_info":
		return handleGetPageInfo(client, tracker, args)
	case "vulpine_press_key":
		return handlePressKey(client, args)
	case "vulpine_clear_input":
		return handleClearInput(client, tracker, args)
	case "vulpine_get_form_errors":
		return handleGetFormErrors(client, tracker, args)

	// Human-like interaction tools
	case "vulpine_human_click":
		return handleHumanClick(client, args)
	case "vulpine_human_type":
		return handleHumanType(client, tracker, args)
	case "vulpine_human_scroll":
		return handleHumanScroll(client, tracker, args)

	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func textResult(text string) *ToolCallResult {
	return &ToolCallResult{
		Content: []ContentBlock{{Type: "text", Text: text}},
	}
}

func errorResult(err error) *ToolCallResult {
	return &ToolCallResult{
		Content: []ContentBlock{{Type: "text", Text: err.Error()}},
		IsError: true,
	}
}

// --- Tool handlers ---

func handleNavigate(client *juggler.Client, tracker *ContextTracker, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		URL       string `json:"url"`
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	if p.URL == "" {
		return errorResult(fmt.Errorf("url is required")), nil
	}
	if strings.TrimSpace(p.URL) != p.URL {
		return errorResult(fmt.Errorf("url must not have leading or trailing whitespace")), nil
	}
	normalizedURL := strings.ToLower(p.URL)
	if strings.HasPrefix(normalizedURL, "javascript:") {
		return errorResult(fmt.Errorf("javascript: URLs are not permitted")), nil
	}
	if !isAllowedSpecialNavigationURL(normalizedURL) && !strings.Contains(p.URL, "://") && !strings.HasPrefix(p.URL, "/") {
		return errorResult(fmt.Errorf("url %q is not absolute (missing scheme); prepend https://", p.URL)), nil
	}

	callParams := map[string]interface{}{"url": p.URL}
	if tracker != nil {
		if ctx := tracker.Get(p.SessionID); ctx != nil && ctx.FrameID != "" {
			callParams["frameId"] = ctx.FrameID
		}
	}

	_, err := client.Call(p.SessionID, "Page.navigate", callParams)
	if err != nil && callParams["frameId"] != nil && strings.Contains(err.Error(), "no browsing context for frameId") {
		delete(callParams, "frameId")
		_, err = client.Call(p.SessionID, "Page.navigate", callParams)
	}
	if err != nil && callParams["frameId"] == nil && isFrameIDRequiredError(err) && tracker != nil {
		ctx, resolveErr := tracker.ResolveFrame(p.SessionID)
		if resolveErr == nil && ctx != nil && ctx.FrameID != "" {
			callParams["frameId"] = ctx.FrameID
			_, err = client.Call(p.SessionID, "Page.navigate", callParams)
		}
	}
	if err != nil {
		return errorResult(err), nil
	}

	if tracker != nil {
		tracker.InvalidateExecutionContext(p.SessionID)
	}

	// Navigation invalidates every objectID captured by the previous
	// page's annotated screenshot. Drop any label mappings for this
	// session so vulpine_click_label fails fast instead of clicking a
	// stale handle that now points nowhere.
	globalLabels.Clear(p.SessionID)
	resetSnapshotProfile(p.SessionID)

	if err := waitForNavigationUsable(client, tracker, p.SessionID, p.URL); err != nil {
		return errorResult(err), nil
	}

	return textResult(fmt.Sprintf("Navigated to %s", p.URL)), nil
}

type navigationPageState struct {
	ReadyState    string `json:"readyState"`
	BodyLen       int    `json:"bodyLen"`
	ResourceCount int    `json:"resourceCount"`
	URL           string `json:"url"`
}

func isAllowedSpecialNavigationURL(normalizedURL string) bool {
	return normalizedURL == "about:blank"
}

func waitForNavigationUsable(client *juggler.Client, tracker *ContextTracker, sessionID, targetURL string) error {
	if isAllowedSpecialNavigationURL(strings.ToLower(targetURL)) {
		return nil
	}

	deadline := time.Now().Add(navigateVerificationTimeout)
	var lastState navigationPageState
	var haveState bool
	var lastErr error

	for {
		state, err := currentNavigationPageState(client, tracker, sessionID)
		if err == nil {
			lastState = state
			haveState = true
			if navigationPageStateUsable(state) {
				return nil
			}
		} else {
			lastErr = err
		}

		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(navigateVerificationPollInterval)
	}

	if haveState {
		return fmt.Errorf("navigation to %s did not load usable content within %s (last readyState=%s bodyLen=%d resourceCount=%d url=%s)",
			targetURL, navigateVerificationTimeout, lastState.ReadyState, lastState.BodyLen, lastState.ResourceCount, lastState.URL)
	}
	if lastErr != nil {
		return fmt.Errorf("navigation to %s could not verify usable content within %s: %w", targetURL, navigateVerificationTimeout, lastErr)
	}
	return fmt.Errorf("navigation to %s could not verify usable content within %s", targetURL, navigateVerificationTimeout)
}

func currentNavigationPageState(client *juggler.Client, tracker *ContextTracker, sessionID string) (navigationPageState, error) {
	js := `(() => {
		let rc = 0;
		try { rc = performance.getEntriesByType('resource').length; } catch(e) {}
		return JSON.stringify({
			readyState: document.readyState,
			bodyLen: document.body ? document.body.innerHTML.length : 0,
			resourceCount: rc,
			url: window.location.href
		});
	})()`
	var (
		result string
		err    error
	)
	if tracker != nil {
		if ctx := tracker.Get(sessionID); ctx != nil && ctx.ExecutionContextID != "" {
			result, err = evalJSWithContextID(client, sessionID, js, ctx.ExecutionContextID)
			if err == nil {
				return parseNavigationPageState(result)
			}
			tracker.InvalidateExecutionContext(sessionID)
		}
	} else {
		result, err = evalJS(client, sessionID, js)
	}
	if tracker != nil {
		result, err = evalJS(client, sessionID, js)
		if err != nil {
			if ctx, resolveErr := tracker.Resolve(sessionID); resolveErr == nil && ctx != nil && ctx.ExecutionContextID != "" {
				result, err = evalJSWithContextID(client, sessionID, js, ctx.ExecutionContextID)
			}
		}
	}
	if err != nil {
		return navigationPageState{}, err
	}
	return parseNavigationPageState(result)
}

func parseNavigationPageState(result string) (navigationPageState, error) {
	var state navigationPageState
	if err := json.Unmarshal([]byte(result), &state); err != nil {
		return navigationPageState{}, err
	}
	return state, nil
}

func navigationPageStateUsable(state navigationPageState) bool {
	if state.URL == "" || strings.EqualFold(state.URL, "about:blank") {
		return false
	}
	if state.ReadyState != "complete" && state.ReadyState != "interactive" {
		return false
	}
	return state.BodyLen > 0 || state.ResourceCount > 0
}

func isFrameIDRequiredError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "frameid") && (strings.Contains(msg, "required") || strings.Contains(msg, "missing") || strings.Contains(msg, "expected"))
}

// isMethodNotSupported reports whether a Juggler/CDP error indicates the
// target method is not implemented by the browser — either because the
// Juggler Dispatcher doesn't recognise it (standard Camoufox without
// VulpineOS additions) or the session handler doesn't implement it.
func isMethodNotSupported(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "is not supported") ||
		strings.Contains(msg, "method not found") ||
		strings.Contains(msg, "does not implement")
}

func handleSnapshot(client *juggler.Client, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID     string `json:"sessionId"`
		Profile       string `json:"profile"`
		Retry         bool   `json:"retry"`
		MaxDepth      int    `json:"maxDepth"`
		MaxNodes      int    `json:"maxNodes"`
		MaxTextLength int    `json:"maxTextLength"`
		ViewportOnly  bool   `json:"viewportOnly"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	profile, err := snapshotProfileByName(p.Profile)
	if err != nil {
		return errorResult(err), nil
	}
	if p.Profile == "" && p.Retry {
		profile = retrySnapshotProfile(p.SessionID)
	}
	explicitLimits := p.MaxDepth > 0 || p.MaxNodes > 0 || p.MaxTextLength > 0
	reportedProfile := profile
	if p.MaxDepth > 0 {
		profile.MaxDepth = p.MaxDepth
	}
	if p.MaxNodes > 0 {
		profile.MaxNodes = p.MaxNodes
	}
	if p.MaxTextLength > 0 {
		profile.MaxTextLength = p.MaxTextLength
	}
	if explicitLimits {
		reportedProfile = profile
		reportedProfile.Name = "custom"
	}

	params := map[string]interface{}{}
	params["profile"] = profile.Name
	params["maxDepth"] = profile.MaxDepth
	params["maxNodes"] = profile.MaxNodes
	params["maxTextLength"] = profile.MaxTextLength
	if p.ViewportOnly {
		params["viewportOnly"] = true
	}

	result, err := client.Call(p.SessionID, "Page.getOptimizedDOM", params)
	if err != nil {
		// If optimized DOM is unavailable or temporarily blocked by page/runtime
		// churn, fall back to the AX tree rather than failing inspection.
		axResult, axErr := client.Call(p.SessionID, "Accessibility.getFullAXTree", nil)
		if axErr != nil {
			if isMethodNotSupported(err) {
				return errorResult(axErr), nil
			}
			return errorResult(fmt.Errorf("optimized DOM failed: %v; AX fallback failed: %w", err, axErr)), nil
		}
		return textResult(string(axResult)), nil
	}

	annotated, truncated, err := annotateSnapshotPayload(result, reportedProfile)
	if err == nil {
		result = annotated
		if !explicitLimits {
			recordSnapshotProfile(p.SessionID, profile, truncated)
		}
	}

	// Apply viewport pruning to reduce token count when requested
	if p.ViewportOnly {
		var payload map[string]interface{}
		if err := json.Unmarshal(result, &payload); err == nil {
			if snapshot, ok := payload["snapshot"].(map[string]interface{}); ok {
				nodes, ok := snapshot["nodes"].([]interface{})
				if !ok {
					return textResult(string(result)), nil
				}
				pruner := tokenopt.NewViewportPruner(1280, 720)
				snapshot["nodes"] = pruner.Prune(nodes)
				if pruned, err := json.Marshal(payload); err == nil {
					return textResult(string(pruned)), nil
				}
			}
		}
		// Fall through to raw result if parsing/pruning fails
	}

	return textResult(string(result)), nil
}

func handleClick(client *juggler.Client, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string  `json:"sessionId"`
		X         float64 `json:"x"`
		Y         float64 `json:"y"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	// mousedown
	_, err := client.Call(p.SessionID, "Page.dispatchMouseEvent", map[string]interface{}{
		"type": "mousedown", "x": p.X, "y": p.Y,
		"button": 0, "clickCount": 1, "modifiers": 0, "buttons": 1,
	})
	if err != nil {
		return errorResult(err), nil
	}

	// mouseup
	_, err = client.Call(p.SessionID, "Page.dispatchMouseEvent", map[string]interface{}{
		"type": "mouseup", "x": p.X, "y": p.Y,
		"button": 0, "clickCount": 1, "modifiers": 0, "buttons": 0,
	})
	if err != nil {
		return errorResult(err), nil
	}

	return textResult(fmt.Sprintf("Clicked at (%v, %v)", p.X, p.Y)), nil
}

func handleType(client *juggler.Client, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Text      string `json:"text"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	_, err := client.Call(p.SessionID, "Page.insertText", map[string]interface{}{
		"text": p.Text,
	})
	if err != nil {
		return errorResult(err), nil
	}

	return textResult(fmt.Sprintf("Typed %d characters", len(p.Text))), nil
}

func handleScreenshot(client *juggler.Client, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	result, err := client.Call(p.SessionID, "Page.screenshot", map[string]interface{}{
		"mimeType": "image/png",
		"clip":     map[string]interface{}{"x": 0, "y": 0, "width": 1280, "height": 720},
	})
	if err != nil {
		return errorResult(err), nil
	}

	var screenshot struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(result, &screenshot); err != nil {
		return errorResult(err), nil
	}

	return &ToolCallResult{
		Content: []ContentBlock{{
			Type:     "image",
			Data:     screenshot.Data,
			MimeType: "image/png",
		}},
	}, nil
}

func handleScroll(client *juggler.Client, tracker *ContextTracker, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string  `json:"sessionId"`
		DeltaY    float64 `json:"deltaY"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	result, err := evalJSWithTracker(client, tracker, p.SessionID, fmt.Sprintf(`(() => {
		window.scrollBy(0, %f);
		return Math.round(window.scrollY);
	})()`, p.DeltaY))
	if err != nil {
		return errorResult(err), nil
	}

	return textResult(fmt.Sprintf("Scrolled by %v pixels to y=%s", p.DeltaY, result)), nil
}

func handleNewContext(client *juggler.Client, args json.RawMessage) (*ToolCallResult, error) {
	// Create context
	ctxResult, err := client.Call("", "Browser.createBrowserContext", map[string]interface{}{
		"removeOnDetach": true,
	})
	if err != nil {
		return errorResult(err), nil
	}

	var ctx struct {
		BrowserContextID string `json:"browserContextId"`
	}
	if err := json.Unmarshal(ctxResult, &ctx); err != nil {
		return errorResult(err), nil
	}
	if ctx.BrowserContextID == "" {
		return errorResult(fmt.Errorf("Browser.createBrowserContext returned empty browserContextId")), nil
	}
	cleanupContext := true
	defer func() {
		if cleanupContext {
			cleanupBrowserContext(client, ctx.BrowserContextID)
		}
	}()

	// Subscribe to get the sessionID from the attachedToTarget event. Filter by
	// browserContextId so concurrent target attaches cannot steal this result.
	sessionCh := make(chan string, 4)
	cancelAttach := client.SubscribeWithCancel("Browser.attachedToTarget", func(_ string, params json.RawMessage) {
		var ev struct {
			SessionID  string `json:"sessionId"`
			TargetInfo struct {
				BrowserContextID string `json:"browserContextId"`
			} `json:"targetInfo"`
		}
		json.Unmarshal(params, &ev)
		if ev.SessionID != "" && ev.TargetInfo.BrowserContextID == ctx.BrowserContextID {
			select {
			case sessionCh <- ev.SessionID:
			default:
			}
		}
	})
	defer cancelAttach()

	// Create page in context
	_, err = client.Call("", "Browser.newPage", map[string]interface{}{
		"browserContextId": ctx.BrowserContextID,
	})
	if err != nil {
		return errorResult(err), nil
	}

	// Wait for session ID from event
	var sessionID string
	select {
	case sessionID = <-sessionCh:
	case <-time.After(newContextAttachTimeout):
		return errorResult(fmt.Errorf("timed out waiting for page session")), nil
	}

	cleanupContext = false
	return textResult(fmt.Sprintf(`{"contextId":"%s","sessionId":"%s"}`, ctx.BrowserContextID, sessionID)), nil
}

func cleanupBrowserContext(client *juggler.Client, contextID string) {
	if client == nil || contextID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = client.CallWithContext(ctx, "", "Browser.removeBrowserContext", map[string]interface{}{
		"browserContextId": contextID,
	})
}

func handleCloseContext(client *juggler.Client, tracker *ContextTracker, screenshots *ScreenshotTracker, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		ContextID string `json:"contextId"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	_, err := client.Call("", "Browser.removeBrowserContext", map[string]interface{}{
		"browserContextId": p.ContextID,
	})
	if err != nil {
		return errorResult(err), nil
	}

	if tracker != nil {
		for _, sessionID := range tracker.SessionsForContext(p.ContextID) {
			tracker.RemoveSession(sessionID)
			resetSnapshotProfile(sessionID)
			if screenshots != nil {
				screenshots.Delete(sessionID)
			}
		}
	}

	return textResult("Context closed"), nil
}

func handleGetAXTree(client *juggler.Client, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	result, err := client.Call(p.SessionID, "Accessibility.getFullAXTree", nil)
	if err != nil {
		return errorResult(err), nil
	}

	return textResult(string(result)), nil
}

// resolveRef resolves an AX tree ref to {x, y, found} coordinates. Tries the
// native Juggler Page.resolveRef first (VulpineOS-enhanced browsers). Falls
// back to a JS-based approach using Runtime.evaluate when the browser doesn't
// support the custom Juggler method (stock Camoufox).
func resolveRef(client *juggler.Client, tracker *ContextTracker, sessionID, ref string) (x, y float64, found bool, err error) {
	result, err := client.Call(sessionID, "Page.resolveRef", map[string]interface{}{
		"ref": ref,
	})
	if err == nil {
		var resolved struct {
			X     float64 `json:"x"`
			Y     float64 `json:"y"`
			Found bool    `json:"found"`
		}
		if uerr := json.Unmarshal(result, &resolved); uerr == nil {
			return resolved.X, resolved.Y, resolved.Found, nil
		} else {
			return 0, 0, false, fmt.Errorf("decode Page.resolveRef: %w", uerr)
		}
	}

	if !isMethodNotSupported(err) {
		return 0, 0, false, err
	}

	// Fallback: re-fetch AX tree, find node by ref, resolve via JS.
	return resolveRefByJS(client, tracker, sessionID, ref)
}

// resolveRefByJS is the JS-based fallback for Page.resolveRef. It re-fetches
// the AX tree, finds the node matching ref, extracts identifying properties,
// and uses Runtime.evaluate to find the matching DOM element's coordinates.
func resolveRefByJS(client *juggler.Client, tracker *ContextTracker, sessionID, ref string) (x, y float64, found bool, err error) {
	axRaw, axErr := client.Call(sessionID, "Accessibility.getFullAXTree", nil)
	if axErr != nil {
		return 0, 0, false, fmt.Errorf("resolveRef fallback: getFullAXTree: %w", axErr)
	}

	var nodes []map[string]interface{}
	if uerr := json.Unmarshal(axRaw, &nodes); uerr != nil {
		return 0, 0, false, fmt.Errorf("resolveRef fallback: decode AX tree: %w", uerr)
	}

	var targetNode map[string]interface{}
	for _, node := range nodes {
		r, _ := node["ref"].(string)
		if r == "" {
			if rf, ok := node["ref"].(float64); ok {
				r = fmt.Sprintf("%.0f", rf)
			}
		}
		if r == ref {
			targetNode = node
			break
		}
	}
	if targetNode == nil {
		return 0, 0, false, fmt.Errorf("element ref %s not found in AX tree (stale snapshot?)", ref)
	}

	role, _ := targetNode["role"].(string)
	name, _ := targetNode["name"].(string)
	value, _ := targetNode["value"].(string)
	tag, _ := targetNode["tag"].(string)
	if tag == "" {
		tag = roleToTag(role)
	}

	// Use JS to find the element by its accessible properties
	js := fmt.Sprintf(`(() => {
		const targetRole = %q;
		const targetName = %q;
		const targetValue = %q;
		const targetTag = %q;

		function getAccessibleName(el) {
			return (el.getAttribute('aria-label') || el.textContent || '').trim();
		}

		function getAccessibleRole(el) {
			const role = el.getAttribute('role');
			if (role) return role;
			const tag = el.tagName.toLowerCase();
			const map = {
				'a': 'link', 'button': 'button', 'input': getInputRole(el),
				'select': 'combobox', 'textarea': 'textbox', 'h1': 'heading',
				'h2': 'heading', 'h3': 'heading', 'h4': 'heading', 'h5': 'heading',
				'h6': 'heading', 'img': 'img', 'nav': 'navigation',
				'main': 'main', 'header': 'banner', 'footer': 'contentinfo',
				'table': 'table', 'form': 'form',
			};
			return map[tag] || tag;
		}

		function getInputRole(el) {
			const t = (el.type || 'text').toLowerCase();
			if (t === 'checkbox') return 'checkbox';
			if (t === 'radio') return 'radio';
			if (t === 'submit' || t === 'button') return 'button';
			return 'textbox';
		}

		function matches(el) {
			const role = getAccessibleRole(el);
			const name = getAccessibleName(el);

			if (targetRole && role !== targetRole && el.tagName.toLowerCase() !== targetRole) return false;

			if (targetName) {
				const lowerName = targetName.toLowerCase();
				const elName = name.toLowerCase();
				if (!elName.includes(lowerName) && !lowerName.includes(elName)) return false;
			}

			const rect = el.getBoundingClientRect();
			return rect.width > 0 && rect.height > 0 && rect.x >= 0 && rect.y >= 0;
		}

		const interactive = 'a, button, input, select, textarea, [role="button"], [role="link"], [role="tab"], [role="menuitem"], [role="checkbox"], [role="radio"], [role="switch"], [role="option"], [tabindex], label, h1, h2, h3, h4, h5, h6, p, span, div, img, nav, main, header, footer, table, form, li, td, th';
		const elements = document.querySelectorAll(interactive);
		for (const el of elements) {
			if (matches(el)) {
				const rect = el.getBoundingClientRect();
				return JSON.stringify({
					x: Math.round(rect.x + rect.width / 2),
					y: Math.round(rect.y + rect.height / 2),
					found: true,
					tag: el.tagName.toLowerCase(),
				});
			}
		}

		// Try all elements if interactive selectors didn't match
		const all = document.querySelectorAll('*');
		for (const el of all) {
			if (matches(el)) {
				const rect = el.getBoundingClientRect();
				return JSON.stringify({
					x: Math.round(rect.x + rect.width / 2),
					y: Math.round(rect.y + rect.height / 2),
					found: true,
					tag: el.tagName.toLowerCase(),
				});
			}
		}

		return JSON.stringify({x: 0, y: 0, found: false});
	})()`, role, name, value, tag)

	jsResult, jsErr := evalJSWithTracker(client, tracker, sessionID, js)
	if jsErr != nil {
		return 0, 0, false, fmt.Errorf("resolveRef fallback JS: %w", jsErr)
	}

	var jsResolved struct {
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
		Found bool    `json:"found"`
	}
	if uerr := json.Unmarshal([]byte(jsResult), &jsResolved); uerr != nil {
		return 0, 0, false, fmt.Errorf("resolveRef fallback decode JS result: %w", uerr)
	}
	return jsResolved.X, jsResolved.Y, jsResolved.Found, nil
}

// roleToTag maps common AX roles to their HTML tag equivalents for DOM search.
func roleToTag(role string) string {
	m := map[string]string{
		"button": "button", "link": "a", "textbox": "input",
		"combobox": "select", "checkbox": "input", "radio": "input",
		"heading": "h1", "img": "img", "navigation": "nav",
		"banner": "header", "contentinfo": "footer", "main": "main",
		"form": "form", "table": "table", "list": "ul",
		"listitem": "li", "text": "p",
	}
	if t, ok := m[strings.ToLower(role)]; ok {
		return t
	}
	return role
}

func handleClickRef(client *juggler.Client, tracker *ContextTracker, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Ref       string `json:"ref"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	x, y, found, err := resolveRef(client, tracker, p.SessionID, p.Ref)
	if err != nil {
		return errorResult(err), nil
	}
	if !found {
		return errorResult(fmt.Errorf("element ref %s not found (stale snapshot?)", p.Ref)), nil
	}

	// mousedown
	_, err = client.Call(p.SessionID, "Page.dispatchMouseEvent", map[string]interface{}{
		"type": "mousedown", "x": x, "y": y,
		"button": 0, "clickCount": 1, "modifiers": 0, "buttons": 1,
	})
	if err != nil {
		return errorResult(err), nil
	}

	// mouseup
	_, err = client.Call(p.SessionID, "Page.dispatchMouseEvent", map[string]interface{}{
		"type": "mouseup", "x": x, "y": y,
		"button": 0, "clickCount": 1, "modifiers": 0, "buttons": 0,
	})
	if err != nil {
		return errorResult(err), nil
	}

	return textResult(fmt.Sprintf("Clicked %s at (%v, %v)", p.Ref, x, y)), nil
}

func handleTypeRef(client *juggler.Client, tracker *ContextTracker, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Ref       string `json:"ref"`
		Text      string `json:"text"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	x, y, found, err := resolveRef(client, tracker, p.SessionID, p.Ref)
	if err != nil {
		return errorResult(err), nil
	}
	if !found {
		return errorResult(fmt.Errorf("element ref %s not found (stale snapshot?)", p.Ref)), nil
	}

	_, err = client.Call(p.SessionID, "Page.dispatchMouseEvent", map[string]interface{}{
		"type": "mousedown", "x": x, "y": y,
		"button": 0, "clickCount": 1, "modifiers": 0, "buttons": 1,
	})
	if err != nil {
		return errorResult(err), nil
	}
	_, err = client.Call(p.SessionID, "Page.dispatchMouseEvent", map[string]interface{}{
		"type": "mouseup", "x": x, "y": y,
		"button": 0, "clickCount": 1, "modifiers": 0, "buttons": 0,
	})
	if err != nil {
		return errorResult(err), nil
	}

	// Type the text
	_, err = client.Call(p.SessionID, "Page.insertText", map[string]interface{}{
		"text": p.Text,
	})
	if err != nil {
		return errorResult(err), nil
	}

	return textResult(fmt.Sprintf("Typed %d characters into %s", len(p.Text), p.Ref)), nil
}

func handleHoverRef(client *juggler.Client, tracker *ContextTracker, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Ref       string `json:"ref"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	x, y, found, err := resolveRef(client, tracker, p.SessionID, p.Ref)
	if err != nil {
		return errorResult(err), nil
	}
	if !found {
		return errorResult(fmt.Errorf("element ref %s not found (stale snapshot?)", p.Ref)), nil
	}

	// mousemove
	_, err = client.Call(p.SessionID, "Page.dispatchMouseEvent", map[string]interface{}{
		"type": "mousemove", "x": x, "y": y,
		"button": 0, "clickCount": 0, "modifiers": 0, "buttons": 0,
	})
	if err != nil {
		return errorResult(err), nil
	}

	return textResult(fmt.Sprintf("Hovered %s at (%v, %v)", p.Ref, x, y)), nil
}
