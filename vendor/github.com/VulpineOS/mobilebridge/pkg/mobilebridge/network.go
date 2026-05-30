package mobilebridge

import (
	"errors"
	"fmt"
)

// NetworkConditions mirrors the params of the CDP Network.emulateNetworkConditions
// command. All rate fields are in the units Chrome expects: bytes per second
// for throughput, milliseconds for latency.
type NetworkConditions struct {
	Offline            bool    `json:"offline"`
	Latency            float64 `json:"latency"`
	DownloadThroughput float64 `json:"downloadThroughput"`
	UploadThroughput   float64 `json:"uploadThroughput"`
	ConnectionType     string  `json:"connectionType,omitempty"`
}

// buildNetworkConditions converts human-friendly mobile units (kbps) into the
// byte-per-second values CDP expects. A non-positive latency becomes 0 (i.e.
// no added latency), and a non-positive throughput becomes 0 which Chrome
// treats as "unthrottled".
func buildNetworkConditions(offline bool, latencyMs int, downloadKbps, uploadKbps int) NetworkConditions {
	lat := float64(latencyMs)
	if lat < 0 {
		lat = 0
	}
	down := float64(downloadKbps) * 1000.0 / 8.0
	if downloadKbps <= 0 {
		down = 0
	}
	up := float64(uploadKbps) * 1000.0 / 8.0
	if uploadKbps <= 0 {
		up = 0
	}
	return NetworkConditions{
		Offline:            offline,
		Latency:            lat,
		DownloadThroughput: down,
		UploadThroughput:   up,
	}
}

// EmulateNetworkConditions forwards a Network.emulateNetworkConditions call
// to the upstream Chrome over the proxy's existing WebSocket. It is a
// passthrough convenience for testing mobile scenarios (offline, 3G, LTE)
// without hand-building the CDP payload.
//
// downloadKbps / uploadKbps are in kilobits-per-second (the unit every
// "throttle me like a 3G phone" UI uses). They are converted to Chrome's
// expected bytes-per-second internally.
func (p *Proxy) EmulateNetworkConditions(offline bool, latencyMs int, downloadKbps, uploadKbps int) error {
	if p == nil {
		return errors.New("mobilebridge: nil proxy")
	}
	return emulateNetworkConditionsOn(p, offline, latencyMs, downloadKbps, uploadKbps)
}

// EmulateNetworkConditions is a thin wrapper around (*Proxy).EmulateNetworkConditions
// kept for backward compatibility with iteration 4 callers.
//
// Deprecated: use p.EmulateNetworkConditions directly; the free function will
// be removed in a future release.
func EmulateNetworkConditions(p *Proxy, offline bool, latencyMs int, downloadKbps, uploadKbps int) error {
	return p.EmulateNetworkConditions(offline, latencyMs, downloadKbps, uploadKbps)
}

// emulateNetworkConditionsOn is the messageSender-typed implementation used
// by EmulateNetworkConditions and by tests that substitute a capture sender.
func emulateNetworkConditionsOn(s messageSender, offline bool, latencyMs int, downloadKbps, uploadKbps int) error {
	// Network domain must be enabled first; Chrome returns a protocol
	// error if it isn't. Send Network.enable first — it's idempotent.
	if err := s.sendUpstream("Network.enable", map[string]interface{}{}); err != nil {
		return fmt.Errorf("enable network domain: %w", err)
	}
	return s.sendUpstream("Network.emulateNetworkConditions",
		buildNetworkConditions(offline, latencyMs, downloadKbps, uploadKbps))
}
