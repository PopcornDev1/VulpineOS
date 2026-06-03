package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"vulpineos/internal/juggler"
)

// --- New agent reliability tools ---

// handleWait waits for a condition to be met on the page.
// Supports: element appears, element contains text, network idle, DOM stable.
func handleWait(client *juggler.Client, tracker *ContextTracker, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Condition string `json:"condition"` // "element", "text", "networkIdle", "domStable", "urlContains"
		Selector  string `json:"selector"`  // CSS selector for "element" condition
		Text      string `json:"text"`      // text to match for "text" or "urlContains"
		Ref       string `json:"ref"`       // element ref for "text" condition
		Timeout   int    `json:"timeout"`   // seconds, default 10
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	timeout := 10
	if p.Timeout > 0 {
		timeout = p.Timeout
	}
	if timeout > 30 {
		timeout = 30
	}

	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	pollInterval := 300 * time.Millisecond

	for time.Now().Before(deadline) {
		met, detail, err := checkCondition(client, tracker, p.SessionID, p.Condition, p.Selector, p.Text, p.Ref)
		if err != nil {
			return errorResult(err), nil
		}
		if met {
			return textResult(fmt.Sprintf("Condition met: %s (%s)", p.Condition, detail)), nil
		}
		time.Sleep(pollInterval)
	}

	return errorResult(fmt.Errorf("timeout after %ds waiting for condition: %s", timeout, p.Condition)), nil
}

func checkCondition(client *juggler.Client, tracker *ContextTracker, sessionID, condition, selector, text, ref string) (bool, string, error) {
	switch condition {
	case "element":
		// Check if element matching CSS selector exists and is visible
		js := fmt.Sprintf(`(() => {
			const el = document.querySelector(%q);
			if (!el) return JSON.stringify({found: false});
			const rect = el.getBoundingClientRect();
			const visible = rect.width > 0 && rect.height > 0 &&
				window.getComputedStyle(el).display !== 'none' &&
				window.getComputedStyle(el).visibility !== 'hidden';
			return JSON.stringify({found: true, visible: visible, text: el.textContent.substring(0, 100)});
		})()`, selector)
		result, err := evalJS(client, sessionID, js)
		if err != nil {
			return false, "", nil // page might not be ready yet
		}
		var r struct {
			Found   bool   `json:"found"`
			Visible bool   `json:"visible"`
			Text    string `json:"text"`
		}
		json.Unmarshal([]byte(result), &r)
		if r.Found && r.Visible {
			return true, fmt.Sprintf("element %q found: %q", selector, truncate(r.Text, 50)), nil
		}
		return false, "", fmt.Errorf("element %q not found or not visible", selector)

	case "text":
		// Check if page body contains specific text
		js := `document.body.innerText`
		result, err := evalJS(client, sessionID, js)
		if err != nil {
			return false, "", nil
		}
		if strings.Contains(result, text) {
			return true, fmt.Sprintf("text %q found on page", truncate(text, 50)), nil
		}
		return false, "", fmt.Errorf("text %q not found on page", text)

	case "networkIdle":
		// Check if there are no pending network requests (via performance API)
		js := `(() => {
			const entries = performance.getEntriesByType('resource');
			const recent = entries.filter(e => (performance.now() - e.startTime) < 500 && e.duration === 0);
			return JSON.stringify({pending: recent.length});
		})()`
		result, err := evalJS(client, sessionID, js)
		if err != nil {
			return false, "", nil
		}
		var r struct {
			Pending int `json:"pending"`
		}
		json.Unmarshal([]byte(result), &r)
		if r.Pending == 0 {
			return true, "network idle", nil
		}
		return false, "", fmt.Errorf("network not idle")

	case "domStable":
		// Take two snapshots 300ms apart and compare
		js := `document.documentElement.innerHTML.length`
		result1, err := evalJS(client, sessionID, js)
		if err != nil {
			return false, "", nil
		}
		time.Sleep(300 * time.Millisecond)
		result2, err := evalJS(client, sessionID, js)
		if err != nil {
			return false, "", nil
		}
		if result1 == result2 {
			return true, "DOM stable", nil
		}
		return false, "", fmt.Errorf("DOM not stable")

	case "urlContains":
		js := `window.location.href`
		result, err := evalJS(client, sessionID, js)
		if err != nil {
			return false, "", nil
		}
		if strings.Contains(result, text) {
			return true, fmt.Sprintf("URL contains %q", text), nil
		}
		return false, "", nil

	default:
		return false, "", fmt.Errorf("unknown condition: %s (use: element, text, networkIdle, domStable, urlContains)", condition)
	}
}

// handleFind searches for elements by text content, role, aria-label, or placeholder.
// Returns matching refs from the current snapshot.
func handleFind(client *juggler.Client, tracker *ContextTracker, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID  string `json:"sessionId"`
		Query      string `json:"query"`      // text to search for
		Role       string `json:"role"`       // optional: filter by role (button, link, input, etc.)
		MaxResults int    `json:"maxResults"` // default 5
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	maxResults := 5
	if p.MaxResults > 0 {
		maxResults = p.MaxResults
	}

	// Use JS to search the DOM for matching interactive elements
	js := fmt.Sprintf(`(() => {
		const query = %q.toLowerCase();
		const roleFilter = %q;
		const maxResults = %d;
		const results = [];

		// Interactive element selectors
		const selectors = 'a, button, input, select, textarea, [role="button"], [role="link"], [role="tab"], [role="menuitem"], [role="checkbox"], [role="radio"], [role="switch"], [role="option"], [tabindex]';
		const elements = document.querySelectorAll(selectors);

		for (const el of elements) {
			if (results.length >= maxResults) break;

			const text = (el.textContent || '').trim().toLowerCase();
			const ariaLabel = (el.getAttribute('aria-label') || '').toLowerCase();
			const placeholder = (el.getAttribute('placeholder') || '').toLowerCase();
			const title = (el.getAttribute('title') || '').toLowerCase();
			const value = (el.value || '').toLowerCase();
			const name = (el.getAttribute('name') || '').toLowerCase();

			const matches = text.includes(query) || ariaLabel.includes(query) ||
				placeholder.includes(query) || title.includes(query) ||
				value.includes(query) || name.includes(query);

			if (!matches) continue;

			// Role filter
			const role = el.getAttribute('role') || el.tagName.toLowerCase();
			if (roleFilter && role !== roleFilter && el.tagName.toLowerCase() !== roleFilter) continue;

			// Get visibility
			const rect = el.getBoundingClientRect();
			const visible = rect.width > 0 && rect.height > 0;
			if (!visible) continue;

			results.push({
				tag: el.tagName.toLowerCase(),
				role: role,
				text: (el.textContent || '').trim().substring(0, 100),
				ariaLabel: el.getAttribute('aria-label') || '',
				placeholder: el.getAttribute('placeholder') || '',
				type: el.type || '',
				x: Math.round(rect.x + rect.width / 2),
				y: Math.round(rect.y + rect.height / 2),
				disabled: el.disabled || false,
			});
		}
		return JSON.stringify(results);
	})()`, p.Query, p.Role, maxResults)

	result, err := evalJS(client, p.SessionID, js)
	if err != nil {
		return errorResult(err), nil
	}

	var matches []struct {
		Tag         string `json:"tag"`
		Role        string `json:"role"`
		Text        string `json:"text"`
		AriaLabel   string `json:"ariaLabel"`
		Placeholder string `json:"placeholder"`
		Type        string `json:"type"`
		X           int    `json:"x"`
		Y           int    `json:"y"`
		Disabled    bool   `json:"disabled"`
	}
	json.Unmarshal([]byte(result), &matches)

	if len(matches) == 0 {
		return textResult(fmt.Sprintf("No elements found matching %q", p.Query)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d elements matching %q:\n", len(matches), p.Query))
	for i, m := range matches {
		label := m.Text
		if label == "" {
			label = m.AriaLabel
		}
		if label == "" {
			label = m.Placeholder
		}
		status := ""
		if m.Disabled {
			status = " [disabled]"
		}
		sb.WriteString(fmt.Sprintf("  %d. <%s> %q at (%d,%d) role=%s%s\n", i+1, m.Tag, truncate(label, 60), m.X, m.Y, m.Role, status))
	}

	return textResult(sb.String()), nil
}

// handleVerify checks the state of an element after an action.
func handleVerify(client *juggler.Client, tracker *ContextTracker, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Check     string `json:"check"`    // "exists", "visible", "checked", "value", "text", "url", "title"
		Selector  string `json:"selector"` // CSS selector
		Expected  string `json:"expected"` // expected value for "value", "text", "url", "title" checks
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	switch p.Check {
	case "exists":
		js := fmt.Sprintf(`!!document.querySelector(%q)`, p.Selector)
		result, err := evalJS(client, p.SessionID, js)
		if err != nil {
			return errorResult(err), nil
		}
		if result == "true" {
			return textResult(fmt.Sprintf("PASS: element %q exists", p.Selector)), nil
		}
		return textResult(fmt.Sprintf("FAIL: element %q does not exist", p.Selector)), nil

	case "visible":
		js := fmt.Sprintf(`(() => {
			const el = document.querySelector(%q);
			if (!el) return "not_found";
			const rect = el.getBoundingClientRect();
			const style = window.getComputedStyle(el);
			return (rect.width > 0 && rect.height > 0 && style.display !== 'none' && style.visibility !== 'hidden') ? "visible" : "hidden";
		})()`, p.Selector)
		result, err := evalJS(client, p.SessionID, js)
		if err != nil {
			return errorResult(err), nil
		}
		if result == "visible" {
			return textResult(fmt.Sprintf("PASS: element %q is visible", p.Selector)), nil
		}
		return textResult(fmt.Sprintf("FAIL: element %q is %s", p.Selector, result)), nil

	case "checked":
		js := fmt.Sprintf(`(() => {
			const el = document.querySelector(%q);
			return el ? String(el.checked) : "not_found";
		})()`, p.Selector)
		result, err := evalJS(client, p.SessionID, js)
		if err != nil {
			return errorResult(err), nil
		}
		if result == "true" {
			return textResult(fmt.Sprintf("PASS: element %q is checked", p.Selector)), nil
		}
		return textResult(fmt.Sprintf("FAIL: element %q checked=%s", p.Selector, result)), nil

	case "value":
		js := fmt.Sprintf(`(() => {
			const el = document.querySelector(%q);
			return el ? el.value : "not_found";
		})()`, p.Selector)
		result, err := evalJS(client, p.SessionID, js)
		if err != nil {
			return errorResult(err), nil
		}
		if result == p.Expected {
			return textResult(fmt.Sprintf("PASS: element %q value is %q", p.Selector, p.Expected)), nil
		}
		return textResult(fmt.Sprintf("FAIL: element %q value is %q, expected %q", p.Selector, result, p.Expected)), nil

	case "text":
		js := fmt.Sprintf(`(() => {
			const el = document.querySelector(%q);
			return el ? el.textContent.trim() : "not_found";
		})()`, p.Selector)
		result, err := evalJS(client, p.SessionID, js)
		if err != nil {
			return errorResult(err), nil
		}
		if strings.Contains(result, p.Expected) {
			return textResult(fmt.Sprintf("PASS: element %q contains %q", p.Selector, p.Expected)), nil
		}
		return textResult(fmt.Sprintf("FAIL: element %q text is %q, expected to contain %q", p.Selector, truncate(result, 100), p.Expected)), nil

	case "url":
		js := `window.location.href`
		result, err := evalJS(client, p.SessionID, js)
		if err != nil {
			return errorResult(err), nil
		}
		if strings.Contains(result, p.Expected) {
			return textResult(fmt.Sprintf("PASS: URL contains %q", p.Expected)), nil
		}
		return textResult(fmt.Sprintf("FAIL: URL is %q, expected to contain %q", result, p.Expected)), nil

	case "title":
		js := `document.title`
		result, err := evalJS(client, p.SessionID, js)
		if err != nil {
			return errorResult(err), nil
		}
		if strings.Contains(result, p.Expected) {
			return textResult(fmt.Sprintf("PASS: title contains %q", p.Expected)), nil
		}
		return textResult(fmt.Sprintf("FAIL: title is %q, expected to contain %q", result, p.Expected)), nil

	default:
		return errorResult(fmt.Errorf("unknown check: %s (use: exists, visible, checked, value, text, url, title)", p.Check)), nil
	}
}

// handleScreenshotDiff takes a screenshot and compares it to a previous one.
func handleScreenshotDiff(client *juggler.Client, tracker *ScreenshotTracker, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Label     string `json:"label"` // label for this checkpoint (e.g. "before_click", "after_click")
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	// Take screenshot
	result, err := client.Call(p.SessionID, "Page.screenshot", map[string]interface{}{
		"mimeType": "image/png",
		"clip":     map[string]interface{}{"x": 0, "y": 0, "width": 1280, "height": 720},
	})
	if err != nil {
		return errorResult(err), nil
	}

	var screenshot struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(result, &screenshot); err != nil {
		return errorResult(err), nil
	}

	// Compare with previous screenshot for this session
	prev := tracker.Get(p.SessionID)
	tracker.Set(p.SessionID, screenshot.Data)

	if prev == "" {
		return textResult(fmt.Sprintf("Screenshot captured as %q (first checkpoint, no comparison)", p.Label)), nil
	}

	// Simple comparison: check if base64 data changed
	if prev == screenshot.Data {
		return textResult(fmt.Sprintf("SAME: screenshot %q is identical to previous — action may have had no effect", p.Label)), nil
	}

	// Calculate rough difference (base64 length difference as proxy)
	lenDiff := len(screenshot.Data) - len(prev)
	var diffDesc string
	if lenDiff > 1000 {
		diffDesc = "significant visual change"
	} else if lenDiff > 100 {
		diffDesc = "minor visual change"
	} else {
		diffDesc = "minimal visual change"
	}

	return textResult(fmt.Sprintf("CHANGED: screenshot %q shows %s from previous checkpoint", p.Label, diffDesc)), nil
}

// handlePageSettled waits until the page is fully loaded and stable.
// Uses two success conditions:
//   - Network idle: performance resource count stable for 600ms (works for SPAs)
//   - DOM stability: full state snapshot identical for 600ms (fast path for static pages)
// Accepts as soon as EITHER condition is met, preventing timeout on modern SPAs.
func handlePageSettled(client *juggler.Client, tracker *ContextTracker, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Timeout   int    `json:"timeout"` // seconds, default 30
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	timeout := 30
	if p.Timeout > 0 {
		timeout = p.Timeout
	}

	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	var lastSnapshot string
	var lastResourceCount string
	var networkIdleCount int
	var domStableCount int

	for time.Now().Before(deadline) {
		js := `(() => {
			let rc = 0;
			try { rc = performance.getEntriesByType('resource').length; } catch(e) {}
			return JSON.stringify({
				readyState: document.readyState,
				bodyLen: document.body ? document.body.innerHTML.length : 0,
				resourceCount: rc,
				url: window.location.href
			});
		})()`
		result, err := evalJS(client, p.SessionID, js)
		if err != nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}

		var state struct {
			ReadyState    string `json:"readyState"`
			BodyLen       int    `json:"bodyLen"`
			ResourceCount int    `json:"resourceCount"`
			URL           string `json:"url"`
		}
		json.Unmarshal([]byte(result), &state)

		// Don't proceed until readyState is complete
		if state.ReadyState != "complete" {
			lastSnapshot = ""
			lastResourceCount = ""
			networkIdleCount = 0
			domStableCount = 0
			time.Sleep(300 * time.Millisecond)
			continue
		}

		// Condition A: network idle — resource count unchanged for 2 polls
		rc := fmt.Sprintf("%d", state.ResourceCount)
		if rc == lastResourceCount {
			networkIdleCount++
		} else {
			networkIdleCount = 0
			lastResourceCount = rc
		}

		// Condition B: DOM stability — full snapshot unchanged for 2 polls
		if result == lastSnapshot {
			domStableCount++
		} else {
			domStableCount = 0
			lastSnapshot = result
		}

		if networkIdleCount >= 2 || domStableCount >= 2 {
			return textResult(fmt.Sprintf("Page settled: readyState=%s, bodyLen=%d, url=%s",
				state.ReadyState, state.BodyLen, state.URL)), nil
		}

		time.Sleep(300 * time.Millisecond)
	}

	return errorResult(fmt.Errorf("page did not settle within %ds", timeout)), nil
}

// handleSelectOption selects an option from a dropdown/select element.
func handleSelectOption(client *juggler.Client, tracker *ContextTracker, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Selector  string `json:"selector"` // CSS selector for the <select> element
		Value     string `json:"value"`    // option value to select
		Text      string `json:"text"`     // or option text to select
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	var js string
	if p.Value != "" {
		js = fmt.Sprintf(`(() => {
			const sel = document.querySelector(%q);
			if (!sel) return JSON.stringify({error: "select not found"});
			sel.value = %q;
			sel.dispatchEvent(new Event('change', {bubbles: true}));
			return JSON.stringify({selected: sel.value, text: sel.options[sel.selectedIndex]?.text});
		})()`, p.Selector, p.Value)
	} else if p.Text != "" {
		js = fmt.Sprintf(`(() => {
			const sel = document.querySelector(%q);
			if (!sel) return JSON.stringify({error: "select not found"});
			const opt = Array.from(sel.options).find(o => o.text.includes(%q));
			if (!opt) return JSON.stringify({error: "option not found: " + %q});
			sel.value = opt.value;
			sel.dispatchEvent(new Event('change', {bubbles: true}));
			return JSON.stringify({selected: sel.value, text: opt.text});
		})()`, p.Selector, p.Text, p.Text)
	} else {
		return errorResult(fmt.Errorf("either value or text is required")), nil
	}

	result, err := evalJS(client, p.SessionID, js)
	if err != nil {
		return errorResult(err), nil
	}

	var r struct {
		Error    string `json:"error"`
		Selected string `json:"selected"`
		Text     string `json:"text"`
	}
	json.Unmarshal([]byte(result), &r)
	if r.Error != "" {
		return errorResult(fmt.Errorf("%s", r.Error)), nil
	}

	return textResult(fmt.Sprintf("Selected option: value=%q text=%q", r.Selected, r.Text)), nil
}

// handleFillForm fills multiple form fields at once.
func handleFillForm(client *juggler.Client, tracker *ContextTracker, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string            `json:"sessionId"`
		Fields    map[string]string `json:"fields"` // selector → value
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	filled := 0
	var errors []string

	for selector, value := range p.Fields {
		js := fmt.Sprintf(`(() => {
			const el = document.querySelector(%q);
			if (!el) return "not_found";
			el.focus();
			el.value = %q;
			el.dispatchEvent(new Event('input', {bubbles: true}));
			el.dispatchEvent(new Event('change', {bubbles: true}));
			return "ok";
		})()`, selector, value)

		result, err := evalJS(client, p.SessionID, js)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", selector, err))
			continue
		}
		if result == "not_found" {
			errors = append(errors, fmt.Sprintf("%s: not found", selector))
			continue
		}
		filled++
	}

	msg := fmt.Sprintf("Filled %d/%d fields", filled, len(p.Fields))
	if len(errors) > 0 {
		msg += "\nErrors: " + strings.Join(errors, "; ")
	}
	return textResult(msg), nil
}

// handleGetPageInfo returns comprehensive page state for agent decision-making.
func handleGetPageInfo(client *juggler.Client, tracker *ContextTracker, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	js := `JSON.stringify({
		url: window.location.href,
		title: document.title,
		readyState: document.readyState,
		scrollY: Math.round(window.scrollY),
		scrollHeight: document.documentElement.scrollHeight,
		viewportHeight: window.innerHeight,
		canScrollDown: (window.scrollY + window.innerHeight) < document.documentElement.scrollHeight - 10,
		forms: document.forms.length,
		inputs: document.querySelectorAll('input, textarea, select').length,
		buttons: document.querySelectorAll('button, [role="button"], input[type="submit"]').length,
		links: document.querySelectorAll('a[href]').length,
		images: document.images.length,
		modals: document.querySelectorAll('[role="dialog"], [role="alertdialog"], .modal, .Modal, [aria-modal="true"]').length,
		focusedTag: document.activeElement ? document.activeElement.tagName.toLowerCase() : null,
		focusedType: document.activeElement ? document.activeElement.type : null,
		alerts: 0,
	})`

	result, err := evalJS(client, p.SessionID, js)
	if err != nil {
		return errorResult(err), nil
	}

	return textResult(result), nil
}

// handlePressKey dispatches keyboard events for special keys with optional modifiers.
func handlePressKey(client *juggler.Client, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Key       string `json:"key"`       // "Enter", "Tab", "Escape", "Backspace", "ArrowDown", etc.
		Modifiers string `json:"modifiers"` // "ctrl", "shift", "alt", "ctrl+shift", etc.
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	// Map key names to key codes
	keyMap := map[string]string{
		"Enter": "Enter", "Tab": "Tab", "Escape": "Escape",
		"Backspace": "Backspace", "Delete": "Delete",
		"ArrowUp": "ArrowUp", "ArrowDown": "ArrowDown",
		"ArrowLeft": "ArrowLeft", "ArrowRight": "ArrowRight",
		"Home": "Home", "End": "End",
		"PageUp": "PageUp", "PageDown": "PageDown",
		"Space": " ",
	}

	key := p.Key
	if mapped, ok := keyMap[p.Key]; ok {
		key = mapped
	}

	// Parse modifiers
	modifiers := 0
	if strings.Contains(p.Modifiers, "alt") {
		modifiers |= 1
	}
	if strings.Contains(p.Modifiers, "ctrl") {
		modifiers |= 2
	}
	if strings.Contains(p.Modifiers, "meta") {
		modifiers |= 4
	}
	if strings.Contains(p.Modifiers, "shift") {
		modifiers |= 8
	}

	if err := dispatchKeyEvent(client, p.SessionID, "keydown", key, modifiers, ""); err != nil {
		return errorResult(err), nil
	}

	if err := dispatchKeyEvent(client, p.SessionID, "keyup", key, modifiers, ""); err != nil {
		return errorResult(err), nil
	}

	desc := key
	if p.Modifiers != "" {
		desc = p.Modifiers + "+" + key
	}
	return textResult(fmt.Sprintf("Pressed %s", desc)), nil
}

// handleClearInput selects all text in the focused input and deletes it.
func handleClearInput(client *juggler.Client, tracker *ContextTracker, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Selector  string `json:"selector"` // optional CSS selector to focus first
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	if p.Selector != "" {
		js := fmt.Sprintf(`(() => {
			const el = document.querySelector(%q);
			if (!el || !('value' in el)) return "not_found";
			el.focus();
			el.value = "";
			el.dispatchEvent(new Event('input', {bubbles: true}));
			el.dispatchEvent(new Event('change', {bubbles: true}));
			return "ok";
		})()`, p.Selector)
		result, err := evalJS(client, p.SessionID, js)
		if err != nil {
			return errorResult(err), nil
		}
		if result == "not_found" {
			return errorResult(fmt.Errorf("element %q not found", p.Selector)), nil
		}
		return textResult("Input cleared"), nil
	}

	js := `(() => {
		const el = document.activeElement;
		if (!el || !('value' in el)) return "not_found";
		el.value = "";
		el.dispatchEvent(new Event('input', {bubbles: true}));
		el.dispatchEvent(new Event('change', {bubbles: true}));
		return "ok";
	})()`
	result, err := evalJS(client, p.SessionID, js)
	if err != nil {
		return errorResult(err), nil
	}
	if result == "not_found" {
		return errorResult(fmt.Errorf("no focused input to clear")), nil
	}

	return textResult("Input cleared"), nil
}

// handleGetFormErrors extracts form validation error messages from the page.
func handleGetFormErrors(client *juggler.Client, tracker *ContextTracker, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Selector  string `json:"selector"` // optional form selector, default "form"
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}

	sel := "form"
	if p.Selector != "" {
		sel = p.Selector
	}

	js := fmt.Sprintf(`(() => {
		const form = document.querySelector(%q);
		if (!form) return JSON.stringify({error: "form not found"});
		const errors = [];
		// Check HTML5 validation
		const inputs = form.querySelectorAll('input, select, textarea');
		for (const input of inputs) {
			if (!input.checkValidity()) {
				errors.push({field: input.name || input.id, message: input.validationMessage, type: "html5"});
			}
		}
		// Check common error class patterns
		const errorEls = form.querySelectorAll('.error, .invalid, .is-invalid, [aria-invalid="true"], .field-error, .form-error, .validation-error');
		for (const el of errorEls) {
			const text = el.textContent.trim();
			if (text) errors.push({field: "", message: text, type: "css"});
		}
		// Check aria-describedby error messages
		const described = form.querySelectorAll('[aria-describedby]');
		for (const el of described) {
			const descId = el.getAttribute('aria-describedby');
			const desc = document.getElementById(descId);
			if (desc && desc.textContent.trim()) {
				errors.push({field: el.name || el.id, message: desc.textContent.trim(), type: "aria"});
			}
		}
		return JSON.stringify({errors: errors, count: errors.length});
	})()`, sel)

	result, err := evalJS(client, p.SessionID, js)
	if err != nil {
		return errorResult(err), nil
	}
	return textResult(result), nil
}

// --- Helper functions ---

// evalJS evaluates JavaScript and returns the string result.
// Fully resolves the execution context when possible for determinism,
// but falls back to calling Runtime.evaluate without a context ID so
// that SPAs which create new targets on navigation (e.g. cross-origin
// redirects in Fission) don't permanently break tool loops.
func evalJS(client *juggler.Client, sessionID, expression string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("no browser connection")
	}

	result, err := client.Call(sessionID, "Runtime.evaluate", map[string]interface{}{
		"expression":    expression,
		"returnByValue": true,
	})
	if err != nil {
		return "", err
	}

	var evalResult struct {
		Result struct {
			Value interface{} `json:"value"`
		} `json:"result"`
		ExceptionDetails *json.RawMessage `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(result, &evalResult); err != nil {
		return "", err
	}
	if evalResult.ExceptionDetails != nil {
		return "", fmt.Errorf("JS exception: %s", string(*evalResult.ExceptionDetails))
	}

	switch v := evalResult.Result.Value.(type) {
	case string:
		return v, nil
	case float64:
		return fmt.Sprintf("%v", v), nil
	case bool:
		return fmt.Sprintf("%v", v), nil
	default:
		data, _ := json.Marshal(v)
		return string(data), nil
	}
}

type keyEventSpec struct {
	Key     string
	Code    string
	KeyCode int
	Text    string
}

func keySpecForKey(key string) keyEventSpec {
	specs := map[string]keyEventSpec{
		"Enter":      {Key: "Enter", Code: "Enter", KeyCode: 13},
		"Tab":        {Key: "Tab", Code: "Tab", KeyCode: 9},
		"Escape":     {Key: "Escape", Code: "Escape", KeyCode: 27},
		"Backspace":  {Key: "Backspace", Code: "Backspace", KeyCode: 8},
		"Delete":     {Key: "Delete", Code: "Delete", KeyCode: 46},
		"ArrowUp":    {Key: "ArrowUp", Code: "ArrowUp", KeyCode: 38},
		"ArrowDown":  {Key: "ArrowDown", Code: "ArrowDown", KeyCode: 40},
		"ArrowLeft":  {Key: "ArrowLeft", Code: "ArrowLeft", KeyCode: 37},
		"ArrowRight": {Key: "ArrowRight", Code: "ArrowRight", KeyCode: 39},
		"Home":       {Key: "Home", Code: "Home", KeyCode: 36},
		"End":        {Key: "End", Code: "End", KeyCode: 35},
		"PageUp":     {Key: "PageUp", Code: "PageUp", KeyCode: 33},
		"PageDown":   {Key: "PageDown", Code: "PageDown", KeyCode: 34},
		"Space":      {Key: " ", Code: "Space", KeyCode: 32, Text: " "},
	}
	if spec, ok := specs[key]; ok {
		return spec
	}
	if len(key) == 1 {
		ch := key[0]
		switch {
		case ch >= 'a' && ch <= 'z':
			return keyEventSpec{
				Key:     string(ch),
				Code:    "Key" + strings.ToUpper(string(ch)),
				KeyCode: int(ch - 32),
				Text:    string(ch),
			}
		case ch >= 'A' && ch <= 'Z':
			return keyEventSpec{
				Key:     string(ch),
				Code:    "Key" + string(ch),
				KeyCode: int(ch),
				Text:    string(ch),
			}
		case ch >= '0' && ch <= '9':
			return keyEventSpec{
				Key:     string(ch),
				Code:    "Digit" + string(ch),
				KeyCode: int(ch),
				Text:    string(ch),
			}
		}
	}
	return keyEventSpec{Key: key, Code: key, KeyCode: 0}
}

func dispatchKeyEvent(client *juggler.Client, sessionID, eventType, key string, modifiers int, text string) error {
	spec := keySpecForKey(key)
	params := map[string]interface{}{
		"type":      eventType,
		"key":       spec.Key,
		"code":      spec.Code,
		"keyCode":   spec.KeyCode,
		"location":  0,
		"repeat":    false,
		"modifiers": modifiers,
	}
	if eventType == "keydown" {
		if text != "" {
			params["text"] = text
		} else if spec.Text != "" && modifiers == 0 {
			params["text"] = spec.Text
		}
	}
	_, err := client.Call(sessionID, "Page.dispatchKeyEvent", params)
	return err
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// ScreenshotTracker stores the last screenshot per session for diff comparison.
type ScreenshotTracker struct {
	mu          sync.RWMutex
	screenshots map[string]string
}

func NewScreenshotTracker() *ScreenshotTracker {
	return &ScreenshotTracker{screenshots: make(map[string]string)}
}

func (t *ScreenshotTracker) Get(sessionID string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.screenshots[sessionID]
}

func (t *ScreenshotTracker) Set(sessionID, data string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.screenshots[sessionID] = data
}

func (t *ScreenshotTracker) Delete(sessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.screenshots, sessionID)
}
