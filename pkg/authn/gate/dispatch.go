package gate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vislake/speed/pkg/authn"
)

// maxPrefixLen is the longest account prefix a backend may declare. An account
// identifier is at most 64 bytes, and a prefix that leaves no room for a byte
// after it would be a namespace nobody could be named in.
const maxPrefixLen = 63

const staticName = "static"

type backend struct {
	// name is what Verifier.Name reported at admission. It is stamped on the
	// identity as Backend and is the key of every log line about this backend.
	name     string
	verifier authn.Verifier
	// schemes are the Authorization schemes the backend takes, folded to lower
	// case, without repeats, in ascending order.
	schemes []string
	// cookies are the cookie names the backend takes, in ascending order.
	cookies []string
	// accounts is the declared account prefix, empty for the library namespace.
	accounts string
	// impersonation and sso are the capabilities the backend declared.
	impersonation bool
	sso           bool
	// challenger and clearer are the optional interfaces the backend
	// implements, found once with a type assertion.
	challenger authn.Challenger
	clearer    authn.CookieClearer
}

// dispatch is the table that says which backend answers for which carrier and
// which account namespace each of them may hand identities back from. It is
// built in Init and read-only afterwards.
type dispatch struct {
	// backends is every admitted backend in ascending name order.
	backends []*backend
	byScheme map[string]*backend
	byCookie map[string]*backend
	// prefixed is the backends that declared a non-empty account prefix.
	prefixed []*backend
}

// buildDispatch admits the backends and builds the table, refusing the set when
// any backend or pair of them breaks an admission condition; the checks run in
// name order, so the failure does not depend on the registry's order.
func buildDispatch(verifiers []authn.Verifier) (*dispatch, error) {
	if len(verifiers) == 0 {
		return nil, fmt.Errorf("%w: this command includes the authentication gateway but not one "+
			"credential-verifying backend, so no credential could ever hold. Write the "+
			"chosen backend subpackage's module name into this command", authn.ErrNoVerifier)
	}
	admitted := make([]*backend, 0, len(verifiers))
	for _, v := range verifiers {
		b, err := admitBackend(v)
		if err != nil {
			return nil, err
		}
		admitted = append(admitted, b)
	}
	sort.SliceStable(admitted, func(i, j int) bool { return admitted[i].name < admitted[j].name })

	d := &dispatch{
		backends: admitted,
		byScheme: make(map[string]*backend),
		byCookie: make(map[string]*backend),
	}
	byName := make(map[string]*backend, len(admitted))
	for _, b := range admitted {
		if other, taken := byName[b.name]; taken {
			return nil, fmt.Errorf("%w: two backends report the name %q (%s and %s), and the name is "+
				"stamped on every identity and on every log line about a backend. Change one "+
				"of the two names", authn.ErrClaimConflict, b.name, describe(other), describe(b))
		}
		byName[b.name] = b
		for _, scheme := range b.schemes {
			if other, taken := d.byScheme[scheme]; taken {
				return nil, fmt.Errorf("%w: backends %q and %q both take the authentication scheme "+
					"%q, and a credential under it would have two answerers. Only one of them "+
					"can take it", authn.ErrClaimConflict, other.name, b.name, scheme)
			}
			d.byScheme[scheme] = b
		}
		for _, cookie := range b.cookies {
			if other, taken := d.byCookie[cookie]; taken {
				return nil, fmt.Errorf("%w: backends %q and %q both take the cookie %q, and a "+
					"credential in it would have two answerers. Only one of them can take it",
					authn.ErrClaimConflict, other.name, b.name, cookie)
			}
			d.byCookie[cookie] = b
		}
		if b.accounts != "" {
			d.prefixed = append(d.prefixed, b)
		}
	}
	// Two prefixes where one begins the other would let an identifier belong
	// to either namespace, and with it two backends' identities share a key.
	for i, a := range d.prefixed {
		for _, b := range d.prefixed[i+1:] {
			if strings.HasPrefix(a.accounts, b.accounts) || strings.HasPrefix(b.accounts, a.accounts) {
				return nil, fmt.Errorf("%w: the account prefixes %q of backend %q and %q of "+
					"backend %q nest or are equal, so an account identifier could belong to "+
					"either. Change one so that neither begins the other",
					authn.ErrClaimConflict, a.accounts, a.name, b.accounts, b.name)
			}
		}
	}
	return d, nil
}

func admitBackend(v authn.Verifier) (*backend, error) {
	if v == nil {
		return nil, fmt.Errorf("%w: a nil backend was delivered as a credential-verifying backend",
			authn.ErrInvalidClaim)
	}
	name := v.Name()
	if !validBackendName(name) {
		return nil, fmt.Errorf("%w: a backend reports the name %q, which is not "+
			"[a-z0-9][a-z0-9.-]*. The name is stamped on every identity the backend "+
			"authenticates and written into log lines", authn.ErrInvalidClaim, clip(name))
	}
	accepts := v.Accepts()
	b := &backend{
		name:          name,
		verifier:      v,
		accounts:      accepts.Accounts,
		impersonation: accepts.Impersonation,
		sso:           accepts.SSO,
	}
	if c, ok := v.(authn.Challenger); ok {
		b.challenger = c
	}
	if c, ok := v.(authn.CookieClearer); ok {
		b.clearer = c
	}

	seenScheme := make(map[string]struct{}, len(accepts.Schemes))
	for _, raw := range accepts.Schemes {
		scheme := strings.ToLower(strings.TrimSpace(raw))
		if !isToken(scheme) {
			return nil, fmt.Errorf("%w: backend %q declares the scheme %q, which is not an RFC 9110 "+
				"token", authn.ErrInvalidClaim, name, clip(raw))
		}
		if _, repeated := seenScheme[scheme]; repeated {
			continue
		}
		seenScheme[scheme] = struct{}{}
		b.schemes = append(b.schemes, scheme)
	}
	sort.Strings(b.schemes)

	seenCookie := make(map[string]struct{}, len(accepts.Cookies))
	for _, cookie := range accepts.Cookies {
		if !isToken(cookie) {
			return nil, fmt.Errorf("%w: backend %q declares the cookie name %q, which is not a "+
				"cookie name (an RFC 6265 token)", authn.ErrInvalidClaim, name, clip(cookie))
		}
		if _, repeated := seenCookie[cookie]; repeated {
			continue
		}
		seenCookie[cookie] = struct{}{}
		b.cookies = append(b.cookies, cookie)
	}
	sort.Strings(b.cookies)

	if len(b.schemes) == 0 && len(b.cookies) == 0 {
		return nil, fmt.Errorf("%w: backend %q declares no scheme and no cookie, so no credential "+
			"would ever reach it", authn.ErrInvalidClaim, name)
	}
	if err := checkPrefix(b); err != nil {
		return nil, err
	}
	return b, nil
}

// checkPrefix holds the account prefix to its own shape and to the reservation
// of authn.StaticPrefix: only the backend named static may declare it or any
// prefix overlapping it, and that backend answers for it and no other namespace.
func checkPrefix(b *backend) error {
	prefix := b.accounts
	if prefix != "" {
		if len(prefix) > maxPrefixLen {
			return fmt.Errorf("%w: backend %q declares an account prefix of %d bytes, and an "+
				"account identifier is at most 64", authn.ErrInvalidClaim, b.name, len(prefix))
		}
		if _, err := authn.ParseAccountID(prefix); err != nil {
			return fmt.Errorf("%w: backend %q declares an account prefix that is not spelt in "+
				"the characters an account identifier is", authn.ErrInvalidClaim, b.name)
		}
	}
	if b.name == staticName {
		if prefix != authn.StaticPrefix {
			return fmt.Errorf("%w: the backend named %q declares the account prefix %q, and it "+
				"answers for %q and no other namespace", authn.ErrInvalidClaim, staticName,
				prefix, authn.StaticPrefix)
		}
		return nil
	}
	if prefix == "" {
		return nil
	}
	if strings.HasPrefix(prefix, authn.StaticPrefix) || strings.HasPrefix(authn.StaticPrefix, prefix) {
		return fmt.Errorf("%w: backend %q declares the account prefix %q, which is or overlaps %q. "+
			"That namespace belongs to the backend named %q and to no other, because an "+
			"account in it is read as an operator", authn.ErrInvalidClaim, b.name, prefix,
			authn.StaticPrefix, staticName)
	}
	return nil
}

// inNamespace reports whether the backend may hand back this account: a declared
// prefix requires a longer identifier beginning with it, and the library
// namespace is any identifier beginning with no other backend's prefix.
func (d *dispatch) inNamespace(b *backend, id authn.AccountID) bool {
	s := string(id)
	if b.accounts != "" {
		return len(s) > len(b.accounts) && strings.HasPrefix(s, b.accounts)
	}
	if id.IsStatic() {
		return false
	}
	for _, other := range d.prefixed {
		if strings.HasPrefix(s, other.accounts) {
			return false
		}
	}
	return true
}

// schemes lists every scheme any backend takes, in ascending order.
func (d *dispatch) schemes() []string {
	out := make([]string, 0, len(d.byScheme))
	for scheme := range d.byScheme {
		out = append(out, scheme)
	}
	sort.Strings(out)
	return out
}

func describe(b *backend) string {
	var parts []string
	if len(b.schemes) > 0 {
		parts = append(parts, "schemes "+strings.Join(b.schemes, ", "))
	}
	if len(b.cookies) > 0 {
		parts = append(parts, "cookies "+strings.Join(b.cookies, ", "))
	}
	if b.accounts != "" {
		parts = append(parts, fmt.Sprintf("accounts %q", b.accounts))
	} else {
		parts = append(parts, "the library account namespace")
	}
	if b.impersonation {
		parts = append(parts, "impersonation")
	}
	if b.sso {
		parts = append(parts, "SSO")
	}
	return b.name + " (" + strings.Join(parts, "; ") + ")"
}

func validBackendName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case i > 0 && (c == '.' || c == '-'):
		default:
			return false
		}
	}
	return true
}

func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// clip shortens a value read from outside before it enters an error or a log
// line, so that a long or odd one cannot bloat or reshape a record.
func clip(s string) string {
	const limit = 64
	if len(s) > limit {
		s = s[:limit] + "..."
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s)
}
