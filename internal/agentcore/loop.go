package agentcore

import (
	"context"
	"fmt"
	"strings"
)

// Completer runs one streaming model turn. *ModelClient implements it; tests
// supply a scripted fake.
type Completer interface {
	Stream(ctx context.Context, model string, messages []ChatMessage, tools []ToolDef, onTextDelta func(string)) (Completion, error)
}

// ToolDispatcher executes a tool call and returns its textual result. isErr
// marks a tool-level failure (fed back to the model); err is a dispatch-level
// failure. *BrowserToolset implements it.
type ToolDispatcher interface {
	Dispatch(ctx context.Context, name string, rawArgs string) (result string, isErr bool, err error)
}

// Events receives the agent's observable activity. The integration layer adapts
// these to the TUI/panel conversation+status streams and vault persistence; the
// loop itself stays free of those dependencies.
type Events interface {
	OnTextDelta(delta string)                     // streamed partial assistant text
	OnAssistant(text string)                      // a completed assistant message (may be intermediate)
	OnToolCall(name, args string)                 // about to run a tool
	OnToolResult(name, result string, isErr bool) // tool finished
	OnStatus(status string)                       // "running" | "completed" | "error"
	OnUsage(turn Usage)                           // token usage for one completed model turn
	OnWarning(text string)                        // operator-facing warning (e.g. false-success)
}

// NopEvents is an Events that ignores everything (useful as a default/base).
type NopEvents struct{}

func (NopEvents) OnTextDelta(string)                {}
func (NopEvents) OnAssistant(string)                {}
func (NopEvents) OnToolCall(string, string)         {}
func (NopEvents) OnToolResult(string, string, bool) {}
func (NopEvents) OnStatus(string)                   {}
func (NopEvents) OnUsage(Usage)                     {}
func (NopEvents) OnWarning(string)                  {}

// LoopConfig configures a single agent run.
type LoopConfig struct {
	// Models is the model fallback chain (primary first). On a provider 429 the
	// loop advances to the next model. Required; at least one entry.
	Models []string
	// SystemPrompt is prepended as the system message.
	SystemPrompt string
	// Tools are the function schemas exposed to the model.
	Tools []ToolDef
	// MaxIterations bounds the model<->tool turns to prevent runaway loops.
	// Defaults to 24 when <= 0.
	MaxIterations int
	// KeepFullToolResults is how many most-recent tool results to keep verbatim;
	// older large results are compressed to a short stub to save context.
	// Defaults to 3 when <= 0.
	KeepFullToolResults int
}

// Loop runs the model<->tool conversation for one agent.
type Loop struct {
	model  Completer
	tools  ToolDispatcher
	events Events
	cfg    LoopConfig
}

// NewLoop builds a loop. events may be nil (NopEvents is used). tools may be nil
// if no tools are configured.
func NewLoop(model Completer, tools ToolDispatcher, events Events, cfg LoopConfig) *Loop {
	if events == nil {
		events = NopEvents{}
	}
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 24
	}
	if cfg.KeepFullToolResults <= 0 {
		cfg.KeepFullToolResults = 3
	}
	return &Loop{model: model, tools: tools, events: events, cfg: cfg}
}

// Run executes the agent on task, seeded with optional prior history (excluding
// the system prompt). It returns the final assistant text. The loop emits
// activity via Events as it goes. Context cancellation stops the run.
func (l *Loop) Run(ctx context.Context, task string, history []ChatMessage) (string, error) {
	if len(l.cfg.Models) == 0 {
		return "", fmt.Errorf("no models configured")
	}

	messages := make([]ChatMessage, 0, len(history)+2)
	if strings.TrimSpace(l.cfg.SystemPrompt) != "" {
		messages = append(messages, ChatMessage{Role: "system", Content: l.cfg.SystemPrompt})
	}
	messages = append(messages, history...)
	messages = append(messages, ChatMessage{Role: "user", Content: task})

	l.events.OnStatus("running")

	// Track whether the most recent tool call failed, so we can warn the operator
	// if the model then replies as if the task succeeded (false-success).
	lastToolFailed := false
	lastFailedTool := ""

	for iter := 0; iter < l.cfg.MaxIterations; iter++ {
		if err := ctx.Err(); err != nil {
			l.events.OnStatus("error")
			return "", err
		}

		comp, err := l.streamWithFallback(ctx, messages)
		if err != nil {
			l.events.OnStatus("error")
			return "", err
		}
		if comp.Usage.TotalTokens > 0 || comp.Usage.PromptTokens > 0 || comp.Usage.CompletionTokens > 0 {
			l.events.OnUsage(comp.Usage)
		}

		if !comp.HasToolCalls() {
			final := comp.Message.Content
			if lastToolFailed {
				l.events.OnWarning(fmt.Sprintf("assistant replied after %s failed, with no successful retry recorded", lastFailedTool))
			}
			l.events.OnAssistant(final)
			l.events.OnStatus("completed")
			return final, nil
		}

		// Assistant turn requested tools. Record it (with any interim text),
		// then execute each tool and append results.
		messages = append(messages, comp.Message)
		if strings.TrimSpace(comp.Message.Content) != "" {
			l.events.OnAssistant(comp.Message.Content)
		}

		for _, tc := range comp.Message.ToolCalls {
			if err := ctx.Err(); err != nil {
				l.events.OnStatus("error")
				return "", err
			}
			l.events.OnToolCall(tc.Function.Name, tc.Function.Arguments)

			var result string
			var isErr bool
			if l.tools == nil {
				result, isErr = fmt.Sprintf("no tools available to satisfy %q", tc.Function.Name), true
			} else if r, e, derr := l.tools.Dispatch(ctx, tc.Function.Name, tc.Function.Arguments); derr != nil {
				result, isErr = "tool dispatch error: "+derr.Error(), true
			} else {
				result, isErr = r, e
			}

			l.events.OnToolResult(tc.Function.Name, result, isErr)
			if isErr {
				lastToolFailed = true
				lastFailedTool = tc.Function.Name
			} else {
				lastToolFailed = false
				lastFailedTool = ""
			}
			messages = append(messages, ChatMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    result,
			})
		}

		l.compressOldToolResults(messages)
	}

	l.events.OnStatus("error")
	return "", fmt.Errorf("agent did not finish within %d iterations", l.cfg.MaxIterations)
}

// streamWithFallback tries each configured model in order, advancing on a
// provider 429 (rate limit). Non-rate-limit errors abort immediately.
func (l *Loop) streamWithFallback(ctx context.Context, messages []ChatMessage) (Completion, error) {
	var lastErr error
	for i, model := range l.cfg.Models {
		comp, err := l.model.Stream(ctx, model, messages, l.cfg.Tools, l.events.OnTextDelta)
		if err == nil {
			return comp, nil
		}
		lastErr = err
		// Only fall back on rate limits, and only if another model remains.
		if IsRateLimited(err) && i < len(l.cfg.Models)-1 {
			continue
		}
		return Completion{}, err
	}
	return Completion{}, lastErr
}

// compressOldToolResults stubs out large tool results older than the most
// recent KeepFullToolResults, preserving the model's recent working context
// while keeping the prompt from growing unbounded over a long task.
func (l *Loop) compressOldToolResults(messages []ChatMessage) {
	const largeThreshold = 2000
	kept := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "tool" {
			continue
		}
		kept++
		if kept > l.cfg.KeepFullToolResults && len(messages[i].Content) > largeThreshold {
			messages[i].Content = fmt.Sprintf("[tool result elided — %d bytes; already processed above]", len(messages[i].Content))
		}
	}
}
