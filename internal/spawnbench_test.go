package internal

import (
	"fmt"
	"os"
	"testing"
	"time"

	"vulpineos/internal/config"
	"vulpineos/internal/nanoclaw"
)

// TestSpawnBench measures the pre-browser "spawn -> first response" path with
// the NanoClaw daemon started in-process (no kernel/browser needed for a
// text-only task). This isolates the segment that dominates the slow startup.
//
// Gated: VULPINEOS_RUN_LIVE=1 and VULPINE_SPAWNBENCH=1. Run with
// VULPINE_SPAWN_TRACE=1 for per-stage timings, and VULPINE_NANOCLAW_SRC set so
// the daemon + agent image resolve.
func TestSpawnBench(t *testing.T) {
	requireLiveNanoClaw(t)
	if os.Getenv("VULPINE_SPAWNBENCH") == "" {
		t.Skip("set VULPINE_SPAWNBENCH=1 to run the spawn latency benchmark")
	}

	check := nanoclaw.NewManager("")
	if !check.NanoClawInstalled() {
		t.Fatal("NanoClaw not installed (set VULPINE_NANOCLAW_SRC to the nanoclaw source dir)")
	}

	cfg, err := config.Load()
	if err != nil || !cfg.SetupComplete {
		t.Fatalf("config not setupComplete (run setup / seed config.json): %v", err)
	}
	if err := cfg.GenerateNanoClawConfig("", cfg.BinaryPath); err != nil {
		t.Fatalf("GenerateNanoClawConfig: %v", err)
	}

	// --- Daemon startup (first run also builds the agent Docker image) ---
	tDaemon := time.Now()
	daemon := nanoclaw.NewDaemon("")
	daemon.SetEnv(nanoclaw.ProviderRuntimeEnv(cfg))
	if err := daemon.Start(); err != nil {
		t.Fatalf("daemon.Start: %v", err)
	}
	defer daemon.Stop()
	t.Logf("SPAWNBENCH stage=daemon.Start dur=%s", time.Since(tDaemon).Round(time.Millisecond))

	// --- Spawn one agent and wait for its first assistant response ---
	mgr := nanoclaw.NewManager("")
	convCh := mgr.ConversationChan()

	agentID := fmt.Sprintf("bench-%d", time.Now().UnixNano())
	sessionName := "vulpine-" + agentID

	tSpawn := time.Now()
	if _, err := mgr.SpawnWithSession(agentID, "Say exactly: INTEGRATION_TEST_OK", sessionName, config.NanoClawConfigPath()); err != nil {
		t.Fatalf("SpawnWithSession: %v", err)
	}
	t.Logf("SPAWNBENCH stage=SpawnWithSession-return dur=%s", time.Since(tSpawn).Round(time.Millisecond))

	deadline := time.After(5 * time.Minute)
	for {
		select {
		case msg, ok := <-convCh:
			if !ok {
				t.Fatal("conversation channel closed before response")
			}
			if msg.AgentID == agentID && msg.Role == "assistant" {
				preview := msg.Content
				if len(preview) > 120 {
					preview = preview[:120]
				}
				t.Logf("SPAWNBENCH stage=spawn->first-response dur=%s content=%q", time.Since(tSpawn).Round(time.Millisecond), preview)
				return
			}
		case <-deadline:
			t.Fatalf("no assistant response within 5m (spawn elapsed=%s)", time.Since(tSpawn).Round(time.Millisecond))
		}
	}
}
