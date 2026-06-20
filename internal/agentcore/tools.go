package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"vulpineos/internal/juggler"
	"vulpineos/internal/mcp"
)

// browserToolAllowList is the curated set of MCP browser tools exposed to the
// model. It covers navigation, inspection, interaction, reliability, and
// human-like input — everything an agent needs to drive Vulpine — while
// excluding page/context lifecycle tools (the loop manages a single page),
// pure-image tools (most models aren't multimodal), and extension-gated tools
// (credential/audio/mobile) that are no-ops without the private build.
var browserToolAllowList = map[string]bool{
	"vulpine_navigate":         true,
	"vulpine_snapshot":         true,
	"vulpine_click":            true,
	"vulpine_type":             true,
	"vulpine_scroll":           true,
	"vulpine_get_ax_tree":      true,
	"vulpine_click_ref":        true,
	"vulpine_type_ref":         true,
	"vulpine_hover_ref":        true,
	"vulpine_scroll_into_view": true,
	"vulpine_wait":             true,
	"vulpine_find":             true,
	"vulpine_verify":           true,
	"vulpine_page_settled":     true,
	"vulpine_select_option":    true,
	"vulpine_fill_form":        true,
	"vulpine_page_info":        true,
	"vulpine_press_key":        true,
	"vulpine_clear_input":      true,
	"vulpine_get_form_errors":  true,
	"vulpine_element_status":   true,
	"vulpine_human_click":      true,
	"vulpine_human_type":       true,
	"vulpine_human_scroll":     true,
}

// sessionIDArg is the per-page session argument the MCP tools take. The loop
// owns a single page and injects this automatically, so it is stripped from the
// schema the model sees.
const sessionIDArg = "sessionId"

// BrowserTools returns the curated agent tools as OpenAI function schemas:
// browser tools derived from the canonical MCP definitions plus local workspace
// file tools. Output is deterministically ordered.
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
	out = append(out, fileTools()...)
	out = append(out, delegationTools()...)
	sort.Slice(out, func(i, j int) bool { return out[i].Function.Name < out[j].Function.Name })
	return out
}

// subAgentTools returns the tool set for sub-agents: browser tools, tabs, and
// file workspace tools, but NOT delegation tools (sub-agents cannot delegate
// further). This prevents the model from seeing irrelevant tool definitions
// that would waste context and produce confusing "not available" errors.
func subAgentTools() []ToolDef {
	all := BrowserTools()
	filtered := make([]ToolDef, 0, len(all))
	for _, td := range all {
		switch td.Function.Name {
		case toolDelegateAgent, toolSteerAgent, toolAgentStatus, toolReleaseAgent, toolGetAgentResult, toolGetAgentSnapshot:
			continue
		}
		filtered = append(filtered, td)
	}
	return filtered
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

	toolListFiles = "vulpine_list_files"
	toolReadFile  = "vulpine_read_file"
	toolWriteFile = "vulpine_write_file"

	toolDelegateAgent    = "vulpine_delegate_agent"
	toolSteerAgent       = "vulpine_steer_agent"
	toolAgentStatus      = "vulpine_agent_status"
	toolReleaseAgent     = "vulpine_release_agent"
	toolGetAgentResult   = "vulpine_get_agent_result"
	toolGetAgentSnapshot = "vulpine_get_agent_snapshot"
)

// DelegationManager is the interface for delegating work to sub-agents.
// The lead agent uses these methods to manage sub-agent lifecycle.
type DelegationManager interface {
	Delegate(mission Mission) (string, error)
	SteerAgent(agentID, message string) error
	AgentStatus(agentID string) (string, error)
	AgentResult(agentID string) (string, error)
	ReleaseAgent(agentID string) error
	AgentSnapshot(agentID string) (string, error) // rich JSON status for diagnostics
}

func fileTools() []ToolDef {
	strProp := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": desc}
	}
	boolProp := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "boolean", "description": desc}
	}
	intProp := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "integer", "description": desc}
	}
	return []ToolDef{
		{Type: "function", Function: FunctionDef{
			Name:        toolListFiles,
			Description: "List files under the local VulpineOS file workspace. Paths must be relative to the workspace root; absolute paths and .. traversal are rejected.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"path":        strProp("Directory to list, relative to the workspace root. Defaults to ."),
				"recursive":   boolProp("Whether to recursively list descendants. Defaults to false."),
				"max_entries": intProp("Maximum entries to return. Defaults to 200, capped at 1000."),
			}},
		}},
		{Type: "function", Function: FunctionDef{
			Name:        toolReadFile,
			Description: "Read a UTF-8 text file from the local VulpineOS file workspace. Paths must be relative to the workspace root; absolute paths and .. traversal are rejected.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"path":      strProp("File path to read, relative to the workspace root."),
				"max_bytes": intProp("Maximum bytes to return. Defaults to 100000, capped at 1000000."),
			}, "required": []string{"path"}},
		}},
		{Type: "function", Function: FunctionDef{
			Name:        toolWriteFile,
			Description: "Create or update a UTF-8 text file in the local VulpineOS file workspace. Parent directories are created as needed. Paths must be relative to the workspace root; absolute paths and .. traversal are rejected.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"path":    strProp("File path to write, relative to the workspace root."),
				"content": strProp("Complete UTF-8 text content to write, or text to append when append is true."),
				"append":  boolProp("Append content instead of replacing the file. Defaults to false."),
			}, "required": []string{"path", "content"}},
		}},
	}
}

func isFileTool(name string) bool {
	switch name {
	case toolListFiles, toolReadFile, toolWriteFile:
		return true
	default:
		return false
	}
}

// BrowserToolset dispatches model tool calls to the live browser via the MCP
// handlers. It holds a persistent mcp.ToolExecutor so page execution contexts
// resolve across the agent's successive tool calls, and it manages one or more
// tabs (pages) within a SINGLE browser context — the agent can open additional
// tabs, switch between them, and close them, but all stay in the one context
// that belongs to the agent. Browser tool calls run against the active tab.
type BrowserToolset struct {
	executor         *mcp.ToolExecutor
	client           *juggler.Client
	contextID        string // the agent's context; "" disables tab management (single page)
	loopDet          *mcp.LoopDetector
	workspace        string
	delegateMgr      DelegationManager // non-nil for lead agents with delegation capability
	delegateParentID string

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
		workspace: agentWorkspaceRoot(),
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

// SetDelegateManager sets the delegation manager for this toolset.
// Called by Manager when preparing a lead agent's toolset.
func (t *BrowserToolset) SetDelegateManager(mgr DelegationManager) {
	t.delegateMgr = mgr
	t.delegateParentID = ""
}

func (t *BrowserToolset) SetDelegateManagerForParent(mgr DelegationManager, parentID string) {
	t.delegateMgr = mgr
	t.delegateParentID = parentID
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
	if isFileTool(name) {
		return t.dispatchFileTool(name, rawArgs)
	}

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

	switch name {
	case toolDelegateAgent, toolSteerAgent, toolAgentStatus, toolReleaseAgent, toolGetAgentResult, toolGetAgentSnapshot:
		return t.dispatchDelegationTool(ctx, name, rawArgs)
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

type fileToolArgs struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Append     bool   `json:"append"`
	Recursive  bool   `json:"recursive"`
	MaxBytes   int    `json:"max_bytes"`
	MaxEntries int    `json:"max_entries"`
}

func (t *BrowserToolset) dispatchFileTool(name, rawArgs string) (string, bool, error) {
	args := fileToolArgs{}
	trimmed := strings.TrimSpace(rawArgs)
	if trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
			return "", false, fmt.Errorf("parse arguments for %s: %w", name, err)
		}
	}
	switch name {
	case toolListFiles:
		return t.listWorkspaceFiles(args)
	case toolReadFile:
		return t.readWorkspaceFile(args)
	case toolWriteFile:
		return t.writeWorkspaceFile(args)
	default:
		return "", false, fmt.Errorf("unknown file tool: %s", name)
	}
}

func (t *BrowserToolset) dispatchDelegationTool(ctx context.Context, name, rawArgs string) (string, bool, error) {
	if t.delegateMgr == nil {
		return "delegation manager not available (only lead agents can delegate)", true, nil
	}
	trimmed := strings.TrimSpace(rawArgs)
	switch name {
	case toolDelegateAgent:
		var args struct {
			RoleSeed    string   `json:"role_seed"`
			Objective   string   `json:"objective"`
			Context     string   `json:"context"`
			Constraints []string `json:"constraints"`
			OutputSpec  string   `json:"output_spec"`
			MaxTurns    int      `json:"max_turns"`
		}
		if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
			return "", false, fmt.Errorf("parse arguments for %s: %w", name, err)
		}
		mission := Mission{
			RoleSeed:    args.RoleSeed,
			Objective:   args.Objective,
			Context:     args.Context,
			Constraints: args.Constraints,
			OutputSpec:  args.OutputSpec,
			MaxTurns:    args.MaxTurns,
		}
		var (
			agentID string
			err     error
		)
		if t.delegateParentID != "" {
			if parentMgr, ok := t.delegateMgr.(interface {
				DelegateForParentMission(Mission, string) (string, error)
			}); ok {
				agentID, err = parentMgr.DelegateForParentMission(mission, t.delegateParentID)
			} else {
				agentID, err = t.delegateMgr.Delegate(mission)
			}
		} else {
			agentID, err = t.delegateMgr.Delegate(mission)
		}
		if err != nil {
			return err.Error(), true, nil
		}
		return fmt.Sprintf("Delegated to sub-agent %s", agentID), false, nil

	case toolSteerAgent:
		var args struct {
			AgentID string `json:"agent_id"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
			return "", false, fmt.Errorf("parse arguments for %s: %w", name, err)
		}
		if args.AgentID == "" {
			return "agent_id is required", true, nil
		}
		if err := t.delegateMgr.SteerAgent(args.AgentID, args.Message); err != nil {
			return err.Error(), true, nil
		}
		return "Steering message sent", false, nil

	case toolAgentStatus:
		var args struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
			return "", false, fmt.Errorf("parse arguments for %s: %w", name, err)
		}
		if args.AgentID == "" {
			return "agent_id is required", true, nil
		}
		status, err := t.delegateMgr.AgentStatus(args.AgentID)
		if err != nil {
			return err.Error(), true, nil
		}
		return status, false, nil

	case toolReleaseAgent:
		var args struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
			return "", false, fmt.Errorf("parse arguments for %s: %w", name, err)
		}
		if args.AgentID == "" {
			return "agent_id is required", true, nil
		}
		if err := t.delegateMgr.ReleaseAgent(args.AgentID); err != nil {
			return err.Error(), true, nil
		}
		return "Sub-agent released", false, nil

	case toolGetAgentResult:
		var args struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
			return "", false, fmt.Errorf("parse arguments for %s: %w", name, err)
		}
		if args.AgentID == "" {
			return "agent_id is required", true, nil
		}
		result, err := t.delegateMgr.AgentResult(args.AgentID)
		if err != nil {
			return err.Error(), true, nil
		}
		if result == "" {
			return "(agent produced no output)", false, nil
		}
		return result, false, nil

	case toolGetAgentSnapshot:
		var args struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
			return "", false, fmt.Errorf("parse arguments for %s: %w", name, err)
		}
		if args.AgentID == "" {
			return "agent_id is required", true, nil
		}
		snapshot, err := t.delegateMgr.AgentSnapshot(args.AgentID)
		if err != nil {
			return err.Error(), true, nil
		}
		return snapshot, false, nil

	default:
		return "", false, fmt.Errorf("unknown delegation tool: %s", name)
	}
}

func delegationTools() []ToolDef {
	intProp := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "integer", "description": desc}
	}
	strProp := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": desc}
	}
	arrStrProp := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "array", "description": desc, "items": map[string]interface{}{"type": "string"}}
	}
	return []ToolDef{
		{Type: "function", Function: FunctionDef{
			Name:        toolDelegateAgent,
			Description: "Delegate a sub-task to a sub-agent with its own system prompt, objective, and isolated browser context. The sub-agent runs independently with full browser and file workspace tools. You can check its status, steer it mid-task, and retrieve its final output. Returns the sub-agent ID.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"role_seed":   strProp("Role identity for the sub-agent, e.g. 'You are a code reviewer.'"),
				"objective":   strProp("Concise objective describing what to accomplish"),
				"context":     strProp("Optional background information the sub-agent needs"),
				"constraints": arrStrProp("Optional list of rules and boundaries for the sub-agent"),
				"output_spec": strProp("Optional expected output format"),
				"max_turns":   intProp("Optional maximum iterations for this mission (default 25)"),
			}, "required": []string{"objective"}},
		}},
		{Type: "function", Function: FunctionDef{
			Name:        toolSteerAgent,
			Description: "Send a mid-task steering message to a running sub-agent. The sub-agent receives the message as guidance on its next turn.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"agent_id": strProp("The sub-agent ID to steer"),
				"message":  strProp("Guidance message for the sub-agent"),
			}, "required": []string{"agent_id", "message"}},
		}},
		{Type: "function", Function: FunctionDef{
			Name:        toolAgentStatus,
			Description: "Check the current status of a sub-agent. Returns the agent's status (running, completed, error) or an error if the agent is not found.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"agent_id": strProp("The sub-agent ID to check"),
			}, "required": []string{"agent_id"}},
		}},
		{Type: "function", Function: FunctionDef{
			Name:        toolReleaseAgent,
			Description: "Release (kill) a sub-agent. The sub-agent is interrupted and its resources cleaned up. Use this when the sub-agent is no longer needed or its mission is superseded.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"agent_id": strProp("The sub-agent ID to release"),
			}, "required": []string{"agent_id"}},
		}},
		{Type: "function", Function: FunctionDef{
			Name:        toolGetAgentResult,
			Description: "Retrieve the final output of a completed sub-agent. Returns the agent's final response text, or an error if the agent is still running or not found. Only completed agents have a result available.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"agent_id": strProp("The sub-agent ID to get the result from"),
			}, "required": []string{"agent_id"}},
		}},
		{Type: "function", Function: FunctionDef{
			Name:        toolGetAgentSnapshot,
			Description: "Get a detailed JSON snapshot of a sub-agent's current state for diagnostics. Includes status, phase (processing/waiting_on_tool/idle/finalizing), turn count, max turns, last activity timestamp, and whether a final result is available. Use this to distinguish actively progressing agents from stuck/idle ones.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"agent_id": strProp("The sub-agent ID to inspect"),
			}, "required": []string{"agent_id"}},
		}},
	}
}

func agentWorkspaceRoot() string {
	root := strings.TrimSpace(os.Getenv("VULPINEOS_AGENT_WORKSPACE"))
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	if root == "" {
		root = "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return root
}

func (t *BrowserToolset) workspacePath(relPath string, existing bool) (string, string, error) {
	root := t.workspace
	if strings.TrimSpace(root) == "" {
		root = agentWorkspaceRoot()
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	if resolvedRoot, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootAbs = resolvedRoot
	}

	if strings.TrimSpace(relPath) == "" {
		relPath = "."
	}
	if filepath.IsAbs(relPath) {
		return "", "", fmt.Errorf("absolute paths are not allowed")
	}
	clean := filepath.Clean(relPath)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path escapes workspace root")
	}
	candidate := filepath.Join(rootAbs, clean)
	checkPath := candidate
	if !existing {
		checkPath = filepath.Dir(candidate)
		resolved, err := existingAncestor(checkPath)
		if err != nil {
			return "", "", err
		}
		if err := ensurePathWithin(rootAbs, resolved); err != nil {
			return "", "", err
		}
		return candidate, clean, nil
	}
	resolved, err := filepath.EvalSymlinks(checkPath)
	if err != nil {
		return "", "", err
	}
	if err := ensurePathWithin(rootAbs, resolved); err != nil {
		return "", "", err
	}
	return candidate, clean, nil
}

func existingAncestor(path string) (string, error) {
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path, nil
		}
		path = parent
	}
}

func ensurePathWithin(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path escapes workspace root")
	}
	return nil
}

func (t *BrowserToolset) listWorkspaceFiles(args fileToolArgs) (string, bool, error) {
	path, rel, err := t.workspacePath(args.Path, true)
	if err != nil {
		return err.Error(), true, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err.Error(), true, nil
	}
	if !info.IsDir() {
		return fmt.Sprintf("%s is not a directory", rel), true, nil
	}
	maxEntries := args.MaxEntries
	if maxEntries <= 0 {
		maxEntries = 200
	}
	if maxEntries > 1000 {
		maxEntries = 1000
	}

	entries := make([]string, 0)
	if args.Recursive {
		walkErr := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if p == path {
				return nil
			}
			if len(entries) >= maxEntries {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			item, err := filepath.Rel(path, p)
			if err != nil {
				return err
			}
			if d.IsDir() {
				item += "/"
			}
			entries = append(entries, item)
			return nil
		})
		if walkErr != nil {
			return walkErr.Error(), true, nil
		}
	} else {
		dirEntries, err := os.ReadDir(path)
		if err != nil {
			return err.Error(), true, nil
		}
		for _, entry := range dirEntries {
			if len(entries) >= maxEntries {
				break
			}
			name := entry.Name()
			if entry.IsDir() {
				name += "/"
			}
			entries = append(entries, name)
		}
	}
	sort.Strings(entries)
	if len(entries) == 0 {
		return "(empty)", false, nil
	}
	return strings.Join(entries, "\n"), false, nil
}

func (t *BrowserToolset) readWorkspaceFile(args fileToolArgs) (string, bool, error) {
	path, _, err := t.workspacePath(args.Path, true)
	if err != nil {
		return err.Error(), true, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err.Error(), true, nil
	}
	if info.IsDir() {
		return "path is a directory", true, nil
	}
	maxBytes := args.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 100000
	}
	if maxBytes > 1000000 {
		maxBytes = 1000000
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err.Error(), true, nil
	}
	if len(data) > maxBytes {
		return string(data[:maxBytes]) + fmt.Sprintf("\n[truncated: %d bytes omitted]", len(data)-maxBytes), false, nil
	}
	return string(data), false, nil
}

func (t *BrowserToolset) writeWorkspaceFile(args fileToolArgs) (string, bool, error) {
	if strings.TrimSpace(args.Path) == "" {
		return "path is required", true, nil
	}
	path, rel, err := t.workspacePath(args.Path, false)
	if err != nil {
		return err.Error(), true, nil
	}
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			return "path is a directory", true, nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "refusing to write through a symlink", true, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err.Error(), true, nil
	}
	content := []byte(args.Content)
	if args.Append {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return err.Error(), true, nil
		}
		_, writeErr := f.Write(content)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr.Error(), true, nil
		}
		if closeErr != nil {
			return closeErr.Error(), true, nil
		}
		return fmt.Sprintf("appended %d bytes to %s", len(content), rel), false, nil
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		return err.Error(), true, nil
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(content), rel), false, nil
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
