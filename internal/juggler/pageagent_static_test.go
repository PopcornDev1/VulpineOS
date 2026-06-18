package juggler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPageAgentAXPendingWaitIsBounded(t *testing.T) {
	path := filepath.Join("..", "..", "additions", "juggler", "content", "PageAgent.js")
	srcBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read PageAgent.js: %v", err)
	}
	src := string(srcBytes)
	if !strings.Contains(src, "async function waitForAXPending") {
		t.Fatalf("PageAgent.js missing bounded AX pending helper")
	}
	if got := strings.Count(src, "while (docAcc.document.isUpdatePendingForJugglerAccessibility)"); got != 1 {
		t.Fatalf("PageAgent.js AX pending wait loop count = %d, want only bounded helper loop", got)
	}
}
