package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
)

func main() {
	var (
		binaryPath = flag.String("binary", "", "Path to VulpineOS/Camoufox binary")
		headless   = flag.Bool("headless", false, "Run in headless mode")
		profileDir = flag.String("profile", "", "Firefox profile directory")
		remote     = flag.String("remote", "", "Connect to remote VulpineOS instance (wss://...)")
		serve      = flag.Bool("serve", false, "Run as remote-accessible server")
		port       = flag.Int("port", 8443, "Server port (with --serve)")
		_          = port   // will be used in M4
		_          = remote // will be used in M4
		_          = serve  // will be used in M4
	)
	flag.Parse()

	if err := run(*binaryPath, *headless, *profileDir); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(binaryPath string, headless bool, profileDir string) error {
	fmt.Println("VulpineOS Kernel Console v0.1.0")
	fmt.Println("Starting kernel...")

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
	fmt.Printf("Kernel started (PID %d)\n", k.PID())

	// Enable the browser protocol
	_, err := client.Call("", "Browser.enable", map[string]interface{}{
		"attachToDefaultContext": true,
	})
	if err != nil {
		return fmt.Errorf("Browser.enable: %w", err)
	}

	// Get browser info
	result, err := client.Call("", "Browser.getInfo", nil)
	if err != nil {
		return fmt.Errorf("Browser.getInfo: %w", err)
	}

	var info juggler.BrowserInfo
	if err := json.Unmarshal(result, &info); err != nil {
		return fmt.Errorf("parse browser info: %w", err)
	}
	fmt.Printf("Engine: %s\nUser-Agent: %s\n", info.Version, info.UserAgent)

	// Subscribe to target events
	client.Subscribe("Browser.attachedToTarget", func(params json.RawMessage) {
		var event juggler.AttachedToTarget
		json.Unmarshal(params, &event)
		fmt.Printf("[event] Target attached: %s (%s)\n", event.TargetInfo.TargetID, event.TargetInfo.URL)
	})

	client.Subscribe("Browser.detachedFromTarget", func(params json.RawMessage) {
		var event juggler.DetachedFromTarget
		json.Unmarshal(params, &event)
		fmt.Printf("[event] Target detached: %s\n", event.TargetID)
	})

	client.Subscribe("Browser.trustWarmingStateChanged", func(params json.RawMessage) {
		var event juggler.TrustWarmingState
		json.Unmarshal(params, &event)
		fmt.Printf("[trust-warm] State: %s, Site: %s\n", event.State, event.CurrentSite)
	})

	fmt.Println("\nKernel ready. Press Ctrl+C to stop.")
	fmt.Println("(TUI dashboard coming in M2)")

	// Wait for interrupt or process exit
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	doneCh := make(chan struct{})
	go func() {
		k.Wait()
		close(doneCh)
	}()

	select {
	case <-sigCh:
		fmt.Println("\nShutting down...")
	case <-doneCh:
		fmt.Println("\nKernel process exited.")
	}

	return nil
}
