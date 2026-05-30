package identity

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

type NetworkIdentity struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`

	ProfileFamily string `json:"profile_family"`

	TLSVersionMin  uint16         `json:"tls_version_min"`
	TLSVersionMax  uint16         `json:"tls_version_max"`
	CipherSuites   []uint16       `json:"cipher_suites"`
	Extensions     []TLSExtension `json:"extensions"`
	SigAlgs        []uint16       `json:"sig_algs"`
	SupportedGroups []uint16      `json:"supported_groups"`
	KeyShareCurves  []uint16      `json:"key_share_curves"`

	ALPNProtos []string `json:"alpn_protos"`

	H2Settings H2SettingsFrame `json:"h2_settings"`

	QUICParams QUICTransportParams `json:"quic_params"`

	ECHPolicy    string `json:"ech_policy"`
	GreasePolicy string `json:"grease_policy"`

	ProxyMode  string `json:"proxy_mode"`
	ProxyLabel string `json:"proxy_label,omitempty"`

	JA4  string `json:"ja4,omitempty"`
}

type TLSExtension struct {
	Type    uint16 `json:"type"`
	Data    []byte `json:"data,omitempty"`
	Present bool   `json:"present"`
}

type H2SettingsFrame struct {
	HeaderTableSize      uint32 `json:"header_table_size"`
	EnablePush           uint32 `json:"enable_push"`
	MaxConcurrentStreams uint32 `json:"max_concurrent_streams"`
	InitialWindowSize    uint32 `json:"initial_window_size"`
	MaxFrameSize         uint32 `json:"max_frame_size"`
	MaxHeaderListSize    uint32 `json:"max_header_list_size"`
}

type QUICTransportParams struct {
	InitialMaxData             uint64 `json:"initial_max_data"`
	InitialMaxStreamDataBidi   uint64 `json:"initial_max_stream_data_bidi"`
	InitialMaxStreamDataUni    uint64 `json:"initial_max_stream_data_uni"`
	InitialMaxStreamsBidi      uint64 `json:"initial_max_streams_bidi"`
	InitialMaxStreamsUni       uint64 `json:"initial_max_streams_un"`
	AckDelayExponent           uint64 `json:"ack_delay_exponent"`
	MaxAckDelay                uint64 `json:"max_ack_delay"`
	DisableActiveMigration     bool   `json:"disable_active_migration"`
	ActiveConnectionIDLimit    uint64 `json:"active_connection_id_limit"`
	InitialSourceConnectionID  []byte `json:"initial_source_connection_id,omitempty"`
}

type IdentityHashes struct {
	JA3 string `json:"ja3"`
	JA4 string `json:"ja4"`

	CipherHash  string `json:"cipher_hash"`
	ExtHash     string `json:"ext_hash"`
	GroupHash   string `json:"group_hash,omitempty"`
	SigAlgHash  string `json:"sig_alg_hash,omitempty"`
	ALPNHash    string `json:"alpn_hash,omitempty"`
}

func NewNetworkIdentity(family string, opts ...IdentityOption) *NetworkIdentity {
	id := &NetworkIdentity{
		ProfileFamily: family,
		TLSVersionMin: 0x0303,
		TLSVersionMax: 0x0304,
		ECHPolicy:     "off",
		GreasePolicy:  "off",
		ProxyMode:     "direct",
	}
	for _, opt := range opts {
		opt(id)
	}
	return id
}

type IdentityOption func(*NetworkIdentity)

func WithTLSVersions(min, max uint16) IdentityOption {
	return func(n *NetworkIdentity) { n.TLSVersionMin = min; n.TLSVersionMax = max }
}

func WithCipherSuites(ciphers ...uint16) IdentityOption {
	return func(n *NetworkIdentity) { n.CipherSuites = ciphers }
}

func WithALPN(protos ...string) IdentityOption {
	return func(n *NetworkIdentity) { n.ALPNProtos = protos }
}

func WithProxy(label string) IdentityOption {
	return func(n *NetworkIdentity) { n.ProxyMode = "proxy-route"; n.ProxyLabel = label }
}

func HashIdentity(id *NetworkIdentity) (*IdentityHashes, error) {
	ciphers := joinUint16(id.CipherSuites)
	exts := joinExtensions(id.Extensions)

	var groups string
	if len(id.SupportedGroups) > 0 {
		groups = joinUint16(id.SupportedGroups)
	}

	var sigAlgs string
	if len(id.SigAlgs) > 0 {
		sigAlgs = joinUint16(id.SigAlgs)
	}

	ver := fmt.Sprintf("%d", id.TLSVersionMax)
	ja3Input := ver + "," + ciphers + "," + exts + "," + groups + ",0-1-2"
	ja3 := md5.Sum([]byte(ja3Input))

	ja4 := computeJA4(id)

	return &IdentityHashes{
		JA3:        hex.EncodeToString(ja3[:]),
		JA4:        ja4,
		CipherHash: truncHash([]byte(ciphers)),
		ExtHash:    truncHash([]byte(exts)),
		GroupHash:  truncHash([]byte(groups)),
		SigAlgHash: truncHash([]byte(sigAlgs)),
	}, nil
}

func computeJA4(id *NetworkIdentity) string {
	proto := ja4Protocol(id.TLSVersionMax)

	cipherCountHex := fmt.Sprintf("%02x", len(id.CipherSuites)&0xff)
	if len(cipherCountHex) > 2 {
		cipherCountHex = cipherCountHex[:2]
	}

	class := ja4CipherClass(id)

	cipherHash := truncHash([]byte(joinUint16(id.CipherSuites)))
	extHash := truncHash([]byte(joinExtensions(id.Extensions)))

	prefix := string(proto) + cipherCountHex + string(class)
	return prefix + "_" + cipherHash + "_" + extHash
}

func ja4Protocol(version uint16) string {
	switch version {
	case 0x0304:
		return "t13"
	case 0x0303:
		return "t12"
	case 0x0302:
		return "t11"
	case 0x0301:
		return "t10"
	default:
		return "t??"
	}
}

func ja4CipherClass(id *NetworkIdentity) string {
	if len(id.CipherSuites) == 0 {
		return "e"
	}
	first := id.CipherSuites[0]
	if first >= 0x1301 && first <= 0x1305 {
		return "d"
	}
	switch first {
	case 0x009C, 0x009D, 0x002F, 0x0035, 0x000A, 0x00FF:
		return "a"
	case 0xC014, 0xC013, 0xC009, 0xC008:
		return "b"
	case 0xCCA8, 0xCCA9:
		return "c"
	case 0xC030, 0xC02F, 0xC028, 0xC027, 0xC024, 0xC023,
		0xC00A, 0xC072, 0xC073, 0xC02B, 0xC02C:
		return "g"
	case 0x0088, 0x0087, 0x008C, 0x008B, 0x0045, 0x0044,
		0x006B, 0x0067, 0x009E, 0x009F:
		return "h"
	case 0xC0AC, 0xC0AD, 0xC0AE, 0xC0AF, 0xC0B0, 0xC0B1,
		0xC0B2, 0xC0B3:
		return "i"
	case 0x003C, 0x003D, 0x003E, 0x003F:
		return "j"
	case 0x00C0, 0x00C1, 0x00C2, 0x00C3, 0x00C4, 0x00C5,
		0x00C6, 0x00C7:
		return "k"
	case 0xCC14, 0xCC15, 0xCC16, 0xCC17, 0xCC18, 0xCC19,
		0xCC1A, 0xCC1B, 0xCC1C, 0xCC1D, 0xCC1E, 0xCC1F,
		0xCC20, 0xCC21:
		return "l"
	default:
		return "e"
	}
}

func truncHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])[:12]
}

func joinUint16(vals []uint16) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = fmt.Sprintf("%d", v)
	}
	return strings.Join(parts, "-")
}

func joinExtensions(exts []TLSExtension) string {
	types := make([]uint16, len(exts))
	for i, e := range exts {
		types[i] = e.Type
	}
	return joinUint16(types)
}

func (n *NetworkIdentity) Clone() *NetworkIdentity {
	c := *n
	c.CipherSuites = append([]uint16{}, n.CipherSuites...)
	c.Extensions = append([]TLSExtension{}, n.Extensions...)
	c.SigAlgs = append([]uint16{}, n.SigAlgs...)
	c.SupportedGroups = append([]uint16{}, n.SupportedGroups...)
	c.KeyShareCurves = append([]uint16{}, n.KeyShareCurves...)
	c.ALPNProtos = append([]string{}, n.ALPNProtos...)
	return &c
}
