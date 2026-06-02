package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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
	sort.Slice(out, func(i, j int) bool { return out[i].Function.Name < out[j].Function.Name })
	return out
}

// BrowserToolset dispatches model tool calls to the live browser via the MCP
// handlers, against a single fixed page session. It holds a persistent
// mcp.ToolExecutor so page execution contexts resolve across the agent's
// successive tool calls.
type BrowserToolset struct {
	executor  *mcp.ToolExecutor
	sessionID string
	loopDet   *mcp.LoopDetector
}

// NewBrowserToolset binds a toolset to a juggler client and the page session
// the agent operates on. Call Close when the agent session ends. It carries a
// loop detector so repeated identical, progress-less tool calls are nudged
// toward a different approach (parity with the MCP server path).
func NewBrowserToolset(client *juggler.Client, sessionID string) *BrowserToolset {
	return &BrowserToolset{executor: mcp.NewToolExecutor(client), sessionID: sessionID, loopDet: mcp.NewLoopDetector(3)}
}

// Close releases the toolset's persistent tracker subscriptions.
func (t *BrowserToolset) Close() {
	if t.executor != nil {
		t.executor.Close()
	}
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
	if !browserToolAllowList[name] {
		return "", false, fmt.Errorf("unknown or disallowed tool: %s", name)
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
		if warn := t.loopDet.Check(t.sessionID, name, trimmed); warn != "" {
			return warn, false, nil
		}
	}

	args[sessionIDArg] = t.sessionID

	encoded, err := json.Marshal(args)
	if err != nil {
		return "", false, fmt.Errorf("encode arguments for %s: %w", name, err)
	}

	res, err := t.executor.Call(ctx, name, encoded)
	if err != nil {
		return "", false, err
	}
	// Navigation moves to a new page; clear the loop history so legitimate
	// repeated actions on the new page aren't falsely flagged.
	if t.loopDet != nil && name == "vulpine_navigate" {
		t.loopDet.Reset(t.sessionID)
	}
	return contentText(res), res.IsError, nil
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
