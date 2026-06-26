package runtimeaudit

import (
	"net/url"
	"regexp"
	"strings"
	"sync"

	"vulpineos/internal/vault"
)

var bearerTokenPattern = regexp.MustCompile(`(?i)(bearer\s+)[^\s,;]+`)
var querySecretPattern = regexp.MustCompile(`(?i)([?&](?:api[_-]?key|apikey|token|access[_-]?token|access[_-]?key|secret|password|credential|authorization|cookie|session)=)[^&#\s"]+`)
var jsonSecretPattern = regexp.MustCompile(`(?i)("(?:apiKey|api_key|apikey|token|access_token|access_key|secret|password|credential|authorization|cookie|session)"\s*:\s*")[^"]+(")`)
var keyValueSecretPattern = regexp.MustCompile(`(?i)(^|[^?&A-Za-z0-9_])((?:api[_-]?key|apikey|token|access[_-]?token|access[_-]?key|secret|password|credential|authorization|cookie|session)\s*=\s*)[^\s,;"]+`)

// Manager persists and broadcasts recent runtime lifecycle events.
type Manager struct {
	vault *vault.DB

	mu    sync.RWMutex
	subs  map[chan vault.RuntimeEvent]struct{}
	close bool
}

// New creates a runtime audit manager.
func New(v *vault.DB) *Manager {
	return &Manager{
		vault: v,
		subs:  make(map[chan vault.RuntimeEvent]struct{}),
	}
}

// Log persists a runtime event and broadcasts it to subscribers.
func (m *Manager) Log(component, level, event, message string, metadata map[string]string) (*vault.RuntimeEvent, error) {
	if m == nil || m.vault == nil {
		return nil, nil
	}
	message = redactRuntimeValue(message)
	metadata = sanitizeRuntimeMetadata(metadata)
	entry, err := m.vault.AppendRuntimeEvent(component, level, event, message, metadata)
	if err != nil || entry == nil {
		return entry, err
	}

	m.mu.RLock()
	for ch := range m.subs {
		select {
		case ch <- *entry:
		default:
		}
	}
	m.mu.RUnlock()
	return entry, nil
}

// List returns the most recent persisted runtime events.
func (m *Manager) List(filter vault.RuntimeEventFilter) ([]vault.RuntimeEvent, error) {
	if m == nil || m.vault == nil {
		return nil, nil
	}
	return m.vault.ListRuntimeEvents(filter)
}

// Settings returns persisted runtime audit settings.
func (m *Manager) Settings() (vault.RuntimeAuditSettings, error) {
	if m == nil || m.vault == nil {
		return vault.RuntimeAuditSettings{}, nil
	}
	return m.vault.RuntimeAuditSettings()
}

// SetRetention updates the persisted runtime audit retention window.
func (m *Manager) SetRetention(limit int) (vault.RuntimeAuditSettings, error) {
	if m == nil || m.vault == nil {
		return vault.RuntimeAuditSettings{}, nil
	}
	return m.vault.SetRuntimeAuditRetention(limit)
}

// Subscribe returns a live event channel.
func (m *Manager) Subscribe() chan vault.RuntimeEvent {
	ch := make(chan vault.RuntimeEvent, 32)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.close {
		close(ch)
		return ch
	}
	m.subs[ch] = struct{}{}
	return ch
}

// Unsubscribe removes a live event subscriber.
func (m *Manager) Unsubscribe(ch chan vault.RuntimeEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.subs[ch]; exists {
		delete(m.subs, ch)
		close(ch)
	}
}

func sanitizeRuntimeMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return nil
	}
	sanitized := make(map[string]string, len(metadata))
	for key, value := range metadata {
		if sensitiveRuntimeMetadataKey(key) {
			sanitized[key] = "[redacted]"
			continue
		}
		sanitized[key] = redactRuntimeValue(value)
	}
	return sanitized
}

func sensitiveRuntimeMetadataKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"))
	for _, marker := range []string{"api_key", "apikey", "token", "secret", "password", "credential", "authorization"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return normalized == "auth" || strings.HasSuffix(normalized, "_auth")
}

func redactRuntimeValue(value string) string {
	value = bearerTokenPattern.ReplaceAllString(value, "${1}[redacted]")
	value = querySecretPattern.ReplaceAllString(value, "${1}[redacted]")
	value = jsonSecretPattern.ReplaceAllString(value, "${1}[redacted]${2}")
	value = keyValueSecretPattern.ReplaceAllString(value, "${1}${2}[redacted]")
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return value
	}
	query := parsed.Query()
	redacted := false
	for key := range query {
		if sensitiveRuntimeMetadataKey(key) {
			query.Set(key, "[redacted]")
			redacted = true
		}
	}
	if redacted {
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return value
}

// Close closes all subscribers.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.close {
		return
	}
	m.close = true
	for ch := range m.subs {
		close(ch)
		delete(m.subs, ch)
	}
}
