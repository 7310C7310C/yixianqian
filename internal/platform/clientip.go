package platform

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ParseTrustedProxies parses a comma-separated list of IP addresses or CIDR
// prefixes. An empty value yields an empty list (no proxy is trusted).
func ParseTrustedProxies(raw string) ([]netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []netip.Prefix
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "/") {
			p, err := netip.ParsePrefix(item)
			if err != nil {
				return nil, fmt.Errorf("platform: parse trusted proxy %q: %w", item, err)
			}
			out = append(out, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("platform: parse trusted proxy %q: %w", item, err)
		}
		addr = addr.Unmap()
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

// ClientIP resolves the caller address. Forwarding headers are only honoured
// when the direct peer is one of the configured trusted proxies.
func ClientIP(remoteAddr string, header http.Header, trusted []netip.Prefix) string {
	peer := parseAddr(remoteAddr)
	if !peer.IsValid() {
		return ""
	}
	if !isTrusted(peer, trusted) {
		return peer.String()
	}
	if addr := parseAddr(header.Get("X-Real-IP")); addr.IsValid() {
		return addr.String()
	}
	if xff := header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if addr := parseAddr(first); addr.IsValid() {
			return addr.String()
		}
	}
	return peer.String()
}

func parseAddr(raw string) netip.Addr {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return netip.Addr{}
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	raw = strings.Trim(raw, "[]")
	addr, err := netip.ParseAddr(raw)
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
