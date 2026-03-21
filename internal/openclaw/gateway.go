package openclaw

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"
)

// Gateway manages the OpenClaw gateway daemon process.
type Gateway struct {
	cmd    *exec.Cmd
	binary string
}

// NewGateway creates a gateway manager.
func NewGateway(binary string) *Gateway {
	return &Gateway{binary: binary}
}

// Start launches the OpenClaw gateway in the background.
func (g *Gateway) Start() error {
	if g.cmd != nil {
		return nil // already running
	}

	openclawBin := g.binary
	if openclawBin == "" {
		// Use the manager's find logic
		mgr := NewManager("")
		openclawBin = mgr.findOpenClaw()
	}
	if openclawBin == "" {
		return fmt.Errorf("OpenClaw binary not found")
	}

	args := []string{
		"--profile", "vulpine",
		"gateway",
		"--bind", "loopback",
		"--allow-unconfigured",
	}

	g.cmd = exec.Command(openclawBin, args...)
	g.cmd.Stdout = nil // suppress output
	g.cmd.Stderr = nil

	// Redirect to log file
	logPath := os.TempDir() + "/vulpineos-gateway.log"
	if logFile, err := os.Create(logPath); err == nil {
		g.cmd.Stdout = logFile
		g.cmd.Stderr = logFile
	}

	if err := g.cmd.Start(); err != nil {
		return fmt.Errorf("start gateway: %w", err)
	}

	log.Printf("OpenClaw gateway started (PID %d), log: %s", g.cmd.Process.Pid, logPath)

	// Wait a moment for it to bind
	time.Sleep(3 * time.Second)

	return nil
}

// Stop kills the gateway process.
func (g *Gateway) Stop() {
	if g.cmd != nil && g.cmd.Process != nil {
		g.cmd.Process.Kill()
		g.cmd.Wait()
		g.cmd = nil
		log.Println("OpenClaw gateway stopped")
	}
}

// Running returns true if the gateway process is alive.
func (g *Gateway) Running() bool {
	return g.cmd != nil && g.cmd.ProcessState == nil
}
