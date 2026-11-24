package gate

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

func named(name string) *fakeVerifier {
	return &fakeVerifier{name: name, accepts: authn.Accepts{Schemes: []string{"s-" + name}}}
}

// TestADeclarationTheGatewayCannotHonourFailsTheStartup holds the shapes a
// backend is refused for on its own.
func TestADeclarationTheGatewayCannotHonourFailsTheStartup(t *testing.T) {
	for _, c := range []struct {
		name     string
		verifier authn.Verifier
		mentions string
	}{
		{"a backend reporting no name", named(""), "not [a-z0-9][a-z0-9.-]*"},
		{"a backend reporting a name of blanks", named("   "), "not [a-z0-9][a-z0-9.-]*"},
		{"a name in capitals", named("Local"), "not [a-z0-9][a-z0-9.-]*"},
		{"a name beginning with a dot", named(".local"), "not [a-z0-9][a-z0-9.-]*"},
		{"a name beginning with a dash", named("-local"), "not [a-z0-9][a-z0-9.-]*"},
		{"a name with a slash", named("corp/ldap"), "not [a-z0-9][a-z0-9.-]*"},
		{"a name with a space", named("corp ldap"), "not [a-z0-9][a-z0-9.-]*"},
		{"a name with an underscore", named("corp_ldap"), "not [a-z0-9][a-z0-9.-]*"},
		{"a name with a line ending", named("corp\nldap"), "not [a-z0-9][a-z0-9.-]*"},

		{"a scheme that is not a token", schemeBackend("local", "Bea rer"), "not an RFC 9110 token"},
		{"a scheme carrying a line ending", schemeBackend("local", "Bearer\r\nX-Evil: 1"), "not an RFC 9110 token"},
		{"a scheme that is empty", schemeBackend("local", ""), "not an RFC 9110 token"},
		{"a scheme of blanks", schemeBackend("local", "  "), "not an RFC 9110 token"},
		{"a scheme with a comma", schemeBackend("local", "a,b"), "not an RFC 9110 token"},
		{"a scheme with a quote", schemeBackend("local", `a"b`), "not an RFC 9110 token"},
		{"a cookie name with a space", cookieBackend("local", "my cookie"), "not a cookie name"},
		{"a cookie name with a semicolon", cookieBackend("local", "a;b"), "not a cookie name"},
		{"a cookie name with an equals sign", cookieBackend("local", "a=b"), "not a cookie name"},
		{"a cookie name that is empty", cookieBackend("local", ""), "not a cookie name"},
		{"a backend taking no carrier at all", &fakeVerifier{name: "local"}, "no scheme and no cookie"},
		{"a nil backend", nil, "nil backend"},

		{
			"an account prefix with a character outside the format",
			&fakeVerifier{name: "local", accepts: authn.Accepts{Schemes: []string{"x"}, Accounts: "lo cal."}},
			"not spelt in the characters",
		},
		{
			"an account prefix that leaves no room",
			&fakeVerifier{name: "local", accepts: authn.Accepts{Schemes: []string{"x"}, Accounts: strings.Repeat("a", 64)}},
			"at most 64",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := buildDispatch([]authn.Verifier{c.verifier})
			if !errors.Is(err, authn.ErrInvalidClaim) {
				t.Fatalf("building the table gave %v, want one wrapping %v", err, authn.ErrInvalidClaim)
			}
			if !strings.Contains(err.Error(), c.mentions) {
				t.Errorf("the failure reads %q, and does not say what is wrong with the declaration "+
					"(looked for %q)", err, c.mentions)
			}
		})
	}

	for _, ok := range []authn.Verifier{
		named("a"),
		named("0.9-x"),
		schemeBackend("local", "Bearer", "x-custom", "!#$%&'*+-.^_`|~09az"),
		cookieBackend("local", "sid", "__Host-session", "a.b-c_d"),
		&fakeVerifier{name: "local", accepts: authn.Accepts{Schemes: []string{"x"}, Accounts: strings.Repeat("a", 63)}},
	} {
		if _, err := buildDispatch([]authn.Verifier{ok}); err != nil {
			t.Errorf("a declaration the gateway honours was refused: %v", err)
		}
	}
}

// TestAGatewayWithNoBackendRefusesToStart holds the one thing a set of no
// backends is: a startup failure, never a gateway that refuses every request.
func TestAGatewayWithNoBackendRefusesToStart(t *testing.T) {
	_, err := buildDispatch(nil)
	if !errors.Is(err, authn.ErrNoVerifier) {
		t.Errorf("an empty set gave %v, want one wrapping %v", err, authn.ErrNoVerifier)
	}
}

// TestTwoBackendsTakingOneCarrierFailTheStartupNamingBoth holds the overlap
// the build is able to detect.
func TestTwoBackendsTakingOneCarrierFailTheStartupNamingBoth(t *testing.T) {
	for _, c := range []struct {
		name     string
		first    authn.Verifier
		second   authn.Verifier
		mentions []string
	}{
		{
			"two backends with one name",
			&fakeVerifier{name: "local", accepts: authn.Accepts{Schemes: []string{"a"}}},
			&fakeVerifier{name: "local", accepts: authn.Accepts{Schemes: []string{"b"}}},
			[]string{`"local"`},
		},
		{
			"one scheme taken twice",
			schemeBackend("one", "bearer"),
			schemeBackend("two", "bearer"),
			[]string{`"one"`, `"two"`, `"bearer"`},
		},
		{
			"one scheme in two spellings",
			schemeBackend("one", "Bearer"),
			schemeBackend("two", "BEARER"),
			[]string{`"one"`, `"two"`, `"bearer"`},
		},
		{
			"one cookie taken twice",
			cookieBackend("one", "sid"),
			cookieBackend("two", "sid"),
			[]string{`"one"`, `"two"`, `"sid"`},
		},
		{
			"two backends taking Basic",
			basicBackend("one", "one."),
			basicBackend("two", "two."),
			[]string{`"one"`, `"two"`, `"basic"`},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Both orders: the failure does not depend on which was listed first.
			for _, pair := range [][]authn.Verifier{{c.first, c.second}, {c.second, c.first}} {
				_, err := buildDispatch(pair)
				if !errors.Is(err, authn.ErrClaimConflict) {
					t.Fatalf("building the table gave %v, want one wrapping %v", err, authn.ErrClaimConflict)
				}
				for _, want := range c.mentions {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the failure reads %q, want it to name %s", err, want)
					}
				}
			}
		})
	}

	if _, err := buildDispatch([]authn.Verifier{schemeBackend("one", "sid"), cookieBackend("two", "sid")}); err != nil {
		t.Errorf("a scheme and a cookie of the same spelling were taken for one key: %v", err)
	}
	if _, err := buildDispatch([]authn.Verifier{cookieBackend("one", "sid"), cookieBackend("two", "SID")}); err != nil {
		t.Errorf("two cookie names that differ in case were taken for one: %v", err)
	}
}

// TestAccountPrefixesMayNotNestOrRepeat holds the namespaces to being disjoint:
// a prefix that begins another, or is the same, would let an identifier belong
// to either backend, and two backends' identities would then share one key.
func TestAccountPrefixesMayNotNestOrRepeat(t *testing.T) {
	prefixed := func(name, scheme, prefix string) *fakeVerifier {
		return &fakeVerifier{name: name, accepts: authn.Accepts{Schemes: []string{scheme}, Accounts: prefix}}
	}
	for _, c := range []struct {
		name   string
		a, b   string
		refuse bool
	}{
		{"the same prefix", "corp.", "corp.", true},
		{"one inside the other", "corp.", "corp.ldap.", true},
		{"the other way round", "corp.ldap.", "corp.", true},
		{"a shorter one without the dot", "co", "corp.", true},
		{"disjoint prefixes", "corp.", "ldap.", false},
		{"prefixes that share only a beginning", "corp.a.", "corp.b.", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, pair := range [][]authn.Verifier{
				{prefixed("one", "a", c.a), prefixed("two", "b", c.b)},
				{prefixed("two", "b", c.b), prefixed("one", "a", c.a)},
			} {
				_, err := buildDispatch(pair)
				if c.refuse && !errors.Is(err, authn.ErrClaimConflict) {
					t.Fatalf("the prefixes %q and %q gave %v, want one wrapping %v", c.a, c.b, err, authn.ErrClaimConflict)
				}
				if !c.refuse && err != nil {
					t.Fatalf("the prefixes %q and %q were refused: %v", c.a, c.b, err)
				}
				if c.refuse {
					for _, want := range []string{`"one"`, `"two"`, c.a, c.b} {
						if !strings.Contains(err.Error(), want) {
							t.Errorf("the failure reads %q, want it to name %s", err, want)
						}
					}
				}
			}
		})
	}

	if _, err := buildDispatch([]authn.Verifier{schemeBackend("one", "a"), schemeBackend("two", "b")}); err != nil {
		t.Errorf("two backends in the library namespace were refused: %v", err)
	}
}

// TestTheStaticPrefixBelongsToTheBackendNamedStatic holds the reservation the
// whole namespace check leans on: the prefix an operator is recognised by is
// declared by the backend named static and by no other, nor by any prefix that
// could hand out an identifier under it.
func TestTheStaticPrefixBelongsToTheBackendNamedStatic(t *testing.T) {
	with := func(name, prefix string) authn.Verifier {
		return &fakeVerifier{name: name, accepts: authn.Accepts{Schemes: []string{"basic"}, Accounts: prefix}}
	}

	if _, err := buildDispatch([]authn.Verifier{with("static", "static.")}); err != nil {
		t.Fatalf("the backend named static was refused its own prefix: %v", err)
	}

	for _, c := range []struct {
		name     string
		verifier authn.Verifier
	}{
		{"another backend declaring the static prefix", with("corp", "static.")},
		{"another backend declaring a prefix under it", with("corp", "static.ops.")},
		{"another backend declaring a prefix that begins it", with("corp", "st")},
		{"another backend declaring a single letter of it", with("corp", "s")},
		{"the backend named static declaring no prefix", with("static", "")},
		{"the backend named static declaring another prefix", with("static", "corp.")},
		{"the backend named static declaring a prefix under its own", with("static", "static.ops.")},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := buildDispatch([]authn.Verifier{c.verifier}); !errors.Is(err, authn.ErrInvalidClaim) {
				t.Errorf("building the table gave %v, want one wrapping %v", err, authn.ErrInvalidClaim)
			}
		})
	}

	// Prefixes that only look like it are fine when no identifier under them can
	// begin with the static prefix, and refused when one can: "static" without
	// its dot hands out static.ops, "statics." and "Static." and "stat." do not.
	for _, c := range []struct {
		prefix string
		ok     bool
	}{
		{"statics.", true},
		{"Static.", true},
		{"stat.", true},
		{"static", false},
		{"stati", false},
		{"staticx.", true},
	} {
		_, err := buildDispatch([]authn.Verifier{with("corp", c.prefix)})
		if c.ok && err != nil {
			t.Errorf("the prefix %q was refused: %v", c.prefix, err)
		}
		if !c.ok && !errors.Is(err, authn.ErrInvalidClaim) {
			t.Errorf("the prefix %q gave %v, want one wrapping %v: it can hand out an identifier "+
				"under the static prefix", c.prefix, err, authn.ErrInvalidClaim)
		}
	}
}

// TestTheTableIsKeyedByFoldedSchemesAndExactCookies holds what the request path
// looks up: schemes folded to lower case and without repeats, cookies as they
// were declared, and a backend that declared a scheme in capitals is found by
// the folded one.
func TestTheTableIsKeyedByFoldedSchemesAndExactCookies(t *testing.T) {
	both := &fakeVerifier{
		name:    "both",
		accepts: authn.Accepts{Schemes: []string{"Bearer", " BEARER ", "Token"}, Cookies: []string{"sid", "sid", "Other"}},
	}
	d, err := buildDispatch([]authn.Verifier{both})
	if err != nil {
		t.Fatal(err)
	}

	if got := d.byScheme["bearer"]; got == nil || got.name != "both" {
		t.Errorf("the scheme bearer is held by %v, want the backend", got)
	}
	if _, found := d.byScheme["Bearer"]; found {
		t.Error("the table holds the scheme in its declared capitals, which the request path never looks up")
	}
	if got := d.byCookie["Other"]; got == nil {
		t.Error("the table does not hold the cookie as it was declared")
	}
	if _, found := d.byCookie["other"]; found {
		t.Error("the table holds a cookie folded, but cookie names are case-sensitive")
	}
	if b := d.backends[0]; len(b.schemes) != 2 || b.schemes[0] != "bearer" || b.schemes[1] != "token" ||
		len(b.cookies) != 2 || b.cookies[0] != "Other" || b.cookies[1] != "sid" {
		t.Errorf("the backend's declarations read %v and %v, want them folded, deduplicated and in order",
			b.schemes, b.cookies)
	}
	if got := d.schemes(); len(got) != 2 || got[0] != "bearer" || got[1] != "token" {
		t.Errorf("the schemes in play read %v", got)
	}
}

// TestABackendIsFoundByTheOptionalInterfacesItImplements holds the type
// assertions Init makes once.
func TestABackendIsFoundByTheOptionalInterfacesItImplements(t *testing.T) {
	plain := schemeBackend("plain", "a")
	challenging := challengingVerifier{
		fakeVerifier: schemeBackend("challenging", "b"),
		challenge:    func(string, bool) []string { return nil },
	}
	var cleared atomic.Int64
	clearing := clearingVerifier{fakeVerifier: cookieBackend("clearing", "sid"), cleared: &cleared}
	d, err := buildDispatch([]authn.Verifier{plain, challenging, clearing})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range d.backends {
		switch b.name {
		case "plain":
			if b.challenger != nil || b.clearer != nil {
				t.Error("a plain backend was found to implement an optional interface")
			}
		case "challenging":
			if b.challenger == nil || b.clearer != nil {
				t.Error("a backend that implements Challenger was not found as one, or was found as a clearer")
			}
		case "clearing":
			if b.clearer == nil || b.challenger != nil {
				t.Error("a backend that implements CookieClearer was not found as one, or was found as a challenger")
			}
		}
	}
}

// TestTheNamespaceCheckHoldsEachBackendToItsOwnNamespace holds the check from
// both sides for the three kinds of namespace there are: a declared prefix, the
// library namespace, and the static prefix.
func TestTheNamespaceCheckHoldsEachBackendToItsOwnNamespace(t *testing.T) {
	corp := &fakeVerifier{name: "corp", accepts: authn.Accepts{Schemes: []string{"a"}, Accounts: "corp."}}
	static := &fakeVerifier{name: "static", accepts: authn.Accepts{Schemes: []string{"basic"}, Accounts: "static."}}
	library := &fakeVerifier{name: "library", accepts: authn.Accepts{Schemes: []string{"b"}}}
	d, err := buildDispatch([]authn.Verifier{corp, static, library})
	if err != nil {
		t.Fatal(err)
	}
	of := func(name string) *backend {
		for _, b := range d.backends {
			if b.name == name {
				return b
			}
		}
		t.Fatalf("no backend %q", name)
		return nil
	}

	for _, c := range []struct {
		backend string
		account authn.AccountID
		inside  bool
	}{
		{"corp", "corp.alice", true},
		{"corp", "corp.a", true},
		{"corp", "corp.", false}, // the namespace and nobody after it
		{"corp", "corpx", false},
		{"corp", "corporate.alice", false},
		{"corp", "alice", false},
		{"corp", "static.ops", false},
		{"corp", "Corp.alice", false},

		{"static", "static.ops", true},
		{"static", "static.", false},
		{"static", "Static.ops", false},
		{"static", "staticops", false},
		{"static", "corp.alice", false},
		{"static", "alice", false},

		{"library", "0198f0aa-0000-7000-8000-000000000000", true},
		{"library", "alice", true},
		{"library", "Static.ops", true},
		{"library", "static.ops", false},
		{"library", "static.", false},
		{"library", "corp.alice", false},
		{"library", "corp.", false},
		{"library", "corporate", true},
	} {
		if got := d.inNamespace(of(c.backend), c.account); got != c.inside {
			t.Errorf("the account %q for the backend %s: inside is %v, want %v", c.account, c.backend, got, c.inside)
		}
	}

	// With no static backend in the assembly, the library namespace still
	// excludes the static prefix: an operator's identifier is never one the
	// library can hand out, whichever backends happen to be installed.
	alone, err := buildDispatch([]authn.Verifier{library})
	if err != nil {
		t.Fatal(err)
	}
	if alone.inNamespace(alone.backends[0], "static.ops") {
		t.Error("with no static backend present, the library namespace was allowed to hand out a static account")
	}
}
