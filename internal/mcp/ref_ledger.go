package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	MaxSnapshotRefSessions  = 256
	maxSnapshotRefsPerPage  = 512
	maxSnapshotRefNameRunes = 120
)

type snapshotRefSummary struct {
	Ref  string
	Role string
	Name string
}

var snapshotRefLedger = struct {
	sync.Mutex
	bySession  map[string]map[string]snapshotRefSummary
	lastAccess map[string]time.Time
}{
	bySession:  make(map[string]map[string]snapshotRefSummary),
	lastAccess: make(map[string]time.Time),
}

func snapshotTextResult(sessionID string, raw []byte) *ToolCallResult {
	recordSnapshotRefSummaries(sessionID, raw)
	return textResult(string(raw))
}

func recordSnapshotRefSummaries(sessionID string, raw []byte) {
	if sessionID == "" {
		return
	}

	refs := extractSnapshotRefSummaries(raw)
	snapshotRefLedger.Lock()
	defer snapshotRefLedger.Unlock()
	if len(refs) == 0 {
		delete(snapshotRefLedger.bySession, sessionID)
		delete(snapshotRefLedger.lastAccess, sessionID)
		return
	}
	if _, exists := snapshotRefLedger.bySession[sessionID]; !exists {
		for len(snapshotRefLedger.bySession) >= MaxSnapshotRefSessions {
			evictOldestSnapshotRefsLocked()
		}
	}
	snapshotRefLedger.bySession[sessionID] = refs
	snapshotRefLedger.lastAccess[sessionID] = time.Now()
}

func clearSnapshotRefSummaries(sessionID string) {
	if sessionID == "" {
		return
	}
	snapshotRefLedger.Lock()
	defer snapshotRefLedger.Unlock()
	delete(snapshotRefLedger.bySession, sessionID)
	delete(snapshotRefLedger.lastAccess, sessionID)
}

func snapshotRefLedgerLen() int {
	snapshotRefLedger.Lock()
	defer snapshotRefLedger.Unlock()
	return len(snapshotRefLedger.bySession)
}

func evictOldestSnapshotRefsLocked() {
	var oldestID string
	var oldest time.Time
	first := true
	for sessionID, accessed := range snapshotRefLedger.lastAccess {
		if first || accessed.Before(oldest) {
			oldestID = sessionID
			oldest = accessed
			first = false
		}
	}
	if oldestID == "" {
		for sessionID := range snapshotRefLedger.bySession {
			oldestID = sessionID
			break
		}
	}
	delete(snapshotRefLedger.bySession, oldestID)
	delete(snapshotRefLedger.lastAccess, oldestID)
}

func snapshotRefActionTarget(sessionID, ref string) string {
	summary, ok := lookupSnapshotRefSummary(sessionID, ref)
	if !ok {
		return ref
	}
	label := snapshotRefLabel(summary)
	if label == "" {
		return ref
	}
	return fmt.Sprintf("%s %s", ref, label)
}

func lookupSnapshotRefSummary(sessionID, ref string) (snapshotRefSummary, bool) {
	snapshotRefLedger.Lock()
	defer snapshotRefLedger.Unlock()
	refs := snapshotRefLedger.bySession[sessionID]
	if refs == nil {
		return snapshotRefSummary{}, false
	}
	summary, ok := refs[ref]
	if ok {
		snapshotRefLedger.lastAccess[sessionID] = time.Now()
	}
	return summary, ok
}

func snapshotRefLabel(summary snapshotRefSummary) string {
	role := snapshotRefRole(summary.Role)
	name := strings.TrimSpace(summary.Name)
	if role == "" && name == "" {
		return ""
	}
	if role == "" {
		return fmt.Sprintf("%q", name)
	}
	if name == "" {
		return role
	}
	return fmt.Sprintf("%s %q", role, name)
}

func extractSnapshotRefSummaries(raw []byte) map[string]snapshotRefSummary {
	var payload struct {
		Snapshot optimizedSnapshotForRefs `json:"snapshot"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil && len(payload.Snapshot.Nodes) > 0 {
		return refsFromOptimizedSnapshot(payload.Snapshot)
	}

	var snapshot optimizedSnapshotForRefs
	if err := json.Unmarshal(raw, &snapshot); err == nil && len(snapshot.Nodes) > 0 {
		return refsFromOptimizedSnapshot(snapshot)
	}
	return nil
}

type optimizedSnapshotForRefs struct {
	Title string            `json:"title"`
	URL   string            `json:"url"`
	Nodes []json.RawMessage `json:"nodes"`
}

func refsFromOptimizedSnapshot(snapshot optimizedSnapshotForRefs) map[string]snapshotRefSummary {
	refs := make(map[string]snapshotRefSummary)
	for _, rawNode := range snapshot.Nodes {
		if len(refs) >= maxSnapshotRefsPerPage {
			break
		}
		var node []interface{}
		if err := json.Unmarshal(rawNode, &node); err != nil || len(node) < 5 {
			continue
		}
		ref, _ := node[4].(string)
		if !strings.HasPrefix(ref, "@") {
			continue
		}
		role, _ := node[1].(string)
		name, _ := node[2].(string)
		refs[ref] = snapshotRefSummary{
			Ref:  ref,
			Role: role,
			Name: compactSnapshotRefText(name, maxSnapshotRefNameRunes),
		}
	}
	return refs
}

func compactSnapshotRefText(value string, maxRunes int) string {
	value = redactMCPDisplayText(value)
	value = strings.Join(strings.Fields(value), " ")
	if maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "..."
}

func snapshotRefRole(code string) string {
	switch code {
	case "btn":
		return "button"
	case "a":
		return "link"
	case "inp":
		return "input"
	case "chk":
		return "checkbox"
	case "rad":
		return "radio"
	case "sel":
		return "select"
	case "mi":
		return "menu item"
	case "tab":
		return "tab"
	case "sw":
		return "switch"
	case "slider":
		return "slider"
	case "spin":
		return "spinbutton"
	default:
		return code
	}
}
