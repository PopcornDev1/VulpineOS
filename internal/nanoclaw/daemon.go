package nanoclaw

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"vulpineos/internal/auth"
	"vulpineos/internal/config"
	"vulpineos/internal/spawntrace"
)

// Daemon owns the long-running NanoClaw socket process used by VulpineOS.
type Daemon struct {
	mu            sync.Mutex
	binary        string
	nanoclawDir   string
	socketPath    string
	logPath       string
	logFile       *os.File
	cmd           *exec.Cmd
	env           map[string]string
	oneCLIShim    *LocalOneCLIShim
	exited        bool
	exitCh        chan error
	waitReadyFunc func() error
}

// NewDaemon creates a daemon manager rooted in the VulpineOS-owned NanoClaw dir.
func NewDaemon(binary string, nanoclawDir ...string) *Daemon {
	dir := VulpineNanoclawDir()
	if len(nanoclawDir) > 0 && strings.TrimSpace(nanoclawDir[0]) != "" {
		dir = strings.TrimSpace(nanoclawDir[0])
	}
	dataDir := filepath.Join(dir, "data")
	return &Daemon{
		binary:      strings.TrimSpace(binary),
		nanoclawDir: dir,
		socketPath:  filepath.Join(dataDir, "cli.sock"),
		logPath:     filepath.Join(dataDir, "nanoclaw.log"),
	}
}

// SetEnv adds non-secret and secret provider runtime variables to the daemon
// environment. Values are inherited by NanoClaw and its containers; callers
// must avoid logging the returned environment.
func (d *Daemon) SetEnv(env map[string]string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(env) == 0 {
		d.env = nil
		return
	}
	d.env = make(map[string]string, len(env))
	for k, v := range env {
		k = strings.TrimSpace(k)
		if k == "" || strings.TrimSpace(v) == "" {
			continue
		}
		d.env[k] = v
	}
}

// Start launches NanoClaw and waits until its CLI socket is present.
func (d *Daemon) Start() error {
	d.mu.Lock()
	if d.cmd != nil && !d.exited {
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()

	tr := spawntrace.Start("nanoclaw.Daemon.Start", "daemon")
	defer tr.End()

	nanoclawBin := strings.TrimSpace(d.binary)
	if nanoclawBin == "" {
		nanoclawBin = NewManager("").findNanoClaw()
	}
	if nanoclawBin == "" {
		return fmt.Errorf("NanoClaw binary not found")
	}

	dataDir := filepath.Join(d.nanoclawDir, "data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("create NanoClaw data dir: %w", err)
	}
	if err := os.Remove(filepath.Join(dataDir, "circuit-breaker.json")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove NanoClaw circuit breaker state: %w", err)
	}
	tr.Lap("prepare-data-dir")
	if err := prepareNanoClawSourceRuntime(d.nanoclawDir); err != nil {
		return fmt.Errorf("prepare NanoClaw source runtime: %w", err)
	}
	tr.Lap("prepare-source-runtime")
	cleanupStreamFiles(d.nanoclawDir)
	if err := os.Remove(d.socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale NanoClaw socket: %w", err)
	}

	overrides := map[string]string{
		"NANOCLAW_HOME":        d.nanoclawDir,
		"NANOCLAW_DIR":         d.nanoclawDir,
		"NANOCLAW_DATA_DIR":    dataDir,
		"NANOCLAW_SOCKET":      d.socketPath,
		"NANOCLAW_CONFIG_PATH": config.NanoClawConfigPath(),
	}
	d.mu.Lock()
	providerEnv := cloneStringMap(d.env)
	for k, v := range d.env {
		overrides[k] = v
	}
	d.mu.Unlock()

	var oneCLIShim *LocalOneCLIShim
	if useLocalOneCLIShim() {
		shim, err := StartDynamicLocalOneCLIShim(func() map[string]string {
			cfg, err := config.Load()
			if err == nil {
				if env := ProviderRuntimeEnv(cfg); len(env) > 0 {
					return env
				}
			}
			return providerEnv
		})
		if err != nil {
			return fmt.Errorf("start local OneCLI shim: %w", err)
		}
		oneCLIShim = shim
		overrides["ONECLI_URL"] = shim.URL()
		overrides["ONECLI_GATEWAY_URL"] = shim.URL()
		overrides["ONECLI_API_KEY"] = shim.AccessKey()
	}

	cmd := exec.Command(nanoclawBin, "run")
	cmd.Env = daemonEnv(os.Environ(), overrides)

	var logFile *os.File
	if f, err := os.OpenFile(d.logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600); err == nil {
		logFile = f
		cmd.Stdout = f
		cmd.Stderr = f
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		if oneCLIShim != nil {
			_ = oneCLIShim.Stop()
		}
		return fmt.Errorf("start NanoClaw daemon: %w", err)
	}

	d.mu.Lock()
	d.cmd = cmd
	d.logFile = logFile
	d.oneCLIShim = oneCLIShim
	d.exited = false
	d.exitCh = make(chan error, 1)
	exitCh := d.exitCh
	d.mu.Unlock()

	go func() {
		err := cmd.Wait()
		d.mu.Lock()
		if d.cmd == cmd {
			d.exited = true
		}
		d.mu.Unlock()
		exitCh <- err
		close(exitCh)
	}()

	tr.Lap("process-start")
	waitReady := d.waitReady
	if d.waitReadyFunc != nil {
		waitReady = d.waitReadyFunc
	}
	if err := waitReady(); err != nil {
		d.Stop()
		return fmt.Errorf("wait for NanoClaw daemon readiness: %w", err)
	}
	tr.Lap("wait-ready-socket")

	log.Printf("NanoClaw daemon started (PID %d), socket: %s, log: %s", cmd.Process.Pid, d.socketPath, d.logPath)
	return nil
}

// Stop terminates the owned NanoClaw daemon process.
func (d *Daemon) Stop() {
	d.mu.Lock()
	cmd := d.cmd
	exitCh := d.exitCh
	exited := d.exited
	logFile := d.logFile
	oneCLIShim := d.oneCLIShim
	d.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		if !exited {
			_ = cmd.Process.Kill()
		}
		if exitCh != nil {
			<-exitCh
		}
	}

	if logFile != nil {
		_ = logFile.Close()
	}
	if oneCLIShim != nil {
		_ = oneCLIShim.Stop()
	}

	d.mu.Lock()
	if d.cmd == cmd {
		d.cmd = nil
		d.exitCh = nil
		d.exited = true
		d.logFile = nil
		d.oneCLIShim = nil
	}
	d.mu.Unlock()
}

// Running returns true while the owned daemon process is alive.
func (d *Daemon) Running() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cmd != nil && !d.exited
}

func (d *Daemon) waitReady() error {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(d.socketPath); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("socket not available: %s", d.socketPath)
}

func ProviderRuntimeEnv(cfg *config.Config) map[string]string {
	if cfg == nil {
		return nil
	}
	env := make(map[string]string)
	providerID := strings.TrimSpace(cfg.Provider)
	model := strings.TrimSpace(cfg.Model)
	apiKey := strings.TrimSpace(cfg.APIKey)

	switch providerID {
	case "openai-oauth":
		if token, err := auth.ValidAccessToken(); err == nil && token != "" {
			apiKey = token
			env["OPENAI_API_KEY"] = token
			env["OPENAI_ACCESS_TOKEN"] = token
		}
	default:
		if provider := config.GetProvider(providerID); provider != nil && strings.TrimSpace(provider.EnvVar) != "" && apiKey != "" {
			env[strings.TrimSpace(provider.EnvVar)] = apiKey
		}
	}
	if providerID != "" {
		env["OPENCODE_PROVIDER"] = providerID
		if model != "" {
			env["OPENCODE_MODEL"] = model
		}
		if apiKey != "" {
			env["OPENCODE_API_KEY"] = apiKey
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func useLocalOneCLIShim() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("VULPINE_NANOCLAW_ONECLI_MODE"))) {
	case "remote", "cloud", "external":
		return false
	default:
		return true
	}
}

func daemonEnv(base []string, overrides map[string]string) []string {
	env := make([]string, 0, len(base)+len(overrides))
	seen := make(map[string]struct{}, len(overrides))
	for key := range overrides {
		seen[key] = struct{}{}
	}
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			if _, override := seen[key]; override {
				continue
			}
		}
		env = append(env, item)
	}
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}
