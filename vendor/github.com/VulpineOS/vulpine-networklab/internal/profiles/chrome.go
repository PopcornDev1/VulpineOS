package profiles

import "github.com/VulpineOS/vulpine-networklab/internal/identity"

func newChromeExtensions() []identity.TLSExtension {
	return []identity.TLSExtension{
		{Type: 0x0000, Present: true}, // server_name
		{Type: 0x000A, Present: true}, // supported_groups
		{Type: 0x000B, Present: true}, // ec_point_formats
		{Type: 0x000D, Present: true}, // signature_algorithms
		{Type: 0x0010, Present: true}, // ALPN
		{Type: 0x0017, Present: true}, // extended_master_secret
		{Type: 0x001B, Present: true}, // compress_certificate
		{Type: 0x0023, Present: true}, // session_ticket
		{Type: 0x002B, Present: true}, // supported_versions
		{Type: 0x002D, Present: true}, // PSK key exchange modes
		{Type: 0x0032, Present: true}, // signature_algorithms_cert
		{Type: 0x0033, Present: true}, // key_share
		{Type: 0xFF01, Present: true}, // renegotiation_info
	}
}

var chromeCipherSuites = []uint16{
	0x1301, // TLS_AES_128_GCM_SHA256
	0x1302, // TLS_AES_256_GCM_SHA384
	0x1303, // TLS_CHACHA20_POLY1305_SHA256
	0xC02B, // TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
	0xC02F, // TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
	0xCCA9, // TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256
	0xCCA8, // TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256
}

var chromeH2Settings = identity.H2SettingsFrame{
	HeaderTableSize:      65536,
	EnablePush:           0,
	MaxConcurrentStreams: 1000,
	InitialWindowSize:    6291456,
	MaxFrameSize:         16384,
	MaxHeaderListSize:    262144,
}

var chromeQUICParams = identity.QUICTransportParams{
	InitialMaxData:             16777216,
	InitialMaxStreamDataBidi:   2097152,
	InitialMaxStreamDataUni:    2097152,
	InitialMaxStreamsBidi:      100,
	InitialMaxStreamsUni:       100,
	AckDelayExponent:           3,
	MaxAckDelay:                25,
	DisableActiveMigration:     false,
	ActiveConnectionIDLimit:    8,
	InitialSourceConnectionID:  nil,
}

var chromeCommonKeyShare = []uint16{0x001D} // X25519 only

var chromeCommonALPN = []string{"h2", "http/1.1"}

func Chrome132MacOS() *identity.NetworkIdentity {
	n := identity.NewNetworkIdentity("chrome132_macos")
	n.ID = "chrome_132_macos_001"
	n.Label = "Chrome 132 macOS 14"
	n.TLSVersionMin = 0x0303
	n.TLSVersionMax = 0x0304
	n.CipherSuites = chromeCipherSuites
	n.Extensions = newChromeExtensions()
	n.SigAlgs = []uint16{
		0x0403, // ecdsa_secp256r1_sha256
		0x0804, // rsa_pss_rsae_sha256
		0x0401, // rsa_pkcs1_sha256
		0x0503, // ecdsa_secp384r1_sha384
		0x0805, // rsa_pss_rsae_sha384
		0x0501, // rsa_pkcs1_sha384
		0x0806, // rsa_pss_rsae_sha512
		0x0601, // rsa_pkcs1_sha512
	}
	n.SupportedGroups = []uint16{0x001D, 0x0017, 0x0018, 0x0019}
	n.KeyShareCurves = chromeCommonKeyShare
	n.ALPNProtos = chromeCommonALPN
	n.H2Settings = chromeH2Settings
	n.QUICParams = chromeQUICParams
	n.ECHPolicy = "off"
	n.GreasePolicy = "off"
	n.ProxyMode = "direct"
	n.ProxyLabel = ""
	return n
}

func Chrome132Windows() *identity.NetworkIdentity {
	n := identity.NewNetworkIdentity("chrome132_windows")
	n.ID = "chrome_132_windows_001"
	n.Label = "Chrome 132 Windows 11"
	n.TLSVersionMin = 0x0303
	n.TLSVersionMax = 0x0304
	n.CipherSuites = chromeCipherSuites
	n.Extensions = newChromeExtensions()
	n.SigAlgs = []uint16{
		0x0804, // rsa_pss_rsae_sha256 (Windows prefers RSA-PSS first)
		0x0403, // ecdsa_secp256r1_sha256
		0x0401, // rsa_pkcs1_sha256
		0x0503, // ecdsa_secp384r1_sha384
		0x0805, // rsa_pss_rsae_sha384
		0x0501, // rsa_pkcs1_sha384
		0x0806, // rsa_pss_rsae_sha512
		0x0601, // rsa_pkcs1_sha512
	}
	n.SupportedGroups = []uint16{0x0017, 0x001D, 0x0018, 0x0019} // P-256 first on Windows
	n.KeyShareCurves = chromeCommonKeyShare
	n.ALPNProtos = chromeCommonALPN
	n.H2Settings = chromeH2Settings
	n.QUICParams = chromeQUICParams
	n.ECHPolicy = "off"
	n.GreasePolicy = "off"
	n.ProxyMode = "direct"
	n.ProxyLabel = ""
	return n
}
