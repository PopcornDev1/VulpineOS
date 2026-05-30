package mobilebridge

import "errors"

// Sentinel errors that callers can match with errors.Is. These wrap the
// lower-level failure sites so the public API is stable even when the
// underlying adb/exec error text changes across platforms.
var (
	// ErrDeviceNotFound is returned when an operation targets a serial
	// that isn't attached (or is in a non-"device" state).
	ErrDeviceNotFound = errors.New("mobilebridge: device not found")

	// ErrADBMissing is returned when exec.LookPath("adb") fails, meaning
	// the adb executable isn't on PATH. Operators hit this on fresh
	// workstations before installing platform-tools.
	ErrADBMissing = errors.New("mobilebridge: adb executable not found in PATH")

	// ErrNoDevtoolsSocket is returned when /proc/net/unix on the device
	// doesn't list a chrome_devtools_remote or webview_devtools_remote
	// abstract socket — usually because Chrome isn't running or USB
	// debugging permission hasn't been granted.
	ErrNoDevtoolsSocket = errors.New("mobilebridge: chrome devtools socket not found on device")
)
