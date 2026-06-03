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
)

// anthropic.go is the native Anthropic Messages API client. It implements
// Completer by translating the OpenAI-compatible ChatMessage/ToolDef shapes the
// loop uses into Anthropic's /v1/messages request (system string, content-block
// messages, tool_use / tool_result blocks) and streaming response, then back
// into a Completion with reconstructed tool calls and token usage.

// AnthropicBaseURL is the default Anthropic API base (the segment before
// "/messages").
const AnthropicBaseURL = "https://api.anthropic.com/v1"

// anthropicVersion is the required api version header value.
const anthropicVersion = "2023-06-01"

// anthropicMaxTokens bounds the model's output. Anthropic requires max_tokens;
// this is generous enough for browser-agent replies + tool calls.
const anthropicMaxTokens = 8192

// AnthropicClient calls the Anthropic Messages API.
type AnthropicClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewAnthropicClient builds a client for the given base URL + API key. An empty
// baseURL defaults to the public Anthropic API. No overall HTTP timeout: streamed
// turns may run for minutes; callers cancel via context.
func NewAnthropicClient(baseURL, apiKey string) *AnthropicClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = AnthropicBaseURL
	}
	return &AnthropicClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:     strings.TrimSpace(apiKey),
		httpClient: &http.Client{},
	}
}

type anthropicBlock struct {
	Type string `json:"type"`
	// text
	Text string `json:"text,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

type anthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

type anthropicRequest struct {
	Model      string             `json:"model"`
	MaxTokens  int                `json:"max_tokens"`
	System     string             `json:"system,omitempty"`
	Messages   []anthropicMessage `json:"messages"`
	Tools      []anthropicTool    `json:"tools,omitempty"`
	ToolChoice map[string]string  `json:"tool_choice,omitempty"`
	Stream     bool               `json:"stream"`
}

// Stream sends one Messages API request and assembles the streamed reply.
func (c *AnthropicClient) Stream(ctx context.Context, model string, messages []ChatMessage, tools []ToolDef, onTextDelta func(string)) (Completion, error) {
	system, msgs := toAnthropicMessages(messages)
	reqBody := anthropicRequest{
		Model:     model,
		MaxTokens: anthropicMaxTokens,
		System:    system,
		Messages:  msgs,
		Tools:     toAnthropicTools(tools),
		Stream:    true,
	}
	if len(reqBody.Tools) > 0 {
		reqBody.ToolChoice = map[string]string{"type": "auto"}
	}
	encoded, err := json.Marshal(reqBody)
	if err != nil {
		return Completion{}, fmt.Errorf("marshal anthropic request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/messages", bytes.NewReader(encoded))
	if err != nil {
		return Completion{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Completion{}, fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return Completion{}, &APIError{Status: resp.StatusCode, Body: string(body)}
	}
	return parseAnthropicStream(resp.Body, onTextDelta)
}

// toAnthropicMessages splits system content out and converts the remaining
// messages into Anthropic content-block messages, coalescing consecutive
// same-role turns (notably multiple tool results) into a single message so the
// API's strict user/assistant alternation holds.
func toAnthropicMessages(messages []ChatMessage) (string, []anthropicMessage) {
	var system strings.Builder
	var out []anthropicMessage
	appendBlock := func(role string, b anthropicBlock) {
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content, b)
			return
		}
		out = append(out, anthropicMessage{Role: role, Content: []anthropicBlock{b}})
	}
	for _, m := range messages {
		switch m.Role {
		case "system":
			if strings.TrimSpace(m.Content) != "" {
				if system.Len() > 0 {
					system.WriteString("\n\n")
				}
				system.WriteString(m.Content)
			}
		case "assistant":
			if strings.TrimSpace(m.Content) != "" {
				appendBlock("assistant", anthropicBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				input := json.RawMessage(strings.TrimSpace(tc.Function.Arguments))
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				appendBlock("assistant", anthropicBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: input,
				})
			}
		case "tool":
			appendBlock("user", anthropicBlock{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			})
		default: // user (and any unknown role) -> user text
			appendBlock("user", anthropicBlock{Type: "text", Text: m.Content})
		}
	}
	return system.String(), out
}

func toAnthropicTools(tools []ToolDef) []anthropicTool {
	out := make([]anthropicTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, anthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}
	return out
}

// parseAnthropicStream consumes the Messages API SSE stream and reconstructs the
// assistant message (text + tool_use calls), finish reason, and usage.
func parseAnthropicStream(body io.Reader, onTextDelta func(string)) (Completion, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var content strings.Builder
	// tool_use blocks keyed by content-block index.
	type toolBlock struct {
		id   string
		name string
		args strings.Builder
	}
	blocks := map[int]*toolBlock{}
	var order []int
	stopReason := ""
	var usage Usage

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
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message struct {
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "message_start":
			usage.PromptTokens = ev.Message.Usage.InputTokens
		case "content_block_start":
			if ev.ContentBlock.Type == "tool_use" {
				tb := &toolBlock{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
				blocks[ev.Index] = tb
				order = append(order, ev.Index)
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" {
					content.WriteString(ev.Delta.Text)
					if onTextDelta != nil {
						onTextDelta(ev.Delta.Text)
					}
				}
			case "input_json_delta":
				if tb := blocks[ev.Index]; tb != nil {
					tb.args.WriteString(ev.Delta.PartialJSON)
				}
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				stopReason = ev.Delta.StopReason
			}
			if ev.Usage.OutputTokens > 0 {
				usage.CompletionTokens = ev.Usage.OutputTokens
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Completion{}, fmt.Errorf("read anthropic stream: %w", err)
	}

	msg := ChatMessage{Role: "assistant", Content: content.String()}
	for _, idx := range order {
		tb := blocks[idx]
		if tb == nil || tb.id == "" || tb.name == "" {
			continue
		}
		args := tb.args.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{
			ID:       tb.id,
			Type:     "function",
			Function: FunctionCall{Name: tb.name, Arguments: args},
		})
	}

	finishReason := "stop"
	if stopReason == "tool_use" || len(msg.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return Completion{Message: msg, FinishReason: finishReason, Usage: usage}, nil
}
