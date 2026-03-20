package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"vulpineos/internal/kernel"
	"vulpineos/internal/tui"
)

func main() {
	var (
		binaryPath = flag.String("binary", "", "Path to VulpineOS/Camoufox binary")
		headless   = flag.Bool("headless", false, "Run in headless mode")
		profileDir = flag.String("profile", "", "Firefox profile directory")
		remote     = flag.String("remote", "", "Connect to remote VulpineOS instance (wss://...)")
		serve      = flag.Bool("serve", false, "Run as remote-accessible server")
		port       = flag.Int("port", 8443, "Server port (with --serve)")
		noBrowser  = flag.Bool("no-browser", false, "Start TUI without launching browser (demo mode)")
		_          = port   // M4
		_          = remote // M4
		_          = serve  // M4
	)
	flag.Parse()

	if err := run(*binaryPath, *headless, *profileDir, *noBrowser); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(binaryPath string, headless bool, profileDir string, noBrowser bool) error {
	var k *kernel.Kernel

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

		// Enable browser protocol
		client := k.Client()
		_, err := client.Call("", "Browser.enable", map[string]interface{}{
			"attachToDefaultContext": true,
		})
		if err != nil {
			return fmt.Errorf("Browser.enable: %w", err)
		}

		// Launch TUI with live kernel
		app := tui.NewApp(k, client)
		p := tea.NewProgram(app, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("TUI: %w", err)
		}
	} else {
		// Demo mode — TUI without browser
		app := tui.NewApp(nil, nil)
		p := tea.NewProgram(app, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("TUI: %w", err)
		}
	}

	return nil
}
