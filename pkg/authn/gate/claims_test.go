package gate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestAClaimTheGatewayCannotHonourFailsTheStartup holds the shapes a claim is
// refused for.
//
// Each of these would otherwise become a request-time surprise: a nameless
// backend cannot be written into an owner qualification, a subject carrying the
// separator cannot be told from a qualification, an open claim with nothing to
// ask is a claim that answers nothing, and a backend claiming no subject space
// at all can never be reached however the table is walked.
func TestAClaimTheGatewayCannotHonourFailsTheStartup(t *testing.T) {
	for _, c := range []struct {
		name     string
		verifier authn.Verifier
		mentions string
	}{
		{
			name:     "a backend reporting no name",
			verifier: enumerating("", "alice"),
			mentions: "nameless",
		},
		{
			name:     "a backend reporting a name of blanks",
			verifier: enumerating("   ", "alice"),
			mentions: "nameless",
		},
		{
			name:     "an enumerated subject that is empty",
			verifier: enumerating("local", ""),
			mentions: "names nobody",
		},
		{
			name:     "an enumerated subject of blanks alone",
			verifier: enumerating("local", "  "),
			mentions: "names nobody",
		},
		{
			name:     "an enumerated subject carrying the separator",
			verifier: enumerating("local", "other/alice"),
			mentions: authn.Separator,
		},
		{
			// The name is the other half of every identity this
			// backend authenticates, and the two halves are split on
			// the first separator. corp/ldap owning alice writes
			// corp/ldap/alice, which reads back as ldap/alice owned
			// by corp — and the qualified form a caller would write
			// resolves to no backend at all, with the gateway
			// reporting that nobody reports that name while the
			// backend is standing right there.
			name:     "a backend name carrying the separator",
			verifier: enumerating("corp/ldap", "alice"),
			mentions: authn.Separator,
		},
		{
			// A scheme name is written into the challenge every
			// refusal carries, so it leaves the process. Anything
			// outside a token reaches a caller as a challenge that
			// cannot be parsed.
			name:     "a claimed scheme that is not a token",
			verifier: claimingScheme("local", "Bea rer"),
			mentions: "not a token",
		},
		{
			name:     "a claimed scheme carrying a line ending",
			verifier: claimingScheme("local", "Bearer\r\nX-Evil: 1"),
			mentions: "not a token",
		},
		{
			name:     "a claimed scheme that is empty",
			verifier: claimingScheme("local", ""),
			mentions: "matches no credential",
		},
		{
			name:     "an open claim from a backend that cannot answer ownership",
			verifier: &fakeVerifier{name: "local", claim: authn.Claim{Open: true}},
			mentions: "does not implement authn.Owner",
		},
		{
			name:     "a backend claiming no subject space at all",
			verifier: &fakeVerifier{name: "local"},
			mentions: "claims no subject space",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := buildOwnership([]authn.Verifier{c.verifier})
			if !errors.Is(err, authn.ErrInvalidClaim) {
				t.Fatalf("building the table gave %v, want one wrapping %v",
					err, authn.ErrInvalidClaim)
			}
			if !strings.Contains(err.Error(), c.mentions) {
				t.Errorf("the failure reads %q, and does not say what is wrong with the "+
					"claim (looked for %q)", err, c.mentions)
			}
		})
	}
}

// TestTwoBackendsOwningOneExactKeyFailTheStartupNamingBoth holds the overlap
// the build is able to detect.
//
// An exact key with two authorities has no answer at request time: whichever
// backend were asked, the other's claim would be quietly ignored, and which one
// that is would turn on registration order. The diagnostic has to name both,
// because the fix is to change one of the two and neither is the wrong one on
// its own.
func TestTwoBackendsOwningOneExactKeyFailTheStartupNamingBoth(t *testing.T) {
	for _, c := range []struct {
		name      string
		verifiers []authn.Verifier
		collision string
	}{
		{
			name: "one authentication scheme claimed whole by both",
			verifiers: []authn.Verifier{
				claimingScheme("first", "Bearer"),
				claimingScheme("second", "bearer"),
			},
			collision: "bearer",
		},
		{
			name: "one subject enumerated by both",
			verifiers: []authn.Verifier{
				enumerating("first", "alice"),
				enumerating("second", "alice"),
			},
			collision: "alice",
		},
		{
			name: "one name reported by both",
			verifiers: []authn.Verifier{
				enumerating("local", "alice"),
				enumerating("local", "bob"),
			},
			collision: "local",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := buildOwnership(c.verifiers)
			if !errors.Is(err, authn.ErrClaimConflict) {
				t.Fatalf("building the table gave %v, want one wrapping %v",
					err, authn.ErrClaimConflict)
			}
			for _, name := range []string{c.verifiers[0].Name(), c.verifiers[1].Name()} {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("the failure reads %q and does not name the backend %q; the fix "+
						"is to change one of the two claims, so both have to be named",
						err, name)
				}
			}
			if !strings.Contains(err.Error(), c.collision) {
				t.Errorf("the failure reads %q and does not name the item they collided on (%q)",
					err, c.collision)
			}
		})
	}
}

// TestAClaimedSchemeIsOneSchemeWhateverItsCase holds the folding that decides
// whether two claims collide at all.
//
// Authentication scheme names are case-insensitive in the protocol, so a
// gateway that indexed them literally would let "Bearer" and "bearer" be
// claimed by two backends without a word, and then send a request to whichever
// of them matched the case the caller happened to write.
func TestAClaimedSchemeIsOneSchemeWhateverItsCase(t *testing.T) {
	table, err := buildOwnership([]authn.Verifier{claimingScheme("issuer", "BeArEr")})
	if err != nil {
		t.Fatalf("building the table: %v", err)
	}
	for _, written := range []string{"Bearer", "bearer", "BEARER", "BeArEr"} {
		if _, claimed := table.byScheme[strings.ToLower(written)]; !claimed {
			t.Errorf("a credential written with the scheme %q finds no claimant, so it would "+
				"reach a different path than the same scheme in another case", written)
		}
	}
}

// TestTwoOpenClaimsBuildWithoutComplaint records the overlap the build cannot
// detect, so that the request-time refusal covering it is not mistaken for a
// gap.
//
// An open claim has no key to collide on: whether two of them overlap is a
// question only the backends can answer, and only about a subject that has
// arrived. The build therefore accepts the pair, and the overlap surfaces as a
// refusal, which is asserted where the request path is tested.
func TestTwoOpenClaimsBuildWithoutComplaint(t *testing.T) {
	yes := func(context.Context, string) (bool, error) { return true, nil }

	table, err := buildOwnership([]authn.Verifier{openly("first", yes), openly("second", yes)})
	if err != nil {
		t.Fatalf("building a table with two open claims gave %v, want it to build: an open "+
			"claim has no key, so the overlap cannot be judged until a subject arrives", err)
	}
	if len(table.open) != 2 {
		t.Errorf("the table holds %d open claim(s), want 2", len(table.open))
	}
}

// TestTheTableNamesTheBackendsInAStableOrder holds the one thing the traversal
// order is for. It is not a domain priority — no step of the request-time
// sequence consults it — it is there so that a diagnostic naming the backends
// reads the same on every build.
func TestTheTableNamesTheBackendsInAStableOrder(t *testing.T) {
	table, err := buildOwnership([]authn.Verifier{
		enumerating("alpha", "a"),
		enumerating("beta", "b"),
	})
	if err != nil {
		t.Fatalf("building the table: %v", err)
	}
	got := strings.Join(table.names(), ",")
	if got != "alpha,beta" {
		t.Errorf("the table names the backends %q, want them in the order they were resolved", got)
	}
}
