package probemux

import (
	"crypto/md5"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/VulpineOS/vulpine-networklab/internal/identity"
)

type ProbeResult struct {
	JA3         string
	JA4         string
	ALPN        []string
	CipherSuite uint16
	TLSVersion  uint16
	ServerName  string
	Matches     bool
}

type ParsedClientHello struct {
	TLSVersion      uint16
	CipherSuites    []uint16
	ExtTypes        []uint16
	SupportedGroups []uint16
	SigAlgs         []uint16
	ALPN            []string
	SNI             string
	Ja3             string
}

func ProbeTLS(target string, expected *identity.NetworkIdentity) (*ProbeResult, error) {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		host = target
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", target, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         host,
	})
	if err != nil {
		return nil, fmt.Errorf("probemux: TLS dial %s: %w", target, err)
	}
	defer conn.Close()

	state := conn.ConnectionState()

	result := &ProbeResult{
		CipherSuite: state.CipherSuite,
		TLSVersion:  state.Version,
		ServerName:  host,
	}
	if len(state.NegotiatedProtocol) > 0 {
		result.ALPN = []string{state.NegotiatedProtocol}
	}

	if expected != nil {
		hashes, err := identity.HashIdentity(expected)
		if err != nil {
			return result, fmt.Errorf("probemux: hash identity: %w", err)
		}
		result.JA3 = hashes.JA3
		result.JA4 = hashes.JA4
	}

	return result, nil
}

func ProbeTLSWithCapture(target string, expected *identity.NetworkIdentity, tlsConfig *tls.Config) (*ProbeResult, error) {
	proxy, err := NewCaptureProxy(target)
	if err != nil {
		return nil, fmt.Errorf("probemux: create capture proxy: %w", err)
	}

	var captured []byte
	captureDone := make(chan error, 1)
	go func() {
		var capErr error
		captured, capErr = proxy.CaptureOne()
		captureDone <- capErr
	}()

	time.Sleep(50 * time.Millisecond)

	if tlsConfig == nil {
		tlsConfig = &tls.Config{InsecureSkipVerify: true}
	}
	if tlsConfig.ServerName == "" {
		host, _, _ := net.SplitHostPort(target)
		tlsConfig.ServerName = host
	}
	tlsConfig.InsecureSkipVerify = true

	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", proxy.Addr().String(), tlsConfig)
	if err != nil {
		proxy.Close()
		return nil, fmt.Errorf("probemux: TLS dial through proxy: %w", err)
	}
	defer conn.Close()

	if err := <-captureDone; err != nil {
		return nil, fmt.Errorf("probemux: capture failed: %w", err)
	}

	parsed, err := ParseClientHello(captured)
	if err != nil {
		return nil, fmt.Errorf("probemux: parse client hello: %w", err)
	}

	ja3 := computeJA3(parsed.TLSVersion, parsed.CipherSuites, parsed.ExtTypes, parsed.SupportedGroups)

	result := &ProbeResult{
		JA3:         ja3,
		CipherSuite: uint16(conn.ConnectionState().CipherSuite),
		TLSVersion:  conn.ConnectionState().Version,
		ALPN:        parsed.ALPN,
		ServerName:  parsed.SNI,
	}

	if expected != nil && expected.JA4 != "" {
		result.JA4 = expected.JA4
	}

	if expected != nil {
		expectedHashes, err := identity.HashIdentity(expected)
		if err == nil {
			if result.JA3 == expectedHashes.JA3 {
				result.Matches = true
			}
		}
	}

	return result, nil
}

func ParseClientHello(data []byte) (*ParsedClientHello, error) {
	if len(data) < 5 {
		return nil, fmt.Errorf("probemux: data too short for TLS record header (%d bytes)", len(data))
	}

	if data[0] != 0x16 {
		return nil, fmt.Errorf("probemux: expected handshake record (0x16), got 0x%02x", data[0])
	}

	recordLen := int(binary.BigEndian.Uint16(data[3:5]))
	if len(data) < 5+recordLen {
		return nil, fmt.Errorf("probemux: record truncated: header claims %d bytes, have %d", recordLen, len(data)-5)
	}

	body := data[5:]
	if len(body) < 1 {
		return nil, fmt.Errorf("probemux: empty record body")
	}

	if body[0] != 0x01 {
		return nil, fmt.Errorf("probemux: expected ClientHello (0x01), got 0x%02x", body[0])
	}

	pos := 1
	if len(body) < pos+3 {
		return nil, fmt.Errorf("probemux: truncated handshake header")
	}
	handshakeLen := int(binary.BigEndian.Uint32([]byte{0, body[pos], body[pos+1], body[pos+2]}))
	pos += 3

	if len(body) < pos+handshakeLen {
		return nil, fmt.Errorf("probemux: handshake truncated: header claims %d bytes, have %d", handshakeLen, len(body)-pos)
	}

	chEnd := pos + handshakeLen

	if len(body) < pos+2 {
		return nil, fmt.Errorf("probemux: truncated client version")
	}
	legacyVersion := binary.BigEndian.Uint16(body[pos:])
	pos += 2

	pos += 32

	if pos >= chEnd {
		return nil, fmt.Errorf("probemux: truncated session id")
	}
	sidLen := int(body[pos])
	pos++
	if pos+sidLen > chEnd {
		return nil, fmt.Errorf("probemux: truncated session id body")
	}
	pos += sidLen

	if pos+2 > chEnd {
		return nil, fmt.Errorf("probemux: truncated cipher suites length")
	}
	cipherLen := int(binary.BigEndian.Uint16(body[pos:]))
	pos += 2

	if pos+cipherLen > chEnd {
		return nil, fmt.Errorf("probemux: truncated cipher suites")
	}
	ciphers := make([]uint16, 0, cipherLen/2)
	for i := 0; i+1 < cipherLen; i += 2 {
		if pos+i+1 >= chEnd {
			break
		}
		c := binary.BigEndian.Uint16(body[pos+i:])
		ciphers = append(ciphers, c)
	}
	pos += cipherLen

	if pos >= chEnd {
		return nil, fmt.Errorf("probemux: truncated compression methods")
	}
	compLen := int(body[pos])
	pos++
	if pos+compLen > chEnd {
		return nil, fmt.Errorf("probemux: truncated compression methods body")
	}
	pos += compLen

	if pos+2 > chEnd {
		return nil, fmt.Errorf("probemux: truncated extensions length")
	}
	extLen := int(binary.BigEndian.Uint16(body[pos:]))
	pos += 2

	parsed := &ParsedClientHello{
		TLSVersion:      legacyVersion,
		CipherSuites:    ciphers,
		ExtTypes:        make([]uint16, 0),
		SupportedGroups: make([]uint16, 0),
		SigAlgs:         make([]uint16, 0),
		ALPN:            make([]string, 0),
	}

	extEnd := pos + extLen
	if extEnd > chEnd {
		extEnd = chEnd
	}

	foundSupportedVersions := false

	for pos+4 <= extEnd {
		extType := binary.BigEndian.Uint16(body[pos:])
		extDataLen := int(binary.BigEndian.Uint16(body[pos+2:]))
		extDataStart := pos + 4
		extDataEnd := extDataStart + extDataLen

		if extDataEnd > extEnd {
			break
		}

		parsed.ExtTypes = append(parsed.ExtTypes, extType)

		switch extType {
		case 0x0000:
			// ServerNameList: list_length(2) + ServerName entries
			// ServerName: name_type(1) + name_length(2) + name(variable)
			if extDataEnd-extDataStart >= 5 {
				p := extDataStart
				listLen := int(binary.BigEndian.Uint16(body[p:]))
				_ = listLen
				p += 2
				if p < extDataEnd {
					nameType := body[p]
					p++
					_ = nameType
					if p+2 <= extDataEnd {
						nameLen := int(binary.BigEndian.Uint16(body[p:]))
						p += 2
						if nameLen > 0 && p+nameLen <= extDataEnd {
							parsed.SNI = string(body[p : p+nameLen])
						}
					}
				}
			}

		case 0x000A:
			if extDataEnd-extDataStart >= 2 {
				groupLen := int(binary.BigEndian.Uint16(body[extDataStart:]))
				if groupLen+2 <= extDataEnd-extDataStart {
					groups := make([]uint16, 0, groupLen/2)
					for i := 0; i+1 < groupLen; i += 2 {
						g := binary.BigEndian.Uint16(body[extDataStart+2+i:])
						groups = append(groups, g)
					}
					parsed.SupportedGroups = groups
				}
			}

		case 0x000D:
			if extDataEnd-extDataStart >= 2 {
				sigLen := int(binary.BigEndian.Uint16(body[extDataStart:]))
				if sigLen+2 <= extDataEnd-extDataStart {
					algs := make([]uint16, 0, sigLen/2)
					for i := 0; i+1 < sigLen; i += 2 {
						a := binary.BigEndian.Uint16(body[extDataStart+2+i:])
						algs = append(algs, a)
					}
					parsed.SigAlgs = algs
				}
			}

		case 0x0010:
			if extDataEnd-extDataStart >= 2 {
				alpnLen := int(binary.BigEndian.Uint16(body[extDataStart:]))
				if alpnLen+2 <= extDataEnd-extDataStart {
					ap := extDataStart + 2
					for ap < extDataStart+2+alpnLen {
						if ap+1 > extDataEnd {
							break
						}
						protoLen := int(body[ap])
						ap++
						if ap+protoLen <= extDataEnd {
							parsed.ALPN = append(parsed.ALPN, string(body[ap:ap+protoLen]))
							ap += protoLen
						} else {
							break
						}
					}
				}
			}

		case 0x002B:
			if extDataEnd-extDataStart >= 1 {
				svLen := int(body[extDataStart])
				if svLen > 0 && extDataStart+1+svLen <= extDataEnd {
					if svLen >= 2 {
						actualVersion := binary.BigEndian.Uint16(body[extDataStart+1:])
						parsed.TLSVersion = actualVersion
						foundSupportedVersions = true
					}
				}
			}
		}

		pos = extDataEnd
	}

	_ = foundSupportedVersions

	parsed.Ja3 = computeJA3(parsed.TLSVersion, parsed.CipherSuites, parsed.ExtTypes, parsed.SupportedGroups)

	return parsed, nil
}

type CaptureProxy struct {
	listener net.Listener
	target   string
}

func NewCaptureProxy(target string) (*CaptureProxy, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("probemux: listener: %w", err)
	}
	return &CaptureProxy{listener: listener, target: target}, nil
}

func (cp *CaptureProxy) Addr() net.Addr {
	return cp.listener.Addr()
}

func (cp *CaptureProxy) Close() error {
	return cp.listener.Close()
}

func (cp *CaptureProxy) CaptureOne() ([]byte, error) {
	clientConn, err := cp.listener.Accept()
	if err != nil {
		return nil, fmt.Errorf("probemux: accept: %w", err)
	}

	captured, err := readClientHelloRecord(clientConn)
	if err != nil {
		clientConn.Close()
		return nil, err
	}

	targetConn, err := net.DialTimeout("tcp", cp.target, 10*time.Second)
	if err != nil {
		clientConn.Close()
		return nil, fmt.Errorf("probemux: connect to target %s: %w", cp.target, err)
	}

	if _, err := targetConn.Write(captured); err != nil {
		clientConn.Close()
		targetConn.Close()
		return nil, fmt.Errorf("probemux: forward client hello: %w", err)
	}

	go func() {
		io.Copy(targetConn, clientConn)
		clientConn.Close()
		targetConn.Close()
	}()
	go func() {
		io.Copy(clientConn, targetConn)
		clientConn.Close()
		targetConn.Close()
	}()

	return captured, nil
}

func readClientHelloRecord(conn net.Conn) ([]byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, fmt.Errorf("probemux: read record header: %w", err)
	}
	if header[0] != 0x16 {
		return nil, fmt.Errorf("probemux: expected handshake (0x16), got 0x%02x", header[0])
	}
	bodyLen := int(binary.BigEndian.Uint16(header[3:5]))
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, fmt.Errorf("probemux: read record body: %w", err)
	}
	out := make([]byte, 5+bodyLen)
	copy(out, header)
	copy(out[5:], body)
	return out, nil
}

func computeJA3(version uint16, ciphers, exts, groups []uint16) string {
	verStr := fmt.Sprintf("%d", version)
	cipherStr := joinUint16(ciphers)
	extStr := joinUint16(exts)
	groupStr := joinUint16(groups)
	input := verStr + "," + cipherStr + "," + extStr + "," + groupStr + ",0-1-2"
	h := md5.Sum([]byte(input))
	return hex.EncodeToString(h[:])
}

func joinUint16(vals []uint16) string {
	if len(vals) == 0 {
		return ""
	}
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = fmt.Sprintf("%d", v)
	}
	return strings.Join(parts, "-")
}
