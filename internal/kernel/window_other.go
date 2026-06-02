//go:build !linux

package kernel

// Non-Linux stubs for the Linux/X11 window helpers. Window control on macOS uses
// AppleScript in window.go; these are never called at runtime off Linux (guarded
// by runtime.GOOS), but must exist for cross-platform compilation.

func (w *WindowController) linuxWindowIDs(onlyVisible bool) []string { return nil }

func (w *WindowController) linuxSetVisible(visible bool) error { return nil }
