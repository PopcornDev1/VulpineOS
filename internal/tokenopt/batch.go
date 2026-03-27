package tokenopt

import (
	"encoding/json"
	"fmt"

	"vulpineos/internal/juggler"
)

// BatchAction describes a single browser action in a batch.
type BatchAction struct {
	Tool   string                 `json:"tool"`   // vulpine_navigate, vulpine_click, etc.
	Params map[string]interface{} `json:"params"` // tool-specific parameters
}

// BatchStepResult holds the result of a single action in the batch.
type BatchStepResult struct {
	Tool    string `json:"tool"`
	Success bool   `json:"success"`
	Data    string `json:"data,omitempty"`
}

// BatchResult holds the collected results of a batch execution.
type BatchResult struct {
	Results []BatchStepResult `json:"results"`
	Errors  []string          `json:"errors,omitempty"`
}

// BatchExecutor runs sequential browser actions as a single batch,
// reducing round-trips and token overhead from multiple MCP calls.
type BatchExecutor struct {
	client *juggler.Client
}

// NewBatchExecutor creates a batch executor backed by the given Juggler client.
func NewBatchExecutor(client *juggler.Client) *BatchExecutor {
	return &BatchExecutor{client: client}
}

// Execute runs actions sequentially, collecting results.
// If any action fails, execution stops and partial results are returned.
func (b *BatchExecutor) Execute(sessionID string, actions []BatchAction) *BatchResult {
	result := &BatchResult{
		Results: make([]BatchStepResult, 0, len(actions)),
	}

	for _, action := range actions {
		stepResult := b.executeOne(sessionID, action)
		result.Results = append(result.Results, stepResult)

		if !stepResult.Success {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %s", action.Tool, stepResult.Data))
			break // stop on first failure
		}
	}

	return result
}

// executeOne dispatches a single action to the Juggler protocol.
func (b *BatchExecutor) executeOne(sessionID string, action BatchAction) BatchStepResult {
	switch action.Tool {
	case "vulpine_navigate":
		return b.execNavigate(sessionID, action.Params)
	case "vulpine_click":
		return b.execClick(sessionID, action.Params)
	case "vulpine_type":
		return b.execType(sessionID, action.Params)
	case "vulpine_scroll":
		return b.execScroll(sessionID, action.Params)
	case "vulpine_snapshot":
		return b.execSnapshot(sessionID, action.Params)
	case "vulpine_screenshot":
		return b.execScreenshot(sessionID, action.Params)
	default:
		return BatchStepResult{
			Tool:    action.Tool,
			Success: false,
			Data:    fmt.Sprintf("unknown tool: %s", action.Tool),
		}
	}
}

func (b *BatchExecutor) execNavigate(sessionID string, params map[string]interface{}) BatchStepResult {
	url, _ := params["url"].(string)
	frameID, _ := params["frameId"].(string)

	callParams := map[string]interface{}{"url": url}
	if frameID != "" {
		callParams["frameId"] = frameID
	}

	_, err := b.client.Call(sessionID, "Page.navigate", callParams)
	if err != nil {
		return BatchStepResult{Tool: "vulpine_navigate", Success: false, Data: err.Error()}
	}
	return BatchStepResult{Tool: "vulpine_navigate", Success: true, Data: fmt.Sprintf("Navigated to %s", url)}
}

func (b *BatchExecutor) execClick(sessionID string, params map[string]interface{}) BatchStepResult {
	x, _ := params["x"].(float64)
	y, _ := params["y"].(float64)

	_, err := b.client.Call(sessionID, "Page.dispatchMouseEvent", map[string]interface{}{
		"type": "mousedown", "x": x, "y": y,
		"button": 0, "clickCount": 1, "modifiers": 0, "buttons": 1,
	})
	if err != nil {
		return BatchStepResult{Tool: "vulpine_click", Success: false, Data: err.Error()}
	}

	_, err = b.client.Call(sessionID, "Page.dispatchMouseEvent", map[string]interface{}{
		"type": "mouseup", "x": x, "y": y,
		"button": 0, "clickCount": 1, "modifiers": 0, "buttons": 0,
	})
	if err != nil {
		return BatchStepResult{Tool: "vulpine_click", Success: false, Data: err.Error()}
	}

	return BatchStepResult{Tool: "vulpine_click", Success: true, Data: fmt.Sprintf("Clicked at (%v, %v)", x, y)}
}

func (b *BatchExecutor) execType(sessionID string, params map[string]interface{}) BatchStepResult {
	text, _ := params["text"].(string)

	_, err := b.client.Call(sessionID, "Page.insertText", map[string]interface{}{
		"text": text,
	})
	if err != nil {
		return BatchStepResult{Tool: "vulpine_type", Success: false, Data: err.Error()}
	}

	return BatchStepResult{Tool: "vulpine_type", Success: true, Data: fmt.Sprintf("Typed %d characters", len(text))}
}

func (b *BatchExecutor) execScroll(sessionID string, params map[string]interface{}) BatchStepResult {
	deltaY, _ := params["deltaY"].(float64)

	_, err := b.client.Call(sessionID, "Page.dispatchWheelEvent", map[string]interface{}{
		"x": 400, "y": 300,
		"deltaX": 0, "deltaY": deltaY, "deltaZ": 0,
		"modifiers": 0,
	})
	if err != nil {
		return BatchStepResult{Tool: "vulpine_scroll", Success: false, Data: err.Error()}
	}

	return BatchStepResult{Tool: "vulpine_scroll", Success: true, Data: fmt.Sprintf("Scrolled by %v pixels", deltaY)}
}

func (b *BatchExecutor) execSnapshot(sessionID string, params map[string]interface{}) BatchStepResult {
	callParams := map[string]interface{}{}
	if v, ok := params["maxDepth"]; ok {
		callParams["maxDepth"] = v
	}
	if v, ok := params["maxNodes"]; ok {
		callParams["maxNodes"] = v
	}

	result, err := b.client.Call(sessionID, "Page.getOptimizedDOM", callParams)
	if err != nil {
		return BatchStepResult{Tool: "vulpine_snapshot", Success: false, Data: err.Error()}
	}

	return BatchStepResult{Tool: "vulpine_snapshot", Success: true, Data: string(result)}
}

func (b *BatchExecutor) execScreenshot(sessionID string, _ map[string]interface{}) BatchStepResult {
	result, err := b.client.Call(sessionID, "Page.screenshot", map[string]interface{}{
		"mimeType": "image/png",
		"clip":     map[string]interface{}{"x": 0, "y": 0, "width": 1280, "height": 720},
	})
	if err != nil {
		return BatchStepResult{Tool: "vulpine_screenshot", Success: false, Data: err.Error()}
	}

	var screenshot struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(result, &screenshot); err != nil {
		return BatchStepResult{Tool: "vulpine_screenshot", Success: false, Data: err.Error()}
	}

	return BatchStepResult{Tool: "vulpine_screenshot", Success: true, Data: screenshot.Data}
}
