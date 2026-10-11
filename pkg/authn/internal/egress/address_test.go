package egress

import (
	"net/netip"
	"testing"
)

type vector struct {
	addr string
	want bool
	why  string
}

// The public vectors guard against a rule that refuses too much; the refused
// ones against a rule that refuses too little. The ones a plain
// IsGlobalUnicast check gets wrong are listed in naiveWrong.
var addressVectors = []vector{
	// IPv4 special-purpose blocks, each refused.
	{"127.0.0.1", false, "loopback"},
	{"127.255.255.255", false, "loopback, top of the block"},
	{"10.0.0.1", false, "private 10/8"},
	{"10.255.255.255", false, "private 10/8, top"},
	{"172.16.0.1", false, "private 172.16/12"},
	{"172.31.255.255", false, "private 172.16/12, top"},
	{"192.168.0.1", false, "private 192.168/16"},
	{"169.254.169.254", false, "link-local, cloud metadata"},
	{"100.64.0.1", false, "shared address space"},
	{"100.127.255.255", false, "shared address space, top"},
	{"198.18.0.1", false, "benchmarking"},
	{"198.19.255.255", false, "benchmarking, top"},
	{"240.0.0.1", false, "reserved"},
	{"247.255.255.255", false, "reserved, middle"},
	{"248.0.0.1", false, "reserved, upper half"},
	{"255.255.255.254", false, "reserved, top"},
	{"192.0.0.1", false, "IETF protocol assignments"},
	{"192.0.0.9", false, "globally reachable anycast inside 192.0.0.0/24, refused with its block"},
	{"192.0.2.1", false, "TEST-NET-1"},
	{"192.88.99.1", false, "deprecated 6to4 relay anycast"},
	{"198.51.100.1", false, "TEST-NET-2"},
	{"203.0.113.1", false, "TEST-NET-3"},
	{"224.0.0.1", false, "multicast"},
	{"239.255.255.255", false, "multicast, top"},
	{"0.0.0.0", false, "unspecified"},
	{"0.255.255.255", false, "this network, top"},
	{"255.255.255.255", false, "limited broadcast"},

	// IPv4 just outside each block, accepted.
	{"8.8.8.8", true, "public"},
	{"1.0.0.1", true, "public"},
	{"1.0.0.0", true, "first address past this-network"},
	{"11.0.0.1", true, "past 10/8"},
	{"9.255.255.255", true, "before 10/8"},
	{"172.15.255.255", true, "before 172.16/12"},
	{"172.32.0.1", true, "past 172.16/12"},
	{"192.167.255.255", true, "before 192.168/16"},
	{"192.169.0.1", true, "past 192.168/16"},
	{"169.253.255.255", true, "before link-local"},
	{"169.255.0.1", true, "past link-local"},
	{"100.63.255.255", true, "before shared address space"},
	{"100.128.0.1", true, "past shared address space"},
	{"198.17.255.255", true, "before benchmarking"},
	{"198.20.0.1", true, "past benchmarking"},
	{"192.0.1.1", true, "past 192.0.0.0/24"},
	{"192.31.196.1", true, "AS112-v4, registry marks it globally reachable"},
	{"192.88.98.255", true, "before 6to4 relay block"},
	{"192.88.100.1", true, "past 6to4 relay block"},
	{"198.51.99.255", true, "before TEST-NET-2"},
	{"198.51.101.1", true, "past TEST-NET-2"},
	{"203.0.112.255", true, "before TEST-NET-3"},
	{"203.0.114.1", true, "past TEST-NET-3"},
	{"223.255.255.255", true, "below multicast"},

	// IPv6 special-purpose blocks, each refused.
	{"::", false, "unspecified"},
	{"::1", false, "loopback"},
	{"100::1", false, "discard-only"},
	{"100:0:0:1::1", false, "dummy prefix"},
	{"2001:2::1", false, "benchmarking"},
	{"2001:db8::1", false, "documentation"},
	{"2001:db8:ffff:ffff:ffff:ffff:ffff:ffff", false, "documentation, top"},
	{"3fff::1", false, "documentation, RFC 9637"},
	{"3fff:fff:ffff:ffff:ffff:ffff:ffff:ffff", false, "documentation RFC 9637, top"},
	{"fc00::1", false, "unique local"},
	{"fdff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", false, "unique local, top"},
	{"fe80::1", false, "link-local"},
	{"fe80::1%eth0", false, "link-local with a zone"},
	{"febf::1", false, "link-local, top"},
	{"ff02::1", false, "multicast"},
	{"fec0::1", false, "deprecated site-local, outside 2000::/3"},
	{"5f00::1", false, "SRv6 SIDs"},
	{"::7f00:1", false, "IPv4-compatible form of 127.0.0.1"},
	{"::808:808", false, "IPv4-compatible form of 8.8.8.8, outside 2000::/3"},
	{"::ffff:0:7f00:1", false, "IPv4-translated form, outside 2000::/3"},
	{"4000::1", false, "above 2000::/3"},
	{"1fff:ffff::1", false, "below 2000::/3"},
	{"2001:1::1", false, "inside 2001::/23, PCP anycast refused with its block"},
	{"2001:1ff:ffff::1", false, "inside 2001::/23, top"},

	// IPv6 accepted.
	{"2606:4700:4700::1111", true, "public"},
	{"2001:4860:4860::8888", true, "public"},
	{"2000::1", true, "bottom of 2000::/3"},
	{"3fff:1000::1", true, "past RFC 9637 documentation"},
	{"3ffe::1", true, "before RFC 9637 documentation"},
	{"2001:200::1", true, "past 2001::/23"},
	{"2001:db7:ffff::1", true, "before documentation"},
	{"2001:db9::1", true, "past documentation"},
	{"2003::1", true, "past 6to4"},
	{"2001:ffff::1", true, "far from the IETF block"},

	// IPv4-mapped IPv6 is judged as the IPv4 address.
	{"::ffff:127.0.0.1", false, "mapped loopback"},
	{"::ffff:10.1.2.3", false, "mapped private"},
	{"::ffff:169.254.169.254", false, "mapped metadata address"},
	{"::ffff:8.8.8.8", true, "mapped public"},

	// NAT64 well-known prefix: the IPv4 address in the last four bytes decides.
	{"64:ff9b::a9fe:a9fe", false, "embeds 169.254.169.254"},
	{"64:ff9b::a01:203", false, "embeds 10.1.2.3"},
	{"64:ff9b::7f00:1", false, "embeds 127.0.0.1"},
	{"64:ff9b::808:808", true, "embeds 8.8.8.8"},
	{"64:ff9b::1:0:0", false, "outside the /96, no embedded address"},
	{"64:ff9b::", false, "embeds 0.0.0.0"},

	// NAT64 local-use prefix: refused whatever it embeds.
	{"64:ff9b:1::", false, "local-use NAT64"},
	{"64:ff9b:1::808:808", false, "local-use NAT64 embedding a public address"},
	{"64:ff9b:1:ffff:ffff:ffff:ffff:ffff", false, "local-use NAT64, top"},
	{"64:ff9b:2::1", false, "outside 2000::/3, past the local-use prefix"},

	// 6to4: bytes 2 to 5 decide, not the last four.
	{"2002:a9fe:a9fe::1", false, "embeds 169.254.169.254"},
	{"2002:a01:203::808:808", false, "embeds 10.1.2.3 while the last four bytes are public"},
	{"2002:7f00:1::1", false, "embeds 127.0.0.1"},
	{"2002:808:808::1", true, "embeds 8.8.8.8"},
	{"2002:808:808::a01:203", true, "embeds 8.8.8.8 while the last four bytes are private"},

	// Teredo: refused whatever it embeds.
	{"2001:0:4136:e378:8000:63bf:3fff:fdd2", false, "Teredo"},
	{"2001::1", false, "Teredo, bottom"},
	{"2001:0:ffff:ffff:ffff:ffff:ffff:ffff", false, "Teredo, top"},
}

// naiveWrong lists the vectors on which a check of IsGlobalUnicast alone
// accepts an address that must be refused.
var naiveWrong = []string{
	"64:ff9b::a9fe:a9fe",
	"2002:a9fe:a9fe::1",
	"2001:0:4136:e378:8000:63bf:3fff:fdd2",
	"198.18.0.1",
	"240.0.0.1",
	"192.0.0.1",
	"100.64.0.1",
	"fec0::1",
	"::7f00:1",
	"3fff::1",
}

func TestPermittedAgainstTheAddressTable(t *testing.T) {
	for _, v := range addressVectors {
		t.Run(v.addr, func(t *testing.T) {
			addr, err := netip.ParseAddr(v.addr)
			if err != nil {
				t.Fatalf("vector %q does not parse: %v", v.addr, err)
			}
			if got := Permitted(addr, false); got != v.want {
				t.Errorf("Permitted(%s) = %v, want %v (%s)", v.addr, got, v.want, v.why)
			}
		})
	}
}

func TestAPlainGlobalUnicastCheckAcceptsWhatPermittedRefuses(t *testing.T) {
	for _, s := range naiveWrong {
		addr := netip.MustParseAddr(s)
		if !addr.IsGlobalUnicast() {
			t.Errorf("%s: IsGlobalUnicast is false, so this vector no longer separates the rule from the plain check", s)
		}
		if Permitted(addr, false) {
			t.Errorf("Permitted(%s) = true, want false", s)
		}
	}
}

func TestTheZeroAddressIsNotPermitted(t *testing.T) {
	for _, loop := range []bool{false, true} {
		if Permitted(netip.Addr{}, loop) {
			t.Errorf("Permitted(zero, %v) = true, want false", loop)
		}
	}
}

func TestTheLoopbackSwitchAdmitsLoopbackAndNothingElse(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.255.255.254", true},
		{"::1", true},
		{"::ffff:127.0.0.1", true},
		// Loopback wrapped in a translation prefix leaves the machine.
		{"64:ff9b::7f00:1", false},
		{"2002:7f00:1::1", false},
		// Every other refused address stays refused.
		{"10.0.0.1", false},
		{"169.254.169.254", false},
		{"192.168.1.1", false},
		{"0.0.0.0", false},
		{"::", false},
		{"fe80::1", false},
		{"fc00::1", false},
		{"2001:db8::1", false},
		{"64:ff9b::a9fe:a9fe", false},
		{"2001:0:4136:e378:8000:63bf:3fff:fdd2", false},
		// And a public address stays accepted.
		{"8.8.8.8", true},
		{"2606:4700:4700::1111", true},
	}
	for _, c := range cases {
		if got := Permitted(netip.MustParseAddr(c.addr), true); got != c.want {
			t.Errorf("Permitted(%s, allowLoopback) = %v, want %v", c.addr, got, c.want)
		}
	}
}

// TestTheEdgesOfTheSpecialPurposeBlocks holds the top and bottom edge of blocks
// the vectors elsewhere only reach in the middle.
func TestTheEdgesOfTheSpecialPurposeBlocks(t *testing.T) {
	for _, v := range []vector{
		{"192.168.255.255", false, "top of 192.168/16"},
		{"192.168.128.0", false, "start of the upper half of 192.168/16"},
		{"192.0.0.255", false, "top of 192.0.0.0/24"},
		{"192.0.2.255", false, "top of TEST-NET-1"},
		{"192.0.3.1", true, "past TEST-NET-1"},
		{"192.88.99.255", false, "top of the 6to4 relay block"},
		{"198.51.100.255", false, "top of TEST-NET-2"},
		{"203.0.113.255", false, "top of TEST-NET-3"},
		{"126.255.255.255", true, "just below loopback"},
		{"128.0.0.1", true, "just above loopback"},
		{"64:ff9b::1:808:808", false, "past the NAT64 /96, public bytes at the end"},
		{"64:ff9b::8efa:b8a4", true, "NAT64 embedding 142.250.184.164, high half of the /96"},
		{"2606:4700:4700::1111%eth0", true, "public with a zone"},
	} {
		addr := netip.MustParseAddr(v.addr)
		if got := Permitted(addr, false); got != v.want {
			t.Errorf("Permitted(%s) = %v, want %v (%s)", v.addr, got, v.want, v.why)
		}
	}
}

// TestTheLoopbackSwitchChangesNothingButLoopback runs the whole vector table with
// the switch on and expects loopback to be the only difference.
func TestTheLoopbackSwitchChangesNothingButLoopback(t *testing.T) {
	for _, v := range addressVectors {
		addr := netip.MustParseAddr(v.addr)
		want := v.want || addr.WithZone("").IsLoopback()
		if got := Permitted(addr, true); got != want {
			t.Errorf("Permitted(%s, loopback) = %v, want %v (%s)", v.addr, got, want, v.why)
		}
	}
}
