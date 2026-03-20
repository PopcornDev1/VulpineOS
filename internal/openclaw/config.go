package openclaw

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// VulpineOSConfig generates an openclaw.json that routes browser ops through VulpineOS MCP.
type VulpineOSConfig struct {
	Plugins  PluginsConfig  `json:"plugins"`
	Browser  BrowserConfig  `json:"browser"`
}

type PluginsConfig struct {
	MCP MCPConfig `json:"mcp"`
}

type MCPConfig struct {
	Servers map[string]MCPServerConfig `json:"servers"`
}

type MCPServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

type BrowserConfig struct {
	Enabled bool `json:"enabled"`
}

// GenerateConfig creates an openclaw.json config file that disables the built-in
// Chromium browser and routes all browser operations through the VulpineOS MCP server.
func GenerateConfig(vulpineosBinary string, wsURL string) (*VulpineOSConfig, error) {
	if vulpineosBinary == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("get executable path: %w", err)
		}
		vulpineosBinary = exe
	}

	args := []string{"--mcp-server"}
	if wsURL != "" {
		args = append(args, "--mcp-connect", wsURL)
	}

	return &VulpineOSConfig{
		Plugins: PluginsConfig{
			MCP: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"vulpineos": {
						Command: vulpineosBinary,
						Args:    args,
						Env:     map[string]string{},
					},
				},
			},
		},
		Browser: BrowserConfig{
			Enabled: false, // Disable Chromium — use VulpineOS instead
		},
	}, nil
}

// WriteConfig writes the OpenClaw config to a file.
func WriteConfig(config *VulpineOSConfig, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}

	path := filepath.Join(dir, "openclaw.json")
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}

	return path, nil
}

// FindBundledOpenClaw checks if OpenClaw is bundled alongside the vulpineos binary.
func FindBundledOpenClaw() (nodePath, openclawPath string, found bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", false
	}
	dir := filepath.Dir(exe)

	// Check for bundled openclaw directory
	openclawDir := filepath.Join(dir, "openclaw")
	nodeBin := filepath.Join(openclawDir, "node")
	openclawMain := filepath.Join(openclawDir, "node_modules", "openclaw")

	if _, err := os.Stat(nodeBin); err != nil {
		// Try system node
		nodeBin = "node"
	}

	if _, err := os.Stat(openclawMain); err == nil {
		return nodeBin, openclawMain, true
	}

	// Check if openclaw is globally installed
	if path, err := filepath.Glob("/usr/local/lib/node_modules/openclaw"); err == nil && len(path) > 0 {
		return "node", path[0], true
	}

	return "", "", false
}
