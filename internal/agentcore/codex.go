package agentcore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"vulpineos/internal/auth"
)

// codex.go is the native ChatGPT/Codex client for the "openai-oauth" provider.
// It calls the Codex Responses API at chatgpt.com with the OAuth access token
// (refreshed via internal/auth), translating the loop's ChatMessage/ToolDef
// shapes into Responses "input" items and back into a Completion. It mirrors the
// request/response shape NanoClaw's codex provider used.

// codexAPI is the Codex Responses endpoint.
const codexAPI = "https://chatgpt.com/backend-api/codex/responses"

// CodexClient calls the Codex Responses API using an OAuth access token.
type CodexClient struct {
	httpClient *http.Client
	// tokenFn returns a valid (refreshed) access token. Overridable in tests.
	tokenFn func() (string, error)
}

// NewCodexClient builds a Codex client. The access token is resolved per request
// via internal/auth so refreshes are picked up.
func NewCodexClient() *CodexClient {
	return &CodexClient{httpClient: &http.Client{}, tokenFn: auth.ValidAccessToken}
}

// normalizeCodexModel maps a configured model to a Codex API model name. Codex
// uses bare names (gpt-5.5, gpt-5.4) and defaults to gpt-5.5 for OAuth.
func normalizeCodexModel(model string) string {
	raw := strings.TrimPrefix(strings.TrimSpace(model), "openai/")
	switch raw {
	case "gpt-5.5", "gpt-5.4":
		return raw
	}
	return "gpt-5.5"
}

type codexInputItem struct {
	Type string `json:"type"`
	// message
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
	Status  string `json:"status,omitempty"`
	// function_call
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	// function_call_output
	Output string `json:"output,omitempty"`
}

type codexTool struct {
	Type        string                 `json:"type"` // "function"
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type codexRequest struct {
	Model        string           `json:"model"`
	Instructions string           `json:"instructions,omitempty"`
	Input        []codexInputItem `json:"input"`
	Tools        []codexTool      `json:"tools,omitempty"`
	ToolChoice   string           `json:"tool_choice,omitempty"`
	Store        bool             `json:"store"`
	Stream       bool             `json:"stream"`
}

// Stream sends one Responses API request and assembles the streamed reply.
func (c *CodexClient) Stream(ctx context.Context, model string, messages []ChatMessage, tools []ToolDef, onTextDelta func(string)) (Completion, error) {
	token, err := c.tokenFn()
	if err != nil {
		return Completion{}, fmt.Errorf("codex auth: %w (run: vulpineos auth login --provider openai)", err)
	}

	instructions, input := toCodexInput(messages)
	reqBody := codexRequest{
		Model:        normalizeCodexModel(model),
		Instructions: instructions,
		Input:        input,
		Tools:        toCodexTools(tools),
		Store:        false,
		Stream:       true,
	}
	if len(reqBody.Tools) > 0 {
		reqBody.ToolChoice = "auto"
	}
	encoded, err := json.Marshal(reqBody)
	if err != nil {
		return Completion{}, fmt.Errorf("marshal codex request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexAPI, bytes.NewReader(encoded))
	if err != nil {
		return Completion{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OpenAI-Beta", "responses=v1")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Completion{}, fmt.Errorf("codex request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return Completion{}, &APIError{Status: resp.StatusCode, Body: string(body)}
	}
	return parseCodexStream(resp.Body, onTextDelta)
}

// toCodexInput splits system content into instructions and converts the rest
// into Responses input items (messages, function_call, function_call_output).
func toCodexInput(messages []ChatMessage) (string, []codexInputItem) {
	var instructions strings.Builder
	var input []codexInputItem
	for _, m := range messages {
		switch m.Role {
		case "system":
			if strings.TrimSpace(m.Content) != "" {
				if instructions.Len() > 0 {
					instructions.WriteString("\n\n")
				}
				instructions.WriteString(m.Content)
			}
		case "assistant":
			if strings.TrimSpace(m.Content) != "" {
				input = append(input, codexInputItem{Type: "message", Role: "assistant", Content: m.Content, Status: "completed"})
			}
			for _, tc := range m.ToolCalls {
				args := tc.Function.Arguments
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				input = append(input, codexInputItem{Type: "function_call", CallID: tc.ID, Name: tc.Function.Name, Arguments: args})
			}
		case "tool":
			input = append(input, codexInputItem{Type: "function_call_output", CallID: m.ToolCallID, Output: m.Content})
		default: // user
			input = append(input, codexInputItem{Type: "message", Role: "user", Content: m.Content})
		}
	}
	return instructions.String(), input
}

func toCodexTools(tools []ToolDef) []codexTool {
	out := make([]codexTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, codexTool{
			Type:        "function",
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}
	return out
}

// parseCodexStream consumes the Responses API SSE stream and reconstructs the
// assistant message (text + function calls), finish reason, and usage.
func parseCodexStream(body io.Reader, onTextDelta func(string)) (Completion, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var content strings.Builder
	type fnCall struct {
		id   string
		name string
		args strings.Builder
	}
	calls := map[int]*fnCall{}
	var order []int
	var usage Usage

	get := func(idx int) *fnCall {
		fc, ok := calls[idx]
		if !ok {
			fc = &fnCall{}
			calls[idx] = fc
			order = append(order, idx)
		}
		return fc
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var ev struct {
			Type        string `json:"type"`
			OutputIndex int    `json:"output_index"`
			Delta       string `json:"delta"`
			CallID      string `json:"call_id"`
			Name        string `json:"name"`
			Item        struct {
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"item"`
			Response struct {
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
					TotalTokens  int `json:"total_tokens"`
				} `json:"usage"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "response.output_text.delta":
			if ev.Delta != "" {
				content.WriteString(ev.Delta)
				if onTextDelta != nil {
					onTextDelta(ev.Delta)
				}
			}
		case "response.output_item.added":
			if ev.Item.Type == "function_call" {
				fc := get(ev.OutputIndex)
				if ev.Item.CallID != "" {
					fc.id = ev.Item.CallID
				}
				if ev.Item.Name != "" {
					fc.name = ev.Item.Name
				}
				if ev.Item.Arguments != "" {
					fc.args.WriteString(ev.Item.Arguments)
				}
			}
		case "response.function_call_arguments.delta":
			fc := get(ev.OutputIndex)
			if fc.id == "" && ev.CallID != "" {
				fc.id = ev.CallID
			}
			if fc.name == "" && ev.Name != "" {
				fc.name = ev.Name
			}
			fc.args.WriteString(ev.Delta)
		case "response.completed", "response.incomplete", "response.done":
			if ev.Response.Usage.TotalTokens > 0 || ev.Response.Usage.InputTokens > 0 || ev.Response.Usage.OutputTokens > 0 {
				usage = Usage{
					PromptTokens:     ev.Response.Usage.InputTokens,
					CompletionTokens: ev.Response.Usage.OutputTokens,
					TotalTokens:      ev.Response.Usage.TotalTokens,
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Completion{}, fmt.Errorf("read codex stream: %w", err)
	}

	msg := ChatMessage{Role: "assistant", Content: content.String()}
	for _, idx := range order {
		fc := calls[idx]
		if fc == nil || fc.name == "" {
			continue
		}
		id := fc.id
		if id == "" {
			id = fmt.Sprintf("call_%d", idx)
		}
		args := fc.args.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{
			ID:       id,
			Type:     "function",
			Function: FunctionCall{Name: fc.name, Arguments: args},
		})
	}

	finishReason := "stop"
	if len(msg.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return Completion{Message: msg, FinishReason: finishReason, Usage: usage}, nil
}
