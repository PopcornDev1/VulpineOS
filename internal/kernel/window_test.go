package kernel

import (
	"runtime"
	"strings"
	"testing"
)

func TestParseAppleScriptBool(t *testing.T) {
	tests := []struct {
		input string
		want  bool
		ok    bool
	}{
		{input: "true\n", want: true, ok: true},
		{input: " false ", want: false, ok: true},
		{input: "maybe", want: false, ok: false},
	}

	for _, tt := range tests {
		got, ok := parseAppleScriptBool(tt.input)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("parseAppleScriptBool(%q) = (%v, %v), want (%v, %v)", tt.input, got, ok, tt.want, tt.ok)
		}
	}
}

func TestToggleRefreshesVisibleStateBeforeHiding(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific window visibility test")
	}

	original := runWindowCommand
	defer func() { runWindowCommand = original }()

	var calls []string
	runWindowCommand = func(name string, args ...string) (string, error) {
		call := name + " " + strings.Join(args, " ")
		calls = append(calls, call)
		switch {
		case name == "ps":
			return "123 1 camoufox\n", nil
		case strings.Contains(call, "get visible of first process whose unix id is 123"):
			return "true\n", nil
		case strings.Contains(call, "set visible of first process whose unix id is 123 to false"):
			return "", nil
		default:
			return "", nil
		}
	}

	w := NewWindowController(123)
	w.visible = false

	visible, err := w.Toggle()
	if err != nil {
		t.Fatalf("Toggle() error = %v", err)
	}
	if visible {
		t.Fatalf("Toggle() visible = %v, want false", visible)
	}
	if len(calls) < 4 || !strings.Contains(calls[1], "get visible") || !strings.Contains(calls[3], "set visible") {
		t.Fatalf("unexpected call order: %#v", calls)
	}
}

func TestToggleRefreshesVisibleStateBeforeShowing(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific window visibility test")
	}

	original := runWindowCommand
	defer func() { runWindowCommand = original }()

	var calls []string
	runWindowCommand = func(name string, args ...string) (string, error) {
		call := name + " " + strings.Join(args, " ")
		calls = append(calls, call)
		switch {
		case name == "ps":
			return "123 1 camoufox\n", nil
		case strings.Contains(call, "get visible of first process whose unix id is 123"):
			return "false\n", nil
		case strings.Contains(call, "set visible of first process whose unix id is 123 to true"):
			return "", nil
		case strings.Contains(call, "set frontmost of first process whose unix id is 123 to true"):
			return "", nil
		default:
			return "", nil
		}
	}

	w := NewWindowController(123)
	w.visible = true

	visible, err := w.Toggle()
	if err != nil {
		t.Fatalf("Toggle() error = %v", err)
	}
	if !visible {
		t.Fatalf("Toggle() visible = %v, want true", visible)
	}
	if len(calls) < 5 || !strings.Contains(calls[1], "get visible") || !strings.Contains(calls[3], "set visible") || !strings.Contains(calls[4], "set frontmost") {
		t.Fatalf("unexpected call order: %#v", calls)
	}
}

func TestStatusRefreshesVisibleState(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific window visibility test")
	}

	original := runWindowCommand
	defer func() { runWindowCommand = original }()

	runWindowCommand = func(name string, args ...string) (string, error) {
		call := name + " " + strings.Join(args, " ")
		switch {
		case name == "ps":
			return "123 1 camoufox\n", nil
		case strings.Contains(call, "get visible of first process whose unix id is 123"):
			return "true\n", nil
		default:
			return "", nil
		}
	}

	w := NewWindowController(123)
	visible, found := w.Status()
	if !found {
		t.Fatal("Status() found = false, want true")
	}
	if !visible {
		t.Fatal("Status() visible = false, want true")
	}
}

func TestStatusChecksParentWhenHelperProcessReportsHidden(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific window visibility test")
	}

	original := runWindowCommand
	defer func() { runWindowCommand = original }()

	runWindowCommand = func(name string, args ...string) (string, error) {
		call := name + " " + strings.Join(args, " ")
		switch {
		case name == "ps":
			return "123 1 camoufox\n200 123 plugin-container\n", nil
		case strings.Contains(call, "get visible of first process whose unix id is 200"):
			return "false\n", nil
		case strings.Contains(call, "get visible of first process whose unix id is 123"):
			return "true\n", nil
		default:
			return "", nil
		}
	}

	w := NewWindowController(123)
	visible, found := w.Status()
	if !found {
		t.Fatal("Status() found = false, want true")
	}
	if !visible {
		t.Fatal("Status() visible = false, want true")
	}
}

func TestCachedStatusDoesNotRunWindowCommands(t *testing.T) {
	original := runWindowCommand
	defer func() { runWindowCommand = original }()

	runWindowCommand = func(name string, args ...string) (string, error) {
		t.Fatalf("CachedStatus should not run %s %#v", name, args)
		return "", nil
	}

	w := NewWindowController(123)
	w.visible = true
	w.found = true

	visible, found := w.CachedStatus()
	if !found {
		t.Fatal("CachedStatus() found = false, want true")
	}
	if !visible {
		t.Fatal("CachedStatus() visible = false, want true")
	}
}

func TestContextWindowActionsUpdateCachedStatus(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		original := runWindowCommand
		defer func() { runWindowCommand = original }()
		runWindowCommand = func(name string, args ...string) (string, error) {
			call := name + " " + strings.Join(args, " ")
			if name == "ps" {
				return "123 1 camoufox\n", nil
			}
			if runtime.GOOS == "linux" {
				switch call {
				case "xdotool search --pid 123", "xdotool windowmap 1001", "xdotool windowactivate 1001", "xdotool windowminimize 1001":
					if call == "xdotool search --pid 123" {
						return "1001\n", nil
					}
					return "", nil
				}
			}
			return "", nil
		}
	}

	w := NewWindowController(123)
	if err := w.ShowContext("missing-context"); err != nil {
		t.Fatalf("ShowContext: %v", err)
	}
	visible, found := w.CachedStatus()
	if !found || !visible {
		t.Fatalf("CachedStatus after ShowContext = (%v, %v), want visible and found", visible, found)
	}

	if err := w.HideContext("missing-context"); err != nil {
		t.Fatalf("HideContext: %v", err)
	}
	visible, found = w.CachedStatus()
	if !found || visible {
		t.Fatalf("CachedStatus after HideContext = (%v, %v), want hidden and found", visible, found)
	}
}

func TestHideAttemptsParentAfterHelperProcess(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific window visibility test")
	}

	original := runWindowCommand
	defer func() { runWindowCommand = original }()

	var calls []string
	runWindowCommand = func(name string, args ...string) (string, error) {
		call := name + " " + strings.Join(args, " ")
		calls = append(calls, call)
		switch {
		case name == "ps":
			return "123 1 camoufox\n200 123 plugin-container\n", nil
		case strings.Contains(call, "set visible of first process whose unix id is 200 to false"):
			return "", nil
		case strings.Contains(call, "set visible of first process whose unix id is 123 to false"):
			return "", nil
		default:
			return "", nil
		}
	}

	w := NewWindowController(123)
	if err := w.Hide(); err != nil {
		t.Fatalf("Hide() error = %v", err)
	}
	var hidHelper, hidParent bool
	for _, call := range calls {
		if strings.Contains(call, "unix id is 200 to false") {
			hidHelper = true
		}
		if strings.Contains(call, "unix id is 123 to false") {
			hidParent = true
		}
	}
	if !hidHelper || !hidParent {
		t.Fatalf("Hide() calls = %#v, want helper and parent hide attempts", calls)
	}
}

func TestShowReturnsUnderlyingAppleScriptError(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific window visibility test")
	}

	original := runWindowCommand
	defer func() { runWindowCommand = original }()

	runWindowCommand = func(name string, args ...string) (string, error) {
		call := name + " " + strings.Join(args, " ")
		switch {
		case name == "ps":
			return "123 1 camoufox\n", nil
		case strings.Contains(call, "set visible of first process whose unix id is 123 to true"):
			return "", assertiveError("not authorized")
		default:
			return "", nil
		}
	}

	w := NewWindowController(123)
	err := w.Show()
	if err == nil {
		t.Fatal("Show() error = nil, want propagated osascript error")
	}
	if !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("Show() error = %v, want propagated osascript detail", err)
	}
}

func TestLinuxHideMinimizesBrowserWindow(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-specific window minimization test")
	}

	original := runWindowCommand
	defer func() { runWindowCommand = original }()

	var calls []string
	runWindowCommand = func(name string, args ...string) (string, error) {
		call := name + " " + strings.Join(args, " ")
		calls = append(calls, call)
		switch call {
		case "ps -axo pid=,ppid=,comm=":
			return "123 1 camoufox\n", nil
		case "xdotool search --pid 123":
			return "1001\n", nil
		case "xdotool windowminimize 1001":
			return "", nil
		default:
			return "", assertiveError("unexpected call: " + call)
		}
	}

	w := NewWindowController(123)
	if err := w.Hide(); err != nil {
		t.Fatalf("Hide() error = %v", err)
	}
	visible, found := w.CachedStatus()
	if !found || visible {
		t.Fatalf("CachedStatus after Hide = (%v, %v), want hidden and found", visible, found)
	}
	if !containsCall(calls, "xdotool windowminimize 1001") {
		t.Fatalf("Hide() calls = %#v, want xdotool windowminimize", calls)
	}
}

func TestLinuxHideFallsBackToWMCTRL(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-specific window minimization test")
	}

	original := runWindowCommand
	defer func() { runWindowCommand = original }()

	var calls []string
	runWindowCommand = func(name string, args ...string) (string, error) {
		call := name + " " + strings.Join(args, " ")
		calls = append(calls, call)
		switch call {
		case "ps -axo pid=,ppid=,comm=":
			return "123 1 camoufox\n", nil
		case "xdotool search --pid 123":
			return "", assertiveError("xdotool unavailable")
		case "wmctrl -lp":
			return "0x03200007  0 123 host VulpineOS\n", nil
		case "xdotool windowminimize 0x03200007":
			return "", assertiveError("xdotool unavailable")
		case "wmctrl -ir 0x03200007 -b add,hidden":
			return "", nil
		default:
			return "", assertiveError("unexpected call: " + call)
		}
	}

	w := NewWindowController(123)
	if err := w.Hide(); err != nil {
		t.Fatalf("Hide() error = %v", err)
	}
	if !containsCall(calls, "wmctrl -ir 0x03200007 -b add,hidden") {
		t.Fatalf("Hide() calls = %#v, want wmctrl hidden fallback", calls)
	}
}

func TestLinuxStatusDetectsHiddenWindow(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-specific window visibility test")
	}

	original := runWindowCommand
	defer func() { runWindowCommand = original }()

	runWindowCommand = func(name string, args ...string) (string, error) {
		call := name + " " + strings.Join(args, " ")
		switch call {
		case "ps -axo pid=,ppid=,comm=":
			return "123 1 camoufox\n", nil
		case "xdotool search --pid 123":
			return "1001\n", nil
		case "xprop -id 1001 _NET_WM_STATE":
			return "_NET_WM_STATE(ATOM) = _NET_WM_STATE_HIDDEN\n", nil
		default:
			return "", assertiveError("unexpected call: " + call)
		}
	}

	w := NewWindowController(123)
	visible, found := w.Status()
	if !found || visible {
		t.Fatalf("Status() = (%v, %v), want hidden and found", visible, found)
	}
}

type assertiveError string

func (e assertiveError) Error() string { return string(e) }

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}
