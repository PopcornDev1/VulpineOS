package mobilebridge

import (
	"context"
	"time"
)

// DeviceEventType distinguishes hotplug add vs remove.
type DeviceEventType int

const (
	DeviceAdded DeviceEventType = iota
	DeviceRemoved
)

func (t DeviceEventType) String() string {
	switch t {
	case DeviceAdded:
		return "added"
	case DeviceRemoved:
		return "removed"
	default:
		return "unknown"
	}
}

// DeviceEvent reports a single hotplug transition.
type DeviceEvent struct {
	Type   DeviceEventType
	Device Device
}

// watchInterval is the poll interval for WatchDevices. Tests may override.
var watchInterval = 2 * time.Second

// WatchDevices polls `adb devices` every watchInterval and emits events for
// devices that appear or disappear. The channel is closed when ctx is done.
//
// It is best-effort: if an individual ADB call fails it is logged to the
// caller via a dropped tick (the previous known state is retained and a new
// attempt is made on the next tick).
func WatchDevices(ctx context.Context) (<-chan DeviceEvent, error) {
	out := make(chan DeviceEvent, 16)
	go func() {
		defer close(out)
		known := map[string]Device{}
		// Prime: don't emit events for initially-present devices as Added so
		// that consumers can choose to enumerate separately. Actually — emit
		// Added so callers can treat the channel as the single source of
		// truth.
		tick := func() {
			devs, err := ListDevices(ctx)
			if err != nil {
				return
			}
			seen := map[string]struct{}{}
			for _, d := range devs {
				seen[d.Serial] = struct{}{}
				prev, ok := known[d.Serial]
				if !ok {
					known[d.Serial] = d
					select {
					case out <- DeviceEvent{Type: DeviceAdded, Device: d}:
					case <-ctx.Done():
						return
					}
					continue
				}
				if prev.State != d.State {
					// State change — treat as remove+add for simplicity.
					known[d.Serial] = d
					select {
					case out <- DeviceEvent{Type: DeviceRemoved, Device: prev}:
					case <-ctx.Done():
						return
					}
					select {
					case out <- DeviceEvent{Type: DeviceAdded, Device: d}:
					case <-ctx.Done():
						return
					}
				}
			}
			for serial, d := range known {
				if _, ok := seen[serial]; !ok {
					delete(known, serial)
					select {
					case out <- DeviceEvent{Type: DeviceRemoved, Device: d}:
					case <-ctx.Done():
						return
					}
				}
			}
		}

		tick()
		t := time.NewTicker(watchInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				tick()
			}
		}
	}()
	return out, nil
}
