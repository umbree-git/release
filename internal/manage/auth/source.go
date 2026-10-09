package auth

import (
	"net/http"
	"net/netip"
	"strings"
)

func (s *Service) source(r *http.Request) string {
	peer := ClientIP(r)
	if !s.TrustedProxy.IsValid() {
		return peer
	}
	addr, err := netip.ParseAddr(peer)
	if err != nil || addr.Unmap() != s.TrustedProxy.Unmap() {
		return peer
	}
	entries := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	last := strings.TrimSpace(entries[len(entries)-1])
	client, err := netip.ParseAddr(last)
	if err != nil || client.Zone() != "" {
		s.Log.Warn("the trusted proxy sent no usable X-Forwarded-For; counting against the proxy", "peer", peer, "entry", last)
		return peer
	}
	return client.Unmap().String()
}
