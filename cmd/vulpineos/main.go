package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"path/filepath"

	"vulpineos/internal/agentbus"
	"vulpineos/internal/config"
	"vulpineos/internal/costtrack"
	"vulpineos/internal/foxbridge"
	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
	"vulpineos/internal/mcp"
	"vulpineos/internal/openclaw"
	"vulpineos/internal/orchestrator"
	"vulpineos/internal/pagecache"
	"vulpineos/internal/pool"
	"vulpineos/internal/recording"
	"vulpineos/internal/remote"
	"vulpineos/internal/tui"
	"vulpineos/internal/tui/loading"
	"vulpineos/internal/tui/setup"
	"vulpineos/internal/vault"
	"vulpineos/internal/webhooks"
)

func main() {
	var (
		binaryPath = flag.String("binary", "", "Path to VulpineOS/Camoufox binary")
		headless   = flag.Bool("headless", false, "Run in headless mode")
		profileDir = flag.String("profile", "", "Firefox profile directory")
		remoteAddr = flag.String("remote", "", "Connect to remote VulpineOS (wss://host:port/ws)")
		serve      = flag.Bool("serve", false, "Run as remote-accessible server")
		port       = flag.Int("port", 8443, "Server port (with --serve)")
		apiKey     = flag.String("api-key", "", "API key for remote authentication")
		tlsCert    = flag.String("tls-cert", "", "TLS certificate file (with --serve)")
		tlsKey     = flag.String("tls-key", "", "TLS key file (with --serve)")
		noTLS      = flag.Bool("no-tls", false, "Disable TLS (plain ws:// instead of wss://)")
		noBrowser  = flag.Bool("no-browser", false, "Start TUI without launching browser (demo mode)")
		mcpServer  = flag.Bool("mcp-server", false, "Run as MCP stdio server (used by OpenClaw)")
		mcpConnect = flag.String("mcp-connect", "", "WebSocket URL to connect MCP server to remote kernel")
		_          = mcpConnect // M4 remote MCP — future use
	)
	flag.Parse()

	var err error
	switch {
	case *mcpServer:
		err = runMCPServer(*binaryPath, *headless, *profileDir)
	case *remoteAddr != "":
		err = runRemote(*remoteAddr, *apiKey)
	case *serve:
		err = runServe(*binaryPath, *headless, *profileDir, *port, *apiKey, *tlsCert, *tlsKey, *noTLS)
	default:
		err = runLocal(*binaryPath, *headless, *profileDir, *noBrowser)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// runMCPServer runs as an MCP stdio server for OpenClaw integration.
// It connects to a running VulpineOS kernel and translates MCP tool calls to Juggler protocol.
func runMCPServer(binaryPath string, headless bool, profileDir string) error {
	k := kernel.New()
	if err := k.Start(kernel.Config{
		BinaryPath: binaryPath,
		Headless:   headless,
		ProfileDir: profileDir,
	}); err != nil {
		return fmt.Errorf("start kernel: %w", err)
	}
	defer k.Stop()

	client := k.Client()

	// Start embedded foxbridge CDP server FIRST — its Browser.enable
	// call sets up event subscriptions needed by both foxbridge and MCP.
	fb := foxbridge.New()
	if err := fb.StartEmbeddedMode(client, 9222); err != nil {
		log.Printf("foxbridge embedded mode failed: %v", err)
		// Fall back to manual Browser.enable for MCP
		if _, err := client.Call("", "Browser.enable", map[string]interface{}{
			"attachToDefaultContext": true,
		}); err != nil {
			return fmt.Errorf("Browser.enable: %w", err)
		}
	} else {
		defer fb.Stop()
		log.Printf("foxbridge CDP proxy on %s", fb.CDPURL())
	}

	server := mcp.NewServer(client)
	return server.Run()
}

// runLocal starts the kernel and TUI locally.
func runLocal(binaryPath string, headless bool, profileDir string, noBrowser bool) error {
	// Check if first-time setup is needed
	cfg, err := config.Load()
	if err != nil {
		log.Printf("Warning: could not load config: %v", err)
		cfg = &config.Config{}
	}

	if cfg.NeedsSetup() {
		// Run setup wizard
		setupModel := setup.New()
		p := tea.NewProgram(setupModel, tea.WithAltScreen())
		result, err := p.Run()
		if err != nil {
			return fmt.Errorf("setup wizard: %w", err)
		}
		finalModel := result.(setup.Model)
		if !finalModel.Done() {
			return nil // user quit setup
		}
		cfg = finalModel.Config()
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		// Generate OpenClaw config
		exe, _ := os.Executable()
		camoufox := binaryPath
		if err := cfg.GenerateOpenClawConfig(exe, camoufox); err != nil {
			log.Printf("Warning: could not generate OpenClaw config: %v", err)
		}
	}

	// Store binary path in config if provided
	if binaryPath != "" && cfg.BinaryPath != binaryPath {
		cfg.BinaryPath = binaryPath
		cfg.Save()
	}

	// Always regenerate openclaw.json to ensure it matches current config
	if cfg.SetupComplete {
		exe, _ := os.Executable()
		if genErr := cfg.GenerateOpenClawConfig(exe, cfg.BinaryPath); genErr != nil {
			log.Printf("Warning: could not generate OpenClaw config: %v", genErr)
		}
	}

	var k *kernel.Kernel
	var client *juggler.Client
	var orch *orchestrator.Orchestrator
	var v *vault.DB
	var gw *openclaw.Gateway
	var fb *foxbridge.Process
	var startErr error

	if !noBrowser {
		// Show loading spinner while kernel starts
		loader := loading.New("Launching VulpineOS")
		loaderProg := tea.NewProgram(loader, tea.WithAltScreen())

		go func() {
			// Open vault
			v, _ = vault.Open()

			// Start kernel
			k = kernel.New()
			startErr = k.Start(kernel.Config{
				BinaryPath: binaryPath,
				Headless:   headless,
				ProfileDir: profileDir,
			})
			if startErr == nil {
				client = k.Client()

				// Create orchestrator with optional subsystems
				if v != nil {
					model := ""
					if cfg != nil {
						model = cfg.Model
					}
					orch = orchestrator.New(k, client, v, pool.DefaultConfig(), "openclaw", orchestrator.Opts{
						AgentBus:  agentbus.New(),
						Costs:     costtrack.New(model),
						Webhooks:  webhooks.New(),
						Recording: recording.NewRecorder(),
						PageCache: pagecache.New(filepath.Join(config.Dir(), "pagecache")),
					})
					orch.Start()
				}
			}

			// Start foxbridge as an embedded CDP server sharing the kernel's Juggler client.
			// This avoids launching a second Firefox — OpenClaw connects to the same kernel.
			if client != nil {
				fb = foxbridge.New()
				fbErr := fb.StartEmbeddedMode(client, 9222)
				if fbErr != nil {
					log.Printf("embedded foxbridge not available: %v (OpenClaw will use built-in Chrome)", fbErr)
					fb = nil
				} else {
					// Set CDP URL in config so OpenClaw routes through foxbridge
					cfg.FoxbridgeCDPURL = fb.CDPURL()
					exe, _ := os.Executable()
					cfg.GenerateOpenClawConfig(exe, binaryPath)
					log.Printf("foxbridge embedded — OpenClaw browser routed through Camoufox at %s", fb.CDPURL())
				}
			}

			// Start OpenClaw gateway for browser support
			mgr := openclaw.NewManager("")
			if mgr.OpenClawInstalled() {
				gw = openclaw.NewGateway("")
				if gwErr := gw.Start(); gwErr != nil {
					log.Printf("Warning: OpenClaw gateway failed to start: %v (browser tools won't work)", gwErr)
				}
			}

			loaderProg.Send(loading.DoneMsg{})
		}()

		result, err := loaderProg.Run()
		if err != nil {
			return fmt.Errorf("loading screen: %w", err)
		}
		if !result.(loading.Model).Done() {
			// User quit during loading
			if k != nil {
				k.Stop()
			}
			if fb != nil {
				fb.Stop()
			}
			return nil
		}
		if startErr != nil {
			return fmt.Errorf("start kernel: %w", startErr)
		}
		defer k.Stop()
		if fb != nil {
			defer fb.Stop()
		}
		if v != nil {
			defer v.Close()
		}
		if orch != nil {
			defer orch.Close()
		}
		if gw != nil {
			defer gw.Stop()
		}
	}

	// Create TUI with event subscriptions in place before Browser.enable
	app := tui.NewApp(k, client, orch, v, cfg)

	if client != nil {
		if _, err := client.Call("", "Browser.enable", map[string]interface{}{
			"attachToDefaultContext": true,
		}); err != nil {
			return fmt.Errorf("Browser.enable: %w", err)
		}
	}
	p := tea.NewProgram(app, tea.WithAltScreen())
	_, runErr := p.Run()
	return runErr
}

// runRemote connects to a remote VulpineOS server and launches the TUI.
func runRemote(addr string, apiKey string) error {
	ctx := context.Background()
	rc, err := remote.Dial(ctx, addr, apiKey)
	if err != nil {
		return fmt.Errorf("connect to remote: %w", err)
	}
	defer rc.Close()

	// Create a Juggler client over the WebSocket transport
	client := juggler.NewClient(rc)
	defer client.Close()

	if _, err := client.Call("", "Browser.enable", map[string]interface{}{
		"attachToDefaultContext": true,
	}); err != nil {
		return fmt.Errorf("Browser.enable (remote): %w", err)
	}

	app := tui.NewApp(nil, client, nil, nil, nil)
	p := tea.NewProgram(app, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

// runServe starts the kernel and exposes it via WebSocket server.
func runServe(binaryPath string, headless bool, profileDir string, port int, apiKey string, tlsCert, tlsKey string, noTLS bool) error {
	k := kernel.New()
	if err := k.Start(kernel.Config{
		BinaryPath: binaryPath,
		Headless:   headless,
		ProfileDir: profileDir,
	}); err != nil {
		return fmt.Errorf("start kernel: %w", err)
	}
	defer k.Stop()

	client := k.Client()
	if _, err := client.Call("", "Browser.enable", map[string]interface{}{
		"attachToDefaultContext": true,
	}); err != nil {
		return fmt.Errorf("Browser.enable: %w", err)
	}

	addr := fmt.Sprintf(":%d", port)
	server := remote.NewServer(addr, apiKey, client)

	// Forward telemetry events to connected clients
	for _, event := range []string{
		"Browser.telemetryUpdate",
		"Browser.injectionAttemptDetected",
		"Browser.trustWarmingStateChanged",
		"Browser.attachedToTarget",
		"Browser.detachedFromTarget",
	} {
		evt := event
		client.Subscribe(evt, func(_ string, params json.RawMessage) {
			server.BroadcastEvent(evt, params)
		})
	}

	// Serve the web panel from embedded files
	if panelFS := PanelFS(); panelFS != nil {
		remote.ServePanel(server.Mux(), panelFS)
		log.Println("web panel available at /")
	}

	// Start embedded foxbridge CDP server
	fb := foxbridge.New()
	if err := fb.StartEmbeddedMode(client, 9222); err != nil {
		log.Printf("foxbridge: %v (CDP proxy not available)", err)
	} else {
		defer fb.Stop()
		log.Printf("foxbridge CDP on %s", fb.CDPURL())
	}

	log.Printf("VulpineOS kernel running (PID %d)", k.PID())

	if noTLS {
		log.Printf("TLS disabled — serving plain ws:// on port %d", port)
		return server.Start()
	}

	// Resolve TLS certificates
	certFile, keyFile := tlsCert, tlsKey
	if certFile == "" || keyFile == "" {
		// Auto-generate self-signed certs
		auto, autoKey, err := remote.GenerateSelfSignedCert()
		if err != nil {
			return fmt.Errorf("auto-generate TLS cert: %w", err)
		}
		certFile, keyFile = auto, autoKey
		log.Printf("Using auto-generated self-signed TLS certificate")
	}

	fp, err := remote.CertFingerprint(certFile)
	if err == nil {
		log.Printf("TLS cert fingerprint: %s", fp)
	}

	return server.StartTLS(certFile, keyFile)
}
