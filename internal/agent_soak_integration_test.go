//go:build !race

package internal

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"vulpineos/internal/agentmsg"
	"vulpineos/internal/config"
	"vulpineos/internal/nanoclaw"
)

func TestIntegration_MultiAgentSessionSoak(t *testing.T) {
	if os.Getenv("VULPINEOS_RUN_SOAK") == "" {
		t.Skip("set VULPINEOS_RUN_SOAK=1 to run the multi-agent session soak")
	}

	iterations := 2
	if raw := os.Getenv("VULPINEOS_SOAK_ITERATIONS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			t.Fatalf("invalid VULPINEOS_SOAK_ITERATIONS %q", raw)
		}
		iterations = n
	}

	mgr := nanoclaw.NewManager("")
	if !mgr.NanoClawInstalled() {
		t.Skip("NanoClaw not installed")
	}
	defer mgr.Dispose()

	cfg, err := config.Load()
	if err != nil || !cfg.SetupComplete {
		t.Skip("VulpineOS not configured — run setup wizard first")
	}
	if err := cfg.GenerateNanoClawConfig("", cfg.BinaryPath); err != nil {
		t.Fatalf("GenerateNanoClawConfig: %v", err)
	}

	for i := 0; i < iterations; i++ {
		t.Run(fmt.Sprintf("iteration-%02d", i+1), func(t *testing.T) {
			aToken := fmt.Sprintf("ALPHA-%d", i+1)
			bToken := fmt.Sprintf("BETA-%d", i+1)
			agentA := fmt.Sprintf("soak-alpha-%02d", i+1)
			agentB := fmt.Sprintf("soak-beta-%02d", i+1)
			sessionA := "vulpine-" + agentA
			sessionB := "vulpine-" + agentB

			if _, err := mgr.SpawnWithSession(agentA, "Remember token "+aToken+" and reply exactly SAVED:"+aToken, sessionA, config.NanoClawConfigPath()); err != nil {
				t.Fatalf("spawn agent A: %v", err)
			}
			if _, err := mgr.SpawnWithSession(agentB, "Remember token "+bToken+" and reply exactly SAVED:"+bToken, sessionB, config.NanoClawConfigPath()); err != nil {
				t.Fatalf("spawn agent B: %v", err)
			}

			waitForAssistantContainsAll(t, mgr.ConversationChan(), map[string]string{
				agentA: "SAVED:" + aToken,
				agentB: "SAVED:" + bToken,
			}, 90*time.Second)

			if _, err := mgr.SpawnWithSession(agentA, "What token did I ask you to remember? Reply exactly TOKEN:"+aToken, sessionA, config.NanoClawConfigPath()); err != nil {
				t.Fatalf("resume agent A: %v", err)
			}
			if _, err := mgr.SpawnWithSession(agentB, "What token did I ask you to remember? Reply exactly TOKEN:"+bToken, sessionB, config.NanoClawConfigPath()); err != nil {
				t.Fatalf("resume agent B: %v", err)
			}

			waitForAssistantContainsAll(t, mgr.ConversationChan(), map[string]string{
				agentA: "TOKEN:" + aToken,
				agentB: "TOKEN:" + bToken,
			}, 90*time.Second)
		})
	}
}

func waitForAssistantContainsAll(t *testing.T, convCh <-chan agentmsg.ConversationMsg, wants map[string]string, timeout time.Duration) {
	t.Helper()

	pending := make(map[string]string, len(wants))
	for agentID, want := range wants {
		pending[agentID] = want
	}

	deadline := time.After(timeout)
	for len(pending) > 0 {
		select {
		case msg, ok := <-convCh:
			if !ok {
				t.Fatal("conversation channel closed")
			}
			if msg.Role != "assistant" {
				continue
			}
			want, exists := pending[msg.AgentID]
			if !exists {
				continue
			}
			t.Logf("Agent response: %s", msg.Content[:min(len(msg.Content), 200)])
			if strings.Contains(msg.Content, want) {
				delete(pending, msg.AgentID)
			}
		case <-deadline:
			t.Fatalf("assistant responses still pending: %#v", pending)
		}
	}
}
