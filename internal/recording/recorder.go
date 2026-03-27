package recording

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"vulpineos/internal/juggler"
)

// ActionType identifies what kind of browser action was recorded.
type ActionType string

const (
	ActionNavigate   ActionType = "navigate"
	ActionClick      ActionType = "click"
	ActionType_      ActionType = "type"
	ActionScroll     ActionType = "scroll"
	ActionScreenshot ActionType = "screenshot"
	ActionEvaluate   ActionType = "evaluate"
	ActionSetContent ActionType = "setContent"
)

// Action is a single recorded browser action with timestamp.
type Action struct {
	AgentID   string          `json:"agentId"`
	Type      ActionType      `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// Recorder stores browser actions per agent as replayable timelines.
// Recordings are ephemeral (in-memory only, not persisted to vault).
type Recorder struct {
	mu      sync.Mutex
	actions map[string][]Action // agentID -> actions
}

// NewRecorder creates a new empty Recorder.
func NewRecorder() *Recorder {
	return &Recorder{
		actions: make(map[string][]Action),
	}
}

// Record appends an action to the given agent's timeline.
func (r *Recorder) Record(agentID string, actionType ActionType, data json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.actions[agentID] = append(r.actions[agentID], Action{
		AgentID:   agentID,
		Type:      actionType,
		Timestamp: time.Now(),
		Data:      data,
	})
}

// GetTimeline returns all recorded actions for the given agent, sorted by timestamp.
func (r *Recorder) GetTimeline(agentID string) []Action {
	r.mu.Lock()
	defer r.mu.Unlock()

	orig := r.actions[agentID]
	if len(orig) == 0 {
		return nil
	}

	// Return a copy sorted by time.
	result := make([]Action, len(orig))
	copy(result, orig)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp.Before(result[j].Timestamp)
	})
	return result
}

// Export returns the agent's timeline as JSON bytes.
func (r *Recorder) Export(agentID string) ([]byte, error) {
	timeline := r.GetTimeline(agentID)
	if timeline == nil {
		timeline = []Action{}
	}
	return json.Marshal(timeline)
}

// Clear removes all recorded actions for the given agent.
func (r *Recorder) Clear(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.actions, agentID)
}

// actionToJugglerCall maps a recorded action to a Juggler method + params.
func actionToJugglerCall(a Action) (method string, params map[string]interface{}, err error) {
	var data map[string]interface{}
	if len(a.Data) > 0 {
		if err := json.Unmarshal(a.Data, &data); err != nil {
			return "", nil, fmt.Errorf("unmarshal action data: %w", err)
		}
	}
	if data == nil {
		data = make(map[string]interface{})
	}

	switch a.Type {
	case ActionNavigate:
		return "Page.navigate", data, nil
	case ActionClick:
		return "Page.dispatchMouseEvent", data, nil
	case ActionType_:
		return "Page.dispatchKeyEvent", data, nil
	case ActionScroll:
		return "Page.dispatchMouseEvent", data, nil
	case ActionScreenshot:
		return "Page.screenshot", data, nil
	case ActionEvaluate:
		return "Runtime.evaluate", data, nil
	case ActionSetContent:
		return "Page.setContent", data, nil
	default:
		return "", nil, fmt.Errorf("unknown action type %q", a.Type)
	}
}

// Replay executes all recorded actions for the given agent against a Juggler client.
// Actions are replayed in timestamp order with the original inter-action delays preserved.
func (r *Recorder) Replay(agentID string, client *juggler.Client, sessionID string) error {
	timeline := r.GetTimeline(agentID)
	if len(timeline) == 0 {
		return nil
	}

	for i, action := range timeline {
		// Preserve original timing gaps between actions.
		if i > 0 {
			gap := action.Timestamp.Sub(timeline[i-1].Timestamp)
			if gap > 0 {
				time.Sleep(gap)
			}
		}

		method, params, err := actionToJugglerCall(action)
		if err != nil {
			return fmt.Errorf("action %d (%s): %w", i, action.Type, err)
		}

		if _, err := client.Call(sessionID, method, params); err != nil {
			return fmt.Errorf("replay action %d (%s): %w", i, action.Type, err)
		}
	}

	return nil
}
