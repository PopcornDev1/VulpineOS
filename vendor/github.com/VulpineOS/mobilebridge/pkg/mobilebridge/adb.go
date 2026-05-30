// Package mobilebridge implements a CDP (Chrome DevTools Protocol) bridge for
// Android Chrome over ADB. See the repo README for an overview.
package mobilebridge

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// Device describes a single Android device as reported by `adb devices -l`
// plus, optionally, fields populated by Enrich from getprop/dumpsys.
type Device struct {
	Serial  string
	State   string // "device", "offline", "unauthorized", ...
	Model   string
	Product string

	// Fields populated by Enrich. Zero values mean "not enriched yet" or
	// "this property couldn't be read" — Enrich is best-effort per field.
	AndroidVersion string
	SDKLevel       int
	RAM_MB         int
	BatteryPercent int
}

// commandRunner runs an external command and returns its combined output.
// It is a package-level variable so tests can stub it out without a real adb
// binary being present.
var commandRunner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

var testOverrideMu sync.RWMutex

// adbPath is the executable used for ADB calls. Tests may override it.
var adbPath = "adb"

// adbLookupFn resolves the adb binary on PATH. Overridable from tests that
// stub commandRunner and don't want a real adb install on the test host.
var adbLookupFn = exec.LookPath

func runADB(ctx context.Context, args ...string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	testOverrideMu.RLock()
	runner := commandRunner
	path := adbPath
	testOverrideMu.RUnlock()
	return runner(ctx, path, args...)
}

// ListDevices runs `adb devices -l` and parses the result. If the adb
// executable isn't on PATH it returns an error wrapping ErrADBMissing so
// callers can errors.Is-check the common operator mistake.
func ListDevices(ctx context.Context) ([]Device, error) {
	testOverrideMu.RLock()
	lookup := adbLookupFn
	path := adbPath
	testOverrideMu.RUnlock()
	if _, lookErr := lookup(path); lookErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrADBMissing, lookErr)
	}
	out, err := runADB(ctx, "devices", "-l")
	if err != nil {
		return nil, fmt.Errorf("adb devices: %w: %s", err, string(out))
	}
	return parseDevices(string(out)), nil
}

// parseDevices parses the textual output of `adb devices -l`.
//
// Example input:
//
//	List of devices attached
//	R58N12ABCDE    device usb:336592896X product:starqltesq model:SM_G960U device:starqltesq transport_id:1
//	emulator-5554  offline
func parseDevices(out string) []Device {
	var devices []Device
	lines := strings.Split(out, "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "List of devices") {
			continue
		}
		if strings.HasPrefix(line, "*") {
			// daemon log lines like "* daemon not running; starting now"
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		d := Device{Serial: fields[0], State: fields[1]}
		for _, kv := range fields[2:] {
			parts := strings.SplitN(kv, ":", 2)
			if len(parts) != 2 {
				continue
			}
			switch parts[0] {
			case "model":
				d.Model = parts[1]
			case "product":
				d.Product = parts[1]
			}
		}
		devices = append(devices, d)
	}
	return devices
}

// Forward runs `adb -s <serial> forward tcp:<localPort> localabstract:<remoteAbstract>`.
// If adb reports the device is not attached, the returned error wraps
// ErrDeviceNotFound so callers can errors.Is-check the operator-common
// "cable unplugged / wrong serial" failure mode.
func Forward(ctx context.Context, serial string, localPort int, remoteAbstract string) error {
	if serial == "" {
		return errors.New("mobilebridge: empty serial")
	}
	out, err := runADB(ctx, "-s", serial, "forward",
		fmt.Sprintf("tcp:%d", localPort),
		"localabstract:"+remoteAbstract,
	)
	if err != nil {
		if isDeviceNotFound(out, err) {
			return fmt.Errorf("%w: serial=%s: %s", ErrDeviceNotFound, serial, strings.TrimSpace(string(out)))
		}
		return fmt.Errorf("adb forward: %w: %s", err, string(out))
	}
	return nil
}

// isDeviceNotFound reports whether an adb command's combined output/error
// indicates the target serial isn't currently attached. adb is not great
// about exit codes here — the signal lives in the text. We match the two
// shapes we've seen in the wild: "error: device 'X' not found" and
// "error: device not found".
func isDeviceNotFound(out []byte, err error) bool {
	msg := strings.ToLower(string(out))
	if err != nil {
		msg += " " + strings.ToLower(err.Error())
	}
	return strings.Contains(msg, "device not found") ||
		strings.Contains(msg, "device '") && strings.Contains(msg, "not found")
}

// Unforward runs `adb -s <serial> forward --remove tcp:<localPort>`.
func Unforward(ctx context.Context, serial string, localPort int) error {
	if serial == "" {
		return errors.New("mobilebridge: empty serial")
	}
	out, err := runADB(ctx, "-s", serial, "forward", "--remove", fmt.Sprintf("tcp:%d", localPort))
	if err != nil {
		return fmt.Errorf("adb forward --remove: %w: %s", err, string(out))
	}
	return nil
}

// devtoolsSocketRe matches abstract socket names for Chrome's devtools socket.
// Typical forms: "chrome_devtools_remote" and "webview_devtools_remote_<pid>".
var devtoolsSocketRe = regexp.MustCompile(`@((?:chrome|webview)_devtools_remote[_A-Za-z0-9]*)`)

// DevtoolsSocketKind distinguishes Chrome's stable devtools socket from a
// WebView host's. CLI tools can use this to label entries clearly.
type DevtoolsSocketKind int

const (
	// SocketKindUnknown is a devtools socket we couldn't classify.
	SocketKindUnknown DevtoolsSocketKind = iota
	// SocketKindChrome is a full Chrome process (@chrome_devtools_remote).
	SocketKindChrome
	// SocketKindWebView is a WebView host process
	// (@webview_devtools_remote_<pid>).
	SocketKindWebView
)

func (k DevtoolsSocketKind) String() string {
	switch k {
	case SocketKindChrome:
		return "chrome"
	case SocketKindWebView:
		return "webview"
	default:
		return "unknown"
	}
}

// DevtoolsSocket describes an abstract socket exposing a Chrome DevTools
// endpoint on the device. The Name field is the abstract socket name (no
// leading @); Kind tells the caller whether to show "Chrome" or "WebView".
type DevtoolsSocket struct {
	Name string
	Kind DevtoolsSocketKind
}

// ChromeDevtoolsSocket queries /proc/net/unix on the device and returns the
// name of the abstract socket that Chrome (or a WebView host) is listening
// on. It returns just the socket name for backwards compatibility; callers
// that need the Chrome-vs-WebView distinction should use
// ChromeDevtoolsSocketInfo instead.
func ChromeDevtoolsSocket(ctx context.Context, serial string) (string, error) {
	info, err := ChromeDevtoolsSocketInfo(ctx, serial)
	if err != nil {
		return "", err
	}
	return info.Name, nil
}

// ChromeDevtoolsSocketInfo is like ChromeDevtoolsSocket but also reports
// whether the socket belongs to Chrome or a WebView host.
func ChromeDevtoolsSocketInfo(ctx context.Context, serial string) (DevtoolsSocket, error) {
	if serial == "" {
		return DevtoolsSocket{}, errors.New("mobilebridge: empty serial")
	}
	out, err := runADB(ctx, "-s", serial, "shell", "cat", "/proc/net/unix")
	if err != nil {
		return DevtoolsSocket{}, fmt.Errorf("adb shell cat /proc/net/unix: %w: %s", err, string(out))
	}
	name, ok := parseDevtoolsSocket(string(out))
	if !ok {
		return DevtoolsSocket{}, ErrNoDevtoolsSocket
	}
	kind := SocketKindUnknown
	switch {
	case strings.HasPrefix(name, "chrome_devtools_remote"):
		kind = SocketKindChrome
	case strings.HasPrefix(name, "webview_devtools_remote"):
		kind = SocketKindWebView
	}
	return DevtoolsSocket{Name: name, Kind: kind}, nil
}

// parseDevtoolsSocket scans /proc/net/unix output for a devtools abstract
// socket. Abstract sockets in that file are prefixed with '@'.
//
// Preference order: chrome_devtools_remote (regular Chrome tabs) wins over
// webview_devtools_remote_<pid> (a WebView host process), since Chrome itself
// is almost always what a caller wants.
func parseDevtoolsSocket(procNetUnix string) (string, bool) {
	var webview string
	for _, line := range strings.Split(procNetUnix, "\n") {
		m := devtoolsSocketRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		if name == "chrome_devtools_remote" || name == "chrome_devtools_remote_0" {
			return name, true
		}
		if strings.HasPrefix(name, "chrome_devtools_remote") {
			return name, true
		}
		if webview == "" && strings.HasPrefix(name, "webview_devtools_remote") {
			webview = name
		}
	}
	if webview != "" {
		return webview, true
	}
	return "", false
}
