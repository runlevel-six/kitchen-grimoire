// SPDX-License-Identifier: Apache-2.0

package safefetch

import (
	"net/netip"
	"testing"
)

// deniedSamples has one representative address per denied range. It is
// shared with the dial-level tests in safefetch_test.go.
var deniedSamples = []struct{ name, addr string }{
	{"v4 loopback", "127.0.0.1"},
	{"v4 loopback high", "127.255.255.254"},
	{"v4 this-network", "0.0.0.0"},
	{"v4 this-network nonzero", "0.1.2.3"},
	{"rfc1918 10/8", "10.0.0.1"},
	{"rfc1918 172.16/12", "172.16.0.1"},
	{"rfc1918 192.168/16", "192.168.1.1"},
	{"cgnat", "100.64.0.1"},
	{"link-local v4", "169.254.1.1"},
	{"cloud metadata", "169.254.169.254"},
	{"v4 multicast", "224.0.0.1"},
	{"v4 reserved class E", "240.0.0.1"},
	{"v4 broadcast", "255.255.255.255"},
	{"v6 loopback", "::1"},
	{"v6 unspecified", "::"},
	{"v6 link-local", "fe80::1"},
	{"v6 link-local with zone", "fe80::1%eth0"},
	{"v6 ULA fc", "fc00::1"},
	{"v6 ULA fd", "fd00::1"},
	{"v6 site-local", "fec0::1"},
	{"v6 multicast", "ff02::1"},
	{"v6 multicast global", "ff0e::1"},
	{"mapped loopback", "::ffff:127.0.0.1"},
	{"mapped private", "::ffff:10.0.0.1"},
	{"mapped metadata", "::ffff:169.254.169.254"},
	{"mapped cgnat", "::ffff:100.64.0.1"},
	{"mapped unspecified", "::ffff:0.0.0.0"},
	{"v4-compatible loopback", "::127.0.0.1"},
	{"v4-compatible private", "::10.0.0.1"},
	{"nat64 loopback", "64:ff9b::7f00:1"},
	{"nat64 private", "64:ff9b::10.0.0.1"},
	{"nat64 local-use", "64:ff9b:1::8.8.8.8"},
	{"6to4 loopback", "2002:7f00:1::1"},
	{"6to4 private", "2002:c0a8:101::1"},
	{"siit translated", "::ffff:0:7f00:1"},
}

func TestDeniedAddr(t *testing.T) {
	for _, tc := range deniedSamples {
		t.Run(tc.name, func(t *testing.T) {
			if !deniedAddr(netip.MustParseAddr(tc.addr), nil) {
				t.Errorf("%s should be denied", tc.addr)
			}
		})
	}
	if !deniedAddr(netip.Addr{}, nil) {
		t.Error("the invalid zero Addr should be denied")
	}
}

func TestAllowedAddr(t *testing.T) {
	for _, a := range []string{
		"93.184.216.34",   // example.com
		"8.8.8.8",         // public DNS
		"1.1.1.1",         // public DNS
		"100.63.255.255",  // just below CGNAT
		"100.128.0.0",     // just above CGNAT
		"172.15.255.255",  // just below 172.16/12
		"172.32.0.0",      // just above 172.16/12
		"192.0.2.1",       // TEST-NET-1 documentation: allowed by design
		"198.18.0.1",      // benchmarking: allowed by design
		"223.255.255.255", // just below multicast
		"2606:4700::1111", // Cloudflare DNS
		"2001:4860:4860::8888",
		"2001:db8::1",          // documentation: allowed by design
		"::ffff:93.184.216.34", // mapped public
		"64:ff9b::808:808",     // NAT64 of 8.8.8.8
		"2002:808:808::1",      // 6to4 of 8.8.8.8
	} {
		if deniedAddr(netip.MustParseAddr(a), nil) {
			t.Errorf("%s should be allowed", a)
		}
	}
}

func TestExtraDeny(t *testing.T) {
	extra := normalizePrefixes([]netip.Prefix{
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("::ffff:198.51.100.0/120"), // mapped form; normalized to 198.51.100.0/24
		netip.MustParsePrefix("2001:db8:1::5/48"),        // unmasked; normalized to 2001:db8:1::/48
	})
	for _, tc := range []struct {
		addr string
		want bool
	}{
		{"203.0.113.9", true},
		{"::ffff:203.0.113.9", true},
		{"64:ff9b::203.0.113.9", true}, // embedded v4 is checked against extra too
		{"198.51.100.7", true},
		{"2001:db8:1::77", true},
		{"2001:db8:2::77", false},
		{"203.0.114.1", false},
	} {
		if got := deniedAddr(netip.MustParseAddr(tc.addr), extra); got != tc.want {
			t.Errorf("deniedAddr(%s) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}
