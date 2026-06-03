package rendercohort

import (
	"strconv"
	"testing"

	"vulpineos/internal/extensions"
)

func cohortSet() []extensions.RenderCohort {
	return []extensions.RenderCohort{
		{Key: "mac_a", ProfileID: "p-mac-a", OS: "mac", ConfigBlob: "blob"},
		{Key: "mac_b", ProfileID: "p-mac-b", OS: "mac", ConfigBlob: "blob"},
		{Key: "win_a", ProfileID: "p-win-a", OS: "win", ConfigBlob: "blob"},
		{Key: "lin_a", ProfileID: "p-lin-a", OS: "lin", ConfigBlob: "blob"},
	}
}

func TestAssignMatchesClaimedOS(t *testing.T) {
	for _, tc := range []struct{ platform, wantOS string }{
		{"MacIntel", "mac"},
		{"Win32", "win"},
		{"Linux x86_64", "lin"},
	} {
		c, ok := Assign(cohortSet(), "agent-1", tc.platform)
		if !ok {
			t.Fatalf("platform %q: expected an assignment", tc.platform)
		}
		if c.OS != tc.wantOS {
			t.Fatalf("platform %q assigned OS %q, want %q", tc.platform, c.OS, tc.wantOS)
		}
	}
}

func TestAssignIsDeterministic(t *testing.T) {
	first, ok := Assign(cohortSet(), "stable-seed", "MacIntel")
	if !ok {
		t.Fatal("expected assignment")
	}
	for i := 0; i < 50; i++ {
		again, ok := Assign(cohortSet(), "stable-seed", "MacIntel")
		if !ok || again.Key != first.Key {
			t.Fatalf("assignment not deterministic: %q vs %q", first.Key, again.Key)
		}
	}
}

func TestAssignSpreadsAcrossPool(t *testing.T) {
	seen := map[string]bool{}
	for _, seed := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		c, ok := Assign(cohortSet(), seed, "MacIntel")
		if !ok {
			t.Fatalf("seed %q: expected assignment", seed)
		}
		seen[c.Key] = true
	}
	if len(seen) < 2 {
		t.Fatalf("expected assignments to spread across the mac pool, got %v", seen)
	}
}

func TestAssignFailsWhenNoMatchingOS(t *testing.T) {
	macOnly := []extensions.RenderCohort{{Key: "mac_a", OS: "mac", ConfigBlob: "b"}}
	if _, ok := Assign(macOnly, "agent", "Win32"); ok {
		t.Fatal("expected no assignment when no cohort matches the claimed OS")
	}
	if _, ok := Assign(macOnly, "agent", ""); ok {
		t.Fatal("expected no assignment for an empty claimed OS")
	}
	if _, ok := Assign(nil, "agent", "MacIntel"); ok {
		t.Fatal("expected no assignment from an empty cohort set")
	}
}

func TestAssignStableRegardlessOfOrder(t *testing.T) {
	forward := cohortSet()
	reversed := []extensions.RenderCohort{forward[3], forward[2], forward[1], forward[0]}
	a, _ := Assign(forward, "seed-x", "MacIntel")
	b, _ := Assign(reversed, "seed-x", "MacIntel")
	if a.Key != b.Key {
		t.Fatalf("assignment depends on enumeration order: %q vs %q", a.Key, b.Key)
	}
}

func TestNormalizeOS(t *testing.T) {
	cases := map[string]string{
		"MacIntel": "mac", "darwin": "mac", "Win32": "win", "Windows NT 10.0": "win",
		"Linux x86_64": "lin", "X11; Ubuntu": "lin", "": "", "solaris": "",
	}
	for in, want := range cases {
		if got := NormalizeOS(in); got != want {
			t.Fatalf("NormalizeOS(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAssignPrevalenceWeightedMatchesDistribution(t *testing.T) {
	// macA is ~9x more prevalent than macB; assignment across many agents should
	// land near that 90/10 split.
	cohorts := []extensions.RenderCohort{
		{Key: "mac_a", OS: "mac", ConfigBlob: "b", Weight: 90},
		{Key: "mac_b", OS: "mac", ConfigBlob: "b", Weight: 10},
	}
	counts := map[string]int{}
	const n = 4000
	for i := 0; i < n; i++ {
		c, ok := Assign(cohorts, "agent-"+strconv.Itoa(i), "MacIntel")
		if !ok {
			t.Fatalf("seed %d: expected assignment", i)
		}
		counts[c.Key]++
	}
	shareA := float64(counts["mac_a"]) / float64(n)
	if shareA < 0.83 || shareA > 0.97 {
		t.Fatalf("weighted share for mac_a = %.3f, want ~0.90 (counts=%v)", shareA, counts)
	}
}

func TestAssignWeightedIsDeterministic(t *testing.T) {
	cohorts := []extensions.RenderCohort{
		{Key: "mac_a", OS: "mac", ConfigBlob: "b", Weight: 70},
		{Key: "mac_b", OS: "mac", ConfigBlob: "b", Weight: 30},
	}
	first, ok := Assign(cohorts, "stable", "MacIntel")
	if !ok {
		t.Fatal("expected assignment")
	}
	for i := 0; i < 50; i++ {
		again, _ := Assign(cohorts, "stable", "MacIntel")
		if again.Key != first.Key {
			t.Fatalf("weighted assignment not deterministic: %q vs %q", first.Key, again.Key)
		}
	}
}

func TestAssignFallsBackToUniformWhenWeightsIncomplete(t *testing.T) {
	// One cohort has an unknown (0) weight -> the whole pool must stay uniform so
	// no cohort is silently dropped.
	cohorts := []extensions.RenderCohort{
		{Key: "mac_a", OS: "mac", ConfigBlob: "b", Weight: 100},
		{Key: "mac_b", OS: "mac", ConfigBlob: "b", Weight: 0},
	}
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c, _ := Assign(cohorts, "agent-"+strconv.Itoa(i), "MacIntel")
		seen[c.Key] = true
	}
	if !seen["mac_a"] || !seen["mac_b"] {
		t.Fatalf("incomplete weights should fall back to uniform (both selectable), got %v", seen)
	}
}
