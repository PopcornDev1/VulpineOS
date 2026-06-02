package kernel

import (
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var runWindowCommand = func(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// WindowController manages browser window visibility.
type WindowController struct {
	visible        bool
	found          bool
	pid            int
	targetPID      int
	mu             sync.Mutex
	contextWindows map[string][]int
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

// CachedStatus returns the latest known visible state without polling the OS.
func (w *WindowController) CachedStatus() (bool, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !windowControlSupported() {
		return w.visible, true
	}
	return w.visible, w.found
}

// Status returns the latest visible state and whether a window process could be found.
func (w *WindowController) Status() (bool, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	found := w.refreshVisibleLocked()
	return w.visible, found
}

func (w *WindowController) setCachedStatus(visible, found bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.visible = visible
	w.found = found
}

// Toggle shows the window if hidden, hides if shown.
func (w *WindowController) Toggle() (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.refreshVisibleLocked()
	if w.visible {
		if err := w.hide(); err != nil {
			return w.visible, err
		}
		w.visible = false
		w.found = true
	} else {
		if err := w.show(); err != nil {
			return w.visible, err
		}
		w.visible = true
		w.found = true
	}
	return w.visible, nil
}

// Show brings the browser window to the front.
func (w *WindowController) Show() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.show(); err != nil {
		return err
	}
	w.visible = true
	w.found = true
	return nil
}

// Hide sends the browser window to the background.
func (w *WindowController) Hide() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.hide(); err != nil {
		return err
	}
	w.visible = false
	w.found = true
	return nil
}

// HideWhenReady waits for the browser window to appear, then hides it.
func (w *WindowController) HideWhenReady() {
	if !windowControlSupported() {
		return
	}

	// Poll until the process has a window, then hide it
	for i := 0; i < 30; i++ { // up to 15 seconds
		time.Sleep(500 * time.Millisecond)
		if err := w.Hide(); err == nil {
			return
		}
	}
}

// RegisterContextWindow registers a window PID for a context.
func (w *WindowController) RegisterContextWindow(contextID string, pid int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.contextWindows == nil {
		w.contextWindows = make(map[string][]int)
	}
	for _, p := range w.contextWindows[contextID] {
		if p == pid {
			return
		}
	}
	w.contextWindows[contextID] = append(w.contextWindows[contextID], pid)
}

// GetContextPIDs returns the window PIDs for a context.
func (w *WindowController) GetContextPIDs(contextID string) []int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.contextWindows[contextID]
}

// ShowContext shows the window(s) for a specific context.
func (w *WindowController) ShowContext(contextID string) error {
	w.mu.Lock()
	pids := w.contextWindows[contextID]
	w.mu.Unlock()

	if len(pids) == 0 {
		// Fall back to main window if no context windows registered
		wc := NewWindowController(w.pid)
		if err := wc.Show(); err != nil {
			return err
		}
		w.setCachedStatus(true, true)
		return nil
	}
	var lastErr error
	for _, pid := range pids {
		wc := NewWindowController(pid)
		if err := wc.Show(); err != nil {
			lastErr = err
			continue
		}
		w.setCachedStatus(true, true)
		return nil
	}
	return lastErr
}

// HideContext hides the window(s) for a specific context.
func (w *WindowController) HideContext(contextID string) error {
	w.mu.Lock()
	pids := w.contextWindows[contextID]
	w.mu.Unlock()

	if len(pids) == 0 {
		// Fall back to main window if no context windows registered
		wc := NewWindowController(w.pid)
		if err := wc.Hide(); err != nil {
			return err
		}
		w.setCachedStatus(false, true)
		return nil
	}
	var lastErr error
	for _, pid := range pids {
		wc := NewWindowController(pid)
		if err := wc.Hide(); err != nil {
			lastErr = err
			continue
		}
		w.setCachedStatus(false, true)
		return nil
	}
	return lastErr
}

// HideAll hides all tracked context windows.
func (w *WindowController) HideAll() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.contextWindows) == 0 {
		if err := w.hide(); err != nil {
			return err
		}
		w.visible = false
		w.found = true
		return nil
	}

	var lastErr error
	for contextID, pids := range w.contextWindows {
		for _, pid := range pids {
			wc := NewWindowController(pid)
			if err := wc.Hide(); err != nil {
				lastErr = err
			}
		}
		_ = contextID
	}
	if lastErr == nil {
		w.visible = false
		w.found = true
	}
	return lastErr
}

// IsContextVisible checks if any window for a context is visible.
func (w *WindowController) IsContextVisible(contextID string) bool {
	w.mu.Lock()
	pids := w.contextWindows[contextID]
	w.mu.Unlock()

	if len(pids) == 0 {
		visible, _ := w.Status()
		return visible
	}

	for _, pid := range pids {
		wc := NewWindowController(pid)
		if visible, _ := wc.Status(); visible {
			return true
		}
	}
	return false
}

func (w *WindowController) show() error {
	if runtime.GOOS == "linux" {
		return w.showLinux()
	}
	if runtime.GOOS != "darwin" {
		return nil
	}
	var lastErr error
	for _, pid := range w.candidatePIDs() {
		if _, err := runWindowCommand("osascript", "-e",
			`tell application "System Events" to set visible of first process whose unix id is `+strconv.Itoa(pid)+` to true`,
		); err != nil {
			lastErr = err
			continue
		}
		if _, err := runWindowCommand("osascript", "-e",
			`tell application "System Events" to set frontmost of first process whose unix id is `+strconv.Itoa(pid)+` to true`,
		); err != nil {
			lastErr = err
			continue
		}
		w.targetPID = pid
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("show browser process tree rooted at %d: %w", w.pid, lastErr)
	}
	return fmt.Errorf("show browser process tree rooted at %d", w.pid)
}

func (w *WindowController) hide() error {
	if runtime.GOOS == "linux" {
		return w.hideLinux()
	}
	if runtime.GOOS != "darwin" {
		return nil
	}
	var lastErr error
	var hidden bool
	for _, pid := range w.candidatePIDs() {
		if _, err := runWindowCommand("osascript", "-e",
			`tell application "System Events" to set visible of first process whose unix id is `+strconv.Itoa(pid)+` to false`,
		); err != nil {
			lastErr = err
			continue
		}
		hidden = true
		w.targetPID = pid
	}
	if hidden {
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("hide browser process tree rooted at %d: %w", w.pid, lastErr)
	}
	return fmt.Errorf("hide browser process tree rooted at %d", w.pid)
}

func (w *WindowController) candidatePIDs() []int {
	out, err := runWindowCommand("ps", "-axo", "pid=,ppid=,comm=")
	if err != nil {
		if w.targetPID != 0 {
			return []int{w.targetPID, w.pid}
		}
		return []int{w.pid}
	}

	children := make(map[int][]int)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		children[ppid] = append(children[ppid], pid)
	}

	seen := map[int]struct{}{w.pid: {}}
	queue := []int{w.pid}
	var ordered []int
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		ordered = append(ordered, pid)
		next := children[pid]
		sort.Ints(next)
		for _, child := range next {
			if _, ok := seen[child]; ok {
				continue
			}
			seen[child] = struct{}{}
			queue = append(queue, child)
		}
	}

	// Prefer descendants before the launcher PID on macOS app bundles.
	if len(ordered) <= 1 {
		if w.targetPID != 0 && (len(ordered) == 0 || ordered[0] != w.targetPID) {
			return append([]int{w.targetPID}, ordered...)
		}
		return ordered
	}
	ordered = append(ordered[1:], ordered[0])
	if w.targetPID != 0 {
		filtered := []int{w.targetPID}
		for _, pid := range ordered {
			if pid != w.targetPID {
				filtered = append(filtered, pid)
			}
		}
		return filtered
	}
	return ordered
}

func (w *WindowController) refreshVisibleLocked() bool {
	if runtime.GOOS == "linux" {
		return w.refreshLinuxVisibleLocked()
	}
	if runtime.GOOS != "darwin" {
		w.found = true
		return true
	}
	var found bool
	var lastFoundPID int
	for _, pid := range w.candidatePIDs() {
		out, err := runWindowCommand("osascript", "-e",
			`tell application "System Events" to get visible of first process whose unix id is `+strconv.Itoa(pid),
		)
		if err != nil {
			continue
		}
		visible, ok := parseAppleScriptBool(out)
		if !ok {
			continue
		}
		found = true
		lastFoundPID = pid
		if visible {
			w.targetPID = pid
			w.visible = true
			w.found = true
			return true
		}
	}
	if found {
		w.targetPID = lastFoundPID
		w.visible = false
		w.found = true
		return true
	}
	w.found = false
	return false
}

func windowControlSupported() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "linux"
}

func (w *WindowController) showLinux() error {
	ids := w.linuxWindowIDs()
	if len(ids) == 0 {
		return fmt.Errorf("show browser process tree rooted at %d", w.pid)
	}

	var lastErr error
	for _, id := range ids {
		if err := showLinuxWindow(id); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("show browser process tree rooted at %d: %w", w.pid, lastErr)
	}
	return fmt.Errorf("show browser process tree rooted at %d", w.pid)
}

func (w *WindowController) hideLinux() error {
	ids := w.linuxWindowIDs()
	if len(ids) == 0 {
		return fmt.Errorf("hide browser process tree rooted at %d", w.pid)
	}

	var lastErr error
	var hidden bool
	for _, id := range ids {
		if err := hideLinuxWindow(id); err != nil {
			lastErr = err
			continue
		}
		hidden = true
	}
	if hidden {
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("hide browser process tree rooted at %d: %w", w.pid, lastErr)
	}
	return fmt.Errorf("hide browser process tree rooted at %d", w.pid)
}

func hideLinuxWindow(id string) error {
	if _, err := runWindowCommand("xdotool", "windowminimize", id); err == nil {
		return nil
	} else if _, fallbackErr := runWindowCommand("wmctrl", "-ir", id, "-b", "add,hidden"); fallbackErr != nil {
		return err
	}
	return nil
}

func showLinuxWindow(id string) error {
	if _, err := runWindowCommand("xdotool", "windowmap", id); err == nil {
		_, _ = runWindowCommand("xdotool", "windowactivate", id)
		return nil
	} else if _, fallbackErr := runWindowCommand("wmctrl", "-ia", id); fallbackErr != nil {
		return err
	}
	return nil
}

func (w *WindowController) refreshLinuxVisibleLocked() bool {
	ids := w.linuxWindowIDs()
	if len(ids) == 0 {
		w.found = false
		return false
	}

	w.found = true
	w.visible = false
	for _, id := range ids {
		hidden, ok := linuxWindowHidden(id)
		if !ok || !hidden {
			w.visible = true
			return true
		}
	}
	return true
}

func linuxWindowHidden(id string) (bool, bool) {
	out, err := runWindowCommand("xprop", "-id", id, "_NET_WM_STATE")
	if err != nil {
		return false, false
	}
	return strings.Contains(out, "_NET_WM_STATE_HIDDEN"), true
}

func (w *WindowController) linuxWindowIDs() []string {
	pids := w.candidatePIDs()
	if len(pids) == 0 {
		return nil
	}
	if ids := linuxWindowIDsFromXDoTool(pids); len(ids) > 0 {
		return ids
	}
	return linuxWindowIDsFromWMCTRL(pids)
}

func linuxWindowIDsFromXDoTool(pids []int) []string {
	seen := make(map[string]struct{})
	var ids []string
	for _, pid := range pids {
		out, err := runWindowCommand("xdotool", "search", "--pid", strconv.Itoa(pid))
		if err != nil {
			continue
		}
		ids = appendWindowIDs(ids, seen, out)
	}
	return ids
}

func linuxWindowIDsFromWMCTRL(pids []int) []string {
	out, err := runWindowCommand("wmctrl", "-lp")
	if err != nil {
		return nil
	}
	pidSet := make(map[int]struct{}, len(pids))
	for _, pid := range pids {
		pidSet[pid] = struct{}{}
	}
	seen := make(map[string]struct{})
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		if _, ok := pidSet[pid]; !ok {
			continue
		}
		ids = appendWindowIDs(ids, seen, fields[0])
	}
	return ids
}

func appendWindowIDs(ids []string, seen map[string]struct{}, out string) []string {
	for _, id := range strings.Fields(out) {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func parseAppleScriptBool(out string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(out)) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}
