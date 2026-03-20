package mcp

import (
	"encoding/json"
	"fmt"

	"vulpineos/internal/juggler"
)

// tools returns the list of VulpineOS browser tools available via MCP.
func tools() []ToolDefinition {
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
			Description: "Get a token-optimized semantic snapshot of the page content for LLM processing. Returns compressed DOM with >50% fewer tokens than raw HTML.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId":     {Type: "string", Description: "Target page session ID"},
					"maxDepth":      {Type: "number", Description: "Max tree depth (default 10)"},
					"maxNodes":      {Type: "number", Description: "Max nodes to return (default 500)"},
					"maxTextLength": {Type: "number", Description: "Max text per node (default 200)"},
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
	}
}

// handleToolCall dispatches a tool call to the appropriate handler.
func handleToolCall(client *juggler.Client, name string, args json.RawMessage) (*ToolCallResult, error) {
	switch name {
	case "vulpine_navigate":
		return handleNavigate(client, args)
	case "vulpine_snapshot":
		return handleSnapshot(client, args)
	case "vulpine_click":
		return handleClick(client, args)
	case "vulpine_type":
		return handleType(client, args)
	case "vulpine_screenshot":
		return handleScreenshot(client, args)
	case "vulpine_scroll":
		return handleScroll(client, args)
	case "vulpine_new_context":
		return handleNewContext(client, args)
	case "vulpine_close_context":
		return handleCloseContext(client, args)
	case "vulpine_get_ax_tree":
		return handleGetAXTree(client, args)
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

func handleNavigate(client *juggler.Client, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		URL       string `json:"url"`
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	// Use Runtime.evaluate to navigate — avoids needing the frameId
	_, err := client.Call(p.SessionID, "Runtime.evaluate", map[string]interface{}{
		"expression":    fmt.Sprintf("window.location.href = %q", p.URL),
		"returnByValue": true,
	})
	if err != nil {
		return errorResult(err), nil
	}

	return textResult(fmt.Sprintf("Navigated to %s", p.URL)), nil
}

func handleSnapshot(client *juggler.Client, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID     string `json:"sessionId"`
		MaxDepth      int    `json:"maxDepth"`
		MaxNodes      int    `json:"maxNodes"`
		MaxTextLength int    `json:"maxTextLength"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	params := map[string]interface{}{}
	if p.MaxDepth > 0 {
		params["maxDepth"] = p.MaxDepth
	}
	if p.MaxNodes > 0 {
		params["maxNodes"] = p.MaxNodes
	}
	if p.MaxTextLength > 0 {
		params["maxTextLength"] = p.MaxTextLength
	}

	result, err := client.Call(p.SessionID, "Page.getOptimizedDOM", params)
	if err != nil {
		return errorResult(err), nil
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

func handleScroll(client *juggler.Client, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string  `json:"sessionId"`
		DeltaY    float64 `json:"deltaY"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	_, err := client.Call(p.SessionID, "Page.dispatchWheelEvent", map[string]interface{}{
		"x": 400, "y": 300,
		"deltaX": 0, "deltaY": p.DeltaY, "deltaZ": 0,
		"modifiers": 0,
	})
	if err != nil {
		return errorResult(err), nil
	}

	return textResult(fmt.Sprintf("Scrolled by %v pixels", p.DeltaY)), nil
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

	// Create page in context
	pageResult, err := client.Call("", "Browser.newPage", map[string]interface{}{
		"browserContextId": ctx.BrowserContextID,
	})
	if err != nil {
		return errorResult(err), nil
	}

	var page struct {
		TargetID string `json:"targetId"`
	}
	json.Unmarshal(pageResult, &page)

	return textResult(fmt.Sprintf(`{"contextId":"%s","targetId":"%s"}`, ctx.BrowserContextID, page.TargetID)), nil
}

func handleCloseContext(client *juggler.Client, args json.RawMessage) (*ToolCallResult, error) {
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
