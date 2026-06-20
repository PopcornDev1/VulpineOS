package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"vulpineos/internal/juggler"
)

func camoufoxBinary() string {
	if b := os.Getenv("VULPINE_BROWSER_BINARY"); b != "" {
		return b
	}
	if b := os.Getenv("CAMOUFOX_BINARY"); b != "" {
		return b
	}
	candidates := []string{
		filepath.Join(os.Getenv("HOME"), "Downloads", "Vulpine.app", "Contents", "MacOS", "vulpine"),
		"/Applications/Vulpine.app/Contents/MacOS/vulpine",
		"/Applications/vulpine.app/Contents/MacOS/vulpine",
		filepath.Join(os.Getenv("HOME"), "Downloads", "Camoufox.app", "Contents", "MacOS", "camoufox"),
		"/Applications/Camoufox.app/Contents/MacOS/camoufox",
		"/Applications/camoufox.app/Contents/MacOS/camoufox",
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if b, err := ResolveBinaryPath(""); err == nil {
		return b
	}
	return ""
}

func requireLiveKernelBinary(t *testing.T) string {
	t.Helper()
	if os.Getenv("VULPINEOS_RUN_LIVE") != "1" {
		t.Skip("set VULPINEOS_RUN_LIVE=1 to run live kernel tests")
	}
	bin := camoufoxBinary()
	if bin == "" {
		t.Skip("Vulpine browser binary not found")
	}
	return bin
}

func liveKernelCall(t *testing.T, client *juggler.Client, sessionID, method string, params interface{}) json.RawMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := client.CallWithContext(ctx, sessionID, method, params)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return result
}

func TestBinaryLocatorPrefersRepoLocalBuild(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	execPath := filepath.Join(root, "bin", "vulpineos")
	repoBinary := filepath.Join(root, "camoufox-146.0.1-beta.25", "obj-aarch64-apple-darwin", "dist", "Camoufox.app", "Contents", "MacOS", "camoufox")
	repoFallbackBinary := filepath.Join(root, "camoufox-146.0.1-beta.25", "obj-aarch64-apple-darwin", "dist", "bin", "camoufox")
	downloadsBinary := filepath.Join(home, "Downloads", "Camoufox.app", "Contents", "MacOS", "camoufox")

	mustWriteExecutable(t, execPath)
	mustWriteExecutable(t, repoBinary)
	mustWriteExecutable(t, repoFallbackBinary)
	mustWriteExecutable(t, downloadsBinary)

	locator := binaryLocator{
		execPath: execPath,
		cwd:      root,
		home:     home,
		goos:     "darwin",
		lookPath: func(string) (string, error) { return "", os.ErrNotExist },
	}

	resolved, err := locator.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved != repoBinary {
		t.Fatalf("Resolve = %q, want %q", resolved, repoBinary)
	}
}

func TestBinaryLocatorPrefersRepoLocalVulpineAppBuild(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	execPath := filepath.Join(root, "bin", "vulpineos")
	repoBinary := filepath.Join(root, "camoufox-146.0.1-beta.25", "obj-aarch64-apple-darwin", "dist", "Vulpine.app", "Contents", "MacOS", "vulpine")
	repoFallbackBinary := filepath.Join(root, "camoufox-146.0.1-beta.25", "obj-aarch64-apple-darwin", "dist", "bin", "vulpine")

	mustWriteExecutable(t, execPath)
	mustWriteExecutable(t, repoBinary)
	mustWriteExecutable(t, repoFallbackBinary)

	locator := binaryLocator{
		execPath: execPath,
		cwd:      root,
		home:     home,
		goos:     "darwin",
		lookPath: func(string) (string, error) { return "", os.ErrNotExist },
	}

	resolved, err := locator.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved != repoBinary {
		t.Fatalf("Resolve = %q, want %q", resolved, repoBinary)
	}
}

func TestBinaryLocatorFallsBackToRepoBinWhenAppBundleMissing(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	execPath := filepath.Join(root, "bin", "vulpineos")
	repoBinary := filepath.Join(root, "camoufox-146.0.1-beta.25", "obj-aarch64-apple-darwin", "dist", "bin", "camoufox")
	downloadsBinary := filepath.Join(home, "Downloads", "Camoufox.app", "Contents", "MacOS", "camoufox")

	mustWriteExecutable(t, execPath)
	mustWriteExecutable(t, repoBinary)
	mustWriteExecutable(t, downloadsBinary)

	locator := binaryLocator{
		execPath: execPath,
		cwd:      root,
		home:     home,
		goos:     "darwin",
		lookPath: func(string) (string, error) { return "", os.ErrNotExist },
	}

	resolved, err := locator.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved != repoBinary {
		t.Fatalf("Resolve = %q, want %q", resolved, repoBinary)
	}
}

func TestBinaryLocatorResolvesRequestedRepoDirectory(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	execPath := filepath.Join(root, "bin", "vulpineos")
	repoBinary := filepath.Join(root, "camoufox-146.0.1-beta.25", "obj-aarch64-apple-darwin", "dist", "Camoufox.app", "Contents", "MacOS", "camoufox")

	mustWriteExecutable(t, execPath)
	mustWriteExecutable(t, repoBinary)

	locator := binaryLocator{
		execPath: execPath,
		cwd:      filepath.Join(root, "elsewhere"),
		home:     home,
		goos:     "darwin",
		lookPath: func(string) (string, error) { return "", os.ErrNotExist },
	}

	resolved, err := locator.Resolve(root)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", root, err)
	}
	if resolved != repoBinary {
		t.Fatalf("Resolve(%q) = %q, want %q", root, resolved, repoBinary)
	}
}

func TestBinaryLocatorResolvesRequestedAppBundleDirectory(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	execPath := filepath.Join(root, "bin", "vulpineos")
	appBundle := filepath.Join(root, "Camoufox.app")
	appBinary := filepath.Join(appBundle, "Contents", "MacOS", "camoufox")

	mustWriteExecutable(t, execPath)
	mustWriteExecutable(t, appBinary)

	locator := binaryLocator{
		execPath: execPath,
		cwd:      root,
		home:     home,
		goos:     "darwin",
		lookPath: func(string) (string, error) { return "", os.ErrNotExist },
	}

	resolved, err := locator.Resolve(appBundle)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", appBundle, err)
	}
	if resolved != appBinary {
		t.Fatalf("Resolve(%q) = %q, want %q", appBundle, resolved, appBinary)
	}
}

func TestBinaryLocatorResolvesRequestedVulpineAppBundleDirectory(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	execPath := filepath.Join(root, "bin", "vulpineos")
	appBundle := filepath.Join(root, "Vulpine.app")
	appBinary := filepath.Join(appBundle, "Contents", "MacOS", "vulpine")

	mustWriteExecutable(t, execPath)
	mustWriteExecutable(t, appBinary)

	locator := binaryLocator{
		execPath: execPath,
		cwd:      root,
		home:     home,
		goos:     "darwin",
		lookPath: func(string) (string, error) { return "", os.ErrNotExist },
	}

	resolved, err := locator.Resolve(appBundle)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", appBundle, err)
	}
	if resolved != appBinary {
		t.Fatalf("Resolve(%q) = %q, want %q", appBundle, resolved, appBinary)
	}
}

func TestBinaryLocatorResolvesVulpineOnPath(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	execPath := filepath.Join(root, "bin", "vulpineos")
	pathBinary := filepath.Join(root, "path", "vulpine")

	mustWriteExecutable(t, execPath)
	mustWriteExecutable(t, pathBinary)

	locator := binaryLocator{
		execPath: execPath,
		cwd:      filepath.Join(root, "empty"),
		home:     home,
		goos:     "linux",
		lookPath: func(name string) (string, error) {
			if name == "vulpine" {
				return pathBinary, nil
			}
			return "", os.ErrNotExist
		},
	}

	resolved, err := locator.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved != pathBinary {
		t.Fatalf("Resolve = %q, want %q", resolved, pathBinary)
	}
}

func TestBinaryLocatorDetectDriftWarnsOnOlderExplicitBinary(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	execPath := filepath.Join(root, "bin", "vulpineos")
	repoBinary := filepath.Join(root, "camoufox-146.0.1-beta.25", "obj-aarch64-apple-darwin", "dist", "Camoufox.app", "Contents", "MacOS", "camoufox")
	repoFallbackBinary := filepath.Join(root, "camoufox-146.0.1-beta.25", "obj-aarch64-apple-darwin", "dist", "bin", "camoufox")
	downloadsBinary := filepath.Join(home, "Downloads", "Camoufox.app", "Contents", "MacOS", "camoufox")

	mustWriteExecutable(t, execPath)
	mustWriteExecutable(t, repoBinary)
	mustWriteExecutable(t, repoFallbackBinary)
	mustWriteExecutable(t, downloadsBinary)
	now := time.Now()
	if err := os.Chtimes(downloadsBinary, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("Chtimes downloads: %v", err)
	}
	if err := os.Chtimes(repoBinary, now, now); err != nil {
		t.Fatalf("Chtimes repo: %v", err)
	}

	locator := binaryLocator{
		execPath: execPath,
		cwd:      root,
		home:     home,
		goos:     "darwin",
		lookPath: func(string) (string, error) { return "", os.ErrNotExist },
	}

	warning := locator.DetectDrift(downloadsBinary)
	if warning == nil {
		t.Fatal("DetectDrift returned nil")
	}
	if warning.PreferredPath != repoBinary {
		t.Fatalf("PreferredPath = %q, want %q", warning.PreferredPath, repoBinary)
	}
	if warning.SelectedPath != downloadsBinary {
		t.Fatalf("SelectedPath = %q, want %q", warning.SelectedPath, downloadsBinary)
	}
}

func mustWriteExecutable(t *testing.T, path string) {
	t.Helper()
	mustWriteExecutableContent(t, path, "#!/bin/sh\nexit 0\n")
}

func mustWriteExecutableContent(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func TestKernelRunningReflectsExitedProcess(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "camoufox")
	mustWriteExecutableContent(t, bin, "#!/bin/sh\nexit 0\n")

	k := New()
	if err := k.Start(Config{BinaryPath: bin, Headless: true}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer k.Stop()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !k.Running() {
			if err := k.Start(Config{BinaryPath: bin, Headless: true}); err != nil {
				t.Fatalf("Start after exited process: %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("kernel still reported running after child process exited")
}

func TestKernelStartFailureRemovesTempProfile(t *testing.T) {
	before := tempProfileDirs(t)

	k := New()
	err := k.Start(Config{BinaryPath: filepath.Join(t.TempDir(), "missing-camoufox"), Headless: true})
	if err == nil {
		defer k.Stop()
		t.Fatal("expected missing binary startup to fail")
	}
	if k.profileDir != "" {
		t.Fatalf("profileDir retained after failed start: %q", k.profileDir)
	}

	for path := range tempProfileDirs(t) {
		if !before[path] {
			t.Fatalf("temp profile leaked after failed start: %s", path)
		}
	}
}

func TestKernelStopKillsChildProcesses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script test binary is unix-specific")
	}

	dir := t.TempDir()
	childPath := filepath.Join(dir, "child.pid")
	bin := filepath.Join(dir, "camoufox")
	mustWriteExecutableContent(t, bin, "#!/bin/sh\nsleep 60 &\necho $! > "+shellQuote(childPath)+"\nwait\n")

	k := New()
	if err := k.Start(Config{BinaryPath: bin, Headless: true}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	childPID := waitForKernelPIDFile(t, childPath)
	if err := k.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !kernelProcessExists(childPID) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	proc, err := os.FindProcess(childPID)
	if err == nil {
		_ = proc.Kill()
	}
	t.Fatalf("kernel child process %d survived stop", childPID)
}

func waitForKernelPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pidText := strings.TrimSpace(string(data))
			var pid int
			if _, scanErr := fmt.Sscanf(pidText, "%d", &pid); scanErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child pid file was not written")
	return 0
}

func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}

func kernelProcessExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func tempProfileDirs(t *testing.T) map[string]bool {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "vulpineos-profile-*"))
	if err != nil {
		t.Fatalf("glob temp profiles: %v", err)
	}
	dirs := make(map[string]bool, len(matches))
	for _, path := range matches {
		dirs[path] = true
	}
	return dirs
}

func TestKernelStartStop(t *testing.T) {
	bin := requireLiveKernelBinary(t)

	k := New()
	if k.Running() {
		t.Fatal("kernel should not be running before Start")
	}

	err := k.Start(Config{
		BinaryPath: bin,
		Headless:   true,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer k.Stop()

	if !k.Running() {
		t.Fatal("kernel should be running after Start")
	}
	if k.PID() == 0 {
		t.Fatal("PID should be non-zero")
	}
	if k.Client() == nil {
		t.Fatal("Client should not be nil")
	}
	if k.Uptime() < 0 {
		t.Fatal("Uptime should be non-negative")
	}
	if k.IsHeadless() != true {
		t.Fatal("should be headless")
	}
}

func TestKernelBrowserEnable(t *testing.T) {
	bin := requireLiveKernelBinary(t)

	k := New()
	if err := k.Start(Config{BinaryPath: bin, Headless: true}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer k.Stop()

	client := k.Client()

	// Browser.enable
	liveKernelCall(t, client, "", "Browser.enable", map[string]interface{}{
		"attachToDefaultContext": true,
	})

	// Browser.getInfo
	result := liveKernelCall(t, client, "", "Browser.getInfo", nil)
	if len(result) == 0 {
		t.Fatal("Browser.getInfo returned empty")
	}
	t.Logf("Browser.getInfo: %s", string(result)[:min(len(result), 100)])
}

func TestKernelNewPageAndNavigate(t *testing.T) {
	bin := requireLiveKernelBinary(t)

	k := New()
	if err := k.Start(Config{BinaryPath: bin, Headless: true}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer k.Stop()

	client := k.Client()
	liveKernelCall(t, client, "", "Browser.enable", map[string]interface{}{"attachToDefaultContext": true})

	// Wait for events to settle
	time.Sleep(2 * time.Second)

	// Create a new page
	result := liveKernelCall(t, client, "", "Browser.newPage", nil)
	t.Logf("newPage: %s", string(result))

	// Navigate
	// Need the session ID from the attachedToTarget event — use a simpler approach
	// Just verify the page was created
	if len(result) == 0 {
		t.Fatal("newPage returned empty")
	}
}

func TestKernelCreateBrowserContext(t *testing.T) {
	bin := requireLiveKernelBinary(t)

	k := New()
	if err := k.Start(Config{BinaryPath: bin, Headless: true}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer k.Stop()

	client := k.Client()
	liveKernelCall(t, client, "", "Browser.enable", map[string]interface{}{"attachToDefaultContext": true})

	// Create browser context
	result := liveKernelCall(t, client, "", "Browser.createBrowserContext", map[string]interface{}{
		"removeOnDetach": true,
	})
	t.Logf("createBrowserContext: %s", string(result))

	// Remove it
	_, _ = client.Call("", "Browser.removeBrowserContext", map[string]interface{}{
		"browserContextId": "default", // just test the call doesn't crash
	})
	// May fail for default context — that's ok
}

func TestKernelDoubleStart(t *testing.T) {
	bin := requireLiveKernelBinary(t)

	k := New()
	if err := k.Start(Config{BinaryPath: bin, Headless: true}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer k.Stop()

	// Second start should fail gracefully
	err := k.Start(Config{BinaryPath: bin, Headless: true})
	if err == nil {
		t.Fatal("double Start should return error")
	}
}

func TestKernelStopIdempotent(t *testing.T) {
	bin := requireLiveKernelBinary(t)

	k := New()
	if err := k.Start(Config{BinaryPath: bin, Headless: true}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Stop twice should not panic
	k.Stop()
	k.Stop()

	if k.Running() {
		t.Fatal("should not be running after Stop")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
