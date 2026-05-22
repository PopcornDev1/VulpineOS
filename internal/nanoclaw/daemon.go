package nanoclaw

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Daemon struct {
	binary      string
	socketPath  string
	running     bool
	process     *os.Process
	logPath     string
}

func NewDaemon(binary, nanoclawDir string) *Daemon {
	return &Daemon{
		binary:    binary,
		logPath:   filepath.Join(nanoclawDir, "data", "nanoclaw.log"),
		socketPath: filepath.Join(nanoclawDir, "data", "cli.sock"),
	}
}

func (d *Daemon) start() error {
	if d.running {
		return nil
	}
	args := []string{"run"}
	cmd := exec.Command(d.binary, args...)
	if env, ok := os.LookupEnv("NANOCLAW_SOCKET"); !ok {
		cmd.Env = append(os.Environ(), "NANOCLAW_SOCKET="+d.socketPath)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}
	d.process = cmd.Process
	d.running = true
	return nil
}

func (d *Daemon) waitForSocket() error {
	if !d.running {
		return nil
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(d.socketPath); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("socket not available: %s", d.socketPath)
}

func (d *Daemon) stop() error {
	if !d.running {
		return nil
	}
	if d.process != nil {
		d.process.Kill()
	}
	d.running = false
	return nil
}