package spawntrace

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
)

// TestMain enables tracing for this package's tests before any Enabled() call
// memoizes the value, so the enabled-path formatting can be exercised.
func TestMain(m *testing.M) {
	os.Setenv("VULPINE_SPAWN_TRACE", "1")
	os.Exit(m.Run())
}

func TestNilTraceIsNoop(t *testing.T) {
	// A nil *Trace is what Start returns when tracing is disabled. Every method
	// must be a safe no-op so call sites never need to nil-check.
	var tr *Trace
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	tr.Lap("stage")
	tr.Mark("event")
	tr.End()

	if buf.Len() != 0 {
		t.Fatalf("nil trace produced output: %q", buf.String())
	}
}

func TestEnabledTraceLogsStages(t *testing.T) {
	if !Enabled() {
		t.Fatal("expected tracing enabled via VULPINE_SPAWN_TRACE")
	}

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	tr := Start("orchestrator.SpawnCitizen", "abc123")
	if tr == nil {
		t.Fatal("Start returned nil while tracing enabled")
	}
	tr.Lap("acquire-context")
	tr.Mark("handoff")
	tr.End()

	out := buf.String()
	for _, want := range []string{
		"spawntrace orchestrator.SpawnCitizen id=abc123 START",
		"id=abc123 stage=acquire-context dur=",
		"id=abc123 mark=handoff at=",
		"id=abc123 TOTAL dur=",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in trace output:\n%s", want, out)
		}
	}
}
