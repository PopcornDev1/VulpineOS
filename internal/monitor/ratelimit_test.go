package monitor

import (
	"testing"
	"time"
)

func TestCheckMessage_RateLimit(t *testing.T) {
	m := New()
	defer m.Dispose()

	m.CheckMessage("agent1", "Error: HTTP 429 Too Many Requests")

	select {
	case a := <-m.AlertChan():
		if a.Type != AlertRateLimit {
			t.Errorf("type = %v, want %v", a.Type, AlertRateLimit)
		}
		if a.AgentID != "agent1" {
			t.Errorf("agentID = %s, want agent1", a.AgentID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected rate limit alert")
	}
}

func TestCheckMessage_RateLimit_CaseInsensitive(t *testing.T) {
	m := New()
	defer m.Dispose()

	m.CheckMessage("a1", "RATE LIMIT exceeded")

	select {
	case a := <-m.AlertChan():
		if a.Type != AlertRateLimit {
			t.Errorf("type = %v, want %v", a.Type, AlertRateLimit)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected rate limit alert")
	}
}

func TestCheckMessage_Captcha(t *testing.T) {
	m := New()
	defer m.Dispose()

	m.CheckMessage("agent2", "Please complete the CAPTCHA to continue")

	select {
	case a := <-m.AlertChan():
		if a.Type != AlertCaptcha {
			t.Errorf("type = %v, want %v", a.Type, AlertCaptcha)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected captcha alert")
	}
}

func TestCheckMessage_VerifyHuman(t *testing.T) {
	m := New()
	defer m.Dispose()

	m.CheckMessage("agent3", "Please verify you are human before proceeding")

	select {
	case a := <-m.AlertChan():
		if a.Type != AlertCaptcha {
			t.Errorf("type = %v, want %v", a.Type, AlertCaptcha)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected captcha alert")
	}
}

func TestCheckMessage_IPBlock_RequiresRepeated(t *testing.T) {
	m := New()
	defer m.Dispose()

	// First match — should NOT trigger alert
	m.CheckMessage("agent4", "Access Denied")

	select {
	case <-m.AlertChan():
		t.Fatal("should not alert on first ip_block match")
	case <-time.After(50 * time.Millisecond):
		// expected
	}

	// Second match — should trigger
	m.CheckMessage("agent4", "Your IP has been blocked")

	select {
	case a := <-m.AlertChan():
		if a.Type != AlertIPBlock {
			t.Errorf("type = %v, want %v", a.Type, AlertIPBlock)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected ip block alert after 2nd match")
	}
}

func TestCheckMessage_IPBlock_PerAgent(t *testing.T) {
	m := New()
	defer m.Dispose()

	// One block for agent5, one for agent6 — neither should alert
	m.CheckMessage("agent5", "Forbidden")
	m.CheckMessage("agent6", "Forbidden")

	select {
	case <-m.AlertChan():
		t.Fatal("should not alert — different agents, only 1 match each")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestCheckMessage_NoMatch(t *testing.T) {
	m := New()
	defer m.Dispose()

	m.CheckMessage("agent7", "Page loaded successfully with 200 OK")

	select {
	case <-m.AlertChan():
		t.Fatal("should not alert on normal content")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestDispose(t *testing.T) {
	m := New()
	m.Dispose()

	// Should not panic or send after dispose
	m.CheckMessage("agent8", "429 rate limited")

	// Channel should be closed
	_, ok := <-m.AlertChan()
	if ok {
		t.Fatal("expected closed channel after dispose")
	}
}
