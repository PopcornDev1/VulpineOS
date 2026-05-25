package nanoclaw

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"vulpineos/internal/config"
)

// Daemon owns the long-running NanoClaw socket process used by VulpineOS.
type Daemon struct {
	mu            sync.Mutex
	binary        string
	nanoclawDir   string
	socketPath    string
	logPath       string
	logFile       *os.File
	cmd           *exec.Cmd
	exited        bool
	exitCh        chan error
	waitReadyFunc func() error
}

// NewDaemon creates a daemon manager rooted in the VulpineOS-owned NanoClaw dir.
func NewDaemon(binary string, nanoclawDir ...string) *Daemon {
	dir := VulpineNanoclawDir()
	if len(nanoclawDir) > 0 && strings.TrimSpace(nanoclawDir[0]) != "" {
		dir = strings.TrimSpace(nanoclawDir[0])
	}
	dataDir := filepath.Join(dir, "data")
	return &Daemon{
		binary:      strings.TrimSpace(binary),
		nanoclawDir: dir,
		socketPath:  filepath.Join(dataDir, "cli.sock"),
		logPath:     filepath.Join(dataDir, "nanoclaw.log"),
	}
}

// Start launches NanoClaw and waits until its CLI socket is present.
func (d *Daemon) Start() error {
	d.mu.Lock()
	if d.cmd != nil && !d.exited {
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()

	nanoclawBin := strings.TrimSpace(d.binary)
	if nanoclawBin == "" {
		nanoclawBin = NewManager("").findNanoClaw()
	}
	if nanoclawBin == "" {
		return fmt.Errorf("NanoClaw binary not found")
	}

	dataDir := filepath.Join(d.nanoclawDir, "data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("create NanoClaw data dir: %w", err)
	}
	if err := os.Remove(d.socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale NanoClaw socket: %w", err)
	}

	cmd := exec.Command(nanoclawBin, "run")
	cmd.Env = daemonEnv(os.Environ(), map[string]string{
		"NANOCLAW_HOME":        d.nanoclawDir,
		"NANOCLAW_DIR":         d.nanoclawDir,
		"NANOCLAW_DATA_DIR":    dataDir,
		"NANOCLAW_SOCKET":      d.socketPath,
		"NANOCLAW_CONFIG_PATH": config.NanoClawConfigPath(),
	})

	var logFile *os.File
	if f, err := os.OpenFile(d.logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600); err == nil {
		logFile = f
		cmd.Stdout = f
		cmd.Stderr = f
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		return fmt.Errorf("start NanoClaw daemon: %w", err)
	}

	d.mu.Lock()
	d.cmd = cmd
	d.logFile = logFile
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

	waitReady := d.waitReady
	if d.waitReadyFunc != nil {
		waitReady = d.waitReadyFunc
	}
	if err := waitReady(); err != nil {
		d.Stop()
		return fmt.Errorf("wait for NanoClaw daemon readiness: %w", err)
	}

	log.Printf("NanoClaw daemon started (PID %d), socket: %s, log: %s", cmd.Process.Pid, d.socketPath, d.logPath)
	return nil
}

// Stop terminates the owned NanoClaw daemon process.
func (d *Daemon) Stop() {
	d.mu.Lock()
	cmd := d.cmd
	exitCh := d.exitCh
	exited := d.exited
	logFile := d.logFile
	d.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		if !exited {
			_ = cmd.Process.Kill()
		}
		if exitCh != nil {
			<-exitCh
		}
	}

	if logFile != nil {
		_ = logFile.Close()
	}

	d.mu.Lock()
	if d.cmd == cmd {
		d.cmd = nil
		d.exitCh = nil
		d.exited = true
		d.logFile = nil
	}
	d.mu.Unlock()
}

// Running returns true while the owned daemon process is alive.
func (d *Daemon) Running() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cmd != nil && !d.exited
}

func (d *Daemon) waitReady() error {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(d.socketPath); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("socket not available: %s", d.socketPath)
}

func daemonEnv(base []string, overrides map[string]string) []string {
	env := make([]string, 0, len(base)+len(overrides))
	seen := make(map[string]struct{}, len(overrides))
	for key := range overrides {
		seen[key] = struct{}{}
	}
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			if _, override := seen[key]; override {
				continue
			}
		}
		env = append(env, item)
	}
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}
