package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"nhooyr.io/websocket"

	"vulpineos/internal/juggler"
)

// Server exposes the VulpineOS kernel over WebSocket for remote TUI clients.
type Server struct {
	auth      *Authenticator
	client    *juggler.Client
	addr      string
	server    *http.Server
	clients   map[*wsClient]struct{}
	clientsMu sync.RWMutex
}

type wsClient struct {
	conn *websocket.Conn
	ctx  context.Context
}

// NewServer creates a remote access server.
func NewServer(addr string, apiKey string, jugglerClient *juggler.Client) *Server {
	s := &Server{
		auth:    NewAuthenticator(apiKey),
		client:  jugglerClient,
		addr:    addr,
		clients: make(map[*wsClient]struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	s.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	return s
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
	return s.server.Shutdown(ctx)
}

// BroadcastEvent sends a Juggler event to all connected clients.
// Clients that fail to receive the message are removed.
func (s *Server) BroadcastEvent(method string, params json.RawMessage) {
	msg, _ := json.Marshal(&juggler.Message{
		Method: method,
		Params: params,
	})
	env, err := NewJugglerEnvelope(msg)
	if err != nil {
		return
	}

	s.clientsMu.RLock()
	var dead []*wsClient
	for c := range s.clients {
		if err := c.conn.Write(c.ctx, websocket.MessageText, env); err != nil {
			dead = append(dead, c)
		}
	}
	s.clientsMu.RUnlock()

	if len(dead) > 0 {
		s.clientsMu.Lock()
		for _, c := range dead {
			delete(s.clients, c)
			c.conn.Close(websocket.StatusGoingAway, "write error")
		}
		s.clientsMu.Unlock()
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	// Authenticate
	if !s.auth.Validate(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Allow cross-origin for TUI clients
	})
	if err != nil {
		log.Printf("websocket accept error: %v", err)
		return
	}

	ctx := r.Context()
	wsc := &wsClient{conn: conn, ctx: ctx}

	s.clientsMu.Lock()
	s.clients[wsc] = struct{}{}
	s.clientsMu.Unlock()

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, wsc)
		s.clientsMu.Unlock()
		conn.Close(websocket.StatusNormalClosure, "")
	}()

	log.Printf("Remote client connected from %s", r.RemoteAddr)

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
			result, err := s.client.Call(msg.SessionID, msg.Method, msg.Params)
			// Send response back
			resp := &juggler.Message{ID: msg.ID, SessionID: msg.SessionID}
			if err != nil {
				resp.Error = &juggler.Error{Message: err.Error()}
			} else {
				resp.Result = result
			}
			respData, _ := json.Marshal(resp)
			respEnv, _ := NewJugglerEnvelope(respData)
			conn.Write(ctx, websocket.MessageText, respEnv)

		case "control":
			// Handle control commands (restart, spawn, etc.)
			s.handleControl(wsc, env.Payload)
		}
	}
}

func (s *Server) handleControl(wsc *wsClient, payload json.RawMessage) {
	var cmd struct {
		Command string          `json:"command"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(payload, &cmd); err != nil {
		return
	}

	var resp []byte
	switch cmd.Command {
	case "ping":
		resp, _ = json.Marshal(map[string]string{"status": "pong"})
	default:
		resp, _ = json.Marshal(map[string]string{"error": fmt.Sprintf("unknown command: %s", cmd.Command)})
	}

	env, _ := NewControlEnvelope("response", json.RawMessage(resp))
	wsc.conn.Write(wsc.ctx, websocket.MessageText, env)
}
