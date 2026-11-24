package identity_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/identity"
	"github.com/vislake/speed/pkg/authn/internal/model"
)

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func principal() model.Principal {
	return model.Principal{
		Account:    "acct-1",
		Session:    "sess-1",
		Methods:    []model.Method{model.MethodPassword, model.MethodTOTP},
		Assurance:  model.AssuranceMulti,
		AuthTime:   t0,
		Provider:   "0192f6c2-7b1e-7d3a-9c11-5a3f0e2b8d44",
		Attributes: map[string]string{"region": "eu", "tier": "gold"},
	}
}

func impersonation() model.Principal {
	p := principal()
	p.Actor = "admin-9"
	p.Scope = "acme"
	return p
}

// TestKeyIsTheActor checks that key is the actor.
func TestKeyIsTheActor(t *testing.T) {
	for _, c := range []struct {
		name    string
		id      identity.Identity
		account model.AccountID
		actor   model.AccountID
		imper   bool
	}{
		{"no impersonation", identity.New(principal(), "session", "Cookie"), "acct-1", "acct-1", false},
		{"impersonation", identity.New(impersonation(), "session", "Cookie"), "acct-1", "admin-9", true},
		{"the zero identity", identity.Identity{}, "", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.id.Account(); got != c.account {
				t.Errorf("Account() = %q, want %q", got, c.account)
			}
			if got := c.id.Actor(); got != c.actor {
				t.Errorf("Actor() = %q, want %q", got, c.actor)
			}
			if got := c.id.Impersonating(); got != c.imper {
				t.Errorf("Impersonating() = %v, want %v", got, c.imper)
			}
			if got := c.id.Key(); got != string(c.id.Actor()) {
				t.Errorf("Key() = %q, want Actor() = %q", got, c.id.Actor())
			}
			if c.imper && c.id.Key() == string(c.id.Account()) {
				t.Errorf("Key() = %q is the impersonated account: an impersonation would spend its allowance", c.id.Key())
			}
		})
	}
}

// checks that the zero identity's key is empty rather than a slash.
func TestTheZeroIdentityKeyIsEmptyNotASlash(t *testing.T) {
	if got := (identity.Identity{}).Key(); got != "" {
		t.Fatalf("zero identity Key() = %q, want the empty string", got)
	}
}

// checks that the impersonation scope is reported, and empty when there is
// none.
func TestScope(t *testing.T) {
	if s, ok := identity.New(principal(), "b", "c").Scope(); ok || s != "" {
		t.Errorf("Scope() = %q, %v on an identity that is not impersonating; want empty, false", s, ok)
	}
	if s, ok := identity.New(impersonation(), "b", "c").Scope(); !ok || s != "acme" {
		t.Errorf("Scope() = %q, %v on an impersonation; want acme, true", s, ok)
	}
	if s, ok := (identity.Identity{}).Scope(); ok || s != "" {
		t.Errorf("Scope() = %q, %v on the zero identity; want empty, false", s, ok)
	}
}

// TestFieldsAreTheOnesStamped checks that fields are the ones stamped.
func TestFieldsAreTheOnesStamped(t *testing.T) {
	p := principal()
	p.IDPAssured = true
	id := identity.New(p, "session", "Cookie")

	if id.Session() != "sess-1" || id.Assurance() != model.AssuranceMulti || !id.AssuranceFromIDP() ||
		!id.AuthTime().Equal(t0) || id.Provider() != p.Provider {
		t.Errorf("a field was not carried from the principal: %+v", id)
	}
	if id.Backend() != "session" || id.Challenge() != "Cookie" {
		t.Errorf("Backend() = %q, Challenge() = %q; want the stamped session, Cookie", id.Backend(), id.Challenge())
	}
	if v, ok := id.Attribute("region"); !ok || v != "eu" {
		t.Errorf("Attribute(region) = %q, %v", v, ok)
	}
	if _, ok := id.Attribute("missing"); ok {
		t.Error("Attribute(missing) reported present")
	}
	if _, ok := (identity.Identity{}).Attribute("region"); ok {
		t.Error("the zero identity has an attribute")
	}
}

// Backend and challenge are the gateway's stamps.
func TestBackendAndChallengeComeFromTheArguments(t *testing.T) {
	a := identity.New(principal(), "one", "Bearer")
	b := identity.New(principal(), "two", "Basic")
	if a.Backend() != "one" || b.Backend() != "two" || a.Challenge() != "Bearer" || b.Challenge() != "Basic" {
		t.Fatal("backend or challenge is not the argument given")
	}
}

// TestIdentityIsACopyOfThePrincipal checks that identity is a copy of the
// principal.
func TestIdentityIsACopyOfThePrincipal(t *testing.T) {
	p := principal()
	id := identity.New(p, "b", "c")

	p.Methods[0] = model.MethodSMS
	p.Methods = append(p.Methods, model.MethodSAML)
	p.Attributes["region"] = "us"
	p.Attributes["injected"] = "x"

	if got := id.Methods(); len(got) != 2 || got[0] != model.MethodPassword || got[1] != model.MethodTOTP {
		t.Errorf("Methods() = %v after the backend changed its principal; want [pwd otp]", got)
	}
	if v, _ := id.Attribute("region"); v != "eu" {
		t.Errorf("Attribute(region) = %q after the backend changed its principal; want eu", v)
	}
	if _, ok := id.Attribute("injected"); ok {
		t.Error("an attribute the backend added after stamping reached the identity")
	}
	if got := id.Attributes(); len(got) != 2 {
		t.Errorf("Attributes() = %v; want the two stamped", got)
	}
}

// TestReadersReturnCopies checks that readers return copies.
func TestReadersReturnCopies(t *testing.T) {
	id := identity.New(principal(), "b", "c")

	m := id.Methods()
	m[0] = model.MethodSMS
	_ = append(m[:1], model.MethodSAML)
	if got := id.Methods(); got[0] != model.MethodPassword || got[1] != model.MethodTOTP {
		t.Errorf("Methods() = %v after the copy was changed; want the original", got)
	}

	a := id.Attributes()
	a["region"] = "us"
	delete(a, "tier")
	a["new"] = "x"
	got := id.Attributes()
	if got["region"] != "eu" || got["tier"] != "gold" || len(got) != 2 {
		t.Errorf("Attributes() = %v after the copy was changed; want the original", got)
	}
}

// TestEmptyReadersAreSafeToWrite checks that empty readers are safe to write.
func TestEmptyReadersAreSafeToWrite(t *testing.T) {
	a := (identity.Identity{}).Attributes()
	if a == nil {
		t.Fatal("Attributes() on an identity with none is nil")
	}
	a["x"] = "y"
	if len((identity.Identity{}).Methods()) != 0 {
		t.Error("the zero identity has methods")
	}
}

// TestCopiesHoldUnderConcurrentUse runs under -race.
func TestCopiesHoldUnderConcurrentUse(t *testing.T) {
	p := principal()
	id := identity.New(p, "b", "c")

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				m := id.Methods()
				m[0] = model.MethodSMS
				a := id.Attributes()
				a["region"] = "us"
				if v, _ := id.Attribute("region"); v != "eu" {
					t.Errorf("Attribute(region) = %q, want eu", v)
					return
				}
				if id.Methods()[0] != model.MethodPassword {
					t.Error("Methods() reflects another goroutine's write")
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			p.Methods[0] = model.MethodSAML
			p.Attributes["region"] = "xx"
		}
	}()
	wg.Wait()
}

// checks that Identity.Satisfies reads the requirement it is given.
func TestSatisfies(t *testing.T) {
	const hour = time.Hour
	at := func(a model.Assurance, auth time.Time, fromIDP bool) identity.Identity {
		p := principal()
		p.Assurance = a
		p.AuthTime = auth
		p.IDPAssured = fromIDP
		return identity.New(p, "b", "c")
	}
	for _, c := range []struct {
		name string
		id   identity.Identity
		r    model.Requirement
		now  time.Time
		want bool
	}{
		{"below the level", at(model.AssuranceSingle, t0, false), model.Requirement{Assurance: model.AssuranceMulti}, t0, false},
		{"none below single", at(model.AssuranceNone, t0, false), model.Requirement{Assurance: model.AssuranceSingle}, t0, false},
		{"at the level", at(model.AssuranceMulti, t0, false), model.Requirement{Assurance: model.AssuranceMulti}, t0, true},
		{"above the level", at(model.AssuranceMulti, t0, false), model.Requirement{Assurance: model.AssuranceSingle}, t0, true},
		{"a level no identity reaches", at(model.AssuranceMulti, t0, false), model.Requirement{Assurance: model.AssuranceMulti + 1}, t0, false},
		{"none asks nothing of a non-zero identity", at(model.AssuranceNone, t0, false), model.Requirement{}, t0, true},

		{"zero max age asks no freshness", at(model.AssuranceMulti, t0.Add(-1000*hour), false), model.Requirement{Assurance: model.AssuranceMulti}, t0, true},
		{"age exactly max age", at(model.AssuranceMulti, t0.Add(-10*time.Minute), false), model.Requirement{Assurance: model.AssuranceMulti, MaxAge: 10 * time.Minute}, t0, true},
		{"age one nanosecond over", at(model.AssuranceMulti, t0.Add(-10*time.Minute-time.Nanosecond), false), model.Requirement{Assurance: model.AssuranceMulti, MaxAge: 10 * time.Minute}, t0, false},
		{"age one nanosecond under", at(model.AssuranceMulti, t0.Add(-10*time.Minute+time.Nanosecond), false), model.Requirement{Assurance: model.AssuranceMulti, MaxAge: 10 * time.Minute}, t0, true},
		{"auth time after now is not stale", at(model.AssuranceMulti, t0.Add(time.Hour), false), model.Requirement{Assurance: model.AssuranceMulti, MaxAge: time.Minute}, t0, true},
		{"zero auth time is stale under a max age", at(model.AssuranceMulti, time.Time{}, false), model.Requirement{Assurance: model.AssuranceMulti, MaxAge: 1000 * hour}, t0, false},
		{"negative max age is met by nobody", at(model.AssuranceMulti, t0, false), model.Requirement{Assurance: model.AssuranceSingle, MaxAge: -time.Second}, t0, false},
		{"a fresh login still fails on level", at(model.AssuranceSingle, t0, false), model.Requirement{Assurance: model.AssuranceMulti, MaxAge: time.Hour}, t0, false},

		{"local refuses an IdP assurance", at(model.AssuranceMulti, t0, true), model.Requirement{Assurance: model.AssuranceMulti, Local: true}, t0, false},
		{"local accepts a local assurance", at(model.AssuranceMulti, t0, false), model.Requirement{Assurance: model.AssuranceMulti, Local: true}, t0, true},
		{"non-local accepts an IdP assurance", at(model.AssuranceMulti, t0, true), model.Requirement{Assurance: model.AssuranceMulti}, t0, true},

		{"zero identity against single", identity.Identity{}, model.Requirement{Assurance: model.AssuranceSingle}, t0, false},
		{"zero identity against nothing", identity.Identity{}, model.Requirement{}, t0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.id.Satisfies(c.r, c.now); got != c.want {
				t.Errorf("Satisfies(%+v) = %v, want %v", c.r, got, c.want)
			}
		})
	}
}

// checks that With and FromContext round-trip an identity.
func TestWithAndFrom(t *testing.T) {
	id := identity.New(principal(), "b", "c")
	ctx := identity.With(context.Background(), id)

	got, ok := identity.From(ctx)
	if !ok || got.Account() != "acct-1" || got.Backend() != "b" {
		t.Fatalf("From(With(id)) = %+v, %v", got, ok)
	}

	other := identity.New(impersonation(), "b2", "c")
	child := identity.With(ctx, other)
	if got, _ := identity.From(child); got.Actor() != "admin-9" {
		t.Errorf("the child context reads %q, want the identity put there", got.Actor())
	}
	if got, _ := identity.From(ctx); got.Actor() != "acct-1" {
		t.Errorf("the parent context changed to %q", got.Actor())
	}
}

type otherKey struct{}

// TestFromHasNoFallback checks that from has no fallback.
func TestFromHasNoFallback(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"background":       context.Background(),
		"with access only": identity.WithAccess(context.Background(), model.AccessOpen),
		"with a value":     context.WithValue(context.Background(), otherKey{}, "v"),
	} {
		if got, ok := identity.From(ctx); ok {
			t.Errorf("%s: From reported an identity: %+v", name, got)
		}
	}
}

// checks that WithAccess and AccessFromContext round-trip the access level.
func TestWithAccessAndAccessFrom(t *testing.T) {
	if got := identity.AccessFrom(context.Background()); got != model.AccessUnknown {
		t.Errorf("an untouched context reads %v, want AccessUnknown", got)
	}
	withID := identity.With(context.Background(), identity.New(principal(), "b", "c"))
	if got := identity.AccessFrom(withID); got != model.AccessUnknown {
		t.Errorf("a context with only an identity reads %v, want AccessUnknown", got)
	}
	for _, a := range []model.Access{model.AccessOpen, model.AccessOptional, model.AccessRequired, model.AccessUnknown} {
		ctx := identity.WithAccess(withID, a)
		if got := identity.AccessFrom(ctx); got != a {
			t.Errorf("AccessFrom(WithAccess(%v)) = %v", a, got)
		}
		if _, ok := identity.From(ctx); !ok {
			t.Errorf("WithAccess(%v) dropped the identity", a)
		}
	}
}

// TestNewSettlesNothing holds that New stores the assurance as handed, so an
// unstated assurance stays AssuranceNone.
func TestNewSettlesNothing(t *testing.T) {
	p := principal()
	p.Assurance = model.AssuranceNone
	if got := identity.New(p, "b", "c").Assurance(); got != model.AssuranceNone {
		t.Errorf("New stored the assurance %d for an unstated one, want it as handed", got)
	}
}
