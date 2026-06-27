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
	calls    []string
	callArgs []string
	results  map[string]string
	errors   map[string]string
}

func (f *fakeDispatcher) Dispatch(_ context.Context, name, rawArgs string) (string, bool, error) {
	f.calls = append(f.calls, name)
	f.callArgs = append(f.callArgs, rawArgs)
	if r, ok := f.errors[name]; ok {
		return r, true, nil
	}
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
	warnings  []string
	observed  []ObservationState
}

func (r *recordEvents) OnTextDelta(d string)               { r.deltas = append(r.deltas, d) }
func (r *recordEvents) OnAssistant(t string)               { r.assistant = append(r.assistant, t) }
func (r *recordEvents) OnToolCall(n, a string)             { r.toolCalls = append(r.toolCalls, n) }
func (r *recordEvents) OnToolResult(n, res string, e bool) { r.toolRes = append(r.toolRes, n) }
func (r *recordEvents) OnStatus(s string)                  { r.statuses = append(r.statuses, s) }
func (r *recordEvents) OnUsage(Usage)                      {}
func (r *recordEvents) OnWarning(w string)                 { r.warnings = append(r.warnings, w) }
func (r *recordEvents) OnPhase(string)                     {}
func (r *recordEvents) OnTurn(int)                         {}
func (r *recordEvents) OnObservationState(s ObservationState) {
	r.observed = append(r.observed, s)
}

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

// failDispatcher reports a tool-level failure for the named tool.
type failDispatcher struct{ failTool string }

func (f *failDispatcher) Dispatch(_ context.Context, name, _ string) (string, bool, error) {
	if name == f.failTool {
		return "element not found", true, nil
	}
	return "ok", false, nil
}

func TestLoopBlocksFinalReplyAfterFailedTool(t *testing.T) {
	// Tool fails, then the model replies as if it succeeded. The loop should not
	// emit the model's unverified final text.
	model := &scriptedCompleter{turns: []Completion{
		toolCallTurn("c1", "vulpine_click", `{"ref":"x"}`),
		{Message: ChatMessage{Role: "assistant", Content: "Done! Clicked it."}, FinishReason: "stop"},
	}}
	ev := &recordEvents{}
	loop := NewLoop(model, &failDispatcher{failTool: "vulpine_click"}, ev, LoopConfig{Models: []string{"m"}})
	final, err := loop.Run(context.Background(), "click it", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(final, "Clicked it") {
		t.Fatalf("final = %q, want unverified failure response instead of model success text", final)
	}
	if !strings.Contains(final, "could not verify") || !strings.Contains(final, "vulpine_click") {
		t.Fatalf("final = %q, want grounded failed-tool response mentioning failed tool", final)
	}
	if len(ev.warnings) != 1 || !strings.Contains(ev.warnings[0], "vulpine_click") {
		t.Fatalf("warnings = %v, want one mentioning vulpine_click", ev.warnings)
	}
	if len(ev.assistant) != 1 || ev.assistant[0] != final {
		t.Fatalf("assistant events = %#v, want only grounded final response %q", ev.assistant, final)
	}
}

func TestLoopNoWarningWhenToolRecovers(t *testing.T) {
	// Tool fails, then a later tool succeeds before the final reply -> no warning.
	model := &scriptedCompleter{turns: []Completion{
		toolCallTurn("c1", "vulpine_click", `{"ref":"x"}`),
		toolCallTurn("c2", "vulpine_find", `{"text":"x"}`),
		{Message: ChatMessage{Role: "assistant", Content: "Done."}, FinishReason: "stop"},
	}}
	ev := &recordEvents{}
	loop := NewLoop(model, &failDispatcher{failTool: "vulpine_click"}, ev, LoopConfig{Models: []string{"m"}})
	if _, err := loop.Run(context.Background(), "go", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(ev.warnings) != 0 {
		t.Fatalf("warnings = %v, want none", ev.warnings)
	}
}

func TestLoopTracksObservationStateInFailedToolReply(t *testing.T) {
	model := &scriptedCompleter{turns: []Completion{
		toolCallTurn("obs", "vulpine_page_info", `{}`),
		toolCallTurn("click", "vulpine_click", `{"x":620,"y":351,"verify":true}`),
		{Message: ChatMessage{Role: "assistant", Content: "The click worked."}, FinishReason: "stop"},
	}}
	disp := &fakeDispatcher{
		results: map[string]string{
			"vulpine_page_info": `{"url":"https://www.reddit.com/register","title":"Reddit","readyState":"complete","forms":1,"inputs":2,"buttons":3}`,
		},
		errors: map[string]string{
			"vulpine_click": "nothing clickable at (620, 351)",
		},
	}
	ev := &recordEvents{}
	loop := NewLoop(model, disp, ev, LoopConfig{Models: []string{"m"}})

	final, err := loop.Run(context.Background(), "create account", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(final, "click worked") {
		t.Fatalf("final = %q, want failed-tool response instead of model success", final)
	}
	if !strings.Contains(final, "https://www.reddit.com/register") || !strings.Contains(final, "vulpine_page_info") {
		t.Fatalf("final = %q, want last confirmed observation context", final)
	}
	if len(ev.observed) < 2 {
		t.Fatalf("observation states = %#v, want observed then unverified", ev.observed)
	}
	if got := ev.observed[0]; got.Confidence != ObservationObserved || got.URL != "https://www.reddit.com/register" {
		t.Fatalf("first observation state = %#v, want observed reddit URL", got)
	}
	if got := ev.observed[len(ev.observed)-1]; got.Confidence != ObservationUnverified || got.LastFailedTool != "vulpine_click" {
		t.Fatalf("last observation state = %#v, want unverified failed click", got)
	}
}

func TestLoopAutoObservesAfterRecoverableBrowserToolFailure(t *testing.T) {
	model := &scriptedCompleter{turns: []Completion{
		toolCallTurn("click", "vulpine_click", `{"x":620,"y":351,"verify":true}`),
		{Message: ChatMessage{Role: "assistant", Content: "Done, clicked."}, FinishReason: "stop"},
	}}
	disp := &fakeDispatcher{
		results: map[string]string{
			"vulpine_observe": `{"confidence":"observed","url":"https://example.com/form","title":"Form","guidance":"Use labeled controls"}`,
		},
		errors: map[string]string{
			"vulpine_click": "nothing clickable at (620, 351)",
		},
	}
	ev := &recordEvents{}
	loop := NewLoop(model, disp, ev, LoopConfig{Models: []string{"m"}})

	final, err := loop.Run(context.Background(), "click continue", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(disp.calls) != 2 || disp.calls[0] != "vulpine_click" || disp.calls[1] != "vulpine_observe" {
		t.Fatalf("dispatcher calls = %#v, want click then automatic observe", disp.calls)
	}
	if len(model.lastMsgs) < 2 {
		t.Fatalf("model calls = %d, want second turn after tool result", len(model.lastMsgs))
	}
	toolMsg := model.lastMsgs[1][len(model.lastMsgs[1])-1]
	if !strings.Contains(toolMsg.Content, "Automatic recovery observation") || !strings.Contains(toolMsg.Content, "https://example.com/form") {
		t.Fatalf("tool message missing automatic observation: %q", toolMsg.Content)
	}
	if strings.Contains(final, "clicked") {
		t.Fatalf("final = %q, want failed-tool guard to remain active", final)
	}
	if !strings.Contains(final, "https://example.com/form") {
		t.Fatalf("final = %q, want automatic observation context", final)
	}
}

func TestLoopDoesNotAutoObserveCaptchaUnavailable(t *testing.T) {
	model := &scriptedCompleter{turns: []Completion{
		toolCallTurn("captcha", "vulpine_captcha_solve", `{"challenge_id":"challenge-1"}`),
		{Message: ChatMessage{Role: "assistant", Content: "I solved it."}, FinishReason: "stop"},
	}}
	disp := &fakeDispatcher{errors: map[string]string{
		"vulpine_captcha_solve": "captcha provider unavailable",
	}}
	loop := NewLoop(model, disp, nil, LoopConfig{Models: []string{"m"}})

	if _, err := loop.Run(context.Background(), "solve challenge", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(disp.calls) != 1 || disp.calls[0] != "vulpine_captcha_solve" {
		t.Fatalf("dispatcher calls = %#v, want no automatic observe for captcha provider state", disp.calls)
	}
}

func TestLoopClassifiesAboutBlankObservationAsLost(t *testing.T) {
	model := &scriptedCompleter{turns: []Completion{
		toolCallTurn("obs", "vulpine_page_info", `{}`),
		{Message: ChatMessage{Role: "assistant", Content: "blank"}, FinishReason: "stop"},
	}}
	disp := &fakeDispatcher{results: map[string]string{
		"vulpine_page_info": `{"url":"about:blank","title":"","readyState":"complete","forms":0,"inputs":0,"buttons":0,"links":0}`,
	}}
	ev := &recordEvents{}
	loop := NewLoop(model, disp, ev, LoopConfig{Models: []string{"m"}})

	if _, err := loop.Run(context.Background(), "inspect", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(ev.observed) == 0 {
		t.Fatal("no observation states emitted")
	}
	got := ev.observed[len(ev.observed)-1]
	if got.Confidence != ObservationLost || !got.Lost || got.URL != "about:blank" {
		t.Fatalf("observation state = %#v, want lost about:blank", got)
	}
}

func TestLoopClassifiesObserveReportAsLost(t *testing.T) {
	model := &scriptedCompleter{turns: []Completion{
		toolCallTurn("obs", "vulpine_observe", `{}`),
		{Message: ChatMessage{Role: "assistant", Content: "blank"}, FinishReason: "stop"},
	}}
	disp := &fakeDispatcher{results: map[string]string{
		"vulpine_observe": `{"confidence":"lost","url":"about:blank","page_info":"{\"url\":\"about:blank\",\"forms\":0,\"inputs\":0,\"buttons\":0,\"links\":0}","guidance":"Retry navigation"}`,
	}}
	ev := &recordEvents{}
	loop := NewLoop(model, disp, ev, LoopConfig{Models: []string{"m"}})

	if _, err := loop.Run(context.Background(), "inspect", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(ev.observed) == 0 {
		t.Fatal("no observation states emitted")
	}
	got := ev.observed[len(ev.observed)-1]
	if got.Confidence != ObservationLost || !got.Lost || got.URL != "about:blank" {
		t.Fatalf("observation state = %#v, want lost observe report", got)
	}
}

func TestLoopBlocksFalseSuccessAfterLostRecoveryObservation(t *testing.T) {
	model := &scriptedCompleter{turns: []Completion{
		toolCallTurn("click", "vulpine_click", `{"x":10,"y":20}`),
		toolCallTurn("obs", "vulpine_observe", `{}`),
		{Message: ChatMessage{Role: "assistant", Content: "Done, I clicked it successfully."}, FinishReason: "stop"},
	}}
	disp := &fakeDispatcher{
		results: map[string]string{
			"vulpine_observe": `{"confidence":"lost","url":"about:blank","guidance":"Retry navigation"}`,
		},
		errors: map[string]string{
			"vulpine_click": "nothing clickable",
		},
	}
	ev := &recordEvents{}
	loop := NewLoop(model, disp, ev, LoopConfig{Models: []string{"m"}})

	final, err := loop.Run(context.Background(), "click the button", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(final, "successfully") {
		t.Fatalf("final = %q, want failed-tool guard to replace fake success", final)
	}
	if !strings.Contains(final, "could not verify") || !strings.Contains(final, "vulpine_click") {
		t.Fatalf("final = %q, want grounded failed-tool response", final)
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

func TestLoopFallsBackOnUnavailableModel(t *testing.T) {
	// First model 404s (removed/no endpoints) -> skip to the next model.
	model := &scriptedCompleter{
		errs:  []error{&APIError{Status: 404, Body: `{"error":{"message":"No endpoints found for x:free."}}`}, nil},
		turns: []Completion{{}, {Message: ChatMessage{Role: "assistant", Content: "done"}, FinishReason: "stop"}},
	}
	loop := NewLoop(model, nil, nil, LoopConfig{Models: []string{"dead-model", "good-model"}})
	final, err := loop.Run(context.Background(), "go", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if final != "done" {
		t.Errorf("final = %q, want done", final)
	}
	if len(model.models) != 2 || model.models[1] != "good-model" {
		t.Errorf("model attempts = %v, want fallback to good-model", model.models)
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

func TestLoopBlocksExtraDetectorNavigationAfterObservedSet(t *testing.T) {
	model := &scriptedCompleter{turns: []Completion{
		toolCallTurn("n1", "vulpine_navigate", `{"url":"https://detector-a.example/"}`),
		toolCallTurn("s1", "vulpine_snapshot", `{}`),
		toolCallTurn("n2", "vulpine_navigate", `{"url":"https://detector-b.example/"}`),
		toolCallTurn("s2", "vulpine_snapshot", `{}`),
		toolCallTurn("n3", "vulpine_navigate", `{"url":"https://detector-c.example/"}`),
		toolCallTurn("s3", "vulpine_snapshot", `{}`),
		toolCallTurn("n4", "vulpine_navigate", `{"url":"https://detector-d.example/"}`),
		{Message: ChatMessage{Role: "assistant", Content: "summary"}, FinishReason: "stop"},
	}}
	disp := &fakeDispatcher{}
	ev := &recordEvents{}
	loop := NewLoop(model, disp, ev, LoopConfig{Models: []string{"m"}, MaxIterations: 12})

	final, err := loop.Run(context.Background(), "Give me the results from the most popular antibot detection sites", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if final != "summary" {
		t.Fatalf("final = %q, want summary", final)
	}

	var navigations []string
	for i, name := range disp.calls {
		if name == "vulpine_navigate" {
			navigations = append(navigations, disp.callArgs[i])
		}
	}
	if len(navigations) != 3 {
		t.Fatalf("dispatched navigations = %#v, want only first three detector pages", navigations)
	}
	if len(ev.toolRes) != 7 {
		t.Fatalf("tool result count = %d, want blocked navigation to still be reported", len(ev.toolRes))
	}
}

func TestLoopBlocksRepeatedObservedDetectorURL(t *testing.T) {
	model := &scriptedCompleter{turns: []Completion{
		toolCallTurn("n1", "vulpine_navigate", `{"url":"https://detector-a.example/"}`),
		toolCallTurn("s1", "vulpine_snapshot", `{}`),
		toolCallTurn("n2", "vulpine_navigate", `{"url":"https://detector-a.example/" }`),
		{Message: ChatMessage{Role: "assistant", Content: "summary"}, FinishReason: "stop"},
	}}
	disp := &fakeDispatcher{}
	ev := &recordEvents{}
	loop := NewLoop(model, disp, ev, LoopConfig{Models: []string{"m"}, MaxIterations: 8})

	if _, err := loop.Run(context.Background(), "Test Vulpine against the top 3 bot detector sites", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var navigations int
	for _, name := range disp.calls {
		if name == "vulpine_navigate" {
			navigations++
		}
	}
	if navigations != 1 {
		t.Fatalf("dispatched navigations = %d, want repeated observed URL blocked", navigations)
	}
}

func TestLoopInjectsInboxMessages(t *testing.T) {
	var inboxCalls int
	model := &scriptedCompleter{turns: []Completion{
		toolCallTurn("c1", "vulpine_navigate", `{}`),
		{Message: ChatMessage{Role: "assistant", Content: "done"}, FinishReason: "stop"},
	}}
	disp := &fakeDispatcher{}
	ev := &recordEvents{}
	loop := NewLoop(model, disp, ev, LoopConfig{
		Models:       []string{"m1"},
		SystemPrompt: "be brief",
		InboxReader: func() []string {
			inboxCalls++
			if inboxCalls == 2 {
				return []string{"focus on speed"}
			}
			return nil
		},
	})

	final, err := loop.Run(context.Background(), "test", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if final != "done" {
		t.Errorf("final = %q, want done", final)
	}
	if inboxCalls < 2 {
		t.Errorf("inbox reader called %d times, want at least 2", inboxCalls)
	}
	// Second model turn must include the steering message
	second := model.lastMsgs[1]
	foundSteer := false
	for _, m := range second {
		if m.Role == "system" && strings.Contains(m.Content, "focus on speed") {
			foundSteer = true
			break
		}
	}
	if !foundSteer {
		roles := make([]string, len(second))
		for i, m := range second {
			roles[i] = m.Role + ":" + m.Content
		}
		t.Fatalf("second-turn messages = %v, want system with 'focus on speed'", roles)
	}
}

func TestLoopInboxReaderReturnsMultipleMessages(t *testing.T) {
	model := &scriptedCompleter{turns: []Completion{
		{Message: ChatMessage{Role: "assistant", Content: "done"}, FinishReason: "stop"},
	}}
	disp := &fakeDispatcher{}
	loop := NewLoop(model, disp, nil, LoopConfig{
		Models:       []string{"m1"},
		SystemPrompt: "be brief",
		InboxReader: func() []string {
			return []string{"msg1", "msg2"}
		},
	})

	_, err := loop.Run(context.Background(), "test", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	msgs := model.lastMsgs[0]
	count := 0
	for _, m := range msgs {
		if m.Role == "system" && strings.Contains(m.Content, "Steering from lead agent") {
			count++
		}
	}
	if count != 2 {
		t.Errorf("found %d steering messages, want 2\nmessages: %#v", count, msgs)
	}
}
