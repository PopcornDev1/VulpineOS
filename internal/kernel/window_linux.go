//go:build linux

package kernel

import (
	"fmt"
	"io"
	"strings"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/ewmh"
	"github.com/jezek/xgbutil/icccm"
)

// xgb logs to its own log.Logger writing straight to os.Stderr, which bypasses
// the TUI's log-file redirect and leaks lines like "XGB: Could not get authority
// info" into the terminal UI. On WSLg there is no ~/.Xauthority and the
// auth-less fallback connection works fine, so these notices are pure noise —
// silence the xgb logger at package init.
func init() {
	xgb.Logger.SetOutput(io.Discard)
}

// Linux (X11 / WSLg) window control via a pure-Go X11 client — no external tools
// (xdotool/wmctrl) required. WSLg's compositor doesn't implement _NET_CLIENT_LIST,
// so windows are found by walking the X window tree (QueryTree) and matching each
// window's _NET_WM_PID (an app-set property) against the browser PID tree, with a
// WM_CLASS fallback. Matched windows are mapped (shown) / unmapped (hidden).

func isBrowserClass(cls *icccm.WmClass) bool {
	if cls == nil {
		return false
	}
	for _, s := range []string{cls.Instance, cls.Class} {
		l := strings.ToLower(s)
		if strings.Contains(l, "vulpine") || strings.Contains(l, "camoufox") || strings.Contains(l, "firefox") || strings.Contains(l, "navigator") {
			return true
		}
	}
	return false
}

// browserWindows walks the X tree for windows owned by the browser process tree,
// returning them plus an open X connection the caller must close.
func (w *WindowController) browserWindows() ([]xproto.Window, *xgbutil.XUtil, error) {
	X, err := xgbutil.NewConn()
	if err != nil {
		return nil, nil, err
	}
	pidset := make(map[uint]bool)
	for _, pid := range w.candidatePIDs() {
		pidset[uint(pid)] = true
	}
	seen := make(map[xproto.Window]bool)
	var matched []xproto.Window
	var walk func(win xproto.Window, depth int)
	walk = func(win xproto.Window, depth int) {
		if depth > 5 {
			return
		}
		if !seen[win] {
			seen[win] = true
			if pid, perr := ewmh.WmPidGet(X, win); perr == nil && pidset[pid] {
				matched = append(matched, win)
			} else if cls, cerr := icccm.WmClassGet(X, win); cerr == nil && isBrowserClass(cls) {
				matched = append(matched, win)
			}
		}
		tree, terr := xproto.QueryTree(X.Conn(), win).Reply()
		if terr != nil || tree == nil {
			return
		}
		for _, c := range tree.Children {
			walk(c, depth+1)
		}
	}
	walk(X.RootWin(), 0)
	return matched, X, nil
}

// linuxWindowIDs lists the browser's X11 window ids; onlyVisible restricts to
// currently-mapped (shown) windows.
func (w *WindowController) linuxWindowIDs(onlyVisible bool) []string {
	wins, X, err := w.browserWindows()
	if err != nil {
		return nil
	}
	defer X.Conn().Close()
	var ids []string
	for _, win := range wins {
		if onlyVisible {
			attr, aerr := xproto.GetWindowAttributes(X.Conn(), win).Reply()
			if aerr != nil || attr.MapState != xproto.MapStateViewable {
				continue
			}
		}
		ids = append(ids, fmt.Sprintf("%d", win))
	}
	return ids
}

// linuxSetVisible maps (shows) or unmaps (hides) the browser's X11 windows.
// Hide unmaps only currently-mapped windows and records them; Show re-maps just
// those recorded windows (so always-hidden helper windows aren't surfaced).
//
// The caller (show()/hide() via Show/Hide/Toggle) already holds w.mu, so this
// accesses w.hiddenWindowIDs directly without locking (the mutex is not
// reentrant — locking here would deadlock).
func (w *WindowController) linuxSetVisible(visible bool) error {
	wins, X, err := w.browserWindows()
	if err != nil {
		return fmt.Errorf("x11 connect: %w", err)
	}
	defer X.Conn().Close()

	if visible {
		toMap := wins
		if len(w.hiddenWindowIDs) > 0 {
			toMap = nil
			for _, id := range w.hiddenWindowIDs {
				toMap = append(toMap, xproto.Window(id))
			}
		}
		if len(toMap) == 0 {
			return fmt.Errorf("no browser window found yet")
		}
		var lastErr error
		for _, win := range toMap {
			if cerr := xproto.MapWindowChecked(X.Conn(), win).Check(); cerr != nil {
				lastErr = cerr
			}
		}
		_ = ewmh.ActiveWindowReq(X, toMap[0])
		w.hiddenWindowIDs = nil
		return lastErr
	}

	// Hide: unmap currently-mapped windows and remember them.
	var hidden []uint32
	var lastErr error
	for _, win := range wins {
		attr, aerr := xproto.GetWindowAttributes(X.Conn(), win).Reply()
		if aerr != nil || attr.MapState != xproto.MapStateViewable {
			continue
		}
		if cerr := xproto.UnmapWindowChecked(X.Conn(), win).Check(); cerr != nil {
			lastErr = cerr
			continue
		}
		hidden = append(hidden, uint32(win))
	}
	if len(hidden) == 0 && lastErr == nil {
		return fmt.Errorf("no visible browser window to hide")
	}
	w.hiddenWindowIDs = hidden
	return lastErr
}
