package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestPrintExtensionStatus_FormatsToBuffer verifies the extension
// status printer writes to an arbitrary io.Writer rather than requiring
// *os.File, and that the default public build reports all providers as
// unavailable.
func TestPrintExtensionStatus_FormatsToBuffer(t *testing.T) {
	var buf bytes.Buffer
	PrintExtensionsStatus(&buf)
	out := buf.String()
	for _, row := range []string{"Credentials", "Audio", "Mobile", "Sentinel", "Captcha", "unavailable"} {
		if !strings.Contains(out, row) {
			t.Errorf("expected %q in output, got:\n%s", row, out)
		}
	}
}
