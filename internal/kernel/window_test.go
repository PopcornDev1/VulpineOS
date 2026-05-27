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

type assertiveError string

func (e assertiveError) Error() string { return string(e) }
