package nanoclaw

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

var traceBearerPattern = regexp.MustCompile(`(?i)(bearer\s+)[^\s,;"]+`)
var traceQuerySecretPattern = regexp.MustCompile(`(?i)([?&](?:api[_-]?key|apikey|token|access[_-]?token|access[_-]?key|secret|password|credential|authorization)=)[^&#\s"]+`)
var traceJSONSecretPattern = regexp.MustCompile(`(?i)("(?:apiKey|api_key|apikey|token|access_token|access_key|secret|password|credential|authorization|cookie|session)"\s*:\s*")[^"]+(")`)
var traceKVSecretPattern = regexp.MustCompile(`(?i)(^|[^?&A-Za-z0-9_])((?:api[_-]?key|apikey|token|access[_-]?token|access[_-]?key|secret|password|credential|authorization|cookie|session)\s*=\s*)[^\s,;"]+`)

// ConversationMsg represents a captured conversation message from an agent.
type ConversationMsg struct {
	AgentID      string
	Role         string
	Content      string
	Tokens       int
	StreamActive bool
}

// AgentStatus represents an agent's current state.
type AgentStatus struct {
	AgentID   string `json:"agent_id"`
	ContextID string `json:"context_id"`
	Status    string `json:"status"`    // starting, running, thinking, paused, completed, error, failed, interrupted
	Objective string `json:"objective"` // current task description
	Tokens    int    `json:"tokens"`    // tokens consumed
}

// AgentOutput is a JSON message from the agent's stdout.
type AgentOutput struct {
	// Standard JSON-lines format (if agent outputs line-by-line)
	Type      string `json:"type"`      // status, log, result, error
	Status    string `json:"status"`    // for type=status
	Objective string `json:"objective"` // for type=status
	Tokens    int    `json:"tokens"`    // for type=status
	Message   string `json:"message"`   // for type=log or type=error
	Result    string `json:"result"`    // for type=result
	Role      string `json:"role"`      // for type=log (user, assistant, etc.)

	// NanoClaw `agent --json` response format
	Payloads []struct {
		Text string `json:"text"`
	} `json:"payloads"`
	Meta *struct {
		DurationMs int `json:"durationMs"`
		AgentMeta  *struct {
			SessionID string `json:"sessionId"`
			Provider  string `json:"provider"`
			Model     string `json:"model"`
			Usage     *struct {
				Input  int `json:"input"`
				Output int `json:"output"`
				Total  int `json:"total"`
			} `json:"usage"`
		} `json:"agentMeta"`
	} `json:"meta"`
}

// Agent manages a single NanoClaw subprocess.
type Agent struct {
	ID             string
	ContextID      string
	cmd            *exec.Cmd
	stdinPipe      io.WriteCloser
	stderrBuf      *bytes.Buffer // captures stderr for error reporting
	statusCh       chan AgentStatus
	conversationCh chan ConversationMsg
	doneCh         chan struct{}
	mu             sync.Mutex
	status         AgentStatus
	startedAt      time.Time
	env            map[string]string // extra environment variables for the subprocess
	RestartCount   int               // number of automatic restarts after crashes
	binary         string            // binary path used to start this agent
	args           []string          // args used to start this agent
	waitState      *agentWaitState
	finishOnce     sync.Once
	stopStatus     string
	sessionLogPath string
	lastToolName   string
	lastToolFailed bool
	lastToolStatus string
	lastToolError  string
	pendingTools   map[string]toolCallInfo
}

type toolCallInfo struct {
	Name      string
	Arguments map[string]interface{}
}

type agentWaitState struct {
	once sync.Once
	err  error
}

// newAgent creates a new agent instance (not yet started).
func newAgent(id, contextID string, statusCh chan AgentStatus) *Agent {
	return &Agent{
		ID:             id,
		ContextID:      contextID,
		statusCh:       statusCh,
		conversationCh: make(chan ConversationMsg, 64),
		doneCh:         make(chan struct{}),
		pendingTools:   make(map[string]toolCallInfo),
		status: AgentStatus{
			AgentID:   id,
			ContextID: contextID,
			Status:    "starting",
		},
	}
}

// start launches the agent subprocess.
func (a *Agent) start(binary string, args []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.binary = binary
	a.args = make([]string, len(args))
	copy(a.args, args)
	a.cmd = exec.Command(binary, args...)
	configureAgentProcess(a.cmd)
	a.waitState = &agentWaitState{}

	// Apply extra environment variables (e.g. NANOCLAW_CONFIG_PATH)
	if len(a.env) > 0 {
		a.cmd.Env = os.Environ()
		for k, v := range a.env {
			a.cmd.Env = append(a.cmd.Env, k+"="+v)
		}
	}

	stdout, err := a.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}

	stdinPipe, err := a.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create stdin pipe: %w", err)
	}
	a.stdinPipe = stdinPipe

	// Capture stderr so we can report errors to the user
	a.stderrBuf = &bytes.Buffer{}
	a.cmd.Stderr = a.stderrBuf

	if err := a.cmd.Start(); err != nil {
		return fmt.Errorf("start agent process: %w", err)
	}

	a.startedAt = time.Now()
	a.status.Status = "running"
	a.emitStatusLocked() // already holding a.mu

	// Start activity watchdog — warns with escalating timeouts
	activityCh := make(chan struct{}, 1)
	go func() {
		firstTimeout := 30 * time.Second
		longTimeout := 90 * time.Second
		timer := time.NewTimer(firstTimeout)
		defer timer.Stop()
		warnings := 0
		for {
			select {
			case <-activityCh:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(firstTimeout)
				warnings = 0
			case <-timer.C:
				a.mu.Lock()
				status := a.status.Status
				a.mu.Unlock()
				if status == "running" || status == "thinking" || status == "active" {
					warnings++
					var msg string
					switch warnings {
					case 1:
						msg = "Agent is working — waiting for response..."
					case 2:
						msg = "Still processing. The agent may be performing a browser action. Press v to view the page."
					default:
						msg = fmt.Sprintf("Agent has been working for %ds with no response. It may be stuck — try sending another message or check the browser with v.", warnings*30+30)
					}
					a.emitConversation(ConversationMsg{
						AgentID: a.ID,
						Role:    "system",
						Content: msg,
					})
				}
				timer.Reset(longTimeout)
			case <-a.doneCh:
				return
			}
		}
	}()

	if a.sessionLogPath != "" {
		go a.streamSessionEvents()
	}

	// Read JSON objects from stdout using a streaming decoder.
	// NanoClaw outputs pretty-printed multi-line JSON, so a line-based
	// scanner would fail to parse it. json.Decoder handles this correctly.
	go func() {
		defer close(a.doneCh)
		defer close(a.conversationCh)

		decoder := json.NewDecoder(stdout)
		for decoder.More() {
			var output AgentOutput
			if err := decoder.Decode(&output); err != nil {
				// If we hit a decode error, try to skip past it
				// by draining to find the next valid JSON
				if err == io.EOF {
					break
				}
				continue
			}
			// Signal activity to watchdog
			select {
			case activityCh <- struct{}{}:
			default:
			}
			a.handleOutput(output)
		}

		// Wait for the process to exit so we can inspect the exit code
		waitErr := a.waitProcess()

		a.mu.Lock()
		if a.stopStatus != "" {
			a.status.Status = a.stopStatus
		} else if a.status.Status != "completed" && a.status.Status != "error" {
			// Process exited without producing a valid response
			if waitErr != nil {
				a.status.Status = "error"
				// Emit stderr content as an error message to the conversation
				stderrText := strings.TrimSpace(a.stderrBuf.String())
				if stderrText != "" {
					a.emitConversation(ConversationMsg{
						AgentID: a.ID,
						Role:    "system",
						Content: fmt.Sprintf("Agent error (exit %v):\n%s", waitErr, truncate(stderrText, 500)),
					})
				} else {
					a.emitConversation(ConversationMsg{
						AgentID: a.ID,
						Role:    "system",
						Content: fmt.Sprintf("Agent exited with error: %v", waitErr),
					})
				}
			} else {
				a.status.Status = "completed"
			}
		} else if a.status.Status == "error" && a.stderrBuf.Len() > 0 {
			// Status was already set to error by handleOutput, but add stderr details
			stderrText := strings.TrimSpace(a.stderrBuf.String())
			if stderrText != "" {
				a.emitConversation(ConversationMsg{
					AgentID: a.ID,
					Role:    "system",
					Content: fmt.Sprintf("Agent stderr:\n%s", truncate(stderrText, 500)),
				})
			}
		}
		a.emitStatusLocked()
		a.mu.Unlock()
	}()

	return nil
}

func (a *Agent) handleOutput(output AgentOutput) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Handle NanoClaw `agent --json` response format (single JSON blob with payloads + meta)
	if len(output.Payloads) > 0 {
		for _, p := range output.Payloads {
			if p.Text != "" {
				a.emitConversation(ConversationMsg{
					AgentID: a.ID,
					Role:    "assistant",
					Content: p.Text,
				})
			}
		}
		if output.Meta != nil && output.Meta.AgentMeta != nil && output.Meta.AgentMeta.Usage != nil {
			a.status.Tokens = output.Meta.AgentMeta.Usage.Total
		}
		a.status.Status = "completed"
		a.emitStatusLocked()
		return
	}

	switch output.Type {
	case "status":
		if output.Status != "" {
			a.status.Status = output.Status
		}
		if output.Objective != "" {
			a.status.Objective = output.Objective
		}
		if output.Tokens > 0 {
			a.status.Tokens = output.Tokens
		}
	case "log":
		if output.Message != "" {
			role := output.Role
			if role == "" {
				role = "assistant"
			}
			a.emitConversation(ConversationMsg{
				AgentID: a.ID,
				Role:    role,
				Content: output.Message,
				Tokens:  output.Tokens,
			})
		}
	case "result":
		content := output.Result
		if content == "" {
			content = output.Message
		}
		if content != "" {
			a.emitConversation(ConversationMsg{
				AgentID: a.ID,
				Role:    "assistant",
				Content: content,
				Tokens:  output.Tokens,
			})
		}
	case "error":
		a.status.Status = "error"
		errMsg := output.Message
		if errMsg != "" {
			a.emitConversation(ConversationMsg{
				AgentID: a.ID,
				Role:    "system",
				Content: "Agent error: " + errMsg,
			})
		}
	}

	a.emitStatusLocked()
}

type sessionLogLine struct {
	Type    string `json:"type"`
	Message *struct {
		Role       string `json:"role"`
		ToolName   string `json:"toolName"`
		ToolCallID string `json:"toolCallId"`
		IsError    bool   `json:"isError"`
		Content    []struct {
			Type      string                 `json:"type"`
			Text      string                 `json:"text"`
			Thinking  string                 `json:"thinking"`
			ID        string                 `json:"id"`
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		} `json:"content"`
		Details map[string]interface{} `json:"details"`
	} `json:"message"`
}

func (a *Agent) streamSessionEvents() {
	path := a.sessionLogPath
	offset := int64(0)
	if info, err := os.Stat(path); err == nil {
		offset = info.Size()
	} else if !os.IsNotExist(err) {
		return
	}

	var pending string
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-a.doneCh:
			return
		case <-ticker.C:
			data, newOffset, err := readSessionChunk(path, offset)
			if err != nil {
				continue
			}
			offset = newOffset
			if len(data) == 0 {
				continue
			}
			blob := pending + string(data)
			lines := strings.Split(blob, "\n")
			pending = ""
			if !strings.HasSuffix(blob, "\n") {
				pending = lines[len(lines)-1]
				lines = lines[:len(lines)-1]
			}
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				a.handleSessionLogLine(line)
			}
		}
	}
}

func readSessionChunk(path string, offset int64) ([]byte, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer file.Close()

	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, offset, err
	}
	return data, offset + int64(len(data)), nil
}

func (a *Agent) handleSessionLogLine(line string) {
	var event sessionLogLine
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return
	}
	if event.Type != "message" || event.Message == nil {
		return
	}

	switch event.Message.Role {
	case "assistant":
		if a.lastToolFailed {
			a.emitConversation(ConversationMsg{
				AgentID: a.ID,
				Role:    "system",
				Content: formatPostFailureWarning(a.lastToolName, a.lastToolStatus, a.lastToolError),
			})
			a.lastToolFailed = false
			a.lastToolStatus = ""
		}
		for _, item := range event.Message.Content {
			if item.Type == "thinking" && strings.TrimSpace(item.Thinking) != "" {
				a.emitConversation(ConversationMsg{
					AgentID: a.ID,
					Role:    "system",
					Content: "Thinking: " + truncate(strings.TrimSpace(item.Thinking), 400),
				})
			}
		}
		for _, item := range event.Message.Content {
			if item.Type == "text" && strings.TrimSpace(item.Text) != "" {
				a.emitConversation(ConversationMsg{
					AgentID: a.ID,
					Role:    "assistant",
					Content: item.Text,
				})
			}
		}
		for _, item := range event.Message.Content {
			if item.Type != "toolCall" {
				continue
			}
			a.lastToolName = item.Name
			a.lastToolFailed = false
			a.lastToolStatus = ""
			a.lastToolError = ""
			if item.ID != "" {
				a.pendingTools[item.ID] = toolCallInfo{
					Name:      item.Name,
					Arguments: item.Arguments,
				}
			}
			if msg := summarizeToolCall(item.Name, item.Arguments); msg != "" {
				a.emitConversation(ConversationMsg{
					AgentID: a.ID,
					Role:    "system",
					Content: msg,
				})
			}
		}
	case "toolResult":
		callInfo, hasCall := a.pendingTools[event.Message.ToolCallID]
		if hasCall {
			delete(a.pendingTools, event.Message.ToolCallID)
		}
		if msg := summarizeToolResult(event.Message.ToolName, callInfo, hasCall, event.Message.Content, event.Message.Details); msg != "" {
			a.emitConversation(ConversationMsg{
				AgentID: a.ID,
				Role:    "system",
				Content: msg,
			})
		}
		status, errText := toolResultStatus(event.Message.IsError, event.Message.Content, event.Message.Details)
		a.lastToolName = event.Message.ToolName
		if status == "error" || status == "timeout" || status == "incomplete" {
			a.lastToolFailed = true
			a.lastToolStatus = status
			a.lastToolError = errText
		} else if status != "" {
			a.lastToolFailed = false
			a.lastToolStatus = status
			a.lastToolError = ""
		}
	}
}

func (a *Agent) handleStreamContent(content string, streamActive bool) {
	a.mu.Lock()
	agentID := a.ID
	a.mu.Unlock()

	if !streamActive {
		// Final flush — emit as normal assistant message
		a.emitConversation(ConversationMsg{
			AgentID:      agentID,
			Role:         "assistant",
			Content:      content,
			StreamActive: false,
		})
		return
	}

	// Streaming update — send minimal content for TUI to render in-place
	a.emitConversation(ConversationMsg{
		AgentID:      agentID,
		Role:         "stream",
		Content:      content,
		StreamActive: true,
	})
}

func (a *Agent) handleActivityContent(content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	a.mu.Lock()
	agentID := a.ID
	a.mu.Unlock()
	a.emitConversation(ConversationMsg{
		AgentID: agentID,
		Role:    "activity",
		Content: content,
	})
}

func formatPostFailureWarning(toolName, status, errText string) string {
	if toolName == "" {
		toolName = "tool"
	}
	outcome := fmt.Sprintf("failed %s action", toolName)
	switch status {
	case "timeout":
		outcome = fmt.Sprintf("timed out %s action", toolName)
	case "incomplete":
		outcome = fmt.Sprintf("incomplete %s action", toolName)
	}
	if errText != "" {
		return fmt.Sprintf("Warning: assistant replied after %s — %s", outcome, truncate(singleLine(redactTraceText(errText)), 180))
	}
	return fmt.Sprintf("Warning: assistant replied after %s with no successful retry recorded", outcome)
}

func summarizeToolCall(name string, args map[string]interface{}) string {
	label := toolActionLabel(name, args)
	if label == "" {
		return ""
	}
	if strings.HasPrefix(label, "browser ") {
		return "Running " + label
	}
	return "Running tool: " + label
}

func summarizeToolResult(name string, call toolCallInfo, hasCall bool, content []struct {
	Type      string                 `json:"type"`
	Text      string                 `json:"text"`
	Thinking  string                 `json:"thinking"`
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}, details map[string]interface{}) string {
	label := name
	if label == "" {
		label = "tool"
	}
	if hasCall {
		if callLabel := toolActionLabel(call.Name, call.Arguments); callLabel != "" {
			label = callLabel
		}
	}
	payload := mergeToolResultPayload(details, content)
	status, errText := toolResultStatus(false, content, details)
	switch status {
	case "error":
		if errText == "" {
			errText = toolResultSnippet(content, payload)
		}
		if errText != "" {
			return fmt.Sprintf("Tool failed: %s — %s", label, truncate(singleLine(redactTraceText(errText)), 180))
		}
		return fmt.Sprintf("Tool failed: %s", label)
	case "timeout":
		if errText == "" {
			errText = toolResultSnippet(content, payload)
		}
		if errText != "" {
			return fmt.Sprintf("Tool timed out: %s — %s", label, truncate(singleLine(redactTraceText(errText)), 180))
		}
		return fmt.Sprintf("Tool timed out: %s", label)
	case "incomplete":
		if errText == "" {
			errText = toolResultSnippet(content, payload)
		}
		if errText != "" {
			return fmt.Sprintf("Tool incomplete: %s — %s", label, truncate(singleLine(redactTraceText(errText)), 180))
		}
		return fmt.Sprintf("Tool incomplete: %s", label)
	case "":
		// Fall through and treat missing status as a successful completion summary.
	}
	if msg := summarizeToolSuccess(name, label, payload); msg != "" {
		return msg
	}
	if snippet := toolResultSnippet(content, payload); snippet != "" {
		return fmt.Sprintf("Tool completed: %s — %s", label, truncate(singleLine(redactTraceText(snippet)), 180))
	}
	return fmt.Sprintf("Tool completed: %s", label)
}

func toolResultStatus(isError bool, content []struct {
	Type      string                 `json:"type"`
	Text      string                 `json:"text"`
	Thinking  string                 `json:"thinking"`
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}, details map[string]interface{}) (status string, errText string) {
	if isError {
		if errText, _ = details["error"].(string); errText != "" {
			return "error", errText
		}
		if aggregated, _ := details["aggregated"].(string); aggregated != "" {
			return "error", aggregated
		}
		return "error", ""
	}

	if exitCode, ok := numericDetail(details["exitCode"]); ok && exitCode != 0 {
		if aggregated, _ := details["aggregated"].(string); aggregated != "" {
			return "error", aggregated
		}
		if errText, _ = details["error"].(string); errText != "" {
			return "error", errText
		}
		if stderr, _ := details["stderr"].(string); stderr != "" {
			return "error", stderr
		}
		return "error", fmt.Sprintf("command exited with code %d", exitCode)
	}
	if okValue, ok := details["ok"].(bool); ok && !okValue {
		if errText, _ = details["error"].(string); errText != "" {
			return "error", errText
		}
		return "error", ""
	}

	if status, _ = details["status"].(string); status != "" {
		errText, _ = details["error"].(string)
		normalized := normalizeToolStatus(status)
		if isProblemToolStatus(normalized) {
			return normalized, errText
		}
	}

	for _, item := range content {
		if item.Type != "text" || item.Text == "" {
			continue
		}
		var payload map[string]interface{}
		if json.Unmarshal([]byte(item.Text), &payload) != nil {
			continue
		}
		if status, errText, ok := problemToolStatusFromPayload(payload); ok {
			return status, errText
		}
	}

	if status, _ = details["status"].(string); status != "" {
		errText, _ = details["error"].(string)
		return normalizeToolStatus(status), errText
	}

	for _, item := range content {
		if item.Type != "text" || item.Text == "" {
			continue
		}
		var payload map[string]interface{}
		if json.Unmarshal([]byte(item.Text), &payload) != nil {
			continue
		}
		status, _ = payload["status"].(string)
		errText, _ = payload["error"].(string)
		if status != "" {
			return normalizeToolStatus(status), errText
		}
	}

	return "", ""
}

func problemToolStatusFromPayload(payload map[string]interface{}) (status string, errText string, ok bool) {
	if exitCode, exists := numericDetail(payload["exitCode"]); exists && exitCode != 0 {
		if aggregated, _ := payload["aggregated"].(string); aggregated != "" {
			return "error", aggregated, true
		}
		if errText, _ = payload["error"].(string); errText != "" {
			return "error", errText, true
		}
		if stderr, _ := payload["stderr"].(string); stderr != "" {
			return "error", stderr, true
		}
		return "error", fmt.Sprintf("command exited with code %d", exitCode), true
	}
	if okValue, exists := payload["ok"].(bool); exists && !okValue {
		if errText, _ = payload["error"].(string); errText != "" {
			return "error", errText, true
		}
		return "error", "", true
	}
	rawStatus, _ := payload["status"].(string)
	normalized := normalizeToolStatus(rawStatus)
	if isProblemToolStatus(normalized) {
		errText, _ = payload["error"].(string)
		return normalized, errText, true
	}
	return "", "", false
}

func isProblemToolStatus(status string) bool {
	return status == "error" || status == "timeout" || status == "incomplete"
}

func normalizeToolStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "ok", "success", "succeeded", "completed", "complete":
		if strings.TrimSpace(status) == "" {
			return ""
		}
		return "completed"
	case "error", "failed", "failure":
		return "error"
	case "timeout", "timed_out":
		return "timeout"
	case "incomplete", "partial", "needs_input":
		return "incomplete"
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

func numericDetail(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func summarizeBrowserCall(args map[string]interface{}) string {
	action, _ := args["action"].(string)
	if action == "" {
		return "browser action"
	}
	switch action {
	case "open":
		if url, _ := args["url"].(string); url != "" {
			return fmt.Sprintf("browser open %s", redactTraceText(url))
		}
	case "snapshot":
		if targetID, _ := args["targetId"].(string); targetID != "" {
			return fmt.Sprintf("browser snapshot target %s", truncate(targetID, 24))
		}
		return "browser snapshot"
	case "click":
		if text, _ := args["text"].(string); text != "" {
			return fmt.Sprintf("browser click %s", truncate(redactTraceText(text), 120))
		}
		if selector, _ := args["selector"].(string); selector != "" {
			return fmt.Sprintf("browser click %s", truncate(redactTraceText(selector), 120))
		}
	case "type":
		if text, _ := args["text"].(string); text != "" {
			return fmt.Sprintf("browser type %s", truncate(singleLine(redactTraceTextForArgs(args, text)), 120))
		}
	case "scroll":
		if direction, _ := args["direction"].(string); direction != "" {
			return fmt.Sprintf("browser scroll %s", direction)
		}
	case "status", "start", "stop", "close":
		return fmt.Sprintf("browser %s", action)
	}
	argText := compactJSON(args)
	if argText == "" {
		return fmt.Sprintf("browser %s", action)
	}
	return fmt.Sprintf("browser %s %s", action, truncate(argText, 160))
}

func toolActionLabel(name string, args map[string]interface{}) string {
	if name == "" {
		return ""
	}
	switch name {
	case "browser":
		return summarizeBrowserCall(args)
	case "web_fetch":
		if url, _ := args["url"].(string); url != "" {
			return fmt.Sprintf("web_fetch %s", redactTraceText(url))
		}
	case "web_search":
		if query, _ := args["query"].(string); query != "" {
			return fmt.Sprintf("web_search %s", truncate(singleLine(redactTraceText(query)), 180))
		}
	case "read", "write":
		if path, _ := args["path"].(string); path != "" {
			return fmt.Sprintf("%s %s", name, truncate(path, 180))
		}
	case "exec":
		if command, _ := args["command"].(string); command != "" {
			return fmt.Sprintf("exec %s", truncate(singleLine(redactTraceText(command)), 180))
		}
	}
	argText := compactJSON(args)
	if argText == "" {
		return name
	}
	return fmt.Sprintf("%s %s", name, truncate(argText, 180))
}

func summarizeToolSuccess(name, label string, payload map[string]interface{}) string {
	if label == "" {
		label = name
	}
	switch name {
	case "browser":
		if url, _ := payload["url"].(string); url != "" {
			safeURL := redactTraceText(url)
			if label == "browser" {
				return fmt.Sprintf("Tool completed: browser — opened %s", truncate(safeURL, 160))
			}
			if strings.Contains(label, url) {
				if title, _ := payload["title"].(string); title != "" {
					return fmt.Sprintf("Tool completed: %s — %s", label, truncate(title, 160))
				}
				return fmt.Sprintf("Tool completed: %s", label)
			}
			return fmt.Sprintf("Tool completed: %s — at %s", label, truncate(safeURL, 160))
		}
		if title, _ := payload["title"].(string); title != "" {
			return fmt.Sprintf("Tool completed: %s — %s", label, truncate(title, 160))
		}
		if targetID, _ := payload["targetId"].(string); targetID != "" {
			return fmt.Sprintf("Tool completed: %s — target %s", label, truncate(targetID, 24))
		}
		return fmt.Sprintf("Tool completed: %s", label)
	case "web_fetch":
		if url, _ := payload["url"].(string); url != "" {
			safeURL := redactTraceText(url)
			if strings.Contains(label, url) {
				return fmt.Sprintf("Tool completed: %s", label)
			}
			return fmt.Sprintf("Tool completed: %s — fetched %s", label, truncate(safeURL, 160))
		}
	case "web_search":
		if query, _ := payload["query"].(string); query != "" {
			safeQuery := redactTraceText(query)
			if strings.Contains(label, query) {
				return fmt.Sprintf("Tool completed: %s", label)
			}
			return fmt.Sprintf("Tool completed: %s — %s", label, truncate(safeQuery, 160))
		}
	}
	return ""
}

func mergeToolResultPayload(details map[string]interface{}, content []struct {
	Type      string                 `json:"type"`
	Text      string                 `json:"text"`
	Thinking  string                 `json:"thinking"`
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}) map[string]interface{} {
	payload := make(map[string]interface{})
	for k, v := range details {
		payload[k] = v
	}
	for _, item := range content {
		if item.Type != "text" || strings.TrimSpace(item.Text) == "" {
			continue
		}
		var parsed map[string]interface{}
		if json.Unmarshal([]byte(item.Text), &parsed) != nil {
			continue
		}
		for k, v := range parsed {
			if existing, ok := payload[k]; ok && existing != nil && existing != "" {
				continue
			}
			payload[k] = v
		}
	}
	if len(payload) == 0 {
		return nil
	}
	return payload
}

func toolResultSnippet(content []struct {
	Type      string                 `json:"type"`
	Text      string                 `json:"text"`
	Thinking  string                 `json:"thinking"`
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}, payload map[string]interface{}) string {
	for _, key := range []string{"aggregated", "output", "stdout", "stderr", "message", "text"} {
		if payload != nil {
			if value, _ := payload[key].(string); strings.TrimSpace(value) != "" {
				return redactTraceText(value)
			}
		}
	}
	for _, item := range content {
		if item.Type != "text" || strings.TrimSpace(item.Text) == "" {
			continue
		}
		var parsed map[string]interface{}
		if json.Unmarshal([]byte(item.Text), &parsed) == nil {
			for _, key := range []string{"aggregated", "output", "stdout", "stderr", "message", "text"} {
				if value, _ := parsed[key].(string); strings.TrimSpace(value) != "" {
					return redactTraceText(value)
				}
			}
			continue
		}
		return redactTraceText(item.Text)
	}
	return ""
}

func compactJSON(v interface{}) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(redactTraceValue(v, false))
	if err != nil {
		return ""
	}
	return string(data)
}

func redactTraceText(value string) string {
	value = traceBearerPattern.ReplaceAllString(value, "${1}[redacted]")
	value = traceQuerySecretPattern.ReplaceAllString(value, "${1}[redacted]")
	value = traceJSONSecretPattern.ReplaceAllString(value, "${1}[redacted]${2}")
	return traceKVSecretPattern.ReplaceAllString(value, "${1}${2}[redacted]")
}

func redactTraceTextForArgs(args map[string]interface{}, value string) string {
	if traceArgsSensitive(args) {
		return "[redacted]"
	}
	redacted := redactTraceText(value)
	if redacted != value {
		return redacted
	}
	return value
}

func redactTraceValue(value interface{}, sensitiveContext bool) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		localSensitive := sensitiveContext
		for key, child := range typed {
			if sensitiveTraceKey(key) || traceDescriptorIdentifiesSensitiveField(key, child) {
				localSensitive = true
				break
			}
		}
		out := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			switch {
			case sensitiveTraceKey(key):
				out[key] = "[redacted]"
			case localSensitive && traceTypedValueKey(key):
				out[key] = "[redacted]"
			default:
				out[key] = redactTraceValue(child, localSensitive)
			}
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i, child := range typed {
			out[i] = redactTraceValue(child, sensitiveContext)
		}
		return out
	case string:
		return redactTraceText(typed)
	default:
		return value
	}
}

func traceArgsSensitive(args map[string]interface{}) bool {
	if len(args) == 0 {
		return false
	}
	sanitized := redactTraceValue(args, false)
	encodedOriginal, _ := json.Marshal(args)
	encodedSanitized, _ := json.Marshal(sanitized)
	return !bytes.Equal(encodedOriginal, encodedSanitized)
}

func sensitiveTraceKey(key string) bool {
	normalized := normalizeTraceToken(key)
	for _, marker := range []string{"api_key", "apikey", "token", "secret", "password", "credential", "authorization", "cookie", "session"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return normalized == "auth" || strings.HasSuffix(normalized, "_auth")
}

func traceDescriptorIdentifiesSensitiveField(key string, value interface{}) bool {
	switch normalizeTraceToken(key) {
	case "selector", "field", "field_name", "name", "label", "placeholder", "aria_label", "type", "input_type":
	default:
		return false
	}
	raw, ok := value.(string)
	if !ok {
		return false
	}
	return sensitiveTraceKey(raw)
}

func traceTypedValueKey(key string) bool {
	switch normalizeTraceToken(key) {
	case "text", "value", "input", "typed_text":
		return true
	default:
		return false
	}
}

func normalizeTraceToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func singleLine(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func (a *Agent) emitStatus() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.emitStatusLocked()
}

func (a *Agent) emitStatusLocked() {
	defer func() {
		_ = recover()
	}()
	select {
	case a.statusCh <- a.status:
	default:
		// Channel full, drop
	}
}

// SendMessage writes a message to the agent's stdin pipe.
func (a *Agent) SendMessage(text string) error {
	a.mu.Lock()
	pipe := a.stdinPipe
	a.mu.Unlock()

	if pipe == nil {
		return fmt.Errorf("stdin pipe not available")
	}
	_, err := fmt.Fprintf(pipe, "%s\n", text)
	return err
}

// emitConversation sends a conversation message to the channel (non-blocking).
func (a *Agent) emitConversation(msg ConversationMsg) {
	defer func() {
		_ = recover()
	}()
	select {
	case a.conversationCh <- msg:
	default:
		// Channel full, drop
	}
}

func (a *Agent) finish(status string) {
	a.finishOnce.Do(func() {
		a.mu.Lock()
		if status != "" {
			a.status.Status = status
		}
		a.mu.Unlock()
		a.emitStatus()
		close(a.doneCh)
		close(a.conversationCh)
	})
}

// Stop kills the agent subprocess and waits for it to exit.
func (a *Agent) Stop() error {
	return a.stopWithStatus("completed")
}

func (a *Agent) stopWithStatus(status string) error {
	a.mu.Lock()
	cmd := a.cmd
	pipe := a.stdinPipe
	if status == "" {
		status = "completed"
	}
	a.stopStatus = status
	a.status.Status = status
	a.mu.Unlock()

	// Try to tell NanoClaw to save state before killing
	if pipe != nil {
		fmt.Fprintf(pipe, "/savestate\n")
		time.Sleep(500 * time.Millisecond)
	}

	if cmd != nil && cmd.Process != nil {
		_ = killAgentProcess(cmd)
		// Reap the process through the shared wait path so Stop and the stdout
		// reader goroutine do not race on exec.Cmd.Wait.
		a.waitProcess()
	}

	if pipe != nil {
		pipe.Close()
	}

	if cmd == nil && pipe == nil {
		a.finish(status)
	} else {
		a.emitStatus()
	}
	return nil
}

// Wait blocks until the agent exits.
func (a *Agent) Wait() {
	<-a.doneCh
}

func (a *Agent) waitProcess() error {
	a.mu.Lock()
	cmd := a.cmd
	state := a.waitState
	a.mu.Unlock()

	if cmd == nil || state == nil {
		return nil
	}

	state.once.Do(func() {
		state.err = cmd.Wait()
	})
	return state.err
}

func (a *Agent) StopRequested() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stopStatus != ""
}

// ExitCode returns the process exit code, or -1 if still running or not started.
func (a *Agent) ExitCode() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cmd == nil || a.cmd.ProcessState == nil {
		return -1
	}
	return a.cmd.ProcessState.ExitCode()
}

// restart re-launches the agent subprocess using the same binary and args.
func (a *Agent) restart() error {
	// Reset channels for the new process
	a.conversationCh = make(chan ConversationMsg, 64)
	a.doneCh = make(chan struct{})
	return a.start(a.binary, a.args)
}

// Status returns the current agent status.
func (a *Agent) Status() AgentStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// truncate shortens a string to maxLen, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
