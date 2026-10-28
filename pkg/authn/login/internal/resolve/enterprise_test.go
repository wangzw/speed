package resolve

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// wantNamedLinkRequired asserts a refusal is the link-required kind and names
// the address whose existing account collided. Every enterprise JIT branch that
// refuses this way must carry the address, because the landing fragment names
// it to the caller the provider proved.
func wantNamedLinkRequired(t *testing.T, err error, want string) {
	t.Helper()
	var conflicted *authn.LinkRequiredError
	if !errors.As(err, &conflicted) {
		t.Fatalf("err = %v, want a *authn.LinkRequiredError", err)
	}
	if conflicted.Email != want {
		t.Errorf("the refusal names %q, want %q", conflicted.Email, want)
	}
}

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

// A preprovisioned provider matches an account an administrator prepared for the
// address, whatever channel the address carries except an import's unproven
// record: an administrator's assertion, a provider's, and an invitation's all
// count.
func TestEnterprisePreprovisionedMatchesAnAdministratorsAccount(t *testing.T) {
	for _, via := range []string{authn.VerifiedViaAdmin, authn.VerifiedViaInvite, "idp:google"} {
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
	if ids := e.s.identifiersOf(res.Account); len(ids) != 0 {
		t.Errorf("identifiers = %+v, want none: the provider's address is not written", ids)
	}
	if got := e.s.created[0]; got.Email != "" || got.EmailVerifiedVia.Valid() {
		t.Errorf("Create carried the address %+v, want none", got)
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

func TestEnterpriseJITAddressTakenIsLinkRequired(t *testing.T) {
	// No login merges into the account an address belongs to, whatever the
	// provider's merge_existing says: the login must bind explicitly.
	for _, merge := range []bool{false, true} {
		t.Run("merge_existing="+map[bool]string{false: "off", true: "on"}[merge], func(t *testing.T) {
			e := newEnv(t, withProvider(authn.ProvisioningJIT, merge))
			e.s.seed(t, seed{id: "a1", status: authn.StatusActive, email: "ann@acme.com", emailVia: "idp:google"})
			_, err := e.resolve(enterprise("u1", "ann@acme.com"))
			if !errors.Is(err, authn.ErrLinkRequired) {
				t.Fatalf("err = %v, want ErrLinkRequired", err)
			}
			wantNamedLinkRequired(t, err, "ann@acme.com")
			if e.s.writes() != 0 {
				t.Errorf("%d writes, want none", e.s.writes())
			}
			if _, ok := e.s.boundTo("oidc:"+idpAcme, "u1"); ok {
				t.Error("the identity was bound")
			}
		})
	}
}

func TestEnterpriseAddressTakenEvenByAnUnprovedRecordIsLinkRequired(t *testing.T) {
	// The address is a clue to an account whenever an account holds it; the
	// channel that proved it does not turn the collision away.
	e := newEnv(t, withProvider(authn.ProvisioningJIT, false))
	e.s.seed(t, seed{id: "a1", status: authn.StatusActive, email: "ann@acme.com", emailVia: authn.VerifiedViaAdmin})
	if _, err := e.resolve(enterprise("u1", "ann@acme.com")); !errors.Is(err, authn.ErrLinkRequired) {
		t.Errorf("err = %v, want ErrLinkRequired", err)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none", e.s.writes())
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
