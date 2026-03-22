package kernel

import (
	"os/exec"
	"runtime"
	"sync"
)

// WindowController manages browser window visibility.
// On macOS, uses osascript to show/hide the Camoufox window.
type WindowController struct {
	visible bool
	pid     int
	mu      sync.Mutex
}

// NewWindowController creates a window controller for the given browser PID.
func NewWindowController(pid int) *WindowController {
	return &WindowController{pid: pid}
}

// IsVisible returns whether the browser window is currently shown.
func (w *WindowController) IsVisible() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.visible
}

// Toggle shows the window if hidden, hides if shown.
func (w *WindowController) Toggle() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.visible {
		w.hide()
		w.visible = false
	} else {
		w.show()
		w.visible = true
	}
	return w.visible
}

// Show brings the browser window to the front.
func (w *WindowController) Show() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.show()
	w.visible = true
}

// Hide sends the browser window to the background.
func (w *WindowController) Hide() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.hide()
	w.visible = false
}

func (w *WindowController) show() {
	if runtime.GOOS == "darwin" {
		// Activate the Camoufox/Firefox app and bring to front
		exec.Command("osascript", "-e", `
			tell application "System Events"
				set frontmost of every process whose unix id is `+itoa(w.pid)+` to true
			end tell
		`).Run()
	}
	// Linux: could use wmctrl or xdotool
	// Windows: could use PowerShell
}

func (w *WindowController) hide() {
	if runtime.GOOS == "darwin" {
		// Hide the Camoufox/Firefox app
		exec.Command("osascript", "-e", `
			tell application "System Events"
				set visible of every process whose unix id is `+itoa(w.pid)+` to false
			end tell
		`).Run()
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
