package remote

import (
	"encoding/json"
	"fmt"
	"strings"

	"vulpineos/internal/config"
	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
	"vulpineos/internal/nanoclaw"
	"vulpineos/internal/orchestrator"
	"vulpineos/internal/proxy"
	"vulpineos/internal/vault"
)

type daemonStatus interface {
	Running() bool
}

// ControlAPI handles remote TUI control messages. The public remote surface is
// the TUI protocol.
type ControlAPI struct {
	Orchestrator     *orchestrator.Orchestrator
	Config           *config.Config
	Vault            *vault.DB
	Kernel           *kernel.Kernel
	Daemon           daemonStatus
	FoxbridgeRunning func() bool
	Client           *juggler.Client
}

func (api *ControlAPI) HandleMessage(method string, params json.RawMessage) (json.RawMessage, error) {
	switch method {
	case "status.get":
		return api.statusGet()
	case "agents.list":
		return api.agentsList()
	case "agents.getMessages":
		return api.agentsGetMessages(params)
	case "agents.spawn":
		return api.agentsSpawn(params)
	case "agents.pause":
		return api.agentStatusCommand(params, "paused")
	case "agents.resume":
		return api.agentsResume(params)
	case "agents.kill":
		return api.agentStatusCommand(params, "interrupted")
	case "agents.pauseMany":
		return api.agentManyStatusCommand(params, "paused")
	case "agents.resumeMany":
		return api.agentManyStatusCommand(params, "active")
	case "agents.killMany":
		return api.agentManyStatusCommand(params, "interrupted")
	default:
		return nil, fmt.Errorf("unknown method: %s", method)
	}
}

func (api *ControlAPI) agentsList() (json.RawMessage, error) {
	if api.Vault == nil {
		return nil, fmt.Errorf("vault not available")
	}
	agents, err := api.Vault.ListAgents()
	if err != nil {
		return nil, err
	}
	type agentSummary struct {
		ID                 string `json:"id"`
		Name               string `json:"name"`
		Status             string `json:"status"`
		Task               string `json:"task"`
		TotalTokens        int    `json:"totalTokens"`
		Fingerprint        string `json:"fingerprint"`
		FingerprintSummary string `json:"fingerprintSummary"`
		ContextID          string `json:"contextId,omitempty"`
	}
	out := make([]agentSummary, 0, len(agents))
	for _, agent := range agents {
		meta, _ := vault.ParseAgentMetadata(agent.Metadata)
		out = append(out, agentSummary{
			ID:                 agent.ID,
			Name:               agent.Name,
			Status:             agent.Status,
			Task:               agent.Task,
			TotalTokens:        agent.TotalTokens,
			Fingerprint:        agent.Fingerprint,
			FingerprintSummary: vault.FingerprintSummary(agent.Fingerprint),
			ContextID:          meta.ContextID,
		})
	}
	return json.Marshal(map[string]any{"agents": out})
}

func (api *ControlAPI) agentsGetMessages(params json.RawMessage) (json.RawMessage, error) {
	if api.Vault == nil {
		return nil, fmt.Errorf("vault not available")
	}
	var p struct {
		AgentID string `json:"agentId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(p.AgentID)
	if agentID == "" {
		return nil, fmt.Errorf("agentId is required")
	}
	messages, err := api.Vault.GetMessages(agentID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"messages": messages})
}

func (api *ControlAPI) agentsSpawn(params json.RawMessage) (json.RawMessage, error) {
	if api.Orchestrator == nil {
		return nil, fmt.Errorf("orchestrator not available")
	}
	if api.Vault == nil {
		return nil, fmt.Errorf("vault not available")
	}
	var p struct {
		Name      string `json:"name"`
		Task      string `json:"task"`
		ContextID string `json:"contextId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(p.Name)
	task := strings.TrimSpace(p.Task)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if task == "" {
		return nil, fmt.Errorf("task is required")
	}

	fp, err := vault.GenerateFingerprint(name)
	if err != nil {
		fp = "{}"
	}
	agent, err := api.Vault.CreateAgent(name, task, fp)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ContextID) != "" {
		metadata := vault.MarshalAgentMetadata(vault.AgentMetadata{ContextID: strings.TrimSpace(p.ContextID)})
		if err := api.Vault.UpdateAgentMetadata(agent.ID, metadata); err == nil {
			agent.Metadata = metadata
		}
	}
	if agent.ProxyConfig != "" {
		var pc proxy.ProxyConfig
		if json.Unmarshal([]byte(agent.ProxyConfig), &pc) == nil {
			if geo, geoErr := proxy.ResolveGeo(pc); geoErr == nil {
				if synced, syncErr := proxy.SyncFingerprintToProxy(agent.Fingerprint, geo); syncErr == nil {
					agent.Fingerprint = synced
					_ = api.Vault.UpdateAgentFingerprint(agent.ID, synced)
				}
			}
		}
	}

	configPath, cleanup, err := api.agentRuntimeConfig(agent)
	if err != nil {
		_ = api.Vault.UpdateAgentStatus(agent.ID, "error")
		_ = api.Vault.AppendMessage(agent.ID, "system", "Failed to prepare runtime: "+err.Error(), 0)
		return json.Marshal(map[string]any{"agentId": agent.ID})
	}
	intro := nanoclaw.IntroMessage(name, task)
	sessionName := "vulpine-" + agent.ID
	if _, err := api.Orchestrator.Agents.SpawnWithSessionIsolated(agent.ID, intro, sessionName, configPath, cleanup); err != nil {
		_ = api.Vault.UpdateAgentStatus(agent.ID, "error")
		_ = api.Vault.AppendMessage(agent.ID, "system", "Failed to start: "+err.Error(), 0)
		return json.Marshal(map[string]any{"agentId": agent.ID})
	}
	_ = api.Vault.UpdateAgentStatus(agent.ID, "active")
	_ = api.Vault.AppendMessage(agent.ID, "system", "Agent starting...", 0)
	return json.Marshal(map[string]any{"agentId": agent.ID})
}

func (api *ControlAPI) agentsResume(params json.RawMessage) (json.RawMessage, error) {
	if api.Orchestrator == nil {
		return nil, fmt.Errorf("orchestrator not available")
	}
	if api.Vault == nil {
		return nil, fmt.Errorf("vault not available")
	}
	var p struct {
		AgentID string `json:"agentId"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(p.AgentID)
	if agentID == "" {
		return nil, fmt.Errorf("agentId is required")
	}
	agent, err := api.Vault.GetAgent(agentID)
	if err != nil {
		return nil, err
	}
	message := strings.TrimSpace(p.Message)
	if message != "" {
		_ = api.Vault.AppendMessage(agentID, "user", message, 0)
	} else {
		message = "Continue from the saved session and resume the current task."
	}
	configPath, cleanup, err := api.agentRuntimeConfig(agent)
	if err != nil {
		return nil, err
	}
	if _, err := api.Orchestrator.Agents.SpawnWithSessionIsolated(agentID, message, "vulpine-"+agentID, configPath, cleanup); err != nil {
		return nil, err
	}
	_ = api.Vault.UpdateAgentStatus(agentID, "active")
	return json.Marshal(map[string]any{"agentId": agentID})
}

func (api *ControlAPI) agentStatusCommand(params json.RawMessage, status string) (json.RawMessage, error) {
	var p struct {
		AgentID string `json:"agentId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(p.AgentID)
	if agentID == "" {
		return nil, fmt.Errorf("agentId is required")
	}
	if err := api.applyAgentStatus(agentID, status); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"agentId": agentID})
}

func (api *ControlAPI) agentManyStatusCommand(params json.RawMessage, status string) (json.RawMessage, error) {
	var p struct {
		AgentIDs []string `json:"agentIds"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	failures := map[string]string{}
	for _, rawID := range p.AgentIDs {
		agentID := strings.TrimSpace(rawID)
		if agentID == "" {
			continue
		}
		if err := api.applyAgentStatus(agentID, status); err != nil {
			failures[agentID] = err.Error()
		}
	}
	return json.Marshal(map[string]any{"failures": failures})
}

func (api *ControlAPI) applyAgentStatus(agentID, status string) error {
	switch status {
	case "paused":
		if api.Orchestrator != nil && api.Orchestrator.Agents != nil {
			if err := api.Orchestrator.Agents.PauseAgent(agentID); err != nil && !strings.Contains(err.Error(), "not found") {
				return err
			}
		}
	case "interrupted":
		if api.Orchestrator != nil {
			if err := api.Orchestrator.KillAgent(agentID); err != nil && !strings.Contains(err.Error(), "not found") {
				return err
			}
		}
	}
	if api.Vault != nil {
		return api.Vault.UpdateAgentStatus(agentID, status)
	}
	return nil
}

func (api *ControlAPI) agentRuntimeConfig(agent *vault.Agent) (string, func(), error) {
	if agent == nil {
		return "", nil, fmt.Errorf("agent not found")
	}
	if api.Config != nil {
		if err := config.RepairNanoClawProfile(api.activeFoxbridgeCDPURL()); err != nil {
			return "", nil, fmt.Errorf("repair nanoclaw profile: %w", err)
		}
	}
	meta, err := vault.ParseAgentMetadata(agent.Metadata)
	if err != nil {
		return "", nil, fmt.Errorf("parse agent metadata: %w", err)
	}
	if meta.ContextID == "" {
		return nanoclaw.PrepareRuntimeConfig(config.NanoClawConfigPath())
	}
	if api.Orchestrator == nil {
		return "", nil, fmt.Errorf("orchestrator not available")
	}
	return api.Orchestrator.PrepareScopedNanoClawConfig(meta.ContextID)
}

func (api *ControlAPI) statusGet() (json.RawMessage, error) {
	route, source := api.browserRoute()
	out := map[string]any{
		"kernel_running":              false,
		"kernel_pid":                  0,
		"kernel_headless":             false,
		"browser_route":               route,
		"browser_route_source":        source,
		"browser_window":              api.browserWindow(),
		"nanoclaw_daemon_running":     false,
		"nanoclaw_profile_configured": config.NanoClawProfileBrowserRoute() != "",
	}
	if api.Kernel != nil {
		out["kernel_running"] = api.Kernel.Running()
		out["kernel_pid"] = api.Kernel.PID()
		out["kernel_headless"] = api.Kernel.IsHeadless()
	}
	if api.Daemon != nil {
		out["nanoclaw_daemon_running"] = api.Daemon.Running()
	}
	if api.Orchestrator != nil {
		status := api.Orchestrator.Status()
		out["orchestrator"] = &status
		out["kernel_running"] = status.KernelRunning
		out["kernel_pid"] = status.KernelPID
		out["pool_available"] = status.PoolAvailable
		out["pool_active"] = status.PoolActive
		out["pool_total"] = status.PoolTotal
		out["active_agents"] = status.ActiveAgents
		out["total_citizens"] = status.TotalCitizens
		out["total_templates"] = status.TotalTemplates
		out["total_cost_usd"] = status.TotalCostUSD
	}
	return json.Marshal(out)
}

func (api *ControlAPI) browserRoute() (string, string) {
	switch {
	case api.Kernel == nil:
		return "disabled", "server"
	case api.activeFoxbridgeCDPURL() != "":
		return "camoufox", "runtime"
	case api.Kernel != nil && api.Kernel.IsHeadless():
		return "headless", "kernel"
	default:
		return "direct", "kernel"
	}
}

func (api *ControlAPI) activeFoxbridgeCDPURL() string {
	if api.Config == nil {
		return ""
	}
	cdpURL := strings.TrimSpace(api.Config.FoxbridgeCDPURL)
	if cdpURL == "" {
		return ""
	}
	if api.FoxbridgeRunning != nil && !api.FoxbridgeRunning() {
		return ""
	}
	return cdpURL
}

func (api *ControlAPI) browserWindow() string {
	if api.Kernel == nil {
		return "n/a"
	}
	if api.Kernel.IsHeadless() {
		return "headless"
	}
	w := api.Kernel.Window()
	if w == nil {
		return "unavailable"
	}
	visible, found := w.Status()
	if !found {
		return "unavailable"
	}
	if visible {
		return "visible"
	}
	return "hidden"
}
