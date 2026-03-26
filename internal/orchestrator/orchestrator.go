package orchestrator

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
	"vulpineos/internal/openclaw"
	"vulpineos/internal/pool"
	"vulpineos/internal/vault"
)

// Status describes the orchestrator's current state.
type Status struct {
	KernelRunning  bool   `json:"kernel_running"`
	KernelPID      int    `json:"kernel_pid"`
	PoolAvailable  int    `json:"pool_available"`
	PoolActive     int    `json:"pool_active"`
	PoolTotal      int    `json:"pool_total"`
	ActiveAgents   int    `json:"active_agents"`
	TotalCitizens  int    `json:"total_citizens"`
	TotalTemplates int    `json:"total_templates"`
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

	// Track which agent owns which context slot
	agentToSlot   map[string]*pool.ContextSlot
	agentToSlotMu sync.Mutex
}

// New creates an orchestrator with all subsystems.
func New(k *kernel.Kernel, client *juggler.Client, v *vault.DB, poolCfg pool.Config, openclawBinary string) *Orchestrator {
	return &Orchestrator{
		Kernel:      k,
		Client:      client,
		Pool:        pool.New(client, poolCfg),
		Vault:       v,
		Agents:      openclaw.NewManager(openclawBinary),
		agentToSlot: make(map[string]*pool.ContextSlot),
	}
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

	log.Printf("orchestrator: spawned nomad agent %s (session=%s, context=%s)", agentID, session.ID, slot.ContextID)
	return agentID, nil
}

// KillAgent stops an agent and releases its context.
func (o *Orchestrator) KillAgent(agentID string) error {
	if err := o.Agents.Kill(agentID); err != nil {
		return err
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

	return Status{
		KernelRunning:  o.Kernel.Running(),
		KernelPID:      o.Kernel.PID(),
		PoolAvailable:  avail,
		PoolActive:     active,
		PoolTotal:      total,
		ActiveAgents:   o.Agents.Count(),
		TotalCitizens:  citizenCount,
		TotalTemplates: templateCount,
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
}

func (o *Orchestrator) applyCitizenToContext(contextID string, citizen *vault.Citizen) error {
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
