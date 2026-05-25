package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"vulpineos/internal/juggler"
	"vulpineos/internal/nanoclaw"
)

const maxWebSocketMessageBytes int64 = 2 << 20
const maxBroadcastQueue = 256

// Server exposes the VulpineOS kernel over WebSocket for remote TUI clients.
type Server struct {
	auth            *Authenticator
	client          *juggler.Client
	addr            string
	server          *http.Server
	mux             *http.ServeMux
	clients         map[*wsClient]struct{}
	clientsMu       sync.RWMutex
	controlAPI      *ControlAPI
	pairingMu       sync.RWMutex
	pairingCode     string
	pairingExchange func(label string) (string, error)

	writeTimeout time.Duration
}

type wsClient struct {
	conn           *websocket.Conn
	ctx            context.Context
	writeMu        sync.Mutex
	events         chan []byte
	enqueueMu      sync.Mutex
	closed         bool
	remoteContexts map[string]struct{}
	contextMu      sync.Mutex
	once           sync.Once
}

func (c *wsClient) enqueueEvent(data []byte) bool {
	c.enqueueMu.Lock()
	defer c.enqueueMu.Unlock()
	if c.closed || c.events == nil {
		return false
	}
	select {
	case c.events <- data:
		return true
	default:
		return false
	}
}

func (c *wsClient) write(ctx context.Context, typ websocket.MessageType, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("websocket connection unavailable")
	}
	return c.conn.Write(ctx, typ, data)
}

func (c *wsClient) close(status websocket.StatusCode, reason string) error {
	c.once.Do(func() {
		c.enqueueMu.Lock()
		defer c.enqueueMu.Unlock()
		c.closed = true
		if c.events != nil {
			close(c.events)
		}
	})
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return nil
	}
	return c.conn.Close(status, reason)
}

func (c *wsClient) trackRemoteContext(contextID string) {
	if contextID == "" {
		return
	}
	c.contextMu.Lock()
	defer c.contextMu.Unlock()
	if c.remoteContexts == nil {
		c.remoteContexts = make(map[string]struct{})
	}
	c.remoteContexts[contextID] = struct{}{}
}

func (c *wsClient) untrackRemoteContext(contextID string) {
	if contextID == "" {
		return
	}
	c.contextMu.Lock()
	defer c.contextMu.Unlock()
	delete(c.remoteContexts, contextID)
}

func (c *wsClient) drainRemoteContexts() []string {
	c.contextMu.Lock()
	defer c.contextMu.Unlock()
	contextIDs := make([]string, 0, len(c.remoteContexts))
	for contextID := range c.remoteContexts {
		contextIDs = append(contextIDs, contextID)
	}
	c.remoteContexts = make(map[string]struct{})
	return contextIDs
}

// NewServer creates a remote access server.
func NewServer(addr string, apiKey string, jugglerClient *juggler.Client) *Server {
	s := &Server{
		auth:    NewAuthenticator(apiKey),
		client:  jugglerClient,
		addr:    addr,
		clients: make(map[*wsClient]struct{}),

		writeTimeout: 2 * time.Second,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		setJSONSecurityHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/auth/check", func(w http.ResponseWriter, r *http.Request) {
		setJSONSecurityHeaders(w)
		if !s.auth.Validate(r) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/pair", s.handlePair)

	s.mux = mux
	s.server = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return s
}

func setJSONSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (s *Server) SetTokenValidator(fn func(string) bool) {
	s.auth.SetTokenValidator(fn)
}

func (s *Server) SetPairing(code string, exchange func(label string) (string, error)) {
	s.pairingMu.Lock()
	defer s.pairingMu.Unlock()
	s.pairingCode = strings.TrimSpace(code)
	s.pairingExchange = exchange
}

// SetControlAPI attaches the TUI/server control surface for remote clients.
func (s *Server) SetControlAPI(api *ControlAPI) {
	s.controlAPI = api
}

// Mux returns the HTTP mux for registering additional handlers.
func (s *Server) Mux() *http.ServeMux {
	return s.mux
}

// Start begins listening for WebSocket connections.
func (s *Server) Start() error {
	log.Printf("VulpineOS remote server listening on %s", s.addr)
	return s.server.ListenAndServe()
}

// StartTLS begins listening with TLS.
func (s *Server) StartTLS(certFile, keyFile string) error {
	log.Printf("VulpineOS remote server listening on %s (TLS)", s.addr)
	return s.server.ListenAndServeTLS(certFile, keyFile)
}

// Stop gracefully shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	clients := s.snapshotClients()
	for _, c := range clients {
		_ = c.close(websocket.StatusGoingAway, "server shutting down")
	}
	s.removeClients(clients)
	return s.server.Shutdown(ctx)
}

// BroadcastEvent sends a Juggler event to all connected clients.
// Clients that fail to receive the message are removed.
func (s *Server) BroadcastEvent(method, sessionID string, params json.RawMessage) {
	msg, _ := json.Marshal(&juggler.Message{
		Method:    method,
		Params:    params,
		SessionID: sessionID,
	})
	env, err := NewJugglerEnvelope(msg)
	if err != nil {
		return
	}

	s.enqueueBroadcast(env)
}

func (s *Server) BroadcastAgentStatus(status nanoclaw.AgentStatus) {
	params, err := json.Marshal(map[string]any{
		"agentId":   status.AgentID,
		"contextId": status.ContextID,
		"status":    status.Status,
		"objective": status.Objective,
		"tokens":    status.Tokens,
	})
	if err != nil {
		return
	}
	s.BroadcastEvent("Vulpine.agentStatus", "", params)
}

func (s *Server) BroadcastConversation(msg nanoclaw.ConversationMsg) {
	params, err := json.Marshal(map[string]any{
		"agentId": msg.AgentID,
		"role":    msg.Role,
		"content": msg.Content,
		"tokens":  msg.Tokens,
	})
	if err != nil {
		return
	}
	s.BroadcastEvent("Vulpine.conversation", "", params)
}

func (s *Server) enqueueBroadcast(env []byte) {
	dead := make([]*wsClient, 0)
	for _, c := range s.snapshotClients() {
		if !c.enqueueEvent(env) {
			dead = append(dead, c)
		}
	}
	s.removeClients(dead)
}

func (s *Server) snapshotClients() []*wsClient {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	clients := make([]*wsClient, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	return clients
}

func (s *Server) removeClients(dead []*wsClient) {
	if len(dead) > 0 {
		s.clientsMu.Lock()
		for _, c := range dead {
			delete(s.clients, c)
		}
		s.clientsMu.Unlock()
		for _, c := range dead {
			go func(client *wsClient) {
				_ = client.close(websocket.StatusGoingAway, "write error")
			}(c)
		}
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	setJSONSecurityHeaders(w)
	acceptedProtocol := s.auth.AcceptedWebSocketSubprotocol(r)
	// Authenticate
	if !s.auth.Validate(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	options := &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Allow cross-origin for TUI clients
	}
	if acceptedProtocol != "" {
		options.Subprotocols = []string{acceptedProtocol}
	}

	conn, err := websocket.Accept(w, r, options)
	if err != nil {
		log.Printf("websocket accept error: %v", err)
		return
	}
	conn.SetReadLimit(maxWebSocketMessageBytes)

	ctx := r.Context()
	wsc := &wsClient{conn: conn, ctx: ctx, events: make(chan []byte, maxBroadcastQueue)}

	s.clientsMu.Lock()
	s.clients[wsc] = struct{}{}
	s.clientsMu.Unlock()

	defer func() {
		s.cleanupRemoteContexts(wsc)
		s.clientsMu.Lock()
		delete(s.clients, wsc)
		s.clientsMu.Unlock()
		_ = wsc.close(websocket.StatusNormalClosure, "")
	}()

	log.Printf("Remote client connected from %s", r.RemoteAddr)
	go s.writeBroadcasts(wsc)

	// Read messages from client and forward to Juggler
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			log.Printf("Remote client disconnected: %v", err)
			return
		}

		var env Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}

		switch env.Type {
		case "juggler":
			// Forward to Firefox via Juggler client
			var msg juggler.Message
			if err := json.Unmarshal(env.Payload, &msg); err != nil {
				continue
			}
			var result json.RawMessage
			var err error
			if s.client == nil {
				err = fmt.Errorf("browser unavailable: server started without a kernel")
			} else {
				result, err = s.client.Call(msg.SessionID, msg.Method, msg.Params)
				s.trackRemoteJugglerCall(wsc, &msg, result, err)
			}
			// Send response back
			resp := &juggler.Message{ID: msg.ID, SessionID: msg.SessionID}
			if err != nil {
				resp.Error = &juggler.Error{Message: err.Error()}
			} else {
				resp.Result = result
			}
			respData, _ := json.Marshal(resp)
			respEnv, _ := NewJugglerEnvelope(respData)
			if err := s.writeClient(wsc, respEnv); err != nil {
				log.Printf("remote response write error: %v", err)
				return
			}

		case "control":
			// Handle control commands (restart, spawn, etc.)
			s.handleControl(wsc, env.Payload)
		}
	}
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	setJSONSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var p struct {
		Code  string `json:"code"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	s.pairingMu.RLock()
	code := s.pairingCode
	exchange := s.pairingExchange
	s.pairingMu.RUnlock()
	if code == "" || exchange == nil || strings.TrimSpace(p.Code) != code {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	token, err := exchange(p.Label)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"apiKey": token})
}

func (s *Server) trackRemoteJugglerCall(wsc *wsClient, msg *juggler.Message, result json.RawMessage, callErr error) {
	if wsc == nil || msg == nil || callErr != nil {
		return
	}
	switch msg.Method {
	case "Browser.createBrowserContext":
		var params struct {
			RemoveOnDetach bool `json:"removeOnDetach"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil || !params.RemoveOnDetach {
			return
		}
		var out struct {
			BrowserContextID string `json:"browserContextId"`
		}
		if err := json.Unmarshal(result, &out); err == nil {
			wsc.trackRemoteContext(out.BrowserContextID)
		}
	case "Browser.removeBrowserContext":
		var params struct {
			BrowserContextID string `json:"browserContextId"`
		}
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			wsc.untrackRemoteContext(params.BrowserContextID)
		}
	}
}

func (s *Server) cleanupRemoteContexts(wsc *wsClient) {
	if wsc == nil || s.client == nil {
		return
	}
	for _, contextID := range wsc.drainRemoteContexts() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := s.client.CallWithContext(ctx, "", "Browser.removeBrowserContext", map[string]interface{}{
			"browserContextId": contextID,
		})
		cancel()
		if err != nil {
			log.Printf("remote context cleanup failed for %s: %v", contextID, err)
		}
	}
}

func (s *Server) writeBroadcasts(wsc *wsClient) {
	for env := range wsc.events {
		if err := s.writeClient(wsc, env); err != nil {
			log.Printf("remote event write error: %v", err)
			s.removeClients([]*wsClient{wsc})
			return
		}
	}
}

func (s *Server) handleControl(wsc *wsClient, payload json.RawMessage) {
	var cmd struct {
		Command string          `json:"command"`
		Params  json.RawMessage `json:"params"`
		ID      int             `json:"id"`
	}
	if err := json.Unmarshal(payload, &cmd); err != nil {
		return
	}

	respPayload := map[string]interface{}{
		"id": cmd.ID,
	}
	switch cmd.Command {
	case "ping":
		respPayload["result"] = map[string]string{"status": "pong"}
	default:
		// Dispatch to the control API if available.
		if s.controlAPI != nil {
			result, err := s.controlAPI.HandleMessage(cmd.Command, cmd.Params)
			if err != nil {
				respPayload["error"] = err.Error()
			} else {
				respPayload["result"] = json.RawMessage(result)
			}
		} else {
			respPayload["error"] = fmt.Sprintf("unknown command: %s", cmd.Command)
		}
	}

	env, _ := NewControlEnvelope("response", respPayload)
	if err := s.writeClient(wsc, env); err != nil {
		log.Printf("remote control write error: %v", err)
	}
}

func (s *Server) writeClient(wsc *wsClient, data []byte) error {
	baseCtx := wsc.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(baseCtx, s.writeTimeout)
	defer cancel()
	return wsc.write(ctx, websocket.MessageText, data)
}
