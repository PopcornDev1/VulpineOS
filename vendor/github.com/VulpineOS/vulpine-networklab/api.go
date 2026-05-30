// Package networklab is the public integration surface for VulpineOS.
// It provides TLS/network identity creation, management, probing, and
// an identity-aware SOCKS5 validation proxy.
package networklab

import (
	"context"
	"errors"
	"fmt"

	"github.com/VulpineOS/vulpine-networklab/internal/identity"
	"github.com/VulpineOS/vulpine-networklab/internal/probemux"
	"github.com/VulpineOS/vulpine-networklab/internal/profiles"
	"github.com/VulpineOS/vulpine-networklab/internal/proxymux"
)

// ErrUnknownFamily is returned when the named profile family does not exist.
var ErrUnknownFamily = errors.New("networklab: unknown profile family")

// ErrProxyStopped is returned when attempting to use a stopped proxy.
var ErrProxyStopped = errors.New("networklab: proxy stopped")

// Identity wraps the internal NetworkIdentity for public consumption.
type Identity struct {
	inner *identity.NetworkIdentity
}

// IdentityHashes contains computed JA3/JA4 hashes for an identity.
type IdentityHashes struct {
	JA3 string `json:"ja3"`
	JA4 string `json:"ja4"`
}

// ProbeResult contains observed TLS handshake parameters from a live probe.
type ProbeResult = probemux.ProbeResult

// ListFamilies returns the names of all available profile families.
func ListFamilies() []string {
	var names []string
	for name := range profiles.Registry {
		names = append(names, name)
	}
	return names
}

// NewIdentity creates a new network identity from a named profile family.
// family must be one of the values returned by ListFamilies().
// opts allows overriding specific parameters (see WithTLS, WithCiphers, etc.).
func NewIdentity(family string, opts ...Option) (*Identity, error) {
	base := profiles.Get(family)
	if base == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownFamily, family)
	}
	profile := base.Clone()
	for _, opt := range opts {
		opt(profile)
	}
	hashes, err := identity.HashIdentity(profile)
	if err != nil {
		return nil, fmt.Errorf("networklab: hash identity: %w", err)
	}
	profile.JA4 = hashes.JA4
	return &Identity{inner: profile}, nil
}

// ID returns the identity's assigned ID (set via WithID).
func (id *Identity) ID() string {
	return id.inner.ID
}

// Label returns the identity's human-readable label.
func (id *Identity) Label() string {
	return id.inner.Label
}

// Family returns the profile family this identity was based on.
func (id *Identity) Family() string {
	return id.inner.ProfileFamily
}

// Hashes computes and returns JA3/JA4 hashes for this identity.
func (id *Identity) Hashes() (*IdentityHashes, error) {
	h, err := identity.HashIdentity(id.inner)
	if err != nil {
		return nil, err
	}
	return &IdentityHashes{JA3: h.JA3, JA4: h.JA4}, nil
}

// HashIdentity computes JA3/JA4 from a raw identity without creating an Identity first.
// Useful for computing hashes during generation or comparison.
func HashIdentity(id *Identity) (*IdentityHashes, error) {
	return id.Hashes()
}

// ProbeCurrentIdentity connects to target and reports what TLS fingerprint
// the current process actually sends on the wire. Useful for verifying that
// NSS patches or proxy rewriting are having the intended effect.
func ProbeCurrentIdentity(target string, expected *Identity) (*ProbeResult, error) {
	var exp *identity.NetworkIdentity
	if expected != nil {
		exp = expected.inner
	}
	return probemux.ProbeTLS(target, exp)
}

// ProbeWithCapture does a full raw ClientHello capture through a local proxy,
// returning parsed parameters including the exact JA3 seen on the wire.
func ProbeWithCapture(target string, expected *Identity) (*ProbeResult, error) {
	var exp *identity.NetworkIdentity
	if expected != nil {
		exp = expected.inner
	}
	return probemux.ProbeTLSWithCapture(target, exp, nil)
}

// Proxy wraps the internal proxymux for identity-aware TLS validation.
type Proxy struct {
	inner *proxymux.Proxy
	stop  func()
}

// ProxyStats exposes proxy runtime metrics.
type ProxyStats struct {
	TotalConns     int64
	ValidatedConns int64
	MatchCount     int64
	MismatchCount  int64
}

// NewValidationProxy creates a SOCKS5 proxy that validates TLS ClientHello
// fingerprints against named identities for matching routes.
// addr is the listen address (e.g., ":1080").
// Routes can be added via proxy.AddRoute() before or after creation.
func NewValidationProxy(addr string) *Proxy {
	identityCheck := func(name string) bool {
		return profiles.Get(name) != nil
	}
	return &Proxy{
		inner: proxymux.New(addr, identityCheck),
	}
}

// AddRoute binds a host pattern to a network identity.
// pattern is a host suffix match (e.g., "example.com", "api.example.com", "*").
// identityName must be one of the values returned by ListFamilies().
func (p *Proxy) AddRoute(pattern, identityName string) error {
	if profiles.Get(identityName) == nil {
		return fmt.Errorf("%w: %s", ErrUnknownFamily, identityName)
	}
	p.inner.AddRoute(proxymux.Route{
		Pattern:      pattern,
		IdentityName: identityName,
	})
	return nil
}

// Start begins accepting SOCKS5 connections. Blocks until ctx is cancelled.
func (p *Proxy) Start(ctx context.Context) error {
	return p.inner.ListenAndServe(ctx)
}

// StartBackground launches the proxy in a goroutine. Call Stop to shut down.
func (p *Proxy) StartBackground() error {
	ctx, cancel := context.WithCancel(context.Background())
	p.stop = cancel
	go func() {
		_ = p.inner.ListenAndServe(ctx)
	}()
	return nil
}

// Stop shuts down the proxy if running in background mode.
func (p *Proxy) Stop() error {
	if p.stop != nil {
		p.stop()
		return nil
	}
	return ErrProxyStopped
}

// Addr returns the proxy's listening address, or nil if not started.
func (p *Proxy) Addr() string {
	a := p.inner.Addr()
	if a == nil {
		return ""
	}
	return a.String()
}

// Stats returns proxy runtime metrics.
func (p *Proxy) Stats() ProxyStats {
	s := p.inner.Stats()
	return ProxyStats{
		TotalConns:     s.TotalConns,
		ValidatedConns: s.ValidatedConns,
		MatchCount:     s.MatchCount,
		MismatchCount:  s.MismatchCount,
	}
}

// WriteIdentities serializes a set of identities and writes them into
// shared memory (~/.vulpineos/network-identities.dat) where the Camoufox
// NetworkIdentityManager (PSM layer) reads them per TLS socket creation.
//
// Format: {"cipher_suites":[0x1301,0x1302,...],"supported_groups":[...],...}
// for a single identity (named ""), or {"ctx-1":{...},"ctx-2":{...}} for
// multiple contexts. The C++ side uses simple strstr matching, not a real
// JSON parser — keep whitespace minimal and hex values 0x-prefixed.
func WriteIdentities(identities map[string]*Identity) error {
	if len(identities) == 0 {
		return nil
	}
	if err := ensureShmemDir(); err != nil {
		return fmt.Errorf("networklab: create shmem dir: %w", err)
	}

	var jsonStr string
	if len(identities) == 1 {
		for _, id := range identities {
			jsonStr = BuildIdentityParamsJSON(id)
		}
	} else {
		jsonStr = BuildMultiIdentityJSON(identities)
	}

	if err := writeShmem([]byte(jsonStr)); err != nil {
		return fmt.Errorf("networklab: write shmem: %w", err)
	}
	return nil
}

// WriteCurrentIdentity is a convenience wrapper for single-identity setups.
// Writes the identity to shmem under the key "current".
func WriteCurrentIdentity(id *Identity) error {
	return WriteIdentities(map[string]*Identity{"current": id})
}

// Option is a function that modifies an Identity during construction.
type Option func(*identity.NetworkIdentity)

// WithID sets the identity's unique ID.
func WithID(id string) Option {
	return func(n *identity.NetworkIdentity) { n.ID = id }
}

// WithLabel sets the identity's human-readable label.
func WithLabel(label string) Option {
	return func(n *identity.NetworkIdentity) { n.Label = label }
}

// WithTLS overrides the TLS version range.
func WithTLS(min, max uint16) Option {
	return func(n *identity.NetworkIdentity) {
		n.TLSVersionMin = min
		n.TLSVersionMax = max
	}
}

// WithCiphers overrides the cipher suite list.
func WithCiphers(ciphers ...uint16) Option {
	return func(n *identity.NetworkIdentity) { n.CipherSuites = ciphers }
}

// WithALPN overrides the ALPN protocol list.
func WithALPN(protos ...string) Option {
	return func(n *identity.NetworkIdentity) { n.ALPNProtos = protos }
}

// WithProxy configures this identity to route through the named proxy.
func WithProxy(label string) Option {
	return func(n *identity.NetworkIdentity) {
		n.ProxyMode = "proxy-route"
		n.ProxyLabel = label
	}
}

// WithECH enables Encrypted ClientHello for this identity.
func WithECH() Option {
	return func(n *identity.NetworkIdentity) { n.ECHPolicy = "grease" }
}

// WithGREASE enables GREASE extensions for this identity.
func WithGREASE() Option {
	return func(n *identity.NetworkIdentity) { n.GreasePolicy = "tls" }
}
