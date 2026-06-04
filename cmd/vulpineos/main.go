package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"vulpineos/internal/agentbus"
	"vulpineos/internal/agentcore"
	"vulpineos/internal/auth"
	"vulpineos/internal/config"
	"vulpineos/internal/costtrack"
	"vulpineos/internal/extensions"
	"vulpineos/internal/foxbridge"
	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
	"vulpineos/internal/mcp"
	"vulpineos/internal/orchestrator"
	"vulpineos/internal/pagecache"
	"vulpineos/internal/pool"
	"vulpineos/internal/recording"
	"vulpineos/internal/remote"
	"vulpineos/internal/runtimeaudit"
	"vulpineos/internal/sentinelcapture"
	"vulpineos/internal/tui"
	"vulpineos/internal/tui/loading"
	"vulpineos/internal/tui/setup"
	"vulpineos/internal/vault"
	"vulpineos/internal/webhooks"
)

// Version is the VulpineOS build version string reported by --version.
// It is overridable via -ldflags "-X main.Version=..." at build time.
var Version = "dev"

// stdout and stderr are indirections so that Run can be driven from a
// test with a captured buffer. Production code uses the package-level
// os.Stdout/os.Stderr by default.
var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
)

// nativeAgentRuntime builds the native, in-process agent backend from config.
// It drives the host Camoufox directly via the MCP/Juggler tools — no daemon,
// no per-agent container. Returns nil only when prerequisites are missing.
func nativeAgentRuntime(cfg *config.Config, client *juggler.Client, v *vault.DB) orchestrator.AgentRuntime {
	if cfg == nil || client == nil {
		return nil
	}
	mgr := agentcore.NewManager(client, agentcore.Config{
		Provider:       cfg.Provider,
		Model:          cfg.Model,
		APIKey:         cfg.APIKey,
		FallbackModels: nativeFallbackModels(cfg.Provider),
	})
	// Reuse each agent's pooled, identity-applied context across chat turns.
	if v != nil {
		mgr.SetVault(v)
	}
	log.Printf("agent runtime: native in-process (provider=%s model=%s)", cfg.Provider, cfg.Model)
	return mgr
}

// nativeFallbackModels returns the rate-limit fallback chain for a provider. The
// chain is only meaningful for OpenRouter (shared free-model ids); other
// providers don't share these slugs, so they get no fallback unless overridden
// via OPENCODE_FALLBACK_MODELS (comma-separated).
func nativeFallbackModels(provider string) []string {
	if override := strings.TrimSpace(os.Getenv("OPENCODE_FALLBACK_MODELS")); override != "" {
		var out []string
		for _, m := range strings.Split(override, ",") {
			if m = strings.TrimSpace(m); m != "" {
				out = append(out, m)
			}
		}
		return out
	}
	if strings.EqualFold(strings.TrimSpace(provider), "openrouter") {
		return agentcore.DefaultFallbackModels()
	}
	return nil
}

func logSentinelRuntimeStatus(audit *runtimeaudit.Manager) {
	status, available, err := extensions.SentinelSnapshot(context.Background())
	if err != nil {
		if audit != nil {
			_, _ = audit.Log("sentinel", "warn", "status_error", "Sentinel status lookup failed", map[string]string{
				"error": err.Error(),
			})
		}
		return
	}
	metadata := map[string]string{
		"available": fmt.Sprintf("%t", available),
		"mode":      status.Mode,
	}
	if status.Provider != "" {
		metadata["provider"] = status.Provider
	}
	if status.EventSink != "" {
		metadata["event_sink"] = status.EventSink
	}
	if status.OutcomeSink != "" {
		metadata["outcome_sink"] = status.OutcomeSink
	}
	if status.VariantSource != "" {
		metadata["variant_source"] = status.VariantSource
	}
	if status.VariantBundles > 0 {
		metadata["variant_bundles"] = fmt.Sprintf("%d", status.VariantBundles)
	}
	if status.TrustRecipes > 0 {
		metadata["trust_recipes"] = fmt.Sprintf("%d", status.TrustRecipes)
	}
	eventName := "provider_unavailable"
	if available {
		eventName = "provider_ready"
		_ = sentinelcapture.RecordRuntimeSignal(context.Background(), eventName, metadata)
		if audit != nil {
			_, _ = audit.Log("sentinel", "info", eventName, "Sentinel provider ready", metadata)
		}
		return
	}
	if audit != nil {
		_, _ = audit.Log("sentinel", "info", eventName, "Sentinel provider unavailable", metadata)
	}
}

func startLocalSessionLogging(baseDir string) (restore func(), path string) {
	prevWriter := log.Writer()
	prevFlags := log.Flags()
	prevPrefix := log.Prefix()

	restore = func() {
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
	}

	logDir := filepath.Join(baseDir, "logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		log.SetOutput(io.Discard)
		return restore, ""
	}

	path = filepath.Join(logDir, "local-tui.log")
	logFile, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		log.SetOutput(io.Discard)
		return restore, ""
	}

	log.SetOutput(logFile)
	restore = func() {
		_ = logFile.Close()
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
	}
	return restore, path
}

func enableBrowser(client *juggler.Client, label string) error {
	var lastErr error
	time.Sleep(1 * time.Second)
	for attempt := 0; attempt < 5; attempt++ {
		_, err := client.Call("", "Browser.enable", map[string]interface{}{
			"attachToDefaultContext": true,
		})
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
	}
	return fmt.Errorf("%s: %w", label, lastErr)
}

func isRemoteBrowserUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "browser unavailable") || strings.Contains(msg, "server started without a kernel")
}

func main() {
	os.Exit(Run(os.Args))
}

// Run is the wrapper-friendly entrypoint for the VulpineOS CLI. It
// parses the given argv (including the program name in args[0]) and
// returns a process exit code: 0 for success, non-zero for error.
//
// This signature exists so that alternate front-end binaries (for
// example a private build that wants to share the same flag parsing)
// can delegate to Run directly instead of re-implementing main.
func Run(args []string) int {
	fs := flag.NewFlagSet("vulpineos", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage:\n")
		fmt.Fprintf(stderr, "  vulpineos [flags]\n")
		fmt.Fprintf(stderr, "  vulpineos --listen [flags]\n")
		fmt.Fprintf(stderr, "  vulpineos serve [flags]\n")
		fmt.Fprintf(stderr, "  vulpineos remote [flags]\n")
		fmt.Fprintf(stderr, "  vulpineos mcp [flags]\n")
		fmt.Fprintf(stderr, "  vulpineos auth <login|status|logout> [--provider openai]\n\n")
		fs.PrintDefaults()
	}

	var (
		binaryPath = fs.String("binary", "", "Path to VulpineOS/Camoufox binary")
		headless   = fs.Bool("headless", false, "Run in headless mode")
		profileDir = fs.String("profile", "", "Firefox profile directory")
		noBrowser  = fs.Bool("no-browser", false, "Start without launching browser/kernel")
		listen     = fs.Bool("listen", false, "Start local TUI and listen for remote TUI clients")
		host       = fs.String("host", "0.0.0.0", "Host/interface for --listen")
		port       = fs.Int("port", 8443, "Remote access port for --listen")
		apiKey     = fs.String("api-key", "", "Bearer access key for remote auth (pairing code if omitted)")
		noTLS      = fs.Bool("no-tls", true, "Disable TLS for --listen")
		tlsCert    = fs.String("tls-cert", "", "TLS certificate file for --listen")
		tlsKey     = fs.String("tls-key", "", "TLS key file for --listen")
		mcpServer  = fs.Bool("mcp-server", false, "Run as MCP stdio server")
		mcpConnect = fs.String("mcp-connect", "", "WebSocket URL to connect MCP server to remote kernel")
		listExt    = fs.Bool("list-extensions", false, "Print the status of optional extension providers and exit")
		showVer    = fs.Bool("version", false, "Print version and exit")
	)

	// args[0] is the program name; pass the rest to Parse.
	var parseArgs []string
	if len(args) > 1 {
		parseArgs = args[1:]
	}
	if len(parseArgs) > 0 && !strings.HasPrefix(parseArgs[0], "-") {
		return runSubcommand(args[0], parseArgs)
	}
	if err := fs.Parse(parseArgs); err != nil {
		// ContinueOnError means Parse already printed to stderr.
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *showVer {
		fmt.Fprintf(stdout, "vulpineos %s\n", Version)
		return 0
	}

	// Initialize the extension registry. On the public build this is a
	// no-op; alternate builds may register providers via build-tagged
	// init() functions that run before this call.
	extensions.Init()

	if *listExt {
		printExtensionStatus(stdout)
		return 0
	}

	var err error
	switch {
	case *mcpServer:
		err = runMCPServer(*binaryPath, *headless, *profileDir, *mcpConnect, *apiKey)
	default:
		var listenCfg *listenConfig
		if *listen {
			listenCfg = &listenConfig{Host: *host, Port: *port, APIKey: *apiKey, TLSCert: *tlsCert, TLSKey: *tlsKey, NoTLS: *noTLS}
		}
		err = runLocal(*binaryPath, *headless, *profileDir, *noBrowser, listenCfg)
	}

	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func runSubcommand(program string, args []string) int {
	switch args[0] {
	case "mcp":
		return runMCPSubcommand(args[1:])
	case "remote":
		return runRemoteSubcommand(args[1:])
	case "serve":
		return runServeSubcommand(args[1:])
	case "auth":
		return runAuthSubcommand(args[1:])
	case "tui", "panel":
		fmt.Fprintf(stderr, "error: %q was removed; use `vulpineos` for local TUI or `vulpineos remote --url ...`\n", args[0])
		return 2
	default:
		fmt.Fprintf(stderr, "error: unknown subcommand %q\n", args[0])
		return 2
	}
}

// runAuthSubcommand handles provider authentication, currently the OpenAI
// (ChatGPT) OAuth flow used by the "openai-oauth" / codex provider:
//
//	vulpineos auth login  --provider openai
//	vulpineos auth status --provider openai
//	vulpineos auth logout --provider openai
func runAuthSubcommand(args []string) int {
	fs := flag.NewFlagSet("vulpineos auth", flag.ContinueOnError)
	fs.SetOutput(stderr)
	provider := fs.String("provider", "openai", "Provider to authenticate (openai)")
	if len(args) == 0 {
		fmt.Fprintf(stderr, "usage: vulpineos auth <login|status|logout> [--provider openai]\n")
		return 2
	}
	action := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if strings.TrimSpace(*provider) != "openai" {
		fmt.Fprintf(stderr, "error: only --provider openai is supported\n")
		return 2
	}
	switch action {
	case "login":
		if err := auth.LoginOpenAI(stdout); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	case "status":
		creds, err := auth.OpenAIStatus()
		if err != nil || creds == nil {
			fmt.Fprintf(stdout, "Not logged in.\n")
			return 1
		}
		expired := auth.IsOAuthTokenExpired(creds, 0)
		fmt.Fprintf(stdout, "Logged in (account %s). Token expired: %t\n", creds.AccountID, expired)
		return 0
	case "logout":
		if err := auth.LogoutOpenAI(); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Logged out.\n")
		return 0
	default:
		fmt.Fprintf(stderr, "error: unknown auth action %q (use login|status|logout)\n", action)
		return 2
	}
}

func runServeSubcommand(args []string) int {
	fs := flag.NewFlagSet("vulpineos serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	binaryPath := fs.String("binary", "", "Path to VulpineOS/Camoufox binary")
	headless := fs.Bool("headless", true, "Run in headless mode")
	profileDir := fs.String("profile", "", "Firefox profile directory")
	host := fs.String("host", "0.0.0.0", "Host/interface to bind")
	port := fs.Int("port", 8443, "Server port")
	apiKey := fs.String("api-key", "", "Bearer access key for remote auth (pairing code if omitted)")
	tlsCert := fs.String("tls-cert", "", "TLS certificate file")
	tlsKey := fs.String("tls-key", "", "TLS key file")
	noTLS := fs.Bool("no-tls", false, "Disable TLS")
	noBrowser := fs.Bool("no-browser", false, "Start without launching browser/kernel")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if err := runServe(*binaryPath, *headless, *profileDir, *host, *port, *apiKey, *tlsCert, *tlsKey, *noTLS, *noBrowser); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func runRemoteSubcommand(args []string) int {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "panel", "tui":
			fmt.Fprintf(stderr, "error: remote %s was removed; use `vulpineos remote --url ...`\n", args[0])
			return 2
		}
	}
	fs := flag.NewFlagSet("vulpineos remote", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rawURL := fs.String("url", "", "Remote VulpineOS URL")
	apiKey := fs.String("api-key", "", "Remote bearer access key")
	pairCode := fs.String("pair-code", "", "Pairing code shown by the host")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *rawURL == "" && fs.NArg() > 0 {
		*rawURL = fs.Arg(0)
	}
	wsURL, err := normalizeRemoteTUIURL(*rawURL)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if err := runRemote(wsURL, *apiKey, *pairCode); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func runMCPSubcommand(args []string) int {
	fs := flag.NewFlagSet("vulpineos mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	binaryPath := fs.String("binary", "", "Path to VulpineOS/Camoufox binary")
	headless := fs.Bool("headless", false, "Run in headless mode")
	profileDir := fs.String("profile", "", "Firefox profile directory")
	connect := fs.String("connect", "", "Remote VulpineOS URL for MCP to attach to")
	apiKey := fs.String("api-key", "", "API key for remote MCP authentication")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if err := runMCPServer(*binaryPath, *headless, *profileDir, *connect, *apiKey); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func ensureAccessKey(apiKey string) (string, bool, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey != "" {
		return apiKey, false, nil
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", false, err
	}
	return hex.EncodeToString(buf), true, nil
}

func generateSecretHex(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func generatePairingCode() (string, error) {
	buf := make([]byte, 3)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	n := (int(buf[0])<<16 | int(buf[1])<<8 | int(buf[2])) % 1000000
	return fmt.Sprintf("%06d", n), nil
}

type listenConfig struct {
	Host    string
	Port    int
	APIKey  string
	TLSCert string
	TLSKey  string
	NoTLS   bool
}

func remoteDisplayHost(host string) string {
	host = strings.TrimSpace(host)
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return "localhost"
	default:
		return host
	}
}

func buildRemoteURL(host string, port int, useTLS bool) string {
	scheme := "ws"
	if useTLS {
		scheme = "wss"
	}
	displayHost := remoteDisplayHost(host)
	if strings.HasPrefix(displayHost, "[") && strings.HasSuffix(displayHost, "]") {
		displayHost = strings.TrimSuffix(strings.TrimPrefix(displayHost, "["), "]")
	}
	u := &url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(displayHost, fmt.Sprintf("%d", port)),
		Path:   "/ws",
	}
	return u.String()
}

func normalizeRemoteTUIURL(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "ws://127.0.0.1:8443/ws", nil
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = "ws://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported remote TUI URL scheme %q", u.Scheme)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/ws"
	} else if !strings.HasSuffix(u.Path, "/ws") {
		u.Path = strings.TrimRight(u.Path, "/") + "/ws"
	}
	return u.String(), nil
}

func printRemoteAccess(host string, port int, useTLS bool, apiKey string, generated bool, pairCode string) string {
	remoteURL := buildRemoteURL(host, port, useTLS)
	if normalized := strings.TrimSpace(host); normalized == "" || normalized == "0.0.0.0" || normalized == "::" || normalized == "[::]" {
		if normalized == "" {
			normalized = "0.0.0.0"
		}
		fmt.Fprintf(stdout, "Listening on: %s:%d\n", normalized, port)
	}
	fmt.Fprintf(stdout, "Remote URL: %s\n", remoteURL)
	if strings.TrimSpace(pairCode) != "" {
		fmt.Fprintf(stdout, "Pairing code: %s\n", pairCode)
	} else if generated {
		fmt.Fprintf(stdout, "API key: %s (generated)\n", apiKey)
	} else if strings.TrimSpace(apiKey) != "" {
		fmt.Fprintf(stdout, "API key: %s\n", apiKey)
	}
	return remoteURL
}

func runRemote(wsURL string, apiKey string, pairCode string) error {
	if strings.TrimSpace(apiKey) == "" {
		token, err := pairRemote(wsURL, pairCode)
		if err != nil {
			return err
		}
		apiKey = token
	}
	rc, err := remote.Dial(context.Background(), wsURL, apiKey)
	if err != nil {
		return err
	}
	defer rc.Close()

	client := juggler.NewClient(rc)
	defer client.Close()
	app := tui.NewAppWithControl(nil, client, nil, nil, nil, nil, rc)
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err = p.Run()
	return err
}

func pairRemote(wsURL string, pairCode string) (string, error) {
	pairCode = strings.TrimSpace(pairCode)
	if pairCode == "" {
		fmt.Fprint(stdout, "Pairing code: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return "", err
		}
		pairCode = strings.TrimSpace(line)
	}
	if pairCode == "" {
		return "", fmt.Errorf("pairing code is required")
	}
	pairURL, err := pairingURL(wsURL)
	if err != nil {
		return "", err
	}
	hostname, _ := os.Hostname()
	payload, _ := json.Marshal(map[string]string{
		"code":  pairCode,
		"label": hostname,
	})
	httpClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpClient.Post(pairURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pairing failed: %s", resp.Status)
	}
	var out struct {
		APIKey string `json:"apiKey"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.APIKey) == "" {
		return "", fmt.Errorf("pairing response did not include an access key")
	}
	return out.APIKey, nil
}

func pairingURL(wsURL string) (string, error) {
	u, err := url.Parse(wsURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	case "http", "https":
	default:
		return "", fmt.Errorf("unsupported pairing URL scheme %q", u.Scheme)
	}
	u.Path = "/pair"
	u.RawQuery = ""
	return u.String(), nil
}

// runMCPServer runs as an MCP stdio server.
// It connects to a running VulpineOS kernel and translates MCP tool calls to Juggler protocol.
func runMCPServer(binaryPath string, headless bool, profileDir string, connectURL string, apiKey string) error {
	if strings.TrimSpace(connectURL) != "" {
		wsURL, err := normalizeRemoteTUIURL(connectURL)
		if err != nil {
			return err
		}
		rc, err := remote.Dial(context.Background(), wsURL, apiKey)
		if err != nil {
			return fmt.Errorf("connect remote MCP transport: %w", err)
		}
		defer rc.Close()

		client := juggler.NewClient(rc)
		defer client.Close()
		extensions.InitWithClient(client)
		if err := enableBrowser(client, "Browser.enable (remote MCP)"); err != nil {
			if isRemoteBrowserUnavailable(err) {
				return fmt.Errorf("remote server was started without a browser; MCP browser tools are unavailable")
			}
			return err
		}

		server := mcp.NewServer(client)
		return server.Run()
	}

	resolvedBinaryPath, err := kernel.ResolveBinaryPath(strings.TrimSpace(binaryPath))
	if err != nil {
		return err
	}
	if warning := kernel.DetectStaleBinary(resolvedBinaryPath); warning != nil {
		log.Printf("Warning: %s", warning.Message())
	}

	k := kernel.New()
	if err := k.Start(kernel.Config{
		BinaryPath: resolvedBinaryPath,
		Headless:   headless,
		ProfileDir: profileDir,
	}); err != nil {
		return fmt.Errorf("start kernel: %w", err)
	}
	defer k.Stop()

	client := k.Client()
	extensions.InitWithClient(client)

	// Start embedded foxbridge CDP server FIRST — its Browser.enable
	// call sets up event subscriptions needed by both foxbridge and MCP.
	fb := foxbridge.New()
	if err := fb.StartEmbeddedMode(client, 9222); err != nil {
		log.Printf("foxbridge embedded mode failed: %v", err)
		// Fall back to manual Browser.enable for MCP
		if err := enableBrowser(client, "Browser.enable"); err != nil {
			return err
		}
	} else {
		defer fb.Stop()
		log.Printf("foxbridge CDP proxy on %s", fb.CDPURL())
	}

	server := mcp.NewServer(client)
	return server.Run()
}

func runServe(binaryPath string, headless bool, profileDir string, host string, port int, apiKey string, tlsCert string, tlsKey string, noTLS bool, noBrowser bool) error {
	cfg, err := config.Load()
	if err != nil {
		cfg = &config.Config{}
	}

	var resolvedBinaryPath string
	if !noBrowser {
		resolvedBinaryPath = strings.TrimSpace(binaryPath)
		if resolvedBinaryPath == "" && cfg != nil {
			resolvedBinaryPath = strings.TrimSpace(cfg.BinaryPath)
		}
		resolvedBinaryPath, err = kernel.ResolveBinaryPath(resolvedBinaryPath)
		if err != nil {
			return err
		}
	}
	v, err := vault.Open()
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	defer v.Close()
	if err := v.ReconcileNonTerminalAgents("interrupted"); err != nil {
		log.Printf("Warning: reconcile agents: %v", err)
	}
	audit := runtimeaudit.New(v)
	defer audit.Close()

	var k *kernel.Kernel
	var client *juggler.Client
	var orch *orchestrator.Orchestrator
	var fb *foxbridge.Process
	if !noBrowser {
		k = kernel.New()
		if err := k.Start(kernel.Config{BinaryPath: resolvedBinaryPath, Headless: headless, ProfileDir: profileDir}); err != nil {
			return fmt.Errorf("start kernel: %w", err)
		}
		defer k.Stop()
		client = k.Client()
		if err := enableBrowser(client, "Browser.enable"); err != nil {
			return err
		}
		extensions.InitWithClient(client)

		fb = foxbridge.New()
		fb.SetRuntimeAudit(audit)
		if err := fb.StartEmbeddedMode(client, 9222); err != nil {
			log.Printf("embedded foxbridge not available: %v", err)
			fb = nil
		} else {
			defer fb.Stop()
			cfg.FoxbridgeCDPURL = fb.CDPURL()
		}

		model := ""
		if cfg != nil {
			model = cfg.Model
		}
		orch = orchestrator.New(k, client, v, pool.DefaultConfig(), orchestrator.Opts{
			AgentBus:     agentbus.New(),
			Costs:        costtrack.New(model),
			Webhooks:     webhooks.New(),
			Recording:    recording.NewRecorder(),
			PageCache:    pagecache.New(filepath.Join(config.Dir(), "pagecache")),
			AgentRuntime: nativeAgentRuntime(cfg, client, v),
		})
		orch.Agents.SetRuntimeAudit(audit)
		if err := orch.Start(); err != nil {
			return err
		}
		defer orch.Close()
	}

	listen := listenConfig{Host: host, Port: port, APIKey: apiKey, TLSCert: tlsCert, TLSKey: tlsKey, NoTLS: noTLS}
	server, err := startRemoteAccessServer(listen, cfg, k, client, orch, v, audit, func() bool {
		return fb != nil && fb.Running()
	}, true)
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Stop(ctx)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	<-sigCh
	return nil
}

func startRemoteAccessServer(cfg listenConfig, appCfg *config.Config, k *kernel.Kernel, client *juggler.Client, orch *orchestrator.Orchestrator, v *vault.DB, audit *runtimeaudit.Manager, foxbridgeRunning func() bool, persistAgentEvents bool) (*remote.Server, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	generated := false
	pairCode := ""
	if apiKey == "" {
		if v != nil {
			var err error
			pairCode, err = generatePairingCode()
			if err != nil {
				return nil, err
			}
		} else {
			var err error
			apiKey, generated, err = ensureAccessKey("")
			if err != nil {
				return nil, err
			}
		}
	}
	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	server := remote.NewServer(addr, apiKey, client)
	if v != nil {
		server.SetTokenValidator(v.ValidateRemoteClientToken)
	}
	if pairCode != "" && v != nil {
		server.SetPairing(pairCode, func(label string) (string, error) {
			token, err := generateSecretHex(32)
			if err != nil {
				return "", err
			}
			if _, err := v.CreateRemoteClient(label, token); err != nil {
				return "", err
			}
			return token, nil
		})
	}
	server.SetControlAPI(&remote.ControlAPI{
		Orchestrator:     orch,
		Config:           appCfg,
		Vault:            v,
		Kernel:           k,
		FoxbridgeRunning: foxbridgeRunning,
		Client:           client,
	})
	wireRemoteAgentEvents(orch, v, server, persistAgentEvents)

	useTLS := !cfg.NoTLS
	if useTLS && (cfg.TLSCert == "" || cfg.TLSKey == "") {
		cert, key, err := remote.GenerateSelfSignedCert()
		if err != nil {
			return nil, err
		}
		cfg.TLSCert = cert
		cfg.TLSKey = key
		if fp, err := remote.CertFingerprint(cert); err == nil {
			fmt.Fprintf(stdout, "TLS certificate fingerprint: %s\n", fp)
		}
	}
	printRemoteAccess(cfg.Host, cfg.Port, useTLS, apiKey, generated, pairCode)

	errCh := make(chan error, 1)
	go func() {
		var err error
		if useTLS {
			err = server.StartTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			err = server.Start()
		}
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("remote server stopped during startup")
	case <-time.After(150 * time.Millisecond):
		return server, nil
	}
}

func wireRemoteAgentEvents(orch *orchestrator.Orchestrator, v *vault.DB, server *remote.Server, persist bool) {
	if orch == nil || server == nil || orch.Agents == nil {
		return
	}
	statusCh := orch.Agents.StatusChan()
	go func() {
		for status := range statusCh {
			if persist && v != nil {
				_ = v.UpdateAgentStatus(status.AgentID, status.Status)
				if status.Tokens > 0 {
					_ = v.UpdateAgentTokens(status.AgentID, status.Tokens)
				}
			}
			server.BroadcastAgentStatus(status)
		}
	}()
	conversationCh := orch.Agents.ConversationChan()
	go func() {
		for msg := range conversationCh {
			// Don't persist transient streaming deltas (Role "stream") — they
			// update one live entry; persisting per-token fragments the history.
			if persist && v != nil && msg.Role != "stream" {
				_ = v.AppendMessage(msg.AgentID, msg.Role, msg.Content, msg.Tokens)
			}
			server.BroadcastConversation(msg)
		}
	}()
}

// runLocal starts the kernel and TUI locally.
func runLocal(binaryPath string, headless bool, profileDir string, noBrowser bool, listenCfg *listenConfig) error {
	restoreLogs, _ := startLocalSessionLogging(config.Dir())
	defer restoreLogs()

	// Check if first-time setup is needed
	cfg, err := config.Load()
	if err != nil {
		log.Printf("Warning: could not load config: %v", err)
		cfg = &config.Config{}
	}
	reconfigureRequested := config.ReconfigureRequested()
	resolvedBinaryPath := strings.TrimSpace(binaryPath)
	if resolvedBinaryPath == "" && cfg != nil {
		resolvedBinaryPath = strings.TrimSpace(cfg.BinaryPath)
	}

	if cfg.NeedsSetup() || reconfigureRequested {
		// Run setup wizard
		setupModel := setup.NewWithConfig(cfg)
		p := tea.NewProgram(setupModel, tea.WithAltScreen())
		result, err := p.Run()
		if err != nil {
			return fmt.Errorf("setup wizard: %w", err)
		}
		finalModel := result.(*setup.Model)
		if !finalModel.Done() {
			_ = config.ClearReconfigureRequest()
			return nil // user quit setup
		}
		cfg = finalModel.Config()
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		_ = config.ClearReconfigureRequest()
	}

	if !noBrowser {
		var resolveErr error
		resolvedBinaryPath, resolveErr = kernel.ResolveBinaryPath(resolvedBinaryPath)
		if resolveErr != nil {
			return resolveErr
		}
	}

	// Store resolved binary path in config when available.
	if resolvedBinaryPath != "" && cfg.BinaryPath != resolvedBinaryPath {
		cfg.BinaryPath = resolvedBinaryPath
		cfg.Save()
	}

	var k *kernel.Kernel
	var client *juggler.Client
	var orch *orchestrator.Orchestrator
	var v *vault.DB
	var audit *runtimeaudit.Manager
	var fb *foxbridge.Process
	var wd *kernel.Watchdog
	var browserEnabled bool
	var startErr error

	if !noBrowser {
		// Show loading spinner while kernel starts
		loader := loading.New("Launching VulpineOS")
		loaderProg := tea.NewProgram(loader, tea.WithAltScreen())

		go func() {
			// Open vault
			v, _ = vault.Open()
			if v != nil {
				if err := v.ReconcileNonTerminalAgents("interrupted"); err != nil {
					log.Printf("Warning: reconcile agents: %v", err)
				}
				audit = runtimeaudit.New(v)
			}

			// Start kernel
			k = kernel.New()
			startErr = k.Start(kernel.Config{
				BinaryPath: resolvedBinaryPath,
				Headless:   headless,
				ProfileDir: profileDir,
			})
			if startErr == nil {
				client = k.Client()
				if err := enableBrowser(client, "Browser.enable"); err != nil {
					startErr = err
				} else {
					browserEnabled = true
					if audit != nil {
						_, _ = audit.Log("kernel", "info", "started", "kernel started", map[string]string{
							"pid":      fmt.Sprintf("%d", k.PID()),
							"headless": fmt.Sprintf("%t", headless),
						})
						wd = kernel.NewWatchdog(k, false)
						wd.SetConfig(kernel.Config{
							BinaryPath: resolvedBinaryPath,
							Headless:   headless,
							ProfileDir: profileDir,
						})
						wd.OnEvent(func(event kernel.WatchdogEvent) {
							level := "warn"
							message := "kernel event"
							switch event.Type {
							case "crashed":
								level = "error"
								message = "kernel exited unexpectedly"
							case "restart_success":
								level = "warn"
								message = "kernel restarted"
							case "restart_failed":
								level = "error"
								message = "kernel restart failed"
							}
							metadata := map[string]string{}
							if event.Attempt > 0 {
								metadata["attempt"] = fmt.Sprintf("%d", event.Attempt)
							}
							if event.Err != nil {
								metadata["error"] = event.Err.Error()
							}
							if _, err := audit.Log("kernel", level, event.Type, message, metadata); err != nil {
								log.Printf("runtime audit kernel %s: %v", event.Type, err)
							}
						})
						wd.Start()
					}

					// Create orchestrator with optional subsystems
					if v != nil {
						model := ""
						if cfg != nil {
							model = cfg.Model
						}
						orch = orchestrator.New(k, client, v, pool.DefaultConfig(), orchestrator.Opts{
							AgentBus:     agentbus.New(),
							Costs:        costtrack.New(model),
							Webhooks:     webhooks.New(),
							Recording:    recording.NewRecorder(),
							PageCache:    pagecache.New(filepath.Join(config.Dir(), "pagecache")),
							AgentRuntime: nativeAgentRuntime(cfg, client, v),
						})
						if audit != nil {
							orch.Agents.SetRuntimeAudit(audit)
						}
						orch.Start()
					}
				}
			}

			// Start foxbridge as an embedded CDP server sharing the kernel's Juggler
			// client, exposing the same Camoufox over CDP on :9222 for external tools.
			if startErr == nil && client != nil {
				fb = foxbridge.New()
				fb.SetRuntimeAudit(audit)
				fbErr := fb.StartEmbeddedMode(client, 9222)
				if fbErr != nil {
					log.Printf("embedded foxbridge not available: %v", fbErr)
					fb = nil
				} else {
					cfg.FoxbridgeCDPURL = fb.CDPURL()
					log.Printf("foxbridge embedded — Camoufox exposed over CDP at %s", fb.CDPURL())
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
			if wd != nil {
				wd.Stop()
			}
			if fb != nil {
				fb.Stop()
			}
			if orch != nil {
				orch.Close()
			}
			if k != nil {
				_ = k.Stop()
			}
			if v != nil {
				_ = v.Close()
			}
			if audit != nil {
				audit.Close()
			}
			return startErr
		}
		if warning := kernel.DetectStaleBinary(resolvedBinaryPath); warning != nil {
			log.Printf("Warning: %s", warning.Message())
			if audit != nil {
				_, _ = audit.Log("kernel", "warn", "stale_binary", "selected browser binary is older than repo-local build", map[string]string{
					"selected":      warning.SelectedPath,
					"preferred":     warning.PreferredPath,
					"selected_mod":  warning.SelectedMod.UTC().Format(time.RFC3339),
					"preferred_mod": warning.PreferredMod.UTC().Format(time.RFC3339),
				})
			}
		}
		defer func() {
			if audit != nil {
				_, _ = audit.Log("kernel", "info", "stopped", "kernel stopped", map[string]string{
					"pid": fmt.Sprintf("%d", k.PID()),
				})
			}
			_ = k.Stop()
		}()
		if wd != nil {
			defer wd.Stop()
		}
		if fb != nil {
			defer fb.Stop()
		}
		if v != nil {
			defer v.Close()
		}
		if audit != nil {
			defer audit.Close()
		}
		if orch != nil {
			defer orch.Close()
		}
	}

	// Create the TUI after startup subsystems are fully initialized.
	app := tui.NewApp(k, client, orch, v, cfg, audit)
	var remoteServer *remote.Server
	if listenCfg != nil {
		srv, err := startRemoteAccessServer(*listenCfg, cfg, k, client, orch, v, audit, func() bool {
			return fb != nil && fb.Running()
		}, false)
		if err != nil {
			return err
		}
		remoteServer = srv
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = remoteServer.Stop(ctx)
		}()
	}

	if client != nil {
		// Wire the live juggler client into any build-tagged extension
		// providers. On the default public build this is a no-op
		// because privateProviders is zero-valued.
		extensions.InitWithClient(client)
		logSentinelRuntimeStatus(audit)
		if !browserEnabled {
			if err := enableBrowser(client, "Browser.enable"); err != nil {
				return err
			}
			browserEnabled = true
		}
	}
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, runErr := p.Run()
	return runErr
}
