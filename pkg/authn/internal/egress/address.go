package egress

import "net/netip"

// specialV4 holds the IPv4 blocks that are not public unicast. The blocks are
// those the IANA IPv4 Special-Purpose Address Registry marks not globally
// reachable, plus multicast.
var specialV4 = prefixes(
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.88.99.0/24",
	"192.168.0.0/16",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
)

// specialV6 holds the IPv6 blocks that are not public unicast and that the
// rules in [Permitted] do not already refuse. Several entries are redundant
// with the 2000::/3 requirement and are listed anyway, so the table reads as
// the registry does.
var specialV6 = prefixes(
	"::/128",
	"::1/128",
	"100::/64",
	"2001::/23",
	"2001:2::/48",
	"2001:db8::/32",
	"3fff::/20",
	"fc00::/7",
	"fe80::/10",
	"ff00::/8",
)

var (
	// globalUnicastV6 is the only block of IPv6 space allocated for global
	// unicast use.
	globalUnicastV6 = netip.MustParsePrefix("2000::/3")

	nat64WellKnown = netip.MustParsePrefix("64:ff9b::/96")

	nat64LocalUse = netip.MustParsePrefix("64:ff9b:1::/48")

	teredo = netip.MustParsePrefix("2001::/32")

	sixToFour = netip.MustParsePrefix("2002::/16")
)

func prefixes(blocks ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(blocks))
	for i, b := range blocks {
		out[i] = netip.MustParsePrefix(b)
	}
	return out
}

func within(addr netip.Addr, blocks []netip.Prefix) bool {
	for _, b := range blocks {
		if b.Contains(addr) {
			return true
		}
	}
	return false
}

// Permitted reports whether a connection to addr is allowed: whether it is a
// public unicast address. The zero Addr is not permitted.
//
// allowLoopback is the development switch. It admits 127.0.0.0/8 and ::1 (an
// IPv4-mapped loopback address included, since it is that IPv4 address) and
// nothing else; a loopback address carried inside a NAT64 or 6to4 address is
// still refused, because the packet leaves the machine.
func Permitted(addr netip.Addr, allowLoopback bool) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.WithZone("").Unmap()
	if addr.IsLoopback() {
		return allowLoopback
	}
	if addr.Is4() {
		return publicV4(addr)
	}

	switch {
	case teredo.Contains(addr), nat64LocalUse.Contains(addr):
		return false
	case nat64WellKnown.Contains(addr):
		b := addr.As16()
		return publicV4(netip.AddrFrom4([4]byte(b[12:16])))
	case sixToFour.Contains(addr):
		b := addr.As16()
		return publicV4(netip.AddrFrom4([4]byte(b[2:6])))
	}
	return addr.IsGlobalUnicast() && globalUnicastV6.Contains(addr) && !within(addr, specialV6)
}

// publicV4 judges an IPv4 address; the caller has already settled loopback.
func publicV4(addr netip.Addr) bool {
	return addr.IsGlobalUnicast() && !within(addr, specialV4)
}
