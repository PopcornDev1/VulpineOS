package kernel

import (
	"sync"
	"time"
)

// Watchdog monitors the Camoufox kernel process and handles crashes
// with optional auto-restart.
type Watchdog struct {
	kernel      *Kernel
	config      Config
	onCrash     func()
	autoRestart bool
	maxRestarts int
	restarts    int
	mu          sync.Mutex
	done        chan struct{}
	closeOnce   sync.Once
}

// NewWatchdog creates a watchdog for the given kernel.
// If autoRestart is true, the watchdog will attempt to restart the kernel
// on crash up to 3 times.
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
			if w.kernel.Running() {
				continue
			}

			// Kernel is not running — fire crash callback
			w.mu.Lock()
			cb := w.onCrash
			w.mu.Unlock()

			if cb != nil {
				cb()
			}

			// Attempt auto-restart if enabled
			w.mu.Lock()
			canRestart := w.autoRestart && w.restarts < w.maxRestarts
			cfg := w.config
			w.mu.Unlock()

			if canRestart {
				if err := w.kernel.Start(cfg); err == nil {
					w.mu.Lock()
					w.restarts++
					w.mu.Unlock()
				}
			}
		}
	}
}
