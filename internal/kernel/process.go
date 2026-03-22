package kernel

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"vulpineos/internal/juggler"
)

// Kernel manages the Firefox/VulpineOS browser process.
type Kernel struct {
	cmd       *exec.Cmd
	client    *juggler.Client
	transport *juggler.PipeTransport
	logFile   *os.File
	startedAt time.Time
	waited    bool // true after cmd.Wait() has been called
	window    *WindowController
	headless  bool
	mu        sync.Mutex
}

// Config holds the kernel launch configuration.
type Config struct {
	// Path to the VulpineOS/Camoufox binary. If empty, auto-detected.
	BinaryPath string
	// Extra Firefox arguments.
	ExtraArgs []string
	// Headless mode.
	Headless bool
	// Profile directory.
	ProfileDir string
}

// New creates a new Kernel instance without starting it.
func New() *Kernel {
	return &Kernel{}
}

// Start launches the Firefox process and establishes the Juggler connection.
func (k *Kernel) Start(cfg Config) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.cmd != nil {
		return fmt.Errorf("kernel already running")
	}

	binary := cfg.BinaryPath
	if binary == "" {
		var err error
		binary, err = findBinary()
		if err != nil {
			return err
		}
	}

	// Build args
	args := []string{
		"--juggler-pipe",
		"--no-remote",
		"--purgecaches", // Force Firefox to re-read omni.ja (needed after patching)
	}
	if cfg.Headless {
		args = append(args, "--headless")
	}
	if cfg.ProfileDir != "" {
		args = append(args, "--profile", cfg.ProfileDir)
	}
	args = append(args, cfg.ExtraArgs...)

	// Create pipes for Juggler transport (FD 3 read, FD 4 write)
	// From Firefox's perspective: it reads from FD 3 and writes to FD 4.
	// From our perspective: we write to Firefox's FD 3 and read from Firefox's FD 4.
	toFirefoxRead, toFirefoxWrite, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create pipe to firefox: %w", err)
	}
	fromFirefoxRead, fromFirefoxWrite, err := os.Pipe()
	if err != nil {
		toFirefoxRead.Close()
		toFirefoxWrite.Close()
		return fmt.Errorf("create pipe from firefox: %w", err)
	}

	cmd := exec.Command(binary, args...)
	// Redirect Firefox stdout/stderr to a log file to keep the TUI clean.
	// If the log file can't be created, fall back to /dev/null.
	logPath := filepath.Join(os.TempDir(), "vulpineos-kernel.log")
	if logFile, err := os.Create(logPath); err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		k.logFile = logFile
	} else {
		devNull, _ := os.Open(os.DevNull)
		cmd.Stdout = devNull
		cmd.Stderr = devNull
	}
	// FD 0=stdin, 1=stdout, 2=stderr, 3=juggler-read (from us), 4=juggler-write (to us)
	cmd.ExtraFiles = []*os.File{toFirefoxRead, fromFirefoxWrite}

	if err := cmd.Start(); err != nil {
		toFirefoxRead.Close()
		toFirefoxWrite.Close()
		fromFirefoxRead.Close()
		fromFirefoxWrite.Close()
		return fmt.Errorf("start firefox: %w", err)
	}

	// Close the ends we don't use
	toFirefoxRead.Close()
	fromFirefoxWrite.Close()

	// We read from fromFirefoxRead (Firefox's FD 4 output) and write to toFirefoxWrite (Firefox's FD 3 input)
	transport := juggler.NewPipeTransport(fromFirefoxRead, toFirefoxWrite)
	client := juggler.NewClient(transport)

	k.cmd = cmd
	k.client = client
	k.transport = transport
	k.startedAt = time.Now()
	k.headless = cfg.Headless

	// Create window controller for non-headless mode
	if !cfg.Headless {
		k.window = NewWindowController(cmd.Process.Pid)
		// Auto-hide the browser window after a brief startup delay
		go func() {
			time.Sleep(2 * time.Second)
			k.window.Hide()
		}()
	}

	return nil
}

// Client returns the Juggler protocol client.
func (k *Kernel) Client() *juggler.Client {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.client
}

// Window returns the window controller (nil if headless).
func (k *Kernel) Window() *WindowController {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.window
}

// IsHeadless returns whether the kernel is running in headless mode.
func (k *Kernel) IsHeadless() bool {
	return k.headless
}

// PID returns the Firefox process ID, or 0 if not running.
func (k *Kernel) PID() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cmd != nil && k.cmd.Process != nil {
		return k.cmd.Process.Pid
	}
	return 0
}

// Uptime returns how long the kernel has been running.
func (k *Kernel) Uptime() time.Duration {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cmd == nil {
		return 0
	}
	return time.Since(k.startedAt)
}

// Running returns true if the kernel process is alive.
func (k *Kernel) Running() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.cmd != nil && k.cmd.ProcessState == nil
}

// Stop gracefully shuts down the Firefox process.
func (k *Kernel) Stop() error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.client != nil {
		// Try graceful shutdown via protocol
		k.client.Call("", "Browser.close", nil)
		k.client.Close()
		k.client = nil
	}

	if k.cmd != nil && k.cmd.Process != nil && !k.waited {
		// Wait briefly for graceful exit, then kill
		done := make(chan error, 1)
		go func() { done <- k.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			k.cmd.Process.Kill()
			<-done
		}
		k.waited = true
		k.cmd = nil
	}

	if k.logFile != nil {
		k.logFile.Close()
		k.logFile = nil
	}

	return nil
}

// LogPath returns the path to the kernel log file.
func (k *Kernel) LogPath() string {
	return filepath.Join(os.TempDir(), "vulpineos-kernel.log")
}

// Wait blocks until the Firefox process exits.
func (k *Kernel) Wait() error {
	k.mu.Lock()
	cmd := k.cmd
	if cmd == nil || k.waited {
		k.mu.Unlock()
		return nil
	}
	k.mu.Unlock()

	err := cmd.Wait()

	k.mu.Lock()
	k.waited = true
	k.mu.Unlock()

	return err
}

// findBinary locates the VulpineOS/Camoufox binary.
func findBinary() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("get executable path: %w", err)
	}
	dir := filepath.Dir(execPath)

	candidates := []string{"camoufox", "camoufox-bin"}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, "../MacOS/camoufox")
	}
	if runtime.GOOS == "windows" {
		candidates = []string{"camoufox.exe"}
	}

	for _, name := range candidates {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	// Try PATH
	for _, name := range []string{"camoufox", "camoufox-bin"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("VulpineOS binary not found (looked in %s and PATH)", dir)
}

// NormalizeOS converts OS name to {macos, windows, linux}.
func NormalizeOS(osName string) string {
	osName = strings.ToLower(osName)
	switch {
	case osName == "darwin" || strings.Contains(osName, "mac"):
		return "macos"
	case strings.Contains(osName, "win"):
		return "windows"
	default:
		return "linux"
	}
}
