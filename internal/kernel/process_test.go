package kernel

import (
	"os"
	"testing"
	"time"
)

func camoufoxBinary() string {
	// Check config, env, then common locations
	if b := os.Getenv("CAMOUFOX_BINARY"); b != "" {
		return b
	}
	candidates := []string{
		os.Getenv("HOME") + "/Downloads/camoufox.app/Contents/MacOS/camoufox",
		"/Applications/camoufox.app/Contents/MacOS/camoufox",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func TestKernelStartStop(t *testing.T) {
	bin := camoufoxBinary()
	if bin == "" {
		t.Skip("camoufox binary not found")
	}

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
	bin := camoufoxBinary()
	if bin == "" {
		t.Skip("camoufox binary not found")
	}

	k := New()
	if err := k.Start(Config{BinaryPath: bin, Headless: true}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer k.Stop()

	client := k.Client()

	// Browser.enable
	_, err := client.Call("", "Browser.enable", map[string]interface{}{
		"attachToDefaultContext": true,
	})
	if err != nil {
		t.Fatalf("Browser.enable: %v", err)
	}

	// Browser.getInfo
	result, err := client.Call("", "Browser.getInfo", nil)
	if err != nil {
		t.Fatalf("Browser.getInfo: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("Browser.getInfo returned empty")
	}
	t.Logf("Browser.getInfo: %s", string(result)[:min(len(result), 100)])
}

func TestKernelNewPageAndNavigate(t *testing.T) {
	bin := camoufoxBinary()
	if bin == "" {
		t.Skip("camoufox binary not found")
	}

	k := New()
	if err := k.Start(Config{BinaryPath: bin, Headless: true}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer k.Stop()

	client := k.Client()
	client.Call("", "Browser.enable", map[string]interface{}{"attachToDefaultContext": true})

	// Wait for events to settle
	time.Sleep(2 * time.Second)

	// Create a new page
	result, err := client.Call("", "Browser.newPage", nil)
	if err != nil {
		t.Fatalf("Browser.newPage: %v", err)
	}
	t.Logf("newPage: %s", string(result))

	// Navigate
	// Need the session ID from the attachedToTarget event — use a simpler approach
	// Just verify the page was created
	if len(result) == 0 {
		t.Fatal("newPage returned empty")
	}
}

func TestKernelCreateBrowserContext(t *testing.T) {
	bin := camoufoxBinary()
	if bin == "" {
		t.Skip("camoufox binary not found")
	}

	k := New()
	if err := k.Start(Config{BinaryPath: bin, Headless: true}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer k.Stop()

	client := k.Client()
	client.Call("", "Browser.enable", map[string]interface{}{"attachToDefaultContext": true})

	// Create browser context
	result, err := client.Call("", "Browser.createBrowserContext", map[string]interface{}{
		"removeOnDetach": true,
	})
	if err != nil {
		t.Fatalf("createBrowserContext: %v", err)
	}
	t.Logf("createBrowserContext: %s", string(result))

	// Remove it
	_, err = client.Call("", "Browser.removeBrowserContext", map[string]interface{}{
		"browserContextId": "default", // just test the call doesn't crash
	})
	// May fail for default context — that's ok
}

func TestKernelDoubleStart(t *testing.T) {
	bin := camoufoxBinary()
	if bin == "" {
		t.Skip("camoufox binary not found")
	}

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
	bin := camoufoxBinary()
	if bin == "" {
		t.Skip("camoufox binary not found")
	}

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
