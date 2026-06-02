package extensions

import (
	"context"
	"errors"
	"testing"
)

func TestDefaultFingerprintProviderIsUnavailableNoop(t *testing.T) {
	p := Registry.Fingerprint()
	if p == nil {
		t.Fatal("Registry.Fingerprint() returned nil; must always be non-nil")
	}
	if p.Available() {
		t.Fatal("default fingerprint provider must report Available()==false")
	}
	out, err := p.Generate(context.Background(), "seed", "mac")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("default Generate err = %v, want ErrUnavailable", err)
	}
	if out != "" {
		t.Fatalf("default Generate out = %q, want empty", out)
	}
}

type fakeFingerprintProvider struct {
	available bool
	out       string
	err       error
	gotSeed   string
	gotHostOS string
}

func (f *fakeFingerprintProvider) Available() bool { return f.available }
func (f *fakeFingerprintProvider) Generate(ctx context.Context, seed, hostOS string) (string, error) {
	f.gotSeed = seed
	f.gotHostOS = hostOS
	return f.out, f.err
}

func TestSetFingerprintRegistersAndReverts(t *testing.T) {
	t.Cleanup(func() { Registry.SetFingerprint(nil) }) // restore no-op default

	fake := &fakeFingerprintProvider{available: true, out: `{"navigator.userAgent":"X"}`}
	Registry.SetFingerprint(fake)

	got := Registry.Fingerprint()
	if got != fake {
		t.Fatalf("Registry.Fingerprint() did not return the registered provider")
	}
	out, err := got.Generate(context.Background(), "s1", "win")
	if err != nil || out != fake.out {
		t.Fatalf("Generate = (%q, %v), want (%q, nil)", out, err, fake.out)
	}
	if fake.gotSeed != "s1" || fake.gotHostOS != "win" {
		t.Fatalf("provider received (seed=%q, hostOS=%q), want (s1, win)", fake.gotSeed, fake.gotHostOS)
	}

	// Passing nil restores the no-op default rather than installing nil.
	Registry.SetFingerprint(nil)
	if Registry.Fingerprint().Available() {
		t.Fatal("SetFingerprint(nil) must restore the unavailable no-op default")
	}
}
