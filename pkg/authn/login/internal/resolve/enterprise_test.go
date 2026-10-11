package resolve

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// A5: an enterprise identity with no binding.

func TestEnterpriseDomainMustBeVerifiedForTheProvidersTenant(t *testing.T) {
	// Someone who administers a provider can make it declare any address. The
	// address's domain has to be one the provider's own tenant verified, and the
	// accounts that hold the address are not looked at before that.
	for _, tc := range []struct {
		name  string
		email string
		setup func(*env)
	}{
		{"domain nobody verified", "ann@elsewhere.com", nil},
		{"domain another tenant verified", "ann@rival.com", func(e *env) {
			e.s.domains["rival.com"] = []authn.SSOProvider{{ID: idpOther, Tenant: tenantElse, Enabled: true}}
		}},
		{"address with no domain", "ann@", nil},
		{"address with two @", "a@b@acme.com", nil},
		{"address outside ASCII", "ann@acme.cöm", nil},
	} {
		for _, provisioning := range []string{authn.ProvisioningJIT, authn.ProvisioningPreprovisioned} {
			t.Run(tc.name+"/"+provisioning, func(t *testing.T) {
				e := newEnv(t, withProvider(provisioning, true))
				if _, err := normalize(authn.IdentifierEmail, tc.email); err == nil {
					e.s.seed(t, seed{id: "victim", status: authn.StatusActive, email: tc.email, emailVia: "idp:google"})
				}
				if tc.setup != nil {
					tc.setup(e)
				}
				_, err := e.resolve(enterprise("u1", tc.email))
				if !errors.Is(err, authn.ErrDomainMismatch) {
					t.Fatalf("err = %v, want ErrDomainMismatch", err)
				}
				if e.s.writes() != 0 || e.s.membershipRead != 0 {
					t.Errorf("%d writes and %d membership reads, want none", e.s.writes(), e.s.membershipRead)
				}
				if _, ok := e.s.boundTo("oidc:"+idpAcme, "u1"); ok {
					t.Error("the identity was bound")
				}
			})
		}
	}
}

func TestEnterpriseWithoutAnAddressIsNotProvisioned(t *testing.T) {
	e := newEnv(t, withProvider(authn.ProvisioningJIT, false))
	_, err := e.resolve(enterprise("u1", ""))
	if !errors.Is(err, authn.ErrNotProvisioned) {
		t.Errorf("err = %v, want ErrNotProvisioned", err)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none", e.s.writes())
	}
}

func TestEnterpriseDomainIsRefusedByDefaultWhenTheRegisterFails(t *testing.T) {
	boom := errors.New("register down")
	e := newEnv(t, withProvider(authn.ProvisioningJIT, false))
	e.s.failDiscover = boom
	_, err := e.resolve(enterprise("u1", "ann@acme.com"))
	if !errors.Is(err, boom) || e.s.writes() != 0 {
		t.Errorf("err = %v with %d writes, want the register's failure and none", err, e.s.writes())
	}
}

func TestEnterprisePreprovisionedMatchesAnAdministratorsAccount(t *testing.T) {
	for _, via := range []string{authn.VerifiedViaAdmin, "idp:google"} {
		t.Run(via, func(t *testing.T) {
			e := newEnv(t, withProvider(authn.ProvisioningPreprovisioned, false))
			id := e.s.seed(t, seed{id: "a1", status: authn.StatusActive, email: "ann@acme.com", emailVia: via, password: true, tenants: []string{tenantAcme}})
			res, err := e.resolve(enterprise("u1", "ann@acme.com"))
			if err != nil || res.Account != id || res.Outcome != OutcomeMatched {
				t.Fatalf("Resolve = %+v, %v, want a1 matched", res, err)
			}
			if got, ok := e.s.boundTo("oidc:"+idpAcme, "u1"); !ok || got != id {
				t.Errorf("binding = %v, %v, want it on a1", got, ok)
			}
			if len(res.Cleanup) != 0 {
				t.Errorf("Cleanup = %v, want none: the account was made for this person", res.Cleanup)
			}
			if len(e.s.membershipPuts) != 0 || e.s.membershipRead != 0 || len(e.s.created) != 0 {
				t.Errorf("memberships %d writes %d reads, %d accounts created, want none", len(e.s.membershipPuts), e.s.membershipRead, len(e.s.created))
			}
		})
	}
}

func TestEnterprisePreprovisionedRefusals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sd       *seed
		disabled bool
		want     error
	}{
		{"no account", nil, false, authn.ErrNotProvisioned},
		{"address from an import", &seed{email: "ann@acme.com", emailVia: authn.VerifiedViaImportUnverified}, false, authn.ErrNotProvisioned},
		{"address under no channel", &seed{email: "ann@acme.com", emailVia: ""}, false, authn.ErrNotProvisioned},
		{"account disabled", &seed{email: "ann@acme.com", emailVia: authn.VerifiedViaAdmin}, true, authn.ErrAccountUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, withProvider(authn.ProvisioningPreprovisioned, true), withSignup(SignupOpen))
			if tc.sd != nil {
				sd := *tc.sd
				sd.id = "a1"
				if !tc.disabled {
					sd.status = authn.StatusActive
				}
				e.s.seed(t, sd)
			}
			_, err := e.resolve(enterprise("u1", "ann@acme.com"))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if e.s.writes() != 0 {
				t.Errorf("%d writes, want none: a preprovisioned provider never creates an account", e.s.writes())
			}
		})
	}
}

func TestEnterpriseJITCreatesTheAccountAndItsMembership(t *testing.T) {
	e := newEnv(t, withProvider(authn.ProvisioningJIT, false))
	res, err := e.resolve(enterprise("u1", "ann@acme.com"))
	if err != nil || res.Outcome != OutcomeCreated || !res.MembershipCreated {
		t.Fatalf("Resolve = %+v, %v, want a created account with a membership", res, err)
	}
	ids := e.s.identifiersOf(res.Account)
	if len(ids) != 1 || ids[0].Value != "ann@acme.com" || ids[0].VerifiedVia != "idp:"+idpAcme {
		t.Errorf("identifiers = %+v, want the address verified through the provider", ids)
	}
	m, ok := e.s.membershipOf(tenantAcme, res.Account)
	if !ok || !m.Active || m.Source != "jit:"+idpAcme {
		t.Errorf("membership = %+v, %v, want an active one sourced jit:%s", m, ok, idpAcme)
	}
	if len(e.s.membershipPuts) != 1 {
		t.Errorf("%d membership writes, want exactly the one", len(e.s.membershipPuts))
	}
	if got := e.s.created[0]; got.Via != "jit" || got.Kind != authn.KindUser {
		t.Errorf("Create = %+v, want a user with via jit", got)
	}
	if got, ok := e.s.boundTo("oidc:"+idpAcme, "u1"); !ok || got != res.Account {
		t.Errorf("binding = %v, %v, want it on the new account", got, ok)
	}
}

func TestEnterpriseJITIgnoresTheSignupSwitch(t *testing.T) {
	e := newEnv(t, withProvider(authn.ProvisioningJIT, false), withSignup(SignupClosed))
	if res, err := e.resolve(enterprise("u1", "ann@acme.com")); err != nil || res.Outcome != OutcomeCreated {
		t.Errorf("Resolve = %+v, %v, want an account created", res, err)
	}
}

func TestEnterpriseJITAddressTakenIsLinkRequiredByDefault(t *testing.T) {
	// merge_existing is off: even an account that passes every condition of the
	// merge is not merged into.
	e := newEnv(t, withProvider(authn.ProvisioningJIT, false))
	e.s.seed(t, seed{id: "a1", status: authn.StatusActive, email: "ann@acme.com", emailVia: "idp:google"})
	_, err := e.resolve(enterprise("u1", "ann@acme.com"))
	if !errors.Is(err, authn.ErrLinkRequired) {
		t.Fatalf("err = %v, want ErrLinkRequired", err)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none", e.s.writes())
	}
}

func mergeBase() seed {
	return seed{id: "a1", status: authn.StatusActive, email: "ann@acme.com", emailVia: "idp:google", tenants: []string{tenantAcme}}
}

func TestEnterpriseMergeExistingMergesAndOwesTheCleanupInOrder(t *testing.T) {
	for _, tenants := range [][]string{{tenantAcme}, nil} {
		t.Run(fmt.Sprint(tenants), func(t *testing.T) {
			e := newEnv(t, withProvider(authn.ProvisioningJIT, true))
			sd := mergeBase()
			sd.tenants = tenants
			e.s.seed(t, sd)
			res, err := e.resolve(enterprise("u1", "ann@acme.com"))
			if err != nil || res.Account != "a1" || res.Outcome != OutcomeMerged {
				t.Fatalf("Resolve = %+v, %v, want a merge into a1", res, err)
			}
			if !slices.Equal(res.Cleanup, []Cleanup{CleanupRevokeSessions, CleanupClearPassword}) {
				t.Errorf("Cleanup = %v, want revoke first and clear second", res.Cleanup)
			}
			if got, ok := e.s.boundTo("oidc:"+idpAcme, "u1"); !ok || got != "a1" {
				t.Errorf("binding = %v, %v, want it on a1", got, ok)
			}
			if len(e.s.membershipPuts) != 0 || len(e.s.created) != 0 {
				t.Errorf("%d membership writes, %d accounts created, want none: the merge writes the binding alone", len(e.s.membershipPuts), len(e.s.created))
			}
			if res.MembershipCreated {
				t.Error("a merge reported a membership created")
			}
		})
	}
}

func TestEnterpriseMergeKeepsASuspendedMembershipSuspended(t *testing.T) {
	e := newEnv(t, withProvider(authn.ProvisioningJIT, true))
	sd := mergeBase()
	sd.tenants, sd.suspended = nil, []string{tenantAcme}
	e.s.seed(t, sd)
	if _, err := e.resolve(enterprise("u1", "ann@acme.com")); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if m, ok := e.s.membershipOf(tenantAcme, "a1"); !ok || m.Active {
		t.Errorf("membership = %+v, %v, want it still suspended", m, ok)
	}
}

// Each case of the table breaks one condition of the merge, and every one of
// them leaves the account untouched and answers link-required.
func TestEnterpriseMergeConditionsEachRefuseTheMerge(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*seed)
		opts   []option
	}{
		{name: "address asserted by an administrator", mutate: func(s *seed) { s.emailVia = authn.VerifiedViaAdmin }},
		{name: "address recorded by an import", mutate: func(s *seed) { s.emailVia = authn.VerifiedViaImportUnverified }},
		{name: "password set", mutate: func(s *seed) { s.password = true }},
		{name: "phone held", mutate: func(s *seed) { s.phone = "+8613800000001" }},
		{name: "another binding", mutate: func(s *seed) { s.bindings = [][2]string{{"google", "g1"}} }},
		{name: "member of another tenant", mutate: func(s *seed) { s.tenants = []string{tenantAcme, tenantElse} }},
		{name: "member of only another tenant", mutate: func(s *seed) { s.tenants = []string{tenantElse} }},
		{name: "suspended in another tenant", mutate: func(s *seed) { s.suspended = []string{tenantElse} }},
		{name: "a factor", mutate: func(s *seed) { s.factors = 1 }},
		{name: "account disabled", mutate: func(s *seed) { s.status = authn.StatusDisabled }},
		{name: "no way to ask about factors", mutate: func(*seed) {}, opts: []option{withoutFactors()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, append([]option{withProvider(authn.ProvisioningJIT, true)}, tc.opts...)...)
			sd := mergeBase()
			tc.mutate(&sd)
			e.s.seed(t, sd)
			before := e.s.account("a1")
			res, err := e.resolve(enterprise("u1", "ann@acme.com"))
			if !errors.Is(err, authn.ErrLinkRequired) {
				t.Fatalf("Resolve = %+v, %v, want ErrLinkRequired", res, err)
			}
			if len(res.Cleanup) != 0 {
				t.Errorf("Cleanup = %v, want none for a refusal", res.Cleanup)
			}
			if _, ok := e.s.boundTo("oidc:"+idpAcme, "u1"); ok || e.s.writes() != 0 {
				t.Errorf("the identity was bound or something was written (%d writes)", e.s.writes())
			}
			if after := e.s.account("a1"); after.PasswordSet != before.PasswordSet || after.Status != before.Status {
				t.Errorf("account changed from %+v to %+v", before, after)
			}
		})
	}
}

// A dependency failing while the conditions of the merge are read leaves the
// account unmerged and the login failed, for every read the merge makes.
func TestEnterpriseMergeFailsClosedWhenADependencyFails(t *testing.T) {
	cases := map[string]func(*store, error){
		"the factor store":     func(s *store, err error) { s.failFactors = err },
		"the binding store":    func(s *store, err error) { s.failExtern = err },
		"the membership store": func(s *store, err error) { s.failTenants = err },
	}
	for name, arm := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, withProvider(authn.ProvisioningJIT, true))
			e.s.seed(t, mergeBase())
			boom := errors.New("dependency down")
			arm(e.s, boom)
			_, err := e.resolve(enterprise("u1", "ann@acme.com"))
			if !errors.Is(err, boom) {
				t.Fatalf("err = %v, want the dependency's failure", err)
			}
			if _, ok := e.s.boundTo("oidc:"+idpAcme, "u1"); ok || e.s.writes() != 0 {
				t.Errorf("the identity was bound or something was written although a dependency failed (%d writes)", e.s.writes())
			}
		})
	}
}

func TestEnterpriseRaceOnTheAddressIsLinkRequired(t *testing.T) {
	e := newEnv(t, withProvider(authn.ProvisioningJIT, false))
	e.s.beforeCreate = func(in authn.CreateAccount) {
		e.s.seed(t, seed{id: "late", status: authn.StatusActive, email: in.Email, emailVia: "idp:google"})
	}
	_, err := e.resolve(enterprise("u1", "ann@acme.com"))
	if !errors.Is(err, authn.ErrLinkRequired) {
		t.Errorf("err = %v, want ErrLinkRequired", err)
	}
	if _, ok := e.s.boundTo("oidc:"+idpAcme, "u1"); ok {
		t.Error("the identity was bound")
	}
}

func TestEnterpriseIdentityIsHeldToItsProvider(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*authn.Proof)
	}{
		{"keyed in the social namespace", func(p *authn.Proof) { p.External.Provider = "google" }},
		{"keyed under another provider", func(p *authn.Proof) { p.External.Provider = "oidc:" + idpOther }},
		{"keyed in no namespace", func(p *authn.Proof) { p.External.Provider = idpAcme }},
		{"another tenant's name", func(p *authn.Proof) { p.External.Tenant = tenantElse }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, withProvider(authn.ProvisioningJIT, true))
			p := enterprise("u1", "ann@acme.com")
			tc.mutate(&p)
			_, err := e.resolve(p)
			if !errors.Is(err, authn.ErrInvalidArgument) || e.s.writes() != 0 {
				t.Errorf("err = %v with %d writes, want ErrInvalidArgument and none", err, e.s.writes())
			}
		})
	}
}

func TestEnterpriseSAMLNamespaceIsItsProvidersToo(t *testing.T) {
	e := newEnv(t, withProvider(authn.ProvisioningJIT, false))
	p := enterprise("u1", "ann@acme.com")
	p.External.Provider = "saml:" + idpAcme
	if res, err := e.resolve(p); err != nil || res.Outcome != OutcomeCreated {
		t.Errorf("Resolve = %+v, %v", res, err)
	}
}

func TestEnterpriseWithoutARegisterIsAnUnavailableDependency(t *testing.T) {
	e := newEnv(t, withProvider(authn.ProvisioningJIT, false), withoutSSO())
	_, err := e.resolve(enterprise("u1", "ann@acme.com"))
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestEnterpriseProviderRowThatCannotBeReadFailsTheLogin(t *testing.T) {
	e := newEnv(t)
	if _, err := e.resolve(enterprise("u1", "ann@acme.com")); err == nil {
		t.Error("an unknown provider resolved")
	}
}

func TestEnterpriseUnknownProvisioningIsRefused(t *testing.T) {
	e := newEnv(t, withProvider("surprise", true))
	_, err := e.resolve(enterprise("u1", "ann@acme.com"))
	if !errors.Is(err, authn.ErrInvalidArgument) || e.s.writes() != 0 {
		t.Errorf("err = %v with %d writes, want ErrInvalidArgument and none", err, e.s.writes())
	}
}
