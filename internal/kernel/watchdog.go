package kernel

import (
	"fmt"
	"sync"
	"time"
)

// Watchdog monitors the Vulpine browser kernel process and handles crashes.
// with optional auto-restart.
type Watchdog struct {
	kernel      *Kernel
	config      Config
	onCrash     func()
	onEvent     func(WatchdogEvent)
	onRestart   func(*Kernel) error
	autoRestart bool
	maxRestarts int
	restarts    int
	attempts    int
	down        bool
	mu          sync.Mutex
	done        chan struct{}
	closeOnce   sync.Once
}

// WatchdogEvent reports a kernel crash or restart attempt.
type WatchdogEvent struct {
	Type    string
	Attempt int
	Err     error
}

// NewWatchdog creates a watchdog for the given kernel.
// If autoRestart is true, the watchdog will attempt to restart the kernel on
// crash only after OnRestart has been configured. Restarting creates a new
// Juggler client, so callers must rewire dependent services in that callback.
func NewWatchdog(k *Kernel, autoRestart bool) *Watchdog {
	return &Watchdog{
		kernel:      k,
		autoRestart: autoRestart,
		maxRestarts: 3,
		done:        make(chan struct{}),
	}
}

// SetConfig stores the kernel config used for restarts.
func (w *Watchdog) SetConfig(cfg Config) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.config = cfg
}

// SetMaxRestarts overrides the default max restart count (3).
func (w *Watchdog) SetMaxRestarts(n int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.maxRestarts = n
}

// OnCrash registers a callback invoked when the kernel crashes.
func (w *Watchdog) OnCrash(fn func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onCrash = fn
}

// OnEvent registers a callback invoked for watchdog lifecycle events.
func (w *Watchdog) OnEvent(fn func(WatchdogEvent)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onEvent = fn
}

// OnRestart registers the callback invoked after a restarted kernel has been
// launched. Use this to re-enable Browser and rewire services that hold the old
// Juggler client. Without this callback, auto-restart is skipped.
func (w *Watchdog) OnRestart(fn func(*Kernel) error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onRestart = fn
}

// Restarts returns the number of auto-restarts performed so far.
func (w *Watchdog) Restarts() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.restarts
}

// Start begins monitoring the kernel in a background goroutine.
// It checks kernel.Running() every 2 seconds.
func (w *Watchdog) Start() {
	go w.monitor()
}

// Stop terminates the watchdog monitoring goroutine.
func (w *Watchdog) Stop() {
	w.closeOnce.Do(func() {
		close(w.done)
	})
}

func (w *Watchdog) monitor() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			w.check()
		}
	}
}

func (w *Watchdog) check() {
	if w.kernel.Running() {
		w.mu.Lock()
		w.down = false
		w.mu.Unlock()
		return
	}

	w.mu.Lock()
	if w.down {
		w.mu.Unlock()
		return
	}
	w.down = true
	cb := w.onCrash
	eventCb := w.onEvent
	w.mu.Unlock()

	// Kernel is not running — fire crash callback
	if cb != nil {
		cb()
	}
	if eventCb != nil {
		eventCb(WatchdogEvent{Type: "crashed"})
	}

	// Attempt auto-restart if enabled
	w.mu.Lock()
	canRestart := w.autoRestart && w.onRestart != nil && w.attempts < w.maxRestarts
	skipRestart := w.autoRestart && w.onRestart == nil
	cfg := w.config
	attempt := w.attempts + 1
	eventCb = w.onEvent
	restartCb := w.onRestart
	if canRestart {
		w.attempts++
	}
	w.mu.Unlock()

	if skipRestart {
		if eventCb != nil {
			eventCb(WatchdogEvent{Type: "restart_skipped", Attempt: attempt, Err: fmt.Errorf("restart handler not configured")})
		}
	} else if canRestart {
		err := w.kernel.Start(cfg)
		if err == nil && restartCb != nil {
			err = restartCb(w.kernel)
		}
		if err == nil {
			w.mu.Lock()
			w.restarts++
			w.down = false
			w.mu.Unlock()
			if eventCb != nil {
				eventCb(WatchdogEvent{Type: "restart_success", Attempt: attempt})
			}
		} else {
			_ = w.kernel.Stop()
			w.mu.Lock()
			w.down = false
			w.mu.Unlock()
			if eventCb != nil {
				eventCb(WatchdogEvent{Type: "restart_failed", Attempt: attempt, Err: err})
			}
		}
	}
}
