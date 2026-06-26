package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
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
	OnPhase(phase string)                         // processing, waiting_on_tool, idle, finalizing
	OnTurn(turn int)                              // current iteration count
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
func (NopEvents) OnPhase(string)                    {}
func (NopEvents) OnTurn(int)                        {}

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
	// Defaults to 100 when <= 0.
	MaxIterations int
	// KeepFullToolResults is how many most-recent tool results to keep verbatim;
	// older large results are compressed to a short stub to save context.
	// Defaults to 3 when <= 0.
	KeepFullToolResults int
	// ModelTimeout is the max duration to wait for a single model API call.
	// When exceeded, the loop returns a timeout error. 0 means no timeout
	// (wait indefinitely, unless the caller's context cancels).
	ModelTimeout time.Duration
	// InboxReader returns pending steering messages for this agent. Called
	// before each model turn; returned messages are injected as system messages.
	// May be nil.
	InboxReader func() []string
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
		cfg.MaxIterations = 100
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
	l.events.OnPhase("processing")

	// Track whether the most recent tool call failed, so we can warn the operator
	// if the model then replies as if the task succeeded (false-success).
	lastToolFailed := false
	lastFailedTool := ""
	var observation ObservationState
	navGuard := newNavigationGuard(task)

	for iter := 0; iter < l.cfg.MaxIterations; iter++ {
		if err := ctx.Err(); err != nil {
			l.events.OnStatus("error")
			return "", err
		}

		l.events.OnTurn(iter + 1)
		l.events.OnPhase("processing")

		if l.cfg.InboxReader != nil {
			for _, msg := range l.cfg.InboxReader() {
				messages = append(messages, ChatMessage{Role: "system", Content: "[Steering from lead agent]: " + msg})
			}
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
			l.events.OnPhase("finalizing")
			final := comp.Message.Content
			if lastToolFailed {
				l.events.OnWarning(fmt.Sprintf("assistant replied after %s failed, with no successful retry recorded", lastFailedTool))
				final = failedToolFinalReply(lastFailedTool, observation)
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
			l.events.OnPhase("waiting_on_tool")
			l.events.OnToolCall(tc.Function.Name, tc.Function.Arguments)

			var result string
			var isErr bool
			policyBlocked := false
			if blockResult, blocked := navGuard.BeforeTool(tc.Function.Name, tc.Function.Arguments); blocked {
				result, isErr = blockResult, true
				policyBlocked = true
			} else if l.tools == nil {
				result, isErr = fmt.Sprintf("no tools available to satisfy %q", tc.Function.Name), true
			} else if r, e, derr := l.tools.Dispatch(ctx, tc.Function.Name, tc.Function.Arguments); derr != nil {
				result, isErr = "tool dispatch error: "+derr.Error(), true
			} else {
				result, isErr = r, e
			}
			l.events.OnPhase("processing")

			l.events.OnToolResult(tc.Function.Name, result, isErr)
			if isErr {
				if !policyBlocked {
					lastToolFailed = true
					lastFailedTool = tc.Function.Name
					observation = unverifiedStateAfterFailure(observation, tc.Function.Name, result)
					emitObservationState(l.events, observation)
				}
			} else {
				if isObservationTool(tc.Function.Name) {
					observation = observedStateFromTool(tc.Function.Name, result)
					emitObservationState(l.events, observation)
					if observation.Confidence == ObservationObserved {
						lastToolFailed = false
						lastFailedTool = ""
					}
				} else {
					lastToolFailed = false
					lastFailedTool = ""
				}
			}
			navGuard.AfterTool(tc.Function.Name, tc.Function.Arguments, isErr)
			messages = append(messages, ChatMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    result,
			})
		}

		l.compressOldToolResults(messages)
	}

	l.events.OnPhase("finalizing")

	l.events.OnStatus("error")
	return "", fmt.Errorf("agent did not finish within %d iterations", l.cfg.MaxIterations)
}

func failedToolFinalReply(tool string, observation ObservationState) string {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		tool = "the last browser tool"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "I could not verify that action. The last browser tool failed: `%s`.", tool)
	if observation.LastObservedTool != "" {
		fmt.Fprintf(&b, " Last confirmed observation: %s.", observation.LastObservedSummary)
	}
	if observation.Lost || observation.Confidence == ObservationLost {
		b.WriteString(" The browser appears to be in a lost page state.")
	}
	b.WriteString(" I will not infer success from a failed tool result; please retry observation, use a visual/browser recovery step, or take over the visible browser for this step.")
	return b.String()
}

type navigationGuard struct {
	active      bool
	maxObserved int
	currentURL  string
	observed    map[string]bool
}

func newNavigationGuard(task string) *navigationGuard {
	g := &navigationGuard{
		maxObserved: 3,
		observed:    make(map[string]bool),
	}
	lower := strings.ToLower(task)
	for _, needle := range []string{
		"bot detector",
		"bot-detector",
		"antibot",
		"anti-bot",
		"detector site",
		"detection site",
		"fingerprint detector",
		"fingerprinting test",
	} {
		if strings.Contains(lower, needle) {
			g.active = true
			break
		}
	}
	if !g.active {
		return g
	}
	if limit := requestedSiteLimit(lower); limit > 0 {
		g.maxObserved = limit
	}
	return g
}

func requestedSiteLimit(task string) int {
	for _, pattern := range []string{
		`\btop\s+(\d+)\b`,
		`\b(\d+)\s+(?:sites|pages|detectors|checks)\b`,
	} {
		re := regexp.MustCompile(pattern)
		if m := re.FindStringSubmatch(task); len(m) == 2 {
			n, err := strconv.Atoi(m[1])
			if err == nil && n > 0 && n <= 10 {
				return n
			}
		}
	}
	return 0
}

func (g *navigationGuard) BeforeTool(name, rawArgs string) (string, bool) {
	if g == nil || !g.active || name != "vulpine_navigate" {
		return "", false
	}
	nextURL, ok := normalizeToolURL(rawArgs)
	if !ok {
		return "", false
	}
	if g.observed[nextURL] {
		return "Navigation guard: that URL has already produced usable page state. Do not revisit it; use the information already captured and summarize the requested results now.", true
	}
	if len(g.observed) >= g.maxObserved {
		return fmt.Sprintf("Navigation guard: already captured usable page state for %d requested detector/check pages. Do not open extra sites; summarize the observed results now.", len(g.observed)), true
	}
	return "", false
}

func (g *navigationGuard) AfterTool(name, rawArgs string, isErr bool) {
	if g == nil || !g.active {
		return
	}
	if name == "vulpine_navigate" {
		if isErr {
			g.currentURL = ""
			return
		}
		if current, ok := normalizeToolURL(rawArgs); ok {
			g.currentURL = current
		}
		return
	}
	if isErr || g.currentURL == "" || !isObservationTool(name) {
		return
	}
	g.observed[g.currentURL] = true
}

func isObservationTool(name string) bool {
	switch name {
	case "vulpine_page_settled", "vulpine_snapshot", "vulpine_page_info", "vulpine_get_ax_tree", "vulpine_observe", "vulpine_annotated_screenshot":
		return true
	default:
		return false
	}
}

func normalizeToolURL(rawArgs string) (string, bool) {
	var args struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return "", false
	}
	raw := strings.TrimSpace(args.URL)
	if raw == "" {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw, true
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String(), true
}

// streamWithFallback tries each configured model in order, advancing on a
// provider 429 (rate limit). Non-rate-limit errors abort immediately.
func (l *Loop) streamWithFallback(ctx context.Context, messages []ChatMessage) (Completion, error) {
	var lastErr error
	for i, model := range l.cfg.Models {
		callCtx := ctx
		cancel := func() {}
		if l.cfg.ModelTimeout > 0 {
			callCtx, cancel = context.WithTimeout(ctx, l.cfg.ModelTimeout)
		}
		comp, err := l.model.Stream(callCtx, model, messages, l.cfg.Tools, l.events.OnTextDelta)
		cancel()
		if err == nil {
			return comp, nil
		}
		lastErr = err
		// Fall back to the next model on a rate limit or an unavailable/removed
		// model (404), as long as another model remains. Other errors abort.
		if (IsRateLimited(err) || IsModelUnavailable(err)) && i < len(l.cfg.Models)-1 {
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
