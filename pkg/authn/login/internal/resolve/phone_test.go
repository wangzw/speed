package resolve

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

func TestPhoneUnknownWithSignupClosedIsRefused(t *testing.T) {
	e := newEnv(t)
	_, err := e.resolve(phone("+8613800000001"))
	if !errors.Is(err, authn.ErrSignupClosed) || e.s.writes() != 0 {
		t.Errorf("err = %v with %d writes, want ErrSignupClosed and none", err, e.s.writes())
	}
}

func TestPhoneUnknownWithSignupOpenCreatesAnAccountWithTheProvedPhone(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	res, err := e.resolve(phone("+8613800000001"))
	if err != nil || res.Outcome != OutcomeCreated || res.MembershipCreated {
		t.Fatalf("Resolve = %+v, %v", res, err)
	}
	ids := e.s.identifiersOf(res.Account)
	if len(ids) != 1 || ids[0].Kind != authn.IdentifierPhone || ids[0].VerifiedVia != authn.VerifiedViaSMSCode {
		t.Errorf("identifiers = %+v, want the phone verified by sms-code", ids)
	}
	c := e.s.created[0]
	if c.Kind != authn.KindUser || c.Via != "signup" || c.PhoneVerifiedVia.Via != authn.VerifiedViaSMSCode {
		t.Errorf("Create = %+v", c)
	}
	if e.s.membershipRead != 0 || len(e.s.membershipPuts) != 0 {
		t.Error("a phone login touched memberships")
	}
}

func TestPhoneWrittenVerificationIsNotTheMethodsClaim(t *testing.T) {
	// The channel on the new identifier is the one the resolver knows a code
	// proves, not whatever the method put on its identifier.
	e := newEnv(t, withSignup(SignupOpen))
	p := phone("+8613800000001")
	p.Identifier.VerifiedVia = authn.VerifiedViaAdmin
	if _, err := e.resolve(p); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := e.s.created[0].PhoneVerifiedVia.Via; got != authn.VerifiedViaSMSCode {
		t.Errorf("PhoneVerifiedVia = %q, want sms-code", got)
	}
}

func TestPhoneProofIsOnlyAPhone(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	for name, p := range map[string]authn.Proof{
		"email":    {Identifier: &authn.Identifier{Kind: authn.IdentifierEmail, Value: "a@b.org"}},
		"username": {Identifier: &authn.Identifier{Kind: authn.IdentifierUsername, Value: "ann"}},
		"empty":    {Identifier: &authn.Identifier{Kind: authn.IdentifierPhone}},
	} {
		if _, err := e.resolve(p); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("%s: err = %v, want ErrInvalidArgument", name, err)
		}
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none", e.s.writes())
	}
}

func TestPhoneThatIsNotWellFormedIsPassedOn(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	if _, err := e.resolve(phone("0138")); !errors.Is(err, authn.ErrInvalidIdentifier) {
		t.Errorf("err = %v, want ErrInvalidIdentifier", err)
	}
}

func TestPhoneCreateFailureIsPassedOn(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	e.s.failCreate = authn.ErrUnavailable
	if _, err := e.resolve(phone("+8613800000001")); !errors.Is(err, authn.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}
