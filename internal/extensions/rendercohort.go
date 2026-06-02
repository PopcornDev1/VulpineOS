package extensions

import "context"

// RenderCohort is one available real-device render profile that a private
// provider can deliver for per-context (per-agent) render isolation.
//
// The contract is deliberately opaque: the public seam never describes how a
// cohort's render config is produced or what it contains. ConfigBlob is an
// already-encoded payload (gzip+base64 of the cohort's render config JSON) that
// the orchestrator delivers verbatim through the per-context storage channel —
// the orchestrator neither parses nor inspects it.
type RenderCohort struct {
	// Key is a stable, pref-safe identifier for the cohort. It is the value
	// stored per browser context to select this cohort, and is the join key the
	// content process uses to find the delivered ConfigBlob.
	Key string
	// ProfileID is the cohort's render profile identity (opaque to the caller).
	ProfileID string
	// OS is the camoufox OS identifier the cohort presents ("mac"/"win"/"lin"),
	// so the orchestrator only assigns a cohort to an agent whose claimed OS
	// matches. A cohort must never be presented to a different-OS agent.
	OS string
	// ConfigBlob is the opaque, transport-ready per-context render config.
	ConfigBlob string
}

// RenderCohortProvider is the public seam for an optional per-context render
// isolation backend. The public build ships a nil-safe no-op, so per-context
// render isolation simply never engages without a private provider and agents
// fall back to the synthetic per-context fingerprint surfaces. Alternate builds
// register a provider backed by verified real-device cohorts from a build-tagged
// init file.
//
//   - Cohorts returns every cohort currently ready to deliver. It MUST return
//     ErrUnavailable (and a nil slice) when no verified cohort is available, so
//     the orchestrator treats per-context render isolation as off rather than
//     guessing. A provider must never return a fabricated or unverified cohort.
//   - Assignment of agents to cohorts is the orchestrator's responsibility; the
//     provider only enumerates what is available and supplies the opaque blob.
type RenderCohortProvider interface {
	Cohorts(ctx context.Context) ([]RenderCohort, error)
	Available() bool
}

var defaultRenderCohortProvider RenderCohortProvider = noopRenderCohortProvider{}

type noopRenderCohortProvider struct{}

func (noopRenderCohortProvider) Cohorts(ctx context.Context) ([]RenderCohort, error) {
	return nil, ErrUnavailable
}

func (noopRenderCohortProvider) Available() bool { return false }
