package remote

import (
	"encoding/json"
	"fmt"

	"vulpineos/internal/agentbus"
	"vulpineos/internal/config"
	"vulpineos/internal/costtrack"
	"vulpineos/internal/kernel"
	"vulpineos/internal/orchestrator"
	"vulpineos/internal/proxy"
	"vulpineos/internal/recording"
	"vulpineos/internal/vault"
	"vulpineos/internal/webhooks"
)

// PanelAPI handles control messages from the web panel, dispatching to subsystems.
type PanelAPI struct {
	Orchestrator *orchestrator.Orchestrator
	Config       *config.Config
	Vault        *vault.DB
	AgentBus     *agentbus.Bus
	Costs        *costtrack.Tracker
	Webhooks     *webhooks.Manager
	Recorder     *recording.Recorder
	Rotator      *proxy.Rotator
	Kernel       *kernel.Kernel
}

// HandleMessage dispatches a control message to the appropriate handler.
// Returns the JSON result or an error.
func (api *PanelAPI) HandleMessage(method string, params json.RawMessage) (json.RawMessage, error) {
	switch method {
	// --- Agent management ---
	case "agents.list":
		return api.agentsList()
	case "agents.spawn":
		return api.agentsSpawn(params)
	case "agents.kill":
		return api.agentsKill(params)
	case "agents.pause":
		return api.agentsPause(params)
	case "agents.resume":
		return api.agentsResume(params)
	case "agents.getMessages":
		return api.agentsGetMessages(params)

	// --- Config ---
	case "config.get":
		return api.configGet()
	case "config.set":
		return api.configSet(params)

	// --- Cost tracking ---
	case "costs.getAll":
		return api.costsGetAll()
	case "costs.setBudget":
		return api.costsSetBudget(params)
	case "costs.total":
		return api.costsTotal()

	// --- Webhooks ---
	case "webhooks.list":
		return api.webhooksList()
	case "webhooks.add":
		return api.webhooksAdd(params)
	case "webhooks.remove":
		return api.webhooksRemove(params)

	// --- Proxies ---
	case "proxies.list":
		return api.proxiesList()
	case "proxies.add":
		return api.proxiesAdd(params)
	case "proxies.delete":
		return api.proxiesDelete(params)
	case "proxies.test":
		return api.proxiesTest(params)
	case "proxies.setRotation":
		return api.proxiesSetRotation(params)

	// --- Agent Bus ---
	case "bus.pending":
		return api.busPending()
	case "bus.approve":
		return api.busApprove(params)
	case "bus.reject":
		return api.busReject(params)
	case "bus.policies":
		return api.busPolicies()
	case "bus.addPolicy":
		return api.busAddPolicy(params)

	// --- Recording ---
	case "recording.getTimeline":
		return api.recordingGetTimeline(params)
	case "recording.export":
		return api.recordingExport(params)

	// --- Fingerprints ---
	case "fingerprints.get":
		return api.fingerprintsGet(params)
	case "fingerprints.generate":
		return api.fingerprintsGenerate(params)

	// --- Status ---
	case "status.get":
		return api.statusGet()

	default:
		return nil, fmt.Errorf("unknown method: %s", method)
	}
}

// ---------------------------------------------------------------------------
// Agent management
// ---------------------------------------------------------------------------

func (api *PanelAPI) agentsList() (json.RawMessage, error) {
	if api.Vault == nil {
		return nil, fmt.Errorf("vault not available")
	}
	agents, err := api.Vault.ListAgents()
	if err != nil {
		return nil, err
	}
	type agentSummary struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Status      string `json:"status"`
		Task        string `json:"task"`
		TotalTokens int    `json:"totalTokens"`
		Fingerprint string `json:"fingerprint"` // summary string
	}
	out := make([]agentSummary, len(agents))
	for i, a := range agents {
		out[i] = agentSummary{
			ID:          a.ID,
			Name:        a.Name,
			Status:      a.Status,
			Task:        a.Task,
			TotalTokens: a.TotalTokens,
			Fingerprint: vault.FingerprintSummary(a.Fingerprint),
		}
	}
	return json.Marshal(out)
}

func (api *PanelAPI) agentsSpawn(params json.RawMessage) (json.RawMessage, error) {
	if api.Orchestrator == nil {
		return nil, fmt.Errorf("orchestrator not available")
	}
	var p struct {
		TemplateID string `json:"templateId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	agentID, err := api.Orchestrator.SpawnNomad(p.TemplateID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"agentId": agentID})
}

func (api *PanelAPI) agentsKill(params json.RawMessage) (json.RawMessage, error) {
	if api.Orchestrator == nil {
		return nil, fmt.Errorf("orchestrator not available")
	}
	var p struct {
		AgentID string `json:"agentId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := api.Orchestrator.KillAgent(p.AgentID); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"status": "ok"})
}

func (api *PanelAPI) agentsPause(params json.RawMessage) (json.RawMessage, error) {
	if api.Orchestrator == nil {
		return nil, fmt.Errorf("orchestrator not available")
	}
	var p struct {
		AgentID string `json:"agentId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := api.Orchestrator.Agents.PauseAgent(p.AgentID); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"status": "ok"})
}

func (api *PanelAPI) agentsResume(params json.RawMessage) (json.RawMessage, error) {
	if api.Orchestrator == nil {
		return nil, fmt.Errorf("orchestrator not available")
	}
	var p struct {
		AgentID     string `json:"agentId"`
		SessionName string `json:"sessionName"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	configPath := config.OpenClawConfigPath()
	id, err := api.Orchestrator.Agents.ResumeWithSession(p.AgentID, p.SessionName, configPath)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"agentId": id})
}

func (api *PanelAPI) agentsGetMessages(params json.RawMessage) (json.RawMessage, error) {
	if api.Vault == nil {
		return nil, fmt.Errorf("vault not available")
	}
	var p struct {
		AgentID string `json:"agentId"`
		Limit   int    `json:"limit,omitempty"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	var msgs []vault.AgentMessage
	var err error
	if p.Limit > 0 {
		msgs, err = api.Vault.GetRecentMessages(p.AgentID, p.Limit)
	} else {
		msgs, err = api.Vault.GetMessages(p.AgentID)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(msgs)
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

func (api *PanelAPI) configGet() (json.RawMessage, error) {
	if api.Config == nil {
		return nil, fmt.Errorf("config not available")
	}
	// Return a safe view (mask the API key)
	out := struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		HasKey   bool   `json:"hasKey"`
	}{
		Provider: api.Config.Provider,
		Model:    api.Config.Model,
		HasKey:   api.Config.APIKey != "",
	}
	return json.Marshal(out)
}

func (api *PanelAPI) configSet(params json.RawMessage) (json.RawMessage, error) {
	if api.Config == nil {
		return nil, fmt.Errorf("config not available")
	}
	var p struct {
		Provider string `json:"provider,omitempty"`
		Model    string `json:"model,omitempty"`
		APIKey   string `json:"apiKey,omitempty"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Provider != "" {
		api.Config.Provider = p.Provider
	}
	if p.Model != "" {
		api.Config.Model = p.Model
	}
	if p.APIKey != "" {
		api.Config.APIKey = p.APIKey
	}
	if err := api.Config.Save(); err != nil {
		return nil, fmt.Errorf("save config: %w", err)
	}
	return json.Marshal(map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------------------
// Cost tracking
// ---------------------------------------------------------------------------

func (api *PanelAPI) costsGetAll() (json.RawMessage, error) {
	if api.Costs == nil {
		return nil, fmt.Errorf("cost tracker not available")
	}
	return json.Marshal(api.Costs.AllUsage())
}

func (api *PanelAPI) costsSetBudget(params json.RawMessage) (json.RawMessage, error) {
	if api.Costs == nil {
		return nil, fmt.Errorf("cost tracker not available")
	}
	var p struct {
		AgentID   string  `json:"agentId"`
		MaxCost   float64 `json:"maxCostUsd"`
		MaxTokens int64   `json:"maxTokens"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	api.Costs.SetBudget(p.AgentID, p.MaxCost, p.MaxTokens)
	return json.Marshal(map[string]string{"status": "ok"})
}

func (api *PanelAPI) costsTotal() (json.RawMessage, error) {
	if api.Costs == nil {
		return nil, fmt.Errorf("cost tracker not available")
	}
	return json.Marshal(map[string]float64{"totalCostUsd": api.Costs.TotalCost()})
}

// ---------------------------------------------------------------------------
// Webhooks
// ---------------------------------------------------------------------------

func (api *PanelAPI) webhooksList() (json.RawMessage, error) {
	if api.Webhooks == nil {
		return nil, fmt.Errorf("webhook manager not available")
	}
	return json.Marshal(api.Webhooks.List())
}

func (api *PanelAPI) webhooksAdd(params json.RawMessage) (json.RawMessage, error) {
	if api.Webhooks == nil {
		return nil, fmt.Errorf("webhook manager not available")
	}
	var p struct {
		URL    string              `json:"url"`
		Events []webhooks.EventType `json:"events"`
		Secret string              `json:"secret"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	id := api.Webhooks.Register(p.URL, p.Events, p.Secret)
	return json.Marshal(map[string]string{"id": id})
}

func (api *PanelAPI) webhooksRemove(params json.RawMessage) (json.RawMessage, error) {
	if api.Webhooks == nil {
		return nil, fmt.Errorf("webhook manager not available")
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	api.Webhooks.Unregister(p.ID)
	return json.Marshal(map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------------------
// Proxies
// ---------------------------------------------------------------------------

func (api *PanelAPI) proxiesList() (json.RawMessage, error) {
	if api.Vault == nil {
		return nil, fmt.Errorf("vault not available")
	}
	proxies, err := api.Vault.ListProxies()
	if err != nil {
		return nil, err
	}
	return json.Marshal(proxies)
}

func (api *PanelAPI) proxiesAdd(params json.RawMessage) (json.RawMessage, error) {
	if api.Vault == nil {
		return nil, fmt.Errorf("vault not available")
	}
	var p struct {
		Config string `json:"config"` // JSON proxy config or proxy URL
		Geo    string `json:"geo"`
		Label  string `json:"label"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	stored, err := api.Vault.AddProxy(p.Config, p.Geo, p.Label)
	if err != nil {
		return nil, err
	}
	return json.Marshal(stored)
}

func (api *PanelAPI) proxiesDelete(params json.RawMessage) (json.RawMessage, error) {
	if api.Vault == nil {
		return nil, fmt.Errorf("vault not available")
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := api.Vault.DeleteProxy(p.ID); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"status": "ok"})
}

func (api *PanelAPI) proxiesTest(params json.RawMessage) (json.RawMessage, error) {
	var p struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	pc, err := proxy.ParseProxyURL(p.URL)
	if err != nil {
		return nil, fmt.Errorf("parse proxy URL: %w", err)
	}
	latency, err := proxy.TestProxy(*pc)
	if err != nil {
		return nil, fmt.Errorf("proxy test failed: %w", err)
	}
	return json.Marshal(map[string]int64{"latencyMs": latency})
}

func (api *PanelAPI) proxiesSetRotation(params json.RawMessage) (json.RawMessage, error) {
	if api.Rotator == nil {
		return nil, fmt.Errorf("proxy rotator not available")
	}
	var p struct {
		AgentID string               `json:"agentId"`
		Config  proxy.RotationConfig `json:"config"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	api.Rotator.SetConfig(p.AgentID, &p.Config)
	return json.Marshal(map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------------------
// Agent Bus
// ---------------------------------------------------------------------------

func (api *PanelAPI) busPending() (json.RawMessage, error) {
	if api.AgentBus == nil {
		return nil, fmt.Errorf("agent bus not available")
	}
	return json.Marshal(api.AgentBus.PendingMessages())
}

func (api *PanelAPI) busApprove(params json.RawMessage) (json.RawMessage, error) {
	if api.AgentBus == nil {
		return nil, fmt.Errorf("agent bus not available")
	}
	var p struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := api.AgentBus.Approve(p.MessageID); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"status": "ok"})
}

func (api *PanelAPI) busReject(params json.RawMessage) (json.RawMessage, error) {
	if api.AgentBus == nil {
		return nil, fmt.Errorf("agent bus not available")
	}
	var p struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := api.AgentBus.Reject(p.MessageID); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"status": "ok"})
}

func (api *PanelAPI) busPolicies() (json.RawMessage, error) {
	if api.AgentBus == nil {
		return nil, fmt.Errorf("agent bus not available")
	}
	return json.Marshal(api.AgentBus.Policies())
}

func (api *PanelAPI) busAddPolicy(params json.RawMessage) (json.RawMessage, error) {
	if api.AgentBus == nil {
		return nil, fmt.Errorf("agent bus not available")
	}
	var p struct {
		FromAgent   string `json:"fromAgent"`
		ToAgent     string `json:"toAgent"`
		AutoApprove bool   `json:"autoApprove"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	api.AgentBus.AddPolicy(p.FromAgent, p.ToAgent, p.AutoApprove)
	return json.Marshal(map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------------------
// Recording
// ---------------------------------------------------------------------------

func (api *PanelAPI) recordingGetTimeline(params json.RawMessage) (json.RawMessage, error) {
	if api.Recorder == nil {
		return nil, fmt.Errorf("recorder not available")
	}
	var p struct {
		AgentID string `json:"agentId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	timeline := api.Recorder.GetTimeline(p.AgentID)
	return json.Marshal(timeline)
}

func (api *PanelAPI) recordingExport(params json.RawMessage) (json.RawMessage, error) {
	if api.Recorder == nil {
		return nil, fmt.Errorf("recorder not available")
	}
	var p struct {
		AgentID string `json:"agentId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	data, err := api.Recorder.Export(p.AgentID)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// ---------------------------------------------------------------------------
// Fingerprints
// ---------------------------------------------------------------------------

func (api *PanelAPI) fingerprintsGet(params json.RawMessage) (json.RawMessage, error) {
	if api.Vault == nil {
		return nil, fmt.Errorf("vault not available")
	}
	var p struct {
		AgentID string `json:"agentId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	agent, err := api.Vault.GetAgent(p.AgentID)
	if err != nil {
		return nil, err
	}
	// Return parsed fingerprint data plus the summary
	fp, _ := vault.ParseFingerprint(agent.Fingerprint)
	out := struct {
		Raw     string              `json:"raw"`
		Parsed  *vault.FingerprintData `json:"parsed,omitempty"`
		Summary string              `json:"summary"`
	}{
		Raw:     agent.Fingerprint,
		Parsed:  fp,
		Summary: vault.FingerprintSummary(agent.Fingerprint),
	}
	return json.Marshal(out)
}

func (api *PanelAPI) fingerprintsGenerate(params json.RawMessage) (json.RawMessage, error) {
	var p struct {
		Seed string `json:"seed"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Seed == "" {
		p.Seed = "default"
	}
	fp, err := vault.GenerateFingerprint(p.Seed)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"fingerprint": fp})
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

func (api *PanelAPI) statusGet() (json.RawMessage, error) {
	out := struct {
		Orchestrator *orchestrator.Status `json:"orchestrator,omitempty"`
		KernelPID    int                  `json:"kernelPid"`
		KernelUp     bool                 `json:"kernelUp"`
	}{}

	if api.Kernel != nil {
		out.KernelUp = api.Kernel.Running()
		out.KernelPID = api.Kernel.PID()
	}
	if api.Orchestrator != nil {
		status := api.Orchestrator.Status()
		out.Orchestrator = &status
	}

	return json.Marshal(out)
}
