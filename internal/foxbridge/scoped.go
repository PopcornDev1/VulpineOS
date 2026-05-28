package foxbridge

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"vulpineos/internal/juggler"

	"github.com/VulpineOS/foxbridge/pkg/backend"
)

type jugglerBackend interface {
	Call(sessionID, method string, params interface{}) (json.RawMessage, error)
	Subscribe(event string, handler juggler.EventHandler)
	SubscribeWithCancel(event string, handler juggler.EventHandler) func()
}

type scopedBackend struct {
	client           jugglerBackend
	browserContextID string

	mu              sync.RWMutex
	allowedSessions map[string]struct{}
	allowedTargets  map[string]struct{}
	allowedRequests map[string]struct{}
	cancelSubs      []func()
}

var _ backend.Backend = (*scopedBackend)(nil)

func newScopedBackend(client jugglerBackend, browserContextID string) *scopedBackend {
	return &scopedBackend{
		client:           client,
		browserContextID: browserContextID,
		allowedSessions:  make(map[string]struct{}),
		allowedTargets:   make(map[string]struct{}),
		allowedRequests:  make(map[string]struct{}),
	}
}

func (b *scopedBackend) Call(sessionID, method string, params json.RawMessage) (json.RawMessage, error) {
	switch method {
	case "Browser.createBrowserContext":
		return json.Marshal(map[string]string{"browserContextId": b.browserContextID})
	case "Browser.removeBrowserContext":
		if err := b.validateOptionalBrowserContext(method, params); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	case "Browser.newPage":
		withContext, err := b.injectBrowserContext(method, params)
		if err != nil {
			return nil, err
		}
		return b.client.Call(sessionID, method, withContext)
	case "Browser.close":
		return nil, fmt.Errorf("%s is blocked for scoped foxbridge sessions", method)
	default:
		if _, ok := contextScopedBrowserMethods[method]; ok {
			withContext, err := b.injectBrowserContext(method, params)
			if err != nil {
				return nil, err
			}
			return b.client.Call(sessionID, method, withContext)
		}
		if _, ok := safeGlobalBrowserMethods[method]; ok {
			return b.client.Call(sessionID, method, params)
		}
		if _, ok := requestScopedBrowserMethods[method]; ok {
			if err := b.validateTrackedRequest(method, params); err != nil {
				return nil, err
			}
			result, err := b.client.Call(sessionID, method, params)
			if err == nil && requestControlRetiresRequest(method) {
				b.retireTrackedRequest(params)
			}
			return result, err
		}
		if strings.HasPrefix(method, "Browser.") {
			return nil, fmt.Errorf("%s is not allowed for scoped foxbridge sessions", method)
		}
		if err := b.validateSession(method, sessionID); err != nil {
			return nil, err
		}
		return b.client.Call(sessionID, method, params)
	}
}

func (b *scopedBackend) Subscribe(event string, handler backend.EventHandler) {
	cancel := b.client.SubscribeWithCancel(event, func(sessionID string, params json.RawMessage) {
		if !b.shouldForward(event, sessionID, params) {
			return
		}
		handler(sessionID, params)
	})
	b.mu.Lock()
	b.cancelSubs = append(b.cancelSubs, cancel)
	b.mu.Unlock()
}

func (b *scopedBackend) Close() error {
	b.mu.Lock()
	cancels := append([]func(){}, b.cancelSubs...)
	b.cancelSubs = nil
	b.allowedSessions = make(map[string]struct{})
	b.allowedTargets = make(map[string]struct{})
	b.allowedRequests = make(map[string]struct{})
	b.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return nil
}

var contextScopedBrowserMethods = map[string]struct{}{
	"Browser.clearCookies":           {},
	"Browser.getCookies":             {},
	"Browser.grantPermissions":       {},
	"Browser.setCookies":             {},
	"Browser.setDefaultViewport":     {},
	"Browser.setDownloadOptions":     {},
	"Browser.setExtraHTTPHeaders":    {},
	"Browser.setGeolocationOverride": {},
	"Browser.setLocaleOverride":      {},
	"Browser.setPlatformOverride":    {},
	"Browser.setRequestInterception": {},
	"Browser.setTimezoneOverride":    {},
	"Browser.setTouchOverride":       {},
	"Browser.setUserAgentOverride":   {},
}

var safeGlobalBrowserMethods = map[string]struct{}{
	"Browser.enable":  {},
	"Browser.getInfo": {},
}

var requestScopedBrowserMethods = map[string]struct{}{
	"Browser.abortInterceptedRequest":    {},
	"Browser.continueInterceptedRequest": {},
	"Browser.fulfillInterceptedRequest":  {},
	"Browser.getResponseBody":            {},
	"Browser.handleAuthRequest":          {},
}

func (b *scopedBackend) injectBrowserContext(method string, params json.RawMessage) (json.RawMessage, error) {
	payload := map[string]interface{}{}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &payload); err != nil {
			return nil, fmt.Errorf("parse %s params: %w", method, err)
		}
	}
	if raw, ok := payload["browserContextId"]; ok {
		if contextID, _ := raw.(string); contextID != "" && contextID != b.browserContextID {
			return nil, fmt.Errorf("browser context %v is outside scoped backend", raw)
		}
	}
	payload["browserContextId"] = b.browserContextID
	return json.Marshal(payload)
}

func (b *scopedBackend) validateOptionalBrowserContext(method string, params json.RawMessage) error {
	if len(params) == 0 {
		return nil
	}
	payload := map[string]interface{}{}
	if err := json.Unmarshal(params, &payload); err != nil {
		return fmt.Errorf("parse %s params: %w", method, err)
	}
	if raw, ok := payload["browserContextId"]; ok {
		if contextID, _ := raw.(string); contextID != "" && contextID != b.browserContextID {
			return fmt.Errorf("browser context %v is outside scoped backend", raw)
		}
	}
	return nil
}

func (b *scopedBackend) validateTrackedRequest(method string, params json.RawMessage) error {
	var payload struct {
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return fmt.Errorf("parse %s params: %w", method, err)
	}
	if payload.RequestID == "" {
		return fmt.Errorf("%s requires requestId for scoped foxbridge sessions", method)
	}
	b.mu.RLock()
	_, ok := b.allowedRequests[payload.RequestID]
	b.mu.RUnlock()
	if !ok {
		return fmt.Errorf("request %s is outside scoped backend", payload.RequestID)
	}
	return nil
}

func (b *scopedBackend) validateSession(method, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	b.mu.RLock()
	_, ok := b.allowedSessions[sessionID]
	b.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%s session %s is outside scoped backend", method, sessionID)
	}
	return nil
}

func (b *scopedBackend) retireTrackedRequest(params json.RawMessage) {
	var payload struct {
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(params, &payload); err != nil || payload.RequestID == "" {
		return
	}
	b.mu.Lock()
	delete(b.allowedRequests, payload.RequestID)
	b.mu.Unlock()
}

func requestControlRetiresRequest(method string) bool {
	switch method {
	case "Browser.abortInterceptedRequest", "Browser.continueInterceptedRequest", "Browser.fulfillInterceptedRequest", "Browser.handleAuthRequest":
		return true
	default:
		return false
	}
}

func (b *scopedBackend) shouldForward(event, sessionID string, params json.RawMessage) bool {
	switch event {
	case "Browser.attachedToTarget":
		var ev struct {
			SessionID  string `json:"sessionId"`
			TargetInfo struct {
				TargetID         string `json:"targetId"`
				BrowserContextID string `json:"browserContextId"`
			} `json:"targetInfo"`
		}
		if err := json.Unmarshal(params, &ev); err != nil {
			return false
		}
		if ev.TargetInfo.BrowserContextID != b.browserContextID {
			return false
		}
		b.mu.Lock()
		_, sessionKnown := b.allowedSessions[ev.SessionID]
		_, targetKnown := b.allowedTargets[ev.TargetInfo.TargetID]
		if sessionKnown || targetKnown {
			b.mu.Unlock()
			return false
		}
		if ev.SessionID != "" {
			b.allowedSessions[ev.SessionID] = struct{}{}
		}
		if ev.TargetInfo.TargetID != "" {
			b.allowedTargets[ev.TargetInfo.TargetID] = struct{}{}
		}
		b.mu.Unlock()
		return true

	case "Browser.detachedFromTarget":
		var ev struct {
			SessionID string `json:"sessionId"`
			TargetID  string `json:"targetId"`
		}
		if err := json.Unmarshal(params, &ev); err != nil {
			return false
		}
		b.mu.Lock()
		_, sessionAllowed := b.allowedSessions[ev.SessionID]
		_, targetAllowed := b.allowedTargets[ev.TargetID]
		if sessionAllowed {
			delete(b.allowedSessions, ev.SessionID)
		}
		if targetAllowed {
			delete(b.allowedTargets, ev.TargetID)
		}
		b.mu.Unlock()
		return sessionAllowed || targetAllowed

	case "Browser.requestIntercepted":
		var ev struct {
			RequestID        string `json:"requestId"`
			BrowserContextID string `json:"browserContextId"`
		}
		if err := json.Unmarshal(params, &ev); err != nil {
			return false
		}
		if ev.BrowserContextID != b.browserContextID {
			return false
		}
		if ev.RequestID != "" {
			b.mu.Lock()
			b.allowedRequests[ev.RequestID] = struct{}{}
			b.mu.Unlock()
		}
		return true
	}

	if sessionID == "" {
		return false
	}

	b.mu.RLock()
	_, ok := b.allowedSessions[sessionID]
	b.mu.RUnlock()
	return ok
}
