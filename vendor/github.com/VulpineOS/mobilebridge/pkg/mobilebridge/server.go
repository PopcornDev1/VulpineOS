package mobilebridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Server exposes Chrome-compatible /json endpoints and a /devtools/page/<id>
// WebSocket endpoint that proxies to an Android device's Chrome over ADB.
type Server struct {
	serial string
	addr   string

	mu       sync.Mutex
	httpSrv  *http.Server
	upgrader websocket.Upgrader

	// listCacheMu guards a short-lived /json/list response cache. Chrome
	// devtools clients poll this endpoint aggressively, so we coalesce
	// upstream GETs to one per jsonListCacheTTL window.
	listCacheMu  sync.Mutex
	listCacheBuf []byte
	listCacheAt  time.Time
}

// jsonListCacheTTL is how long /json/list responses are cached. Short enough
// that a newly-opened tab shows up within one poll interval, long enough to
// soak a burst of concurrent polls down to a single upstream request.
const jsonListCacheTTL = 500 * time.Millisecond

// WatchDeviceChanges starts a WatchDevices goroutine bound to ctx and
// invalidates the /json/list cache on every add/remove event, so the next
// poll fetches a fresh list rather than serving the previous device's tabs
// for up to jsonListCacheTTL. Returns immediately after starting the watcher.
func (s *Server) WatchDeviceChanges(ctx context.Context) error {
	events, err := WatchDevices(ctx)
	if err != nil {
		return err
	}
	go func() {
		for range events {
			s.invalidateListCache()
		}
	}()
	return nil
}

// invalidateListCache wipes the /json/list response cache so the next hit
// forces a fresh upstream fetch. Called on device hotplug and whenever a
// new proxy is attached via RunWithProxy.
func (s *Server) invalidateListCache() {
	s.listCacheMu.Lock()
	s.listCacheBuf = nil
	s.listCacheAt = time.Time{}
	s.listCacheMu.Unlock()
}

// NewServer constructs a Server bound to addr (e.g. "127.0.0.1:9222") that
// will proxy CDP traffic to the named device.
func NewServer(serial string, addr string) *Server {
	return &Server{
		serial:   serial,
		addr:     addr,
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
	}
}

// Start begins listening. It returns once the listener is accepting connections
// (or immediately on bind failure).
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", s.handleVersion)
	mux.HandleFunc("/json/list", s.handleList)
	mux.HandleFunc("/json", s.handleList)
	mux.HandleFunc("/json/new", s.handleNew)
	mux.HandleFunc("/devtools/page/", s.handleWebSocket)
	mux.HandleFunc("/devtools/browser", s.handleWebSocket)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.mu.Lock()
	s.httpSrv = srv
	s.mu.Unlock()

	go func() {
		_ = srv.Serve(ln)
	}()
	return nil
}

// Stop shuts the HTTP server down with a short grace period.
func (s *Server) Stop() error {
	s.mu.Lock()
	srv := s.httpSrv
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

// handleVersion proxies Chrome's /json/version from the device via the adb
// forward, so clients see a real browser descriptor.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	s.proxyJSON(w, "/json/version")
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	s.proxyJSON(w, "/json/list")
}

func (s *Server) handleNew(w http.ResponseWriter, r *http.Request) {
	relPath := "/json/new"
	if q := r.URL.Query().Get("url"); q != "" {
		relPath += "?" + r.URL.RawQuery
	}
	s.proxyJSON(w, relPath)
}

// proxyJSON forwards a GET to the local ADB-forwarded port (set up by the
// caller, typically via NewProxy). This is a best-effort helper: if no proxy
// has been wired up yet it returns 503.
func (s *Server) proxyJSON(w http.ResponseWriter, path string) {
	_, port, err := net.SplitHostPort(s.addr)
	if err != nil {
		http.Error(w, "bad server addr: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// NOTE: by default we assume the adb-forwarded Chrome lives on the same
	// port as we serve on, which will not be true in production usage. A
	// real caller wires a Proxy into the server (see RunWithProxy) to expose
	// the forwarded port directly. The server's /json endpoints in that
	// case are provided by the downstream Chrome itself.
	_ = port
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]string{
		"Browser": "mobilebridge (not yet attached)",
		"error":   "proxy not attached; call RunWithProxy",
	}
	b, _ := json.Marshal(resp)
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write(b)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "mobilebridge: websocket endpoint requires RunWithProxy wiring", http.StatusServiceUnavailable)
}

// RunWithProxy wires a live Proxy into an already-Started Server. After
// this returns, the Server's /json/* endpoints forward to the proxy's
// adb-forwarded Chrome and /devtools/page/<id> websocket handshakes are
// routed into Proxy.Serve. Single-client: the websocket handler uses
// Proxy.Busy to return 503 if another client is already attached.
//
// Call Start before RunWithProxy. Passing a nil proxy returns an error.
func (s *Server) RunWithProxy(p *Proxy) error {
	if p == nil {
		return errors.New("mobilebridge: nil proxy")
	}
	// A new proxy means a (potentially) new device / Chrome instance — any
	// cached /json/list body belongs to the previous session and must not
	// leak across the swap.
	s.invalidateListCache()
	s.mu.Lock()
	srv := s.httpSrv
	s.mu.Unlock()
	if srv == nil {
		return errors.New("mobilebridge: server not started")
	}

	mux := http.NewServeMux()
	base := fmt.Sprintf("http://127.0.0.1:%d", p.localPort)

	// publicHost is the host:port clients see. If s.addr binds 0.0.0.0 or
	// omits a host, we default to 127.0.0.1 so rewritten URLs are usable.
	publicHost := s.addr
	if host, port, err := net.SplitHostPort(s.addr); err == nil {
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		publicHost = net.JoinHostPort(host, port)
	}

	forwardJSON := func(path string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			u := base + path
			if r.URL.RawQuery != "" {
				u += "?" + r.URL.RawQuery
			}
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Get(u)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			rewritten := rewriteDevtoolsJSON(body, publicHost)
			for k, vv := range resp.Header {
				if k == "Content-Length" {
					continue
				}
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(rewritten)
		}
	}

	// /json/list is cached for jsonListCacheTTL to coalesce polling clients.
	listHandler := forwardJSON("/json/list")
	cachedList := func(w http.ResponseWriter, r *http.Request) {
		s.listCacheMu.Lock()
		if time.Since(s.listCacheAt) < jsonListCacheTTL && s.listCacheBuf != nil {
			body := s.listCacheBuf
			s.listCacheMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
			return
		}
		s.listCacheMu.Unlock()
		rec := &cachingResponseWriter{ResponseWriter: w}
		listHandler(rec, r)
		// Only cache a verified 200. Upstream 5xx bodies are transient
		// errors and must not be served to subsequent clients for the
		// next jsonListCacheTTL window. forwardJSON always calls
		// WriteHeader with upstream's status, so rec.status == 0 means
		// the handler errored out before writing anything and also
		// should not be cached.
		if rec.status == http.StatusOK {
			s.listCacheMu.Lock()
			s.listCacheBuf = rec.buf
			s.listCacheAt = time.Now()
			s.listCacheMu.Unlock()
		}
	}

	mux.HandleFunc("/json/version", forwardJSON("/json/version"))
	mux.HandleFunc("/json/list", cachedList)
	mux.HandleFunc("/json", cachedList)
	mux.HandleFunc("/json/new", forwardJSON("/json/new"))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})

	// WebSocket: accept a downstream client, then hand to proxy.Serve. Note
	// that each inbound websocket consumes the single upstream owned by the
	// proxy; concurrent clients are not supported in this MVP.
	wsHandler := func(w http.ResponseWriter, r *http.Request) {
		if p.Busy() {
			http.Error(w, "mobilebridge: another client is already attached (single-client MVP)", http.StatusServiceUnavailable)
			return
		}
		ws, err := s.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		if err := p.Serve(r.Context(), ws); err != nil && errors.Is(err, ErrBusy) {
			_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"error":"mobilebridge busy"}`))
		}
	}
	mux.HandleFunc("/devtools/page/", wsHandler)
	mux.HandleFunc("/devtools/browser", wsHandler)

	srv.Handler = mux
	return nil
}

// cachingResponseWriter is a thin http.ResponseWriter wrapper that buffers
// the body and captures the status code so a successful /json/list reply
// can be cached for jsonListCacheTTL.
type cachingResponseWriter struct {
	http.ResponseWriter
	status int
	buf    []byte
}

func (c *cachingResponseWriter) WriteHeader(code int) {
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

func (c *cachingResponseWriter) Write(b []byte) (int, error) {
	// Only buffer the body if we're going to actually cache it. Errors
	// (5xx, 4xx) pass through to the client without being stored.
	if c.status == 0 || c.status == http.StatusOK {
		c.buf = append(c.buf, b...)
	}
	return c.ResponseWriter.Write(b)
}

// rewriteDevtoolsJSON takes a raw /json/version or /json/list body from the
// upstream Chrome (served via the adb forward on 127.0.0.1:<localPort>) and
// rewrites any webSocketDebuggerUrl / devtoolsFrontendUrl fields so they
// point back at this server's publicHost. That ensures CDP clients which
// follow those URLs (Puppeteer, chrome-remote-interface) keep their traffic
// going through mobilebridge instead of bypassing it.
//
// If the body isn't JSON we recognise, it's returned unchanged.
func rewriteDevtoolsJSON(body []byte, publicHost string) []byte {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return body
	}
	switch trimmed[0] {
	case '[':
		var arr []map[string]interface{}
		if err := json.Unmarshal(body, &arr); err != nil {
			return body
		}
		for _, entry := range arr {
			rewriteEntry(entry, publicHost)
		}
		out, err := json.Marshal(arr)
		if err != nil {
			return body
		}
		return out
	case '{':
		var obj map[string]interface{}
		if err := json.Unmarshal(body, &obj); err != nil {
			return body
		}
		rewriteEntry(obj, publicHost)
		out, err := json.Marshal(obj)
		if err != nil {
			return body
		}
		return out
	}
	return body
}

func rewriteEntry(entry map[string]interface{}, publicHost string) {
	if v, ok := entry["webSocketDebuggerUrl"].(string); ok {
		entry["webSocketDebuggerUrl"] = rewriteWSURL(v, publicHost)
	}
	if v, ok := entry["devtoolsFrontendUrl"].(string); ok {
		entry["devtoolsFrontendUrl"] = rewriteFrontendURL(v, publicHost)
	}
}

// rewriteWSURL replaces the host component of a ws:// URL with publicHost,
// leaving scheme/path/query intact. Uses net/url.Parse so IPv6 literals like
// ws://[::1]:9999/devtools/page/X (whose brackets contain colons) parse
// correctly instead of breaking the naive string splice.
//
// Security: the original URL's scheme is always preserved. If the upstream
// URL is wss://, rewriting to publicHost would silently downgrade clients
// to plain ws:// against our HTTP listener (we don't terminate TLS in this
// MVP), so wss:// URLs are returned unchanged — clients will fail loudly
// against the original upstream host instead of accidentally TLS-handshaking
// against a plaintext server.
func rewriteWSURL(raw, publicHost string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return raw
	}
	if u.Scheme != "ws" {
		// wss:// or any non-ws scheme: leave untouched. Rewriting would
		// downgrade TLS clients to a plaintext listener.
		return raw
	}
	u.Host = publicHost
	return u.String()
}

// rewriteFrontendURL rewrites the `ws=host:port/path` query parameter Chrome
// embeds in devtoolsFrontendUrl so opening the inspector routes through us.
// Chrome emits this unescaped (e.g. "ws=127.0.0.1:9222/devtools/page/ABC")
// and we must preserve that shape — url.Values.Encode would percent-encode
// the slashes and break every real devtools frontend. We locate the `ws=`
// segment by splitting on the query separator so matches inside the path
// (e.g. "/devtools/ws=test/inspector.html") don't get mangled.
func rewriteFrontendURL(raw, publicHost string) string {
	// Split into path and query exactly once on the first '?'.
	qIdx := strings.IndexByte(raw, '?')
	if qIdx < 0 {
		return raw
	}
	pathPart := raw[:qIdx+1]
	query := raw[qIdx+1:]
	// Drop off any fragment so it doesn't appear inside query segments.
	var fragment string
	if f := strings.IndexByte(query, '#'); f >= 0 {
		fragment = query[f:]
		query = query[:f]
	}
	// Walk the &-separated params, only rewriting the literal "ws" key.
	params := strings.Split(query, "&")
	found := false
	for i, part := range params {
		if !strings.HasPrefix(part, "ws=") {
			continue
		}
		found = true
		value := part[3:]
		// Within value, /path starts at the first '/' after the host.
		// IPv6 bracketed host "[::1]:9222/..." is safe because brackets
		// contain no '/'.
		slash := strings.IndexByte(value, '/')
		if slash < 0 {
			params[i] = "ws=" + publicHost
		} else {
			params[i] = "ws=" + publicHost + value[slash:]
		}
		break
	}
	if !found {
		return raw
	}
	return pathPart + strings.Join(params, "&") + fragment
}
