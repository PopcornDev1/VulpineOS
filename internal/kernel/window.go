package kernel

import (
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// WindowController manages browser window visibility.
type WindowController struct {
	visible   bool
	pid       int
	processName string // resolved macOS process name
	mu        sync.Mutex
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

// HideWhenReady waits for the browser window to appear, then hides it.
func (w *WindowController) HideWhenReady() {
	if runtime.GOOS != "darwin" {
		return
	}

	// Poll until the process has a window, then hide it
	for i := 0; i < 30; i++ { // up to 15 seconds
		time.Sleep(500 * time.Millisecond)
		name := w.resolveProcessName()
		if name != "" {
			w.mu.Lock()
			w.processName = name
			w.hide()
			w.visible = false
			w.mu.Unlock()
			return
		}
	}
}

// resolveProcessName finds the macOS process name for our PID.
// Camoufox may register as "camoufox", "firefox", or "Camoufox" in System Events.
func (w *WindowController) resolveProcessName() string {
	if w.processName != "" {
		return w.processName
	}

	// Ask System Events for the process name matching our PID
	out, err := exec.Command("osascript", "-e",
		`tell application "System Events" to get name of first process whose unix id is `+strconv.Itoa(w.pid),
	).Output()
	if err == nil {
		name := string(out)
		// Trim whitespace/newline
		for len(name) > 0 && (name[len(name)-1] == '\n' || name[len(name)-1] == '\r' || name[len(name)-1] == ' ') {
			name = name[:len(name)-1]
		}
		if name != "" {
			return name
		}
	}
	return ""
}

func (w *WindowController) show() {
	if runtime.GOOS != "darwin" {
		return
	}
	name := w.resolveProcessName()
	if name == "" {
		return
	}
	exec.Command("osascript", "-e",
		`tell application "System Events" to set visible of process "`+name+`" to true`,
	).Run()
	exec.Command("osascript", "-e",
		`tell application "System Events" to set frontmost of process "`+name+`" to true`,
	).Run()
}

func (w *WindowController) hide() {
	if runtime.GOOS != "darwin" {
		return
	}
	name := w.resolveProcessName()
	if name == "" {
		return
	}
	exec.Command("osascript", "-e",
		`tell application "System Events" to set visible of process "`+name+`" to false`,
	).Run()
}
