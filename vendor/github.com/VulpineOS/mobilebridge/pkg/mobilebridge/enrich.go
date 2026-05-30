package mobilebridge

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// enrichPerCallTimeout bounds each individual adb shell lookup performed by
// Enrich. A single hung call (locked screen, adb daemon wedge) must not stall
// the other three reads, so each goroutine gets its own child context with
// this deadline. Exposed as a package-level var so tests can shrink it.
var enrichPerCallTimeout = 5 * time.Second

// Enrich fills in AndroidVersion, SDKLevel, RAM_MB and BatteryPercent by
// shelling out to the device via adb. Individual lookups that fail are left
// at their zero values — the caller can rely on the Serial/State/Model/
// Product fields that were already parsed by ListDevices.
//
// This is best-effort: a single unreachable device or a locked screen can
// cause any of the four reads to return garbage. Enrich returns the last
// error encountered only if *every* read failed; a partial success returns
// nil.
func (d *Device) Enrich(ctx context.Context) error {
	if d == nil {
		return errors.New("mobilebridge: nil device")
	}
	if d.Serial == "" {
		return errors.New("mobilebridge: empty serial")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Run the four adb reads in parallel with a per-call timeout so a single
	// hung shell can't stall the others. Each worker stores its parsed value
	// under mu and folds any error into firstErr via record().
	var (
		mu       sync.Mutex
		firstErr error
		okAny    bool
	)
	record := func(ok bool, err error) {
		mu.Lock()
		defer mu.Unlock()
		if ok {
			okAny = true
			return
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	runShell := func(label string, shellArgs []string, parse func([]byte) (bool, error)) {
		cctx, cancel := context.WithTimeout(ctx, enrichPerCallTimeout)
		defer cancel()
		args := append([]string{"-s", d.Serial, "shell"}, shellArgs...)
		out, err := commandRunner(cctx, adbPath, args...)
		if err != nil {
			record(false, fmt.Errorf("%s: %w", label, err))
			return
		}
		ok, perr := parse(out)
		if !ok {
			record(false, fmt.Errorf("%s: %w", label, perr))
			return
		}
		record(true, nil)
	}

	var wg sync.WaitGroup
	wg.Add(4)

	go func() {
		defer wg.Done()
		runShell("android version", []string{"getprop", "ro.build.version.release"},
			func(out []byte) (bool, error) {
				raw := strings.TrimSpace(string(out))
				if raw == "" {
					return false, errors.New("parse android version: empty getprop output")
				}
				mu.Lock()
				d.AndroidVersion = raw
				mu.Unlock()
				return true, nil
			})
	}()

	go func() {
		defer wg.Done()
		runShell("sdk level", []string{"getprop", "ro.build.version.sdk"},
			func(out []byte) (bool, error) {
				raw := strings.TrimSpace(string(out))
				n, perr := strconv.Atoi(raw)
				if perr != nil {
					return false, fmt.Errorf("parse sdk level %q: %w", raw, perr)
				}
				mu.Lock()
				d.SDKLevel = n
				mu.Unlock()
				return true, nil
			})
	}()

	go func() {
		defer wg.Done()
		runShell("ram meminfo", []string{"cat", "/proc/meminfo"},
			func(out []byte) (bool, error) {
				mb := parseMemTotalMB(string(out))
				if mb <= 0 {
					return false, errors.New("parse meminfo: no MemTotal line")
				}
				mu.Lock()
				d.RAM_MB = mb
				mu.Unlock()
				return true, nil
			})
	}()

	go func() {
		defer wg.Done()
		runShell("battery level", []string{"dumpsys", "battery"},
			func(out []byte) (bool, error) {
				pct, ok := parseBatteryLevel(string(out))
				if !ok {
					return false, errors.New("parse dumpsys battery: no level line")
				}
				mu.Lock()
				d.BatteryPercent = pct
				mu.Unlock()
				return true, nil
			})
	}()

	wg.Wait()

	if !okAny {
		if firstErr != nil {
			return firstErr
		}
		return errors.New("mobilebridge: enrich: no fields could be read")
	}
	return nil
}

// parseMemTotalMB extracts the MemTotal line from /proc/meminfo output,
// e.g. "MemTotal:        5879072 kB" → 5741 (MB, rounded down).
func parseMemTotalMB(meminfo string) int {
	for _, line := range strings.Split(meminfo, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.Atoi(fields[1])
		if err != nil {
			return 0
		}
		return kb / 1024
	}
	return 0
}

// parseBatteryLevel extracts the "level:" line from `dumpsys battery` output.
// Typical shape:
//
//	Current Battery Service state:
//	  AC powered: false
//	  USB powered: true
//	  level: 87
//	  scale: 100
//
// Returns the int percentage and ok=true if a level line was found.
func parseBatteryLevel(dumpsys string) (int, bool) {
	for _, line := range strings.Split(dumpsys, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "level:") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "level:"))
		n, err := strconv.Atoi(rest)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}
