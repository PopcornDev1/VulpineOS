package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"vulpineos/internal/extensions"
	"vulpineos/internal/extensions/extensionstest"
)

// ctxKey is a private test marker type used by
// TestHandleAutofillThreadsContext to verify that handleAutofill
// passes a real per-call context into the credential provider's Fill
// method instead of dropping it for context.Background().
type ctxKey struct{ name string }

// missingCredProvider is a CredentialProvider whose Lookup always
// returns (nil, nil). Used to exercise the "no match" branch of
// handleGetCredential, which must now return {"found":false}.
type missingCredProvider struct{}

func (missingCredProvider) Available() bool { return true }
func (missingCredProvider) Lookup(_ context.Context, _ string) (*extensions.Credential, error) {
	return nil, nil
}
func (missingCredProvider) Fill(_ context.Context, _ string, _ extensions.FillTarget) error {
	return nil
}
func (missingCredProvider) GenerateCode(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (missingCredProvider) List(_ context.Context) ([]extensions.Credential, error) {
	return nil, nil
}

func TestGetCredentialMissReturnsFoundFalse(t *testing.T) {
	original := extensions.Registry.Credentials()
	t.Cleanup(func() { extensions.Registry.SetCredentials(original) })
	extensions.Registry.SetCredentials(missingCredProvider{})

	res := runExtTool(t, "vulpine_get_credential", map[string]interface{}{
		"site_url": "https://no-such-site.example",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %+v", res)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if found, ok := parsed["found"].(bool); !ok || found {
		t.Errorf("expected {\"found\":false}, got %q", res.Content[0].Text)
	}
}

func TestCredentialToolDisplayRedactsSecretURLs(t *testing.T) {
	withFakeCredentials(t, &extensionstest.FakeCredentialProvider{
		AvailableFlag: true,
		Cred: extensions.Credential{
			ID:       "cred-1",
			Site:     "https://user:pass@example.com/login?token=site-token&view=ok",
			Username: "alice",
		},
	})

	res := runExtTool(t, "vulpine_get_credential", map[string]interface{}{
		"site_url": "https://example.com",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %+v", res)
	}
	body := res.Content[0].Text
	for _, leaked := range []string{"user:pass", "site-token"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("credential metadata leaked %q: %s", leaked, body)
		}
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	site, _ := parsed["site"].(string)
	if !strings.Contains(site, "redacted:redacted@example.com") || !strings.Contains(site, "token=%5Bredacted%5D") {
		t.Fatalf("site was not redacted as expected: %q", site)
	}
}

func TestAutofillMissRedactsLookupURL(t *testing.T) {
	original := extensions.Registry.Credentials()
	t.Cleanup(func() { extensions.Registry.SetCredentials(original) })
	extensions.Registry.SetCredentials(missingCredProvider{})

	res := runExtTool(t, "vulpine_autofill", map[string]interface{}{
		"site_url":          "https://user:pass@example.com/login?token=lookup-token&view=ok",
		"page_id":           "p1",
		"username_selector": "#user",
		"password_selector": "#pass",
	})
	if !res.IsError {
		t.Fatalf("expected missing credential error: %+v", res)
	}
	text := res.Content[0].Text
	for _, leaked := range []string{"user:pass", "lookup-token"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("autofill error leaked %q: %s", leaked, text)
		}
	}
	if !strings.Contains(text, "redacted:redacted@example.com") || !strings.Contains(text, "token=%5Bredacted%5D") {
		t.Fatalf("autofill error was not redacted as expected: %s", text)
	}
}

func TestHandleAutofillThreadsContext(t *testing.T) {
	marker := &ctxKey{name: "autofill-ctx"}
	var seen context.Context
	fake := withFakeCredentials(t, &extensionstest.FakeCredentialProvider{
		AvailableFlag: true,
		Cred: extensions.Credential{
			ID:       "cred-ctx",
			Site:     "https://example.com",
			Username: "alice",
		},
		FillFn: func(ctx context.Context, credID string, target extensions.FillTarget) error {
			if seen == nil {
				seen = ctx
			}
			return nil
		},
	})
	_ = fake

	ctx := context.WithValue(context.Background(), marker, "present")
	args, _ := json.Marshal(map[string]interface{}{
		"site_url":          "https://example.com",
		"page_id":           "p1",
		"frame_id":          "f1",
		"username_selector": "#user",
		"password_selector": "#pass",
	})
	res, ok := handleExtensionTool(ctx, nil, "vulpine_autofill", args)
	if !ok {
		t.Fatal("autofill not dispatched")
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if seen == nil {
		t.Fatal("Fill was never called")
	}
	if got, _ := seen.Value(marker).(string); got != "present" {
		t.Errorf("marker not visible inside Fill: got %q", got)
	}
}

// withFakeCredentials installs a fake credential provider for the
// duration of the test and returns the fake so assertions can inspect
// recorded calls.
func withFakeCredentials(t *testing.T, fake *extensionstest.FakeCredentialProvider) *extensionstest.FakeCredentialProvider {
	t.Helper()
	original := extensions.Registry.Credentials()
	t.Cleanup(func() { extensions.Registry.SetCredentials(original) })
	extensions.Registry.SetCredentials(fake)
	return fake
}

func withFakeAudio(t *testing.T, fake *extensionstest.FakeAudioCapturer) *extensionstest.FakeAudioCapturer {
	t.Helper()
	original := extensions.Registry.Audio()
	t.Cleanup(func() { extensions.Registry.SetAudio(original) })
	extensions.Registry.SetAudio(fake)
	return fake
}

func withFakeMobile(t *testing.T, fake *extensionstest.FakeMobileBridge) *extensionstest.FakeMobileBridge {
	t.Helper()
	original := extensions.Registry.Mobile()
	t.Cleanup(func() { extensions.Registry.SetMobile(original) })
	extensions.Registry.SetMobile(fake)
	return fake
}

func withFakeCaptcha(t *testing.T, fake *extensionstest.FakeCaptchaProvider) *extensionstest.FakeCaptchaProvider {
	t.Helper()
	original := extensions.Registry.Captcha()
	t.Cleanup(func() { extensions.Registry.SetCaptcha(original) })
	extensions.Registry.SetCaptcha(fake)
	return fake
}

func TestCaptchaToolsReturnSanitizedMetadata(t *testing.T) {
	fake := withFakeCaptcha(t, &extensionstest.FakeCaptchaProvider{
		AvailableFlag: true,
		Challenge: extensions.CaptchaChallenge{
			ID:                   "challenge-1",
			Vendor:               extensions.CaptchaVendorTurnstile,
			Type:                 "managed",
			Domain:               "example.com",
			SiteKey:              "site-key-public",
			RequiresConfirmation: true,
			PolicyDecision:       extensions.CaptchaPolicyAllow,
		},
		Solution: extensions.CaptchaSolution{
			ID:                "solution-1",
			ChallengeID:       "challenge-1",
			Provider:          "fake-solver",
			Status:            extensions.CaptchaSolutionSolved,
			Token:             "secret-solution-token",
			CostEstimateCents: 7,
			NeedsConfirmation: false,
		},
	})

	detect := runExtTool(t, "vulpine_captcha_detect", map[string]interface{}{
		"page_id": "page-1",
		"url":     "https://example.com/login?token=secret-url-token",
	})
	if detect.IsError {
		t.Fatalf("detect returned error: %+v", detect.Content)
	}
	detectText := detect.Content[0].Text
	if strings.Contains(detectText, "secret-url-token") {
		t.Fatalf("detect leaked URL secret: %s", detectText)
	}
	if !strings.Contains(detectText, `"challenge_id":"challenge-1"`) || !strings.Contains(detectText, `"vendor":"turnstile"`) {
		t.Fatalf("detect metadata missing expected fields: %s", detectText)
	}

	solve := runExtTool(t, "vulpine_captcha_solve", map[string]interface{}{
		"challenge_id":     "challenge-1",
		"allow_cost_cents": 10,
	})
	if solve.IsError {
		t.Fatalf("solve returned error: %+v", solve.Content)
	}
	solveText := solve.Content[0].Text
	if strings.Contains(solveText, "secret-solution-token") {
		t.Fatalf("solve leaked raw token: %s", solveText)
	}
	if !strings.Contains(solveText, `"solution_id":"solution-1"`) || !strings.Contains(solveText, `"cost_estimate_cents":7`) {
		t.Fatalf("solve metadata missing expected fields: %s", solveText)
	}

	apply := runExtTool(t, "vulpine_captcha_apply", map[string]interface{}{
		"challenge_id": "challenge-1",
		"solution_id":  "solution-1",
		"submit":       false,
	})
	if apply.IsError {
		t.Fatalf("apply returned error: %+v", apply.Content)
	}
	if !strings.Contains(apply.Content[0].Text, `"applied":true`) {
		t.Fatalf("apply result missing applied=true: %s", apply.Content[0].Text)
	}

	if got := fake.LastDetectRequest().PageID; got != "page-1" {
		t.Fatalf("detect page id = %q, want page-1", got)
	}
	if got := fake.LastSolveRequest().ChallengeID; got != "challenge-1" {
		t.Fatalf("solve challenge id = %q, want challenge-1", got)
	}
	if got := fake.LastApplyRequest().SolutionID; got != "solution-1" {
		t.Fatalf("apply solution id = %q, want solution-1", got)
	}
}

func TestGetCredentialReturnsCredJSON(t *testing.T) {
	withFakeCredentials(t, &extensionstest.FakeCredentialProvider{
		AvailableFlag: true,
		Cred: extensions.Credential{
			ID:       "cred-1",
			Site:     "https://example.com",
			Username: "alice",
			HasTOTP:  true,
			Notes:    "main",
		},
	})
	res := runExtTool(t, "vulpine_get_credential", map[string]interface{}{
		"site_url": "https://example.com",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %+v", res)
	}
	body := res.Content[0].Text
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed["id"] != "cred-1" {
		t.Errorf("id = %v, want cred-1", parsed["id"])
	}
	if parsed["username"] != "alice" {
		t.Errorf("username = %v, want alice", parsed["username"])
	}
	if parsed["hasTOTP"] != true {
		t.Errorf("hasTOTP = %v, want true", parsed["hasTOTP"])
	}
	// Password must never appear in the tool boundary.
	if strings.Contains(strings.ToLower(body), "password") {
		t.Errorf("credential JSON leaked password-like field: %q", body)
	}
}

func TestAutofillCallsFillTwice(t *testing.T) {
	fake := withFakeCredentials(t, &extensionstest.FakeCredentialProvider{
		AvailableFlag: true,
		Cred: extensions.Credential{
			ID:       "cred-1",
			Site:     "https://example.com",
			Username: "alice",
		},
	})
	res := runExtTool(t, "vulpine_autofill", map[string]interface{}{
		"site_url":          "https://example.com",
		"page_id":           "p1",
		"frame_id":          "f1",
		"username_selector": "#user",
		"password_selector": "#pass",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %+v", res)
	}
	calls := fake.RecordedFills()
	if len(calls) != 2 {
		t.Fatalf("expected 2 Fill calls, got %d", len(calls))
	}
	if calls[0].Target.Field != "username" || calls[0].Target.Selector != "#user" {
		t.Errorf("first call = %+v, want username/#user", calls[0])
	}
	if calls[1].Target.Field != "password" || calls[1].Target.Selector != "#pass" {
		t.Errorf("second call = %+v, want password/#pass", calls[1])
	}
	if calls[0].CredID != "cred-1" || calls[1].CredID != "cred-1" {
		t.Errorf("Fill called with wrong credID: %+v", calls)
	}
}

func TestStartAudioCaptureAppliesDefaults(t *testing.T) {
	fake := withFakeAudio(t, &extensionstest.FakeAudioCapturer{
		AvailableFlag: true,
		Handle:        extensions.CaptureHandle{ID: "h1", Format: "pcm"},
	})
	// Empty request — handler must apply format=pcm, sampleRate=16000,
	// channels=1 before calling Start.
	res := runExtTool(t, "vulpine_start_audio_capture", map[string]interface{}{})
	if res.IsError {
		t.Fatalf("unexpected error: %+v", res)
	}
	req := fake.LastStartRequest()
	if req.Format != "pcm" {
		t.Errorf("format = %q, want pcm", req.Format)
	}
	if req.SampleRate != 16000 {
		t.Errorf("sampleRate = %d, want 16000", req.SampleRate)
	}
	if req.Channels != 1 {
		t.Errorf("channels = %d, want 1", req.Channels)
	}
}

func TestListMobileDevicesReturnsList(t *testing.T) {
	withFakeMobile(t, &extensionstest.FakeMobileBridge{
		AvailableFlag: true,
		Devices: []extensions.MobileDevice{
			{UDID: "ABC123", Name: "Test Phone", Platform: "android", Model: "Pixel 8"},
		},
	})
	res := runExtTool(t, "vulpine_list_mobile_devices", map[string]interface{}{})
	if res.IsError {
		t.Fatalf("unexpected error: %+v", res)
	}
	body := res.Content[0].Text
	if !strings.Contains(body, "ABC123") {
		t.Errorf("expected body to contain UDID, got %q", body)
	}
	if !strings.Contains(body, "Pixel 8") {
		t.Errorf("expected body to contain model, got %q", body)
	}
}

func TestConnectMobileDeviceReturnsSession(t *testing.T) {
	withFakeMobile(t, &extensionstest.FakeMobileBridge{
		AvailableFlag: true,
		Session: extensions.MobileSession{
			ID:          "mobile-session-1",
			UDID:        "ABC123",
			CDPEndpoint: "http://127.0.0.1:9222",
			Protocol:    "cdp",
		},
	})
	res := runExtTool(t, "vulpine_connect_mobile_device", map[string]interface{}{
		"udid": "ABC123",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %+v", res)
	}
	body := res.Content[0].Text
	if !strings.Contains(body, "mobile-session-1") || !strings.Contains(body, "http://127.0.0.1:9222") {
		t.Errorf("expected body to contain session details, got %q", body)
	}
}

func TestDisconnectMobileDeviceReturnsOK(t *testing.T) {
	fake := withFakeMobile(t, &extensionstest.FakeMobileBridge{
		AvailableFlag: true,
	})
	res := runExtTool(t, "vulpine_disconnect_mobile_device", map[string]interface{}{
		"session_id": "mobile-session-1",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %+v", res)
	}
	if got := res.Content[0].Text; got != `{"ok":true}` {
		t.Fatalf("response = %q", got)
	}
	if len(fake.Disconnected) != 1 || fake.Disconnected[0] != "mobile-session-1" {
		t.Fatalf("disconnected = %+v", fake.Disconnected)
	}
}
