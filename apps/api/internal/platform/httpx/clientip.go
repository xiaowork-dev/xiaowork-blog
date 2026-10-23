package httpx

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP resolves the caller's address. X-Forwarded-For is only consulted when the direct peer is a
// trusted proxy, and then walked right-to-left, skipping trusted hops, so a client cannot spoof it by
// prepending values (SAD §7.2, API-DD §2).
func ClientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	peer := parseAddr(r.RemoteAddr)
	if !peer.IsValid() || !isTrusted(peer, trusted) {
		return peer
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		addr := parseAddr(strings.TrimSpace(hops[i]))
		if !addr.IsValid() {
			break
		}
		if !isTrusted(addr, trusted) {
			return addr
		}
		peer = addr
	}
	return peer
}

func parseAddr(s string) netip.Addr {
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

func isTrusted(addr netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
