package recording

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRecordAndGetTimeline(t *testing.T) {
	r := NewRecorder()

	data1 := json.RawMessage(`{"url":"https://example.com"}`)
	data2 := json.RawMessage(`{"x":100,"y":200}`)

	r.Record("agent-1", ActionNavigate, data1)
	time.Sleep(time.Millisecond) // ensure distinct timestamps
	r.Record("agent-1", ActionClick, data2)

	timeline := r.GetTimeline("agent-1")
	if len(timeline) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(timeline))
	}
	if timeline[0].Type != ActionNavigate {
		t.Errorf("expected first action navigate, got %s", timeline[0].Type)
	}
	if timeline[1].Type != ActionClick {
		t.Errorf("expected second action click, got %s", timeline[1].Type)
	}
	if !timeline[0].Timestamp.Before(timeline[1].Timestamp) {
		t.Error("expected actions sorted by timestamp")
	}
}

func TestExportReturnsValidJSON(t *testing.T) {
	r := NewRecorder()

	r.Record("agent-1", ActionNavigate, json.RawMessage(`{"url":"https://example.com"}`))
	r.Record("agent-1", ActionClick, json.RawMessage(`{"x":50,"y":75}`))

	data, err := r.Export("agent-1")
	if err != nil {
		t.Fatalf("Export error: %v", err)
	}

	var actions []Action
	if err := json.Unmarshal(data, &actions); err != nil {
		t.Fatalf("Export returned invalid JSON: %v", err)
	}
	if len(actions) != 2 {
		t.Errorf("expected 2 actions in export, got %d", len(actions))
	}
}

func TestExportEmptyTimeline(t *testing.T) {
	r := NewRecorder()

	data, err := r.Export("nonexistent")
	if err != nil {
		t.Fatalf("Export error: %v", err)
	}

	var actions []Action
	if err := json.Unmarshal(data, &actions); err != nil {
		t.Fatalf("Export returned invalid JSON: %v", err)
	}
	if len(actions) != 0 {
		t.Errorf("expected empty array, got %d actions", len(actions))
	}
}

func TestClearRemovesActions(t *testing.T) {
	r := NewRecorder()

	r.Record("agent-1", ActionNavigate, json.RawMessage(`{"url":"https://example.com"}`))
	r.Record("agent-1", ActionClick, json.RawMessage(`{"x":10,"y":20}`))

	r.Clear("agent-1")

	timeline := r.GetTimeline("agent-1")
	if len(timeline) != 0 {
		t.Errorf("expected empty timeline after clear, got %d actions", len(timeline))
	}
}

func TestEmptyTimeline(t *testing.T) {
	r := NewRecorder()

	timeline := r.GetTimeline("agent-1")
	if timeline != nil {
		t.Errorf("expected nil timeline for unknown agent, got %v", timeline)
	}
}

func TestMultipleAgentsDontInterfere(t *testing.T) {
	r := NewRecorder()

	r.Record("agent-1", ActionNavigate, json.RawMessage(`{"url":"https://a.com"}`))
	r.Record("agent-2", ActionClick, json.RawMessage(`{"x":1,"y":2}`))
	r.Record("agent-1", ActionScroll, json.RawMessage(`{"deltaY":100}`))

	t1 := r.GetTimeline("agent-1")
	t2 := r.GetTimeline("agent-2")

	if len(t1) != 2 {
		t.Errorf("agent-1: expected 2 actions, got %d", len(t1))
	}
	if len(t2) != 1 {
		t.Errorf("agent-2: expected 1 action, got %d", len(t2))
	}

	// Verify agent-1 actions are correct types
	if t1[0].Type != ActionNavigate || t1[1].Type != ActionScroll {
		t.Errorf("agent-1: unexpected action types: %s, %s", t1[0].Type, t1[1].Type)
	}
	if t2[0].Type != ActionClick {
		t.Errorf("agent-2: expected click, got %s", t2[0].Type)
	}

	// Clear agent-1 should not affect agent-2
	r.Clear("agent-1")
	if len(r.GetTimeline("agent-2")) != 1 {
		t.Error("clearing agent-1 affected agent-2")
	}
}
