// SPDX-License-Identifier: GPL-3.0-or-later

// Package clientip works out who sent a request, given that the request itself
// gets a say in the answer.
//
// X-Forwarded-For is set by whoever sent the request. Behind a proxy it has to
// be read — RemoteAddr is the proxy for every visitor, and rate-limiting on
// that would treat the whole internet as one client — but reading it without
// asking where it came from is how a rate limiter becomes decorative: vary the
// header, get a fresh bucket, submit as often as you like.
//
// So the header is believed only as far as the proxies that added it are
// trusted, and not at all otherwise.
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Unknown is returned when nothing usable could be determined. It is a value
// rather than an empty string so a log line and a rate-limit key both say
// plainly that the address was not established.
const Unknown = "unknown"

// Resolver answers "who sent this" for one deployment's network shape.
//
// The zero value trusts nothing and always answers with the connection's own
// address, which is the right behaviour for a service exposed directly.
type Resolver struct {
	trusted []netip.Prefix
}

// NewResolver builds a Resolver from a list of trusted proxy addresses or
// CIDRs — the hops between the internet and this service, whose contribution to
// X-Forwarded-For can be believed.
//
// A bare address is accepted and treated as a single-host prefix, because
// "10.0.0.5" is what a person writes when there is one proxy.
func NewResolver(trusted []string) (Resolver, error) {
	var prefixes []netip.Prefix

	for _, raw := range trusted {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}

		if strings.Contains(raw, "/") {
			p, err := netip.ParsePrefix(raw)
			if err != nil {
				return Resolver{}, fmt.Errorf("trusted proxy %q: %w", raw, err)
			}
			prefixes = append(prefixes, p.Masked())
			continue
		}

		addr, err := netip.ParseAddr(raw)
		if err != nil {
			return Resolver{}, fmt.Errorf("trusted proxy %q: not an address or CIDR", raw)
		}
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}

	return Resolver{trusted: prefixes}, nil
}

// From returns the address to hold the sender to.
//
// With no trusted proxies it is the connection's own address and the header is
// ignored. With them, it is the rightmost entry in X-Forwarded-For that is not
// itself one of ours: the chain is appended left to right, so everything to the
// left of our own proxies is whatever the client chose to send, and only the
// part our proxies added can be believed.
func (r Resolver) From(req *http.Request) string {
	remote := parse(req.RemoteAddr)

	// Nothing is trusted, or the connection is not from something we trust, so
	// the header is somebody's suggestion rather than evidence.
	if len(r.trusted) == 0 || !remote.IsValid() || !r.isTrusted(remote) {
		return stringOrUnknown(remote)
	}

	xff := req.Header.Get("X-Forwarded-For")
	if xff == "" {
		return stringOrUnknown(remote)
	}

	// Right to left: skip the hops we recognise as our own and stop at the
	// first one we do not.
	hops := strings.Split(xff, ",")
	for i := len(hops) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			// An entry that will not parse is not an address we can hold
			// anyone to, and everything left of it is behind it in a chain we
			// can no longer follow.
			break
		}
		if r.isTrusted(addr) {
			continue
		}
		return addr.Unmap().String()
	}

	// Every hop was one of ours, so the closest thing to a client we have is
	// whatever connected to us.
	return stringOrUnknown(remote)
}

func (r Resolver) isTrusted(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range r.trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// parse pulls an address out of a RemoteAddr, which usually carries a port and
// may bracket an IPv6 address.
func parse(remoteAddr string) netip.Addr {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		remoteAddr = host
	}
	addr, err := netip.ParseAddr(strings.Trim(remoteAddr, "[]"))
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

func stringOrUnknown(addr netip.Addr) string {
	if !addr.IsValid() {
		return Unknown
	}
	return addr.String()
}
