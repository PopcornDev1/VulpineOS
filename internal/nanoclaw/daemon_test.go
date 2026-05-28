package nanoclaw

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vulpineos/internal/config"
)

func writeDaemonTestBinary(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nanoclaw")
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatalf("write daemon test binary: %v", err)
	}
	return path
}

func TestDaemonStartUsesVulpineOwnedSocketAndEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	envPath := filepath.Join(t.TempDir(), "daemon.env")
	t.Setenv("VULPINE_TEST_DAEMON_ENV_FILE", envPath)

	bin := writeDaemonTestBinary(t, `#!/bin/sh
printf 'socket=%s
home=%s
data=%s
config=%s
args=%s
' "$NANOCLAW_SOCKET" "$NANOCLAW_HOME" "$NANOCLAW_DATA_DIR" "$NANOCLAW_CONFIG_PATH" "$*" > "$VULPINE_TEST_DAEMON_ENV_FILE"
touch "$NANOCLAW_SOCKET"
sleep 30
`)
	daemon := NewDaemon(bin)
	if err := daemon.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(daemon.Stop)

	wantDir := filepath.Join(home, ".vulpineos", "nanoclaw")
	wantSocket := filepath.Join(wantDir, "data", "cli.sock")
	if !daemon.Running() {
		t.Fatal("daemon should report running after Start")
	}
	if got, ok := FindNanoclawSocket(); !ok || got != wantSocket {
		t.Fatalf("FindNanoclawSocket() = %q %v, want %q true", got, ok, wantSocket)
	}

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env capture: %v", err)
	}
	env := string(data)
	for _, want := range []string{
		"socket=" + wantSocket,
		"home=" + wantDir,
		"data=" + filepath.Join(wantDir, "data"),
		"config=" + filepath.Join(wantDir, "nanoclaw.json"),
		"args=run",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("daemon env capture %q does not contain %q", env, want)
		}
	}
}

func TestDaemonStartCleansUpOnReadinessFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bin := writeDaemonTestBinary(t, "#!/bin/sh\nsleep 30\n")
	daemon := NewDaemon(bin)
	daemon.waitReadyFunc = func() error {
		return os.ErrDeadlineExceeded
	}

	if err := daemon.Start(); err == nil {
		t.Fatal("expected readiness failure")
	}
	if daemon.Running() {
		t.Fatal("daemon should not report running after failed Start")
	}
}

func TestDaemonStartClearsCircuitBreakerState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".vulpineos", "nanoclaw", "data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	circuitPath := filepath.Join(dataDir, "circuit-breaker.json")
	if err := os.WriteFile(circuitPath, []byte(`{"attempt":6}`), 0600); err != nil {
		t.Fatalf("write circuit breaker: %v", err)
	}

	bin := writeDaemonTestBinary(t, "#!/bin/sh\ntouch \"$NANOCLAW_SOCKET\"\nsleep 30\n")
	daemon := NewDaemon(bin)
	if err := daemon.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(daemon.Stop)

	if _, err := os.Stat(circuitPath); !os.IsNotExist(err) {
		t.Fatalf("circuit breaker state still exists after Start: %v", err)
	}
}

func TestProviderRuntimeEnvIncludesOpenCodeSettings(t *testing.T) {
	env := ProviderRuntimeEnv(&config.Config{
		Provider: "opencode-go",
		Model:    "opencode-go/deepseek-v4-flash",
		APIKey:   "secret-key",
	})

	if env["OPENCODE_PROVIDER"] != "opencode-go" {
		t.Fatalf("OPENCODE_PROVIDER = %q, want opencode-go", env["OPENCODE_PROVIDER"])
	}
	if env["OPENCODE_MODEL"] != "opencode-go/deepseek-v4-flash" {
		t.Fatalf("OPENCODE_MODEL = %q, want opencode-go/deepseek-v4-flash", env["OPENCODE_MODEL"])
	}
	if env["OPENCODE_API_KEY"] != "secret-key" {
		t.Fatalf("OPENCODE_API_KEY was not propagated")
	}
}

func TestDaemonStartMergesProviderRuntimeEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	envPath := filepath.Join(t.TempDir(), "daemon.env")
	t.Setenv("VULPINE_TEST_DAEMON_ENV_FILE", envPath)

	bin := writeDaemonTestBinary(t, `#!/bin/sh
printf 'provider=%s
model=%s
key=%s
' "$OPENCODE_PROVIDER" "$OPENCODE_MODEL" "$OPENCODE_API_KEY" > "$VULPINE_TEST_DAEMON_ENV_FILE"
touch "$NANOCLAW_SOCKET"
sleep 30
`)
	daemon := NewDaemon(bin)
	daemon.SetEnv(ProviderRuntimeEnv(&config.Config{
		Provider: "opencode",
		Model:    "opencode/deepseek-v4-flash-free",
		APIKey:   "secret-key",
	}))
	if err := daemon.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(daemon.Stop)

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env capture: %v", err)
	}
	env := string(data)
	for _, want := range []string{
		"provider=opencode",
		"model=opencode/deepseek-v4-flash-free",
		"key=secret-key",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("daemon env capture %q does not contain %q", env, want)
		}
	}
}

func TestDaemonStartProvidesLocalOneCLIShim(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ONECLI_URL", "https://app.onecli.sh")
	envPath := filepath.Join(t.TempDir(), "daemon.env")
	t.Setenv("VULPINE_TEST_DAEMON_ENV_FILE", envPath)

	bin := writeDaemonTestBinary(t, `#!/bin/sh
printf 'onecli_url=%s
onecli_gateway=%s
onecli_key=%s
' "$ONECLI_URL" "$ONECLI_GATEWAY_URL" "$ONECLI_API_KEY" > "$VULPINE_TEST_DAEMON_ENV_FILE"
touch "$NANOCLAW_SOCKET"
sleep 30
`)
	daemon := NewDaemon(bin)
	daemon.SetEnv(map[string]string{
		"OPENCODE_API_KEY": "secret-key",
		"OPENCODE_MODEL":   "opencode/deepseek-v4-flash-free",
	})
	if err := daemon.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(daemon.Stop)

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env capture: %v", err)
	}
	env := string(data)
	if strings.Contains(env, "onecli_url=https://app.onecli.sh") {
		t.Fatalf("daemon env capture %q should use local OneCLI shim", env)
	}
	if !strings.Contains(env, "onecli_url=http://127.0.0.1:") {
		t.Fatalf("daemon env capture %q does not include local OneCLI URL", env)
	}
	if !strings.Contains(env, "onecli_gateway=http://127.0.0.1:") {
		t.Fatalf("daemon env capture %q does not include local OneCLI gateway URL", env)
	}
	if strings.Contains(env, "onecli_key=\n") {
		t.Fatalf("daemon env capture %q should include a local OneCLI access key", env)
	}

	var onecliURL, onecliKey string
	for _, line := range strings.Split(env, "\n") {
		if value, ok := strings.CutPrefix(line, "onecli_url="); ok {
			onecliURL = strings.TrimSpace(value)
		}
		if value, ok := strings.CutPrefix(line, "onecli_key="); ok {
			onecliKey = strings.TrimSpace(value)
		}
	}
	req, err := http.NewRequest(http.MethodGet, onecliURL+"/api/container-config", nil)
	if err != nil {
		t.Fatalf("build container-config request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/container-config without auth: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		resp.Body.Close()
		t.Fatalf("GET /api/container-config without auth status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	req, err = http.NewRequest(http.MethodGet, onecliURL+"/api/container-config", nil)
	if err != nil {
		t.Fatalf("build authenticated container-config request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+onecliKey)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/container-config: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/container-config status = %d, want 200", resp.StatusCode)
	}
	var payload struct {
		Env map[string]string `json:"env"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode container config: %v", err)
	}
	if payload.Env["OPENCODE_API_KEY"] != "secret-key" {
		t.Fatalf("OPENCODE_API_KEY = %q, want secret-key", payload.Env["OPENCODE_API_KEY"])
	}
	if payload.Env["OPENCODE_MODEL"] != "opencode/deepseek-v4-flash-free" {
		t.Fatalf("OPENCODE_MODEL = %q, want configured model", payload.Env["OPENCODE_MODEL"])
	}
}

func TestLocalOneCLIShimReadsContainerEnvDynamically(t *testing.T) {
	model := "opencode/deepseek-v4-flash-free"
	shim, err := StartDynamicLocalOneCLIShim(func() map[string]string {
		return map[string]string{
			"OPENCODE_API_KEY": "secret-key",
			"OPENCODE_MODEL":   model,
		}
	})
	if err != nil {
		t.Fatalf("StartDynamicLocalOneCLIShim: %v", err)
	}
	t.Cleanup(func() { _ = shim.Stop() })

	readModel := func() string {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, shim.URL()+"/api/container-config", nil)
		if err != nil {
			t.Fatalf("build container-config request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+shim.AccessKey())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /api/container-config: %v", err)
		}
		defer resp.Body.Close()
		var payload struct {
			Env map[string]string `json:"env"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode container config: %v", err)
		}
		return payload.Env["OPENCODE_MODEL"]
	}

	if got := readModel(); got != "opencode/deepseek-v4-flash-free" {
		t.Fatalf("initial OPENCODE_MODEL = %q", got)
	}
	model = "opencode/big-pickle"
	if got := readModel(); got != "opencode/big-pickle" {
		t.Fatalf("updated OPENCODE_MODEL = %q", got)
	}
}
