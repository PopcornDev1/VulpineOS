package agentcore

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// scriptedCompleter returns queued completions in order; records the messages
// it was called with on each turn.
type scriptedCompleter struct {
	turns    []Completion
	errs     []error // optional, parallel to turns
	calls    int
	lastMsgs [][]ChatMessage
	models   []string
}

func (s *scriptedCompleter) Stream(_ context.Context, model string, msgs []ChatMessage, _ []ToolDef, onDelta func(string)) (Completion, error) {
	i := s.calls
	s.calls++
	s.models = append(s.models, model)
	cp := append([]ChatMessage(nil), msgs...)
	s.lastMsgs = append(s.lastMsgs, cp)
	if s.errs != nil && i < len(s.errs) && s.errs[i] != nil {
		return Completion{}, s.errs[i]
	}
	if i >= len(s.turns) {
		return Completion{Message: ChatMessage{Role: "assistant", Content: "(no more script)"}, FinishReason: "stop"}, nil
	}
	if onDelta != nil && s.turns[i].Message.Content != "" {
		onDelta(s.turns[i].Message.Content)
	}
	return s.turns[i], nil
}

type fakeDispatcher struct {
	calls   []string
	results map[string]string
}

func (f *fakeDispatcher) Dispatch(_ context.Context, name, _ string) (string, bool, error) {
	f.calls = append(f.calls, name)
	if r, ok := f.results[name]; ok {
		return r, false, nil
	}
	return "ok", false, nil
}

type recordEvents struct {
	deltas    []string
	assistant []string
	toolCalls []string
	toolRes   []string
	statuses  []string
}

func (r *recordEvents) OnTextDelta(d string)               { r.deltas = append(r.deltas, d) }
func (r *recordEvents) OnAssistant(t string)               { r.assistant = append(r.assistant, t) }
func (r *recordEvents) OnToolCall(n, a string)             { r.toolCalls = append(r.toolCalls, n) }
func (r *recordEvents) OnToolResult(n, res string, e bool) { r.toolRes = append(r.toolRes, n) }
func (r *recordEvents) OnStatus(s string)                  { r.statuses = append(r.statuses, s) }
func (r *recordEvents) OnUsage(Usage)                      {}

func toolCallTurn(id, name, args string) Completion {
	return Completion{
		Message: ChatMessage{Role: "assistant", ToolCalls: []ToolCall{
			{ID: id, Type: "function", Function: FunctionCall{Name: name, Arguments: args}},
		}},
		FinishReason: "tool_calls",
	}
}

func TestLoopExecutesToolThenReturnsFinal(t *testing.T) {
	model := &scriptedCompleter{turns: []Completion{
		toolCallTurn("c1", "vulpine_navigate", `{"url":"https://proton.me"}`),
		{Message: ChatMessage{Role: "assistant", Content: "EMAIL_SENT"}, FinishReason: "stop"},
	}}
	disp := &fakeDispatcher{results: map[string]string{"vulpine_navigate": "navigated"}}
	ev := &recordEvents{}
	loop := NewLoop(model, disp, ev, LoopConfig{Models: []string{"m1"}, SystemPrompt: "be brief"})

	final, err := loop.Run(context.Background(), "send the email", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if final != "EMAIL_SENT" {
		t.Errorf("final = %q, want EMAIL_SENT", final)
	}
	if len(disp.calls) != 1 || disp.calls[0] != "vulpine_navigate" {
		t.Errorf("dispatcher calls = %v, want [vulpine_navigate]", disp.calls)
	}
	// Second model turn must include: system, user, assistant(tool_calls), tool result.
	second := model.lastMsgs[1]
	if len(second) != 4 || second[0].Role != "system" || second[1].Role != "user" || second[2].Role != "assistant" || second[3].Role != "tool" {
		roles := make([]string, len(second))
		for i, m := range second {
			roles[i] = m.Role
		}
		t.Fatalf("second-turn message roles = %v, want [system user assistant tool]", roles)
	}
	if second[3].ToolCallID != "c1" || second[3].Content != "navigated" {
		t.Errorf("tool result message = %+v, want tool_call_id c1 content 'navigated'", second[3])
	}
	if len(ev.statuses) == 0 || ev.statuses[0] != "running" || ev.statuses[len(ev.statuses)-1] != "completed" {
		t.Errorf("status sequence = %v, want running..completed", ev.statuses)
	}
}

func TestLoopFallsBackToNextModelOn429(t *testing.T) {
	model := &scriptedCompleter{
		errs:  []error{&APIError{Status: http.StatusTooManyRequests, Body: "rl"}, nil},
		turns: []Completion{{}, {Message: ChatMessage{Role: "assistant", Content: "done"}, FinishReason: "stop"}},
	}
	loop := NewLoop(model, nil, nil, LoopConfig{Models: []string{"primary", "backup"}})
	final, err := loop.Run(context.Background(), "go", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if final != "done" {
		t.Errorf("final = %q, want done", final)
	}
	if len(model.models) != 2 || model.models[0] != "primary" || model.models[1] != "backup" {
		t.Errorf("model attempts = %v, want [primary backup]", model.models)
	}
}

func TestLoopReturnsNonRateLimitErrorImmediately(t *testing.T) {
	model := &scriptedCompleter{errs: []error{errors.New("boom")}, turns: []Completion{{}}}
	loop := NewLoop(model, nil, nil, LoopConfig{Models: []string{"a", "b"}})
	_, err := loop.Run(context.Background(), "go", nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected boom error, got %v", err)
	}
	if model.calls != 1 {
		t.Errorf("should not fall back on non-429; calls = %d, want 1", model.calls)
	}
}

func TestLoopStopsAtMaxIterations(t *testing.T) {
	// Always returns a tool call -> never finishes -> must hit the guard.
	model := &scriptedCompleter{}
	for i := 0; i < 10; i++ {
		model.turns = append(model.turns, toolCallTurn("c", "vulpine_wait", `{}`))
	}
	loop := NewLoop(model, &fakeDispatcher{}, nil, LoopConfig{Models: []string{"m"}, MaxIterations: 3})
	_, err := loop.Run(context.Background(), "loop forever", nil)
	if err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("expected max-iteration error, got %v", err)
	}
	if model.calls != 3 {
		t.Errorf("model called %d times, want 3 (MaxIterations)", model.calls)
	}
}
