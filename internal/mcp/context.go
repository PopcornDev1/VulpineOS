package mcp

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"vulpineos/internal/juggler"
)

// SessionContext tracks the execution context and frame IDs for a Juggler session.
type SessionContext struct {
	ExecutionContextID string
	FrameID            string
}

// ContextTracker subscribes to Juggler events and tracks execution contexts
// and frame IDs per session. Required because Juggler's Runtime.evaluate needs
// executionContextId and Page.navigate needs frameId.
type ContextTracker struct {
	mu       sync.RWMutex
	contexts map[string]*SessionContext // sessionID → context
	client   *juggler.Client
}

// NewContextTracker creates a tracker and subscribes to the relevant events.
func NewContextTracker(client *juggler.Client) *ContextTracker {
	ct := &ContextTracker{
		contexts: make(map[string]*SessionContext),
		client:   client,
	}

	client.Subscribe("Runtime.executionContextCreated", func(sessionID string, params json.RawMessage) {
		var ev struct {
			ExecutionContextID string `json:"executionContextId"`
			AuxData            struct {
				FrameID string `json:"frameId"`
			} `json:"auxData"`
		}
		json.Unmarshal(params, &ev)

		ct.mu.Lock()
		defer ct.mu.Unlock()

		ctx, ok := ct.contexts[sessionID]
		if !ok {
			ctx = &SessionContext{}
			ct.contexts[sessionID] = ctx
		}
		if ev.ExecutionContextID != "" {
			ctx.ExecutionContextID = ev.ExecutionContextID
		}
		if ev.AuxData.FrameID != "" {
			ctx.FrameID = ev.AuxData.FrameID
		}
	})

	client.Subscribe("Page.frameAttached", func(sessionID string, params json.RawMessage) {
		var ev struct {
			FrameID       string `json:"frameId"`
			ParentFrameID string `json:"parentFrameId"`
		}
		json.Unmarshal(params, &ev)

		// Only track main frames (no parent)
		if ev.ParentFrameID == "" && ev.FrameID != "" {
			ct.mu.Lock()
			ctx, ok := ct.contexts[sessionID]
			if !ok {
				ctx = &SessionContext{}
				ct.contexts[sessionID] = ctx
			}
			ctx.FrameID = ev.FrameID
			ct.mu.Unlock()
		}
	})

	client.Subscribe("Browser.attachedToTarget", func(_ string, params json.RawMessage) {
		var ev struct {
			SessionID string `json:"sessionId"`
		}
		json.Unmarshal(params, &ev)
		if ev.SessionID != "" {
			ct.mu.Lock()
			if _, ok := ct.contexts[ev.SessionID]; !ok {
				ct.contexts[ev.SessionID] = &SessionContext{}
			}
			ct.mu.Unlock()
		}
	})

	return ct
}

// Get returns the tracked context for a session.
func (ct *ContextTracker) Get(sessionID string) *SessionContext {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	return ct.contexts[sessionID]
}

// Resolve discovers the execution context and frame for a session.
// If not already tracked, triggers an AX tree probe to init the content process.
func (ct *ContextTracker) Resolve(sessionID string) (*SessionContext, error) {
	// Check if already tracked
	ct.mu.RLock()
	ctx := ct.contexts[sessionID]
	ct.mu.RUnlock()

	if ctx != nil && ctx.ExecutionContextID != "" && ctx.FrameID != "" {
		return ctx, nil
	}

	// Trigger content process init via AX tree probe
	ct.client.Call(sessionID, "Accessibility.getFullAXTree", mustJSONMap(map[string]interface{}{}))

	// Wait for the context to appear
	for i := 0; i < 20; i++ {
		time.Sleep(250 * time.Millisecond)
		ct.mu.RLock()
		ctx = ct.contexts[sessionID]
		ct.mu.RUnlock()
		if ctx != nil && ctx.ExecutionContextID != "" {
			return ctx, nil
		}
	}

	return nil, fmt.Errorf("could not discover execution context for session %s (timed out after 5s)", sessionID)
}

func mustJSONMap(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
