package mobilebridge

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
)

// defaultScreenRecordArgs is the list of flags appended to `adb shell
// screenrecord` when starting a recording. `--time-limit 180` caps the file
// at three minutes (Android's own hard ceiling) and `--bit-rate 4000000`
// picks a reasonable balance for 1080p content.
var defaultScreenRecordArgs = []string{"--time-limit", "180", "--bit-rate", "4000000"}

// remoteScreenRecordPath is where the recording is written on the device.
// It lives under /sdcard so non-root users can read it back via `adb pull`.
const remoteScreenRecordPath = "/sdcard/mobilebridge-screenrecord.mp4"

// screenRecordCmdBuilder lets tests substitute the exec.Cmd factory without
// forking a real adb process.
var screenRecordCmdBuilder = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

// buildScreenRecordArgs returns the full argv (excluding the adb binary
// itself) for `adb -s <serial> shell screenrecord ... <remotePath>`. Broken
// out so tests can verify the command shape without spawning anything.
func buildScreenRecordArgs(serial, remotePath string) []string {
	args := []string{"-s", serial, "shell", "screenrecord"}
	args = append(args, defaultScreenRecordArgs...)
	args = append(args, remotePath)
	return args
}

// screenRecording is the per-Proxy state for a running recording.
type screenRecording struct {
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	remotePath string
	outputPath string
}

// screenRecordings holds one record per proxy. We keep it in a package-level
// map keyed by the proxy pointer so the recording API doesn't bloat Proxy's
// public shape and so tests can clear it between runs. Serialized by mu.
var (
	screenRecordMu sync.Mutex
	screenRecords  = map[*Proxy]*screenRecording{}
)

// StartScreenRecording kicks off `adb shell screenrecord` in the background
// for the proxy's serial and records state so StopScreenRecording can later
// terminate it and pull the file back to outputPath on the host.
//
// The caller's ctx is intentionally NOT used to drive the recording
// subprocess. Recordings live until StopScreenRecording is invoked, so
// tying them to a short-lived request ctx (tool call, HTTP handler) would
// cause the child process to die without flushing the .mp4 file. Instead
// we detach from ctx via context.WithoutCancel and store the per-proxy
// cancel func so Stop can still kill the child deterministically.
//
// Only one recording per Proxy is allowed; calling Start twice returns an
// error without touching the existing recording.
func (p *Proxy) StartScreenRecording(ctx context.Context, outputPath string) error {
	if p == nil {
		return errors.New("mobilebridge: nil proxy")
	}
	if p.serial == "" {
		return errors.New("mobilebridge: proxy has no device serial")
	}
	if outputPath == "" {
		return errors.New("mobilebridge: empty output path")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	screenRecordMu.Lock()
	defer screenRecordMu.Unlock()
	if _, running := screenRecords[p]; running {
		return errors.New("mobilebridge: screen recording already in progress for this proxy")
	}

	// Detach from the caller ctx so parent cancellation (e.g. tool call
	// timeout) does NOT tear down the long-lived recording subprocess.
	detached := context.WithoutCancel(ctx)
	cctx, cancel := context.WithCancel(detached)
	args := buildScreenRecordArgs(p.serial, remoteScreenRecordPath)
	cmd := screenRecordCmdBuilder(cctx, adbPath, args...)
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start screenrecord: %w", err)
	}
	screenRecords[p] = &screenRecording{
		cmd:        cmd,
		cancel:     cancel,
		remotePath: remoteScreenRecordPath,
		outputPath: outputPath,
	}
	return nil
}

// StopScreenRecording terminates the running recording, waits for the device
// to flush the file, then pulls it to the outputPath registered at Start.
// Returns nil if no recording was active.
func (p *Proxy) StopScreenRecording(ctx context.Context) error {
	if p == nil {
		return errors.New("mobilebridge: nil proxy")
	}
	screenRecordMu.Lock()
	rec, ok := screenRecords[p]
	if ok {
		delete(screenRecords, p)
	}
	screenRecordMu.Unlock()
	if !ok {
		return nil
	}

	// Cancel first so screenrecord gets SIGKILLed and flushes the file.
	rec.cancel()
	_ = rec.cmd.Wait()

	// Pull the file back.
	out, err := commandRunner(ctx, adbPath, "-s", p.serial, "pull",
		rec.remotePath, rec.outputPath)
	if err != nil {
		return fmt.Errorf("adb pull screenrecord: %w: %s", err, string(out))
	}
	// Best-effort cleanup on the device.
	_, _ = commandRunner(ctx, adbPath, "-s", p.serial, "shell", "rm", "-f", rec.remotePath)
	return nil
}
