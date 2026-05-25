package integration

import (
	"encoding/json"
	"testing"

	"vulpineos/internal/vault"
)

func TestControlQuery_GetMessages(t *testing.T) {
	env := newTestEnv(t)

	agent, err := env.Vault.CreateAgent("control-test", "test task", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	if err := env.Vault.AppendMessage(agent.ID, "user", "hello", 2); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if err := env.Vault.AppendMessage(agent.ID, "assistant", "world", 3); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}

	messages, err := env.Vault.GetMessages(agent.ID)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(messages))
	}

	if messages[0].Role != "user" || messages[0].Content != "hello" {
		t.Errorf("first message = %+v", messages[0])
	}
	if messages[1].Role != "assistant" || messages[1].Content != "world" {
		t.Errorf("second message = %+v", messages[1])
	}
}

func TestControlQuery_Truncation(t *testing.T) {
	env := newTestEnv(t)

	agent, err := env.Vault.CreateAgent("truncation-test", "test task", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err := env.Vault.AppendMessage(agent.ID, "user", "message", 1); err != nil {
			t.Fatalf("append message: %v", err)
		}
	}

	messages, err := env.Vault.GetRecentMessages(agent.ID, 3)
	if err != nil {
		t.Fatalf("get recent messages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(messages))
	}
}

func TestControlQuery_InvalidAgentIDRejected(t *testing.T) {
	env := newTestEnv(t)

	_, err := env.Vault.GetAgent("invalid-agent-id-12345")
	if err == nil {
		t.Fatal("expected error for invalid agent ID, got nil")
	}
}

func TestControlQuery_JSONShape(t *testing.T) {
	env := newTestEnv(t)

	agent, err := env.Vault.CreateAgent("json-shape-test", "test task", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	if err := env.Vault.AppendMessage(agent.ID, "user", "What is 2+2?", 5); err != nil {
		t.Fatalf("append message: %v", err)
	}
	if err := env.Vault.AppendMessage(agent.ID, "assistant", "4", 1); err != nil {
		t.Fatalf("append message: %v", err)
	}

	retrievedAgent, err := env.Vault.GetAgent(agent.ID)
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}

	data := map[string]interface{}{
		"agent":    retrievedAgent,
		"messages": nil,
	}

	data["messages"], _ = env.Vault.GetMessages(agent.ID)

	jsonBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &parsed); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}

	agentMap := parsed["agent"].(map[string]interface{})
	if agentMap["id"] == nil || agentMap["name"] == nil || agentMap["task"] == nil {
		t.Error("agent missing required fields")
	}

	messages := parsed["messages"].([]interface{})
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}

	msg0 := messages[0].(map[string]interface{})
	if msg0["role"] != "user" || msg0["content"] != "What is 2+2?" {
		t.Errorf("first message mismatch: %+v", msg0)
	}

	msg1 := messages[1].(map[string]interface{})
	if msg1["role"] != "assistant" || msg1["content"] != "4" {
		t.Errorf("second message mismatch: %+v", msg1)
	}

	_ = vault.AgentMessage{}
}
