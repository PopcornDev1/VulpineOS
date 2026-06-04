package mcp

import (
	"context"
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
	BrowserContextID   string
}

// ContextTracker subscribes to Juggler events and tracks execution contexts
// and frame IDs per session. Required because Juggler's Runtime.evaluate needs
// executionContextId and Page.navigate needs frameId.
type ContextTracker struct {
	mu       sync.RWMutex
	contexts map[string]*SessionContext // sessionID → context
	client   *juggler.Client
	cancels  []func()
}

func cloneSessionContext(ctx *SessionContext) *SessionContext {
	if ctx == nil {
		return nil
	}
	dup := *ctx
	return &dup
}

// NewContextTracker creates a tracker and subscribes to the relevant events.
func NewContextTracker(client *juggler.Client) *ContextTracker {
	ct := &ContextTracker{
		contexts: make(map[string]*SessionContext),
		client:   client,
	}

	ct.subscribe("Runtime.executionContextCreated", func(sessionID string, params json.RawMessage) {
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
		if ev.AuxData.FrameID == "" {
			return
		}
		if ctx.FrameID != "" && ctx.FrameID != ev.AuxData.FrameID {
			return
		}
		ctx.FrameID = ev.AuxData.FrameID
		if ev.ExecutionContextID != "" {
			ctx.ExecutionContextID = ev.ExecutionContextID
		}
	})

	ct.subscribe("Runtime.executionContextDestroyed", func(sessionID string, params json.RawMessage) {
		var ev struct {
			ExecutionContextID string `json:"executionContextId"`
		}
		json.Unmarshal(params, &ev)

		ct.mu.Lock()
		defer ct.mu.Unlock()

		ctx := ct.contexts[sessionID]
		if ctx != nil && ctx.ExecutionContextID == ev.ExecutionContextID {
			ctx.ExecutionContextID = ""
		}
	})

	ct.subscribe("Runtime.executionContextsCleared", func(sessionID string, _ json.RawMessage) {
		ct.mu.Lock()
		defer ct.mu.Unlock()

		if ctx := ct.contexts[sessionID]; ctx != nil {
			ctx.ExecutionContextID = ""
		}
	})

	ct.subscribe("Page.frameAttached", func(sessionID string, params json.RawMessage) {
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
			if ctx.FrameID != "" && ctx.FrameID != ev.FrameID {
				ctx.ExecutionContextID = ""
			}
			ctx.FrameID = ev.FrameID
			ct.mu.Unlock()
		}
	})

	ct.subscribe("Browser.attachedToTarget", func(_ string, params json.RawMessage) {
		var ev struct {
			SessionID  string `json:"sessionId"`
			TargetInfo struct {
				BrowserContextID string `json:"browserContextId"`
			} `json:"targetInfo"`
		}
		json.Unmarshal(params, &ev)
		if ev.SessionID != "" {
			ct.mu.Lock()
			ctx, ok := ct.contexts[ev.SessionID]
			if !ok {
				ctx = &SessionContext{}
				ct.contexts[ev.SessionID] = ctx
			}
			if ev.TargetInfo.BrowserContextID != "" {
				ctx.BrowserContextID = ev.TargetInfo.BrowserContextID
			}
			ct.mu.Unlock()
		}
	})

	ct.subscribe("Browser.detachedFromTarget", func(_ string, params json.RawMessage) {
		var ev struct {
			SessionID string `json:"sessionId"`
		}
		json.Unmarshal(params, &ev)
		if ev.SessionID != "" {
			ct.mu.Lock()
			delete(ct.contexts, ev.SessionID)
			ct.mu.Unlock()
		}
	})

	return ct
}

func (ct *ContextTracker) subscribe(event string, handler juggler.EventHandler) {
	cancel := ct.client.SubscribeWithCancel(event, handler)
	ct.cancels = append(ct.cancels, cancel)
}

// Close removes the tracker's event subscriptions.
func (ct *ContextTracker) Close() {
	ct.mu.Lock()
	cancels := append([]func(){}, ct.cancels...)
	ct.cancels = nil
	ct.contexts = make(map[string]*SessionContext)
	ct.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
}

// Get returns the tracked context for a session.
func (ct *ContextTracker) Get(sessionID string) *SessionContext {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	return cloneSessionContext(ct.contexts[sessionID])
}

func (ct *ContextTracker) SessionsForContext(contextID string) []string {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	var sessions []string
	for sessionID, ctx := range ct.contexts {
		if ctx != nil && ctx.BrowserContextID == contextID {
			sessions = append(sessions, sessionID)
		}
	}
	return sessions
}

func (ct *ContextTracker) RemoveSession(sessionID string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	delete(ct.contexts, sessionID)
}

// InvalidateExecutionContext forces the next Resolve call for the
// given session to wait for a fresh execution context event. This is
// needed across navigations, where the old context may briefly remain
// readable while already pointing at the previous document.
func (ct *ContextTracker) InvalidateExecutionContext(sessionID string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	if ctx := ct.contexts[sessionID]; ctx != nil {
		ctx.ExecutionContextID = ""
	}
}

// Resolve discovers the execution context and frame for a session.
// If not already tracked, triggers an AX tree probe to init the content process.
func (ct *ContextTracker) Resolve(sessionID string) (*SessionContext, error) {
	// Check if already tracked
	ct.mu.RLock()
	ctx := cloneSessionContext(ct.contexts[sessionID])
	ct.mu.RUnlock()

	if ctx != nil && ctx.ExecutionContextID != "" && ctx.FrameID != "" {
		return ctx, nil
	}

	// Trigger content process init via AX tree probe.
	probeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, err := ct.client.CallWithContext(probeCtx, sessionID, "Accessibility.getFullAXTree", mustJSONMap(map[string]interface{}{}))
	cancel()
	if err != nil {
		return nil, fmt.Errorf("probe accessibility tree for session %s: %w", sessionID, err)
	}

	// Wait for the context to appear
	for i := 0; i < 20; i++ {
		time.Sleep(250 * time.Millisecond)
		ct.mu.RLock()
		ctx = cloneSessionContext(ct.contexts[sessionID])
		ct.mu.RUnlock()
		if ctx != nil && ctx.ExecutionContextID != "" && ctx.FrameID != "" {
			return ctx, nil
		}
	}

	return nil, fmt.Errorf("could not discover execution context for session %s (timed out after 5s)", sessionID)
}

func (ct *ContextTracker) ResolveFrame(sessionID string) (*SessionContext, error) {
	ct.mu.RLock()
	ctx := cloneSessionContext(ct.contexts[sessionID])
	ct.mu.RUnlock()
	if ctx != nil && ctx.FrameID != "" {
		return ctx, nil
	}

	probeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, err := ct.client.CallWithContext(probeCtx, sessionID, "Accessibility.getFullAXTree", mustJSONMap(map[string]interface{}{}))
	cancel()
	if err != nil {
		return nil, fmt.Errorf("probe accessibility tree for session %s: %w", sessionID, err)
	}

	for i := 0; i < 20; i++ {
		time.Sleep(250 * time.Millisecond)
		ct.mu.RLock()
		ctx = cloneSessionContext(ct.contexts[sessionID])
		ct.mu.RUnlock()
		if ctx != nil && ctx.FrameID != "" {
			return ctx, nil
		}
	}

	return nil, fmt.Errorf("could not discover frame for session %s (timed out after 5s)", sessionID)
}

func mustJSONMap(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
