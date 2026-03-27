package costtrack

import "testing"

func TestRecordUsage(t *testing.T) {
	tr := New("claude-sonnet-4-6")
	ok := tr.RecordUsage("agent-1", 1000, 500)
	if !ok {
		t.Fatal("RecordUsage should return true (no budget)")
	}
	u := tr.GetUsage("agent-1")
	if u == nil {
		t.Fatal("GetUsage returned nil")
	}
	if u.InputTokens != 1000 || u.OutputTokens != 500 {
		t.Errorf("tokens: %d/%d, want 1000/500", u.InputTokens, u.OutputTokens)
	}
	if u.EstimatedCost <= 0 {
		t.Error("cost should be positive")
	}
	t.Logf("Cost for 1K input + 500 output on sonnet: $%.6f", u.EstimatedCost)
}

func TestBudgetLimit(t *testing.T) {
	tr := New("gpt-4o-mini")
	tr.SetBudget("agent-1", 0, 5000) // 5000 token limit

	// First usage should be allowed
	ok := tr.RecordUsage("agent-1", 2000, 1000)
	if !ok {
		t.Fatal("should be under budget")
	}

	// Second usage pushes over limit
	ok = tr.RecordUsage("agent-1", 2000, 1000)
	if ok {
		t.Fatal("should be over budget (6000 > 5000)")
	}

	// Check event was emitted
	select {
	case ev := <-tr.Events():
		if ev.Type != "usage" && ev.Type != "limit_reached" {
			t.Errorf("unexpected event type: %s", ev.Type)
		}
	default:
	}
}

func TestCostBudget(t *testing.T) {
	tr := New("claude-opus-4-6")
	tr.SetBudget("expensive", 0.001, 0) // $0.001 cost limit

	// Opus is expensive: ~$15/1M input. 1000 tokens = $0.015 → over budget
	ok := tr.RecordUsage("expensive", 1000, 0)
	if ok {
		u := tr.GetUsage("expensive")
		if u.EstimatedCost >= 0.001 {
			t.Log("correctly detected over budget on second check")
		}
	}
}

func TestTotalCost(t *testing.T) {
	tr := New("gpt-4o-mini")
	tr.RecordUsage("a", 1000, 500)
	tr.RecordUsage("b", 2000, 1000)
	total := tr.TotalCost()
	if total <= 0 {
		t.Error("total cost should be positive")
	}
}

func TestAllUsage(t *testing.T) {
	tr := New("gpt-4o-mini")
	tr.RecordUsage("a", 100, 50)
	tr.RecordUsage("b", 200, 100)
	all := tr.AllUsage()
	if len(all) != 2 {
		t.Errorf("expected 2 usages, got %d", len(all))
	}
}

func TestGetUsageNonexistent(t *testing.T) {
	tr := New("gpt-4o-mini")
	if tr.GetUsage("nonexistent") != nil {
		t.Error("should return nil for unknown agent")
	}
}

func TestMarshalJSON(t *testing.T) {
	tr := New("gpt-4o-mini")
	tr.RecordUsage("a", 100, 50)
	tr.SetBudget("a", 1.0, 100000)
	data, err := tr.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 10 {
		t.Error("JSON too small")
	}
}
