package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
	"vulpineos/internal/remote"
	"vulpineos/internal/tui"
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
		noBrowser  = flag.Bool("no-browser", false, "Start TUI without launching browser (demo mode)")
	)
	flag.Parse()

	var err error
	switch {
	case *remoteAddr != "":
		err = runRemote(*remoteAddr, *apiKey)
	case *serve:
		err = runServe(*binaryPath, *headless, *profileDir, *port, *apiKey, *tlsCert, *tlsKey)
	default:
		err = runLocal(*binaryPath, *headless, *profileDir, *noBrowser)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// runLocal starts the kernel and TUI locally.
func runLocal(binaryPath string, headless bool, profileDir string, noBrowser bool) error {
	var k *kernel.Kernel
	var client *juggler.Client

	if !noBrowser {
		k = kernel.New()
		if err := k.Start(kernel.Config{
			BinaryPath: binaryPath,
			Headless:   headless,
			ProfileDir: profileDir,
		}); err != nil {
			return fmt.Errorf("start kernel: %w", err)
		}
		defer k.Stop()

		client = k.Client()
		if _, err := client.Call("", "Browser.enable", map[string]interface{}{
			"attachToDefaultContext": true,
		}); err != nil {
			return fmt.Errorf("Browser.enable: %w", err)
		}
	}

	app := tui.NewApp(k, client)
	p := tea.NewProgram(app, tea.WithAltScreen())
	_, err := p.Run()
	return err
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

	app := tui.NewApp(nil, client)
	p := tea.NewProgram(app, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

// runServe starts the kernel and exposes it via WebSocket server.
func runServe(binaryPath string, headless bool, profileDir string, port int, apiKey string, tlsCert, tlsKey string) error {
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
		client.Subscribe(evt, func(params json.RawMessage) {
			server.BroadcastEvent(evt, params)
		})
	}

	log.Printf("VulpineOS kernel running (PID %d)", k.PID())

	if tlsCert != "" && tlsKey != "" {
		return server.StartTLS(tlsCert, tlsKey)
	}
	return server.Start()
}
