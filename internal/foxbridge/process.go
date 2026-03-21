package foxbridge

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Process manages the foxbridge CDP-to-Juggler proxy process.
type Process struct {
	cmd    *exec.Cmd
	port   int
	binary string
}

// Config holds foxbridge startup configuration.
type Config struct {
	CamoufoxBinary string // path to camoufox/firefox binary
	Port           int    // CDP port (default 9222)
	Headless       bool
	ProfileDir     string
}

// New creates a new foxbridge process manager.
func New() *Process {
	return &Process{port: 9222}
}

// Start launches foxbridge, which in turn launches Camoufox with Juggler pipe.
func (p *Process) Start(cfg Config) error {
	if p.cmd != nil {
		return nil // already running
	}

	bin := findFoxbridge()
	if bin == "" {
		return fmt.Errorf("foxbridge binary not found (install with: go install github.com/PopcornDev1/foxbridge/cmd/foxbridge@latest)")
	}
	p.binary = bin

	port := cfg.Port
	if port == 0 {
		port = 9222
	}
	p.port = port

	args := []string{
		"--port", fmt.Sprintf("%d", port),
	}
	if cfg.CamoufoxBinary != "" {
		args = append(args, "--binary", cfg.CamoufoxBinary)
	}
	if cfg.Headless {
		args = append(args, "--headless")
	}
	if cfg.ProfileDir != "" {
		args = append(args, "--profile", cfg.ProfileDir)
	}

	p.cmd = exec.Command(bin, args...)

	// Log foxbridge output
	logPath := filepath.Join(os.TempDir(), "vulpineos-foxbridge.log")
	if logFile, err := os.Create(logPath); err == nil {
		p.cmd.Stdout = logFile
		p.cmd.Stderr = logFile
	}

	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("start foxbridge: %w", err)
	}

	log.Printf("foxbridge started (PID %d, port %d), log: %s", p.cmd.Process.Pid, port, logPath)

	// Wait for the CDP port to become available
	if err := waitForPort(port, 15*time.Second); err != nil {
		p.Stop()
		return fmt.Errorf("foxbridge failed to start: %w", err)
	}

	log.Printf("foxbridge CDP proxy ready on ws://127.0.0.1:%d", port)
	return nil
}

// Stop kills the foxbridge process.
func (p *Process) Stop() {
	if p.cmd != nil && p.cmd.Process != nil {
		p.cmd.Process.Kill()
		p.cmd.Wait()
		p.cmd = nil
		log.Println("foxbridge stopped")
	}
}

// Running returns true if foxbridge is alive.
func (p *Process) Running() bool {
	return p.cmd != nil && p.cmd.ProcessState == nil
}

// CDPURL returns the CDP WebSocket URL for connecting clients.
func (p *Process) CDPURL() string {
	return fmt.Sprintf("ws://127.0.0.1:%d", p.port)
}

// Port returns the CDP port.
func (p *Process) Port() int {
	return p.port
}

// PID returns the process ID.
func (p *Process) PID() int {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}

// findFoxbridge searches for the foxbridge binary in common locations.
func findFoxbridge() string {
	// 1. Next to our own executable
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidate := filepath.Join(dir, "foxbridge")
		if runtime.GOOS == "windows" {
			candidate += ".exe"
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// 2. In current working directory
	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, "foxbridge")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// 3. GOPATH/bin
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		candidate := filepath.Join(gopath, "bin", "foxbridge")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// 4. ~/go/bin (default GOPATH)
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, "go", "bin", "foxbridge")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// 5. System PATH
	if path, err := exec.LookPath("foxbridge"); err == nil {
		return path
	}

	return ""
}

// waitForPort polls until a TCP port is accepting connections.
func waitForPort(port int, timeout time.Duration) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("port %d not ready after %v", port, timeout)
}
