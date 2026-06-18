package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

// LoopDetector tracks repeated identical tool calls per session. Current-page
// history is reset after navigation, while repeated navigations are tracked
// across resets so broad browsing prompts cannot loop through the same URLs.
type LoopDetector struct {
	mu         sync.Mutex
	history    map[string][]string // sessionID → list of recent current-page action hashes
	navigation map[string][]string // sessionID → list of recent navigation action hashes
	maxRepeat  int                 // repeated identical actions before warning
}

func NewLoopDetector(maxRepeat int) *LoopDetector {
	if maxRepeat <= 0 {
		maxRepeat = 3
	}
	return &LoopDetector{
		history:    make(map[string][]string),
		navigation: make(map[string][]string),
		maxRepeat:  maxRepeat,
	}
}

// Check returns a warning message if a loop is detected, or empty string if ok.
func (ld *LoopDetector) Check(sessionID, toolName, argsStr string) string {
	ld.mu.Lock()
	defer ld.mu.Unlock()

	hash := hashAction(toolName, argsStr)
	history := ld.history[sessionID]

	// Count consecutive identical actions at the end of current-page history.
	consecutive := 0
	for i := len(history) - 1; i >= 0; i-- {
		if history[i] == hash {
			consecutive++
		} else {
			break
		}
	}

	repeated := 0
	if isNavigationAction(toolName) {
		for _, h := range ld.navigation[sessionID] {
			if h == hash {
				repeated++
			}
		}
	}

	if isNavigationAction(toolName) {
		navHistory := append(ld.navigation[sessionID], hash)
		if len(navHistory) > 20 {
			navHistory = navHistory[len(navHistory)-20:]
		}
		ld.navigation[sessionID] = navHistory
	}

	// Add current action to current-page history.
	history = append(history, hash)
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	ld.history[sessionID] = history

	if consecutive >= ld.maxRepeat {
		return fmt.Sprintf("WARNING: You have called %s with the same arguments %d times in a row. This action is not making progress. Try a different approach: use vulpine_find to locate the correct element, vulpine_verify to check page state, or vulpine_page_info to reassess the situation.", toolName, consecutive+1)
	}

	if repeated+1 >= ld.maxRepeat {
		return fmt.Sprintf("WARNING: You have called %s with the same arguments %d times in this session. This is not making progress. Do not revisit the same page again; summarize what you already observed or use a different inspection approach.", toolName, repeated+1)
	}

	return ""
}

// Reset clears current-page history for a session (e.g. after navigation to a
// new page). Repeated navigations are intentionally preserved.
func (ld *LoopDetector) Reset(sessionID string) {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	delete(ld.history, sessionID)
}

func isNavigationAction(toolName string) bool {
	return toolName == "vulpine_navigate"
}

func hashAction(tool, args string) string {
	h := sha256.Sum256([]byte(tool + ":" + args))
	return hex.EncodeToString(h[:8])
}
