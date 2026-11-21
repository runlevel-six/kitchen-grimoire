// SPDX-License-Identifier: Apache-2.0

package safefetch

import "net/netip"

// Fixed IPv4 ranges we refuse to connect to that netip.Addr has no helper
// method for. The helpers (IsLoopback, IsPrivate, ...) cover the rest; see
// deniedAddr.
var deniedV4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),     // "this network"; 0.0.0.0 often means localhost to a kernel
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT shared address space (RFC 6598)
	netip.MustParsePrefix("240.0.0.0/4"),   // reserved "class E", includes broadcast 255.255.255.255
}

// Fixed IPv6 ranges with no netip helper.
var deniedV6 = []netip.Prefix{
	netip.MustParsePrefix("fec0::/10"),       // deprecated site-local; some stacks still route it locally
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64 (RFC 8215): translates into the local network
	netip.MustParsePrefix("::ffff:0:0:0/96"), // SIIT "IPv4-translated" (RFC 2765); unusual, refuse outright
}

// IPv6 ranges that embed an IPv4 address. A dial to one of these can end up
// at the embedded IPv4 address (via a NAT64 gateway, a 6to4 relay, or a
// kernel that still honors IPv4-compatible addresses), so we judge them by
// the embedded address. embedOffset is the byte offset of the 4-byte IPv4
// address inside the 16-byte IPv6 address.
var embeddingV6 = []struct {
	prefix      netip.Prefix
	embedOffset int
}{
	{netip.MustParsePrefix("64:ff9b::/96"), 12}, // NAT64 well-known prefix (RFC 6052)
	{netip.MustParsePrefix("::/96"), 12},        // deprecated IPv4-compatible (::a.b.c.d)
	{netip.MustParsePrefix("2002::/16"), 2},     // 6to4 (RFC 3056): 2002:AABB:CCDD::/48
}

// deniedAddr reports whether a connection to addr must be refused. extra
// holds additional operator-configured prefixes (for example the cluster's
// pod and service CIDRs); they should already be normalized by
// normalizePrefixes.
//
// Ranges that are merely "special" but harmless to us, such as the
// documentation and benchmarking ranges, are allowed: a request to them
// either fails or reaches the public internet, never something internal.
func deniedAddr(addr netip.Addr, extra []netip.Prefix) bool {
	if !addr.IsValid() {
		return true
	}
	// Drop any IPv6 zone ("%eth0") so prefix comparisons work, and turn
	// IPv4-mapped IPv6 (::ffff:10.0.0.1) into plain IPv4 (10.0.0.1) so a
	// mapped address gets exactly the same verdict as the IPv4 it maps.
	addr = addr.WithZone("").Unmap()

	// These helpers cover both families:
	//   IsLoopback:  127.0.0.0/8, ::1
	//   IsPrivate:   10/8, 172.16/12, 192.168/16 (RFC 1918), fc00::/7 (ULA)
	//   IsLinkLocalUnicast: 169.254/16 (includes cloud metadata 169.254.169.254), fe80::/10
	//   IsMulticast: 224/4, ff00::/8 (covers the link- and interface-local helpers too)
	//   IsUnspecified: 0.0.0.0, ::
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}

	if addr.Is4() {
		for _, p := range deniedV4 {
			if p.Contains(addr) {
				return true
			}
		}
	} else {
		for _, p := range deniedV6 {
			if p.Contains(addr) {
				return true
			}
		}
		for _, e := range embeddingV6 {
			if e.prefix.Contains(addr) {
				b := addr.As16()
				v4 := netip.AddrFrom4([4]byte(b[e.embedOffset : e.embedOffset+4]))
				if deniedAddr(v4, extra) {
					return true
				}
			}
		}
	}

	for _, p := range extra {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// normalizePrefixes masks each prefix and rewrites IPv4-mapped IPv6 prefixes
// (::ffff:10.0.0.0/104) as plain IPv4 (10.0.0.0/8), matching the
// normalization deniedAddr applies to addresses. Invalid prefixes are
// dropped.
func normalizePrefixes(in []netip.Prefix) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(in))
	for _, p := range in {
		if !p.IsValid() {
			continue
		}
		if a := p.Addr(); a.Is4In6() && p.Bits() >= 96 {
			p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
		}
		out = append(out, p.Masked())
	}
	return out
}
