package orchestrator

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"vulpineos/internal/agentbus"
	"vulpineos/internal/costtrack"
	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
	"vulpineos/internal/openclaw"
	"vulpineos/internal/pagecache"
	"vulpineos/internal/pool"
	"vulpineos/internal/recording"
	"vulpineos/internal/vault"
	"vulpineos/internal/webhooks"
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

// Orchestrator ties together kernel, pool, vault, and OpenClaw manager.
type Orchestrator struct {
	Kernel  *kernel.Kernel
	Client  *juggler.Client
	Pool    *pool.Pool
	Vault   *vault.DB
	Agents  *openclaw.Manager

	// Optional subsystems (nil-safe)
	AgentBus  *agentbus.Bus
	Costs     *costtrack.Tracker
	Webhooks  *webhooks.Manager
	Recording *recording.Recorder
	PageCache *pagecache.Cache

	// Track which agent owns which context slot
	agentToSlot   map[string]*pool.ContextSlot
	agentToSlotMu sync.Mutex
}

// Opts holds optional subsystem dependencies for the orchestrator.
type Opts struct {
	AgentBus  *agentbus.Bus
	Costs     *costtrack.Tracker
	Webhooks  *webhooks.Manager
	Recording *recording.Recorder
	PageCache *pagecache.Cache
}

// New creates an orchestrator with all subsystems.
func New(k *kernel.Kernel, client *juggler.Client, v *vault.DB, poolCfg pool.Config, openclawBinary string, opts ...Opts) *Orchestrator {
	o := &Orchestrator{
		Kernel:      k,
		Client:      client,
		Pool:        pool.New(client, poolCfg),
		Vault:       v,
		Agents:      openclaw.NewManager(openclawBinary),
		agentToSlot: make(map[string]*pool.ContextSlot),
	}
	if len(opts) > 0 {
		o.AgentBus = opts[0].AgentBus
		o.Costs = opts[0].Costs
		o.Webhooks = opts[0].Webhooks
		o.Recording = opts[0].Recording
		o.PageCache = opts[0].PageCache
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
	if err := o.applyCitizenToContext(slot.ContextID, citizen); err != nil {
		o.Pool.Release(slot)
		return "", fmt.Errorf("apply citizen: %w", err)
	}

	// Write SOP and spawn agent
	sopFile, err := openclaw.WriteSOP(tmpl.SOP)
	if err != nil {
		o.Pool.Release(slot)
		return "", fmt.Errorf("write SOP: %w", err)
	}

	agentID, err := o.Agents.Spawn(slot.ContextID, sopFile)
	if err != nil {
		openclaw.CleanupSOP(sopFile)
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

	// Write SOP and spawn agent
	sopFile, err := openclaw.WriteSOP(tmpl.SOP)
	if err != nil {
		o.Pool.Release(slot)
		return "", fmt.Errorf("write SOP: %w", err)
	}

	agentID, err := o.Agents.Spawn(slot.ContextID, sopFile)
	if err != nil {
		openclaw.CleanupSOP(sopFile)
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
		return err
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
		o.Webhooks.Fire(webhooks.AgentCompleted, map[string]interface{}{
			"agentId": agentID,
		})
	}

	o.agentToSlotMu.Lock()
	slot, ok := o.agentToSlot[agentID]
	if ok {
		delete(o.agentToSlot, agentID)
	}
	o.agentToSlotMu.Unlock()
	if ok {
		o.Pool.Release(slot)
	}
	return nil
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
	o.Agents.Dispose()
	o.Pool.Close()
	if o.Client != nil {
		o.Client.Close()
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

func (o *Orchestrator) applyCitizenToContext(contextID string, citizen *vault.Citizen) error {
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

	// Apply locale/timezone if set
	if citizen.Locale != "" {
		o.Client.Call("", "Browser.setLocaleOverride", map[string]interface{}{
			"browserContextId": contextID,
			"locale":           citizen.Locale,
		})
	}
	if citizen.Timezone != "" {
		o.Client.Call("", "Browser.setTimezoneOverride", map[string]interface{}{
			"browserContextId": contextID,
			"timezoneId":       citizen.Timezone,
		})
	}

	// Apply fingerprint to the browser context via existing Juggler per-context methods.
	// Camoufox supports per-context: user agent, viewport, device scale factor, locale,
	// timezone, geolocation, proxy — no C++ patches needed.
	if citizen.Fingerprint != "" {
		var fp vault.FingerprintData
		if err := json.Unmarshal([]byte(citizen.Fingerprint), &fp); err == nil {
			if fp.UserAgent != "" {
				o.Client.Call("", "Browser.setUserAgentOverride", map[string]interface{}{
					"browserContextId": contextID,
					"userAgent":        fp.UserAgent,
				})
			}
			if fp.ScreenWidth > 0 && fp.ScreenHeight > 0 {
				o.Client.Call("", "Browser.setDefaultViewport", map[string]interface{}{
					"browserContextId": contextID,
					"viewport": map[string]interface{}{
						"width":  fp.ScreenWidth,
						"height": fp.ScreenHeight,
					},
				})
			}
			if fp.Language != "" && citizen.Locale == "" {
				o.Client.Call("", "Browser.setLocaleOverride", map[string]interface{}{
					"browserContextId": contextID,
					"locale":           fp.Language,
				})
			}
			uaSummary := fp.UserAgent
			if len(uaSummary) > 40 {
				uaSummary = uaSummary[:40] + "..."
			}
			log.Printf("orchestrator: fingerprint applied to context %s (ua=%s, screen=%dx%d)",
				contextID, uaSummary, fp.ScreenWidth, fp.ScreenHeight)
		}
	}

	return nil
}

// statusRelay forwards agent status updates (for use by TUI or other consumers).
func (o *Orchestrator) statusRelay() {
	for status := range o.Agents.StatusChan() {
		if status.Status == "completed" || status.Status == "error" {
			o.agentToSlotMu.Lock()
			slot, ok := o.agentToSlot[status.AgentID]
			if ok {
				delete(o.agentToSlot, status.AgentID)
			}
			o.agentToSlotMu.Unlock()
			if ok {
				o.Pool.Release(slot)
			}
		}
	}
}
