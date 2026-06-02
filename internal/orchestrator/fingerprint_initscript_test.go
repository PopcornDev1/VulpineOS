package orchestrator

import (
	"strings"
	"testing"

	"vulpineos/internal/vault"
)

func TestBuildFingerprintInitScriptEmptyWhenNoPerContextFields(t *testing.T) {
	// Only stock-override fields present (UA/platform/screen) -> no init script.
	fp := vault.FingerprintData{
		UserAgent:    "Mozilla/5.0",
		Platform:     "MacIntel",
		ScreenWidth:  1920,
		ScreenHeight: 1080,
	}
	if got := buildFingerprintInitScript(fp); got != "" {
		t.Fatalf("expected empty script, got: %q", got)
	}
}

func TestBuildFingerprintInitScriptDrivesPerContextSetters(t *testing.T) {
	fp := vault.FingerprintData{
		WebGLVendor:         "Google Inc. (Apple)",
		WebGLRenderer:       "ANGLE (Apple, Apple M1)",
		AudioSeed:           123456,
		FontSpacingSeed:     7891011,
		Fonts:               []string{"Arial", "Helvetica Neue"},
		OsCPU:               "Intel Mac OS X 10.15",
		HardwareConcurrency: 8,
	}
	script := buildFingerprintInitScript(fp)
	if script == "" {
		t.Fatal("expected a non-empty init script")
	}

	wantContains := []string{
		`f("setWebGLVendor","Google Inc. (Apple)")`,
		`f("setWebGLRenderer","ANGLE (Apple, Apple M1)")`,
		`f("setAudioFingerprintSeed",123456)`,
		`f("setFontSpacingSeed",7891011)`,
		`f("setFontList","Arial,Helvetica Neue")`,
		`f("setNavigatorOscpu","Intel Mac OS X 10.15")`,
		`f("setNavigatorHardwareConcurrency",8)`,
	}
	for _, w := range wantContains {
		if !strings.Contains(script, w) {
			t.Errorf("init script missing %q\nscript:\n%s", w, script)
		}
	}
	// Must be a self-invoking guarded IIFE so unavailable/self-destructed
	// setters never throw into the page.
	if !strings.HasPrefix(script, "(function(){") || !strings.HasSuffix(script, "})();") {
		t.Errorf("script is not a wrapped IIFE: %s", script)
	}
	if !strings.Contains(script, "typeof w[n]==='function'") {
		t.Error("script does not guard setter presence")
	}
}

func TestBuildFingerprintInitScriptEscapesValues(t *testing.T) {
	// A renderer string containing quotes/backslashes must be safely encoded.
	fp := vault.FingerprintData{WebGLRenderer: `ANGLE ("quoted" \back)`}
	script := buildFingerprintInitScript(fp)
	// json.Marshal-based encoding must escape the embedded quotes/backslashes.
	if !strings.Contains(script, `\"quoted\"`) || !strings.Contains(script, `\\back`) {
		t.Fatalf("value not safely escaped: %s", script)
	}
}
