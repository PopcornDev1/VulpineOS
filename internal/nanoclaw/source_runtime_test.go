package nanoclaw

import (
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
