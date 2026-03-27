package recording

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestShowFormatsTimeline(t *testing.T) {
	r := NewRecorder()
	var buf bytes.Buffer
	v := NewViewer(r, &buf)

	r.Record("agent-1", ActionNavigate, json.RawMessage(`{"url":"https://example.com"}`))
	time.Sleep(2 * time.Millisecond)
	r.Record("agent-1", ActionClick, json.RawMessage(`{"selector":"button#submit"}`))
	time.Sleep(2 * time.Millisecond)
	r.Record("agent-1", ActionType_, json.RawMessage(`{"selector":"input#search","text":"hello world"}`))

	if err := v.Show("agent-1"); err != nil {
		t.Fatalf("Show error: %v", err)
	}

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), output)
	}

	// First line: navigate
	if !strings.Contains(lines[0], "NAVIGATE") {
		t.Errorf("line 0 missing NAVIGATE: %s", lines[0])
	}
	if !strings.Contains(lines[0], "https://example.com") {
		t.Errorf("line 0 missing URL: %s", lines[0])
	}
	if !strings.Contains(lines[0], "[00:00.000]") {
		t.Errorf("line 0 should start at 00:00.000: %s", lines[0])
	}

	// Second line: click
	if !strings.Contains(lines[1], "CLICK") {
		t.Errorf("line 1 missing CLICK: %s", lines[1])
	}
	if !strings.Contains(lines[1], "button#submit") {
		t.Errorf("line 1 missing selector: %s", lines[1])
	}

	// Third line: type
	if !strings.Contains(lines[2], "TYPE") {
		t.Errorf("line 2 missing TYPE: %s", lines[2])
	}
	if !strings.Contains(lines[2], `"hello world"`) {
		t.Errorf("line 2 missing text: %s", lines[2])
	}
}

func TestShowEmptyTimeline(t *testing.T) {
	r := NewRecorder()
	var buf bytes.Buffer
	v := NewViewer(r, &buf)

	if err := v.Show("nonexistent"); err != nil {
		t.Fatalf("Show error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "No actions recorded") {
		t.Errorf("expected 'No actions recorded' message, got: %s", output)
	}
}

func TestShowScrollAction(t *testing.T) {
	r := NewRecorder()
	var buf bytes.Buffer
	v := NewViewer(r, &buf)

	r.Record("agent-1", ActionScroll, json.RawMessage(`{"deltaX":0,"deltaY":500}`))

	if err := v.Show("agent-1"); err != nil {
		t.Fatalf("Show error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "SCROLL") {
		t.Errorf("missing SCROLL: %s", output)
	}
	if !strings.Contains(output, "(0, 500)") {
		t.Errorf("missing scroll coords: %s", output)
	}
}

func TestShowScreenshotAction(t *testing.T) {
	r := NewRecorder()
	var buf bytes.Buffer
	v := NewViewer(r, &buf)

	r.Record("agent-1", ActionScreenshot, json.RawMessage(`{"width":1280,"height":720}`))

	if err := v.Show("agent-1"); err != nil {
		t.Fatalf("Show error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "SCREENSHOT") {
		t.Errorf("missing SCREENSHOT: %s", output)
	}
	if !strings.Contains(output, "(1280x720)") {
		t.Errorf("missing dimensions: %s", output)
	}
}

func TestShowClickWithCoordinates(t *testing.T) {
	r := NewRecorder()
	var buf bytes.Buffer
	v := NewViewer(r, &buf)

	r.Record("agent-1", ActionClick, json.RawMessage(`{"x":100,"y":200}`))

	if err := v.Show("agent-1"); err != nil {
		t.Fatalf("Show error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "(100, 200)") {
		t.Errorf("missing click coordinates: %s", output)
	}
}

func TestShowEvaluateAction(t *testing.T) {
	r := NewRecorder()
	var buf bytes.Buffer
	v := NewViewer(r, &buf)

	r.Record("agent-1", ActionEvaluate, json.RawMessage(`{"expression":"document.title"}`))

	if err := v.Show("agent-1"); err != nil {
		t.Fatalf("Show error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "EVALUATE") {
		t.Errorf("missing EVALUATE: %s", output)
	}
	if !strings.Contains(output, "document.title") {
		t.Errorf("missing expression: %s", output)
	}
}
