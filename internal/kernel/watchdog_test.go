package kernel

import (
	"testing"
)

func TestNewWatchdog(t *testing.T) {
	k := New()
	w := NewWatchdog(k, true)
	if w == nil {
		t.Fatal("NewWatchdog returned nil")
	}
	if w.kernel != k {
		t.Fatal("kernel not set")
	}
	if !w.autoRestart {
		t.Fatal("autoRestart should be true")
	}
	if w.maxRestarts != 3 {
		t.Fatal("default maxRestarts should be 3")
	}
}

func TestWatchdog_OnCrash(t *testing.T) {
	k := New()
	w := NewWatchdog(k, false)

	called := false
	w.OnCrash(func() { called = true })

	w.mu.Lock()
	cb := w.onCrash
	w.mu.Unlock()

	if cb == nil {
		t.Fatal("OnCrash callback not registered")
	}
	cb()
	if !called {
		t.Fatal("callback was not invoked")
	}
}

func TestWatchdog_RestartsStartsAtZero(t *testing.T) {
	k := New()
	w := NewWatchdog(k, true)
	if w.Restarts() != 0 {
		t.Fatalf("expected 0 restarts, got %d", w.Restarts())
	}
}

func TestWatchdog_StopDoesNotPanic(t *testing.T) {
	k := New()
	w := NewWatchdog(k, false)
	w.Start()
	// Stop twice — should not panic
	w.Stop()
	w.Stop()
}

func TestWatchdog_SetMaxRestarts(t *testing.T) {
	k := New()
	w := NewWatchdog(k, true)
	w.SetMaxRestarts(5)
	w.mu.Lock()
	if w.maxRestarts != 5 {
		t.Fatalf("expected maxRestarts=5, got %d", w.maxRestarts)
	}
	w.mu.Unlock()
}

func TestWatchdog_SetConfig(t *testing.T) {
	k := New()
	w := NewWatchdog(k, true)
	cfg := Config{BinaryPath: "/usr/bin/test", Headless: true}
	w.SetConfig(cfg)
	w.mu.Lock()
	if w.config.BinaryPath != "/usr/bin/test" {
		t.Fatal("config not stored")
	}
	if !w.config.Headless {
		t.Fatal("headless not stored")
	}
	w.mu.Unlock()
}
