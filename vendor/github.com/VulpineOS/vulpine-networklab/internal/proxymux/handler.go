package proxymux

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VulpineOS/vulpine-networklab/internal/identity"
	"github.com/VulpineOS/vulpine-networklab/internal/probemux"
	"github.com/VulpineOS/vulpine-networklab/internal/profiles"
)

func (p *Proxy) handleConn(client net.Conn) {
	defer client.Close()

	client.SetDeadline(time.Now().Add(10 * time.Second))

	authMethod, err := readSocks5Auth(client)
	if err != nil {
		log.Printf("[proxymux] auth error: %v", err)
		return
	}

	switch authMethod {
	case socks5AuthNoMethods:
		client.Write([]byte{socks5Version, socks5AuthNoMethods})
		return
	case socks5AuthPassword:
		client.Write([]byte{socks5Version, socks5AuthNoMethods})
		return
	case socks5AuthNone:
		client.Write([]byte{socks5Version, socks5AuthNone})
	}

	req, err := readSocks5Connect(client)
	if err != nil {
		log.Printf("[proxymux] connect read error: %v", err)
		return
	}

	route := p.matchRoute(req.addr)

	target, err := dialTarget(req)
	if err != nil {
		log.Printf("[proxymux] connect to %s:%d: %v", req.addr, req.port, err)
		writeSocks5Response(client, socks5StatusFailure, req)
		return
	}
	defer target.Close()

	client.SetDeadline(time.Time{})

	if err := writeSocks5Response(client, socks5StatusSuccess, req); err != nil {
		log.Printf("[proxymux] write SOCKS5 response: %v", err)
		return
	}

	if route != nil && route.IdentityName != "" {
		p.captureAndValidate(client, target, route.IdentityName)
	} else {
		relay(client, target)
	}
}

func (p *Proxy) captureAndValidate(client net.Conn, target net.Conn, identityName string) {
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	captured, err := readTLSRecord(client)
	client.SetReadDeadline(time.Time{})

	var parsed *probemux.ParsedClientHello
	var matched bool

	if err == nil {
		parsed, err = probemux.ParseClientHello(captured)
	}

	if err == nil && parsed != nil {
		prof := profiles.Get(identityName)
		if prof != nil {
			expectedHashes, hashErr := identity.HashIdentity(prof)
			if hashErr == nil {
				matched = parsed.Ja3 == expectedHashes.JA3
			}
		}

		atomic.AddInt64(&p.validatedConns, 1)
		if matched {
			atomic.AddInt64(&p.matchCount, 1)
			log.Printf("[proxymux] MATCH: %s JA3=%s", identityName, parsed.Ja3)
		} else {
			atomic.AddInt64(&p.mismatchCount, 1)
			log.Printf("[proxymux] MISMATCH: %s JA3=%s", identityName, parsed.Ja3)
		}
	}

	if len(captured) > 0 {
		target.Write(captured)
	}

	relay(client, target)
}

func relay(client, target net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(target, client)
		target.Close()
	}()
	go func() {
		defer wg.Done()
		io.Copy(client, target)
		client.Close()
	}()

	wg.Wait()
}

func readTLSRecord(r io.Reader) ([]byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("proxymux: read TLS record header: %w", err)
	}
	bodyLen := int(uint16(header[3])<<8 | uint16(header[4]))
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return append(header, body...), fmt.Errorf("proxymux: read TLS record body: %w", err)
	}
	out := make([]byte, 5+bodyLen)
	copy(out, header)
	copy(out[5:], body)
	return out, nil
}
