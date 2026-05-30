package mobilebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync/atomic"
	"time"
)

// TouchPoint mirrors the CDP Input.TouchPoint payload used inside
// Input.dispatchTouchEvent. Only X and Y are required; the other fields
// match Chrome's wire format for advanced pressure/radius/id metadata and
// are safe to leave zero for normal taps and swipes.
type TouchPoint struct {
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	RadiusX     float64 `json:"radiusX,omitempty"`
	RadiusY     float64 `json:"radiusY,omitempty"`
	Force       float64 `json:"force,omitempty"`
	ID          float64 `json:"id,omitempty"`
	TangentialP float64 `json:"tangentialPressure,omitempty"`
}

// TouchEventParams is the full payload for a single Input.dispatchTouchEvent
// call. Callers typically do not build this directly — the Tap, LongPress,
// Swipe, and Pinch helpers on *Proxy assemble the correct start/move/end
// sequence — but it is exported so external code can compose custom gesture
// sequences when needed.
type TouchEventParams struct {
	Type        string       `json:"type"` // touchStart | touchMove | touchEnd | touchCancel
	TouchPoints []TouchPoint `json:"touchPoints"`
}

// cdpMessage is the JSON wire format sent over the CDP websocket.
type cdpMessage struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// messageSender is the subset of Proxy that gesture helpers need. Tests can
// substitute a fake implementation without needing a real websocket.
type messageSender interface {
	sendUpstream(method string, params any) error
}

// idSource provides CDP request ids. Shared across gestures on the same Proxy.
var globalID int64

func nextID() int64 { return atomic.AddInt64(&globalID, 1) }

// buildTouchEvent constructs a JSON-ready Input.dispatchTouchEvent payload.
func buildTouchEvent(kind string, points []TouchPoint) TouchEventParams {
	if points == nil {
		points = []TouchPoint{}
	}
	return TouchEventParams{Type: kind, TouchPoints: points}
}

// maxCoord is the upper bound we allow for touch coordinates. Real mobile
// viewports top out well below this; anything larger is almost certainly a
// caller bug (negative ints coerced to huge values, off-by-one, etc).
const maxCoord = 100000

func validCoord(x, y int) error {
	if x < 0 || y < 0 || x > maxCoord || y > maxCoord {
		return fmt.Errorf("mobilebridge: coord out of range: (%d, %d)", x, y)
	}
	return nil
}

// sender returns the messageSender backing this Proxy. Defaults to the Proxy
// itself; tests override via direct assignment to p.senderOverride (allowed
// because messageSender is unexported).
func (p *Proxy) sender() messageSender {
	if p.senderOverride != nil {
		return p.senderOverride
	}
	return p
}

// Tap sends a single-finger touchStart then touchEnd at (x, y).
func (p *Proxy) Tap(ctx context.Context, x, y int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validCoord(x, y); err != nil {
		return err
	}
	s := p.sender()
	points := []TouchPoint{{X: float64(x), Y: float64(y), ID: 0, Force: 1}}
	if err := s.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchStart", points)); err != nil {
		return err
	}
	return s.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchEnd", nil))
}

// LongPress sends a touchStart, waits durationMs, then sends a touchEnd.
// The wait honors ctx cancellation.
func (p *Proxy) LongPress(ctx context.Context, x, y, durationMs int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validCoord(x, y); err != nil {
		return err
	}
	if durationMs <= 0 {
		return fmt.Errorf("mobilebridge: long press duration must be > 0, got %d", durationMs)
	}
	s := p.sender()
	points := []TouchPoint{{X: float64(x), Y: float64(y), ID: 0, Force: 1}}
	if err := s.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchStart", points)); err != nil {
		return err
	}
	select {
	case <-time.After(time.Duration(durationMs) * time.Millisecond):
	case <-ctx.Done():
		// Best-effort touchEnd so we don't leave a dangling touchStart.
		_ = s.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchEnd", nil))
		return ctx.Err()
	}
	return s.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchEnd", nil))
}

// Swipe performs a single-finger drag from (fromX, fromY) to (toX, toY) over
// durationMs milliseconds, interpolating with a fixed number of move events.
func (p *Proxy) Swipe(ctx context.Context, fromX, fromY, toX, toY, durationMs int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	const steps = 10
	if err := validCoord(fromX, fromY); err != nil {
		return err
	}
	if err := validCoord(toX, toY); err != nil {
		return err
	}
	if durationMs < 0 {
		durationMs = 0
	}
	s := p.sender()
	start := []TouchPoint{{X: float64(fromX), Y: float64(fromY), ID: 0, Force: 1}}
	if err := s.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchStart", start)); err != nil {
		return err
	}
	stepSleep := time.Duration(durationMs) * time.Millisecond / time.Duration(steps+1)
	for i := 1; i <= steps; i++ {
		frac := float64(i) / float64(steps+1)
		x := float64(fromX) + (float64(toX-fromX) * frac)
		y := float64(fromY) + (float64(toY-fromY) * frac)
		move := []TouchPoint{{X: x, Y: y, ID: 0, Force: 1}}
		if err := s.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchMove", move)); err != nil {
			return err
		}
		if stepSleep > 0 {
			select {
			case <-time.After(stepSleep):
			case <-ctx.Done():
				_ = s.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchEnd", nil))
				return ctx.Err()
			}
		}
	}
	end := []TouchPoint{{X: float64(toX), Y: float64(toY), ID: 0, Force: 1}}
	if err := s.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchMove", end)); err != nil {
		return err
	}
	return s.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchEnd", nil))
}

// Pinch performs a two-finger pinch centered at (centerX, centerY). A scale
// greater than 1 zooms in (fingers move apart); less than 1 zooms out.
func (p *Proxy) Pinch(ctx context.Context, centerX, centerY int, scale float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if scale <= 0 {
		return fmt.Errorf("mobilebridge: pinch scale must be > 0, got %v", scale)
	}
	const steps = 10
	const baseOffset = 200.0 // starting half-distance between fingers in px

	// Starting finger positions (horizontal axis) and end positions.
	startOffset := baseOffset
	endOffset := baseOffset * scale

	s := p.sender()
	startA := TouchPoint{X: float64(centerX) - startOffset, Y: float64(centerY), ID: 0, Force: 1}
	startB := TouchPoint{X: float64(centerX) + startOffset, Y: float64(centerY), ID: 1, Force: 1}

	if err := s.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchStart", []TouchPoint{startA, startB})); err != nil {
		return err
	}

	for i := 1; i <= steps; i++ {
		if err := ctx.Err(); err != nil {
			_ = s.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchEnd", nil))
			return err
		}
		frac := float64(i) / float64(steps)
		off := startOffset + (endOffset-startOffset)*frac
		a := TouchPoint{X: float64(centerX) - off, Y: float64(centerY), ID: 0, Force: 1}
		b := TouchPoint{X: float64(centerX) + off, Y: float64(centerY), ID: 1, Force: 1}
		if err := s.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchMove", []TouchPoint{a, b})); err != nil {
			return err
		}
	}
	return s.sendUpstream("Input.dispatchTouchEvent", buildTouchEvent("touchEnd", nil))
}

// round is an internal helper for tests.
func round(f float64) float64 { return math.Round(f*1000) / 1000 }
