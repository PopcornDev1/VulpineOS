package openclaw

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
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

// AgentOutput is a JSON-lines message from the agent's stdout.
type AgentOutput struct {
	Type      string `json:"type"`      // status, log, result, error
	Status    string `json:"status"`    // for type=status
	Objective string `json:"objective"` // for type=status
	Tokens    int    `json:"tokens"`    // for type=status
	Message   string `json:"message"`   // for type=log or type=error
	Result    string `json:"result"`    // for type=result
	Role      string `json:"role"`      // for type=log (user, assistant, etc.)
}

// Agent manages a single OpenClaw subprocess.
type Agent struct {
	ID             string
	ContextID      string
	cmd            *exec.Cmd
	stdinPipe      io.WriteCloser
	statusCh       chan AgentStatus
	conversationCh chan ConversationMsg
	doneCh         chan struct{}
	mu             sync.Mutex
	status         AgentStatus
	startedAt      time.Time
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
	stdout, err := a.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}

	stdinPipe, err := a.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create stdin pipe: %w", err)
	}
	a.stdinPipe = stdinPipe

	if err := a.cmd.Start(); err != nil {
		return fmt.Errorf("start agent process: %w", err)
	}

	a.startedAt = time.Now()
	a.status.Status = "running"
	a.emitStatus()

	// Read JSON-lines from stdout
	go func() {
		defer close(a.doneCh)
		defer close(a.conversationCh)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1MB max line
		for scanner.Scan() {
			var output AgentOutput
			if err := json.Unmarshal(scanner.Bytes(), &output); err != nil {
				continue
			}
			a.handleOutput(output)
		}

		// Process exited
		a.mu.Lock()
		if a.status.Status != "completed" && a.status.Status != "error" {
			a.status.Status = "completed"
		}
		a.emitStatusLocked()
		a.mu.Unlock()
	}()

	return nil
}

func (a *Agent) handleOutput(output AgentOutput) {
	a.mu.Lock()
	defer a.mu.Unlock()

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
