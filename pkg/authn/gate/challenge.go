package gate

import (
	"sort"
	"strings"

	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// cookieChallengeScheme is the scheme name a 401 carries when no backend takes
// an Authorization scheme, so the credentials in the assembly all travel in
// cookies. RFC 9110 requires a 401 to carry at least one challenge. The name is
// not in the IANA registry of authentication schemes, and no standard client
// knows it: it is a convention that satisfies the requirement, and a browser
// does not open a password prompt for a scheme other than Basic and Digest.
const cookieChallengeScheme = "Cookie"

// challenge is the WWW-Authenticate value the 401s on one endpoint carry. It is
// built once per endpoint in Init and read-only afterwards.
type challenge struct {
	// base is the value for a 401 on a request that carried no credential a
	// backend took, or one that was not refused on a backend's account.
	base string
	// rejected is the value for a 401 on a request whose credential reached the
	// backend and was refused, for the backends that write their own challenge:
	// the backend's part is built again with rejected set, which is where a
	// token backend adds error="invalid_token". A backend without an entry
	// answers with base.
	rejected map[*backend]string
}

// newChallenge builds the challenge of one endpoint.
//
// The schemes are the ones backends take whole, each written the way a
// challenge writes a scheme, and a backend that implements Challenger writes its
// own part instead. The realm is the endpoint's name: a realm is the name of a
// protection space, which is what an endpoint is here, since what a gateway
// guards is named per endpoint and a caller's credentials are worth exactly as
// much as the endpoint they were issued for. Inventing a word instead would put
// a name of this module's choosing into every client that keeps credentials per
// realm. It is escaped before it is handed to a backend or written, so a quote
// or a backslash in an endpoint name cannot end the quoted string early.
//
// With no challenge to write, which is the case when no backend takes a scheme,
// the value is the cookie challenge, so a 401 never goes out bare.
func newChallenge(d *dispatch, endpoint string) *challenge {
	realm := mount.QuoteRealm(endpoint)
	parts := make(map[*backend][]string, len(d.backends))
	rejectedParts := make(map[*backend][]string)
	for _, b := range d.backends {
		parts[b] = backendChallenge(b, realm, false)
		if b.challenger != nil {
			rejectedParts[b] = backendChallenge(b, realm, true)
		}
	}
	join := func(override *backend, replacement []string) string {
		var all []string
		for _, b := range d.backends {
			if b == override {
				all = append(all, replacement...)
				continue
			}
			all = append(all, parts[b]...)
		}
		if len(all) == 0 {
			all = append(all, cookieChallengeScheme+` realm="`+realm+`"`)
		}
		return joinChallenges(all)
	}

	c := &challenge{base: join(nil, nil), rejected: make(map[*backend]string, len(rejectedParts))}
	for b, replacement := range rejectedParts {
		c.rejected[b] = join(b, replacement)
	}
	return c
}

// valueFor is the value a 401 is written with, given the backend whose
// credential was refused, or nil when none was.
func (c *challenge) valueFor(refused *backend) string {
	if refused != nil {
		if v, ok := c.rejected[refused]; ok {
			return v
		}
	}
	return c.base
}

// backendChallenge is one backend's part of the challenge: what it wrote itself,
// or one challenge per scheme it takes.
func backendChallenge(b *backend, realm string, rejected bool) []string {
	if b.challenger != nil {
		given := b.challenger.Challenge(realm, rejected)
		out := make([]string, 0, len(given))
		for _, v := range given {
			// A line ending would not be part of a value but the start of a
			// second header, so a value carrying one is dropped.
			if v = strings.TrimSpace(v); v != "" && !strings.ContainsAny(v, "\r\n") {
				out = append(out, v)
			}
		}
		return out
	}
	out := make([]string, 0, len(b.schemes))
	for _, scheme := range b.schemes {
		out = append(out, canonicalScheme(scheme)+` realm="`+realm+`"`)
	}
	return out
}

// joinChallenges writes the challenges as one field value: without repeats, in
// ascending order, separated by a comma.
func joinChallenges(all []string) string {
	seen := make(map[string]struct{}, len(all))
	out := make([]string, 0, len(all))
	for _, v := range all {
		if _, repeated := seen[v]; repeated {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// canonicalScheme renders a folded scheme name the way a challenge writes it.
// Scheme names are case-insensitive, so this is presentation alone.
func canonicalScheme(scheme string) string {
	if scheme == "" {
		return scheme
	}
	return strings.ToUpper(scheme[:1]) + scheme[1:]
}
