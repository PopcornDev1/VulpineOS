package tokenopt

import (
	"fmt"
	"strings"
	"sync"
)

// SnapshotDiffer tracks per-agent DOM snapshots and computes incremental diffs
// between turns, sending only changed content to reduce token usage.
type SnapshotDiffer struct {
	mu        sync.RWMutex
	snapshots map[string]string // agentID -> last snapshot JSON
}

// NewSnapshotDiffer creates a new differ instance.
func NewSnapshotDiffer() *SnapshotDiffer {
	return &SnapshotDiffer{
		snapshots: make(map[string]string),
	}
}

// Diff compares the current snapshot against the previous one for this agent.
// Returns: (result string, reduction percentage 0.0-1.0).
// If >80% of lines are the same, returns only the changed lines with context.
// If <80% same (or first snapshot), returns the full snapshot.
func (d *SnapshotDiffer) Diff(agentID, currentSnapshot string) (string, float64) {
	d.mu.Lock()
	prev, hasPrev := d.snapshots[agentID]
	d.snapshots[agentID] = currentSnapshot
	d.mu.Unlock()

	if !hasPrev {
		return currentSnapshot, 0.0
	}

	if prev == currentSnapshot {
		return `{"unchanged":true}`, 1.0
	}

	prevLines := strings.Split(prev, "\n")
	currLines := strings.Split(currentSnapshot, "\n")

	// Count matching lines using simple LCS-like scan
	matches := countMatches(prevLines, currLines)
	maxLen := len(currLines)
	if len(prevLines) > maxLen {
		maxLen = len(prevLines)
	}
	if maxLen == 0 {
		return currentSnapshot, 0.0
	}

	similarity := float64(matches) / float64(maxLen)

	// If less than 80% similar, return full snapshot
	if similarity < 0.8 {
		reduction := 1.0 - float64(len(currentSnapshot))/float64(len(currentSnapshot))
		return currentSnapshot, reduction // 0.0 reduction
	}

	// Build diff with context lines
	diff := buildDiff(prevLines, currLines)
	if len(diff) == 0 {
		return `{"unchanged":true}`, 1.0
	}

	diffStr := strings.Join(diff, "\n")
	reduction := 1.0 - float64(len(diffStr))/float64(len(currentSnapshot))
	if reduction < 0 {
		reduction = 0
	}

	return diffStr, reduction
}

// Clear removes the stored snapshot for an agent.
func (d *SnapshotDiffer) Clear(agentID string) {
	d.mu.Lock()
	delete(d.snapshots, agentID)
	d.mu.Unlock()
}

// countMatches counts how many lines in curr also appear in prev (order-independent).
func countMatches(prev, curr []string) int {
	prevSet := make(map[string]int, len(prev))
	for _, line := range prev {
		prevSet[line]++
	}

	matches := 0
	for _, line := range curr {
		if prevSet[line] > 0 {
			matches++
			prevSet[line]--
		}
	}
	return matches
}

// buildDiff produces a minimal diff showing only changed/added/removed lines
// with 1 line of context around each change.
func buildDiff(prev, curr []string) []string {
	const contextLines = 1

	// Build a set of prev lines with positions for quick lookup
	prevSet := make(map[string]bool, len(prev))
	for _, line := range prev {
		prevSet[line] = true
	}

	currSet := make(map[string]bool, len(curr))
	for _, line := range curr {
		currSet[line] = true
	}

	// Find changed line indices in curr
	changed := make([]bool, len(curr))
	anyChange := false
	for i, line := range curr {
		if !prevSet[line] {
			changed[i] = true
			anyChange = true
		}
	}

	// Find removed lines
	var removed []string
	for _, line := range prev {
		if !currSet[line] {
			removed = append(removed, fmt.Sprintf("- %s", line))
		}
	}

	if !anyChange && len(removed) == 0 {
		return nil
	}

	// Expand context around changed lines
	include := make([]bool, len(curr))
	for i := range curr {
		if changed[i] {
			for j := max(0, i-contextLines); j <= min(len(curr)-1, i+contextLines); j++ {
				include[j] = true
			}
		}
	}

	// Build output
	var result []string
	result = append(result, "--- diff ---")
	inBlock := false
	for i, line := range curr {
		if include[i] {
			if !inBlock && i > 0 {
				result = append(result, fmt.Sprintf("@@ line %d @@", i+1))
			}
			prefix := "  "
			if changed[i] {
				prefix = "+ "
			}
			result = append(result, prefix+line)
			inBlock = true
		} else {
			inBlock = false
		}
	}

	if len(removed) > 0 {
		result = append(result, "@@ removed @@")
		result = append(result, removed...)
	}

	return result
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
