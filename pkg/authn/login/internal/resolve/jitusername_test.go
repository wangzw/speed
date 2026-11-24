package resolve

import (
	"errors"
	"fmt"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestClaimStrategyOpensTheAccountImmediately holds that the assertion's username is normalized and the account takes the provider's name and membership.
func TestClaimStrategyOpensTheAccountImmediately(t *testing.T) {
	e := newEnv(t, withClaimProvider(authn.UsernameClaimPreferredUsername))
	res, err := e.resolve(enterpriseWithUsername("u1", "ann@acme.com", "Ann-Smith"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Pending != nil {
		t.Fatalf("the claim strategy parked at the pending signup: %+v", res.Pending)
	}
	if res.Outcome != OutcomeCreated || !res.MembershipCreated {
		t.Fatalf("Resolve = %+v, want a created account with a membership", res)
	}
	got := e.s.created[0]
	if got.Username != "ann-smith" {
		t.Errorf("Create carried the username %q, want the normalized claim", got.Username)
	}
	if got.Via != "jit" || got.Kind != authn.KindUser {
		t.Errorf("Create = %+v, want a user with via jit", got)
	}
	if got.UsernameVerifiedVia.Via != authn.VerifiedViaIDPPrefix+idpAcme {
		t.Errorf("the username's channel is %q, want the provider's", got.UsernameVerifiedVia.Via)
	}
	if m, ok := e.s.membershipOf(tenantAcme, res.Account); !ok || !m.Active || m.Source != "jit:"+idpAcme {
		t.Errorf("membership = %+v, %v, want an active one sourced jit:%s", m, ok, idpAcme)
	}
	if got, ok := e.s.boundTo("oidc:"+idpAcme, "u1"); !ok || got != res.Account {
		t.Errorf("binding = %v, %v, want it on the new account", got, ok)
	}
}

// The email claim's value is the address's local part, normalized the same way.
func TestClaimStrategyReadsTheEmailLocalPart(t *testing.T) {
	e := newEnv(t, withClaimProvider(authn.UsernameClaimEmail))
	res, err := e.resolve(enterprise("u1", "Ann-Smith@acme.com"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Outcome != OutcomeCreated {
		t.Fatalf("Resolve = %+v, want a created account", res)
	}
	if got := e.s.created[0].Username; got != "ann-smith" {
		t.Errorf("Create carried the username %q, want the address's local part", got)
	}
}

// A claim the assertion does not carry refuses the login, and nothing is
// written: no account, binding or membership.
func TestClaimStrategyRefusesAMissingClaim(t *testing.T) {
	e := newEnv(t, withClaimProvider(authn.UsernameClaimPreferredUsername))
	_, err := e.resolve(enterpriseWithUsername("u1", "ann@acme.com", ""))
	if !errors.Is(err, authn.ErrJITUsernameMissing) {
		t.Fatalf("err = %v, want ErrJITUsernameMissing", err)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none", e.s.writes())
	}
}

// TestEmailClaimRefusesALocalPartThatIsNotAUsername holds that a dotted local part is refused rather than folded.
func TestEmailClaimRefusesALocalPartThatIsNotAUsername(t *testing.T) {
	e := newEnv(t, withClaimProvider(authn.UsernameClaimEmail))
	_, err := e.resolve(enterprise("u1", "ann.smith@acme.com"))
	if !errors.Is(err, authn.ErrJITUsernameInvalid) {
		t.Fatalf("err = %v, want ErrJITUsernameInvalid", err)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none", e.s.writes())
	}
}

// A claim value that does not normalize to a username refuses the login, and
// nothing is written.
func TestClaimStrategyRefusesAnInvalidClaim(t *testing.T) {
	e := newEnv(t, withClaimProvider(authn.UsernameClaimPreferredUsername))
	_, err := e.resolve(enterpriseWithUsername("u1", "ann@acme.com", "Ann Smith!"))
	if !errors.Is(err, authn.ErrJITUsernameInvalid) {
		t.Fatalf("err = %v, want ErrJITUsernameInvalid", err)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none", e.s.writes())
	}
}

// TestClaimStrategyRefusesAValueTheAccountModuleKeepsForItself holds that a reserved value is a JIT username refusal.
func TestClaimStrategyRefusesAValueTheAccountModuleKeepsForItself(t *testing.T) {
	e := newEnv(t, withClaimProvider(authn.UsernameClaimPreferredUsername))
	e.s.failLookup = authn.UsernameShape(authn.ErrInvalidIdentifier)
	_, err := e.resolve(enterpriseWithUsername("u1", "ann@acme.com", "123e4567-e89b-12d3-a456-426614174000"))
	if !errors.Is(err, authn.ErrJITUsernameInvalid) {
		t.Fatalf("err = %v, want ErrJITUsernameInvalid", err)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none", e.s.writes())
	}
}

// A claim value another account already holds as a username refuses the login,
// and nothing is written.
func TestClaimStrategyRefusesATakenUsername(t *testing.T) {
	e := newEnv(t, withClaimProvider(authn.UsernameClaimPreferredUsername))
	e.s.seed(t, seed{id: "a1", status: authn.StatusActive, username: "ann"})
	_, err := e.resolve(enterpriseWithUsername("u1", "ann@acme.com", "ann"))
	if !errors.Is(err, authn.ErrJITUsernameTaken) {
		t.Fatalf("err = %v, want ErrJITUsernameTaken", err)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none", e.s.writes())
	}
}

// TestClaimStrategyMapsACreationTimeNameConflictToTheUsernameRefusal holds that a name held by a business tenant surfaces as the username refusal.
func TestClaimStrategyMapsACreationTimeNameConflictToTheUsernameRefusal(t *testing.T) {
	e := newEnv(t, withClaimProvider(authn.UsernameClaimPreferredUsername))
	e.s.failCreate = fmt.Errorf("%w: creating an account: %w", authn.ErrAccountExists,
		errors.New("personal: the personal tenant's name is held by another tenant"))
	_, err := e.resolve(enterpriseWithUsername("u1", "ann@acme.com", "ann"))
	if !errors.Is(err, authn.ErrJITUsernameTaken) {
		t.Fatalf("err = %v, want ErrJITUsernameTaken", err)
	}
	if errors.Is(err, authn.ErrUnavailable) {
		t.Errorf("err = %v, want the name taken, not an unavailable dependency", err)
	}
}

// A stored provider whose strategy is absent or unknown cannot determine
// a username, so the login is refused and no account is opened.
func TestJustInTimeRefusesAProviderWithNoUsableStrategy(t *testing.T) {
	for _, strategy := range []string{"", "surprise"} {
		t.Run("strategy="+strategy, func(t *testing.T) {
			e := newEnv(t, withProvider(authn.ProvisioningJIT, false))
			p := e.s.providers[idpAcme]
			p.UsernameStrategy = strategy
			e.s.providers[idpAcme] = p
			e.s.domains["acme.com"] = []authn.SSOProvider{p}
			_, err := e.resolve(enterprise("u1", "ann@acme.com"))
			if !errors.Is(err, authn.ErrJITUsernameStrategy) {
				t.Fatalf("err = %v, want ErrJITUsernameStrategy", err)
			}
			if e.s.writes() != 0 {
				t.Errorf("%d writes, want none", e.s.writes())
			}
		})
	}
}

// A claim strategy whose provider names a claim this build does not know is
// refused as a configuration failure, not as a missing claim.
func TestClaimStrategyRefusesAnUnknownClaimConfiguration(t *testing.T) {
	e := newEnv(t, withClaimProvider("surprise"))
	_, err := e.resolve(enterpriseWithUsername("u1", "ann@acme.com", "ann"))
	if !errors.Is(err, authn.ErrJITUsernameStrategy) {
		t.Fatalf("err = %v, want ErrJITUsernameStrategy", err)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none", e.s.writes())
	}
}

// TestUserConfirmStrategyAsksBeforeItOpensTheAccount holds that the resolution writes nothing until the person confirms a username.
func TestUserConfirmStrategyAsksBeforeItOpensTheAccount(t *testing.T) {
	e := newEnv(t, withProvider(authn.ProvisioningJIT, false))
	res, err := e.pending(enterprise("u1", "ann@acme.com"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Pending == nil || res.Pending.Source != SourceEnterprise {
		t.Fatalf("Resolve = %+v, want a pending enterprise signup", res)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes before the username answer, want none", e.s.writes())
	}
	created, err := e.r.Create(t.Context(), *res.Pending, "confirmed", "", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Outcome != OutcomeCreated {
		t.Fatalf("Create = %+v, want a created account", created)
	}
	got := e.s.created[0]
	if got.Username != "confirmed" || got.Via != "jit" {
		t.Errorf("Create carried %+v, want the confirmed username with via jit", got)
	}
	if got.UsernameVerifiedVia.Via != authn.VerifiedViaSelf {
		t.Errorf("the username's channel is %q, want self", got.UsernameVerifiedVia.Via)
	}
}

// A preprovisioned provider needs no username strategy: it never opens an
// account, and the existing fixture states none on purpose.
func TestPreprovisionedNeedsNoUsernameStrategy(t *testing.T) {
	e := newEnv(t, withProvider(authn.ProvisioningPreprovisioned, false))
	if got := e.s.providers[idpAcme].UsernameStrategy; got != "" {
		t.Fatalf("the fixture states the strategy %q, want it to state none", got)
	}
	id := e.s.seed(t, seed{
		id: "a1", status: authn.StatusActive, email: "ann@acme.com",
		emailVia: authn.VerifiedViaAdmin, tenants: []string{tenantAcme},
	})
	res, err := e.resolve(enterprise("u1", "ann@acme.com"))
	if err != nil || res.Account != id || res.Outcome != OutcomeMatched {
		t.Fatalf("Resolve = %+v, %v, want a1 matched", res, err)
	}
	if len(e.s.created) != 0 {
		t.Errorf("%d accounts created, want none", len(e.s.created))
	}
}
