package mcp

import (
	"strings"
	"testing"
	"time"

	"vulpineos/internal/juggler"
	"vulpineos/internal/testutil"
)

func TestContextTrackerRemovesSessionOnDetach(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	client := juggler.NewClient(transport)
	t.Cleanup(func() { _ = client.Close() })

	tracker := NewContextTracker(client)
	t.Cleanup(tracker.Close)
	transport.InjectEvent("", "Browser.attachedToTarget", map[string]any{
		"sessionId": "session-1",
	})
	waitForContext(t, tracker, "session-1", true)

	transport.InjectEvent("", "Browser.detachedFromTarget", map[string]any{
		"sessionId": "session-1",
		"targetId":  "target-1",
	})
	waitForContext(t, tracker, "session-1", false)
}

func TestContextTrackerCloseUnsubscribesEvents(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	client := juggler.NewClient(transport)
	t.Cleanup(func() { _ = client.Close() })

	tracker := NewContextTracker(client)
	tracker.Close()
	transport.InjectEvent("", "Browser.attachedToTarget", map[string]any{
		"sessionId": "session-closed",
	})
	time.Sleep(50 * time.Millisecond)

	if got := tracker.Get("session-closed"); got != nil {
		t.Fatalf("closed tracker recorded context: %+v", got)
	}
}

func TestServerCleansSessionStateOnDetach(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	client := juggler.NewClient(transport)
	t.Cleanup(func() { _ = client.Close() })

	server := NewServer(client)
	t.Cleanup(server.Close)
	server.screenshots.Set("session-cleanup", "screenshot-data")
	recordSnapshotProfile("session-cleanup", defaultSnapshotProfile(), true)

	transport.InjectEvent("", "Browser.detachedFromTarget", map[string]any{
		"sessionId": "session-cleanup",
	})

	waitForSessionCleanup(t, server.screenshots, "session-cleanup")
}

func TestCloseContextCleansTrackedSessionState(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondJSON("Browser.removeBrowserContext", map[string]any{})
	client := juggler.NewClient(transport)
	t.Cleanup(func() { _ = client.Close() })

	server := NewServer(client)
	t.Cleanup(server.Close)
	transport.InjectEvent("", "Browser.attachedToTarget", map[string]any{
		"sessionId": "session-close",
		"targetInfo": map[string]any{
			"browserContextId": "context-close",
		},
	})
	waitForContext(t, server.tracker, "session-close", true)
	server.screenshots.Set("session-close", "screenshot-data")
	recordSnapshotProfile("session-close", defaultSnapshotProfile(), true)

	result, err := handleToolCallFull(
		t.Context(),
		client,
		server.tracker,
		server.screenshots,
		"vulpine_close_context",
		[]byte(`{"contextId":"context-close"}`),
	)
	if err != nil {
		t.Fatalf("close context returned error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("close context result = %+v", result)
	}

	waitForSessionCleanup(t, server.screenshots, "session-close")
}

func TestContextTrackerResolveReturnsAXProbeError(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondError("Accessibility.getFullAXTree", "target detached")
	client := juggler.NewClient(transport)
	t.Cleanup(func() { _ = client.Close() })

	tracker := NewContextTracker(client)
	t.Cleanup(tracker.Close)

	started := time.Now()
	_, err := tracker.Resolve("session-missing")
	if err == nil || !strings.Contains(err.Error(), "target detached") {
		t.Fatalf("Resolve error = %v, want AX probe error", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Resolve took %s after AX probe error", elapsed)
	}
}

func TestContextTrackerResolveRequiresFrameID(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondJSON("Accessibility.getFullAXTree", map[string]any{})
	client := juggler.NewClient(transport)
	t.Cleanup(func() { _ = client.Close() })

	tracker := NewContextTracker(client)
	t.Cleanup(tracker.Close)
	tracker.mu.Lock()
	tracker.contexts["session-no-frame"] = &SessionContext{ExecutionContextID: "exec-1"}
	tracker.mu.Unlock()

	_, err := tracker.Resolve("session-no-frame")
	if err == nil || !strings.Contains(err.Error(), "could not discover") {
		t.Fatalf("Resolve error = %v, want missing frame timeout", err)
	}
}

func TestContextTrackerClearsExecutionContextOnContextsCleared(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	client := juggler.NewClient(transport)
	t.Cleanup(func() { _ = client.Close() })

	tracker := NewContextTracker(client)
	t.Cleanup(tracker.Close)
	tracker.mu.Lock()
	tracker.contexts["session-clear"] = &SessionContext{
		ExecutionContextID: "exec-old",
		FrameID:            "frame-main",
	}
	tracker.mu.Unlock()

	transport.InjectEvent("session-clear", "Runtime.executionContextsCleared", map[string]any{})

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		ctx := tracker.Get("session-clear")
		if ctx != nil && ctx.ExecutionContextID == "" && ctx.FrameID == "frame-main" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("execution context was not cleared; got %+v", tracker.Get("session-clear"))
}

func TestContextTrackerKeepsMainExecutionContextWhenSubframeContextAppears(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	client := juggler.NewClient(transport)
	t.Cleanup(func() { _ = client.Close() })

	tracker := NewContextTracker(client)
	t.Cleanup(tracker.Close)

	transport.InjectEvent("session-frames", "Page.frameAttached", map[string]any{
		"frameId": "frame-main",
	})
	transport.InjectEvent("session-frames", "Runtime.executionContextCreated", map[string]any{
		"executionContextId": "exec-main",
		"auxData": map[string]any{
			"frameId": "frame-main",
		},
	})
	waitForTrackedContext(t, tracker, "session-frames", "frame-main", "exec-main")

	transport.InjectEvent("session-frames", "Page.frameAttached", map[string]any{
		"frameId":       "frame-child",
		"parentFrameId": "frame-main",
	})
	transport.InjectEvent("session-frames", "Runtime.executionContextCreated", map[string]any{
		"executionContextId": "exec-child",
		"auxData": map[string]any{
			"frameId": "frame-child",
		},
	})
	transport.InjectEvent("session-frames", "Runtime.executionContextCreated", map[string]any{
		"executionContextId": "exec-worker",
	})
	time.Sleep(50 * time.Millisecond)

	ctx := tracker.Get("session-frames")
	if ctx == nil {
		t.Fatal("tracked context missing")
	}
	if ctx.FrameID != "frame-main" || ctx.ExecutionContextID != "exec-main" {
		t.Fatalf("tracked context = %+v, want main frame/main execution context", ctx)
	}
}

func waitForContext(t *testing.T, tracker *ContextTracker, sessionID string, wantPresent bool) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		present := tracker.Get(sessionID) != nil
		if present == wantPresent {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("context presence for %s did not become %v", sessionID, wantPresent)
}

func waitForTrackedContext(t *testing.T, tracker *ContextTracker, sessionID, frameID, executionContextID string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		ctx := tracker.Get(sessionID)
		if ctx != nil && ctx.FrameID == frameID && ctx.ExecutionContextID == executionContextID {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("context for %s did not become frame=%s exec=%s; got %+v", sessionID, frameID, executionContextID, tracker.Get(sessionID))
}

func waitForSessionCleanup(t *testing.T, screenshots *ScreenshotTracker, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		snapshotRetryState.Lock()
		_, hasSnapshotRetry := snapshotRetryState.bySession[sessionID]
		snapshotRetryState.Unlock()
		if screenshots.Get(sessionID) == "" && !hasSnapshotRetry {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session state for %s was not cleaned up", sessionID)
}
