package proxymux

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	socks5Version          = 0x05
	socks5AuthNone         = 0x00
	socks5AuthPassword     = 0x02
	socks5AuthNoMethods    = 0xFF
	socks5CmdConnect       = 0x01
	socks5AddrIPv4         = 0x01
	socks5AddrDomain       = 0x03
	socks5AddrIPv6         = 0x04
	socks5StatusSuccess    = 0x00
	socks5StatusFailure    = 0x01
	socks5StatusNotAllowed = 0x02
)

type socks5Request struct {
	cmd      byte
	addrType byte
	addr     string
	port     uint16
}

func readSocks5Auth(client io.Reader) (byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(client, header); err != nil {
		return 0, fmt.Errorf("proxymux: read auth header: %w", err)
	}
	if header[0] != socks5Version {
		return 0, fmt.Errorf("proxymux: unsupported SOCKS version: %d", header[0])
	}
	nmethods := int(header[1])
	if nmethods < 1 || nmethods > 255 {
		return 0, fmt.Errorf("proxymux: invalid nmethods: %d", nmethods)
	}
	methods := make([]byte, nmethods)
	if _, err := io.ReadFull(client, methods); err != nil {
		return 0, fmt.Errorf("proxymux: read methods: %w", err)
	}
	for _, m := range methods {
		if m == socks5AuthNone {
			return socks5AuthNone, nil
		}
	}
	for _, m := range methods {
		if m == socks5AuthPassword {
			return socks5AuthPassword, nil
		}
	}
	return socks5AuthNoMethods, nil
}

func readSocks5Connect(r io.Reader) (*socks5Request, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("proxymux: read connect header: %w", err)
	}
	if header[0] != socks5Version {
		return nil, fmt.Errorf("proxymux: unsupported SOCKS version in connect: %d", header[0])
	}
	if header[1] != socks5CmdConnect {
		return nil, fmt.Errorf("proxymux: unsupported command: %d", header[1])
	}

	req := &socks5Request{
		cmd:      header[1],
		addrType: header[3],
	}

	switch req.addrType {
	case socks5AddrIPv4:
		addr := make([]byte, 4)
		if _, err := io.ReadFull(r, addr); err != nil {
			return nil, fmt.Errorf("proxymux: read IPv4 address: %w", err)
		}
		req.addr = net.IP(addr).String()
	case socks5AddrDomain:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(r, lenBuf); err != nil {
			return nil, fmt.Errorf("proxymux: read domain length: %w", err)
		}
		domain := make([]byte, lenBuf[0])
		if _, err := io.ReadFull(r, domain); err != nil {
			return nil, fmt.Errorf("proxymux: read domain: %w", err)
		}
		req.addr = string(domain)
	case socks5AddrIPv6:
		addr := make([]byte, 16)
		if _, err := io.ReadFull(r, addr); err != nil {
			return nil, fmt.Errorf("proxymux: read IPv6 address: %w", err)
		}
		req.addr = net.IP(addr).String()
	default:
		return nil, fmt.Errorf("proxymux: unsupported address type: %d", req.addrType)
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(r, portBuf); err != nil {
		return nil, fmt.Errorf("proxymux: read port: %w", err)
	}
	req.port = binary.BigEndian.Uint16(portBuf)

	return req, nil
}

func writeSocks5Response(w io.Writer, status byte, req *socks5Request) error {
	var bindAddr []byte
	switch req.addrType {
	case socks5AddrIPv4:
		ip := net.ParseIP(req.addr).To4()
		if ip == nil {
			ip = net.IPv4(0, 0, 0, 0)
		}
		bindAddr = ip
	case socks5AddrDomain:
		bindAddr = make([]byte, 1+len(req.addr))
		bindAddr[0] = byte(len(req.addr))
		copy(bindAddr[1:], req.addr)
	case socks5AddrIPv6:
		ip := net.ParseIP(req.addr).To16()
		if ip == nil {
			ip = net.IPv6zero
		}
		bindAddr = ip
	}

	resp := make([]byte, 0, 4+len(bindAddr)+2)
	resp = append(resp, socks5Version, status, 0x00, req.addrType)
	resp = append(resp, bindAddr...)
	resp = append(resp, byte(req.port>>8), byte(req.port))
	_, err := w.Write(resp)
	return err
}

func dialTarget(req *socks5Request) (net.Conn, error) {
	addr := net.JoinHostPort(req.addr, fmt.Sprintf("%d", req.port))
	return net.DialTimeout("tcp", addr, 10*time.Second)
}
