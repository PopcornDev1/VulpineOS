package vault

import (
	"context"
	"encoding/json"
	"testing"

	"vulpineos/internal/extensions"
)

// tierFakeProvider is a test FingerprintProvider for exercising the tiering in
// GenerateFingerprint without depending on a private build.
type tierFakeProvider struct {
	available bool
	out       string
	err       error
}

func (f tierFakeProvider) Available() bool { return f.available }
func (f tierFakeProvider) Generate(ctx context.Context, seed, hostOS string) (string, error) {
	return f.out, f.err
}

// TestGenerateFingerprintAlwaysReturnsValidConfig verifies the chain always
// yields a usable config even when no private provider and no BrowserForge
// pipeline are present (the offline fallback tier).
func TestGenerateFingerprintAlwaysReturnsValidConfig(t *testing.T) {
	t.Cleanup(func() { extensions.Registry.SetFingerprint(nil) })
	extensions.Registry.SetFingerprint(nil) // ensure no provider

	out, err := GenerateFingerprint(NewID())
	if err != nil {
		t.Fatalf("GenerateFingerprint err = %v", err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if _, ok := cfg["navigator.userAgent"]; !ok {
		t.Fatalf("config missing navigator.userAgent: %s", out)
	}
}

// TestGenerateFingerprintUsesAvailableProvider verifies the private provider
// tier wins when it can produce a value.
func TestGenerateFingerprintUsesAvailableProvider(t *testing.T) {
	t.Cleanup(func() { extensions.Registry.SetFingerprint(nil) })

	sentinel := `{"navigator.userAgent":"PROVIDER-WON","screen.width":1234}`
	extensions.Registry.SetFingerprint(tierFakeProvider{available: true, out: sentinel})

	out, err := GenerateFingerprint(NewID())
	if err != nil {
		t.Fatalf("GenerateFingerprint err = %v", err)
	}
	if out != sentinel {
		t.Fatalf("expected provider output, got: %s", out)
	}
}

// TestGenerateFingerprintNeverGuessesUnavailableProvider verifies that a
// provider returning ErrUnavailable is skipped (never substituted) and the
// chain falls through to a real generated config.
func TestGenerateFingerprintNeverGuessesUnavailableProvider(t *testing.T) {
	t.Cleanup(func() { extensions.Registry.SetFingerprint(nil) })

	// Available() true but Generate signals "cohort not ready".
	extensions.Registry.SetFingerprint(tierFakeProvider{available: true, out: "", err: extensions.ErrUnavailable})

	out, err := GenerateFingerprint(NewID())
	if err != nil {
		t.Fatalf("GenerateFingerprint err = %v", err)
	}
	if out == "" {
		t.Fatal("expected a fallen-through config, got empty")
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatalf("fallthrough output is not valid JSON: %v", err)
	}
	if ua, _ := cfg["navigator.userAgent"].(string); ua == "PROVIDER-WON" {
		t.Fatal("must not have used the unavailable provider's value")
	}
}
