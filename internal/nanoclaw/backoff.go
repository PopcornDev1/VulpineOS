package nanoclaw

import "time"

// Backoff provides adaptive polling intervals. It shrinks toward min when work
// is found and grows toward max after consecutive idle cycles.
type Backoff struct {
	Min       time.Duration
	Max       time.Duration
	current   time.Duration
	idleCount int
	idleLimit int
}

// defaultBackoff creates a Backoff with sensible defaults:
// 10ms min, 500ms max, doubles after 3 consecutive idle ticks.
func defaultBackoff() Backoff {
	return Backoff{
		Min:       10 * time.Millisecond,
		Max:       500 * time.Millisecond,
		current:   10 * time.Millisecond,
		idleLimit: 3,
	}
}

// After returns a channel that fires after the current interval.
// Use Tick to advance the state for the next cycle.
func (b *Backoff) After() <-chan time.Time {
	return time.After(b.current)
}

// Tick adjusts the interval based on whether work was found this cycle
// and returns the next interval.
func (b *Backoff) Tick(foundWork bool) time.Duration {
	if foundWork {
		b.idleCount = 0
		b.current /= 2
		if b.current < b.Min {
			b.current = b.Min
		}
	} else {
		b.idleCount++
		if b.idleCount >= b.idleLimit {
			b.idleCount = 0
			b.current *= 2
			if b.current > b.Max {
				b.current = b.Max
			}
		}
	}
	return b.current
}

// Next returns the current interval and then adjusts state for the next cycle.
// Use this for blocking loops: time.Sleep(b.Next(foundWork)).
func (b *Backoff) Next(foundWork bool) time.Duration {
	d := b.current
	b.Tick(foundWork)
	return d
}

// Reset returns the poller to its minimum interval.
func (b *Backoff) Reset() {
	b.current = b.Min
	b.idleCount = 0
}
