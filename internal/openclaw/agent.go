package openclaw

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ConversationMsg represents a captured conversation message from an agent.
type ConversationMsg struct {
	AgentID string
	Role    string
	Content string
	Tokens  int
}

// AgentStatus represents an agent's current state.
type AgentStatus struct {
	AgentID   string `json:"agent_id"`
	ContextID string `json:"context_id"`
	Status    string `json:"status"`    // starting, running, thinking, completed, error
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

	// OpenClaw `agent --json` response format
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

// Agent manages a single OpenClaw subprocess.
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
}

// newAgent creates a new agent instance (not yet started).
func newAgent(id, contextID string, statusCh chan AgentStatus) *Agent {
	return &Agent{
		ID:             id,
		ContextID:      contextID,
		statusCh:       statusCh,
		conversationCh: make(chan ConversationMsg, 64),
		doneCh:         make(chan struct{}),
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

	a.cmd = exec.Command(binary, args...)

	// Apply extra environment variables (e.g. OPENCLAW_CONFIG_PATH)
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

	// Start activity watchdog — warns if agent produces no output for too long
	activityCh := make(chan struct{}, 1)
	go func() {
		timeout := 60 * time.Second
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		warned := false
		for {
			select {
			case <-activityCh:
				// Agent produced output — reset timer and warning
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(timeout)
				warned = false
			case <-timer.C:
				// No output for 60s
				if !warned {
					a.mu.Lock()
					if a.status.Status == "running" || a.status.Status == "thinking" {
						a.emitConversation(ConversationMsg{
							AgentID: a.ID,
							Role:    "system",
							Content: "Agent has been inactive for 60s — may be stuck on a browser action. Check if the page is loading or if there's a dialog/captcha blocking.",
						})
						warned = true
					}
					a.mu.Unlock()
				}
				timer.Reset(timeout)
			case <-a.doneCh:
				return
			}
		}
	}()

	// Read JSON objects from stdout using a streaming decoder.
	// OpenClaw outputs pretty-printed multi-line JSON, so a line-based
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
		waitErr := a.cmd.Wait()

		a.mu.Lock()
		if a.status.Status != "completed" && a.status.Status != "error" {
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

	// Handle OpenClaw `agent --json` response format (single JSON blob with payloads + meta)
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

func (a *Agent) emitStatus() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.emitStatusLocked()
}

func (a *Agent) emitStatusLocked() {
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
	select {
	case a.conversationCh <- msg:
	default:
		// Channel full, drop
	}
}

// Stop kills the agent subprocess and waits for it to exit.
func (a *Agent) Stop() error {
	a.mu.Lock()
	cmd := a.cmd
	pipe := a.stdinPipe
	a.mu.Unlock()

	// Try to tell OpenClaw to save state before killing
	if pipe != nil {
		fmt.Fprintf(pipe, "/savestate\n")
		time.Sleep(500 * time.Millisecond)
	}

	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		// Reap the zombie process; ignore error since Kill causes a non-zero exit.
		cmd.Wait()
	}

	if pipe != nil {
		pipe.Close()
	}

	a.mu.Lock()
	a.status.Status = "completed"
	a.emitStatusLocked()
	a.mu.Unlock()
	return nil
}

// Wait blocks until the agent exits.
func (a *Agent) Wait() {
	<-a.doneCh
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
