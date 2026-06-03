package orchestrator

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"vulpineos/internal/juggler"
	"vulpineos/internal/pool"
	"vulpineos/internal/testutil"
	"vulpineos/internal/vault"
)

func TestOrchestratorTracksContextOwnership(t *testing.T) {
	fake := testutil.NewFakeJugglerTransport(t)
	fake.RespondJSON("Browser.createBrowserContext", map[string]string{"browserContextId": "ctx-owned"})
	fake.RespondJSON("Browser.removeBrowserContext", map[string]any{})

	client := juggler.NewClient(fake)
	defer client.Close()

	vdb := openTestVault(t)
	defer vdb.Close()

	o := New(nil, client, vdb, pool.Config{PreWarm: 0, MaxActive: 1, MaxUsesPerSlot: 50}, "")
	o.Pool.Start()

	slot, err := o.Pool.Acquire()
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	o.agentToSlotMu.Lock()
	o.agentToSlot["agent-x"] = slot
	o.agentToSlotMu.Unlock()

	o.agentToSlotMu.Lock()
	tracked := o.agentToSlot["agent-x"]
	o.agentToSlotMu.Unlock()

	if tracked == nil {
		t.Fatal("context ownership not tracked")
	}
	if tracked.ContextID != "ctx-owned" {
		t.Fatalf("tracked contextID = %q, want ctx-owned", tracked.ContextID)
	}

	o.agentToSlotMu.Lock()
	delete(o.agentToSlot, "agent-x")
	o.agentToSlotMu.Unlock()
	o.Pool.Release(slot)
}

func TestOrchestratorReleasesContextOnAgentKill(t *testing.T) {
	fake := testutil.NewFakeJugglerTransport(t)
	fake.RespondJSON("Browser.createBrowserContext", map[string]string{"browserContextId": "ctx-kill"})
	fake.RespondJSON("Browser.removeBrowserContext", map[string]any{})

	client := juggler.NewClient(fake)
	defer client.Close()

	vdb := openTestVault(t)
	defer vdb.Close()

	o := New(nil, client, vdb, pool.Config{PreWarm: 0, MaxActive: 1, MaxUsesPerSlot: 1}, "", Opts{})
	o.Pool.Start()

	slot, err := o.Pool.Acquire()
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	o.agentToSlotMu.Lock()
	o.agentToSlot["agent-kill"] = slot
	o.agentToSlotMu.Unlock()

	o.agentToSlotMu.Lock()
	delete(o.agentToSlot, "agent-kill")
	o.agentToSlotMu.Unlock()
	o.Pool.Release(slot)

	calls := fake.CallsByMethod("Browser.removeBrowserContext")
	if len(calls) != 1 {
		t.Fatalf("remove calls = %d, want 1", len(calls))
	}

	removeParams := testutil.ParamsAs[struct {
		BrowserContextID string `json:"browserContextId"`
	}](t, calls[0].Params)
	if removeParams.BrowserContextID != "ctx-kill" {
		t.Fatalf("removed contextId = %q, want ctx-kill", removeParams.BrowserContextID)
	}
}

func TestOrchestratorCloseLeavesSharedClientUsable(t *testing.T) {
	fake := testutil.NewFakeJugglerTransport(t)
	fake.RespondJSON("Browser.createBrowserContext", map[string]string{"browserContextId": "ctx-close"})
	fake.RespondJSON("Browser.removeBrowserContext", map[string]any{})
	fake.RespondJSON("Browser.close", map[string]any{})

	client := juggler.NewClient(fake)
	defer client.Close()

	vdb := openTestVault(t)
	o := New(nil, client, vdb, pool.Config{PreWarm: 0, MaxActive: 1, MaxUsesPerSlot: 1}, "", Opts{})

	if _, err := o.Pool.Acquire(); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	o.Close()

	if _, err := client.Call("", "Browser.close", map[string]any{}); err != nil {
		t.Fatalf("shared client unusable after orchestrator close: %v", err)
	}
	if len(fake.CallsByMethod("Browser.removeBrowserContext")) != 1 {
		t.Fatalf("active pool context was not removed on close")
	}
}

func TestOrchestratorAppliesFingerprintViaJuggler(t *testing.T) {
	fake := testutil.NewFakeJugglerTransport(t)
	fake.RespondJSON("Browser.createBrowserContext", map[string]string{"browserContextId": "ctx-fp"})
	fake.RespondJSON("Browser.setCookies", map[string]any{})
	fake.RespondJSON("Browser.setUserAgentOverride", map[string]any{})
	fake.RespondJSON("Browser.setPlatformOverride", map[string]any{})
	fake.RespondJSON("Browser.setDefaultViewport", map[string]any{})
	fake.RespondJSON("Browser.setExtraHTTPHeaders", map[string]any{})
	fake.RespondJSON("Browser.setLocaleOverride", map[string]any{})
	fake.RespondJSON("Browser.setTimezoneOverride", map[string]any{})

	client := juggler.NewClient(fake)
	defer client.Close()

	vdb := openTestVault(t)
	defer vdb.Close()

	fp := vault.FingerprintData{
		Platform:     "MacIntel",
		UserAgent:    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)",
		ScreenWidth:  1920,
		ScreenHeight: 1080,
		Language:     "en-US",
		Languages:    []string{"en-US", "en"},
		Timezone:     "America/New_York",
		DeviceScale:  2,
	}
	fpJSON, _ := json.Marshal(fp)

	cit := &vault.Citizen{
		ID:          "citizen-fp",
		Fingerprint: string(fpJSON),
	}

	o := New(nil, client, vdb, pool.Config{PreWarm: 0, MaxActive: 1, MaxUsesPerSlot: 50}, "")

	if err := o.applyCitizenToContext("ctx-fp", 1, cit); err != nil {
		t.Fatalf("applyCitizenToContext: %v", err)
	}

	uaCalls := fake.CallsByMethod("Browser.setUserAgentOverride")
	if len(uaCalls) != 1 {
		t.Fatalf("setUserAgentOverride calls = %d, want 1", len(uaCalls))
	}
	uaParams := testutil.ParamsAs[struct {
		BrowserContextID string `json:"browserContextId"`
		UserAgent        string `json:"userAgent"`
	}](t, uaCalls[0].Params)
	if uaParams.UserAgent != fp.UserAgent {
		t.Fatalf("userAgent = %q, want %q", uaParams.UserAgent, fp.UserAgent)
	}

	platformCalls := fake.CallsByMethod("Browser.setPlatformOverride")
	if len(platformCalls) != 1 {
		t.Fatalf("setPlatformOverride calls = %d, want 1", len(platformCalls))
	}
	platformParams := testutil.ParamsAs[struct {
		Platform string `json:"platform"`
	}](t, platformCalls[0].Params)
	if platformParams.Platform != "MacIntel" {
		t.Fatalf("platform = %q, want MacIntel", platformParams.Platform)
	}

	viewportCalls := fake.CallsByMethod("Browser.setDefaultViewport")
	if len(viewportCalls) != 1 {
		t.Fatalf("setDefaultViewport calls = %d, want 1", len(viewportCalls))
	}
	viewportParams := testutil.ParamsAs[struct {
		BrowserContextID string `json:"browserContextId"`
		Viewport         struct {
			ViewportSize struct {
				Width  int `json:"width"`
				Height int `json:"height"`
			} `json:"viewportSize"`
			DeviceScaleFactor float64 `json:"deviceScaleFactor"`
		} `json:"viewport"`
	}](t, viewportCalls[0].Params)
	if viewportParams.BrowserContextID != "ctx-fp" {
		t.Fatalf("viewport context = %q, want ctx-fp", viewportParams.BrowserContextID)
	}
	if viewportParams.Viewport.ViewportSize.Width != 1920 || viewportParams.Viewport.ViewportSize.Height != 1080 {
		t.Fatalf("viewport size = %+v, want 1920x1080", viewportParams.Viewport.ViewportSize)
	}
	if viewportParams.Viewport.DeviceScaleFactor != 2 {
		t.Fatalf("deviceScaleFactor = %v, want 2", viewportParams.Viewport.DeviceScaleFactor)
	}

	localeCalls := fake.CallsByMethod("Browser.setLocaleOverride")
	if len(localeCalls) != 1 {
		t.Fatalf("setLocaleOverride calls = %d, want 1", len(localeCalls))
	}
	localeParams := testutil.ParamsAs[struct {
		Locale string `json:"locale"`
	}](t, localeCalls[0].Params)
	if localeParams.Locale != "en-US" {
		t.Fatalf("locale = %q, want en-US", localeParams.Locale)
	}

	timezoneCalls := fake.CallsByMethod("Browser.setTimezoneOverride")
	if len(timezoneCalls) != 1 {
		t.Fatalf("setTimezoneOverride calls = %d, want 1", len(timezoneCalls))
	}
	timezoneParams := testutil.ParamsAs[struct {
		TimezoneID string `json:"timezoneId"`
	}](t, timezoneCalls[0].Params)
	if timezoneParams.TimezoneID != "America/New_York" {
		t.Fatalf("timezoneId = %q, want America/New_York", timezoneParams.TimezoneID)
	}

	headerCalls := fake.CallsByMethod("Browser.setExtraHTTPHeaders")
	if len(headerCalls) != 1 {
		t.Fatalf("setExtraHTTPHeaders calls = %d, want 1", len(headerCalls))
	}
	headerParams := testutil.ParamsAs[struct {
		Headers []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
	}](t, headerCalls[0].Params)
	if len(headerParams.Headers) != 1 || headerParams.Headers[0].Name != "Accept-Language" || headerParams.Headers[0].Value != "en-US,en;q=0.9" {
		t.Fatalf("headers = %+v, want Accept-Language en-US,en;q=0.9", headerParams.Headers)
	}
}

func TestApplyFingerprintContinuesWhenContextFingerprintMethodUnsupported(t *testing.T) {
	fake := testutil.NewFakeJugglerTransport(t)
	fake.RespondError("Browser.setContextFingerprint", "ERROR: method 'Browser.setContextFingerprint' is not supported")

	client := juggler.NewClient(fake)
	defer client.Close()

	fp := vault.FingerprintData{
		Platform:      "MacIntel",
		UserAgent:     "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)",
		WebGLVendor:   "Apple",
		WebGLRenderer: "Apple M1",
	}
	fpJSON, _ := json.Marshal(fp)

	o := New(nil, client, nil, pool.Config{PreWarm: 0, MaxActive: 1, MaxUsesPerSlot: 50}, "")
	if err := o.applyFingerprintToContext("ctx-old-browser", 12, string(fpJSON), "", ""); err != nil {
		t.Fatalf("applyFingerprintToContext should not fail on unsupported Browser.setContextFingerprint: %v", err)
	}
	if got := len(fake.CallsByMethod("Browser.setContextFingerprint")); got != 1 {
		t.Fatalf("setContextFingerprint calls = %d, want 1", got)
	}
}

func TestOrchestratorSanitizesCamoufoxUserAgent(t *testing.T) {
	fake := testutil.NewFakeJugglerTransport(t)
	fake.RespondJSON("Browser.setCookies", map[string]any{})
	fake.RespondJSON("Browser.setUserAgentOverride", map[string]any{})

	client := juggler.NewClient(fake)
	defer client.Close()

	vdb := openTestVault(t)
	defer vdb.Close()

	cit := &vault.Citizen{
		ID: "citizen-camoufox",
		Fingerprint: `{
			"navigator.userAgent":"Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:146.0) Gecko/20100101 Camoufox/146.0.1-beta.25",
			"navigator.platform":"MacIntel",
			"screen.width":1440,
			"screen.height":900
		}`,
	}

	o := New(nil, client, vdb, pool.Config{PreWarm: 0, MaxActive: 1, MaxUsesPerSlot: 50}, "")
	if err := o.applyCitizenToContext("ctx-camoufox", 2, cit); err != nil {
		t.Fatalf("applyCitizenToContext: %v", err)
	}

	calls := fake.CallsByMethod("Browser.setUserAgentOverride")
	if len(calls) != 1 {
		t.Fatalf("setUserAgentOverride calls = %d, want 1", len(calls))
	}
	params := testutil.ParamsAs[struct {
		UserAgent string `json:"userAgent"`
	}](t, calls[0].Params)
	if strings.Contains(params.UserAgent, "Camoufox") {
		t.Fatalf("applied Camoufox-branded user agent: %s", params.UserAgent)
	}
	if !strings.Contains(params.UserAgent, "Firefox/146.0") {
		t.Fatalf("sanitized user agent = %q, want Firefox 146", params.UserAgent)
	}
}

func TestEnsureAgentBrowserContextCreatesPersistentScopedContext(t *testing.T) {
	fake := testutil.NewFakeJugglerTransport(t)
	fake.RespondJSON("Browser.createBrowserContext", map[string]string{"browserContextId": "ctx-agent"})
	fake.RespondJSON("Browser.removeBrowserContext", map[string]any{})
	fake.RespondJSON("Browser.setUserAgentOverride", map[string]any{})
	fake.RespondJSON("Browser.setPlatformOverride", map[string]any{})
	fake.RespondJSON("Browser.setDefaultViewport", map[string]any{})
	fake.RespondJSON("Browser.setLocaleOverride", map[string]any{})
	fake.RespondJSON("Browser.setTimezoneOverride", map[string]any{})
	fake.RespondJSON("Browser.setExtraHTTPHeaders", map[string]any{})

	client := juggler.NewClient(fake)
	defer client.Close()

	vdb := openTestVault(t)
	defer vdb.Close()

	fp, err := vault.GenerateFingerprint("agent-owned-context")
	if err != nil {
		t.Fatalf("GenerateFingerprint: %v", err)
	}
	agent, err := vdb.CreateAgent("agent-owned-context", "use browser", fp)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	o := New(nil, client, vdb, pool.Config{PreWarm: 0, MaxActive: 1, MaxUsesPerSlot: 50}, "")
	o.Pool.Start()
	defer o.Close()

	contextID, err := o.EnsureAgentBrowserContext(agent)
	if err != nil {
		t.Fatalf("EnsureAgentBrowserContext: %v", err)
	}
	if contextID != "ctx-agent" {
		t.Fatalf("contextID = %q, want ctx-agent", contextID)
	}
	persisted, err := vdb.GetAgent(agent.ID)
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	meta, err := vault.ParseAgentMetadata(persisted.Metadata)
	if err != nil {
		t.Fatalf("ParseAgentMetadata: %v", err)
	}
	if meta.ContextID != "ctx-agent" {
		t.Fatalf("persisted contextID = %q, want ctx-agent", meta.ContextID)
	}

	o.handleTerminalAgentStatus(agent.ID)
	if len(fake.CallsByMethod("Browser.removeBrowserContext")) != 0 {
		t.Fatal("persistent agent context was released after one completed turn")
	}

	o.ReleaseAgentContext(agent.ID)
	if len(fake.CallsByMethod("Browser.removeBrowserContext")) != 1 {
		t.Fatal("explicit agent context release did not remove browser context")
	}
}

func TestReleaseAgentContextRemovesPersistedUntrackedContext(t *testing.T) {
	fake := testutil.NewFakeJugglerTransport(t)
	fake.RespondJSON("Browser.removeBrowserContext", map[string]any{})

	client := juggler.NewClient(fake)
	defer client.Close()

	vdb := openTestVault(t)
	defer vdb.Close()

	agent, err := vdb.CreateAgent("persisted-context", "cleanup", "{}")
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := vdb.UpdateAgentMetadata(agent.ID, vault.MarshalAgentMetadata(vault.AgentMetadata{ContextID: "ctx-persisted"})); err != nil {
		t.Fatalf("UpdateAgentMetadata: %v", err)
	}

	o := New(nil, client, vdb, pool.Config{PreWarm: 0, MaxActive: 1, MaxUsesPerSlot: 50}, "")
	o.ReleaseAgentContext(agent.ID)

	calls := fake.CallsByMethod("Browser.removeBrowserContext")
	if len(calls) != 1 {
		t.Fatalf("remove calls = %d, want 1", len(calls))
	}
	params := testutil.ParamsAs[struct {
		BrowserContextID string `json:"browserContextId"`
	}](t, calls[0].Params)
	if params.BrowserContextID != "ctx-persisted" {
		t.Fatalf("removed context = %q, want ctx-persisted", params.BrowserContextID)
	}

	persisted, err := vdb.GetAgent(agent.ID)
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	meta, err := vault.ParseAgentMetadata(persisted.Metadata)
	if err != nil {
		t.Fatalf("ParseAgentMetadata: %v", err)
	}
	if meta.ContextID != "" {
		t.Fatalf("metadata contextID = %q, want cleared", meta.ContextID)
	}
}

func openTestVault(t *testing.T) *vault.DB {
	f, err := os.CreateTemp("", "orchestrator-contract-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	db, err := vault.OpenPath(f.Name())
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
