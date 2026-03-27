package slog

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestLogger_JSONOutput(t *testing.T) {
	var buf bytes.Buffer
	log := New("kernel", &buf)

	log.Info("started", "pid", 1234)

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}

	if entry["level"] != "info" {
		t.Fatalf("expected level=info, got %v", entry["level"])
	}
	if entry["component"] != "kernel" {
		t.Fatalf("expected component=kernel, got %v", entry["component"])
	}
	if entry["msg"] != "started" {
		t.Fatalf("expected msg=started, got %v", entry["msg"])
	}
	if entry["pid"] != float64(1234) {
		t.Fatalf("expected pid=1234, got %v", entry["pid"])
	}
	if _, ok := entry["ts"]; !ok {
		t.Fatal("missing ts field")
	}
}

func TestLogger_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log := New("test", &buf)
	log.SetLevel(Warn)

	log.Debug("should be dropped")
	log.Info("should be dropped too")
	log.Warn("visible")
	log.Error("also visible")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %s", len(lines), buf.String())
	}

	var entry map[string]interface{}
	json.Unmarshal([]byte(lines[0]), &entry)
	if entry["level"] != "warn" {
		t.Fatalf("first line should be warn, got %v", entry["level"])
	}
}

func TestLogger_Component(t *testing.T) {
	var buf bytes.Buffer
	log := New("pool", &buf)
	log.Info("test")

	var entry map[string]interface{}
	json.Unmarshal(buf.Bytes(), &entry)
	if entry["component"] != "pool" {
		t.Fatalf("expected component=pool, got %v", entry["component"])
	}
}

func TestLogger_With(t *testing.T) {
	var buf bytes.Buffer
	log := New("test", &buf)
	child := log.With("request_id", "abc-123")

	child.Info("handled")

	var entry map[string]interface{}
	json.Unmarshal(buf.Bytes(), &entry)
	if entry["request_id"] != "abc-123" {
		t.Fatalf("expected request_id=abc-123, got %v", entry["request_id"])
	}
}

func TestLogger_WithDoesNotMutateParent(t *testing.T) {
	var buf bytes.Buffer
	log := New("test", &buf)
	_ = log.With("extra", "value")

	log.Info("parent")

	var entry map[string]interface{}
	json.Unmarshal(buf.Bytes(), &entry)
	if _, ok := entry["extra"]; ok {
		t.Fatal("parent logger should not have child's field")
	}
}

func TestLogger_AllLevels(t *testing.T) {
	var buf bytes.Buffer
	log := New("test", &buf)
	log.SetLevel(Debug)

	log.Debug("d")
	log.Info("i")
	log.Warn("w")
	log.Error("e")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(lines))
	}

	expected := []string{"debug", "info", "warn", "error"}
	for i, line := range lines {
		var entry map[string]interface{}
		json.Unmarshal([]byte(line), &entry)
		if entry["level"] != expected[i] {
			t.Fatalf("line %d: expected level=%s, got %v", i, expected[i], entry["level"])
		}
	}
}

func TestLevel_String(t *testing.T) {
	tests := []struct {
		level Level
		want  string
	}{
		{Debug, "debug"},
		{Info, "info"},
		{Warn, "warn"},
		{Error, "error"},
		{Level(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.level.String(); got != tt.want {
			t.Errorf("Level(%d).String() = %q, want %q", tt.level, got, tt.want)
		}
	}
}
