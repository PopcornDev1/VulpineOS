package integration

import (
	"testing"
)

func TestStateTransition_RunToPauseRetainsContext(t *testing.T) {
	env := newTestEnv(t)

	slot, err := env.Pool.Acquire()
	if err != nil {
		t.Fatalf("pool acquire: %v", err)
	}

	availBefore, _, _ := env.Pool.Stats()

	env.Pool.Release(slot)

	availAfter, _, _ := env.Pool.Stats()

	if availAfter <= availBefore {
		t.Fatalf("available slots = %d, want > %d after release", availAfter, availBefore)
	}
}

func TestStateTransition_RunToCompleteReleasesContext(t *testing.T) {
	env := newTestEnv(t)

	slot, err := env.Pool.Acquire()
	if err != nil {
		t.Fatalf("pool acquire: %v", err)
	}

	_, _, totalBefore := env.Pool.Stats()
	contextID := slot.ContextID

	env.Pool.Release(slot)

	_, activeAfter, totalAfter := env.Pool.Stats()

	if activeAfter != 0 {
		t.Fatalf("active slots = %d, want 0 after terminal status", activeAfter)
	}

	if totalAfter != totalBefore {
		t.Fatalf("total slots = %d, want %d (slot not destroyed, returned to pool)", totalAfter, totalBefore)
	}

	if contextID == "" {
		t.Fatal("contextID should be preserved for reuse")
	}
}

func TestStateTransition_KillAgentReleasesContext(t *testing.T) {
	env := newTestEnv(t)

	env.FakeJuggler.RespondJSON("Target.closeTarget", map[string]any{})
	env.FakeJuggler.RespondJSON("Log.entryAdded", map[string]any{})

	slot, err := env.Pool.Acquire()
	if err != nil {
		t.Fatalf("pool acquire: %v", err)
	}

	_, activeBefore, _ := env.Pool.Stats()
	if activeBefore != 1 {
		t.Fatalf("active = %d, want 1 after acquire", activeBefore)
	}

	env.Pool.Release(slot)

	availAfter, activeAfter, _ := env.Pool.Stats()
	if activeAfter != 0 {
		t.Fatalf("active slots = %d, want 0 after release", activeAfter)
	}
	if availAfter != 1 {
		t.Fatalf("available slots = %d, want 1 after release", availAfter)
	}
}
