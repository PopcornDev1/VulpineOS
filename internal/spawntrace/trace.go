// Package spawntrace provides lightweight, env-gated stage timing for the
// agent spawn → first-action lifecycle. It is a measurement aid: when the
// VULPINE_SPAWN_TRACE environment variable is unset/falsey every call is a
// cheap no-op (no allocation, no output), so it is safe to leave the
// instrumentation in place permanently without changing runtime behavior.
//
// Output goes through the standard log package so traces land in the same
// destinations as the rest of the runtime (e.g. ~/.vulpineos/logs/local-tui.log
// for local TUI, stderr otherwise). Each line is prefixed "spawntrace" and
// carries a correlation id so concurrent spawns can be told apart:
//
//	spawntrace orchestrator.SpawnCitizen id=ab12cd START
//	spawntrace orchestrator.SpawnCitizen id=ab12cd stage=acquire-context dur=3.2ms
//	spawntrace orchestrator.SpawnCitizen id=ab12cd stage=apply-citizen dur=812.4ms
//	spawntrace orchestrator.SpawnCitizen id=ab12cd TOTAL dur=1043.7ms
package spawntrace

import (
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	enabledOnce sync.Once
	enabled     bool
)

// Enabled reports whether spawn tracing is turned on. The VULPINE_SPAWN_TRACE
// environment variable is read once on first use; values "1", "true", "yes",
// and "on" (case-insensitive) enable tracing.
func Enabled() bool {
	enabledOnce.Do(func() {
		switch strings.ToLower(strings.TrimSpace(os.Getenv("VULPINE_SPAWN_TRACE"))) {
		case "1", "true", "yes", "on":
			enabled = true
		}
	})
	return enabled
}

// Trace is a running timer for one named operation. A nil *Trace (returned when
// tracing is disabled) is valid and all methods are no-ops on it.
type Trace struct {
	name  string
	id    string
	start time.Time
	last  time.Time
}

// Start begins a trace for the named operation, correlated by id. When tracing
// is disabled it returns nil, which all Trace methods accept as a no-op.
func Start(name, id string) *Trace {
	if !Enabled() {
		return nil
	}
	now := time.Now()
	log.Printf("spawntrace %s id=%s START", name, id)
	return &Trace{name: name, id: id, start: now, last: now}
}

// Lap logs the elapsed time since the previous Lap (or since Start) and labels
// it as the given stage. Use it to attribute time to sequential sub-steps.
func (t *Trace) Lap(stage string) {
	if t == nil {
		return
	}
	now := time.Now()
	log.Printf("spawntrace %s id=%s stage=%s dur=%.1fms", t.name, t.id, stage, float64(now.Sub(t.last))/float64(time.Millisecond))
	t.last = now
}

// Mark logs an instantaneous checkpoint (offset from Start) without resetting
// the lap clock. Useful for noting when an async boundary is crossed.
func (t *Trace) Mark(event string) {
	if t == nil {
		return
	}
	log.Printf("spawntrace %s id=%s mark=%s at=%.1fms", t.name, t.id, event, float64(time.Since(t.start))/float64(time.Millisecond))
}

// End logs the total elapsed time since Start.
func (t *Trace) End() {
	if t == nil {
		return
	}
	log.Printf("spawntrace %s id=%s TOTAL dur=%.1fms", t.name, t.id, float64(time.Since(t.start))/float64(time.Millisecond))
}
