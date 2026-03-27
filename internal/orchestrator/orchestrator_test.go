package orchestrator

import (
	"encoding/json"
	"testing"
)

func TestStatusType(t *testing.T) {
	s := Status{
		KernelRunning:  true,
		KernelPID:      1234,
		PoolAvailable:  5,
		PoolActive:     3,
		PoolTotal:      8,
		ActiveAgents:   2,
		TotalCitizens:  10,
		TotalTemplates: 3,
	}

	// Verify JSON serialization round-trips correctly
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var s2 Status
	if err := json.Unmarshal(data, &s2); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if s2.KernelRunning != true {
		t.Error("KernelRunning should be true")
	}
	if s2.KernelPID != 1234 {
		t.Errorf("KernelPID = %d, want 1234", s2.KernelPID)
	}
	if s2.PoolAvailable != 5 {
		t.Errorf("PoolAvailable = %d, want 5", s2.PoolAvailable)
	}
	if s2.PoolActive != 3 {
		t.Errorf("PoolActive = %d, want 3", s2.PoolActive)
	}
	if s2.PoolTotal != 8 {
		t.Errorf("PoolTotal = %d, want 8", s2.PoolTotal)
	}
	if s2.ActiveAgents != 2 {
		t.Errorf("ActiveAgents = %d, want 2", s2.ActiveAgents)
	}
	if s2.TotalCitizens != 10 {
		t.Errorf("TotalCitizens = %d, want 10", s2.TotalCitizens)
	}
	if s2.TotalTemplates != 3 {
		t.Errorf("TotalTemplates = %d, want 3", s2.TotalTemplates)
	}
}

func TestAgentResultType(t *testing.T) {
	r := AgentResult{
		AgentID: "agent-1",
		Status:  "completed",
		Result:  "task done",
		Err:     nil,
	}
	if r.AgentID != "agent-1" {
		t.Errorf("AgentID = %q, want %q", r.AgentID, "agent-1")
	}
	if r.Status != "completed" {
		t.Errorf("Status = %q, want %q", r.Status, "completed")
	}
	if r.Result != "task done" {
		t.Errorf("Result = %q, want %q", r.Result, "task done")
	}
	if r.Err != nil {
		t.Errorf("Err = %v, want nil", r.Err)
	}
}

func TestStatusJSONFieldNames(t *testing.T) {
	s := Status{
		KernelRunning: true,
		PoolAvailable: 1,
	}
	data, _ := json.Marshal(s)
	var m map[string]interface{}
	json.Unmarshal(data, &m)

	expectedFields := []string{
		"kernel_running", "kernel_pid", "pool_available", "pool_active",
		"pool_total", "active_agents", "total_citizens", "total_templates",
	}
	for _, field := range expectedFields {
		if _, ok := m[field]; !ok {
			t.Errorf("missing JSON field %q", field)
		}
	}
}

func TestStatusZeroValues(t *testing.T) {
	s := Status{}
	if s.KernelRunning {
		t.Error("zero Status should have KernelRunning=false")
	}
	if s.ActiveAgents != 0 {
		t.Errorf("zero Status ActiveAgents = %d, want 0", s.ActiveAgents)
	}
}
