package orchestrator

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"

	"vulpineos/internal/agentbus"
	"vulpineos/internal/config"
	"vulpineos/internal/costtrack"
	"vulpineos/internal/foxbridge"
	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
	"vulpineos/internal/nanoclaw"
	"vulpineos/internal/pagecache"
	"vulpineos/internal/pool"
	"vulpineos/internal/proxy"
	"vulpineos/internal/recording"
	"vulpineos/internal/security"
	"vulpineos/internal/vault"
	"vulpineos/internal/webhooks"

	"github.com/VulpineOS/vulpine-networklab"
)

// Status describes the orchestrator's current state.
type Status struct {
	KernelRunning  bool    `json:"kernel_running"`
	KernelPID      int     `json:"kernel_pid"`
	PoolAvailable  int     `json:"pool_available"`
	PoolActive     int     `json:"pool_active"`
	PoolTotal      int     `json:"pool_total"`
	ActiveAgents   int     `json:"active_agents"`
	TotalCitizens  int     `json:"total_citizens"`
	TotalTemplates int     `json:"total_templates"`
	TotalCostUSD   float64 `json:"total_cost_usd,omitempty"`
}

// AgentResult is returned when an agent completes.
type AgentResult struct {
	AgentID string
	Status  string
	Result  string
	Err     error
}

// Orchestrator ties together kernel, pool, vault, and NanoClaw manager.
type Orchestrator struct {
	Kernel *kernel.Kernel
	Client *juggler.Client
	Pool   *pool.Pool
	Vault  *vault.DB
	Agents *nanoclaw.Manager

	// Optional subsystems (nil-safe)
	AgentBus        *agentbus.Bus
	Costs           *costtrack.Tracker
	Webhooks        *webhooks.Manager
	Recording       *recording.Recorder
	PageCache       *pagecache.Cache
	SecurityEnabled bool // when true, inject CSP and sandbox protections on context creation
	memMonitor      *pool.MemoryMonitor
	mutationObs     *security.MutationMonitor

	// Track which agent owns which context slot
	agentToSlot          map[string]*pool.ContextSlot
	persistentAgentSlots map[string]bool
	agentToSlotMu        sync.Mutex

	// Central per-context network-identity registry keyed by userContextId.
	// The full set is flushed to the networklab shmem as a ctx-<id> map so the
	// NSS NetworkIdentityManager applies the right TLS identity per context.
	netIdentities   map[uint32]*networklab.Identity
	netIdentitiesMu sync.Mutex
}

// Opts holds optional subsystem dependencies for the orchestrator.
type Opts struct {
	AgentBus        *agentbus.Bus
	Costs           *costtrack.Tracker
	Webhooks        *webhooks.Manager
	Recording       *recording.Recorder
	PageCache       *pagecache.Cache
	SecurityEnabled bool
	MemoryMonitor   *pool.MemoryMonitor
	MutationMonitor *security.MutationMonitor
}

// New creates an orchestrator with all subsystems.
func New(k *kernel.Kernel, client *juggler.Client, v *vault.DB, poolCfg pool.Config, nanoclawBinary string, opts ...Opts) *Orchestrator {
	o := &Orchestrator{
		Kernel:               k,
		Client:               client,
		Pool:                 pool.New(client, poolCfg),
		Vault:                v,
		Agents:               nanoclaw.NewManager(nanoclawBinary),
		agentToSlot:          make(map[string]*pool.ContextSlot),
		persistentAgentSlots: make(map[string]bool),
	}
	if len(opts) > 0 {
		o.AgentBus = opts[0].AgentBus
		o.Costs = opts[0].Costs
		o.Webhooks = opts[0].Webhooks
		o.Recording = opts[0].Recording
		o.PageCache = opts[0].PageCache
		o.SecurityEnabled = opts[0].SecurityEnabled
		o.memMonitor = opts[0].MemoryMonitor
		o.mutationObs = opts[0].MutationMonitor
	}
	return o
}

// Start initializes the pool and begins the agent status relay.
func (o *Orchestrator) Start() error {
	if err := o.Pool.Start(); err != nil {
		return fmt.Errorf("start pool: %w", err)
	}
	go o.statusRelay()
	return nil
}

// SpawnCitizen creates an agent bound to a long-lived citizen identity.
func (o *Orchestrator) SpawnCitizen(citizenID, templateID string) (string, error) {
	// Load citizen
	citizen, err := o.Vault.GetCitizen(citizenID)
	if err != nil {
		return "", fmt.Errorf("load citizen: %w", err)
	}

	// Load template
	tmpl, err := o.Vault.GetTemplate(templateID)
	if err != nil {
		return "", fmt.Errorf("load template: %w", err)
	}

	// Acquire context
	slot, err := o.Pool.Acquire()
	if err != nil {
		return "", fmt.Errorf("acquire context: %w", err)
	}

	// Apply citizen identity to context
	if err := o.applyCitizenToContext(slot.ContextID, slot.UserContextID, citizen); err != nil {
		o.Pool.Release(slot)
		return "", fmt.Errorf("apply citizen: %w", err)
	}

	// Write SOP and spawn agent
	sopFile, err := nanoclaw.WriteSOP(tmpl.SOP)
	if err != nil {
		o.Pool.Release(slot)
		return "", fmt.Errorf("write SOP: %w", err)
	}

	agentID, err := o.spawnScopedAgent(slot.ContextID, sopFile)
	if err != nil {
		nanoclaw.CleanupSOP(sopFile)
		o.Pool.Release(slot)
		return "", fmt.Errorf("spawn agent: %w", err)
	}

	o.agentToSlotMu.Lock()
	o.agentToSlot[agentID] = slot
	o.agentToSlotMu.Unlock()
	o.Vault.UpdateCitizenUsage(citizenID)

	// Start recording for this agent
	if o.Recording != nil {
		o.Recording.Record(agentID, recording.ActionNavigate, nil)
	}

	log.Printf("orchestrator: spawned citizen agent %s (citizen=%s, context=%s)", agentID, citizen.Label, slot.ContextID)
	return agentID, nil
}

// SpawnNomad creates an ephemeral agent with auto-generated identity.
func (o *Orchestrator) SpawnNomad(templateID string) (string, error) {
	// Load template
	tmpl, err := o.Vault.GetTemplate(templateID)
	if err != nil {
		return "", fmt.Errorf("load template: %w", err)
	}

	// Acquire context
	slot, err := o.Pool.Acquire()
	if err != nil {
		return "", fmt.Errorf("acquire context: %w", err)
	}

	// Record nomad session
	session, err := o.Vault.CreateNomadSession(templateID, "{}")
	if err != nil {
		o.Pool.Release(slot)
		return "", fmt.Errorf("create nomad session: %w", err)
	}

	// Apply security protections for nomad contexts
	if o.SecurityEnabled {
		o.applySecurityToContext(slot.ContextID, session.ID)
	}

	// Apply default network identity based on host OS
	o.applyDefaultNetworkIdentity(slot.ContextID, slot.UserContextID, session.ID)

	// Write SOP and spawn agent
	sopFile, err := nanoclaw.WriteSOP(tmpl.SOP)
	if err != nil {
		o.Pool.Release(slot)
		return "", fmt.Errorf("write SOP: %w", err)
	}

	agentID, err := o.spawnScopedAgent(slot.ContextID, sopFile)
	if err != nil {
		nanoclaw.CleanupSOP(sopFile)
		o.Pool.Release(slot)
		return "", fmt.Errorf("spawn agent: %w", err)
	}

	o.agentToSlotMu.Lock()
	o.agentToSlot[agentID] = slot
	o.agentToSlotMu.Unlock()

	// Start recording for this agent
	if o.Recording != nil {
		o.Recording.Record(agentID, recording.ActionNavigate, nil)
	}

	log.Printf("orchestrator: spawned nomad agent %s (session=%s, context=%s)", agentID, session.ID, slot.ContextID)
	return agentID, nil
}

// KillAgent stops an agent and releases its context.
func (o *Orchestrator) KillAgent(agentID string) error {
	if err := o.Agents.Kill(agentID); err != nil {
		if !strings.Contains(err.Error(), "not found") {
			return err
		}
	}

	// Stop recording for this agent
	if o.Recording != nil {
		o.Recording.Clear(agentID)
	}

	// Save page cache state
	if o.PageCache != nil {
		o.PageCache.Save(&pagecache.PageState{AgentID: agentID})
	}

	// Fire webhook notification
	if o.Webhooks != nil {
		o.Webhooks.Fire(webhooks.AgentInterrupted, map[string]interface{}{
			"agentId": agentID,
		})
	}

	o.ReleaseAgentContext(agentID)
	return nil
}

// EnsureAgentBrowserContext gives a persistent chat agent a browser context and
// applies its current fingerprint before NanoClaw is routed into that context.
func (o *Orchestrator) EnsureAgentBrowserContext(agent *vault.Agent) (string, error) {
	if o == nil || o.Pool == nil || o.Client == nil {
		return "", fmt.Errorf("orchestrator browser context pool not available")
	}
	if agent == nil {
		return "", fmt.Errorf("agent not found")
	}

	meta, err := vault.ParseAgentMetadata(agent.Metadata)
	if err != nil {
		return "", fmt.Errorf("parse agent metadata: %w", err)
	}
	if meta.ContextID != "" {
		if err := o.applyAgentToContext(meta.ContextID, meta.UserContextID, agent); err == nil {
			o.agentToSlotMu.Lock()
			o.persistentAgentSlots[agent.ID] = true
			o.agentToSlotMu.Unlock()
			return meta.ContextID, nil
		} else {
			log.Printf("orchestrator: existing context %s for agent %s is unavailable: %v", meta.ContextID, agent.ID, err)
		}
	}

	slot, err := o.Pool.Acquire()
	if err != nil {
		return "", fmt.Errorf("acquire agent context: %w", err)
	}
	if err := o.applyAgentToContext(slot.ContextID, slot.UserContextID, agent); err != nil {
		o.Pool.Discard(slot)
		return "", fmt.Errorf("apply agent identity: %w", err)
	}

	meta.ContextID = slot.ContextID
	meta.UserContextID = slot.UserContextID
	metadata := vault.MarshalAgentMetadata(meta)
	if o.Vault != nil {
		if err := o.Vault.UpdateAgentMetadata(agent.ID, metadata); err != nil {
			o.Pool.Discard(slot)
			return "", fmt.Errorf("persist agent context: %w", err)
		}
	}
	agent.Metadata = metadata

	o.agentToSlotMu.Lock()
	o.agentToSlot[agent.ID] = slot
	o.persistentAgentSlots[agent.ID] = true
	o.agentToSlotMu.Unlock()

	return slot.ContextID, nil
}

// ReleaseAgentContext releases a persistent chat agent's browser context. It is
// used for explicit kill/delete, not for ordinary per-turn NanoClaw completion.
func (o *Orchestrator) ReleaseAgentContext(agentID string) {
	if o == nil || agentID == "" {
		return
	}
	contextID := ""
	o.agentToSlotMu.Lock()
	slot, ok := o.agentToSlot[agentID]
	if ok {
		delete(o.agentToSlot, agentID)
		contextID = slot.ContextID
	}
	delete(o.persistentAgentSlots, agentID)
	o.agentToSlotMu.Unlock()

	if ok {
		o.unregisterNetworkIdentity(slot.UserContextID)
	}

	var meta vault.AgentMetadata
	var hasMetadata bool
	if o.Vault != nil {
		if agent, err := o.Vault.GetAgent(agentID); err == nil {
			if parsed, err := vault.ParseAgentMetadata(agent.Metadata); err == nil {
				meta = parsed
				hasMetadata = true
				if contextID == "" {
					contextID = meta.ContextID
				}
			}
		}
	}

	if ok {
		o.Pool.Discard(slot)
	} else if contextID != "" && o.Client != nil {
		if _, err := o.Client.Call("", "Browser.removeBrowserContext", map[string]interface{}{
			"browserContextId": contextID,
		}); err != nil {
			log.Printf("orchestrator: warning: failed to remove untracked context %s for agent %s: %v", contextID, agentID, err)
		}
	}
	if o.Vault != nil && hasMetadata && meta.ContextID != "" {
		meta.ContextID = ""
		_ = o.Vault.UpdateAgentMetadata(agentID, vault.MarshalAgentMetadata(meta))
	}
}

// Status returns the orchestrator's current state.
func (o *Orchestrator) Status() Status {
	avail, active, total := o.Pool.Stats()

	citizenCount := 0
	templateCount := 0
	if citizens, err := o.Vault.ListCitizens(); err == nil {
		citizenCount = len(citizens)
	}
	if templates, err := o.Vault.ListTemplates(); err == nil {
		templateCount = len(templates)
	}

	var totalCost float64
	if o.Costs != nil {
		totalCost = o.Costs.TotalCost()
	}

	return Status{
		KernelRunning:  o.Kernel.Running(),
		KernelPID:      o.Kernel.PID(),
		PoolAvailable:  avail,
		PoolActive:     active,
		PoolTotal:      total,
		ActiveAgents:   o.Agents.Count(),
		TotalCitizens:  citizenCount,
		TotalTemplates: templateCount,
		TotalCostUSD:   totalCost,
	}
}

// Close shuts down all subsystems.
// Note: kernel.Stop() is the caller's responsibility and must be called separately.
func (o *Orchestrator) Close() {
	o.Agents.KillAll()
	o.Agents.Dispose()
	o.Pool.Close()
	if o.memMonitor != nil {
		o.memMonitor.Stop()
	}
	o.Vault.Close()
	// Clean up optional subsystems (nil-safe)
	// AgentBus, Costs, Webhooks, Recording, PageCache have no Close methods
	// but we nil them to release references
	o.AgentBus = nil
	o.Costs = nil
	o.Webhooks = nil
	o.Recording = nil
	o.PageCache = nil
}

func (o *Orchestrator) applyCitizenToContext(contextID string, userContextID uint32, citizen *vault.Citizen) error {
	// Restore cached page state if resuming
	if o.PageCache != nil {
		if state := o.PageCache.Load(citizen.ID); state != nil {
			log.Printf("orchestrator: restoring cached page state for citizen %s (url=%s)", citizen.ID, state.URL)
		}
	}

	// Inject cookies
	cookies, err := o.Vault.GetCookies(citizen.ID)
	if err != nil {
		return err
	}
	for _, cc := range cookies {
		var cookieArray json.RawMessage
		if err := json.Unmarshal([]byte(cc.Cookies), &cookieArray); err != nil {
			continue
		}
		if _, err := o.Client.Call("", "Browser.setCookies", map[string]interface{}{
			"browserContextId": contextID,
			"cookies":          cookieArray,
		}); err != nil {
			log.Printf("orchestrator: warning: failed to set cookies for citizen %s on context %s: %v", citizen.ID, contextID, err)
		}
	}

	if citizen.Fingerprint != "" {
		if err := o.applyFingerprintToContext(contextID, userContextID, citizen.Fingerprint, citizen.Locale, citizen.Timezone); err != nil {
			return err
		}
	}

	// Apply network identity (TLS fingerprint) and proxy for this citizen
	o.applyCitizenNetworkIdentity(contextID, userContextID, citizen)

	// Apply security protections (CSP + sandbox) when enabled
	if o.SecurityEnabled {
		o.applySecurityToContext(contextID, citizen.ID)
	}

	return nil
}

func (o *Orchestrator) applyAgentToContext(contextID string, userContextID uint32, agent *vault.Agent) error {
	if agent == nil {
		return fmt.Errorf("agent not found")
	}
	if agent.Fingerprint != "" {
		if err := o.applyFingerprintToContext(contextID, userContextID, agent.Fingerprint, agent.Locale, agent.Timezone); err != nil {
			return err
		}
	}
	if err := o.applyNetworkIdentity(contextID, userContextID, agent); err != nil {
		log.Printf("orchestrator: warning: failed to apply network identity for agent %s: %v", agent.ID, err)
	}
	if agent.ProxyConfig != "" {
		if err := o.applyProxyToContext(contextID, agent.ProxyConfig); err != nil {
			log.Printf("orchestrator: warning: failed to apply proxy for agent %s on context %s: %v", agent.ID, contextID, err)
		}
	}
	if o.SecurityEnabled {
		o.applySecurityToContext(contextID, agent.ID)
	}
	return nil
}

// buildContextFingerprintPrefs builds the privileged-side per-context
// fingerprint delivery: roverfox.s.<key>_<userContextId> prefs for the
// high-entropy surfaces NOT covered by the stock per-context Browser.*Override
// calls (webgl vendor/renderer, audio seed, font-spacing seed, oscpu, hardware
// concurrency, WebRTC IP). The C++ managers read these prefs per-context on
// every page load, so no content-world window.set* call is needed and those
// setters stay invisible to web content (ChromeOnly). Returns nil when there
// is nothing to set. (Font list and the WebGL 147-param set are not pref-backed
// — see FINGERPRINT_SECURE_PLAN.md Phase 2.1.)
func buildContextFingerprintPrefs(fp vault.FingerprintData, userContextID uint32) []map[string]interface{} {
	type kv struct{ key, val string }
	var items []kv
	addStr := func(key, val string) {
		if val != "" {
			items = append(items, kv{key, val})
		}
	}
	addStr("webgl_vendor", fp.WebGLVendor)
	addStr("webgl_renderer", fp.WebGLRenderer)
	if fp.AudioSeed != 0 {
		items = append(items, kv{"audioFingerprintSeed", strconv.FormatUint(uint64(fp.AudioSeed), 10)})
	}
	if fp.FontSpacingSeed != 0 {
		items = append(items, kv{"seed", strconv.FormatUint(uint64(fp.FontSpacingSeed), 10)})
	}
	addStr("nav_oscpu", fp.OsCPU)
	if fp.HardwareConcurrency > 0 {
		items = append(items, kv{"nav_hwc", strconv.Itoa(fp.HardwareConcurrency)})
	}
	addStr("webrtc_ipv4", fp.WebRTCIPv4)
	addStr("webrtc_ipv6", fp.WebRTCIPv6)
	// The 147-param WebGL set, delivered per-context as a JSON blob the C++
	// WebGLParamsManager reads (roverfox.s.webgl_params_<ucid> / webgl2_params).
	if len(fp.WebGLParams) > 0 {
		items = append(items, kv{"webgl_params", string(fp.WebGLParams)})
	}
	if len(fp.WebGL2Params) > 0 {
		items = append(items, kv{"webgl2_params", string(fp.WebGL2Params)})
	}
	// Per-context WebGL extension lists (JSON arrays) the C++ WebGLParamsManager
	// reads (roverfox.s.webgl_extensions_<ucid> / webgl2_extensions_<ucid>).
	if len(fp.WebGLExtensions) > 0 {
		items = append(items, kv{"webgl_extensions", string(fp.WebGLExtensions)})
	}
	if len(fp.WebGL2Extensions) > 0 {
		items = append(items, kv{"webgl2_extensions", string(fp.WebGL2Extensions)})
	}
	if len(items) == 0 {
		return nil
	}
	prefs := make([]map[string]interface{}, 0, len(items))
	for _, it := range items {
		prefs = append(prefs, map[string]interface{}{
			"name":  fmt.Sprintf("roverfox.s.%s_%d", it.key, userContextID),
			"value": it.val,
		})
	}
	return prefs
}

func (o *Orchestrator) applyFingerprintToContext(contextID string, userContextID uint32, fpJSON, explicitLocale, explicitTimezone string) error {
	raw := map[string]interface{}{}
	if err := json.Unmarshal([]byte(fpJSON), &raw); err != nil {
		return fmt.Errorf("parse fingerprint: %w", err)
	}
	var fp vault.FingerprintData
	if err := json.Unmarshal([]byte(fpJSON), &fp); err != nil {
		return fmt.Errorf("parse fingerprint fields: %w", err)
	}

	ua := sanitizeFingerprintUserAgent(firstNonEmpty(fp.UserAgent, stringField(raw, "navigator.userAgent")), fp.Platform)
	if ua != "" {
		if _, err := o.Client.Call("", "Browser.setUserAgentOverride", map[string]interface{}{
			"browserContextId": contextID,
			"userAgent":        ua,
		}); err != nil {
			return fmt.Errorf("set user agent: %w", err)
		}
	}

	platform := firstNonEmpty(fp.Platform, stringField(raw, "navigator.platform"))
	if platform != "" {
		if _, err := o.Client.Call("", "Browser.setPlatformOverride", map[string]interface{}{
			"browserContextId": contextID,
			"platform":         platform,
		}); err != nil {
			return fmt.Errorf("set platform: %w", err)
		}
	}

	width := firstPositiveInt(fp.ScreenWidth, intField(raw, "screen.width"))
	height := firstPositiveInt(fp.ScreenHeight, intField(raw, "screen.height"))
	deviceScale := firstPositiveFloat(fp.DeviceScale, floatField(raw, "window.devicePixelRatio"))
	if width > 0 && height > 0 {
		viewport := map[string]interface{}{
			"viewportSize": map[string]interface{}{
				"width":  width,
				"height": height,
			},
		}
		if deviceScale > 0 {
			viewport["deviceScaleFactor"] = deviceScale
		}
		if _, err := o.Client.Call("", "Browser.setDefaultViewport", map[string]interface{}{
			"browserContextId": contextID,
			"viewport":         viewport,
		}); err != nil {
			return fmt.Errorf("set viewport: %w", err)
		}
	}

	locale := firstNonEmpty(explicitLocale, fp.Language, stringField(raw, "navigator.language"))
	if locale == "" {
		locale = vault.DefaultLocale()
	}
	if locale != "" {
		if _, err := o.Client.Call("", "Browser.setLocaleOverride", map[string]interface{}{
			"browserContextId": contextID,
			"locale":           locale,
		}); err != nil {
			return fmt.Errorf("set locale: %w", err)
		}
	}

	languages := fp.Languages
	if len(languages) == 0 {
		languages = languagesField(raw, "navigator.languages")
	}
	if explicitLocale != "" {
		languages = vault.LanguagesForLocale(explicitLocale)
	}
	if len(languages) == 0 && locale != "" {
		languages = vault.LanguagesForLocale(locale)
	}
	if header := acceptLanguageHeader(languages); header != "" {
		if _, err := o.Client.Call("", "Browser.setExtraHTTPHeaders", map[string]interface{}{
			"browserContextId": contextID,
			"headers": []map[string]string{
				{"name": "Accept-Language", "value": header},
			},
		}); err != nil {
			return fmt.Errorf("set accept-language: %w", err)
		}
	}

	timezone := firstNonEmpty(explicitTimezone, fp.Timezone, stringField(raw, "timezone"))
	if timezone == "" {
		timezone = vault.DefaultTimezone()
	}
	if timezone != "" {
		if _, err := o.Client.Call("", "Browser.setTimezoneOverride", map[string]interface{}{
			"browserContextId": contextID,
			"timezoneId":       timezone,
		}); err != nil {
			return fmt.Errorf("set timezone: %w", err)
		}
	}

	if lat, lon, ok := geolocationFields(raw); ok {
		geo := map[string]interface{}{
			"latitude":  lat,
			"longitude": lon,
		}
		if accuracy := floatField(raw, "geolocation:accuracy"); accuracy > 0 {
			geo["accuracy"] = accuracy
		}
		if _, err := o.Client.Call("", "Browser.setGeolocationOverride", map[string]interface{}{
			"browserContextId": contextID,
			"geolocation":      geo,
		}); err != nil {
			return fmt.Errorf("set geolocation: %w", err)
		}
	}

	// Clear any per-context fingerprint prefs left from a prior agent on this
	// (possibly reused) userContextId so it cannot inherit a stale identity,
	// then deliver this context's values.
	if _, err := o.Client.Call("", "Browser.clearContextFingerprint", map[string]interface{}{
		"userContextId": userContextID,
	}); err != nil {
		log.Printf("orchestrator: warning: clear context fingerprint failed for ctx %d: %v", userContextID, err)
	}

	// Per-context high-entropy surfaces (webgl/audio/font-spacing/oscpu/hwconc/
	// webrtc) that the stock overrides above do not cover: deliver them from the
	// privileged side as roverfox.s.<key>_<userContextId> prefs, which the C++
	// managers read per-context on every page load. No content-world setter is
	// involved, so the window.set* methods stay invisible to web content.
	if prefs := buildContextFingerprintPrefs(fp, userContextID); len(prefs) > 0 {
		if _, err := o.Client.Call("", "Browser.setContextFingerprint", map[string]interface{}{
			"prefs": prefs,
		}); err != nil {
			return fmt.Errorf("set context fingerprint: %w", err)
		}
	}

	uaSummary := ua
	if len(uaSummary) > 40 {
		uaSummary = uaSummary[:40] + "..."
	}
	log.Printf("orchestrator: fingerprint applied to context %s (ua=%s, screen=%dx%d)",
		contextID, uaSummary, width, height)
	return nil
}

// applySecurityToContext injects CSP headers and logs sandbox activation for a context.
func (o *Orchestrator) applySecurityToContext(contextID, ownerID string) {
	// Inject Content-Security-Policy headers via Juggler
	cspCfg := security.DefaultCSPConfig()
	if err := security.InjectCSP(o.Client, contextID, cspCfg); err != nil {
		log.Printf("orchestrator: warning: CSP injection failed for context %s: %v", contextID, err)
	}

	// Inject mutation observer if configured
	if o.mutationObs != nil {
		if err := security.InjectObserver(o.Client, contextID); err != nil {
			log.Printf("orchestrator: warning: mutation observer injection failed for context %s: %v", contextID, err)
		}
	}

	// Prepare sandbox (the Sandbox wraps JS expressions at call sites;
	// here we log that it is active so operators know protections are in place)
	sb := security.NewSandbox()
	log.Printf("orchestrator: security suite active for context %s (owner=%s, csp=inline-blocked, sandbox=%v)",
		contextID, ownerID, sb.BlockedAPIs())
}

// applyProxyToContext applies a proxy configuration to a running browser context.
func (o *Orchestrator) applyProxyToContext(contextID string, proxyConfigJSON string) error {
	var pc proxy.ProxyConfig
	if err := json.Unmarshal([]byte(proxyConfigJSON), &pc); err != nil {
		return fmt.Errorf("parse proxy config: %w", err)
	}
	_, err := o.Client.Call("", "Browser.setContextProxy", map[string]interface{}{
		"browserContextId": contextID,
		"proxy":            pc.URL(),
	})
	return err
}

// statusRelay forwards agent status updates (for use by TUI or other consumers).
func (o *Orchestrator) statusRelay() {
	for status := range o.Agents.StatusChan() {
		if isTerminalAgentStatus(status.Status) {
			o.handleTerminalAgentStatus(status.AgentID)
		}
	}
}

func (o *Orchestrator) handleTerminalAgentStatus(agentID string) {
	o.agentToSlotMu.Lock()
	if o.persistentAgentSlots[agentID] {
		o.agentToSlotMu.Unlock()
		return
	}
	slot, ok := o.agentToSlot[agentID]
	if ok {
		delete(o.agentToSlot, agentID)
	}
	o.agentToSlotMu.Unlock()
	if ok {
		o.unregisterNetworkIdentity(slot.UserContextID)
		o.Pool.Release(slot)
	}
}

func isTerminalAgentStatus(status string) bool {
	switch status {
	case "completed", "error", "failed", "interrupted":
		return true
	default:
		return false
	}
}

func sanitizeFingerprintUserAgent(userAgent, platform string) string {
	userAgent = strings.TrimSpace(userAgent)
	if userAgent == "" || strings.Contains(userAgent, "Camoufox") {
		return vault.DefaultUserAgentForHost(hostOSForPlatform(platform))
	}
	return userAgent
}

func hostOSForPlatform(platform string) string {
	switch {
	case strings.Contains(platform, "Win"):
		return "win"
	case strings.Contains(platform, "Mac"):
		return "mac"
	case strings.Contains(platform, "Linux"):
		return "lin"
	default:
		return vault.HostOS()
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstPositiveFloat(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func stringField(raw map[string]interface{}, key string) string {
	value, _ := raw[key].(string)
	return strings.TrimSpace(value)
}

func intField(raw map[string]interface{}, key string) int {
	switch value := raw[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		if n, err := value.Int64(); err == nil {
			return int(n)
		}
	}
	return 0
}

func floatField(raw map[string]interface{}, key string) float64 {
	switch value := raw[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case json.Number:
		if n, err := value.Float64(); err == nil {
			return n
		}
	}
	return 0
}

func languagesField(raw map[string]interface{}, key string) []string {
	switch value := raw[key].(type) {
	case []interface{}:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	case []string:
		return append([]string(nil), value...)
	case string:
		parts := strings.Split(value, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if s := strings.TrimSpace(part); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func acceptLanguageHeader(languages []string) string {
	cleaned := make([]string, 0, len(languages))
	seen := map[string]struct{}{}
	for _, lang := range languages {
		lang = strings.TrimSpace(lang)
		if lang == "" {
			continue
		}
		if _, ok := seen[lang]; ok {
			continue
		}
		seen[lang] = struct{}{}
		cleaned = append(cleaned, lang)
	}
	if len(cleaned) == 0 {
		return ""
	}
	parts := []string{cleaned[0]}
	for i := 1; i < len(cleaned); i++ {
		q := 1.0 - (float64(i) * 0.1)
		if q < 0.1 {
			q = 0.1
		}
		parts = append(parts, fmt.Sprintf("%s;q=%.1f", cleaned[i], q))
	}
	return strings.Join(parts, ",")
}

func geolocationFields(raw map[string]interface{}) (float64, float64, bool) {
	lat := floatField(raw, "geolocation:latitude")
	lon := floatField(raw, "geolocation:longitude")
	if lat == 0 && lon == 0 {
		return 0, 0, false
	}
	return lat, lon, true
}

func (o *Orchestrator) spawnScopedAgent(contextID, sopFile string) (string, error) {
	scopedConfig, cleanup, err := o.PrepareScopedNanoClawConfig(contextID)
	if err != nil {
		return "", err
	}

	agentID, err := o.Agents.SpawnIsolated(contextID, sopFile, scopedConfig, cleanup)
	if err != nil {
		cleanup()
		return "", err
	}

	return agentID, nil
}

// PrepareScopedNanoClawConfig builds a per-context NanoClaw config overlay.
func (o *Orchestrator) PrepareScopedNanoClawConfig(contextID string) (string, func(), error) {
	if o == nil || o.Client == nil {
		return "", nil, fmt.Errorf("orchestrator client not available")
	}
	if contextID == "" {
		return "", nil, fmt.Errorf("context id is required")
	}

	scopedFoxbridge, err := foxbridge.StartEmbeddedScoped(o.Client, 0, contextID)
	if err != nil {
		return "", nil, fmt.Errorf("start scoped foxbridge: %w", err)
	}

	scopedConfig, cleanupConfig, err := nanoclaw.PrepareScopedConfig(config.NanoClawConfigPath(), scopedFoxbridge.CDPURL())
	if err != nil {
		scopedFoxbridge.Stop()
		return "", nil, fmt.Errorf("prepare scoped NanoClaw config: %w", err)
	}

	cleanup := func() {
		cleanupConfig()
		scopedFoxbridge.Stop()
	}
	return scopedConfig, cleanup, nil
}

// profileFamilyForPlatform maps a platform string to a networklab profile family.
// VulpineOS is Camoufox-based, so all profiles are Firefox variants.
func profileFamilyForPlatform(platform string) string {
	switch {
	case strings.Contains(platform, "Win"), strings.Contains(platform, "Windows"):
		return "firefox146_windows"
	case strings.Contains(platform, "Linux"), strings.Contains(platform, "linux"):
		return "firefox146_linux"
	default:
		return "firefox146_macos"
	}
}

// createNetworklabIdentity creates a networklab identity from a fingerprint string
// and writes it to shared memory for the Camoufox NetworkIdentityManager to consume.
// Returns the identity, computed hashes, and the profile family name.
// registerNetworkIdentity records the identity for a context (keyed by the
// numeric userContextId) and flushes the full set to the networklab shmem as a
// ctx-<id> map, so each context's TLS identity is applied independently.
func (o *Orchestrator) registerNetworkIdentity(userContextID uint32, nid *networklab.Identity) {
	o.netIdentitiesMu.Lock()
	defer o.netIdentitiesMu.Unlock()
	if o.netIdentities == nil {
		o.netIdentities = make(map[uint32]*networklab.Identity)
	}
	o.netIdentities[userContextID] = nid
	o.flushNetworkIdentitiesLocked()
}

// unregisterNetworkIdentity drops a context's identity and rewrites the shmem so
// a torn-down context's TLS identity is not left applying to a reused id.
func (o *Orchestrator) unregisterNetworkIdentity(userContextID uint32) {
	o.netIdentitiesMu.Lock()
	defer o.netIdentitiesMu.Unlock()
	if o.netIdentities == nil {
		return
	}
	delete(o.netIdentities, userContextID)
	o.flushNetworkIdentitiesLocked()
}

// flushNetworkIdentitiesLocked writes the current registry to shmem. Caller
// must hold netIdentitiesMu. A single entry serializes to the top-level form
// (applies to every context); multiple entries serialize to the ctx-<id> map.
func (o *Orchestrator) flushNetworkIdentitiesLocked() {
	m := make(map[string]*networklab.Identity, len(o.netIdentities))
	for ucid, nid := range o.netIdentities {
		m[fmt.Sprintf("ctx-%d", ucid)] = nid
	}
	if err := networklab.WriteIdentities(m); err != nil {
		log.Printf("orchestrator: failed to flush %d network identities to shmem: %v", len(m), err)
	}
}

func (o *Orchestrator) createNetworklabIdentity(fingerprint string, contextID string, userContextID uint32) (*networklab.Identity, *networklab.IdentityHashes, string, error) {
	var fp vault.FingerprintData
	if err := json.Unmarshal([]byte(fingerprint), &fp); err != nil {
		return nil, nil, "", fmt.Errorf("parse fingerprint: %w", err)
	}
	family := profileFamilyForPlatform(fp.Platform)
	nid, err := networklab.NewIdentity(family)
	if err != nil {
		return nil, nil, family, fmt.Errorf("new identity on %s: %w", family, err)
	}
	hashes, err := nid.Hashes()
	if err != nil {
		return nil, nil, family, fmt.Errorf("hashes on %s: %w", family, err)
	}
	o.registerNetworkIdentity(userContextID, nid)
	return nid, hashes, family, nil
}

// applyNetworkIdentity creates a networklab identity for the agent and stores
// its metadata in the vault. The identity is also written to shared memory so
// the Camoufox NetworkIdentityManager applies TLS parameters per socket.
func (o *Orchestrator) applyNetworkIdentity(contextID string, userContextID uint32, agent *vault.Agent) error {
	if agent == nil || agent.Fingerprint == "" {
		return nil
	}
	_, hashes, family, err := o.createNetworklabIdentity(agent.Fingerprint, contextID, userContextID)
	if err != nil {
		log.Printf("orchestrator: networklab identity unavailable for %s on %s: %v", agent.ID, family, err)
		return nil
	}
	log.Printf("orchestrator: agent %s network identity %s → JA3=%s JA4=%s (context=%s)",
		agent.ID, family, hashes.JA3, hashes.JA4, contextID)

	if o.Vault != nil {
		meta, err := vault.ParseAgentMetadata(agent.Metadata)
		if err == nil {
			meta.NetworkIdentity = &vault.NetworkIdentityMetadata{
				ProfileFamily: family,
				JA3:           hashes.JA3,
				JA4:           hashes.JA4,
			}
			metaJSON := vault.MarshalAgentMetadata(meta)
			if err := o.Vault.UpdateAgentMetadata(agent.ID, metaJSON); err != nil {
				log.Printf("orchestrator: failed to store network identity metadata for %s: %v", agent.ID, err)
			}
			agent.Metadata = metaJSON
		}
	}

	// Sync proxy with network identity: if the agent has a proxy,
	// validate the geo-sync is consistent
	if agent.ProxyConfig != "" {
		var pc proxy.ProxyConfig
		if err := json.Unmarshal([]byte(agent.ProxyConfig), &pc); err == nil && pc.URL() != "" {
			log.Printf("orchestrator: agent %s network identity synced with proxy %s (context=%s)",
				agent.ID, pc.URL(), contextID)
		}
	}

	return nil
}

// applyCitizenNetworkIdentity applies networklab identity and proxy for a citizen
// context. Called from SpawnCitizen since citizens have fingerprints and proxies.
func (o *Orchestrator) applyCitizenNetworkIdentity(contextID string, userContextID uint32, citizen *vault.Citizen) {
	if citizen.Fingerprint == "" {
		return
	}
	_, hashes, family, err := o.createNetworklabIdentity(citizen.Fingerprint, contextID, userContextID)
	if err != nil {
		log.Printf("orchestrator: citizen networklab identity unavailable for %s: %v", citizen.ID, err)
		return
	}
	log.Printf("orchestrator: citizen %s network identity %s → JA3=%s JA4=%s (context=%s)",
		citizen.Label, family, hashes.JA3, hashes.JA4, contextID)

	if citizen.ProxyConfig != "" {
		if err := o.applyProxyToContext(contextID, citizen.ProxyConfig); err != nil {
			log.Printf("orchestrator: failed to apply proxy for citizen %s: %v", citizen.ID, err)
		}
	}
}

// applyDefaultNetworkIdentity creates a default networklab identity based on the
// host OS platform and writes it to shared memory. Used by nomad sessions and
// other ephemeral contexts that lack a stored fingerprint.
func (o *Orchestrator) applyDefaultNetworkIdentity(contextID string, userContextID uint32, ownerID string) {
	family := profileFamilyForPlatform(vault.DefaultPlatformForHostOS())
	nid, err := networklab.NewIdentity(family)
	if err != nil {
		log.Printf("orchestrator: default networklab identity unavailable for %s on %s: %v", ownerID, family, err)
		return
	}
	hashes, err := nid.Hashes()
	if err != nil {
		log.Printf("orchestrator: default networklab hashes unavailable for %s on %s: %v", ownerID, family, err)
		return
	}
	o.registerNetworkIdentity(userContextID, nid)
	log.Printf("orchestrator: nomad %s default network identity %s → JA3=%s JA4=%s (context=%s)",
		ownerID, family, hashes.JA3, hashes.JA4, contextID)
}
