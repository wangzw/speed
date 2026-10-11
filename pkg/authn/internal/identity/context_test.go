package identity_test

import (
	"context"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/identity"
	"github.com/vislake/speed/pkg/authn/internal/model"
)

// Local is a clause of its own: an IdP-declared level never satisfies it, at
// whatever level and freshness the rest of the requirement asks for.
func TestSatisfiesLocalIsIndependentOfTheOtherClauses(t *testing.T) {
	idp := func(a model.Assurance) identity.Identity {
		p := principal()
		p.Assurance, p.IDPAssured, p.AuthTime = a, true, t0
		return identity.New(p, "b", "c")
	}
	local := func(a model.Assurance) identity.Identity {
		p := principal()
		p.Assurance, p.IDPAssured, p.AuthTime = a, false, t0
		return identity.New(p, "b", "c")
	}
	for _, lvl := range []model.Assurance{model.AssuranceNone, model.AssuranceSingle, model.AssuranceMulti} {
		for _, age := range []time.Duration{0, time.Hour} {
			r := model.Requirement{Assurance: lvl, MaxAge: age, Local: true}
			if idp(model.AssuranceMulti).Satisfies(r, t0) {
				t.Errorf("an IdP-assured identity satisfies %+v", r)
			}
			if idp(model.AssuranceSingle).Satisfies(r, t0) {
				t.Errorf("an IdP-assured Single identity satisfies %+v", r)
			}
			if !local(model.AssuranceMulti).Satisfies(r, t0) {
				t.Errorf("a locally assured identity does not satisfy %+v", r)
			}
		}
	}
}

// An attribute may hold the empty string; present is not the same as non-empty.
func TestAnEmptyAttributeValueIsPresent(t *testing.T) {
	p := principal()
	p.Attributes = map[string]string{"beta": ""}
	id := identity.New(p, "b", "c")
	if v, ok := id.Attribute("beta"); !ok || v != "" {
		t.Errorf("Attribute(beta) = %q, %v; want empty, true", v, ok)
	}
	if got := id.Attributes(); len(got) != 1 {
		t.Errorf("Attributes() = %v; want the one empty-valued key", got)
	}
}

// With and WithAccess derive from the context they are given.
func TestWritersKeepTheParentContext(t *testing.T) {
	type k struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), k{}, "v"))
	id := identity.New(principal(), "b", "c")
	for name, ctx := range map[string]context.Context{
		"With":                 identity.With(parent, id),
		"WithAccess":           identity.WithAccess(parent, model.AccessRequired),
		"WithAccess then With": identity.With(identity.WithAccess(parent, model.AccessRequired), id),
		"With then WithAccess": identity.WithAccess(identity.With(parent, id), model.AccessRequired),
	} {
		if ctx.Value(k{}) != "v" {
			t.Errorf("%s dropped the parent's values", name)
		}
		if name != "With" && identity.AccessFrom(ctx) != model.AccessRequired {
			t.Errorf("%s: AccessFrom = %v", name, identity.AccessFrom(ctx))
		}
		if name != "WithAccess" {
			if _, ok := identity.From(ctx); !ok {
				t.Errorf("%s: no identity", name)
			}
		}
	}
	both := identity.With(identity.WithAccess(parent, model.AccessRequired), id)
	cancel()
	select {
	case <-both.Done():
	default:
		t.Error("cancelling the parent did not cancel the context With returned")
	}
}

// No value another package can write is read as an identity or an access level.
func TestContextKeysCannotBeForged(t *testing.T) {
	id := identity.New(principal(), "b", "c")
	for name, key := range map[string]any{"string": "identity", "access string": "access", "empty struct": struct{}{}, "int": 0} {
		ctx := context.WithValue(context.Background(), key, id)
		if _, ok := identity.From(ctx); ok {
			t.Errorf("From read an identity stored under the %s key", name)
		}
		ctx = context.WithValue(context.Background(), key, model.AccessOpen)
		if got := identity.AccessFrom(ctx); got != model.AccessUnknown {
			t.Errorf("AccessFrom read %v stored under the %s key", got, name)
		}
	}
}

// Impersonating is Actor() != Account(), nothing else; Scope is the scope.
func TestImpersonatingIsOnlyActorDiffersFromAccount(t *testing.T) {
	same := identity.New(model.Principal{Account: "a", Actor: "a", Session: "s", Scope: "acme"}, "b", "c")
	if same.Impersonating() {
		t.Error("an actor equal to the account reads as an impersonation")
	}
	scoped := identity.New(model.Principal{Account: "a", Scope: "acme"}, "b", "c")
	if scoped.Impersonating() {
		t.Error("a scope alone reads as an impersonation")
	}
	actorOnly := identity.New(model.Principal{Account: "a", Actor: "x"}, "b", "c")
	if !actorOnly.Impersonating() {
		t.Error("an actor differing from the account is not an impersonation")
	}
	if s, ok := actorOnly.Scope(); ok || s != "" {
		t.Errorf("Scope() = %q, %v with no scope stamped", s, ok)
	}
}

// From's second result tells whether an identity was put in, not whether it
// has an account.
func TestFromTellsPresenceNotNonEmptiness(t *testing.T) {
	if _, ok := identity.From(identity.With(context.Background(), identity.Identity{})); !ok {
		t.Error("From reported no identity after With put one in")
	}
}
