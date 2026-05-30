package proxymux

import (
	"context"
	"fmt"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

type Route struct {
	Pattern      string
	IdentityName string
	ID           string
}

type Proxy struct {
	addr       string
	routes     []Route
	identityFn func(name string) bool

	listener net.Listener

	totalConns     int64
	validatedConns int64
	matchCount     int64
	mismatchCount  int64

	mu sync.RWMutex
}

type Stats struct {
	TotalConns     int64
	ValidatedConns int64
	MatchCount     int64
	MismatchCount  int64
}

func New(addr string, identityCheck func(name string) bool) *Proxy {
	return &Proxy{
		addr:       addr,
		identityFn: identityCheck,
	}
}

func (p *Proxy) AddRoute(r Route) {
	if p.identityFn != nil && r.IdentityName != "" && !p.identityFn(r.IdentityName) {
		log.Printf("[proxymux] warning: identity %q not found for route %q", r.IdentityName, r.Pattern)
	}
	if r.ID == "" {
		r.ID = fmt.Sprintf("%s|%s", r.IdentityName, r.Pattern)
	}
	p.mu.Lock()
	p.routes = append(p.routes, r)
	sort.SliceStable(p.routes, func(i, j int) bool {
		pi, pj := p.routes[i].Pattern, p.routes[j].Pattern
		if pi == "*" && pj != "*" {
			return false
		}
		if pj == "*" && pi != "*" {
			return true
		}
		return len(pi) > len(pj)
	})
	p.mu.Unlock()
}

func (p *Proxy) ListenAndServe(ctx context.Context) error {
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", p.addr)
	if err != nil {
		return fmt.Errorf("proxymux: listen %s: %w", p.addr, err)
	}
	p.mu.Lock()
	p.listener = listener
	p.mu.Unlock()

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		client, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				log.Printf("[proxymux] accept error: %v", err)
				continue
			}
		}
		atomic.AddInt64(&p.totalConns, 1)
		go p.handleConn(client)
	}
}

func (p *Proxy) matchRoute(host string) *Route {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for i := range p.routes {
		r := &p.routes[i]
		if r.Pattern == "*" {
			return r
		}
		if strings.HasSuffix(host, r.Pattern) {
			return r
		}
	}
	return nil
}

func (p *Proxy) Addr() net.Addr {
	p.mu.RLock()
	l := p.listener
	p.mu.RUnlock()
	if l == nil {
		return nil
	}
	return l.Addr()
}

func (p *Proxy) Stats() Stats {
	return Stats{
		TotalConns:     atomic.LoadInt64(&p.totalConns),
		ValidatedConns: atomic.LoadInt64(&p.validatedConns),
		MatchCount:     atomic.LoadInt64(&p.matchCount),
		MismatchCount:  atomic.LoadInt64(&p.mismatchCount),
	}
}
