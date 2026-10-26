package guard

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/authztest"
	"github.com/vislake/speed/pkg/core"
)

// stubEnforcer records every query and answers from a function, so a case can
// hold both what the decider put to the engine and what it did with the answer.
type stubEnforcer struct {
	mu     sync.Mutex
	answer func(authz.Query) (bool, error)
	asked  []authz.Query
}

func (s *stubEnforcer) Allowed(_ context.Context, q authz.Query) (bool, error) {
	s.mu.Lock()
	s.asked = append(s.asked, q)
	answer := s.answer
	s.mu.Unlock()
	if answer != nil {
		return answer(q)
	}
	return false, nil
}

func (s *stubEnforcer) queries() []authz.Query {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.asked)
}

// identity builds the identity a request would carry, centred on account. Any
// further option, such as WithActor, makes it what it makes it.
func identity(t *testing.T, account authn.AccountID, opts ...authntest.Option) authn.Identity {
	t.Helper()
	opts = append([]authntest.Option{authntest.WithAccount(account)}, opts...)
	ctx := authntest.Context(t, t.Context(), opts...)
	id, ok := authn.FromContext(ctx)
	if !ok {
		t.Fatalf("the fixture left no identity on the context")
	}
	return id
}

// A known action is translated to the engine's (object, action) pair and put to
// the engine with the actor's key and the decision's tenant.
func TestDecideTranslatesAndQueries(t *testing.T) {
	enforcer := &stubEnforcer{}
	enforcer.answer = func(authz.Query) (bool, error) { return true, nil }
	p := &product{enforcer: enforcer}

	allow, err := p.Decide(t.Context(), authn.Decision{
		Action: authn.ActionAccountRead,
		Actor:  identity(t, "alice"),
		Tenant: "acme",
	})
	if err != nil || !allow {
		t.Fatalf("Decide gave (%t, %v), want (true, nil)", allow, err)
	}
	want := authz.Query{Subject: "alice", Domain: "acme", Object: "account", Action: "read"}
	got := enforcer.queries()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("the engine was asked %v, want exactly %v", got, want)
	}
}

// A known action the engine refuses is a denial without an error, and the
// action has no fallback: one query is put.
func TestDecideCarriesADenial(t *testing.T) {
	enforcer := &stubEnforcer{}
	enforcer.answer = func(authz.Query) (bool, error) { return false, nil }
	p := &product{enforcer: enforcer}

	allow, err := p.Decide(t.Context(), authn.Decision{
		Action: authn.ActionAccountRead,
		Actor:  identity(t, "alice"),
		Tenant: "acme",
	})
	if err != nil || allow {
		t.Fatalf("Decide gave (%t, %v), want (false, nil)", allow, err)
	}
	if got := enforcer.queries(); len(got) != 1 {
		t.Fatalf("the engine was asked %v, want one query", got)
	}
}

// An action the translation does not hold is refused without an error, and the
// engine is never asked: a denial is the safe answer for an action this module
// cannot express.
func TestDecideRefusesAnUnknownAction(t *testing.T) {
	for _, c := range []struct {
		name string
		lead bool
	}{
		{"an engine that would allow", true},
		{"an engine that would deny", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			enforcer := &stubEnforcer{}
			enforcer.answer = func(authz.Query) (bool, error) { return c.lead, nil }
			p := &product{enforcer: enforcer}

			allow, err := p.Decide(t.Context(), authn.Decision{
				Action: authn.Action("authn.not.a.real.action"),
				Actor:  identity(t, "alice"),
				Tenant: "acme",
			})
			if err != nil || allow {
				t.Fatalf("Decide gave (%t, %v), want (false, nil)", allow, err)
			}
			if n := len(enforcer.queries()); n != 0 {
				t.Fatalf("the engine was asked %d times for an unknown action, want none", n)
			}
		})
	}
}

// An engine error is carried out and is neither a denial nor a permission.
func TestDecideCarriesAnEngineError(t *testing.T) {
	boom := errors.New("the store is down")
	enforcer := &stubEnforcer{}
	enforcer.answer = func(authz.Query) (bool, error) { return false, boom }
	p := &product{enforcer: enforcer}

	allow, err := p.Decide(t.Context(), authn.Decision{
		Action: authn.ActionAccountRead,
		Actor:  identity(t, "alice"),
		Tenant: "acme",
	})
	if allow || !errors.Is(err, boom) {
		t.Fatalf("Decide gave (%t, %v), want (false, the engine's error)", allow, err)
	}
}

// authn.tenant.access decides four ways: a match in the tenant's own domain is an
// allow with one query; a miss there falls back to the platform domain, where a
// match is an allow and a miss is a denial, each with two queries; and a decision
// already in the platform domain, the tenant being empty, is one query with no
// fallback to run.
func TestTenantAccessDecidesFourWays(t *testing.T) {
	cases := []struct {
		name        string
		tenant      string
		answer      func(authz.Query) bool
		wantAllow   bool
		wantDomains []authz.Domain
	}{
		{
			name:        "the tenant domain matches",
			tenant:      "acme",
			answer:      func(q authz.Query) bool { return q.Domain == "acme" },
			wantAllow:   true,
			wantDomains: []authz.Domain{"acme"},
		},
		{
			name:        "the tenant domain misses and the platform domain matches",
			tenant:      "acme",
			answer:      func(q authz.Query) bool { return q.Domain == authz.PlatformDomain },
			wantAllow:   true,
			wantDomains: []authz.Domain{"acme", authz.PlatformDomain},
		},
		{
			name:        "both domains miss",
			tenant:      "acme",
			answer:      func(authz.Query) bool { return false },
			wantAllow:   false,
			wantDomains: []authz.Domain{"acme", authz.PlatformDomain},
		},
		{
			name:        "the tenant is empty and the platform domain matches",
			tenant:      "",
			answer:      func(q authz.Query) bool { return q.Domain == authz.PlatformDomain },
			wantAllow:   true,
			wantDomains: []authz.Domain{authz.PlatformDomain},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			enforcer := &stubEnforcer{}
			enforcer.answer = func(q authz.Query) (bool, error) { return c.answer(q), nil }
			p := &product{enforcer: enforcer}

			allow, err := p.Decide(t.Context(), authn.Decision{
				Action: authn.ActionTenantAccess,
				Actor:  identity(t, "alice"),
				Tenant: c.tenant,
			})
			if err != nil || allow != c.wantAllow {
				t.Fatalf("Decide gave (%t, %v), want (%t, nil)", allow, err, c.wantAllow)
			}
			got := enforcer.queries()
			domains := make([]authz.Domain, len(got))
			for i, q := range got {
				domains[i] = q.Domain
				if q.Object != "tenant" || q.Action != "access" {
					t.Errorf("query %d was %q on %q, want the tenant object on access", i, q.Object, q.Action)
				}
			}
			if !slices.Equal(domains, c.wantDomains) {
				t.Fatalf("the domains were %v, want %v", domains, c.wantDomains)
			}
		})
	}
}

// A non-tenant-access action puts exactly one query, whatever its domain: an
// account-level action whose decision names no tenant is decided in the
// platform domain, and a tenant-level action in the tenant's own domain. A miss
// in that one domain is a denial, and the engine is asked no second time —
// authn.tenant.access is the only action with a query after a miss.
func TestANonAccessActionDecidesInOneQuery(t *testing.T) {
	cases := []struct {
		name   string
		action authn.Action
		tenant string
		domain authz.Domain
	}{
		{"an account-level action with no tenant decides in the platform domain", authn.ActionAccountRead, "", authz.PlatformDomain},
		{"a tenant-level action decides in its tenant's domain", authn.ActionSessionRead, "acme", "acme"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			enforcer := &stubEnforcer{}
			enforcer.answer = func(authz.Query) (bool, error) { return false, nil }
			p := &product{enforcer: enforcer}

			allow, err := p.Decide(t.Context(), authn.Decision{
				Action: c.action,
				Actor:  identity(t, "alice"),
				Tenant: c.tenant,
			})
			if err != nil {
				t.Fatalf("Decide gave an error: %v", err)
			}
			if allow {
				t.Fatal("Decide allowed a query the engine denied")
			}
			got := enforcer.queries()
			if len(got) != 1 || got[0].Domain != c.domain {
				t.Fatalf("the engine was asked %v, want one query in the %q domain", got, c.domain)
			}
		})
	}
}

// The three impersonation actions decide in the platform domain whatever tenant
// the decision names. The tenant an impersonation is confined to is the data the
// action operates on, not the policy domain: a platform administrator needs no
// grant in it, and a tenant administrator's role, held in the tenant domain,
// carries no platform permission. The engine is asked once, in the platform
// domain; a miss there denies and is not retried in the named tenant.
func TestImpersonationDecidesInThePlatformDomain(t *testing.T) {
	for _, c := range []struct {
		action authn.Action
		want   string
	}{
		{authn.ActionImpersonateStart, "start"},
		{authn.ActionImpersonationRead, "read"},
		{authn.ActionImpersonationRevoke, "revoke"},
	} {
		t.Run(string(c.action), func(t *testing.T) {
			enforcer := &stubEnforcer{}
			enforcer.answer = func(q authz.Query) (bool, error) {
				return q.Domain == authz.PlatformDomain && q.Object == "impersonation" && q.Action == c.want, nil
			}
			p := &product{enforcer: enforcer}

			// The platform administrator holds no binding in acme. The decision
			// must still be allowed, because it is asked in the platform domain.
			allow, err := p.Decide(t.Context(), authn.Decision{
				Action: c.action,
				Actor:  identity(t, "acct-platform"),
				Tenant: "acme",
			})
			if err != nil || !allow {
				t.Fatalf("Decide gave (%t, %v), want (true, nil) for a platform administrator with no acme grant", allow, err)
			}
			got := enforcer.queries()
			if len(got) != 1 || got[0].Domain != authz.PlatformDomain {
				t.Fatalf("the engine was asked %v, want one query in the platform domain", got)
			}

			// The tenant administrator holds the grant in acme only. The
			// decision must refuse it, and a query in acme would be the defect.
			enforcer.answer = func(q authz.Query) (bool, error) { return q.Domain == "acme", nil }
			allow, err = p.Decide(t.Context(), authn.Decision{
				Action: c.action,
				Actor:  identity(t, "acct-tenant-admin"),
				Tenant: "acme",
			})
			if err != nil || allow {
				t.Fatalf("Decide gave (%t, %v), want (false, nil) for a tenant administrator with no platform permission", allow, err)
			}
			got = enforcer.queries()
			if len(got) != 2 || got[1].Domain != authz.PlatformDomain {
				t.Fatalf("the engine was asked %v, want a second query in the platform domain", got)
			}
		})
	}
}

// The subject is the effective account. Under an impersonation the decision is
// made for the account the request is taken to be from, not for the account
// whose hands are on the request: the engine is asked about the impersonated
// account, so the operator's own permissions do not carry.
func TestDecideTakesTheEffectiveAccount(t *testing.T) {
	enforcer := &stubEnforcer{}
	enforcer.answer = func(authz.Query) (bool, error) { return true, nil }
	p := &product{enforcer: enforcer}

	actor := identity(t, "acct-bob",
		authntest.WithActor("acct-alice"),
		authntest.WithSession("sess-1"),
		authntest.WithScope("acme"))
	if actor.Account() != "acct-bob" || actor.Actor() != "acct-alice" {
		t.Fatalf("the impersonated identity is account %q actor %q, want acct-bob acct-alice",
			actor.Account(), actor.Actor())
	}

	_, err := p.Decide(t.Context(), authn.Decision{
		Action: authn.ActionAccountRead,
		Actor:  actor,
		Tenant: "acme",
	})
	if err != nil {
		t.Fatalf("Decide gave an error: %v", err)
	}
	got := enforcer.queries()
	if len(got) != 1 || got[0].Subject != "acct-bob" {
		t.Fatalf("the engine was asked %v, want the subject acct-bob", got)
	}
}

// The module's own descriptor is driven through a real registry, holding what a
// decider built by hand cannot: that New takes the engine up from the registry
// the way a host's command does, and that the module provides the seam
// authn.Decider under the name the command tree uses.
func TestTheRegistryDeliversTheDecider(t *testing.T) {
	enforcer := authztest.NewEnforcer()
	enforcer.Answer(func(authz.Query) bool { return true })

	reg := authztest.Registry(t, guardedBody, Module())
	authztest.AddProvider(reg, authztest.EnforcerProvider(enforcer))
	authztest.AddProvider(reg, authztest.CatalogProvider(authztest.NewCatalog(authz.PermissionAccountRead)))

	var delivered authn.Decider
	err := reg.Drive(t.Context(), core.Drive{
		Depth: core.StageNew,
		// The name is a literal and not ModuleName: the host writes the
		// published key into its command tree, so a rename that left the
		// constant and its uses agreeing would break that host with the tests
		// still green.
		Participants: []string{"authz.guard"},
		Action: func(ctx context.Context, reg *core.Registry) error {
			d, err := core.Resolve[authn.Decider](reg)
			if err != nil {
				return err
			}
			delivered = d
			allow, err := d.Decide(ctx, authn.Decision{
				Action: authn.ActionAccountRead,
				Actor:  identity(t, "alice"),
				Tenant: "acme",
			})
			if err != nil {
				return err
			}
			if !allow {
				t.Error("the delivered decider refused an action the engine allowed")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("the assembly did not drive: %v", err)
	}
	if delivered == nil {
		t.Fatal("the drive delivered no authn.Decider")
	}
	want := authz.Query{Subject: "alice", Domain: "acme", Object: "account", Action: "read"}
	if got := enforcer.Asked(); !slices.Equal(got, []authz.Query{want}) {
		t.Errorf("the engine was asked %v, want %v", got, want)
	}
}

// The module requires the catalog as it requires the engine: a command that
// brings this module in without a catalog fails the startup, so a host that
// provides an engine but no policy catalog learns it is incomplete rather than
// starting a decider whose route declarations can check nothing.
func TestTheRegistryRefusesWithoutACatalog(t *testing.T) {
	reg := authztest.Registry(t, guardedBody, Module())
	authztest.AddProvider(reg, authztest.EnforcerProvider(authztest.NewEnforcer()))

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{ModuleName},
	})
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("a drive with no catalog returned %v, want ErrMissingProvider", err)
	}
}

// A second provider of the decision seam fails the startup. The module delivers
// authn.Decider exclusively and states StateEnabled, so two providers is not a
// configuration resolution stands one of them down from: the seam has one
// implementation, and which one answers must not be left to resolution order or
// to a runtime ErrAmbiguousProvider. The stand-in claims nothing exclusively,
// which is what the guard module's own exclusive claim has to catch.
func TestASecondDeciderFailsTheStartup(t *testing.T) {
	reg := authztest.Registry(t, guardedBody, Module())
	authztest.AddProvider(reg, authztest.EnforcerProvider(authztest.NewEnforcer()))
	authztest.AddProvider(reg, authztest.CatalogProvider(authztest.NewCatalog(authz.PermissionAccountRead)))
	authztest.AddProvider(reg, authztest.Provider{
		Name:     "stand-in.decider",
		Token:    (*authn.Decider)(nil),
		Instance: refusingDecider{},
	})

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: []string{ModuleName},
	})
	if !errors.Is(err, core.ErrExclusiveViolated) {
		t.Fatalf("a second provider of the seam ended the drive with %v, want ErrExclusiveViolated", err)
	}
}

// refusingDecider is a second implementation of the seam, delivered by a
// stand-in module that claims nothing exclusively.
type refusingDecider struct{}

func (refusingDecider) Decide(context.Context, authn.Decision) (bool, error) { return false, nil }

// stubPermissions answers the platform-domain read the impersonation rule makes.
type stubPermissions struct {
	held     map[string][]authz.Permission
	err      error
	mu       sync.Mutex
	subjects []string
	domains  []authz.Domain
}

func (s *stubPermissions) Held(_ context.Context, domain authz.Domain, subject string) ([]authz.Permission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subjects = append(s.subjects, subject)
	s.domains = append(s.domains, domain)
	if s.err != nil {
		return nil, s.err
	}
	return s.held[subject], nil
}

// An impersonation may not target an account holding a platform permission:
// that account is an administrator, and administrators are never impersonated.
func TestDecideRefusesAnAdministratorTarget(t *testing.T) {
	enforcer := &stubEnforcer{}
	enforcer.answer = func(authz.Query) (bool, error) { return true, nil }
	permissions := &stubPermissions{held: map[string][]authz.Permission{
		"acct-admin": {"tenant.read"},
	}}
	p := &product{enforcer: enforcer, permissions: permissions}

	allow, err := p.Decide(t.Context(), authn.Decision{
		Action:        authn.ActionImpersonateStart,
		Actor:         identity(t, "acct-ops"),
		Tenant:        "acme",
		TargetAccount: "acct-admin",
	})
	if err != nil || allow {
		t.Fatalf("Decide gave (%t, %v), want (false, nil)", allow, err)
	}
	if len(permissions.subjects) != 1 || permissions.subjects[0] != "acct-admin" {
		t.Fatalf("the permission reader was asked %v, want acct-admin", permissions.subjects)
	}
	if permissions.domains[0] != authz.PlatformDomain {
		t.Fatalf("the permission reader was asked in %q, want the platform domain", permissions.domains[0])
	}
}

// A target holding no platform permission passes the impersonation rule, and a
// decision that is not an impersonation start never reads the target's
// permissions at all.
func TestDecideAllowsANonAdministratorTargetAndSkipsOtherActions(t *testing.T) {
	enforcer := &stubEnforcer{}
	enforcer.answer = func(authz.Query) (bool, error) { return true, nil }
	permissions := &stubPermissions{held: map[string][]authz.Permission{}}
	p := &product{enforcer: enforcer, permissions: permissions}

	allow, err := p.Decide(t.Context(), authn.Decision{
		Action:        authn.ActionImpersonateStart,
		Actor:         identity(t, "acct-ops"),
		Tenant:        "acme",
		TargetAccount: "acct-member",
	})
	if err != nil || !allow {
		t.Fatalf("Decide gave (%t, %v), want (true, nil)", allow, err)
	}

	if _, err := p.Decide(t.Context(), authn.Decision{
		Action: authn.ActionAccountRead,
		Actor:  identity(t, "acct-ops"),
		Tenant: "acme",
	}); err != nil {
		t.Fatalf("Decide gave an error: %v", err)
	}
	if len(permissions.subjects) != 1 {
		t.Fatalf("the permission reader was asked %v, want only the impersonation target", permissions.subjects)
	}
}

// A permission read that fails is an outage, not an empty set: the target's
// rank is unknown and the impersonation does not start.
func TestDecideCarriesAPermissionReadFailure(t *testing.T) {
	enforcer := &stubEnforcer{}
	enforcer.answer = func(authz.Query) (bool, error) { return true, nil }
	failure := errors.New("policy is unavailable")
	p := &product{enforcer: enforcer, permissions: &stubPermissions{err: failure}}

	allow, err := p.Decide(t.Context(), authn.Decision{
		Action:        authn.ActionImpersonateStart,
		Actor:         identity(t, "acct-ops"),
		Tenant:        "acme",
		TargetAccount: "acct-member",
	})
	if allow || !errors.Is(err, failure) {
		t.Fatalf("Decide gave (%t, %v), want (false, %v)", allow, err, failure)
	}
}
