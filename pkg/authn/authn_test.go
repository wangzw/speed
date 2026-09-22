package authn

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn/internal/identity"
	"github.com/vislake/speed/pkg/authn/internal/verdict"
)

// TestAVerdictNobodyAnsweredReadsAsSaidNothing holds the criterion the whole
// shape of a verdict rests on.
//
// A backend that returns before it answers, or that builds a verdict some way
// its author did not intend, hands back the zero value. Reading that as a pass
// would authenticate a caller no backend ever vouched for, which is why the
// fields are unexported and the constructors are the only way in. It is counted
// apart from a refusal as well: a refusal says the credential does not hold, a
// verdict nobody spoke says the backend is defective, and an operator needs to
// tell a wrong password from a broken plugin.
func TestAVerdictNobodyAnsweredReadsAsSaidNothing(t *testing.T) {
	var nobodyAnswered Verdict

	if got := verdict.OutcomeOf(nobodyAnswered); got != verdict.Unspoken {
		t.Errorf("a verdict nobody answered reads as %v, want %v: a backend that returns before it "+
			"answers would then be taken for one that vouched for the caller",
			got, verdict.Unspoken)
	}
	if subject := verdict.SubjectOf(nobodyAnswered); subject != "" {
		t.Errorf("a verdict nobody answered carries the subject %q, want none", subject)
	}
}

// TestEachConstructorSaysWhatItWasCalledFor holds the three answers apart. A
// constructor whose outcome drifted would send refusals into the pass arm or
// outages into the refusal arm, and the status code the caller sees is decided
// from nothing else.
func TestEachConstructorSaysWhatItWasCalledFor(t *testing.T) {
	broken := errors.New("the directory is unreachable")

	for _, c := range []struct {
		name string
		made Verdict
		want verdict.Outcome
	}{
		{"a credential that holds", Authenticated("alice"), verdict.Authenticated},
		{"a credential that does not", Denied(errors.New("wrong password")), verdict.Denied},
		{"a backend that cannot say", Unavailable(broken), verdict.Unavailable},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := verdict.OutcomeOf(c.made); got != c.want {
				t.Errorf("the verdict reads as %v, want %v", got, c.want)
			}
		})
	}
}

// TestAnAuthenticatedVerdictCarriesItsSubject pins the one payload that decides
// who the caller is. A subject lost between the backend and the gateway would
// leave an identity naming nobody.
func TestAnAuthenticatedVerdictCarriesItsSubject(t *testing.T) {
	if got := verdict.SubjectOf(Authenticated("alice")); got != "alice" {
		t.Errorf("the authenticated verdict carries the subject %q, want %q", got, "alice")
	}
}

// TestWhatARefusalCarriesDoesNotChangeThatItIsARefusal holds the one thing a
// backend must not be able to do with the cause it hands over.
//
// Were the cause allowed to decide, a backend could return the timeout the
// gateway uses for its own budget and turn a wrong password into an outage —
// and an outage is the status code every lockout, rate limiter and alert rule
// is keyed off not being.
func TestWhatARefusalCarriesDoesNotChangeThatItIsARefusal(t *testing.T) {
	for _, cause := range []error{
		errors.New("wrong password"),
		context.DeadlineExceeded,
		context.Canceled,
		nil,
	} {
		refused := Denied(cause)
		if got := verdict.OutcomeOf(refused); got != verdict.Denied {
			t.Errorf("a refusal carrying %v reads as %v, want %v", cause, got, verdict.Denied)
		}
		if got := verdict.CauseOf(refused); !errors.Is(got, cause) {
			t.Errorf("the refusal carries %v, want %v: the cause reaches the log unchanged", got, cause)
		}
	}
}

// TestAnIdentityNamesItsOwnerWhereverItIsWritten holds the qualification that
// is not optional.
//
// A bare subject and a qualified one would otherwise both name the caller, and
// a policy written against either would silently miss the other. One backend
// running alone does not make it safe to drop: the second backend arrives
// later, and every policy written in between is then wrong.
func TestAnIdentityNamesItsOwnerWhereverItIsWritten(t *testing.T) {
	one := identity.New("local", "alice")

	if got := one.Key(); got != "local"+Separator+"alice" {
		t.Errorf("the identity is written %q, want the owner-qualified form: a bare %q and a "+
			"qualified one would both name the caller, and a policy written against either "+
			"would silently miss the other", got, one.Subject())
	}
	if one.Key() == one.Subject() {
		t.Error("the written form is the bare subject, so the qualification is dropped; one " +
			"backend running today does not make that safe, because the second arrives later " +
			"and every policy written in between is then wrong")
	}
	if got := one.Owner(); got != "local" {
		t.Errorf("the identity's owner is %q, want the name the authority reported", got)
	}

	carried, ok := FromContext(identity.With(context.Background(), one))
	if !ok {
		t.Fatal("an identity put into a context does not come back out of it")
	}
	if carried.Key() != one.Key() {
		t.Errorf("the context carried %q, want %q", carried.Key(), one.Key())
	}
}

// TestAContextThatWentThroughNoGatewayCarriesNobody holds the other half: the
// absence has to read as absence. A fallback here would turn an anonymous
// request into some subject, which is why FromContext has none.
func TestAContextThatWentThroughNoGatewayCarriesNobody(t *testing.T) {
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("a context that went through no gateway carries an identity, so an anonymous " +
			"request would read as some subject")
	}
}

// Why there is no case here asserting that nothing outside this capability
// directory can write an identity: making one and putting one into a context
// both live in pkg/authn/internal/identity, and Go admits an internal package
// to the subtree it sits in alone. The compiler enforces it for every package
// outside, and a case written from inside — the only place a case can be
// written — would hold nothing, since the restriction does not apply there and
// is not meant to. A test that cannot fail under a wrong implementation is a
// comment wearing a test's name, so this is a comment. The half that is
// observable from outside is held above: a context nothing wrote to carries
// nobody.
