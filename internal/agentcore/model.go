// Package agentcore implements VulpineOS's native, in-process agent runtime:
// a streaming model loop that drives the host Camoufox browser through the
// existing MCP/Juggler tools. It replaces the per-agent NanoClaw Docker
// container for VulpineOS's narrow use case (one task -> one agent -> LLM +
// browser-over-CDP -> reply), eliminating the container cold-start, the agent
// image build, the daemon/gateway processes, and the fragile source-patch glue.
//
// model.go is the provider layer: an OpenAI-compatible chat-completions client
// with SSE streaming and tool-calling. It is provider-agnostic (OpenRouter,
// OpenAI, Groq, Together, etc. all speak this shape); Anthropic's native API is
// handled separately where needed.
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

// ChatMessage is one message in the OpenAI-compatible chat format.
type ChatMessage struct {
	Role       string     `json:"role"` // system | user | assistant | tool
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is a function call requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // always "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall is the name + JSON-encoded arguments of a tool call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef describes a tool exposed to the model.
type ToolDef struct {
	Type     string      `json:"type"` // "function"
	Function FunctionDef `json:"function"`
}

// FunctionDef is a tool's name, description, and JSON-schema parameters.
type FunctionDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// Completion is the result of one model turn.
type Completion struct {
	Message      ChatMessage // assistant message (content and/or tool_calls)
	FinishReason string      // "stop" | "tool_calls" | "length" | ...
}

// HasToolCalls reports whether the model asked to invoke tools.
func (c Completion) HasToolCalls() bool {
	return c.FinishReason == "tool_calls" || len(c.Message.ToolCalls) > 0
}

// APIError carries the HTTP status of a non-2xx provider response so callers
// can decide whether to fall back to another model (e.g. on 429).
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("provider returned %d: %s", e.Status, truncate(e.Body, 300))
}

// ModelClient calls an OpenAI-compatible chat-completions endpoint.
type ModelClient struct {
	baseURL    string // e.g. https://openrouter.ai/api/v1
	apiKey     string
	httpClient *http.Client
	referer    string
	title      string
}

// NewModelClient builds a client for the given base URL + API key. An empty
// baseURL defaults to OpenRouter. The HTTP client has no overall timeout
// because streamed turns can legitimately run for minutes; callers cancel via
// the context instead.
func NewModelClient(baseURL, apiKey string) *ModelClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = OpenRouterBaseURL
	}
	return &ModelClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:     strings.TrimSpace(apiKey),
		httpClient: &http.Client{},
		referer:    "https://vulpineos.com",
		title:      "VulpineOS",
	}
}

// OpenRouterBaseURL is the default provider endpoint.
const OpenRouterBaseURL = "https://openrouter.ai/api/v1"

// providerBaseURLs maps a VulpineOS provider ID to its OpenAI-compatible base
// URL. Providers not listed fall back to OpenRouter (the common multiplexer).
var providerBaseURLs = map[string]string{
	"openrouter": OpenRouterBaseURL,
	"openai":     "https://api.openai.com/v1",
	"groq":       "https://api.groq.com/openai/v1",
	"together":   "https://api.together.xyz/v1",
	"cerebras":   "https://api.cerebras.ai/v1",
	"mistral":    "https://api.mistral.ai/v1",
	"xai":        "https://api.x.ai/v1",
}

// BaseURLForProvider returns the chat-completions base URL for a provider ID,
// defaulting to OpenRouter.
func BaseURLForProvider(providerID string) string {
	if u, ok := providerBaseURLs[strings.ToLower(strings.TrimSpace(providerID))]; ok {
		return u
	}
	return OpenRouterBaseURL
}

type chatRequest struct {
	Model      string        `json:"model"`
	Messages   []ChatMessage `json:"messages"`
	Tools      []ToolDef     `json:"tools,omitempty"`
	ToolChoice string        `json:"tool_choice,omitempty"`
	Stream     bool          `json:"stream"`
}

// Stream sends one chat-completions request with streaming enabled. Text deltas
// are delivered to onTextDelta as they arrive (may be nil). It returns the fully
// assembled assistant message (content + any reconstructed tool calls) and the
// finish reason. Context cancellation aborts the request.
func (c *ModelClient) Stream(ctx context.Context, model string, messages []ChatMessage, tools []ToolDef, onTextDelta func(string)) (Completion, error) {
	reqBody := chatRequest{Model: model, Messages: messages, Tools: tools, Stream: true}
	if len(tools) > 0 {
		reqBody.ToolChoice = "auto"
	}
	encoded, err := json.Marshal(reqBody)
	if err != nil {
		return Completion{}, fmt.Errorf("marshal chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(encoded))
	if err != nil {
		return Completion{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.referer != "" {
		req.Header.Set("HTTP-Referer", c.referer)
	}
	if c.title != "" {
		req.Header.Set("X-Title", c.title)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Completion{}, fmt.Errorf("chat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return Completion{}, &APIError{Status: resp.StatusCode, Body: string(body)}
	}

	return parseSSEStream(resp.Body, onTextDelta)
}

// toolCallAccumulator reassembles a streamed tool call from its delta fragments.
type toolCallAccumulator struct {
	id   string
	name string
	args strings.Builder
}

// parseSSEStream consumes an OpenAI-style streamed completion: lines of
// "data: {json}" terminated by "data: [DONE]". It accumulates text content and
// tool-call fragments (keyed by index) into a final assistant message.
func parseSSEStream(body io.Reader, onTextDelta func(string)) (Completion, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var content strings.Builder
	toolAccum := map[int]*toolCallAccumulator{}
	var toolOrder []int
	finishReason := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// Skip malformed/keepalive lines rather than aborting the turn.
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.Delta.Content != "" {
			content.WriteString(choice.Delta.Content)
			if onTextDelta != nil {
				onTextDelta(choice.Delta.Content)
			}
		}
		for _, tc := range choice.Delta.ToolCalls {
			acc, ok := toolAccum[tc.Index]
			if !ok {
				acc = &toolCallAccumulator{}
				toolAccum[tc.Index] = acc
				toolOrder = append(toolOrder, tc.Index)
			}
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function.Name != "" {
				acc.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				acc.args.WriteString(tc.Function.Arguments)
			}
		}
		if choice.FinishReason != "" {
			finishReason = choice.FinishReason
		}
	}
	if err := scanner.Err(); err != nil {
		return Completion{}, fmt.Errorf("read stream: %w", err)
	}

	msg := ChatMessage{Role: "assistant", Content: content.String()}
	for _, idx := range toolOrder {
		acc := toolAccum[idx]
		if acc.id == "" || acc.name == "" {
			continue
		}
		args := acc.args.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{
			ID:       acc.id,
			Type:     "function",
			Function: FunctionCall{Name: acc.name, Arguments: args},
		})
	}
	if finishReason == "" {
		if len(msg.ToolCalls) > 0 {
			finishReason = "tool_calls"
		} else {
			finishReason = "stop"
		}
	}
	return Completion{Message: msg, FinishReason: finishReason}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// IsRateLimited reports whether err is a provider 429 (caller may fall back to
// another model).
func IsRateLimited(err error) bool {
	var apiErr *APIError
	if ok := asAPIError(err, &apiErr); ok {
		return apiErr.Status == http.StatusTooManyRequests
	}
	return false
}

func asAPIError(err error, target **APIError) bool {
	for err != nil {
		if e, ok := err.(*APIError); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := err.(unwrapper); ok {
			err = u.Unwrap()
		} else {
			break
		}
	}
	return false
}
