package backend

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

const (
	guardDialTimeout     = 30 * time.Second
	guardTLSTimeout      = 30 * time.Second
	guardResponseTimeout = 10 * time.Minute
	guardIdleConnTimeout = 90 * time.Second
)

var ErrRedirect = errors.New("outbound guard: redirects are refused")

type Guard struct{}

func (g *Guard) Client() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           g.dial,
			TLSHandshakeTimeout:   guardTLSTimeout,
			ResponseHeaderTimeout: guardResponseTimeout,
			IdleConnTimeout:       guardIdleConnTimeout,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return fmt.Errorf("%w: %s", ErrRedirect, req.URL.Redacted())
		},
	}
}

func (g *Guard) CheckURL(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("outbound guard: %q is not https", u.Redacted())
	}
	if u.User != nil {
		return fmt.Errorf("outbound guard: %q carries userinfo", u.Redacted())
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("outbound guard: %q has no host", u.Redacted())
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return fmt.Errorf("outbound guard: %q is an IP literal", u.Redacted())
	}
	return nil
}

func (g *Guard) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("outbound guard: %q is not host:port", address)
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil, fmt.Errorf("outbound guard: %q is an IP literal", host)
	}
	addrs, err := (&net.Resolver{}).LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("outbound guard: resolve %q: %w", host, err)
	}
	dialer := net.Dialer{Timeout: guardDialTimeout}
	for _, a := range addrs {
		if !isPublic(a) {
			return nil, fmt.Errorf("outbound guard: %q resolves to the private address %s", host, a)
		}
		if conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(a.String(), port)); err == nil {
			return conn, nil
		}
	}
	return nil, fmt.Errorf("outbound guard: could not connect to %q", host)
}

func isPublic(a netip.Addr) bool {
	a = a.Unmap()
	switch {
	case !a.IsValid(), a.IsLoopback(), a.IsPrivate(), a.IsUnspecified(),
		a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast(), a.IsMulticast(),
		a.IsInterfaceLocalMulticast():
		return false
	}
	if a.Is4() {
		b := a.As4()
		if b[0] == 100 && b[1] >= 64 && b[1] <= 127 {
			return false
		}
	}
	return true
}
