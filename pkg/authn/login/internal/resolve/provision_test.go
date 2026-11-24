package resolve

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestProvisionerChangesDisplayNameAndAttributesAndNothingElse checks that the hook may change only those two fields.
func TestProvisionerChangesDisplayNameAndAttributesAndNothingElse(t *testing.T) {
	var gotVia string
	var gotIn authn.CreateAccount
	hook := func(_ context.Context, p authn.CreateAccount, via string) (authn.CreateAccount, error) {
		gotVia, gotIn = via, p.Clone()
		p.DisplayName = "Ann (host)"
		p.Attributes = map[string]string{"region": "eu"}
		p.ID, p.Kind = "forged", authn.KindService
		p.Email, p.EmailVerifiedVia = "evil@x.org", authn.Verification{Via: authn.VerifiedViaAdmin}
		p.Phone, p.PhoneVerifiedVia = "+1555", authn.Verification{Via: authn.VerifiedViaAdmin}
		p.Password, p.PasswordHash, p.MustChangePassword = "pw", "$hash", true
		p.MFARequired, p.Via, p.Username = true, "import", "root"
		return p, nil
	}
	e := newEnv(t, withSignup(SignupOpen), withProvisioner(hook))
	res, err := e.resolve(social("google", "g1", "ann@home.org", true))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if gotVia != "signup" || gotIn.Email != "ann@home.org" || gotIn.Kind != authn.KindUser {
		t.Errorf("the hook was given via %q and %+v, want signup and the account about to be made", gotVia, gotIn)
	}
	c := e.s.created[0]
	if c.DisplayName != "Ann (host)" || c.Attributes["region"] != "eu" {
		t.Errorf("Create = %+v, want the host's name and attributes", c)
	}
	want := authn.CreateAccount{
		Kind: authn.KindUser, DisplayName: "Ann (host)", Username: testUsername,
		UsernameVerifiedVia: authn.Verification{Via: authn.VerifiedViaSelf},
		Email:               "ann@home.org", EmailVerifiedVia: authn.Verification{Via: "idp:google"},
		Password:   testPassword,
		Attributes: map[string]string{"region": "eu"},
		Via:        "signup",
	}
	if fmt.Sprint(c) != fmt.Sprint(want) {
		t.Errorf("Create = %+v, want %+v: only the display name and attributes may change", c, want)
	}
	if a := e.s.account(res.Account); a.Kind != authn.KindUser || a.MFARequired || !a.PasswordSet {
		t.Errorf("account = %+v, want the forged fields ignored and the collected password written", a)
	}
}

// TestProvisionerSeesTheViaOfEachFirstLogin checks that the hook sees via signup
// for a social and a phone first login and via jit for an enterprise one.
func TestProvisionerSeesTheViaOfEachFirstLogin(t *testing.T) {
	var vias []string
	hook := func(_ context.Context, p authn.CreateAccount, via string) (authn.CreateAccount, error) {
		vias = append(vias, via)
		return p, nil
	}
	e := newEnv(t, withSignup(SignupOpen), withProvisioner(hook), withProvider(authn.ProvisioningJIT, false))
	for _, p := range []authn.Proof{social("github", "gh1", "", false), phone("+8613800000001"), enterprise("u1", "ann@acme.com")} {
		if _, err := e.resolve(p); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
	}
	if fmt.Sprint(vias) != "[signup signup jit]" {
		t.Errorf("vias = %v, want signup for social and phone and jit for an enterprise provider", vias)
	}
}

// TestProvisionerRefusalCreatesNothing checks that a hook's refusal is reported
// as a provision refusal, not an unavailable dependency, and creates no account.
func TestProvisionerRefusalCreatesNothing(t *testing.T) {
	refuse := func(context.Context, authn.CreateAccount, string) (authn.CreateAccount, error) {
		return authn.CreateAccount{}, fmt.Errorf("no sign-ups from this domain: %w", authn.ErrProvisionRefused)
	}
	for name, tc := range map[string]struct {
		proof authn.Proof
		opts  []option
	}{
		"social":     {social("github", "gh1", "", false), nil},
		"phone":      {phone("+8613800000001"), nil},
		"enterprise": {enterprise("u1", "ann@acme.com"), []option{withProvider(authn.ProvisioningJIT, false)}},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, append([]option{withSignup(SignupOpen), withProvisioner(refuse)}, tc.opts...)...)
			_, err := e.resolve(tc.proof)
			if !errors.Is(err, authn.ErrProvisionRefused) || errors.Is(err, authn.ErrUnavailable) {
				t.Errorf("err = %v, want ErrProvisionRefused and not an unavailable dependency", err)
			}
			if e.s.writes() != 0 {
				t.Errorf("%d writes, want none", e.s.writes())
			}
		})
	}
}

// TestProvisionerFaultIsAnUnavailableDependencyAndCreatesNothing checks that a hook fault reads as unavailable and creates nothing.
func TestProvisionerFaultIsAnUnavailableDependencyAndCreatesNothing(t *testing.T) {
	fault := func(context.Context, authn.CreateAccount, string) (authn.CreateAccount, error) {
		return authn.CreateAccount{DisplayName: "half a result"}, fmt.Errorf("db down: %w", authn.ErrLinkRequired)
	}
	e := newEnv(t, withSignup(SignupOpen), withProvisioner(fault))
	_, err := e.resolve(social("github", "gh1", "", false))
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
	if errors.Is(err, authn.ErrProvisionRefused) || errors.Is(err, authn.ErrLinkRequired) {
		t.Errorf("err = %v, must not read as a refusal or as the sentinel the hook wrapped", err)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none: nothing is created from half a result", e.s.writes())
	}
}

// TestWithoutAProvisionerTheAccountIsMadeFromTheProvidersName checks that
// without a hook the account takes the provider's name and no attributes.
func TestWithoutAProvisionerTheAccountIsMadeFromTheProvidersName(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	res, err := e.resolve(social("github", "gh1", "", false))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if a := e.s.account(res.Account); a.DisplayName != "Ann" || a.Attributes != nil {
		t.Errorf("account = %+v, want the provider's name and no attributes", a)
	}
}

// TestProvisionerIsNotAskedWhenNothingIsCreated checks that the hook is not
// asked when the resolution creates no account.
func TestProvisionerIsNotAskedWhenNothingIsCreated(t *testing.T) {
	asked := 0
	hook := func(_ context.Context, p authn.CreateAccount, _ string) (authn.CreateAccount, error) {
		asked++
		return p, nil
	}
	e := newEnv(t, withProvisioner(hook), withProvider(authn.ProvisioningPreprovisioned, false))
	e.s.seed(t, seed{id: "a1", status: authn.StatusActive, email: "ann@acme.com", emailVia: authn.VerifiedViaAdmin, bindings: [][2]string{{"google", "g1"}}, phone: "+8613800000001"})
	for _, p := range []authn.Proof{
		social("google", "g1", "", false),
		social("github", "gh1", "", false),
		phone("+8613800000001"),
		enterprise("u1", "ann@acme.com"),
		enterprise("u2", "bob@acme.com"),
		enterprise("u3", "x@elsewhere.com"),
	} {
		_, _ = e.resolve(p)
	}
	if asked != 0 {
		t.Errorf("the hook was asked %d times, want 0", asked)
	}
}

// TestProvisionerNameIsCutLikeTheProvidersOwn checks that a display name the hook
// returns is cut to 100 characters like the provider's own.
func TestProvisionerNameIsCutLikeTheProvidersOwn(t *testing.T) {
	hook := func(_ context.Context, p authn.CreateAccount, _ string) (authn.CreateAccount, error) {
		p.DisplayName = strings.Repeat("x", 300)
		return p, nil
	}
	e := newEnv(t, withSignup(SignupOpen), withProvisioner(hook))
	if _, err := e.resolve(social("github", "gh1", "", false)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if n := len(e.s.created[0].DisplayName); n != 100 {
		t.Errorf("display name is %d characters, want 100", n)
	}
}

// TestProvisionerReturningOnlyANameLeavesTheRestOfTheAccountAlone checks that a
// hook returning only a name leaves the rest of the account to the resolver.
func TestProvisionerReturningOnlyANameLeavesTheRestOfTheAccountAlone(t *testing.T) {
	hook := func(context.Context, authn.CreateAccount, string) (authn.CreateAccount, error) {
		return authn.CreateAccount{DisplayName: "kept"}, nil
	}
	e := newEnv(t, withSignup(SignupOpen), withProvisioner(hook))
	if _, err := e.resolve(social("google", "g1", "ann@home.org", true)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	c := e.s.created[0]
	if c.Email != "ann@home.org" || c.Kind != authn.KindUser || c.DisplayName != "kept" || len(c.Attributes) != 0 {
		t.Errorf("Create = %+v, want the resolver's own account with the hook's name", c)
	}
}

// TestAFirstLoginThatWouldOpenAnAccountParksAtThePendingSignup holds that the facts are carried and nothing is written before a username.
func TestAFirstLoginThatWouldOpenAnAccountParksAtThePendingSignup(t *testing.T) {
	for name, tc := range map[string]struct {
		proof  authn.Proof
		opts   []option
		source Source
	}{
		"social":     {social("github", "gh1", "", false), nil, SourceSocial},
		"phone":      {phone("+8613800000001"), nil, SourcePhone},
		"enterprise": {enterprise("u1", "ann@acme.com"), []option{withProvider(authn.ProvisioningJIT, false)}, SourceEnterprise},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, append([]option{withSignup(SignupOpen)}, tc.opts...)...)
			res, err := e.pending(tc.proof)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if res.Pending == nil || res.Pending.Source != tc.source {
				t.Fatalf("Resolve = %+v, want a pending %s signup", res, tc.source)
			}
			if res.Account != "" || res.Outcome != 0 {
				t.Errorf("Resolve = %+v, want no account and no outcome yet", res)
			}
			if e.s.writes() != 0 {
				t.Errorf("%d writes, want none: the account is opened only after the username", e.s.writes())
			}
		})
	}
}

// TestAFirstLoginCreatesTheAccountWithTheCollectedUsername holds that the collected username is used on all three opening paths.
func TestAFirstLoginCreatesTheAccountWithTheCollectedUsername(t *testing.T) {
	for name, tc := range map[string]struct {
		proof authn.Proof
		opts  []option
	}{
		"social":     {social("github", "gh1", "", false), nil},
		"phone":      {phone("+8613800000001"), nil},
		"enterprise": {enterprise("u1", "ann@acme.com"), []option{withProvider(authn.ProvisioningJIT, false)}},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, append([]option{withSignup(SignupOpen)}, tc.opts...)...)
			res, err := e.resolveAs("collected", tc.proof)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if res.Outcome != OutcomeCreated {
				t.Fatalf("outcome = %v, want created", res.Outcome)
			}
			if got := e.s.created[len(e.s.created)-1].Username; got != "collected" {
				t.Errorf("Create was given the username %q, want collected", got)
			}
		})
	}
}
