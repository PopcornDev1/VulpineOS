// Package networklab is the public-build fallback for VulpineOS network
// identity integration. Private builds can replace this module with the full
// implementation.
package networklab

import "errors"

var errUnavailable = errors.New("networklab is unavailable in this build")

// Identity represents a browser network identity.
type Identity struct {
	Family string
}

// IdentityHashes contains JA3/JA4 fingerprints for an identity.
type IdentityHashes struct {
	JA3 string
	JA4 string
}

// ProxyStats reports validation proxy counters.
type ProxyStats struct {
	TotalConns     int
	ValidatedConns int
	MatchCount     int
	MismatchCount  int
}

// Proxy is a placeholder validation proxy for public builds.
type Proxy struct{}

// NewIdentity reports that private network identity generation is unavailable.
func NewIdentity(family string) (*Identity, error) {
	return nil, errUnavailable
}

// Hashes reports that private network identity hashing is unavailable.
func (i *Identity) Hashes() (*IdentityHashes, error) {
	return nil, errUnavailable
}

// WriteCurrentIdentity reports that private network identity sharing is unavailable.
func WriteCurrentIdentity(identity *Identity) error {
	return errUnavailable
}

// BuildIdentityParamsJSON returns an empty identity payload for public builds.
func BuildIdentityParamsJSON(identity *Identity) string {
	return "{}"
}

// NewValidationProxy creates a placeholder validation proxy.
func NewValidationProxy(addr string) *Proxy {
	return &Proxy{}
}

// AddRoute reports that the validation proxy is unavailable.
func (p *Proxy) AddRoute(pattern, identityName string) error {
	return errUnavailable
}

// StartBackground reports that the validation proxy is unavailable.
func (p *Proxy) StartBackground() error {
	return errUnavailable
}

// Stop is a no-op for the placeholder validation proxy.
func (p *Proxy) Stop() {}

// Addr returns an empty address for the placeholder validation proxy.
func (p *Proxy) Addr() string {
	return ""
}

// Stats returns zero counters for the placeholder validation proxy.
func (p *Proxy) Stats() ProxyStats {
	return ProxyStats{}
}
