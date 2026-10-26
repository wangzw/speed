// Package egress builds the HTTP client every server-side request goes through
// when an outside party decides where it is sent: the URL a tenant
// administrator typed in, and the addresses a discovery document or a metadata
// file leads to. Such a request is the shape of server-side request forgery, so
// the client refuses to reach anything that is not a public unicast address.
//
// # What the client does
//
//   - Every connection attempt is judged on the address actually being
//     connected to, inside [net.Dialer].Control. A host name resolving to
//     several records, a record that changes between two lookups, and the new
//     connection a redirect would open are all judged the same way, because the
//     judgement happens after resolution and once per attempt rather than
//     before resolution and once per request. A refused attempt fails with
//     [ErrDestinationDenied].
//   - Only https is spoken. Any other scheme fails with [ErrSchemeNotHTTPS]
//     before a connection is opened.
//   - A redirect is not followed: the 3xx response comes back as it is and no
//     second request is made.
//   - A response body is capped at [MaxBodyBytes], whatever its status. Reading
//     past the cap fails with [ErrBodyTooLarge]; the body is never silently
//     truncated. The header block is capped at 64 KiB, and a response over it
//     fails the call.
//   - No proxy is used. Through a proxy the connection goes to the proxy, so the
//     address the judgement sees is the proxy's and says nothing about the
//     destination.
//   - Certificates are verified against the system roots, and verification is
//     never switched off. [Options].RootCAs replaces the roots for a deployment
//     whose identity provider uses a private certificate authority, or a test
//     that serves its own certificate; [RootCAsFromFile] reads a PEM file into
//     one. It is the only knob on verification.
//   - The client sets no total timeout. The deadline of each call is the
//     deadline of its context, so a caller must hand one in: a call whose
//     context carries none is bounded only by the handshake timeout and by the
//     peer. The name resolution and the connection attempts of a call end with
//     the call, at its deadline or when it is cancelled, and each attempt is
//     given an equal share of the time that is left, so that an unreachable
//     first address cannot spend the time the later ones need. net/http runs a
//     dial on a context detached from the call's, so the client carries the
//     call's context across itself. [http.Client.CloseIdleConnections] releases
//     the idle connections.
//
// The development switch [Options].AllowLoopback admits loopback addresses and
// nothing else.
//
// # Propagating the trace
//
// A caller that carries the trace across the hop wraps the client's Transport
// from outside: client.Transport = observability.Transport(client.Transport).
// The judgement of the address is in the dialer and the scheme and body checks
// are in the transport the wrapper delegates to, so the wrapper changes none of
// them, and the package offers no constructor of its own for it.
//
// # Which addresses are public
//
// [Permitted] states the rule. An IPv6 address wrapping an IPv4 one is first
// reduced to the IPv4 address: an IPv4-mapped address (::ffff:0:0/96) by
// unmapping, a NAT64 address in 64:ff9b::/96 (RFC 6052) by taking its last four
// bytes, and a 6to4 address in 2002::/16 (RFC 3056) by taking bytes 2 to 5. The
// IPv4 address is then judged as IPv4. Teredo (2001::/32) and the local-use
// NAT64 prefix 64:ff9b:1::/48 are refused outright, because the IPv4 address
// they carry is not the place the packet goes. Every other address must be a
// global unicast address inside 2000::/3 (IPv6) or a global unicast address
// (IPv4), and must not fall in a special-purpose block below.
//
// [netip.Addr.IsGlobalUnicast] alone is not enough: it answers true for the
// NAT64 and 6to4 forms of 169.254.169.254, for Teredo addresses, and for
// 100.64.0.0/10, 192.0.0.0/24, 198.18.0.0/15 and 240.0.0.0/4. It also answers
// true for every IPv6 address outside 2000::/3, the only block allocated for
// global unicast, except the multicast, link-local, loopback and unspecified
// ones (fec0::/10 and the IPv4-compatible ::/96 among them), which is why the
// IPv6 rule requires 2000::/3 as well.
//
// # The special-purpose blocks, checked against the registries
//
// The tables below were compared with the IANA IPv4 and IPv6 Special-Purpose
// Address Registries (retrieved 2026-10-11), entry by entry.
//
// IPv4: every block the registry marks not globally reachable is covered —
// 0.0.0.0/8 (and with it 0.0.0.0/32), 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8,
// 169.254.0.0/16, 172.16.0.0/12, 192.0.0.0/24, 192.0.2.0/24, 192.88.99.0/24
// (deprecated), 192.168.0.0/16, 198.18.0.0/15, 198.51.100.0/24, 203.0.113.0/24
// and 240.0.0.0/4 (and with it 255.255.255.255/32). 224.0.0.0/4 is multicast,
// which has a registry of its own, and is refused as well. The registry's
// globally reachable entries (192.31.196.0/24, 192.52.193.0/24,
// 192.175.48.0/24) stay reachable; the two globally reachable anycast entries
// inside 192.0.0.0/24 (192.0.0.9, 192.0.0.10) are refused with their parent
// block, which is the conservative reading.
//
// IPv6: ::1/128, ::/128, 64:ff9b:1::/48, 100::/64, 2001:2::/48, 2001:db8::/32,
// fc00::/7 and fe80::/10 are listed in the tables or fall outside 2000::/3;
// ::ffff:0:0/96 is handled by unmapping; 64:ff9b::/96, 2001::/32 and 2002::/16
// are handled by the embedding rules above; ff00::/8 is multicast and is
// refused. Four further registry entries are covered: 2001::/23 (IETF Protocol
// Assignments, not globally reachable as a block; it contains Teredo and the
// benchmarking block, and its globally reachable sub-entries are anycast and
// overlay prefixes no identity provider lives at), 3fff::/20 (documentation,
// RFC 9637), 100:0:0:1::/64 (dummy prefix, RFC 9780) and 5f00::/16 (SRv6 SIDs,
// RFC 9602), the last two being refused by the 2000::/3 requirement. The
// registry's globally reachable IPv6 entries outside 2001::/23
// (2620:4f:8000::/48) stay reachable.
//
// The package keeps no copy of the registries; the vectors in its tests are the
// record of what each block was checked for.
package egress
