// Package extensions defines pluggable provider interfaces that optional
// VulpineOS features depend on. The public build of VulpineOS ships
// nil-safe, no-op default implementations for every provider, so the
// tree always compiles and runs without any extra dependencies.
//
// Alternate builds may replace the defaults at init() time by calling
// the setter methods on Registry from a build-tagged file. Callers
// should always check Available() on a provider before invoking it,
// and must handle the "feature unavailable" error returned by the
// default stubs.
package extensions

import (
	"context"
	"encoding/json"
	"sync"
)

// JugglerCallable is the minimal slice of the Juggler client surface
// that extension adapters depend on. Keeping the interface narrow here
// avoids a dependency on the concrete *juggler.Client type from this
// package, which in turn keeps alternate-build adapter code from having
// to import vulpineos/internal/juggler directly.
//
// The live *juggler.Client type satisfies this interface by structural
// match, so callers pass it in as-is via InitWithClient.
type JugglerCallable interface {
	Call(sessionID, method string, params interface{}) (json.RawMessage, error)
	CallWithContext(ctx context.Context, sessionID, method string, params interface{}) (json.RawMessage, error)
}

// registry holds the active provider implementations. Reads and writes
// are guarded by an embedded RWMutex so that setters running from
// build-tagged init files do not race with MCP tool handlers reading
// providers on worker goroutines.
//
// Access the providers via the Credentials/Audio/Mobile accessor
// methods rather than touching fields directly; this keeps the
// read-path lock-safe under -race.
type registry struct {
	mu          sync.RWMutex
	credentials CredentialProvider
	audio       AudioCapturer
	mobile      MobileBridge
	sentinel     SentinelProvider
	fingerprint  FingerprintProvider
	renderCohort RenderCohortProvider
}

// Credentials returns the currently-registered credential provider.
// Always non-nil; returns the no-op default when nothing is registered.
func (r *registry) Credentials() CredentialProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.credentials
}

// Audio returns the currently-registered audio capturer. Always
// non-nil; returns the no-op default when nothing is registered.
func (r *registry) Audio() AudioCapturer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.audio
}

// Mobile returns the currently-registered mobile bridge. Always
// non-nil; returns the no-op default when nothing is registered.
func (r *registry) Mobile() MobileBridge {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.mobile
}

// SetCredentials registers a credential provider. Intended to be called
// from init() in build-tagged extension files. Safe to call from tests
// that swap in a fake provider.
func (r *registry) SetCredentials(p CredentialProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p == nil {
		p = defaultCredentialProvider
	}
	r.credentials = p
}

// SetAudio registers an audio capturer. Intended to be called from
// init() in build-tagged extension files.
func (r *registry) SetAudio(c AudioCapturer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c == nil {
		c = defaultAudioCapturer
	}
	r.audio = c
}

// SetMobile registers a mobile bridge. Intended to be called from
// init() in build-tagged extension files.
func (r *registry) SetMobile(b MobileBridge) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b == nil {
		b = defaultMobileBridge
	}
	r.mobile = b
}

// Sentinel returns the currently-registered sentinel provider.
// Always non-nil; returns the no-op default when nothing is registered.
func (r *registry) Sentinel() SentinelProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sentinel
}

// SetSentinel registers a sentinel provider.
func (r *registry) SetSentinel(p SentinelProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p == nil {
		p = defaultSentinelProvider
	}
	r.sentinel = p
}

// Fingerprint returns the currently-registered fingerprint provider.
// Always non-nil; returns the no-op default when nothing is registered.
func (r *registry) Fingerprint() FingerprintProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.fingerprint
}

// SetFingerprint registers a fingerprint provider. Intended to be called
// from init() in build-tagged extension files.
func (r *registry) SetFingerprint(p FingerprintProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p == nil {
		p = defaultFingerprintProvider
	}
	r.fingerprint = p
}

// RenderCohort returns the currently-registered render cohort provider.
// Always non-nil; returns the no-op default when nothing is registered.
func (r *registry) RenderCohort() RenderCohortProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.renderCohort
}

// SetRenderCohort registers a render cohort provider. Intended to be called
// from init() in build-tagged extension files.
func (r *registry) SetRenderCohort(p RenderCohortProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p == nil {
		p = defaultRenderCohortProvider
	}
	r.renderCohort = p
}

// Registry is the global provider registry. It is a pointer so that
// alternate builds can mutate it from their own init() functions.
var Registry = &registry{
	credentials: defaultCredentialProvider,
	audio:       defaultAudioCapturer,
	mobile:      defaultMobileBridge,
	sentinel:     defaultSentinelProvider,
	fingerprint:  defaultFingerprintProvider,
	renderCohort: defaultRenderCohortProvider,
}

// privateProviders holds constructors supplied by local build-tagged
// extension files. The constructors are invoked by InitWithClient once
// the runtime has a live juggler client to hand out. Public builds leave
// the struct zero-valued so every entry is nil and InitWithClient becomes
// a no-op.
var privateProviders = struct {
	Vault       func(jc JugglerCallable) CredentialProvider
	Audio       func(jc JugglerCallable) AudioCapturer
	Mobile      func(jc JugglerCallable) MobileBridge
	Sentinel     func(jc JugglerCallable) SentinelProvider
	Fingerprint  func() FingerprintProvider
	RenderCohort func() RenderCohortProvider
}{}

// Init is called once at startup before a juggler client is available.
// It exists as a stable anchor for the CLI entrypoint. For adapters
// that need the juggler client to be wired, use InitWithClient once
// the kernel has produced one.
func Init() {
	InitWithClient(nil)
}

// InitWithClient wires any registered extension provider constructors to
// the live juggler client and installs the resulting adapters in the
// global Registry. The public build leaves privateProviders zero, so
// each nil-check short-circuits and the Registry keeps its no-op stubs.
// Passing a nil jc is allowed and skips registration entirely — that
// is the default-build path.
func InitWithClient(jc JugglerCallable) {
	// The fingerprint provider generates identity config offline and needs no
	// juggler client, so wire it regardless of jc (including the Init()/nil path).
	if privateProviders.Fingerprint != nil {
		if p := privateProviders.Fingerprint(); p != nil {
			Registry.SetFingerprint(p)
		}
	}
	// Render cohorts are produced offline from verified runtime bundles and need
	// no juggler client, so wire the provider on the Init()/nil path too.
	if privateProviders.RenderCohort != nil {
		if p := privateProviders.RenderCohort(); p != nil {
			Registry.SetRenderCohort(p)
		}
	}
	if jc == nil {
		return
	}
	if privateProviders.Vault != nil {
		if p := privateProviders.Vault(jc); p != nil {
			Registry.SetCredentials(p)
		}
	}
	if privateProviders.Audio != nil {
		if a := privateProviders.Audio(jc); a != nil {
			Registry.SetAudio(a)
		}
	}
	if privateProviders.Mobile != nil {
		if m := privateProviders.Mobile(jc); m != nil {
			Registry.SetMobile(m)
		}
	}
	if privateProviders.Sentinel != nil {
		if p := privateProviders.Sentinel(jc); p != nil {
			Registry.SetSentinel(p)
		}
	}
}
