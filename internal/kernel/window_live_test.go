package kernel

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestLive_WindowHideShow verifies the browser window auto-hides on launch and
// that Show/Hide toggle it (Linux/X11 via WSLg, or macOS). Gated by
// VULPINEOS_RUN_LIVE=1 + CAMOUFOX_BINARY.
func TestLive_WindowHideShow(t *testing.T) {
	if os.Getenv("VULPINEOS_RUN_LIVE") == "" {
		t.Skip("set VULPINEOS_RUN_LIVE=1 to run the live window test")
	}
	binary := strings.TrimSpace(os.Getenv("CAMOUFOX_BINARY"))
	if binary == "" {
		t.Skip("set CAMOUFOX_BINARY to the camoufox-bin path")
	}

	k := New()
	if err := k.Start(Config{BinaryPath: binary, Headless: false}); err != nil {
		t.Fatalf("start kernel: %v", err)
	}
	defer k.Stop()
	if _, err := k.Client().Call("", "Browser.enable", map[string]interface{}{"attachToDefaultContext": true}); err != nil {
		t.Fatalf("Browser.enable: %v", err)
	}
	win := k.Window()
	if win == nil {
		t.Fatal("no window controller")
	}

	// Wait for the window to appear (X11 enumeration works).
	appeared := false
	for i := 0; i < 30; i++ {
		time.Sleep(500 * time.Millisecond)
		if len(win.linuxWindowIDs(false)) > 0 {
			appeared = true
			break
		}
	}
	if !appeared {
		t.Fatal("browser window never appeared in X11 tree")
	}

	// It should auto-hide on launch (HideWhenReady runs shortly after Start).
	autoHidden := false
	for i := 0; i < 20; i++ {
		if visible, _ := win.Status(); !visible {
			autoHidden = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !autoHidden {
		t.Fatal("window did not auto-hide on launch")
	}
	t.Logf("auto-hide on launch OK")

	if err := win.Show(); err != nil {
		t.Fatalf("Show: %v", err)
	}
	if visible, _ := win.Status(); !visible {
		t.Fatal("window not visible after Show")
	}
	t.Logf("show OK")

	if err := win.Hide(); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if visible, _ := win.Status(); visible {
		t.Fatal("window still visible after Hide")
	}
	t.Logf("hide OK")
}
