package mobilebridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// mobileBridgeMethodMarker is the fast-path sentinel for maybeHandleSynthetic.
// Any CDP frame whose body does NOT contain this byte sequence cannot possibly
// be a MobileBridge.* synthetic method, so we skip the json.Unmarshal entirely.
var mobileBridgeMethodMarker = []byte(`"MobileBridge.`)

// unmarshalProbeCount is incremented every time maybeHandleSynthetic actually
// decodes a frame. Tests use it to assert the fast-path is engaging.
var unmarshalProbeCount uint64

// Proxy owns one upstream CDP WebSocket connection to Chrome on the device
// plus, while Serve is running, exactly one downstream client WebSocket. It
// pumps frames bidirectionally and tears everything down on Close.
//
// Multi-client fan-out is intentionally NOT supported in this MVP. A CDP
// session is inherently stateful (target ids, enabled domains, outstanding
// request ids) so fan-out would require message multiplexing with per-client
// id remapping. Until that lands, Serve will refuse a second concurrent
// client by returning ErrBusy; callers wiring Serve into an HTTP handler
// should surface that as a 503 so clients get a clear failure instead of
// silent frame interleaving.
type Proxy struct {
	serial     string
	localPort  int
	remoteSock string

	// upstreamMu guards reads/writes of the upstream pointer itself. Held
	// for the duration of reconnect()'s swap so the reader goroutine never
	// observes a half-updated value. Read-locked by Upstream() accessors.
	upstreamMu sync.RWMutex
	upstream   *websocket.Conn

	// reconnectGate is signaled when a reconnect finishes (success or
	// failure). The upstream->downstream reader blocks on this when its
	// ReadMessage returns an error while a reconnect is in progress, so it
	// can pick up the new p.upstream without racing the swap. Nil when no
	// reconnect is pending; always recreated under upstreamMu.
	reconnectGate chan struct{}
	reconnectErr  error

	// writeMu serializes writes on the upstream connection. Gesture helpers
	// and the downstream->upstream pump both use it.
	writeMu sync.Mutex

	// serveMu guards single-client enforcement. busy is set while Serve is
	// actively pumping frames.
	serveMu sync.Mutex
	busy    bool

	// methodHandlers maps synthetic CDP method names (e.g. "MobileBridge.tap")
	// to handler functions. Handlers receive the raw "params" JSON and return
	// an arbitrary result value which the Proxy wraps into a CDP response.
	// The map is populated by registerDefaultMethodHandlers in NewProxy and
	// may also be extended via RegisterMethod for tests or callers embedding
	// the Proxy directly.
	methodMu       sync.RWMutex
	methodHandlers map[string]func(params json.RawMessage) (interface{}, error)

	// senderOverride, if non-nil, replaces p itself as the messageSender
	// used by gesture methods. Tests use this to inject a fake sender;
	// production code leaves it nil. The field is unexported so only
	// same-package tests can set it.
	senderOverride messageSender

	closeOnce sync.Once
	closed    chan struct{}

	// doneOnce + done combine into a one-shot signal that the proxy can no
	// longer recover. It is closed either by Close() or by reconnect() when
	// the backoff schedule is exhausted. Consumers select on Done() to
	// notice upstream loss without polling Upstream(). doneInitOnce lazily
	// allocates p.done for test-constructed Proxy{} values; NewProxy pre-
	// allocates the channel so production code never hits it.
	doneInitOnce sync.Once
	doneOnce     sync.Once
	done         chan struct{}
}

// ErrBusy is returned by Proxy.Serve if a second client tries to attach
// while another is already connected. See the Proxy doc comment for the
// single-client limitation.
var ErrBusy = errors.New("mobilebridge: proxy is already serving a client")

// NewProxy sets up adb forwarding to the given device's Chrome devtools
// socket, queries Chrome's /json/version endpoint to find the browser-level
// WebSocket URL, and dials it. It returns a ready-to-Serve Proxy.
func NewProxy(ctx context.Context, serial string, localPort int) (*Proxy, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sock, err := ChromeDevtoolsSocket(ctx, serial)
	if err != nil {
		return nil, fmt.Errorf("find devtools socket: %w", err)
	}
	if err := Forward(ctx, serial, localPort, sock); err != nil {
		return nil, fmt.Errorf("adb forward: %w", err)
	}

	wsURL, err := fetchBrowserWebSocketURL(fmt.Sprintf("http://127.0.0.1:%d", localPort))
	if err != nil {
		_ = Unforward(ctx, serial, localPort)
		return nil, fmt.Errorf("fetch browser ws url: %w", err)
	}

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		_ = Unforward(ctx, serial, localPort)
		return nil, fmt.Errorf("dial upstream: %w", err)
	}

	p := &Proxy{
		serial:     serial,
		localPort:  localPort,
		remoteSock: sock,
		upstream:   conn,
		closed:     make(chan struct{}),
		done:       make(chan struct{}),
	}
	p.registerDefaultMethodHandlers()
	return p, nil
}

// RegisterMethod installs a synthetic CDP method handler. If a handler with
// the same name already exists it is replaced. Handlers are called from the
// downstream read goroutine, so they should return quickly; long-running work
// should be dispatched to its own goroutine.
func (p *Proxy) RegisterMethod(name string, fn func(params json.RawMessage) (interface{}, error)) {
	p.methodMu.Lock()
	defer p.methodMu.Unlock()
	if p.methodHandlers == nil {
		p.methodHandlers = make(map[string]func(params json.RawMessage) (interface{}, error))
	}
	p.methodHandlers[name] = fn
}

// lookupMethod returns the handler for name if one is registered.
func (p *Proxy) lookupMethod(name string) (func(params json.RawMessage) (interface{}, error), bool) {
	p.methodMu.RLock()
	defer p.methodMu.RUnlock()
	fn, ok := p.methodHandlers[name]
	return fn, ok
}

// registerDefaultMethodHandlers wires the built-in MobileBridge.* gesture
// methods into methodHandlers. Called from NewProxy and also from tests that
// construct a bare Proxy{}.
func (p *Proxy) registerDefaultMethodHandlers() {
	// Synthetic method handlers use a background context because they run
	// off the downstream read goroutine and have no ambient request ctx.
	// Long-running gestures (LongPress) will still return promptly because
	// durations are caller-provided in ms.
	p.RegisterMethod("MobileBridge.tap", func(params json.RawMessage) (interface{}, error) {
		var args struct{ X, Y int }
		if len(params) > 0 {
			if err := json.Unmarshal(params, &args); err != nil {
				return nil, err
			}
		}
		if err := p.Tap(context.Background(), args.X, args.Y); err != nil {
			return nil, err
		}
		return map[string]interface{}{}, nil
	})
	p.RegisterMethod("MobileBridge.longPress", func(params json.RawMessage) (interface{}, error) {
		var args struct {
			X, Y, DurationMs int
		}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &args); err != nil {
				return nil, err
			}
		}
		if err := p.LongPress(context.Background(), args.X, args.Y, args.DurationMs); err != nil {
			return nil, err
		}
		return map[string]interface{}{}, nil
	})
	p.RegisterMethod("MobileBridge.swipe", func(params json.RawMessage) (interface{}, error) {
		var args struct {
			FromX, FromY, ToX, ToY, DurationMs int
		}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &args); err != nil {
				return nil, err
			}
		}
		if err := p.Swipe(context.Background(), args.FromX, args.FromY, args.ToX, args.ToY, args.DurationMs); err != nil {
			return nil, err
		}
		return map[string]interface{}{}, nil
	})
	p.RegisterMethod("MobileBridge.pinch", func(params json.RawMessage) (interface{}, error) {
		var args struct {
			CenterX, CenterY int
			Scale            float64
		}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &args); err != nil {
				return nil, err
			}
		}
		if err := p.Pinch(context.Background(), args.CenterX, args.CenterY, args.Scale); err != nil {
			return nil, err
		}
		return map[string]interface{}{}, nil
	})
}

// browserVersionInfo is the subset of /json/version we care about.
type browserVersionInfo struct {
	Browser              string `json:"Browser"`
	ProtocolVersion      string `json:"Protocol-Version"`
	UserAgent            string `json:"User-Agent"`
	V8Version            string `json:"V8-Version"`
	WebKitVersion        string `json:"WebKit-Version"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func fetchBrowserWebSocketURL(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = "/json/version"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(u.String())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("json/version status %d: %s", resp.StatusCode, string(body))
	}
	var v browserVersionInfo
	if err := json.Unmarshal(body, &v); err != nil {
		return "", fmt.Errorf("decode json/version: %w", err)
	}
	if v.WebSocketDebuggerURL == "" {
		return "", errors.New("json/version: no webSocketDebuggerUrl")
	}
	return v.WebSocketDebuggerURL, nil
}

// sendUpstream serializes params to JSON and writes one CDP message. This
// satisfies the messageSender interface so gesture helpers can drive it.
func (p *Proxy) sendUpstream(method string, params any) error {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = b
	}
	msg := cdpMessage{ID: nextID(), Method: method, Params: raw}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	// Hold upstreamMu.RLock() for the full WriteMessage duration so that
	// reconnect() (which takes the write side) cannot nil and Close() the
	// conn out from under an in-flight write. Without this, a concurrent
	// reconnect-caused old.Close() could race the write and corrupt the
	// gorilla/websocket send state.
	p.upstreamMu.RLock()
	defer p.upstreamMu.RUnlock()
	conn := p.upstream
	if conn == nil {
		return errors.New("mobilebridge: proxy not connected")
	}
	return conn.WriteMessage(websocket.TextMessage, b)
}

// Serve pumps frames in both directions until either side hangs up or ctx
// is canceled. Only one client may be attached at a time; a second
// concurrent call returns ErrBusy without touching the connection.
func (p *Proxy) Serve(ctx context.Context, downstream *websocket.Conn) error {
	if downstream == nil {
		return errors.New("mobilebridge: nil downstream")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.serveMu.Lock()
	if p.busy {
		p.serveMu.Unlock()
		return ErrBusy
	}
	p.busy = true
	p.serveMu.Unlock()
	defer func() {
		p.serveMu.Lock()
		p.busy = false
		p.serveMu.Unlock()
	}()
	errCh := make(chan error, 2)

	// serveDone is closed as soon as either direction errors, so the other
	// goroutine can tell "this Serve call is tearing down" and skip the
	// reconnect dance. Without this the reader could notice its upstream
	// read fail (because downstream closed and the writer bailed) and then
	// try to reconnect forever in the background, outliving the test/caller.
	serveDone := make(chan struct{})
	var serveDoneOnce sync.Once
	signalServeDone := func() { serveDoneOnce.Do(func() { close(serveDone) }) }
	// serveWG is waited on before Serve returns so the two pump goroutines
	// can't outlive the call — important for goroutine hygiene and for
	// avoiding races on package-level test overrides like reconnectBackoff.
	var serveWG sync.WaitGroup
	serveWG.Add(2)
	serveActive := func() bool {
		select {
		case <-serveDone:
			return false
		default:
			return true
		}
	}

	// downstreamWriteMu serializes writes on the downstream connection.
	// Synthetic responses and the upstream->downstream pump both use it.
	var downstreamWriteMu sync.Mutex
	writeDownstream := func(mt int, b []byte) error {
		downstreamWriteMu.Lock()
		defer downstreamWriteMu.Unlock()
		return downstream.WriteMessage(mt, b)
	}

	// Downstream -> upstream. Intercept synthetic MobileBridge.* methods
	// and translate them into real CDP calls; forward everything else.
	go func() {
		defer serveWG.Done()
		defer signalServeDone()
		for {
			mt, data, err := downstream.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			if mt != websocket.TextMessage {
				continue
			}
			if handled, resp := p.maybeHandleSynthetic(data); handled {
				if resp != nil {
					if werr := writeDownstream(websocket.TextMessage, resp); werr != nil {
						errCh <- werr
						return
					}
				}
				continue
			}
			if werr := p.writeUpstream(data); werr != nil {
				// Upstream died mid-write — most commonly because the
				// adb forward dropped. Try to re-establish (or wait for
				// the reader to finish its own in-flight reconnect); if
				// that succeeds, replay this frame on the new conn.
				if !serveActive() {
					errCh <- werr
					return
				}
				if rerr := p.ensureReconnect(); rerr != nil {
					errCh <- werr
					return
				}
				if werr := p.writeUpstream(data); werr != nil {
					errCh <- werr
					return
				}
			}
		}
	}()

	// Upstream -> downstream. When the upstream read fails we check for an
	// in-flight reconnect; if one is in progress we wait for it and pick up
	// the new connection instead of tearing the whole Serve loop down. If
	// Serve itself is already tearing down (writer bailed, ctx cancelled)
	// we exit instead of kicking off a reconnect we can't deliver through.
	go func() {
		defer serveWG.Done()
		defer signalServeDone()
		for {
			p.upstreamMu.RLock()
			conn := p.upstream
			gate := p.reconnectGate
			p.upstreamMu.RUnlock()
			if conn == nil {
				if gate != nil {
					select {
					case <-gate:
					case <-serveDone:
						errCh <- errors.New("mobilebridge: serve exiting")
						return
					}
					continue
				}
				errCh <- errors.New("mobilebridge: upstream nil and no reconnect in progress")
				return
			}
			mt, data, err := conn.ReadMessage()
			if err != nil {
				if !serveActive() {
					errCh <- err
					return
				}
				// Either the writer already initiated a reconnect and
				// closed the conn under us (reconnectGate is set), or we
				// are the first side to notice upstream loss and need to
				// kick off the reconnect ourselves. ensureReconnect() does
				// the right thing in both cases: it waits on an in-flight
				// attempt or starts a new one, returning the final error.
				if rerr := p.ensureReconnect(); rerr != nil {
					errCh <- err
					return
				}
				continue
			}
			if err := writeDownstream(mt, data); err != nil {
				errCh <- err
				return
			}
		}
	}()

	var exitErr error
	select {
	case err := <-errCh:
		exitErr = err
	case <-p.closed:
		// exitErr stays nil
	case <-ctx.Done():
		exitErr = ctx.Err()
	}
	// Tell both pump goroutines to bail. Close the upstream so any in-flight
	// ReadMessage in the reader returns immediately; close the downstream so
	// the writer's ReadMessage returns too. We close upstream via reconnect()
	// style only when the conn is still the same — but since tests hold real
	// connections we just unblock the pumps by calling Close on whatever we
	// hold. The defer above closes serveDone so serveActive() returns false.
	signalServeDone()
	p.upstreamMu.RLock()
	up := p.upstream
	p.upstreamMu.RUnlock()
	if up != nil {
		_ = up.SetReadDeadline(time.Now())
	}
	_ = downstream.SetReadDeadline(time.Now())
	// Wait for both pumps to actually exit before returning so Serve's
	// lifetime bounds the goroutines' lifetime. errCh is buffered(2) so
	// sends never block the exit path.
	serveWG.Wait()
	return exitErr
}

// cdpResponse is the JSON wire format for a CDP method reply. Either Result
// or Error is set, matching the shape real Chrome uses.
type cdpResponse struct {
	ID     int64           `json:"id"`
	Result interface{}     `json:"result,omitempty"`
	Error  *cdpErrorObject `json:"error,omitempty"`
}

type cdpErrorObject struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// maybeHandleSynthetic peeks at an incoming CDP message. If the method name
// is registered in methodHandlers the handler is invoked and a CDP response
// is encoded for the caller to write back to the downstream client. Returns
// handled=true whenever the message was consumed; the upstream is never told
// about synthetic methods.
func (p *Proxy) maybeHandleSynthetic(raw []byte) (bool, []byte) {
	// Fast path: 99.9% of CDP traffic is real Chrome methods that cannot
	// possibly match MobileBridge.*. Skip the unmarshal for those frames.
	if !bytes.Contains(raw, mobileBridgeMethodMarker) {
		return false, nil
	}
	atomic.AddUint64(&unmarshalProbeCount, 1)
	var probe struct {
		ID     int64           `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false, nil
	}
	if probe.Method == "" {
		return false, nil
	}
	handler, ok := p.lookupMethod(probe.Method)
	if !ok {
		// Not a registered synthetic method — but the fast path already
		// confirmed it's in the MobileBridge.* namespace, so return a
		// proper CDP "method not found" error instead of forwarding to
		// upstream Chrome which doesn't know about MobileBridge.* either.
		if strings.HasPrefix(probe.Method, "MobileBridge.") {
			resp := cdpResponse{
				ID: probe.ID,
				Error: &cdpErrorObject{
					Code:    -32601,
					Message: fmt.Sprintf("mobilebridge: unknown synthetic method %q", probe.Method),
				},
			}
			b, _ := json.Marshal(resp)
			return true, b
		}
		return false, nil
	}

	result, err := handler(probe.Params)
	if err != nil {
		resp := cdpResponse{
			ID: probe.ID,
			Error: &cdpErrorObject{
				Code:    -32000,
				Message: err.Error(),
			},
		}
		b, _ := json.Marshal(resp)
		return true, b
	}
	if result == nil {
		result = map[string]interface{}{}
	}
	resp := cdpResponse{ID: probe.ID, Result: result}
	b, _ := json.Marshal(resp)
	return true, b
}

// ensureDoneChan lazily allocates p.done via a dedicated once, so Done()
// and signalDone() never touch upstreamMu. NewProxy pre-allocates p.done
// so production code never hits the once, but hand-built Proxy{} values in
// tests still work without having to set p.done explicitly.
func (p *Proxy) ensureDoneChan() {
	p.doneInitOnce.Do(func() {
		if p.done == nil {
			p.done = make(chan struct{})
		}
	})
}

// Done returns a channel that is closed when the proxy can no longer serve
// traffic — either because Close was called or because reconnect() gave up
// after exhausting the backoff schedule. CLI callers select on this to
// notice upstream loss without polling Upstream().
func (p *Proxy) Done() <-chan struct{} {
	p.ensureDoneChan()
	return p.done
}

// signalDone closes the Done() channel at most once. Uses its own once so
// it does not contend with in-flight upstream writes on upstreamMu.
func (p *Proxy) signalDone() {
	p.ensureDoneChan()
	p.doneOnce.Do(func() { close(p.done) })
}

// Close tears down the upstream WebSocket and removes the adb forward.
func (p *Proxy) Close() error {
	var err error
	p.closeOnce.Do(func() {
		close(p.closed)
		p.signalDone()
		p.upstreamMu.Lock()
		conn := p.upstream
		p.upstream = nil
		p.upstreamMu.Unlock()
		if conn != nil {
			err = conn.Close()
		}
		if p.serial != "" && p.localPort != 0 {
			if uerr := Unforward(context.Background(), p.serial, p.localPort); uerr != nil && err == nil {
				err = uerr
			}
		}
	})
	return err
}

// reconnectSwapHook is called, if non-nil, right after reconnect() clears
// p.upstream and sets p.reconnectGate, before the backoff loop starts.
// Tests use it to widen the swap window and verify the reader pump does
// not tear down during the gap. Production code leaves it nil.
var reconnectSwapHook func()

// reconnectBackoff is the escalating delay sequence for reconnect attempts.
// Overridable from tests.
var reconnectBackoff = []time.Duration{
	100 * time.Millisecond,
	300 * time.Millisecond,
	1 * time.Second,
	3 * time.Second,
}

// writeUpstream serializes a raw text frame write to the upstream WebSocket
// under writeMu. upstreamMu.RLock() is held for the duration of the
// WriteMessage call so reconnect() (which takes the write lock) cannot
// Close() the conn out from under a concurrent writer.
func (p *Proxy) writeUpstream(data []byte) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	p.upstreamMu.RLock()
	defer p.upstreamMu.RUnlock()
	conn := p.upstream
	if conn == nil {
		return errors.New("mobilebridge: upstream nil")
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

// ensureReconnect starts a reconnect if one isn't already running, otherwise
// waits on the existing attempt. Returns the final error from whichever
// goroutine actually executed the reconnect. This is how the reader and
// writer goroutines in Serve() coordinate: whichever side notices upstream
// loss first kicks off the swap, and the other picks up the new conn via the
// reconnectGate. Without this, a reader-first failure would return from
// Serve (tearing down the loop) instead of recovering like a writer-first
// failure does.
func (p *Proxy) ensureReconnect() error {
	p.upstreamMu.Lock()
	if p.reconnectGate != nil {
		// Someone is already reconnecting; wait for them.
		gate := p.reconnectGate
		p.upstreamMu.Unlock()
		<-gate
		p.upstreamMu.RLock()
		err := p.reconnectErr
		p.upstreamMu.RUnlock()
		return err
	}
	p.upstreamMu.Unlock()
	return p.reconnect()
}

// reconnect tears down the current upstream connection and tries to
// re-establish the adb forward + Chrome WebSocket with an escalating
// backoff. Used by Serve() when a read or write on upstream fails — the
// most common cause is the adb forward dropping (cable jiggle, adb server
// restart, device sleep). Returns the final error if all attempts fail.
//
// Callers should prefer ensureReconnect() which deduplicates concurrent
// reader/writer invocations; reconnect() itself is ALSO safe to call from
// multiple goroutines — if another reconnect is already in flight, this
// call waits for it and returns its result instead of starting a second
// cycle. Without that, two racing direct callers could both exhaust the
// backoff and one could call signalDone() while the other installed a
// healthy upstream, leaving Done() permanently closed on a live proxy.
//
// While reconnect runs, p.reconnectGate is non-nil and the peer goroutine
// in Serve() (reader or writer) will block on it after observing its own
// I/O error, so the peer resumes on the new connection once the swap
// succeeds.
func (p *Proxy) reconnect() error {
	testOverrideMu.RLock()
	backoff := append([]time.Duration(nil), reconnectBackoff...)
	hook := reconnectSwapHook
	testOverrideMu.RUnlock()

	// Serialize: if another reconnect is already running, piggy-back on it
	// instead of starting a second cycle in parallel.
	gate := make(chan struct{})
	p.upstreamMu.Lock()
	if existing := p.reconnectGate; existing != nil {
		p.upstreamMu.Unlock()
		<-existing
		p.upstreamMu.RLock()
		err := p.reconnectErr
		p.upstreamMu.RUnlock()
		return err
	}
	// Open the gate so the reader knows to wait for us instead of tearing
	// the whole Serve loop down. Close the old conn to unblock any
	// in-flight ReadMessage.
	p.reconnectGate = gate
	p.reconnectErr = nil
	old := p.upstream
	p.upstream = nil
	p.upstreamMu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	if hook != nil {
		hook()
	}

	var lastErr error
	for _, delay := range backoff {
		time.Sleep(delay)
		// Best-effort: re-run adb forward in case the forward was torn
		// down underneath us.
		if err := Forward(context.Background(), p.serial, p.localPort, p.remoteSock); err != nil {
			lastErr = fmt.Errorf("adb forward: %w", err)
			continue
		}
		wsURL, err := fetchBrowserWebSocketURL(fmt.Sprintf("http://127.0.0.1:%d", p.localPort))
		if err != nil {
			lastErr = fmt.Errorf("fetch browser ws url: %w", err)
			continue
		}
		dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
		conn, _, err := dialer.Dial(wsURL, nil)
		if err != nil {
			lastErr = fmt.Errorf("dial upstream: %w", err)
			continue
		}
		p.upstreamMu.Lock()
		p.upstream = conn
		p.reconnectGate = nil
		p.upstreamMu.Unlock()
		close(gate)
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("mobilebridge: reconnect gave up")
	}
	p.upstreamMu.Lock()
	p.reconnectGate = nil
	p.reconnectErr = lastErr
	p.upstreamMu.Unlock()
	close(gate)
	// Signal permanent loss so consumers selecting on Done() can react.
	p.signalDone()
	return lastErr
}

// Upstream returns the raw upstream websocket connection without locking.
// It is intended for read-only inspection (e.g. telemetry, tests); callers
// MUST NOT Read or Write on the returned conn — doing so races the internal
// Serve goroutines and reconnect swap. To send a CDP frame, use a gesture
// helper (Tap/Swipe/...) or forward the frame through Serve.
func (p *Proxy) Upstream() *websocket.Conn {
	p.upstreamMu.RLock()
	defer p.upstreamMu.RUnlock()
	return p.upstream
}

// Busy reports whether a client is currently attached via Serve. HTTP
// handlers should check Busy before upgrading so a second concurrent
// client sees a clean 503 instead of a silently-rejected Serve call.
func (p *Proxy) Busy() bool {
	p.serveMu.Lock()
	defer p.serveMu.Unlock()
	return p.busy
}
