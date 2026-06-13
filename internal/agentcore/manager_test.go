package agentcore

import (
	"testing"

	"vulpineos/internal/vault"
)

type fakeStore struct {
	agent        *vault.Agent
	err          error
	messages     []vault.AgentMessage
	appendErr    error
}

func (f *fakeStore) GetAgent(id string) (*vault.Agent, error) { return f.agent, f.err }
func (f *fakeStore) AppendMessage(agentID, role, content string, tokens int) error {
	if f.appendErr != nil {
		return f.appendErr
	}
	f.messages = append(f.messages, vault.AgentMessage{
		AgentID: agentID,
		Role:    role,
		Content: content,
		Tokens:  tokens,
	})
	return nil
}

func TestResolveContextID(t *testing.T) {
	m := NewManager(nil, Config{})
	defer m.Dispose()

	// No store -> empty.
	if got := m.resolveContextID("a1"); got != "" {
		t.Errorf("no store: got %q, want empty", got)
	}

	// Store with a pooled context id in metadata -> resolved.
	m.SetVault(&fakeStore{agent: &vault.Agent{
		ID:       "a1",
		Metadata: vault.MarshalAgentMetadata(vault.AgentMetadata{ContextID: "ctx-123"}),
	}})
	if got := m.resolveContextID("a1"); got != "ctx-123" {
		t.Errorf("resolved context = %q, want ctx-123", got)
	}

	// Empty/blank metadata -> empty.
	m.SetVault(&fakeStore{agent: &vault.Agent{ID: "a1", Metadata: "{}"}})
	if got := m.resolveContextID("a1"); got != "" {
		t.Errorf("blank metadata: got %q, want empty", got)
	}
}
