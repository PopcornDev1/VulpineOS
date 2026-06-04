package juggler

import (
	"os"
	"strings"
	"testing"
)

func TestTargetRegistryNewPageReusesExistingContextWindow(t *testing.T) {
	data, err := os.ReadFile("../../additions/juggler/TargetRegistry.js")
	if err != nil {
		t.Fatalf("read TargetRegistry.js: %v", err)
	}
	source := string(data)

	required := []string{
		"async _openPageInBrowserContext(browserContext)",
		"const existingPage = browserContext.pages.values().next().value;",
		"return this._openTabInWindow(existingPage._window, browserContext);",
		"const tab = window.gBrowser.addTab('about:blank', options);",
		"async _openWindowForBrowserContext(browserContext)",
	}
	for _, needle := range required {
		if !strings.Contains(source, needle) {
			t.Fatalf("TargetRegistry.js missing %q", needle)
		}
	}

	newPageStart := strings.Index(source, "async _newPageInternal({browserContextId})")
	if newPageStart == -1 {
		t.Fatal("could not locate _newPageInternal body")
	}
	newPageEnd := strings.Index(source[newPageStart:], "\n  async _openPageInBrowserContext")
	if newPageEnd == -1 {
		t.Fatal("could not locate _newPageInternal body")
	}
	newPageBody := source[newPageStart : newPageStart+newPageEnd]
	if strings.Contains(newPageBody, "Services.ww.openWindow") {
		t.Fatal("_newPageInternal should not open a new browser window directly")
	}
}
