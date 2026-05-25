# NanoClaw Daemon Manager Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Start the NanoClaw daemon (`src/index.ts`) automatically on Vulpine startup so agents can communicate via the Unix socket.

**Architecture:** Create a new `Daemon` struct in `internal/nanoclaw/daemon.go` that manages the NanoClaw daemon process. Vulpine calls `daemon.Start()` during initialization, waits for `cli.sock` to appear, and manages the daemon lifecycle.

**Tech Stack:** Go (os/exec, time, sync), NanoClaw TypeScript daemon

---

### Task 1: Create Daemon struct and Start method

**Files:**
- Create: `internal/nanoclaw/daemon.go`
- Test: `internal/nanoclaw/daemon_test.go`

- [ ] **Step 1: Write the failing test**

```go
package nanoclaw

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDaemonStartCreatesSocket(t *testing.T) {
	// Skip if NanoClaw isn't installed
	mgr := NewManager("")
	if !mgr.NanoClawInstalled() {
		t.Skip("NanoClaw not installed")
	}

	daemon := NewDaemon("")

	// Start should succeed and create the socket
	err := daemon.Start()
	if err != nil {
		t.Fatalf("daemon.Start() error = %v", err)
	}
	defer daemon.Stop()

	// Verify socket exists
	socketPath := filepath.Join(GetNanoclawDir(), "data", "cli.sock")

	// Wait up to 10 seconds for socket
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(socketPath); err == nil {
			return // Success
		}
		time.Sleep(500 * time.Millisecond)
	}

	t.Fatal("cli.sock was not created within 10 seconds")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/nanoclaw -run TestDaemonStartCreatesSocket -v`
Expected: FAIL with "NewDaemon not defined"

- [ ] **Step 3: Write minimal implementation**

Create `internal/nanoclaw/daemon.go`:

```go
package nanoclaw

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"vulpineos/internal/config"
)

// Daemon manages the NanoClaw daemon process.
type Daemon struct {
	cmd        *exec.Cmd
	binary     string
	socketPath string
	mu         sync.Mutex
	exited     bool
	exitCh     chan error
}

// NewDaemon creates a new daemon manager.
func NewDaemon(binary string) *Daemon {
	return &Daemon{binary: binary}
}

// Start launches the NanoClaw daemon and waits for the socket to be ready.
func (d *Daemon) Start() error {
	d.mu.Lock()
	if d.cmd != nil && !d.exited {
		d.mu.Unlock()
		return nil // already running
	}
	d.mu.Unlock()

	nanoclawBin := d.binary
	if nanoclawBin == "" {
		mgr := NewManager("")
		nanoclawBin = mgr.findNanoclaw()
	}
	if nanoclawBin == "" {
		return fmt.Errorf("NanoClaw binary not found")
	}

	// Build command: run the daemon with vulpine profile
	args := []string{
		"--profile", "vulpine",
	}

	cmd := exec.Command(nanoclawBin, args...)

	// Inject OpenRouter env vars if configured
	if cfg, err := config.Load(); err == nil && cfg.Provider == "openrouter" {
		cmd.Env = append(os.Environ(),
			"OPENCODE_PROVIDER=openrouter",
			"OPENCODE_MODEL="+cfg.Model,
		)
	}

	// Log to temp file
	logPath := os.TempDir() + "/vulpineos-nanoclaw.log"
	if logFile, err := os.Create(logPath); err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start nanoclaw daemon: %w", err)
	}

	d.mu.Lock()
	d.cmd = cmd
	d.exited = false
	d.exitCh = make(chan error, 1)
	exitCh := d.exitCh
	d.mu.Unlock()

	go func() {
		err := cmd.Wait()
		d.mu.Lock()
		if d.cmd == cmd {
			d.exited = true
		}
		d.mu.Unlock()
		exitCh <- err
		close(exitCh)
	}()

	// Wait for socket to appear
	nanoclawDir := GetNanoclawDir()
	if nanoclawDir == "" {
		return fmt.Errorf("NanoClaw directory not found")
	}
	d.socketPath = filepath.Join(nanoclawDir, "data", "cli.sock")

	log.Printf("NanoClaw daemon starting, waiting for socket at %s", d.socketPath)

	for i := 0; i < 30; i++ { // 15 seconds max
		if _, err := os.Stat(d.socketPath); err == nil {
			log.Printf("NanoClaw daemon ready (socket found)")
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("NanoClaw daemon did not create socket within 15 seconds")
}

// Stop gracefully terminates the daemon.
func (d *Daemon) Stop() error {
	d.mu.Lock()
	if d.cmd == nil || d.exited {
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()

	if d.cmd.Process != nil {
		if err := d.cmd.Process.Signal(os.Interrupt); err != nil {
			d.cmd.Process.Kill()
		}
	}

	select {
	case <-d.exitCh:
		return nil
	case <-time.After(5 * time.Second):
		if d.cmd.Process != nil {
			d.cmd.Process.Kill()
		}
		return fmt.Errorf("daemon did not exit within 5 seconds")
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/nanoclaw -run TestDaemonStartCreatesSocket -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/nanoclaw/daemon.go internal/nanoclaw/daemon_test.go
git commit -m "feat: add NanoClaw daemon manager with Start/Stop lifecycle"
```

---

### Task 2: Integrate Daemon into Vulpine startup

**Files:**
- Modify: `cmd/vulpineos/main.go`

- [ ] **Step 1: Add daemon to main.go startup**

In `cmd/vulpineos/main.go`, find where the Gateway is started (around line 56-84 in `startGatewayIfAvailable`). Add daemon startup BEFORE the gateway:

```go
var startDaemonIfAvailable = func(cfg *config.Config, audit *runtimeaudit.Manager) *nanoclaw.Daemon {
	mgr := nanoclaw.NewManager("")
	if !mgr.NanoClawInstalled() {
		return nil
	}
	daemon := nanoclaw.NewDaemon("")
	if err := daemon.Start(); err != nil {
		log.Printf("Warning: NanoClaw daemon failed to start: %v (agents won't work)", err)
		if audit != nil {
			_, _ = audit.Log("nanoclaw", "error", "daemon_start_failed", "NanoClaw daemon failed to start", map[string]string{
				"error": err.Error(),
			})
		}
		return nil
	}
	if audit != nil {
		_, _ = audit.Log("nanoclaw", "info", "daemon_started", "NanoClaw daemon started", nil)
	}
	return daemon
}
```

Then in the main startup flow (around line 718 where orchestrator is created), add:

```go
// Start NanoClaw daemon before gateway
daemon := startDaemonIfAvailable(cfg, audit)
defer func() {
	if daemon != nil {
		daemon.Stop()
	}
}()
```

- [ ] **Step 2: Rebuild and test**

Run: `go build -o vulpineos ./cmd/vulpineos`
Expected: Build succeeds

- [ ] **Step 3: Commit**

```bash
git add cmd/vulpineos/main.go
git commit -m "feat: start NanoClaw daemon on Vulpine startup"
```

---

### Task 3: Verify end-to-end

- [ ] **Step 1: Run Vulpine and check daemon starts**

Run: `./vulpineos`
Check logs for: `NanoClaw daemon starting, waiting for socket at...`
Check logs for: `NanoClaw daemon ready (socket found)`

- [ ] **Step 2: Create agent with OpenRouter**

In Vulpine UI, create agent with provider=OpenRouter, model=openrouter/free
Expected: Agent starts successfully and responds to messages

- [ ] **Step 3: Commit any fixes**

```bash
git add -A
git commit -m "fix: end-to-end verification fixes"
```
