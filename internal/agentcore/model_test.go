package agentcore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sseServer returns a test server that streams the given raw SSE lines.
func sseServer(t *testing.T, lines []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing/wrong auth header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range lines {
			_, _ = w.Write([]byte(l + "\n"))
		}
	}))
}

func TestStreamAssemblesTextAndDeltas(t *testing.T) {
	srv := sseServer(t, []string{
		`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
		`data: {"choices":[{"delta":{"content":", world"}}]}`,
		`data: {"choices":[{"delta":{"content":"!"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	})
	defer srv.Close()

	c := NewModelClient(srv.URL, "test-key")
	var streamed strings.Builder
	comp, err := c.Stream(context.Background(), "test-model", []ChatMessage{{Role: "user", Content: "hi"}}, nil, func(d string) {
		streamed.WriteString(d)
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if comp.Message.Content != "Hello, world!" {
		t.Errorf("content = %q, want %q", comp.Message.Content, "Hello, world!")
	}
	if streamed.String() != "Hello, world!" {
		t.Errorf("streamed deltas = %q, want %q", streamed.String(), "Hello, world!")
	}
	if comp.FinishReason != "stop" {
		t.Errorf("finish = %q, want stop", comp.FinishReason)
	}
	if comp.HasToolCalls() {
		t.Error("did not expect tool calls")
	}
}

func TestStreamReassemblesToolCallsAcrossChunks(t *testing.T) {
	srv := sseServer(t, []string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"vulpine_navigate"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"url\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"https://x.com\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	})
	defer srv.Close()

	c := NewModelClient(srv.URL, "test-key")
	comp, err := c.Stream(context.Background(), "m", []ChatMessage{{Role: "user", Content: "go"}}, nil, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !comp.HasToolCalls() {
		t.Fatal("expected tool calls")
	}
	if len(comp.Message.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(comp.Message.ToolCalls))
	}
	tc := comp.Message.ToolCalls[0]
	if tc.ID != "call_1" || tc.Function.Name != "vulpine_navigate" {
		t.Errorf("tool call id/name = %q/%q", tc.ID, tc.Function.Name)
	}
	if tc.Function.Arguments != `{"url":"https://x.com"}` {
		t.Errorf("args = %q, want %q", tc.Function.Arguments, `{"url":"https://x.com"}`)
	}
}

func TestStreamReturnsAPIErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer srv.Close()

	c := NewModelClient(srv.URL, "test-key")
	_, err := c.Stream(context.Background(), "m", []ChatMessage{{Role: "user", Content: "x"}}, nil, nil)
	if err == nil {
		t.Fatal("expected error on 429")
	}
	if !IsRateLimited(err) {
		t.Errorf("IsRateLimited(%v) = false, want true", err)
	}
}

func TestBaseURLForProvider(t *testing.T) {
	if got := BaseURLForProvider("openrouter"); got != OpenRouterBaseURL {
		t.Errorf("openrouter base = %q", got)
	}
	if got := BaseURLForProvider("totally-unknown"); got != OpenRouterBaseURL {
		t.Errorf("unknown provider should default to OpenRouter, got %q", got)
	}
	if got := BaseURLForProvider("openai"); got != "https://api.openai.com/v1" {
		t.Errorf("openai base = %q", got)
	}
}
