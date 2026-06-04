package foxbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"vulpineos/internal/juggler"

	"github.com/VulpineOS/foxbridge/pkg/backend"
	"github.com/VulpineOS/foxbridge/pkg/bridge"
	"github.com/VulpineOS/foxbridge/pkg/cdp"
)

// jugglerAdapter wraps a VulpineOS juggler.Client as a foxbridge backend.Backend.
// The key difference is the Call signature: VulpineOS takes interface{}, foxbridge takes json.RawMessage.
// Since json.RawMessage implements json.Marshaler, passing it as interface{} to VulpineOS's
// client (which calls json.Marshal) produces identical bytes — no double-encoding.
type jugglerAdapter struct {
	client *juggler.Client

	mu               sync.Mutex
	attachedSessions map[string]string
	attachedTargets  map[string]string
	cancelSubs       []func()
}

// Verify jugglerAdapter implements backend.Backend at compile time.
var _ backend.Backend = (*jugglerAdapter)(nil)

func (a *jugglerAdapter) Call(sessionID, method string, params json.RawMessage) (json.RawMessage, error) {
	// Pass json.RawMessage directly as interface{} — VulpineOS's client marshals it correctly.
	return a.client.Call(sessionID, method, params)
}

func (a *jugglerAdapter) Subscribe(event string, handler backend.EventHandler) {
	var cancel func()
	switch event {
	case "Browser.attachedToTarget":
		cancel = a.client.SubscribeWithCancel(event, func(sessionID string, params json.RawMessage) {
			if !a.recordAttachedTarget(params) {
				return
			}
			handler(sessionID, params)
		})
	case "Browser.detachedFromTarget":
		cancel = a.client.SubscribeWithCancel(event, func(sessionID string, params json.RawMessage) {
			a.recordDetachedTarget(params)
			handler(sessionID, params)
		})
	default:
		// Both sides use func(sessionID string, params json.RawMessage) — direct passthrough.
		cancel = a.client.SubscribeWithCancel(event, juggler.EventHandler(handler))
	}
	a.mu.Lock()
	a.cancelSubs = append(a.cancelSubs, cancel)
	a.mu.Unlock()
}

func (a *jugglerAdapter) Close() error {
	// Don't close the underlying client — it belongs to the kernel.
	a.mu.Lock()
	cancels := append([]func(){}, a.cancelSubs...)
	a.cancelSubs = nil
	a.attachedSessions = nil
	a.attachedTargets = nil
	a.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return nil
}

func (a *jugglerAdapter) recordAttachedTarget(params json.RawMessage) bool {
	var ev struct {
		SessionID  string `json:"sessionId"`
		TargetInfo struct {
			TargetID string `json:"targetId"`
		} `json:"targetInfo"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return true
	}
	if ev.SessionID == "" && ev.TargetInfo.TargetID == "" {
		return true
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.ensureAttachMapsLocked()

	if ev.SessionID != "" {
		if _, ok := a.attachedSessions[ev.SessionID]; ok {
			return false
		}
	}
	if ev.TargetInfo.TargetID != "" {
		if _, ok := a.attachedTargets[ev.TargetInfo.TargetID]; ok {
			return false
		}
	}

	if ev.SessionID != "" {
		a.attachedSessions[ev.SessionID] = ev.TargetInfo.TargetID
	}
	if ev.TargetInfo.TargetID != "" {
		a.attachedTargets[ev.TargetInfo.TargetID] = ev.SessionID
	}
	return true
}

func (a *jugglerAdapter) recordDetachedTarget(params json.RawMessage) {
	var ev struct {
		SessionID string `json:"sessionId"`
		TargetID  string `json:"targetId"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.ensureAttachMapsLocked()

	if ev.SessionID != "" {
		if targetID := a.attachedSessions[ev.SessionID]; targetID != "" {
			delete(a.attachedTargets, targetID)
		}
		delete(a.attachedSessions, ev.SessionID)
	}
	if ev.TargetID != "" {
		if sessionID := a.attachedTargets[ev.TargetID]; sessionID != "" {
			delete(a.attachedSessions, sessionID)
		}
		delete(a.attachedTargets, ev.TargetID)
	}
}

func (a *jugglerAdapter) ensureAttachMapsLocked() {
	if a.attachedSessions == nil {
		a.attachedSessions = make(map[string]string)
	}
	if a.attachedTargets == nil {
		a.attachedTargets = make(map[string]string)
	}
}

// EmbeddedServer runs foxbridge's CDP server in-process, wrapping the kernel's Juggler client.
type EmbeddedServer struct {
	server   *cdp.Server
	sessions *cdp.SessionManager
	bridge   *bridge.Bridge
	backend  backend.Backend
	port     int
	done     chan struct{}
}

// StartEmbedded creates and starts an embedded foxbridge CDP server using the kernel's Juggler client.
// The server runs in a background goroutine. Call Stop() to shut it down.
func StartEmbedded(client *juggler.Client, port int) (*EmbeddedServer, error) {
	if client == nil {
		return nil, fmt.Errorf("juggler client is nil")
	}
	if port == 0 {
		port = 9222
	}

	// Wrap VulpineOS's juggler client as a foxbridge backend.
	be := &jugglerAdapter{client: client}

	return startEmbeddedWithBackend(be, port, true)
}

// StartEmbeddedScoped creates an embedded foxbridge server limited to a single browser context.
// The server only exposes targets and events for browserContextId and forces new pages into it.
func StartEmbeddedScoped(client *juggler.Client, port int, browserContextID string) (*EmbeddedServer, error) {
	if client == nil {
		return nil, fmt.Errorf("juggler client is nil")
	}
	if browserContextID == "" {
		return nil, fmt.Errorf("browser context id is required")
	}
	if port == 0 {
		var err error
		port, err = reservePort()
		if err != nil {
			return nil, err
		}
	}

	be := newScopedBackend(client, browserContextID)
	return startEmbeddedWithBackend(be, port, false)
}

func startEmbeddedWithBackend(be backend.Backend, port int, attachToDefaultContext bool) (*EmbeddedServer, error) {
	if err := ensurePortAvailable(port); err != nil {
		_ = be.Close()
		return nil, fmt.Errorf("embedded foxbridge port unavailable: %w", err)
	}

	// Create CDP session manager and server (same wiring as foxbridge main.go).
	sessions := cdp.NewSessionManager()

	var b *bridge.Bridge
	server := cdp.NewServer(port, func(conn *cdp.Connection, msg *cdp.Message) {
		b.HandleMessage(conn, msg)
	}, sessions)

	b = bridge.New(be, sessions, server)
	b.SetupEventSubscriptions()

	// Browser.enable is already called by the kernel — the adapter shares the same
	// underlying Juggler connection, so existing subscriptions and targets are visible.
	// However, foxbridge needs its own Browser.enable to receive attachedToTarget events
	// through the bridge's event subscriptions.
	enableParams, _ := json.Marshal(map[string]interface{}{
		"attachToDefaultContext": attachToDefaultContext,
	})
	if _, err := be.Call("", "Browser.enable", enableParams); err != nil {
		_ = be.Close()
		return nil, fmt.Errorf("Browser.enable via embedded foxbridge: %w", err)
	}

	es := &EmbeddedServer{
		server:   server,
		sessions: sessions,
		bridge:   b,
		backend:  be,
		port:     port,
		done:     make(chan struct{}),
	}

	// Start CDP server in background.
	go func() {
		defer close(es.done)
		if err := server.Start(); err != nil {
			log.Printf("embedded foxbridge CDP server error: %v", err)
		}
	}()

	if err := waitForPort(port, 2*time.Second); err != nil {
		es.Stop()
		return nil, err
	}
	log.Printf("embedded foxbridge CDP server listening on 127.0.0.1:%d", port)
	return es, nil
}

func reservePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("reserve foxbridge port: %w", err)
	}
	defer listener.Close()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("reserve foxbridge port: unexpected address type %T", listener.Addr())
	}
	return addr.Port, nil
}

// CDPURL returns the CDP WebSocket URL for connecting clients.
func (es *EmbeddedServer) CDPURL() string {
	return fmt.Sprintf("ws://127.0.0.1:%d", es.port)
}

// Port returns the CDP server port.
func (es *EmbeddedServer) Port() int {
	return es.port
}

// Stop shuts down the embedded CDP server.
// It does NOT close the underlying Juggler client (owned by the kernel).
func (es *EmbeddedServer) Stop() {
	if es == nil || es.server == nil {
		return
	}
	if es.backend != nil {
		_ = es.backend.Close()
		es.backend = nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	if err := es.server.Shutdown(ctx); err != nil {
		log.Printf("embedded foxbridge graceful shutdown failed: %v", err)
		_ = es.server.Close()
	}
	cancel()
	select {
	case <-es.done:
	case <-time.After(2 * time.Second):
		log.Printf("embedded foxbridge shutdown timed out")
		_ = es.server.Close()
		select {
		case <-es.done:
		case <-time.After(500 * time.Millisecond):
			log.Printf("embedded foxbridge forced shutdown timed out")
		}
	}
	log.Println("embedded foxbridge stopped")
}
