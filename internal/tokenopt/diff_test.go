package tokenopt

import (
	"strings"
	"testing"
)

func TestDiffFirstSnapshot(t *testing.T) {
	d := NewSnapshotDiffer()
	result, reduction := d.Diff("agent1", `{"nodes":[[0,"doc","Page"]]}`)
	if reduction != 0.0 {
		t.Fatalf("first snapshot should have 0 reduction, got %v", reduction)
	}
	if result != `{"nodes":[[0,"doc","Page"]]}` {
		t.Fatalf("first snapshot should return full content, got %s", result)
	}
}

func TestDiffIdentical(t *testing.T) {
	d := NewSnapshotDiffer()
	snapshot := `{"nodes":[[0,"doc","Page"],[1,"h1","Title"]]}` + "\n"
	d.Diff("agent1", snapshot)

	result, reduction := d.Diff("agent1", snapshot)
	if reduction != 1.0 {
		t.Fatalf("identical snapshots should have 100%% reduction, got %v", reduction)
	}
	if result != `{"unchanged":true}` {
		t.Fatalf("expected unchanged marker, got %s", result)
	}
}

func TestDiffMinorChange(t *testing.T) {
	d := NewSnapshotDiffer()

	// Create a snapshot with many lines
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, `[1,"btn","Button"]`)
	}
	snapshot1 := strings.Join(lines, "\n")
	d.Diff("agent1", snapshot1)

	// Change just one line (>80% same)
	lines[10] = `[1,"btn","Changed"]`
	snapshot2 := strings.Join(lines, "\n")

	result, reduction := d.Diff("agent1", snapshot2)

	if reduction <= 0 {
		t.Fatalf("expected positive reduction for minor change, got %v", reduction)
	}
	if !strings.Contains(result, "Changed") {
		t.Fatalf("diff should contain the changed line, got %s", result)
	}
	if !strings.Contains(result, "--- diff ---") {
		t.Fatalf("diff should have header, got %s", result)
	}
}

func TestDiffMajorChange(t *testing.T) {
	d := NewSnapshotDiffer()

	snapshot1 := "line1\nline2\nline3\nline4\nline5"
	d.Diff("agent1", snapshot1)

	// Completely different content (<80% same)
	snapshot2 := "alpha\nbeta\ngamma\ndelta\nepsilon"
	result, reduction := d.Diff("agent1", snapshot2)

	// Should return full snapshot
	if result != snapshot2 {
		t.Fatalf("major change should return full snapshot, got %s", result)
	}
	if reduction != 0.0 {
		t.Fatalf("major change should have 0 reduction, got %v", reduction)
	}
}

func TestDiffClear(t *testing.T) {
	d := NewSnapshotDiffer()
	d.Diff("agent1", "snapshot1")
	d.Clear("agent1")

	// After clear, should behave like first snapshot
	result, reduction := d.Diff("agent1", "snapshot2")
	if reduction != 0.0 {
		t.Fatalf("after clear, should be like first snapshot, got reduction %v", reduction)
	}
	if result != "snapshot2" {
		t.Fatalf("expected full snapshot after clear, got %s", result)
	}
}

func TestDiffMultipleAgents(t *testing.T) {
	d := NewSnapshotDiffer()
	d.Diff("agent1", "snap-a")
	d.Diff("agent2", "snap-b")

	// Agent1 identical
	result1, red1 := d.Diff("agent1", "snap-a")
	if red1 != 1.0 {
		t.Fatal("agent1 identical should be 1.0 reduction")
	}
	if result1 != `{"unchanged":true}` {
		t.Fatal("agent1 should be unchanged")
	}

	// Agent2 different — full return (short string = <80% similar)
	result2, _ := d.Diff("agent2", "snap-c")
	if result2 != "snap-c" {
		t.Fatalf("agent2 full change should return full snapshot, got %s", result2)
	}
}

func TestCountMatches(t *testing.T) {
	prev := []string{"a", "b", "c", "d"}
	curr := []string{"a", "b", "e", "d"}

	m := countMatches(prev, curr)
	if m != 3 {
		t.Fatalf("expected 3 matches, got %d", m)
	}
}

func TestBuildDiffEmpty(t *testing.T) {
	prev := []string{"a", "b", "c"}
	curr := []string{"a", "b", "c"}

	diff := buildDiff(prev, curr)
	if diff != nil {
		t.Fatalf("identical lines should produce nil diff, got %v", diff)
	}
}
