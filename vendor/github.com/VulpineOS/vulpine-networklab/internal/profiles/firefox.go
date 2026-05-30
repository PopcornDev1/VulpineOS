package profiles

import "github.com/VulpineOS/vulpine-networklab/internal/identity"

func newFirefoxExtensions() []identity.TLSExtension {
	return []identity.TLSExtension{
		{Type: 0x0000, Present: true}, // server_name
		{Type: 0x0017, Present: true}, // extended_master_secret
		{Type: 0xFF01, Present: true}, // renegotiation_info
		{Type: 0x000A, Present: true}, // supported_groups
		{Type: 0x000B, Present: true}, // ec_point_formats
		{Type: 0x0023, Present: true}, // session_ticket
		{Type: 0x0010, Present: true}, // ALPN
		{Type: 0x0005, Present: true}, // status_request
		{Type: 0x0022, Present: true}, // delegated_credentials
		{Type: 0x0012, Present: true}, // signed_certificate_timestamp
		{Type: 0x0033, Present: true}, // key_share
		{Type: 0x002B, Present: true}, // supported_versions
		{Type: 0x000D, Present: true}, // signature_algorithms
		{Type: 0x002D, Present: true}, // PSK key exchange modes
		{Type: 0x001C, Present: true}, // record_size_limit
		{Type: 0x001B, Present: true}, // compressed_certificate
		{Type: 0xFE0D, Present: true}, // encrypted_client_hello (ECH)
	}
}

var firefoxCipherSuites = []uint16{
	0x1301, // TLS_AES_128_GCM_SHA256
	0x1302, // TLS_AES_256_GCM_SHA384
	0x1303, // TLS_CHACHA20_POLY1305_SHA256
	0xC02B, // TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
	0xC02F, // TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
	0xCCA9, // TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256
	0xCCA8, // TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256
	0xC00A, // TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA
	0xC009, // TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA
	0xC013, // TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA
	0xC014, // TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA
	0x009C, // TLS_RSA_WITH_AES_128_GCM_SHA256
	0x009D, // TLS_RSA_WITH_AES_256_GCM_SHA384
	0x002F, // TLS_RSA_WITH_AES_128_CBC_SHA
	0x0035, // TLS_RSA_WITH_AES_256_CBC_SHA
	0x000A, // TLS_RSA_WITH_3DES_EDE_CBC_SHA
}

var firefoxH2Settings = identity.H2SettingsFrame{
	HeaderTableSize:      4096,
	EnablePush:           0,
	MaxConcurrentStreams: 100,
	InitialWindowSize:    131072,
	MaxFrameSize:         16384,
	MaxHeaderListSize:    262144,
}

var firefoxQUICParams = identity.QUICTransportParams{
	InitialMaxData:             12533760,
	InitialMaxStreamDataBidi:   1048576,
	InitialMaxStreamDataUni:    1048576,
	InitialMaxStreamsBidi:      100,
	InitialMaxStreamsUni:       100,
	AckDelayExponent:           3,
	MaxAckDelay:                25,
	DisableActiveMigration:     false,
	ActiveConnectionIDLimit:    8,
	InitialSourceConnectionID:  nil,
}

var firefoxCommonGroups = []uint16{0x11EC, 0x001D, 0x0017, 0x0018, 0x0019, 0x0100, 0x0101} // X25519MLKEM768, X25519, P-256, P-384, P-521, ffdhe2048, ffdhe3072

var firefoxCommonKeyShare = []uint16{0x001D} // X25519 only

var firefoxCommonALPN = []string{"h2", "http/1.1"}

func Firefox131MacOS() *identity.NetworkIdentity {
	n := identity.NewNetworkIdentity("firefox131_macos")
	n.ID = "firefox_131_macos_001"
	n.Label = "Firefox 131 macOS 14"
	n.TLSVersionMin = 0x0303
	n.TLSVersionMax = 0x0304
	n.CipherSuites = firefoxCipherSuites
	n.Extensions = newFirefoxExtensions()
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
	n.SupportedGroups = firefoxCommonGroups
	n.KeyShareCurves = firefoxCommonKeyShare
	n.ALPNProtos = firefoxCommonALPN
	n.H2Settings = firefoxH2Settings
	n.QUICParams = firefoxQUICParams
	n.ECHPolicy = "off"
	n.GreasePolicy = "off"
	n.ProxyMode = "direct"
	n.ProxyLabel = ""
	return n
}

func Firefox131Windows() *identity.NetworkIdentity {
	n := identity.NewNetworkIdentity("firefox131_windows")
	n.ID = "firefox_131_windows_001"
	n.Label = "Firefox 131 Windows 11"
	n.TLSVersionMin = 0x0303
	n.TLSVersionMax = 0x0304
	n.CipherSuites = firefoxCipherSuites
	n.Extensions = newFirefoxExtensions()
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
	n.SupportedGroups = []uint16{0x0017, 0x001D, 0x0018} // P-256 first on Windows
	n.KeyShareCurves = firefoxCommonKeyShare
	n.ALPNProtos = firefoxCommonALPN
	n.H2Settings = firefoxH2Settings
	n.QUICParams = firefoxQUICParams
	n.ECHPolicy = "off"
	n.GreasePolicy = "off"
	n.ProxyMode = "direct"
	n.ProxyLabel = ""
	return n
}

func Firefox131Linux() *identity.NetworkIdentity {
	n := identity.NewNetworkIdentity("firefox131_linux")
	n.ID = "firefox_131_linux_001"
	n.Label = "Firefox 131 Ubuntu 22.04"
	n.TLSVersionMin = 0x0303
	n.TLSVersionMax = 0x0304
	n.CipherSuites = firefoxCipherSuites
	n.Extensions = newFirefoxExtensions()
	n.SigAlgs = []uint16{
		0x0403, // ecdsa_secp256r1_sha256
		0x0401, // rsa_pkcs1_sha256 (Linux places RSA-PKCS before RSA-PSS)
		0x0804, // rsa_pss_rsae_sha256
		0x0503, // ecdsa_secp384r1_sha384
		0x0805, // rsa_pss_rsae_sha384
		0x0501, // rsa_pkcs1_sha384
		0x0806, // rsa_pss_rsae_sha512
		0x0601, // rsa_pkcs1_sha512
	}
	n.SupportedGroups = []uint16{0x001D, 0x0017, 0x0018, 0x0019} // includes P-521 on Linux
	n.KeyShareCurves = firefoxCommonKeyShare
	n.ALPNProtos = firefoxCommonALPN
	n.H2Settings = firefoxH2Settings
	n.QUICParams = firefoxQUICParams
	n.ECHPolicy = "off"
	n.GreasePolicy = "off"
	n.ProxyMode = "direct"
	n.ProxyLabel = ""
	return n
}
