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

// TestDecideTranslatesAndQueries checks that decide translates and queries.
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

// TestDecideCarriesADenial checks that Decide carries a denial.
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

// TestDecideRefusesAnUnknownAction checks that decide refuses an unknown action.
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

// TestDecideCarriesAnEngineError checks that Decide carries an engine error.
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

// TestTenantAccessDecidesFourWays checks that tenant access decides four ways.
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

// TestANonAccessActionDecidesInOneQuery checks that a non access action decides in one query.
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

// TestImpersonationDecidesInThePlatformDomain checks that impersonation decides in the platform domain.
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

// TestDecideTakesTheEffectiveAccount checks that Decide takes the effective account.
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

// TestTheRegistryDeliversTheDecider checks that the registry delivers the decider.
func TestTheRegistryDeliversTheDecider(t *testing.T) {
	enforcer := authztest.NewEnforcer()
	enforcer.Answer(func(authz.Query) bool { return true })

	reg := authztest.Registry(t, guardedBody, Module())
	authztest.AddProvider(reg, authztest.EnforcerProvider(enforcer))
	authztest.AddProvider(reg, authztest.CatalogProvider(authztest.NewCatalog(authz.PermissionAccountRead)))

	var delivered authn.Decider
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageNew,
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

// TestTheRegistryRefusesWithoutACatalog checks that the registry refuses without a catalog.
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

// TestASecondDeciderFailsTheStartup checks that a second decider fails the startup.
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

type refusingDecider struct{}

func (refusingDecider) Decide(context.Context, authn.Decision) (bool, error) { return false, nil }

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

// TestDecideRefusesAnAdministratorTarget checks that decide refuses an administrator target.
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

// TestDecideAllowsANonAdministratorTargetAndSkipsOtherActions checks that decide allows a non administrator target and skips other actions.
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

// TestDecideCarriesAPermissionReadFailure checks that Decide carries a permission read failure.
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
