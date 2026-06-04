package vault

import (
	"fmt"
	"testing"
)

func TestAppendAndListRuntimeEvents(t *testing.T) {
	db, err := OpenPath(t.TempDir() + "/vault.db")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.AppendRuntimeEvent("kernel", "error", "crashed", "kernel exited unexpectedly", map[string]string{
		"pid": "1234",
	})
	if err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}
	_, err = db.AppendRuntimeEvent("agentcore", "warn", "restart_failed", "agent restart failed", nil)
	if err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}

	events, err := db.ListRuntimeEvents(RuntimeEventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListRuntimeEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Component != "agentcore" || events[0].Event != "restart_failed" {
		t.Fatalf("unexpected newest event: %+v", events[0])
	}
	if got := events[1].Metadata["pid"]; got != "1234" {
		t.Fatalf("expected pid metadata, got %q", got)
	}
}

func TestAppendRuntimeEventTrimsHistory(t *testing.T) {
	db, err := OpenPath(t.TempDir() + "/vault.db")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for i := 0; i < defaultMaxRuntimeEvents+5; i++ {
		if _, err := db.AppendRuntimeEvent("kernel", "info", "started", fmt.Sprintf("event-%d", i), map[string]string{
			"seq": fmt.Sprintf("%d", i),
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent %d: %v", i, err)
		}
	}

	events, err := db.ListRuntimeEvents(RuntimeEventFilter{Limit: defaultMaxRuntimeEvents + 10})
	if err != nil {
		t.Fatalf("ListRuntimeEvents: %v", err)
	}
	if len(events) != defaultMaxRuntimeEvents {
		t.Fatalf("expected %d retained events, got %d", defaultMaxRuntimeEvents, len(events))
	}
	if oldest := events[len(events)-1].Metadata["seq"]; oldest != "5" {
		t.Fatalf("expected oldest retained seq 5, got %q", oldest)
	}
}

func TestListRuntimeEventsFilters(t *testing.T) {
	db, err := OpenPath(t.TempDir() + "/vault.db")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, _ = db.AppendRuntimeEvent("kernel", "info", "started", "kernel booted", map[string]string{"pid": "1"})
	_, _ = db.AppendRuntimeEvent("agentcore", "warn", "restart_failed", "restart failed for agent", map[string]string{"agent": "a-1"})
	_, _ = db.AppendRuntimeEvent("foxbridge", "error", "crashed", "foxbridge crashed hard", map[string]string{"pid": "2"})

	events, err := db.ListRuntimeEvents(RuntimeEventFilter{Level: "error", Limit: 10})
	if err != nil {
		t.Fatalf("ListRuntimeEvents level: %v", err)
	}
	if len(events) != 1 || events[0].Component != "foxbridge" {
		t.Fatalf("unexpected error filter result: %+v", events)
	}

	events, err = db.ListRuntimeEvents(RuntimeEventFilter{Query: "agent", Limit: 10})
	if err != nil {
		t.Fatalf("ListRuntimeEvents query: %v", err)
	}
	if len(events) != 1 || events[0].Component != "agentcore" {
		t.Fatalf("unexpected query filter result: %+v", events)
	}
}

func TestSetRuntimeAuditRetention(t *testing.T) {
	db, err := OpenPath(t.TempDir() + "/vault.db")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	settings, err := db.SetRuntimeAuditRetention(40)
	if err != nil {
		t.Fatalf("SetRuntimeAuditRetention: %v", err)
	}
	if settings.Retention != 40 {
		t.Fatalf("retention = %d, want 40", settings.Retention)
	}

	loaded, err := db.RuntimeAuditSettings()
	if err != nil {
		t.Fatalf("RuntimeAuditSettings: %v", err)
	}
	if loaded.Retention != 40 {
		t.Fatalf("loaded retention = %d, want 40", loaded.Retention)
	}
}
