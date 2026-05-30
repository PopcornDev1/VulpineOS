package mobilebridge

import (
	"context"
	"fmt"
	"net"
)

var newProxyForAttachedServer = NewProxy

// AttachedServer is a running local HTTP server wired to one Android
// device's Chrome devtools socket through a Proxy.
type AttachedServer struct {
	Serial   string
	Addr     string
	Endpoint string

	Server *Server
	Proxy  *Proxy
}

// StartAttachedServer starts a local CDP bridge for serial at addr. The
// public HTTP server listens on addr while the ADB forward uses a separate
// ephemeral localhost port.
func StartAttachedServer(ctx context.Context, serial string, addr string) (*AttachedServer, error) {
	adbPort, err := freeTCPPort()
	if err != nil {
		return nil, err
	}
	return StartAttachedServerWithADBPort(ctx, serial, adbPort, addr)
}

// StartAttachedServerWithADBPort is StartAttachedServer with an explicit ADB
// forward port. It is useful for tests and callers that manage port allocation.
func StartAttachedServerWithADBPort(ctx context.Context, serial string, adbPort int, addr string) (*AttachedServer, error) {
	proxy, err := newProxyForAttachedServer(ctx, serial, adbPort)
	if err != nil {
		return nil, err
	}
	server := NewServer(serial, addr)
	if err := server.Start(); err != nil {
		_ = proxy.Close()
		return nil, err
	}
	if err := server.RunWithProxy(proxy); err != nil {
		_ = server.Stop()
		_ = proxy.Close()
		return nil, err
	}
	return &AttachedServer{
		Serial:   serial,
		Addr:     addr,
		Endpoint: "http://" + publicEndpointHost(addr),
		Server:   server,
		Proxy:    proxy,
	}, nil
}

// Close stops the public server first, then tears down the ADB proxy.
func (a *AttachedServer) Close() error {
	if a == nil {
		return nil
	}
	var err error
	if a.Server != nil {
		if serverErr := a.Server.Stop(); serverErr != nil {
			err = serverErr
		}
	}
	if a.Proxy != nil {
		if proxyErr := a.Proxy.Close(); proxyErr != nil && err == nil {
			err = proxyErr
		}
	}
	return err
}

// Done is closed when the underlying proxy can no longer recover.
func (a *AttachedServer) Done() <-chan struct{} {
	if a == nil || a.Proxy == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return a.Proxy.Done()
}

func publicEndpointHost(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func freeTCPPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("mobilebridge: unexpected listener addr %T", ln.Addr())
	}
	return addr.Port, nil
}
