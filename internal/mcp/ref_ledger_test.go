package mcp

import (
	"fmt"
	"strings"
	"testing"
)

func TestSnapshotRefLedgerReplacesAndClearsSession(t *testing.T) {
	const sessionID = "ledger-replace"
	clearSnapshotRefSummaries(sessionID)
	t.Cleanup(func() { clearSnapshotRefSummaries(sessionID) })

	recordSnapshotRefSummaries(sessionID, []byte(`{"snapshot":{"nodes":[[1,"btn","Accept all",null,"@1"]]}}`))
	if got := snapshotRefActionTarget(sessionID, "@1"); !strings.Contains(got, `button "Accept all"`) {
		t.Fatalf("initial ref target = %q, want label", got)
	}

	recordSnapshotRefSummaries(sessionID, []byte(`{"snapshot":{"nodes":[[1,"btn","Reject all",null,"@2"]]}}`))
	if got := snapshotRefActionTarget(sessionID, "@1"); got != "@1" {
		t.Fatalf("old ref target after replacement = %q, want bare ref", got)
	}
	if got := snapshotRefActionTarget(sessionID, "@2"); !strings.Contains(got, `button "Reject all"`) {
		t.Fatalf("new ref target = %q, want replacement label", got)
	}

	clearSnapshotRefSummaries(sessionID)
	if got := snapshotRefActionTarget(sessionID, "@2"); got != "@2" {
		t.Fatalf("ref target after clear = %q, want bare ref", got)
	}
}

func TestCleanupToolSessionClearsSnapshotRefLedger(t *testing.T) {
	const sessionID = "ledger-cleanup"
	clearSnapshotRefSummaries(sessionID)
	t.Cleanup(func() { clearSnapshotRefSummaries(sessionID) })

	recordSnapshotRefSummaries(sessionID, []byte(`{"snapshot":{"nodes":[[1,"btn","Continue",null,"@1"]]}}`))
	cleanupToolSession(nil, nil, sessionID)
	if got := snapshotRefActionTarget(sessionID, "@1"); got != "@1" {
		t.Fatalf("cleanupToolSession should clear snapshot refs, got %q", got)
	}
}

func TestSnapshotRefLedgerRedactsLabelsAndClearsOnNonOptimizedSnapshot(t *testing.T) {
	const sessionID = "ledger-redact"
	clearSnapshotRefSummaries(sessionID)
	t.Cleanup(func() { clearSnapshotRefSummaries(sessionID) })

	recordSnapshotRefSummaries(sessionID, []byte(`{"snapshot":{"nodes":[[1,"btn","Continue token=secret123",null,"@1"]]}}`))
	got := snapshotRefActionTarget(sessionID, "@1")
	if strings.Contains(got, "secret123") {
		t.Fatalf("ref target leaked secret label text: %q", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("ref target = %q, want redacted marker", got)
	}

	recordSnapshotRefSummaries(sessionID, []byte(`[{"role":"document","name":"Fallback"}]`))
	if got := snapshotRefActionTarget(sessionID, "@1"); got != "@1" {
		t.Fatalf("non-optimized snapshot should clear old refs, got %q", got)
	}
}

func TestSnapshotRefLedgerIsBounded(t *testing.T) {
	for i := 0; i < MaxSnapshotRefSessions+5; i++ {
		sessionID := fmt.Sprintf("ledger-bound-%d", i)
		recordSnapshotRefSummaries(sessionID, []byte(`{"snapshot":{"nodes":[[1,"btn","Continue",null,"@1"]]}}`))
		t.Cleanup(func() { clearSnapshotRefSummaries(sessionID) })
	}
	if got := snapshotRefLedgerLen(); got > MaxSnapshotRefSessions {
		t.Fatalf("snapshot ref ledger len = %d, want <= %d", got, MaxSnapshotRefSessions)
	}
}
