package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// ProxyConfig holds the parsed proxy connection details.
type ProxyConfig struct {
	Type     string `json:"type"`               // http, https, socks, socks4, socks5
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// GeoInfo holds geolocation data resolved through a proxy.
type GeoInfo struct {
	IP       string  `json:"ip"`
	Timezone string  `json:"timezone"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Country  string  `json:"country"`
	City     string  `json:"city"`
	Region   string  `json:"region"`
	ISP      string  `json:"isp"`
}

// ParseProxyURL parses a proxy string in the form "http://user:pass@host:port"
// or "host:port" (defaults to http).
func ParseProxyURL(raw string) (*ProxyConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty proxy string")
	}

	// If no scheme, try host:port format
	if !strings.Contains(raw, "://") {
		host, portStr, err := net.SplitHostPort(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy format %q: %w", raw, err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("invalid port %q: %w", portStr, err)
		}
		return &ProxyConfig{Type: "http", Host: host, Port: port}, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse proxy URL: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https", "socks", "socks4", "socks5":
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", scheme)
	}

	host := u.Hostname()
	portStr := u.Port()
	if portStr == "" {
		switch scheme {
		case "http":
			portStr = "80"
		case "https":
			portStr = "443"
		case "socks", "socks4", "socks5":
			portStr = "1080"
		}
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid port %q: %w", portStr, err)
	}

	pc := &ProxyConfig{
		Type: scheme,
		Host: host,
		Port: port,
	}
	if u.User != nil {
		pc.Username = u.User.Username()
		pc.Password, _ = u.User.Password()
	}
	return pc, nil
}

// ParseProxyList parses a newline-separated list of proxy URLs.
// Empty lines and lines starting with '#' are skipped.
func ParseProxyList(text string) ([]*ProxyConfig, error) {
	var proxies []*ProxyConfig
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p, err := ParseProxyURL(line)
		if err != nil {
			return nil, fmt.Errorf("line %q: %w", line, err)
		}
		proxies = append(proxies, p)
	}
	return proxies, nil
}

// URL formats the proxy config back to a URL string.
func (p ProxyConfig) URL() string {
	var userinfo string
	if p.Username != "" {
		if p.Password != "" {
			userinfo = url.UserPassword(p.Username, p.Password).String() + "@"
		} else {
			userinfo = url.User(p.Username).String() + "@"
		}
	}
	return fmt.Sprintf("%s://%s%s:%d", p.Type, userinfo, p.Host, p.Port)
}

// String returns a human-readable display format like "http 1.2.3.4:8080".
func (p ProxyConfig) String() string {
	return fmt.Sprintf("%s %s:%d", p.Type, p.Host, p.Port)
}

// httpTransport builds an *http.Transport configured to use this proxy.
func (p ProxyConfig) httpTransport() (*http.Transport, error) {
	switch p.Type {
	case "http", "https":
		proxyURL, err := url.Parse(p.URL())
		if err != nil {
			return nil, err
		}
		return &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		}, nil
	case "socks", "socks4", "socks5":
		var auth *proxy.Auth
		if p.Username != "" {
			auth = &proxy.Auth{User: p.Username, Password: p.Password}
		}
		addr := net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
		dialer, err := proxy.SOCKS5("tcp", addr, auth, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("create SOCKS dialer: %w", err)
		}
		contextDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("SOCKS dialer does not support DialContext")
		}
		return &http.Transport{
			DialContext: contextDialer.DialContext,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported proxy type %q", p.Type)
	}
}

// geoAPIURL is the endpoint used for geo resolution.
const geoAPIURL = "http://ip-api.com/json?fields=query,timezone,lat,lon,country,city,regionName,isp"

// ResolveGeo makes an HTTP request through the proxy to ip-api.com and returns
// the geo information of the proxy's exit IP.
func ResolveGeo(p ProxyConfig) (*GeoInfo, error) {
	transport, err := p.httpTransport()
	if err != nil {
		return nil, fmt.Errorf("build transport: %w", err)
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
	}

	resp, err := client.Get(geoAPIURL)
	if err != nil {
		return nil, fmt.Errorf("geo request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read geo response: %w", err)
	}

	// The API returns regionName, we map it to Region.
	var raw struct {
		Query      string  `json:"query"`
		Timezone   string  `json:"timezone"`
		Lat        float64 `json:"lat"`
		Lon        float64 `json:"lon"`
		Country    string  `json:"country"`
		City       string  `json:"city"`
		RegionName string  `json:"regionName"`
		ISP        string  `json:"isp"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse geo JSON: %w", err)
	}

	return &GeoInfo{
		IP:       raw.Query,
		Timezone: raw.Timezone,
		Lat:      raw.Lat,
		Lon:      raw.Lon,
		Country:  raw.Country,
		City:     raw.City,
		Region:   raw.RegionName,
		ISP:      raw.ISP,
	}, nil
}

// TestProxy measures the round-trip latency through the proxy to ip-api.com.
func TestProxy(p ProxyConfig) (latencyMs int64, err error) {
	transport, err := p.httpTransport()
	if err != nil {
		return 0, fmt.Errorf("build transport: %w", err)
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
	}

	start := time.Now()
	resp, err := client.Get(geoAPIURL)
	if err != nil {
		return 0, fmt.Errorf("proxy test: %w", err)
	}
	resp.Body.Close()
	return time.Since(start).Milliseconds(), nil
}
