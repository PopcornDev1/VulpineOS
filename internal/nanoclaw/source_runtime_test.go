package nanoclaw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelaxBunFrozenLockfile(t *testing.T) {
	in := "RUN --mount=type=cache,target=/root/.bun/install/cache \\\n    bun install --frozen-lockfile\n"

	got, ok := relaxBunFrozenLockfile(in)
	if !ok {
		t.Fatal("relaxBunFrozenLockfile() ok = false, want true")
	}
	if strings.Contains(got, "--frozen-lockfile") {
		t.Fatalf("relaxed Dockerfile still contains frozen lockfile flag:\n%s", got)
	}
	if !strings.Contains(got, "bun install") {
		t.Fatalf("relaxed Dockerfile lost bun install command:\n%s", got)
	}
}

func TestRelaxBunFrozenLockfileNoop(t *testing.T) {
	in := "FROM node:22-slim\n"

	got, ok := relaxBunFrozenLockfile(in)
	if ok {
		t.Fatal("relaxBunFrozenLockfile() ok = true, want false")
	}
	if got != in {
		t.Fatalf("relaxBunFrozenLockfile() changed Dockerfile without target")
	}
}

func TestPatchNanoClawOpenCodeProviderUsesInjectedAPIKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.ts")
	in := "const providerOptions = { opencode: { options: { apiKey: 'placeholder', baseURL: proxyUrl }, models: {} } };\n"
	if err := os.WriteFile(path, []byte(in), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := patchNanoClawOpenCodeProvider(path); err != nil {
		t.Fatalf("patchNanoClawOpenCodeProvider: %v", err)
	}
	if err := patchNanoClawOpenCodeProvider(path); err != nil {
		t.Fatalf("patchNanoClawOpenCodeProvider second run: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched fixture: %v", err)
	}
	got := string(data)
	want := "apiKey: process.env.OPENCODE_API_KEY || 'placeholder'"
	if !strings.Contains(got, want) {
		t.Fatalf("patched provider missing %q:\n%s", want, got)
	}
	if strings.Count(got, "process.env.OPENCODE_API_KEY") != 1 {
		t.Fatalf("patch should be idempotent, got:\n%s", got)
	}
}
