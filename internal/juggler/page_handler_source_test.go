package juggler

import (
	"os"
	"strings"
	"testing"
)

func TestPageNavigateAllowsMainFrameFallback(t *testing.T) {
	handlerData, err := os.ReadFile("../../additions/juggler/protocol/PageHandler.js")
	if err != nil {
		t.Fatalf("read PageHandler.js: %v", err)
	}
	handlerSource := string(handlerData)

	for _, needle := range []string{
		"async ['Page.navigate']({frameId, url, referer})",
		"frameId ? this._pageTarget.frameIdToBrowsingContext(frameId) : this._pageTarget.mainBrowsingContext()",
		"Cannot navigate: no browsing context for frameId",
	} {
		if !strings.Contains(handlerSource, needle) {
			t.Fatalf("PageHandler.js missing %q", needle)
		}
	}

	targetData, err := os.ReadFile("../../additions/juggler/TargetRegistry.js")
	if err != nil {
		t.Fatalf("read TargetRegistry.js: %v", err)
	}
	targetSource := string(targetData)
	for _, needle := range []string{
		"mainBrowsingContext()",
		"return this._linkedBrowser.browsingContext;",
	} {
		if !strings.Contains(targetSource, needle) {
			t.Fatalf("TargetRegistry.js missing %q", needle)
		}
	}

	protocolData, err := os.ReadFile("../../additions/juggler/protocol/Protocol.js")
	if err != nil {
		t.Fatalf("read Protocol.js: %v", err)
	}
	if !strings.Contains(string(protocolData), "frameId: t.Optional(t.String)") {
		t.Fatal("Protocol.js should make Page.navigate frameId optional")
	}
}
