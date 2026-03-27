package security

import (
	"encoding/json"
	"fmt"
	"time"
	"vulpineos/internal/juggler"
)

// MutationAlert represents a suspicious DOM mutation detected by the observer.
type MutationAlert struct {
	AgentID   string    `json:"agentId"`
	Type      string    `json:"type"` // "hidden_text", "new_script", "dynamic_element"
	Selector  string    `json:"selector"`
	Content   string    `json:"content"` // first 200 chars
	Timestamp time.Time `json:"timestamp"`
}

// MutationMonitor watches for elements injected after page load.
type MutationMonitor struct {
	alerts chan MutationAlert
}

// NewMutationMonitor creates a new MutationMonitor.
func NewMutationMonitor() *MutationMonitor {
	return &MutationMonitor{
		alerts: make(chan MutationAlert, 100),
	}
}

// Alerts returns a read-only channel of mutation alerts.
func (m *MutationMonitor) Alerts() <-chan MutationAlert {
	return m.alerts
}

// GenerateObserverScript returns JS to inject via addScriptToEvaluateOnNewDocument
// that sets up a MutationObserver watching for suspicious DOM mutations.
func GenerateObserverScript() string {
	return `(function() {
  if (window.__vulpineObserverInstalled) return;
  window.__vulpineObserverInstalled = true;
  window.__vulpineAlerts = [];

  function truncate(s, n) { return s && s.length > n ? s.substring(0, n) : (s || ''); }

  function getSelector(el) {
    if (el.id) return '#' + el.id;
    if (el.className && typeof el.className === 'string') {
      var cls = el.className.trim().split(/\s+/).slice(0, 2).join('.');
      if (cls) return el.tagName.toLowerCase() + '.' + cls;
    }
    return el.tagName ? el.tagName.toLowerCase() : 'unknown';
  }

  function isHidden(el) {
    if (!el || !el.style) return false;
    var cs = window.getComputedStyle(el);
    if (!cs) return false;
    if (cs.display === 'none') return true;
    if (cs.visibility === 'hidden' || cs.visibility === 'collapse') return true;
    if (cs.opacity === '0') return true;
    var rect = el.getBoundingClientRect();
    if (rect.width === 0 && rect.height === 0) return true;
    if (rect.top < -500 || rect.left < -500 || rect.top > window.innerHeight + 500 || rect.left > window.innerWidth + 500) return true;
    return false;
  }

  function addAlert(type, el) {
    var text = truncate(el.textContent || el.innerText || '', 200);
    window.__vulpineAlerts.push({
      type: type,
      selector: getSelector(el),
      content: text,
      timestamp: Date.now()
    });
  }

  var observer = new MutationObserver(function(mutations) {
    for (var i = 0; i < mutations.length; i++) {
      var mutation = mutations[i];
      for (var j = 0; j < mutation.addedNodes.length; j++) {
        var node = mutation.addedNodes[j];
        if (node.nodeType !== 1) continue;

        if (node.tagName === 'SCRIPT') {
          addAlert('new_script', node);
          continue;
        }

        if (isHidden(node)) {
          var text = (node.textContent || '').trim();
          if (text.length > 0) {
            addAlert('hidden_text', node);
            continue;
          }
        }

        addAlert('dynamic_element', node);
      }
    }
  });

  observer.observe(document.documentElement || document.body, {
    childList: true,
    subtree: true
  });
})();`
}

// rawAlert is the JSON shape returned by the observer script.
type rawAlert struct {
	Type      string `json:"type"`
	Selector  string `json:"selector"`
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp"`
}

// CheckAlerts evaluates the page to retrieve any stored mutation alerts.
func (m *MutationMonitor) CheckAlerts(client *juggler.Client, sessionID, agentID string) []MutationAlert {
	// Evaluate JS to retrieve and clear alerts
	script := `(function() {
		var alerts = window.__vulpineAlerts || [];
		window.__vulpineAlerts = [];
		return JSON.stringify(alerts);
	})()`

	result, err := client.Call(sessionID, "Runtime.evaluate", map[string]interface{}{
		"expression":    script,
		"returnByValue": true,
	})
	if err != nil {
		return nil
	}

	// Parse the result — Juggler returns {result: {value: "..."}}
	var evalResult struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(result, &evalResult); err != nil {
		return nil
	}

	// The value is a JSON string inside a JSON value
	var jsonStr string
	if err := json.Unmarshal(evalResult.Result.Value, &jsonStr); err != nil {
		return nil
	}

	var raws []rawAlert
	if err := json.Unmarshal([]byte(jsonStr), &raws); err != nil {
		return nil
	}

	alerts := make([]MutationAlert, 0, len(raws))
	for _, r := range raws {
		alert := MutationAlert{
			AgentID:   agentID,
			Type:      r.Type,
			Selector:  r.Selector,
			Content:   r.Content,
			Timestamp: time.UnixMilli(r.Timestamp),
		}
		alerts = append(alerts, alert)

		// Also push to channel (non-blocking)
		select {
		case m.alerts <- alert:
		default:
		}
	}

	return alerts
}

// InjectObserver adds the mutation observer script to a page session via
// Page.addScriptToEvaluateOnNewDocument.
func InjectObserver(client *juggler.Client, sessionID string) error {
	script := GenerateObserverScript()
	_, err := client.Call(sessionID, "Page.addScriptToEvaluateOnNewDocument", map[string]interface{}{
		"script": script,
	})
	if err != nil {
		return fmt.Errorf("inject observer: %w", err)
	}
	return nil
}
