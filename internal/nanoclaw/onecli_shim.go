package nanoclaw

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// LocalOneCLIShim provides the small OneCLI API surface current NanoClaw
// expects before spawning an agent container. It keeps VulpineOS local agents
// independent from app.onecli.sh while still letting upstream NanoClaw run
// unmodified.
type LocalOneCLIShim struct {
	server    *http.Server
	listener  net.Listener
	accessKey string
	envSource func() map[string]string
}

func StartLocalOneCLIShim(env map[string]string) (*LocalOneCLIShim, error) {
	return StartDynamicLocalOneCLIShim(func() map[string]string {
		return cloneStringMap(env)
	})
}

func StartDynamicLocalOneCLIShim(envSource func() map[string]string) (*LocalOneCLIShim, error) {
	accessKey, err := randomAccessKey()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen local OneCLI shim: %w", err)
	}
	shim := &LocalOneCLIShim{
		listener:  ln,
		accessKey: accessKey,
		envSource: envSource,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/agents", shim.handleAgents)
	mux.HandleFunc("/api/container-config", shim.handleContainerConfig)
	mux.HandleFunc("/api/gateway-url", shim.handleGatewayURL)
	mux.HandleFunc("/api/approvals/pending", shim.handlePendingApprovals)
	mux.HandleFunc("/api/skill/gateway", shim.handleGatewaySkill)
	mux.HandleFunc("/api/approvals/", shim.handleApprovalDecision)
	shim.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := shim.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			// The daemon owns this shim; startup and runtime health are checked
			// by NanoClaw's own spawn path.
			return
		}
	}()
	return shim, nil
}

func (s *LocalOneCLIShim) URL() string {
	if s == nil || s.listener == nil {
		return ""
	}
	return "http://" + s.listener.Addr().String()
}

func (s *LocalOneCLIShim) AccessKey() string {
	if s == nil {
		return ""
	}
	return s.accessKey
}

func (s *LocalOneCLIShim) Stop() error {
	if s == nil || s.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}

func (s *LocalOneCLIShim) authorize(w http.ResponseWriter, r *http.Request) bool {
	if strings.TrimSpace(s.accessKey) == "" {
		http.Error(w, "local OneCLI shim unavailable", http.StatusServiceUnavailable)
		return false
	}
	if r.Header.Get("Authorization") != "Bearer "+s.accessKey {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func (s *LocalOneCLIShim) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorize(w, r) {
		return
	}
	var input struct {
		Name       string `json:"name"`
		Identifier string `json:"identifier"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":       input.Name,
		"identifier": input.Identifier,
		"created":    true,
	})
}

func (s *LocalOneCLIShim) handleContainerConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorize(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"env":                            s.containerEnv(),
		"caCertificate":                  "",
		"caCertificateContainerPath":     "/tmp/onecli-proxy-ca.pem",
		"caCertificateCombinedContainer": "/tmp/onecli-combined-ca.pem",
	})
}

func (s *LocalOneCLIShim) containerEnv() map[string]string {
	if s == nil || s.envSource == nil {
		return map[string]string{}
	}
	return cloneStringMap(s.envSource())
}

func (s *LocalOneCLIShim) handleGatewayURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorize(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": s.URL()})
}

func (s *LocalOneCLIShim) handlePendingApprovals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorize(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"requests":       []interface{}{},
		"timeoutSeconds": 30,
	})
}

func (s *LocalOneCLIShim) handleGatewaySkill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorize(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write([]byte("# VulpineOS Local Gateway\n\nLocal VulpineOS agents run without the OneCLI cloud credential gateway.\n"))
}

func (s *LocalOneCLIShim) handleApprovalDecision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorize(w, r) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func randomAccessKey() (string, error) {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate local OneCLI access key: %w", err)
	}
	return "local-" + hex.EncodeToString(buf[:]), nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		key = strings.TrimSpace(key)
		if key == "" || strings.TrimSpace(value) == "" {
			continue
		}
		out[key] = value
	}
	return out
}
