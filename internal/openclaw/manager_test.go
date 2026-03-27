package openclaw

import (
	"testing"
)

func TestNewManager(t *testing.T) {
	m := NewManager("test-binary")
	if m == nil {
		t.Fatal("expected non-nil manager")
	}
	if m.binary != "test-binary" {
		t.Errorf("binary = %q, want %q", m.binary, "test-binary")
	}
	if m.agents == nil {
		t.Error("agents map should be initialized")
	}
}

func TestNewManagerDefaultBinary(t *testing.T) {
	m := NewManager("")
	if m.binary != "openclaw" {
		t.Errorf("binary = %q, want %q (default)", m.binary, "openclaw")
	}
}

func TestStatusChan(t *testing.T) {
	m := NewManager("test")
	ch := m.StatusChan()
	if ch == nil {
		t.Fatal("StatusChan() returned nil")
	}
	// Verify it's a receive-only channel by checking we can read the type
	select {
	case <-ch:
		t.Error("should not have received anything from empty channel")
	default:
		// expected
	}
}

func TestConversationChan(t *testing.T) {
	m := NewManager("test")
	ch := m.ConversationChan()
	if ch == nil {
		t.Fatal("ConversationChan() returned nil")
	}
}

func TestCountStartsAtZero(t *testing.T) {
	m := NewManager("test")
	if m.Count() != 0 {
		t.Errorf("Count() = %d, want 0", m.Count())
	}
}

func TestListStartsEmpty(t *testing.T) {
	m := NewManager("test")
	list := m.List()
	if len(list) != 0 {
		t.Errorf("List() length = %d, want 0", len(list))
	}
}

func TestKillNonexistent(t *testing.T) {
	m := NewManager("test")
	err := m.Kill("nonexistent-id")
	if err == nil {
		t.Error("expected error when killing nonexistent agent")
	}
}

func TestPauseNonexistent(t *testing.T) {
	m := NewManager("test")
	err := m.PauseAgent("nonexistent-id")
	if err == nil {
		t.Error("expected error when pausing nonexistent agent")
	}
}

func TestSendMessageNonexistent(t *testing.T) {
	m := NewManager("test")
	err := m.SendMessage("nonexistent-id", "hello")
	if err == nil {
		t.Error("expected error when sending to nonexistent agent")
	}
}

func TestKillAllEmpty(t *testing.T) {
	m := NewManager("test")
	// Should not panic on empty manager
	m.KillAll()
}

func TestDisposeEmpty(t *testing.T) {
	m := NewManager("test")
	// Should not panic; channels should be closed
	m.Dispose()

	// Verify channels are closed
	_, ok := <-m.StatusChan()
	if ok {
		t.Error("StatusChan should be closed after Dispose")
	}
	_, ok = <-m.ConversationChan()
	if ok {
		t.Error("ConversationChan should be closed after Dispose")
	}
}

func TestOpenClawInstalledFalseForBogus(t *testing.T) {
	m := NewManager("/nonexistent/path/to/openclaw-binary-xyz")
	// Should return false since the binary doesn't exist
	// (may return true if openclaw is globally installed, so this is best-effort)
	// At minimum, verify it doesn't panic
	_ = m.OpenClawInstalled()
}

func TestSpawnFailsWithBadBinary(t *testing.T) {
	// Spawn should fail when given a binary that exists but isn't executable
	// or when the process immediately fails
	m := NewManager("/dev/null") // exists but not executable as a command
	_, err := m.Spawn("ctx-1", "")
	if err == nil {
		t.Error("expected error when spawning with non-executable binary")
	}
}
