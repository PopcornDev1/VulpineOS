package kernel

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"vulpineos/internal/juggler"
)

// plainFirefoxUserAgent returns the real Firefox UA for the host OS, with no
// Camoufox token. Keep the version aligned with the engine (bump on upgrades).
func plainFirefoxUserAgent() string {
	const v = "146.0"
	switch runtime.GOOS {
	case "windows":
		return "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:" + v + ") Gecko/20100101 Firefox/" + v
	case "darwin":
		return "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:" + v + ") Gecko/20100101 Firefox/" + v
	default:
		return "Mozilla/5.0 (X11; Linux x86_64; rv:" + v + ") Gecko/20100101 Firefox/" + v
	}
}

// writeDefaultUserAgentPref appends a plain-Firefox general.useragent.override
// to the profile's user.js (append-mode, coexists with other writers) so the
// default UA never advertises Camoufox.
func writeDefaultUserAgentPref(profileDir string) {
	line := fmt.Sprintf("\n// VulpineOS: default UA scrub (no Camoufox token)\nuser_pref(\"general.useragent.override\", %q);\n", plainFirefoxUserAgent())
	path := filepath.Join(profileDir, "user.js")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

// Kernel manages the Firefox/VulpineOS browser process.
type Kernel struct {
	cmd        *exec.Cmd
	client     *juggler.Client
	transport  *juggler.PipeTransport
	logFile    *os.File
	profileDir string
	startedAt  time.Time
	waited     bool // true after cmd.Wait() has been called
	waitDone   chan struct{}
	exitErr    error
	window     *WindowController
	headless   bool
	mu         sync.Mutex
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

// BinaryWarning describes a selected browser binary that appears older than a
// newer repo-local Camoufox build on disk.
type BinaryWarning struct {
	SelectedPath  string
	PreferredPath string
	SelectedMod   time.Time
	PreferredMod  time.Time
}

// Message returns a human-readable warning describing the drift.
func (w *BinaryWarning) Message() string {
	if w == nil {
		return ""
	}
	return fmt.Sprintf(
		"selected browser binary %s is older than repo-local build %s (%s < %s)",
		w.SelectedPath,
		w.PreferredPath,
		w.SelectedMod.UTC().Format(time.RFC3339),
		w.PreferredMod.UTC().Format(time.RFC3339),
	)
}

type binaryLocator struct {
	execPath string
	cwd      string
	home     string
	goos     string
	lookPath func(string) (string, error)
}

// New creates a new Kernel instance without starting it.
func New() *Kernel {
	return &Kernel{}
}

// ResolveBinaryPath returns the browser binary path to use for startup. An
// explicit path is treated as authoritative; otherwise common packaged,
// repo-local, and installed locations are searched.
func ResolveBinaryPath(requested string) (string, error) {
	locator, err := newBinaryLocator()
	if err != nil {
		return "", err
	}
	return locator.Resolve(requested)
}

// DetectStaleBinary returns a warning when the selected browser path is older
// than a newer repo-local Camoufox build.
func DetectStaleBinary(selected string) *BinaryWarning {
	locator, err := newBinaryLocator()
	if err != nil {
		return nil
	}
	return locator.DetectDrift(selected)
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
		binary, err = ResolveBinaryPath("")
		if err != nil {
			return err
		}
	}

	profileDir := cfg.ProfileDir
	tempProfileDir := ""
	if profileDir == "" {
		var err error
		tempProfileDir, err = os.MkdirTemp("", "vulpineos-profile-*")
		if err != nil {
			return fmt.Errorf("create temp profile: %w", err)
		}
		profileDir = tempProfileDir
	}

	// Scrub the "Camoufox" product token from the DEFAULT user agent: any
	// context without a per-context UA override (default context, early reads,
	// nomad/MCP paths) otherwise leaks "Camoufox/<ver>". Per-context overrides
	// (Browser.setUserAgentOverride -> customUserAgent) still take precedence.
	writeDefaultUserAgentPref(profileDir)

	// Build args
	args := []string{
		"--juggler-pipe",
		"--no-remote",
		"--new-instance",
		"--purgecaches", // Force Firefox to re-read omni.ja (needed after patching)
	}
	if cfg.Headless {
		args = append(args, "--headless")
	}
	args = append(args, "--profile", profileDir)
	args = append(args, cfg.ExtraArgs...)

	// Create pipes for Juggler transport (FD 3 read, FD 4 write)
	// From Firefox's perspective: it reads from FD 3 and writes to FD 4.
	// From our perspective: we write to Firefox's FD 3 and read from Firefox's FD 4.
	toFirefoxRead, toFirefoxWrite, err := os.Pipe()
	if err != nil {
		if tempProfileDir != "" {
			_ = os.RemoveAll(tempProfileDir)
		}
		return fmt.Errorf("create pipe to firefox: %w", err)
	}
	fromFirefoxRead, fromFirefoxWrite, err := os.Pipe()
	if err != nil {
		toFirefoxRead.Close()
		toFirefoxWrite.Close()
		if tempProfileDir != "" {
			_ = os.RemoveAll(tempProfileDir)
		}
		return fmt.Errorf("create pipe from firefox: %w", err)
	}

	cmd := exec.Command(binary, args...)
	configureKernelProcess(cmd)
	applyKernelProcessHooks(cmd)
	// Redirect Firefox stdout/stderr to a log file to keep the TUI clean.
	// If the log file can't be created, fall back to /dev/null.
	logPath := filepath.Join(os.TempDir(), "vulpineos-kernel.log")
	var outputFile *os.File
	if logFile, err := os.Create(logPath); err == nil {
		outputFile = logFile
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	} else {
		devNull, openErr := os.Open(os.DevNull)
		if openErr == nil {
			outputFile = devNull
			cmd.Stdout = devNull
			cmd.Stderr = devNull
		}
	}
	// FD 0=stdin, 1=stdout, 2=stderr, 3=juggler-read (from us), 4=juggler-write (to us)
	cmd.ExtraFiles = []*os.File{toFirefoxRead, fromFirefoxWrite}

	if err := cmd.Start(); err != nil {
		toFirefoxRead.Close()
		toFirefoxWrite.Close()
		fromFirefoxRead.Close()
		fromFirefoxWrite.Close()
		if outputFile != nil {
			_ = outputFile.Close()
		}
		if tempProfileDir != "" {
			_ = os.RemoveAll(tempProfileDir)
		}
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
	k.logFile = outputFile
	k.profileDir = tempProfileDir
	k.startedAt = time.Now()
	k.waited = false
	k.exitErr = nil
	k.waitDone = make(chan struct{})
	k.headless = cfg.Headless
	if !cfg.Headless {
		k.window = NewWindowController(cmd.Process.Pid)
		go func() {
			time.Sleep(2 * time.Second)
			if k.window != nil {
				k.window.HideWhenReady()
			}
		}()
	}
	go k.waitForExit(cmd, k.waitDone)

	return nil
}

func (k *Kernel) waitForExit(cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()

	var client *juggler.Client
	var logFile *os.File
	var profileDir string
	k.mu.Lock()
	if k.cmd == cmd {
		client = k.client
		logFile = k.logFile
		profileDir = k.profileDir

		k.cmd = nil
		k.client = nil
		k.transport = nil
		k.logFile = nil
		k.profileDir = ""
		k.window = nil
		k.waited = true
		k.exitErr = err
	}
	k.mu.Unlock()

	if client != nil {
		_ = client.Close()
	}
	if logFile != nil {
		_ = logFile.Close()
	}
	if profileDir != "" {
		_ = os.RemoveAll(profileDir)
	}
	close(done)
}

func newBinaryLocator() (binaryLocator, error) {
	execPath, err := os.Executable()
	if err != nil {
		return binaryLocator{}, fmt.Errorf("get executable path: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	home, _ := os.UserHomeDir()
	return binaryLocator{
		execPath: execPath,
		cwd:      cwd,
		home:     home,
		goos:     runtime.GOOS,
		lookPath: exec.LookPath,
	}, nil
}

func (l binaryLocator) Resolve(requested string) (string, error) {
	if requested != "" {
		if resolved := l.normalizeIfRunnable(requested); resolved != "" {
			return resolved, nil
		}
		if resolved := l.resolveDirectory(requested); resolved != "" {
			return resolved, nil
		}
		return "", fmt.Errorf("VulpineOS binary not found at %s (expected an executable, a Camoufox.app bundle, a browser dist directory, or a repo-local camoufox-*/obj-*/dist build)", requested)
	}

	if resolved := l.firstExisting(l.packagedCandidates()); resolved != "" {
		return resolved, nil
	}
	if resolved := l.repoLocalBuild(); resolved != "" {
		return resolved, nil
	}
	if resolved := l.firstExisting(l.installedCandidates()); resolved != "" {
		return resolved, nil
	}

	for _, name := range []string{"camoufox", "camoufox-bin"} {
		if l.lookPath == nil {
			break
		}
		if p, err := l.lookPath(name); err == nil {
			if resolved := l.normalizeIfRunnable(p); resolved != "" {
				return resolved, nil
			}
		}
	}

	execDir := filepath.Dir(l.execPath)
	return "", fmt.Errorf("VulpineOS binary not found (looked near %s, repo-local builds, common install paths, and PATH)", execDir)
}

func (l binaryLocator) DetectDrift(selected string) *BinaryWarning {
	selected = l.normalizeIfRunnable(selected)
	if selected == "" {
		return nil
	}
	preferred := l.repoLocalBuild()
	if preferred == "" {
		return nil
	}
	if sameFile(selected, preferred) {
		return nil
	}
	selectedInfo, err := os.Stat(selected)
	if err != nil {
		return nil
	}
	preferredInfo, err := os.Stat(preferred)
	if err != nil {
		return nil
	}
	if !preferredInfo.ModTime().After(selectedInfo.ModTime()) {
		return nil
	}
	return &BinaryWarning{
		SelectedPath:  selected,
		PreferredPath: preferred,
		SelectedMod:   selectedInfo.ModTime(),
		PreferredMod:  preferredInfo.ModTime(),
	}
}

func (l binaryLocator) packagedCandidates() []string {
	execDir := filepath.Dir(l.execPath)
	candidates := []string{
		filepath.Join(execDir, "camoufox"),
		filepath.Join(execDir, "camoufox-bin"),
	}
	if l.goos == "darwin" {
		candidates = append(candidates, filepath.Join(execDir, "../MacOS/camoufox"))
	}
	if l.goos == "windows" {
		candidates = []string{filepath.Join(execDir, "camoufox.exe")}
	}
	return candidates
}

func (l binaryLocator) installedCandidates() []string {
	var candidates []string
	switch l.goos {
	case "darwin":
		candidates = append(candidates,
			filepath.Join(l.home, "Downloads", "Camoufox.app", "Contents", "MacOS", "camoufox"),
			"/Applications/Camoufox.app/Contents/MacOS/camoufox",
			"/Applications/camoufox.app/Contents/MacOS/camoufox",
		)
	case "windows":
		candidates = append(candidates,
			filepath.Join(l.home, "Downloads", "Camoufox", "camoufox.exe"),
		)
	default:
		candidates = append(candidates,
			filepath.Join(l.home, ".camoufox", "camoufox"),
			"/usr/local/bin/camoufox",
		)
	}
	return candidates
}

func (l binaryLocator) repoLocalBuild() string {
	return l.repoLocalBuildInRoots(l.searchRoots())
}

func (l binaryLocator) repoLocalBuildInRoots(roots []string) string {
	var preferred []string
	var fallback []string
	seen := map[string]struct{}{}
	patterns := []string{
		filepath.Join("camoufox-*", "obj-*", "dist", "bin", "camoufox"),
	}
	if l.goos == "darwin" {
		patterns = append(patterns, filepath.Join("camoufox-*", "obj-*", "dist", "Camoufox.app", "Contents", "MacOS", "camoufox"))
	}
	for _, root := range roots {
		for _, pattern := range patterns {
			found, _ := filepath.Glob(filepath.Join(root, pattern))
			sort.Strings(found)
			for _, path := range found {
				if normalized := l.normalizeIfRunnable(path); normalized != "" {
					if _, ok := seen[normalized]; ok {
						continue
					}
					seen[normalized] = struct{}{}
					if l.goos == "darwin" && strings.Contains(normalized, ".app/Contents/MacOS/camoufox") {
						preferred = append(preferred, normalized)
					} else {
						fallback = append(fallback, normalized)
					}
				}
			}
		}
	}
	if preferredPath := newestExisting(preferred); preferredPath != "" {
		return preferredPath
	}
	return newestExisting(fallback)
}

func (l binaryLocator) resolveDirectory(path string) string {
	cleaned, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		cleaned = filepath.Clean(path)
	}
	info, err := os.Stat(cleaned)
	if err != nil || !info.IsDir() {
		return ""
	}

	var candidates []string
	switch l.goos {
	case "windows":
		candidates = append(candidates, filepath.Join(cleaned, "camoufox.exe"))
	default:
		candidates = append(candidates,
			filepath.Join(cleaned, "camoufox"),
			filepath.Join(cleaned, "camoufox-bin"),
			filepath.Join(cleaned, "bin", "camoufox"),
		)
	}
	if l.goos == "darwin" {
		candidates = append(candidates,
			filepath.Join(cleaned, "Camoufox.app", "Contents", "MacOS", "camoufox"),
			filepath.Join(cleaned, "camoufox.app", "Contents", "MacOS", "camoufox"),
		)
		if strings.HasSuffix(cleaned, ".app") {
			candidates = append(candidates, filepath.Join(cleaned, "Contents", "MacOS", "camoufox"))
		}
	}
	if resolved := l.firstExisting(candidates); resolved != "" {
		return resolved
	}
	return l.repoLocalBuildInRoots([]string{cleaned})
}

func (l binaryLocator) searchRoots() []string {
	roots := []string{}
	seen := map[string]struct{}{}
	appendRoot := func(path string) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		for i := 0; i < 6; i++ {
			if _, ok := seen[path]; !ok {
				seen[path] = struct{}{}
				roots = append(roots, path)
			}
			parent := filepath.Dir(path)
			if parent == path {
				break
			}
			path = parent
		}
	}
	appendRoot(l.cwd)
	appendRoot(filepath.Dir(l.execPath))
	return roots
}

func (l binaryLocator) firstExisting(candidates []string) string {
	for _, candidate := range candidates {
		if resolved := l.normalizeIfRunnable(candidate); resolved != "" {
			return resolved
		}
	}
	return ""
}

func (l binaryLocator) normalizeIfRunnable(path string) string {
	if path == "" {
		return ""
	}
	cleaned, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		cleaned = filepath.Clean(path)
	}
	info, err := os.Stat(cleaned)
	if err != nil || info.IsDir() {
		return ""
	}
	if info.Mode().Type() != 0 {
		return ""
	}
	if l.goos == "windows" {
		return cleaned
	}
	if info.Mode()&0111 == 0 {
		return ""
	}
	return cleaned
}

func newestExisting(paths []string) string {
	var newestPath string
	var newestMod time.Time
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if newestPath == "" || info.ModTime().After(newestMod) || (info.ModTime().Equal(newestMod) && path < newestPath) {
			newestPath = path
			newestMod = info.ModTime()
		}
	}
	return newestPath
}

func sameFile(a, b string) bool {
	aInfo, err := os.Stat(a)
	if err != nil {
		return false
	}
	bInfo, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(aInfo, bInfo)
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
	if k.cmd == nil || k.waited {
		return false
	}
	if k.waitDone == nil {
		return true
	}
	select {
	case <-k.waitDone:
		return false
	default:
		return true
	}
}

// Stop gracefully shuts down the Firefox process.
func (k *Kernel) Stop() error {
	k.mu.Lock()
	client := k.client
	cmd := k.cmd
	waitDone := k.waitDone
	k.mu.Unlock()

	if client != nil {
		// Try graceful shutdown via protocol
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = client.CallWithContext(ctx, "", "Browser.close", nil)
		cancel()
		_ = client.Close()
	}

	if cmd != nil && cmd.Process != nil {
		// Wait briefly for graceful exit, then kill
		if waitDone != nil {
			select {
			case <-waitDone:
			case <-time.After(5 * time.Second):
				_ = killKernelProcess(cmd)
				<-waitDone
			}
		} else {
			_ = killKernelProcess(cmd)
		}
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
	waitDone := k.waitDone
	if waitDone == nil || k.waited {
		err := k.exitErr
		k.mu.Unlock()
		return err
	}
	k.mu.Unlock()

	<-waitDone

	k.mu.Lock()
	err := k.exitErr
	k.mu.Unlock()

	return err
}

// findBinary locates the VulpineOS/Camoufox binary.
func findBinary() (string, error) {
	return ResolveBinaryPath("")
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
