package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"vulpineos/internal/extensions"
	"vulpineos/internal/juggler"
)

var (
	mcpDisplayBearerPattern   = regexp.MustCompile(`(?i)(bearer\s+)[^\s,;"]+`)
	mcpDisplayQuerySecret     = regexp.MustCompile(`(?i)([?&](?:api[_-]?key|apikey|token|access[_-]?token|access[_-]?key|secret|password|credential|authorization|cookie|session)=)[^&#\s"]+`)
	mcpDisplayUserinfoPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s"@]+:[^/\s"@]+@`)
	mcpDisplayKeyValuePattern = regexp.MustCompile(`(?i)(^|[^?&A-Za-z0-9_])((?:api[_-]?key|apikey|token|access[_-]?token|access[_-]?key|secret|password|credential|authorization|cookie|session)\s*=\s*)[^\s,;"]+`)
)

// normalizeArgs replaces nil / "null" raw JSON with an empty object so
// json.Unmarshal into a struct pointer succeeds for callers that pass
// no arguments at all. Some MCP clients serialize absent tool args as
// a JSON null literal; others send nothing. Both should be treated as
// "empty object" by tolerant handlers.
func normalizeArgs(args json.RawMessage) json.RawMessage {
	if len(args) == 0 || bytes.Equal(args, []byte("null")) {
		return json.RawMessage("{}")
	}
	return args
}

// extensionTools returns the MCP tool definitions backed by the
// extensions package (credentials, audio capture, mobile bridge, and
// annotated screenshots). Each tool returns a graceful "feature
// unavailable" error when no provider is registered for the backing
// feature.
func extensionTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "vulpine_annotated_screenshot",
			Description: "Capture a screenshot of the current page and return it as base64 PNG. Future: will overlay element labels when an extension provider is available.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId":   {Type: "string", Description: "Target page session ID"},
					"format":      {Type: "string", Description: "Image format (default: png)"},
					"maxElements": {Type: "number", Description: "Max labeled elements to return (future use)"},
				},
				Required: []string{"sessionId"},
			},
		},
		{
			Name:        "vulpine_get_credential",
			Description: "Look up stored credential metadata for a given site URL. Returns metadata only (never the password). Returns 'credential provider unavailable' when no provider is registered.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"site_url": {Type: "string", Description: "Site URL to look up credentials for"},
				},
				Required: []string{"site_url"},
			},
		},
		{
			Name:        "vulpine_captcha_detect",
			Description: "Detect whether the current page has a challenge/captcha and return sanitized metadata only. Public builds report captcha provider unavailable unless an authorized provider is registered.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId":       {Type: "string", Description: "Target page session ID"},
					"page_id":         {Type: "string", Description: "Browser page identifier (legacy alias for sessionId)"},
					"frame_id":        {Type: "string", Description: "Optional frame identifier"},
					"url":             {Type: "string", Description: "Current page URL, redacted in tool output"},
					"vendor_hint":     {Type: "string", Description: "Optional challenge vendor hint: recaptcha, hcaptcha, turnstile, image, unknown"},
					"send_screenshot": {Type: "boolean", Description: "Whether policy allows sending screenshot evidence to the registered provider"},
				},
				Required: []string{"sessionId"},
			},
		},
		{
			Name:        "vulpine_captcha_solve",
			Description: "Request an authorized provider to solve a detected challenge. Returns only sanitized status/metadata; raw solution tokens never cross the tool boundary.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"challenge_id":     {Type: "string", Description: "Challenge ID returned by vulpine_captcha_detect"},
					"allow_cost_cents": {Type: "number", Description: "Maximum allowed provider cost in cents for this solve"},
					"auto_apply":       {Type: "boolean", Description: "Whether policy allows applying after solve"},
				},
				Required: []string{"challenge_id"},
			},
		},
		{
			Name:        "vulpine_captcha_apply",
			Description: "Apply a provider-owned challenge solution. Does not submit the surrounding form unless submit is true and policy/provider allows it.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sessionId":    {Type: "string", Description: "Target page session ID"},
					"challenge_id": {Type: "string", Description: "Challenge ID returned by vulpine_captcha_detect"},
					"solution_id":  {Type: "string", Description: "Solution ID returned by vulpine_captcha_solve"},
					"page_id":      {Type: "string", Description: "Browser page identifier (legacy alias for sessionId)"},
					"frame_id":     {Type: "string", Description: "Optional frame identifier"},
					"submit":       {Type: "boolean", Description: "Whether to submit the challenge/form after applying"},
				},
				Required: []string{"sessionId", "challenge_id", "solution_id"},
			},
		},
		{
			Name:        "vulpine_autofill",
			Description: "Look up a credential for site_url and ask the registered credential provider to fill the username and password fields. Plaintext never crosses the tool boundary.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"site_url":          {Type: "string", Description: "Site URL to look up credentials for"},
					"page_id":           {Type: "string", Description: "Browser page identifier"},
					"frame_id":          {Type: "string", Description: "Optional frame identifier"},
					"username_selector": {Type: "string", Description: "CSS selector for the username field"},
					"password_selector": {Type: "string", Description: "CSS selector for the password field"},
				},
				Required: []string{"site_url", "page_id", "username_selector", "password_selector"},
			},
		},
		{
			Name:        "vulpine_start_audio_capture",
			Description: "Start an audio capture session. Returns the capture handle ID. Requires a registered audio capture provider.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"format":      {Type: "string", Description: "Audio format (pcm, opus, wav)"},
					"sample_rate": {Type: "number", Description: "Sample rate in Hz (e.g. 48000)"},
					"channels":    {Type: "number", Description: "Channel count (1=mono, 2=stereo)"},
				},
			},
		},
		{
			Name:        "vulpine_stop_audio_capture",
			Description: "Stop an active audio capture session.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"handle_id": {Type: "string", Description: "Capture handle ID from vulpine_start_audio_capture"},
				},
				Required: []string{"handle_id"},
			},
		},
		{
			Name:        "vulpine_read_audio_chunk",
			Description: "Read a chunk of data from an active audio capture session. Returns base64-encoded bytes and an EOF flag.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"handle_id": {Type: "string", Description: "Capture handle ID"},
					"max_bytes": {Type: "number", Description: "Max bytes to read (default 65536)"},
				},
				Required: []string{"handle_id"},
			},
		},
		{
			Name:        "vulpine_click_label",
			Description: "Click an element previously labeled by vulpine_annotated_screenshot. The label is the `@N` identifier (or backend-supplied name) returned alongside the annotated PNG. Requires a prior annotated screenshot in the same session.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"session_id": {Type: "string", Description: "Target page session ID"},
					"label":      {Type: "string", Description: "Label identifier (e.g. @3)"},
				},
				Required: []string{"session_id", "label"},
			},
		},
		{
			Name:        "vulpine_list_mobile_devices",
			Description: "List mobile devices visible to the registered mobile bridge provider. Returns an empty list / unavailable error when no provider is registered.",
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]Property{},
			},
		},
		{
			Name:        "vulpine_connect_mobile_device",
			Description: "Start a local CDP bridge to a mobile device. Returns a session ID and CDP endpoint.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"udid": {Type: "string", Description: "Mobile device UDID / adb serial"},
				},
				Required: []string{"udid"},
			},
		},
		{
			Name:        "vulpine_disconnect_mobile_device",
			Description: "Stop a mobile bridge session created by vulpine_connect_mobile_device.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"session_id": {Type: "string", Description: "Mobile bridge session ID"},
				},
				Required: []string{"session_id"},
			},
		},
	}
}

// handleExtensionTool dispatches an extension-backed tool call. Returns
// (nil, false) if the name is not recognized so the caller can fall
// through to other handlers. The ctx argument is threaded through to
// every provider method so call-scoped deadlines, cancellation, and
// test markers propagate into extension backends.
func handleExtensionTool(ctx context.Context, client *juggler.Client, name string, args json.RawMessage) (*ToolCallResult, bool) {
	switch name {
	case "vulpine_annotated_screenshot":
		return handleAnnotatedScreenshot(ctx, client, args), true
	case "vulpine_get_credential":
		return handleGetCredential(ctx, args), true
	case "vulpine_captcha_detect":
		return handleCaptchaDetect(ctx, args), true
	case "vulpine_captcha_solve":
		return handleCaptchaSolve(ctx, args), true
	case "vulpine_captcha_apply":
		return handleCaptchaApply(ctx, args), true
	case "vulpine_autofill":
		return handleAutofill(ctx, client, args), true
	case "vulpine_start_audio_capture":
		return handleStartAudioCapture(ctx, args), true
	case "vulpine_stop_audio_capture":
		return handleStopAudioCapture(ctx, args), true
	case "vulpine_read_audio_chunk":
		return handleReadAudioChunk(ctx, args), true
	case "vulpine_list_mobile_devices":
		return handleListMobileDevices(ctx, args), true
	case "vulpine_connect_mobile_device":
		return handleConnectMobileDevice(ctx, args), true
	case "vulpine_disconnect_mobile_device":
		return handleDisconnectMobileDevice(ctx, args), true
	case "vulpine_click_label":
		return handleClickLabel(ctx, client, args), true
	}
	return nil, false
}

// handleAnnotatedScreenshot first tries the Juggler binding
// Page.getAnnotatedScreenshot, which returns the PNG plus a structured
// element list. On success the element list is stashed into the
// per-session label index so that vulpine_click_label can look up an
// objectID by label afterwards. On any failure (including "method not
// found" from a backend that doesn't implement the protocol yet) it
// falls back to a plain Page.screenshot so the tool degrades
// gracefully.
func handleAnnotatedScreenshot(ctx context.Context, client *juggler.Client, args json.RawMessage) *ToolCallResult {
	var p struct {
		SessionID   string `json:"sessionId"`
		Format      string `json:"format"`
		MaxElements int    `json:"maxElements"`
	}
	if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
		return errorResult(err)
	}
	if client == nil {
		return errorResult(fmt.Errorf("annotated screenshot: juggler client unavailable"))
	}

	// Attempt the real annotated screenshot binding first.
	if img, elements, err := client.GetAnnotatedScreenshot(ctx, p.SessionID, p.Format, p.MaxElements); err == nil {
		globalLabels.Set(p.SessionID, elements)
		b64 := base64.StdEncoding.EncodeToString(img)
		elementsJSON, _ := json.Marshal(elements)
		return &ToolCallResult{
			Content: []ContentBlock{
				{Type: "image", Data: b64, MimeType: "image/png"},
				{Type: "text", Text: string(elementsJSON)},
			},
		}
	}

	// Fallback: plain Page.screenshot.
	result, err := client.Call(p.SessionID, "Page.screenshot", map[string]interface{}{
		"mimeType": "image/png",
		"clip":     map[string]interface{}{"x": 0, "y": 0, "width": 1280, "height": 720},
	})
	if err != nil {
		return errorResult(err)
	}
	var shot struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(result, &shot); err != nil {
		return errorResult(err)
	}
	return &ToolCallResult{
		Content: []ContentBlock{{
			Type:     "image",
			Data:     shot.Data,
			MimeType: "image/png",
		}},
	}
}

// handleClickLabel looks up an objectID/frameID in the per-session label index
// and clicks it via the supported object-handle mouse path. Returns an error when the label is
// unknown (e.g. no annotated screenshot has been taken in this session
// yet) or when the underlying click call fails.
func handleClickLabel(ctx context.Context, client *juggler.Client, args json.RawMessage) *ToolCallResult {
	var p struct {
		SessionID string `json:"session_id"`
		Label     string `json:"label"`
	}
	if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
		return errorResult(err)
	}
	if client == nil {
		return errorResult(fmt.Errorf("click_label: juggler client unavailable"))
	}
	if p.SessionID == "" || p.Label == "" {
		return errorResult(fmt.Errorf("click_label: session_id and label are required"))
	}
	target, ok := globalLabels.Get(p.SessionID, p.Label)
	if !ok {
		return errorResult(fmt.Errorf("click_label: label %q not found for session %q (take an annotated screenshot first)", p.Label, p.SessionID))
	}
	if target.ObjectID == "" || target.FrameID == "" {
		return errorResult(fmt.Errorf("click_label: label %q is missing frame/object metadata", p.Label))
	}
	if err := client.ClickByObjectID(ctx, p.SessionID, target.FrameID, target.ObjectID); err != nil {
		return errorResult(fmt.Errorf("click_label: click object %q: %w", target.ObjectID, err))
	}
	return textResult(fmt.Sprintf("clicked label %s (objectId=%s frameId=%s)", p.Label, target.ObjectID, target.FrameID))
}

func handleCaptchaDetect(ctx context.Context, args json.RawMessage) *ToolCallResult {
	var p struct {
		SessionID      string `json:"sessionId"`
		PageID         string `json:"page_id"`
		FrameID        string `json:"frame_id"`
		URL            string `json:"url"`
		VendorHint     string `json:"vendor_hint"`
		SendScreenshot bool   `json:"send_screenshot"`
	}
	if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
		return errorResult(err)
	}
	provider := extensions.Registry.Captcha()
	if provider == nil || !provider.Available() {
		return errorResult(fmt.Errorf("captcha provider unavailable"))
	}
	pageID := p.SessionID
	if pageID == "" {
		pageID = p.PageID
	}
	challenge, err := provider.Detect(ctx, extensions.CaptchaDetectRequest{
		PageID:         pageID,
		FrameID:        p.FrameID,
		URL:            p.URL,
		VendorHint:     p.VendorHint,
		SendScreenshot: p.SendScreenshot,
	})
	if err != nil {
		return errorResult(fmt.Errorf("%s", redactMCPDisplayText(err.Error())))
	}
	if challenge == nil || challenge.ID == "" {
		b, _ := json.Marshal(map[string]interface{}{"found": false})
		return textResult(string(b))
	}
	body := map[string]interface{}{
		"found":                 true,
		"challenge_id":          challenge.ID,
		"vendor":                challenge.Vendor,
		"type":                  challenge.Type,
		"domain":                challenge.Domain,
		"site_key":              challenge.SiteKey,
		"action":                challenge.Action,
		"requires_confirmation": challenge.RequiresConfirmation,
		"policy_decision":       challenge.PolicyDecision,
		"url":                   redactMCPURLForDisplay(p.URL),
	}
	if !challenge.DetectedAt.IsZero() {
		body["detected_at"] = challenge.DetectedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	b, _ := json.Marshal(body)
	return textResult(string(b))
}

func handleCaptchaSolve(ctx context.Context, args json.RawMessage) *ToolCallResult {
	var p struct {
		ChallengeID    string `json:"challenge_id"`
		AllowCostCents int    `json:"allow_cost_cents"`
		AutoApply      bool   `json:"auto_apply"`
	}
	if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
		return errorResult(err)
	}
	provider := extensions.Registry.Captcha()
	if provider == nil || !provider.Available() {
		return errorResult(fmt.Errorf("captcha provider unavailable"))
	}
	solution, err := provider.Solve(ctx, extensions.CaptchaSolveRequest{
		ChallengeID:    p.ChallengeID,
		AllowCostCents: p.AllowCostCents,
		AutoApply:      p.AutoApply,
	})
	if err != nil {
		return errorResult(fmt.Errorf("%s", redactMCPDisplayText(err.Error())))
	}
	if solution == nil || solution.ID == "" {
		return errorResult(fmt.Errorf("captcha solve returned no solution"))
	}
	body := map[string]interface{}{
		"solution_id":         solution.ID,
		"challenge_id":        solution.ChallengeID,
		"provider":            solution.Provider,
		"status":              solution.Status,
		"instructions":        redactMCPDisplayText(solution.Instructions),
		"cost_estimate_cents": solution.CostEstimateCents,
		"needs_confirmation":  solution.NeedsConfirmation,
	}
	if !solution.ExpiresAt.IsZero() {
		body["expires_at"] = solution.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")
	}
	b, _ := json.Marshal(body)
	return textResult(string(b))
}

func handleCaptchaApply(ctx context.Context, args json.RawMessage) *ToolCallResult {
	var p struct {
		SessionID   string `json:"sessionId"`
		ChallengeID string `json:"challenge_id"`
		SolutionID  string `json:"solution_id"`
		PageID      string `json:"page_id"`
		FrameID     string `json:"frame_id"`
		Submit      bool   `json:"submit"`
	}
	if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
		return errorResult(err)
	}
	provider := extensions.Registry.Captcha()
	if provider == nil || !provider.Available() {
		return errorResult(fmt.Errorf("captcha provider unavailable"))
	}
	pageID := p.SessionID
	if pageID == "" {
		pageID = p.PageID
	}
	if err := provider.Apply(ctx, extensions.CaptchaApplyRequest{
		ChallengeID: p.ChallengeID,
		SolutionID:  p.SolutionID,
		PageID:      pageID,
		FrameID:     p.FrameID,
		Submit:      p.Submit,
	}); err != nil {
		return errorResult(fmt.Errorf("%s", redactMCPDisplayText(err.Error())))
	}
	b, _ := json.Marshal(map[string]interface{}{
		"applied":      true,
		"challenge_id": p.ChallengeID,
		"solution_id":  p.SolutionID,
		"submitted":    p.Submit,
	})
	return textResult(string(b))
}

func handleGetCredential(ctx context.Context, args json.RawMessage) *ToolCallResult {
	var p struct {
		SiteURL string `json:"site_url"`
	}
	if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
		return errorResult(err)
	}
	provider := extensions.Registry.Credentials()
	if provider == nil || !provider.Available() {
		return errorResult(fmt.Errorf("credential provider unavailable"))
	}
	cred, err := provider.Lookup(ctx, p.SiteURL)
	if err != nil {
		return errorResult(fmt.Errorf("%s", redactMCPDisplayText(err.Error())))
	}
	if cred == nil {
		b, _ := json.Marshal(map[string]interface{}{"found": false})
		return textResult(string(b))
	}
	meta := map[string]interface{}{
		"found":    true,
		"id":       cred.ID,
		"site":     redactMCPURLForDisplay(cred.Site),
		"username": cred.Username,
		"hasTOTP":  cred.HasTOTP,
		"notes":    cred.Notes,
	}
	b, _ := json.Marshal(meta)
	return textResult(string(b))
}

func handleAutofill(ctx context.Context, client *juggler.Client, args json.RawMessage) *ToolCallResult {
	_ = client // reserved for selector-value fallback path
	var p struct {
		SiteURL          string `json:"site_url"`
		PageID           string `json:"page_id"`
		FrameID          string `json:"frame_id"`
		UsernameSelector string `json:"username_selector"`
		PasswordSelector string `json:"password_selector"`
	}
	if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
		return errorResult(err)
	}
	provider := extensions.Registry.Credentials()
	if provider == nil || !provider.Available() {
		return errorResult(fmt.Errorf("credential provider unavailable"))
	}
	cred, err := provider.Lookup(ctx, p.SiteURL)
	if err != nil {
		return errorResult(fmt.Errorf("%s", redactMCPDisplayText(err.Error())))
	}
	if cred == nil {
		return errorResult(fmt.Errorf("no credential found for %s", redactMCPURLForDisplay(p.SiteURL)))
	}
	if err := provider.Fill(ctx, cred.ID, extensions.FillTarget{
		PageID:   p.PageID,
		FrameID:  p.FrameID,
		Selector: p.UsernameSelector,
		Field:    "username",
	}); err != nil {
		return errorResult(fmt.Errorf("fill username: %s", redactMCPDisplayText(err.Error())))
	}
	if err := provider.Fill(ctx, cred.ID, extensions.FillTarget{
		PageID:   p.PageID,
		FrameID:  p.FrameID,
		Selector: p.PasswordSelector,
		Field:    "password",
	}); err != nil {
		return errorResult(fmt.Errorf("fill password: %s", redactMCPDisplayText(err.Error())))
	}
	return textResult(fmt.Sprintf("autofilled credential %s", cred.ID))
}

func redactMCPURLForDisplay(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return redactMCPDisplayText(raw)
	}
	if parsed.User != nil {
		parsed.User = url.UserPassword("redacted", "redacted")
	}
	query := parsed.Query()
	redacted := false
	for key := range query {
		if sensitiveMCPDisplayKey(key) {
			query.Set(key, "[redacted]")
			redacted = true
		}
	}
	if redacted {
		parsed.RawQuery = query.Encode()
	}
	return parsed.String()
}

func redactMCPDisplayText(value string) string {
	value = mcpDisplayUserinfoPattern.ReplaceAllString(value, "${1}redacted:redacted@")
	value = mcpDisplayBearerPattern.ReplaceAllString(value, "${1}[redacted]")
	value = mcpDisplayQuerySecret.ReplaceAllString(value, "${1}[redacted]")
	value = mcpDisplayKeyValuePattern.ReplaceAllString(value, "${1}${2}[redacted]")
	return value
}

func sensitiveMCPDisplayKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"))
	for _, marker := range []string{"api_key", "apikey", "token", "secret", "password", "credential", "authorization", "cookie", "session"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func handleStartAudioCapture(ctx context.Context, args json.RawMessage) *ToolCallResult {
	var p struct {
		Format     string `json:"format"`
		SampleRate int    `json:"sample_rate"`
		Channels   int    `json:"channels"`
	}
	if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
		return errorResult(err)
	}
	// Apply defaults for unset fields. 16kHz mono PCM is a reasonable
	// baseline for speech capture and keeps backend implementations
	// from having to special-case zero values.
	if p.Format == "" {
		p.Format = "pcm"
	}
	if p.SampleRate == 0 {
		p.SampleRate = 16000
	}
	if p.Channels == 0 {
		p.Channels = 1
	}
	cap := extensions.Registry.Audio()
	if cap == nil || !cap.Available() {
		return errorResult(fmt.Errorf("audio capture unavailable"))
	}
	handle, err := cap.Start(ctx, extensions.CaptureRequest{
		Format:     p.Format,
		SampleRate: p.SampleRate,
		Channels:   p.Channels,
	})
	if err != nil {
		return errorResult(err)
	}
	payload, err := json.Marshal(map[string]interface{}{
		"handle_id": handle.ID,
		"format":    handle.Format,
	})
	if err != nil {
		return errorResult(fmt.Errorf("marshal start audio response: %w", err))
	}
	return textResult(string(payload))
}

func handleStopAudioCapture(ctx context.Context, args json.RawMessage) *ToolCallResult {
	var p struct {
		HandleID string `json:"handle_id"`
	}
	if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
		return errorResult(err)
	}
	cap := extensions.Registry.Audio()
	if cap == nil || !cap.Available() {
		return errorResult(fmt.Errorf("audio capture unavailable"))
	}
	if err := cap.Stop(ctx, p.HandleID); err != nil {
		return errorResult(err)
	}
	return textResult("stopped")
}

func handleReadAudioChunk(ctx context.Context, args json.RawMessage) *ToolCallResult {
	var p struct {
		HandleID string `json:"handle_id"`
		MaxBytes int    `json:"max_bytes"`
	}
	if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
		return errorResult(err)
	}
	if p.MaxBytes <= 0 {
		p.MaxBytes = 65536
	}
	cap := extensions.Registry.Audio()
	if cap == nil || !cap.Available() {
		return errorResult(fmt.Errorf("audio capture unavailable"))
	}
	chunk, eof, err := cap.Read(ctx, p.HandleID, p.MaxBytes)
	if err != nil {
		return errorResult(err)
	}
	payload := map[string]interface{}{
		"data": base64.StdEncoding.EncodeToString(chunk),
		"eof":  eof,
	}
	b, _ := json.Marshal(payload)
	return textResult(string(b))
}

func handleListMobileDevices(ctx context.Context, args json.RawMessage) *ToolCallResult {
	_ = args
	m := extensions.Registry.Mobile()
	if m == nil || !m.Available() {
		return errorResult(fmt.Errorf("mobile bridge unavailable"))
	}
	devices, err := m.ListDevices(ctx)
	if err != nil {
		return errorResult(err)
	}
	b, _ := json.Marshal(devices)
	return textResult(string(b))
}

func handleConnectMobileDevice(ctx context.Context, args json.RawMessage) *ToolCallResult {
	var p struct {
		UDID string `json:"udid"`
	}
	if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
		return errorResult(err)
	}
	m := extensions.Registry.Mobile()
	if m == nil || !m.Available() {
		return errorResult(fmt.Errorf("mobile bridge unavailable"))
	}
	session, err := m.Connect(ctx, p.UDID)
	if err != nil {
		return errorResult(err)
	}
	b, _ := json.Marshal(session)
	return textResult(string(b))
}

func handleDisconnectMobileDevice(ctx context.Context, args json.RawMessage) *ToolCallResult {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
		return errorResult(err)
	}
	m := extensions.Registry.Mobile()
	if m == nil || !m.Available() {
		return errorResult(fmt.Errorf("mobile bridge unavailable"))
	}
	if err := m.Disconnect(ctx, p.SessionID); err != nil {
		return errorResult(err)
	}
	return textResult(`{"ok":true}`)
}
