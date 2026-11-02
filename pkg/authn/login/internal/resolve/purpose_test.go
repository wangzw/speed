package resolve

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

func (e *env) resolveFor(purpose string, account authn.AccountID, p authn.Proof) (Result, error) {
	e.t.Helper()
	return e.r.Resolve(context.Background(), Request{Proof: p, Purpose: purpose, Account: account})
}

// A link, a step-up and an enrollment never create, link, merge or touch a
// membership; the explicit link of A6 writes nothing before the caller acts on
// it.

func TestLinkAnswersWhetherTheKeyIsFreeAndWritesNothing(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	e.s.seed(t, seed{id: "a1", status: authn.StatusActive, password: true, email: "ann@home.org", emailVia: "idp:google"})
	e.s.seed(t, seed{id: "b1", status: authn.StatusActive, bindings: [][2]string{{"github", "gh-taken"}}})

	// A key nobody holds, with an address an account already carries.
	res, err := e.resolveFor(authn.PurposeLink, "a1", social("google", "g1", "ann@home.org", true))
	if err != nil || res.Account != "a1" || res.Outcome != OutcomeVerified {
		t.Fatalf("Resolve = %+v, %v, want a1 verified", res, err)
	}
	// A key another account holds.
	if _, err := e.resolveFor(authn.PurposeLink, "a1", social("github", "gh-taken", "", false)); !errors.Is(err, authn.ErrBindingConflict) {
		t.Errorf("err = %v, want ErrBindingConflict", err)
	}
	// A key this account holds already.
	if res, err := e.resolveFor(authn.PurposeLink, "b1", social("github", "gh-taken", "", false)); err != nil || res.Account != "b1" {
		t.Errorf("Resolve = %+v, %v, want b1", res, err)
	}
	if e.s.writes() != 0 || e.s.membershipRead != 0 {
		t.Errorf("%d writes and %d membership reads, want none", e.s.writes(), e.s.membershipRead)
	}
}

func TestLinkIsOfAnExternalIdentityForTheFlowsAccountOnly(t *testing.T) {
	e := newEnv(t)
	e.s.seed(t, seed{id: "a1", status: authn.StatusActive})
	e.s.seed(t, seed{id: "off", status: authn.StatusDisabled})
	if _, err := e.resolveFor(authn.PurposeLink, "a1", authn.Proof{Account: "a1"}); !errors.Is(err, authn.ErrInvalidArgument) {
		t.Errorf("a link of a password proof: err = %v, want ErrInvalidArgument", err)
	}
	if _, err := e.resolveFor(authn.PurposeLink, "off", social("google", "g1", "", false)); !errors.Is(err, authn.ErrAccountUnavailable) {
		t.Errorf("a link on a disabled account: err = %v, want ErrAccountUnavailable", err)
	}
	if _, err := e.resolveFor(authn.PurposeLink, "ghost", social("google", "g1", "", false)); !errors.Is(err, authn.ErrAccountNotFound) {
		t.Errorf("a link on a missing account: err = %v, want ErrAccountNotFound", err)
	}
	if _, err := e.r.Resolve(context.Background(), Request{Proof: social("google", "g1", "", false), Purpose: authn.PurposeLink}); !errors.Is(err, authn.ErrInvalidArgument) {
		t.Errorf("a link with no account: err = %v, want ErrInvalidArgument", err)
	}
}

func TestStepUpAndEnrollNeedTheProofToBeTheFlowsOwn(t *testing.T) {
	for _, purpose := range []string{authn.PurposeStepUp, authn.PurposeEnroll} {
		t.Run(purpose, func(t *testing.T) {
			e := newEnv(t, withSignup(SignupOpen))
			e.s.seed(t, seed{id: "a1", status: authn.StatusActive, phone: "+8613800000001", bindings: [][2]string{{"google", "g1"}}})
			e.s.seed(t, seed{id: "b1", status: authn.StatusActive, phone: "+8613800000002", bindings: [][2]string{{"google", "g2"}}})
			// A login name and its password arrive as the account source: the
			// password method resolves them to an account and names no
			// identifier (resolve accepts only a phone identifier, and the
			// phone has its own case).
			own := map[string]authn.Proof{
				"account":  {Account: "a1"},
				"password": {Account: "a1", Methods: []authn.Method{authn.MethodPassword}},
				"phone":    phone("+8613800000001"),
				"external": social("google", "g1", "", false),
			}
			for name, p := range own {
				if res, err := e.resolveFor(purpose, "a1", p); err != nil || res.Account != "a1" || res.Outcome != OutcomeVerified {
					t.Errorf("%s: %+v, %v, want a1 verified", name, res, err)
				}
			}
			foreign := map[string]authn.Proof{
				"account":     {Account: "b1"},
				"password":    {Account: "b1", Methods: []authn.Method{authn.MethodPassword}},
				"phone":       phone("+8613800000002"),
				"external":    social("google", "g2", "", false),
				"unknown":     phone("+8613800000099"),
				"unbound key": social("google", "g-new", "", false),
			}
			for name, p := range foreign {
				if _, err := e.resolveFor(purpose, "a1", p); !errors.Is(err, authn.ErrInvalidCredentials) {
					t.Errorf("%s: err = %v, want ErrInvalidCredentials", name, err)
				}
			}
			if e.s.writes() != 0 {
				t.Errorf("%d writes, want none: a step-up creates and links nothing", e.s.writes())
			}
		})
	}
}

func TestStepUpAcceptsAnEarlierWeChatKeyOfTheFlowsAccount(t *testing.T) {
	e := newEnv(t)
	e.s.seed(t, seed{id: "a1", status: authn.StatusActive, bindings: [][2]string{{"wechat-openid:app1", "o1"}}})
	if res, err := e.resolveFor(authn.PurposeStepUp, "a1", wechat("u1", "o1")); err != nil || res.Account != "a1" {
		t.Errorf("Resolve = %+v, %v", res, err)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none", e.s.writes())
	}
}

func TestPurposeAndSourceAreChecked(t *testing.T) {
	e := newEnv(t)
	for name, req := range map[string]Request{
		"no purpose":      {Proof: authn.Proof{Account: "a1"}},
		"unknown purpose": {Proof: authn.Proof{Account: "a1"}, Purpose: "signup"},
		"no source":       {Purpose: authn.PurposeLogin},
		"two sources":     {Proof: authn.Proof{Account: "a1", Identifier: &authn.Identifier{Kind: authn.IdentifierPhone, Value: "+1"}}, Purpose: authn.PurposeLogin},
		"three sources": {Proof: authn.Proof{
			Account: "a1", Identifier: &authn.Identifier{Kind: authn.IdentifierPhone, Value: "+1"},
			External: &authn.External{Provider: "google", Subject: "g"},
		}, Purpose: authn.PurposeLogin},
		"no provider": {Proof: authn.Proof{External: &authn.External{Subject: "g"}}, Purpose: authn.PurposeLogin},
		"no subject":  {Proof: authn.Proof{External: &authn.External{Provider: "google"}}, Purpose: authn.PurposeLogin},
	} {
		if res, err := e.r.Resolve(context.Background(), req); !errors.Is(err, authn.ErrInvalidArgument) || res.Account != "" || res.Outcome != 0 {
			t.Errorf("%s: %+v, %v, want ErrInvalidArgument and an empty result", name, res, err)
		}
	}
}
