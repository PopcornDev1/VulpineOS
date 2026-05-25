package nanoclaw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
