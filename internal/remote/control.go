package remote

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"vulpineos/internal/agentprompt"
	"vulpineos/internal/config"
	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
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
	case "settings.get":
		return api.settingsGet()
	case "config.providers":
		return api.configProviders()
	case "config.set":
		return api.configSet(params)
	case "proxies.add":
		return api.proxiesAdd(params)
	case "proxies.delete":
		return api.proxiesDelete(params)
	case "proxies.test":
		return api.proxiesTest(params)
	case "skills.set":
		return api.skillsSet(params)
	case "agents.list":
		return api.agentsList()
	case "agents.getMessages":
		return api.agentsGetMessages(params)
	case "agents.getSessionLog":
		return api.agentsGetSessionLog(params)
	case "agents.create":
		return api.agentsCreate(params)
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

type configSummary struct {
	Provider                string  `json:"provider"`
	ProviderName            string  `json:"providerName"`
	Model                   string  `json:"model"`
	APIKeySet               bool    `json:"apiKeySet"`
	SetupComplete           bool    `json:"setupComplete"`
	ResizePanelsWithArrows  bool    `json:"resizePanelsWithArrows"`
	DefaultBudgetMaxCostUSD float64 `json:"defaultBudgetMaxCostUsd,omitempty"`
	DefaultBudgetMaxTokens  int64   `json:"defaultBudgetMaxTokens,omitempty"`
}

type providerSummary struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	EnvVar       string   `json:"envVar"`
	DefaultModel string   `json:"defaultModel"`
	Models       []string `json:"models"`
	NeedsKey     bool     `json:"needsKey"`
}

type proxySummary struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Type    string `json:"type"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Country string `json:"country,omitempty"`
	Latency string `json:"latency"`
}

type skillSummary struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

func (api *ControlAPI) settingsGet() (json.RawMessage, error) {
	out := map[string]any{
		"config":    summarizeConfig(api.Config),
		"proxies":   []proxySummary{},
		"skills":    summarizeSkills(api.Config),
		"providers": summarizeProviders(),
	}
	if api.Vault != nil {
		proxies, err := api.Vault.ListProxies()
		if err != nil {
			return nil, err
		}
		out["proxies"] = summarizeProxies(proxies)
	}
	return json.Marshal(out)
}

func (api *ControlAPI) configProviders() (json.RawMessage, error) {
	return json.Marshal(map[string]any{"providers": summarizeProviders()})
}

func (api *ControlAPI) configSet(params json.RawMessage) (json.RawMessage, error) {
	if api.Config == nil {
		return nil, fmt.Errorf("config not available")
	}
	var p struct {
		Provider               string `json:"provider"`
		Model                  string `json:"model"`
		APIKey                 string `json:"apiKey"`
		KeepAPIKey             bool   `json:"keepApiKey"`
		SetupComplete          *bool  `json:"setupComplete"`
		ResizePanelsWithArrows *bool  `json:"resizePanelsWithArrows"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if provider := strings.TrimSpace(p.Provider); provider != "" {
		api.Config.Provider = provider
	}
	if model := strings.TrimSpace(p.Model); model != "" {
		api.Config.Model = model
	}
	if key := strings.TrimSpace(p.APIKey); key != "" {
		api.Config.APIKey = key
	} else if !p.KeepAPIKey {
		// A blank key is treated as "preserve existing" by default so remote
		// settings never need to read the host secret back.
	}
	if p.ResizePanelsWithArrows != nil {
		api.Config.ResizePanelsWithArrows = *p.ResizePanelsWithArrows
	}
	api.Config.RefreshSetupComplete()
	if p.SetupComplete != nil && !*p.SetupComplete {
		api.Config.SetupComplete = false
	}
	if err := api.Config.Save(); err != nil {
		return nil, err
	}
	return json.Marshal(summarizeConfig(api.Config))
}

func (api *ControlAPI) proxiesAdd(params json.RawMessage) (json.RawMessage, error) {
	if api.Vault == nil {
		return nil, fmt.Errorf("vault not available")
	}
	var p struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	pc, err := proxy.ParseProxyURL(p.URL)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(pc)
	if _, err := api.Vault.AddProxy(string(data), "", pc.String()); err != nil {
		return nil, err
	}
	return api.proxiesListResult()
}

func (api *ControlAPI) proxiesDelete(params json.RawMessage) (json.RawMessage, error) {
	if api.Vault == nil {
		return nil, fmt.Errorf("vault not available")
	}
	var p struct {
		ProxyID string `json:"proxyId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(p.ProxyID)
	if id == "" {
		return nil, fmt.Errorf("proxyId is required")
	}
	if err := api.Vault.DeleteProxy(id); err != nil {
		return nil, err
	}
	return api.proxiesListResult()
}

func (api *ControlAPI) proxiesTest(params json.RawMessage) (json.RawMessage, error) {
	if api.Vault == nil {
		return nil, fmt.Errorf("vault not available")
	}
	var p struct {
		ProxyID string `json:"proxyId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(p.ProxyID)
	if id == "" {
		return nil, fmt.Errorf("proxyId is required")
	}
	stored, err := api.Vault.GetProxy(id)
	if err != nil {
		return nil, err
	}
	var pc proxy.ProxyConfig
	if err := json.Unmarshal([]byte(stored.Config), &pc); err != nil {
		return nil, fmt.Errorf("invalid proxy config")
	}
	latency, err := proxy.TestProxy(pc)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"proxyId": id,
		"latency": fmt.Sprintf("%dms", latency),
	}
	if geo, err := proxy.ResolveGeo(pc); err == nil {
		out["exitIp"] = geo.IP
		out["country"] = geo.Country
		if geoJSON, err := json.Marshal(geo); err == nil {
			_ = api.Vault.UpdateProxyGeo(id, string(geoJSON))
		}
	}
	return json.Marshal(out)
}

func (api *ControlAPI) proxiesListResult() (json.RawMessage, error) {
	proxies, err := api.Vault.ListProxies()
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"proxies": summarizeProxies(proxies)})
}

func (api *ControlAPI) skillsSet(params json.RawMessage) (json.RawMessage, error) {
	if api.Config == nil {
		return nil, fmt.Errorf("config not available")
	}
	var p struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if p.Enabled {
		api.Config.AddGlobalSkill(name, nil)
	} else {
		api.Config.RemoveGlobalSkill(name)
	}
	if err := api.Config.Save(); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"skills": summarizeSkills(api.Config)})
}

func summarizeConfig(cfg *config.Config) configSummary {
	if cfg == nil {
		return configSummary{}
	}
	providerName := cfg.Provider
	if p := config.GetProvider(cfg.Provider); p != nil {
		providerName = p.Name
	}
	return configSummary{
		Provider:                cfg.Provider,
		ProviderName:            providerName,
		Model:                   cfg.Model,
		APIKeySet:               strings.TrimSpace(cfg.APIKey) != "",
		SetupComplete:           cfg.SetupComplete,
		ResizePanelsWithArrows:  cfg.ResizePanelsWithArrows,
		DefaultBudgetMaxCostUSD: cfg.DefaultBudgetMaxCostUSD,
		DefaultBudgetMaxTokens:  cfg.DefaultBudgetMaxTokens,
	}
}

func summarizeProviders() []providerSummary {
	providers := config.MergedProviders()
	out := make([]providerSummary, 0, len(providers))
	for _, p := range providers {
		out = append(out, providerSummary{
			ID:           p.ID,
			Name:         p.Name,
			EnvVar:       p.EnvVar,
			DefaultModel: p.DefaultModel,
			Models:       append([]string(nil), p.Models...),
			NeedsKey:     p.NeedsKey,
		})
	}
	return out
}

func summarizeSkills(cfg *config.Config) []skillSummary {
	if cfg == nil {
		return []skillSummary{}
	}
	out := make([]skillSummary, 0, len(cfg.GlobalSkills))
	for _, s := range cfg.GlobalSkills {
		out = append(out, skillSummary{Name: s.Name, Enabled: s.Enabled})
	}
	return out
}

func summarizeProxies(proxies []vault.StoredProxy) []proxySummary {
	out := make([]proxySummary, 0, len(proxies))
	for _, stored := range proxies {
		item := proxySummary{ID: stored.ID, Latency: "untested"}
		var pc proxy.ProxyConfig
		if json.Unmarshal([]byte(stored.Config), &pc) == nil {
			item.Type = pc.Type
			item.Host = pc.Host
			item.Port = pc.Port
			item.Label = pc.String()
		} else {
			item.Label = safeProxyLabel(stored.Label)
		}
		if item.Label == "" {
			item.Label = stored.ID
		}
		var geo struct {
			Country string `json:"country"`
		}
		if json.Unmarshal([]byte(stored.Geo), &geo) == nil {
			item.Country = geo.Country
		}
		out = append(out, item)
	}
	return out
}

func safeProxyLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	if pc, err := proxy.ParseProxyURL(label); err == nil {
		return pc.String()
	}
	return label
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

func (api *ControlAPI) agentsGetSessionLog(params json.RawMessage) (json.RawMessage, error) {
	var p struct {
		AgentID    string `json:"agentId"`
		Offset     int64  `json:"offset"`
		LimitBytes int64  `json:"limitBytes"`
		Tail       bool   `json:"tail"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(p.AgentID)
	if agentID == "" {
		return nil, fmt.Errorf("agentId is required")
	}
	if api.Vault != nil {
		if _, err := api.Vault.GetAgent(agentID); err != nil {
			return nil, err
		}
	}
	path, err := sessionLogPathForAgentID(agentID)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return json.Marshal(map[string]any{
				"agentId":    agentID,
				"exists":     false,
				"redacted":   true,
				"content":    "",
				"offset":     int64(0),
				"nextOffset": int64(0),
				"eof":        true,
			})
		}
		return nil, err
	}
	size := info.Size()
	limit := p.LimitBytes
	if limit <= 0 || limit > 65536 {
		limit = 65536
	}
	offset := p.Offset
	if p.Tail && size > limit {
		offset = size - limit
	}
	if offset < 0 {
		offset = 0
	}
	if offset > size {
		offset = size
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, 0); err != nil {
		return nil, err
	}
	buf := make([]byte, limit)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return nil, err
	}
	next := offset + int64(n)
	content := redactRemoteLogText(string(buf[:n]))
	return json.Marshal(map[string]any{
		"agentId":    agentID,
		"exists":     true,
		"sizeBytes":  size,
		"offset":     offset,
		"nextOffset": next,
		"eof":        next >= size,
		"truncated":  next < size,
		"redacted":   true,
		"content":    content,
	})
}

type agentCreateParams struct {
	Name      string `json:"name"`
	Task      string `json:"task"`
	ContextID string `json:"contextId"`
}

func decodeAgentCreateParams(params json.RawMessage) (agentCreateParams, error) {
	var p agentCreateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return p, err
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Task = strings.TrimSpace(p.Task)
	p.ContextID = strings.TrimSpace(p.ContextID)
	if p.Name == "" {
		return p, fmt.Errorf("name is required")
	}
	if p.Task == "" {
		return p, fmt.Errorf("task is required")
	}
	return p, nil
}

func (api *ControlAPI) createAgentRecord(p agentCreateParams) (*vault.Agent, error) {
	if api.Vault == nil {
		return nil, fmt.Errorf("vault not available")
	}

	// Seed the fingerprint by a unique id, not the (possibly-colliding) name.
	agentID := vault.NewID()
	fp, err := vault.GenerateFingerprint(agentID)
	if err != nil {
		fp = "{}"
	}
	agent, err := api.Vault.CreateAgentWithID(agentID, p.Name, p.Task, fp)
	if err != nil {
		return nil, err
	}
	if p.ContextID != "" {
		metadata := vault.MarshalAgentMetadata(vault.AgentMetadata{ContextID: p.ContextID})
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
	return agent, nil
}

func (api *ControlAPI) agentsCreate(params json.RawMessage) (json.RawMessage, error) {
	p, err := decodeAgentCreateParams(params)
	if err != nil {
		return nil, err
	}
	agent, err := api.createAgentRecord(p)
	if err != nil {
		return nil, err
	}
	agent.Status = "ready"
	_ = api.Vault.UpdateAgentStatus(agent.ID, "ready")
	return json.Marshal(map[string]any{"agentId": agent.ID})
}

func (api *ControlAPI) agentsSpawn(params json.RawMessage) (json.RawMessage, error) {
	if api.Orchestrator == nil {
		return nil, fmt.Errorf("orchestrator not available")
	}
	p, err := decodeAgentCreateParams(params)
	if err != nil {
		return nil, err
	}
	agent, err := api.createAgentRecord(p)
	if err != nil {
		return nil, err
	}

	configPath, cleanup, err := api.agentRuntimeConfig(agent)
	if err != nil {
		_ = api.Vault.UpdateAgentStatus(agent.ID, "error")
		_ = api.Vault.AppendMessage(agent.ID, "system", "Failed to prepare runtime: "+err.Error(), 0)
		return json.Marshal(map[string]any{"agentId": agent.ID})
	}
	intro := agentprompt.IntroMessage(p.Name, p.Task)
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
		AgentID        string `json:"agentId"`
		Message        string `json:"message"`
		DisplayContent string `json:"displayContent"`
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
		_ = api.Vault.AppendMessageWithDisplay(agentID, "user", message, strings.TrimSpace(p.DisplayContent), 0)
	} else {
		message = "Continue from the saved session and resume the current task."
	}
	configPath, cleanup, err := api.agentRuntimeConfig(agent)
	if err != nil {
		return nil, err
	}
	turnMessage := message
	if history, err := api.Vault.GetRecentMessages(agentID, 16); err == nil {
		turnMessage = agentprompt.FormatTurnPrompt(history, message)
	}
	if _, err := api.Orchestrator.Agents.SpawnWithSessionIsolated(agentID, turnMessage, "vulpine-"+agentID, configPath, cleanup); err != nil {
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
	if api.Orchestrator == nil {
		return "", nil, fmt.Errorf("orchestrator not available")
	}
	contextID, err := api.Orchestrator.EnsureAgentBrowserContext(agent)
	if err != nil {
		return "", nil, err
	}
	cleanup, err := api.Orchestrator.AgentRuntimeConfig(contextID)
	if err != nil {
		return "", nil, err
	}
	return "", cleanup, nil
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
		"nanoclaw_profile_configured": false,
	}
	if api.Kernel != nil {
		out["kernel_running"] = api.Kernel.Running()
		out["kernel_pid"] = api.Kernel.PID()
		out["kernel_headless"] = api.Kernel.IsHeadless()
	}
	out["nanoclaw_daemon_running"] = daemonRunning(api.Daemon)
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

func daemonRunning(daemon daemonStatus) bool {
	if daemon == nil {
		return false
	}
	value := reflect.ValueOf(daemon)
	if value.Kind() == reflect.Ptr && value.IsNil() {
		return false
	}
	return daemon.Running()
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

func sessionLogPathForAgentID(agentID string) (string, error) {
	id := strings.TrimSpace(agentID)
	if id == "" {
		return "", fmt.Errorf("agent id is required")
	}
	if strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return "", fmt.Errorf("invalid agent id")
	}
	sessionsDir := filepath.Join(config.NanoClawProfileDir(), "agents", "main", "sessions")
	path := filepath.Join(sessionsDir, "vulpine-"+id+".jsonl")
	rel, err := filepath.Rel(sessionsDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("invalid agent id")
	}
	return path, nil
}

var (
	remoteBearerPattern      = regexp.MustCompile(`(?i)(Authorization:\s*Bearer\s+)[^\s",}]+`)
	remoteQuerySecretPattern = regexp.MustCompile(`(?i)([?&](?:token|api[_-]?key|key|secret|password|signature|sig|auth|access[_-]?token)=)[^&\s"']+`)
	remoteJSONSecretPattern  = regexp.MustCompile(`(?i)("(?:token|api[_-]?key|key|secret|password|signature|cookie|authorization|credential)"\s*:\s*")[^"]*(")`)
	remoteKVSecretPattern    = regexp.MustCompile(`(?i)\b(token|api[_-]?key|key|secret|password|signature|cookie|credential)(\s*[:=]\s*)[^\s",}]+`)
)

func redactRemoteLogText(value string) string {
	value = remoteBearerPattern.ReplaceAllString(value, "${1}[redacted]")
	value = remoteQuerySecretPattern.ReplaceAllString(value, "${1}[redacted]")
	value = remoteJSONSecretPattern.ReplaceAllString(value, "${1}[redacted]${2}")
	return remoteKVSecretPattern.ReplaceAllString(value, "${1}${2}[redacted]")
}
