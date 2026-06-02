package extensions

import "context"

// FingerprintProvider is the public seam for an optional, higher-fidelity
// per-context fingerprint generator. The public build of VulpineOS ships a
// nil-safe no-op implementation, so fingerprint generation always falls back
// to the in-tree public generator (BrowserForge when available, otherwise the
// deterministic offline profile). Alternate builds may register a richer
// provider (for example one backed by verified real-device runtime bundles)
// from a build-tagged init file.
//
// The contract is deliberately narrow and detail-free so the public seam never
// exposes how a private provider produces its values:
//
//   - Generate returns a fingerprint config encoded as a JSON object whose keys
//     are the camoufox dotted-key namespace (navigator.userAgent,
//     navigator.oscpu, screen.width, audio:seed, fonts, ...) — the same shape
//     the public generator emits, so callers apply it identically.
//   - Generate MUST return ErrUnavailable (and an empty string) whenever it
//     cannot produce a real value for the requested identity. Callers treat
//     that as "fall back to the public generator" and never guess a provider
//     value. A provider must not emit a fabricated or approximate config in
//     place of a real one.
//   - seed is a stable per-identity key (the agent/citizen UUID). hostOS is the
//     camoufox OS identifier ("mac"/"win"/"lin").
type FingerprintProvider interface {
	Generate(ctx context.Context, seed, hostOS string) (string, error)
	Available() bool
}

var defaultFingerprintProvider FingerprintProvider = noopFingerprintProvider{}

type noopFingerprintProvider struct{}

func (noopFingerprintProvider) Generate(ctx context.Context, seed, hostOS string) (string, error) {
	return "", ErrUnavailable
}

func (noopFingerprintProvider) Available() bool { return false }
