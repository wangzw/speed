package authn

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/identity"
	"github.com/vislake/speed/pkg/authn/internal/step"
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
	if got := verdict.PrincipalOf(nobodyAnswered); got.Account != "" {
		t.Errorf("a verdict nobody answered carries the account %q, want none", got.Account)
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
		{"a credential that holds", Authenticated(Principal{Account: "static.alice"}), verdict.Authenticated},
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

// TestAnAuthenticatedVerdictCarriesItsPrincipal pins the payload that decides
// who the caller is. A principal lost between the backend and the gateway would
// leave an identity naming nobody, and one the backend could still change after
// returning would let it rewrite what the gateway goes on to check.
func TestAnAuthenticatedVerdictCarriesItsPrincipal(t *testing.T) {
	authTime := time.Date(2026, time.October, 11, 8, 0, 0, 0, time.UTC)
	p := Principal{
		Account:    "static.alice",
		Methods:    []Method{MethodStatic},
		Assurance:  AssuranceSingle,
		AuthTime:   authTime,
		Attributes: map[string]string{"team": "ops"},
	}

	made := Authenticated(p)
	p.Methods[0] = MethodSAML
	p.Attributes["team"] = "intruders"

	got := verdict.PrincipalOf(made)
	if got.Account != "static.alice" || got.Assurance != AssuranceSingle || !got.AuthTime.Equal(authTime) {
		t.Errorf("the verdict carries %+v, want the account, assurance and time the backend handed over", got)
	}
	if len(got.Methods) != 1 || got.Methods[0] != MethodStatic {
		t.Errorf("the verdict carries the methods %v, want [%s]: the backend changed its own "+
			"copy after returning and that must not reach what the gateway reads",
			got.Methods, MethodStatic)
	}
	if got.Attributes["team"] != "ops" {
		t.Errorf("the verdict carries the attribute %q, want %q: the backend changed its own "+
			"copy after returning and that must not reach what the gateway reads",
			got.Attributes["team"], "ops")
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

// TestAnIdentityIsAccountedForByItsActor holds the accountability key through
// the surface a reader of FromContext sees.
//
// Rate limiting counts by the key and the audit trail records it as the actor,
// so an impersonation has to spend the actor's allowance and not the
// impersonated account's. A key that followed Account would let one actor draw
// on as many allowances as there are accounts to impersonate, and put the blame
// on the account impersonated.
func TestAnIdentityIsAccountedForByItsActor(t *testing.T) {
	plain := identity.New(Principal{Account: "acct-1", Methods: []Method{MethodPassword}}, "session", "Cookie")
	impersonating := identity.New(Principal{
		Account: "acct-1",
		Actor:   "acct-9",
		Session: "sess-1",
		Scope:   "tenant-a",
		Methods: []Method{MethodPassword},
	}, "session", "Cookie")

	if got := plain.Key(); got != "acct-1" {
		t.Errorf("an identity with no impersonation is keyed %q, want its account", got)
	}
	if got := impersonating.Key(); got != "acct-9" {
		t.Errorf("an impersonating identity is keyed %q, want the actor: the impersonated account "+
			"would be charged for what the actor does", got)
	}
	if got := impersonating.Account(); got != "acct-1" {
		t.Errorf("an impersonating identity is for the account %q, want the one impersonated", got)
	}

	carried, ok := FromContext(identity.With(context.Background(), impersonating))
	if !ok {
		t.Fatal("an identity put into a context does not come back out of it")
	}
	if carried.Key() != impersonating.Key() || carried.Backend() != "session" {
		t.Errorf("the context carried the key %q and the backend %q, want %q and %q",
			carried.Key(), carried.Backend(), impersonating.Key(), "session")
	}
}

// TestAContextThatWentThroughNoGatewayCarriesNobody holds the other half: the
// absence has to read as absence. A fallback here would turn an anonymous
// request into some account, which is why FromContext has none.
func TestAContextThatWentThroughNoGatewayCarriesNobody(t *testing.T) {
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("a context that went through no gateway carries an identity, so an anonymous " +
			"request would read as some account")
	}
	if got := AccessFromContext(context.Background()); got != AccessUnknown {
		t.Errorf("a context the gateway decided nothing for reads as %v, want %v: reading it as "+
			"open would let a request through that nobody judged", got, AccessUnknown)
	}
}

// TestTheAccessLevelTravelsAlongsideTheIdentity pins the wrapper that reads the
// level out of the context: a level that came back as another one would tell a
// consumer a guarded route was open.
func TestTheAccessLevelTravelsAlongsideTheIdentity(t *testing.T) {
	for _, level := range []Access{AccessOpen, AccessOptional, AccessRequired} {
		ctx := identity.WithAccess(context.Background(), level)
		if got := AccessFromContext(ctx); got != level {
			t.Errorf("a context carrying %v reads as %v", level, got)
		}
	}
}

// TestAnAccountIdentifierIsCheckedAgainstTheFormat holds the wrapper and the
// sentinel it returns. The sentinel has to be the one the model package
// returns: a copy declared here would not match errors.Is on a failure that
// came from the parser, and a caller testing for it would miss every one.
func TestAnAccountIdentifierIsCheckedAgainstTheFormat(t *testing.T) {
	id, err := ParseAccountID("static.ops")
	if err != nil {
		t.Fatalf("a well-formed identifier is refused: %v", err)
	}
	if !id.IsStatic() {
		t.Error("an identifier with the static prefix does not read as static")
	}
	if id.String() != "static.ops" {
		t.Errorf("the identifier reads as %q, want %q", id.String(), "static.ops")
	}

	for _, bad := range []string{"", "has space", "slash/ed"} {
		got, err := ParseAccountID(bad)
		if !errors.Is(err, ErrInvalidAccountID) {
			t.Errorf("ParseAccountID(%q) failed with %v, want ErrInvalidAccountID", bad, err)
		}
		if got != "" {
			t.Errorf("ParseAccountID(%q) returned %q alongside its failure", bad, got)
		}
	}
}

// TestEachStepConstructorMakesTheKindItIsNamedFor holds the nine answers apart.
// A wrapper that called its neighbour would turn a refusal into an outage or a
// first factor holding into a second one, and the engine decides what to do
// from the kind alone.
func TestEachStepConstructorMakesTheKindItIsNamedFor(t *testing.T) {
	cause := errors.New("cause")
	for _, c := range []struct {
		name string
		made Step
		want step.Kind
	}{
		{"prompt", StepPrompt("code", nil, Field{Name: "code", Kind: FieldCode}), step.KindPrompt},
		{"redirect", StepRedirect("go", nil, "https://idp.example/authorize"), step.KindRedirect},
		{"data", StepData("options", nil, map[string]string{"k": "v"}), step.KindData},
		{"proven", StepProven(Proof{Account: "acct-1"}), step.KindProven},
		{"refused", StepRefused(cause), step.KindRefused},
		{"throttled", StepThrottled(time.Second), step.KindThrottled},
		{"unavailable", StepUnavailable(cause), step.KindUnavailable},
		{"passed", StepPassed(Pass{Methods: []Method{MethodTOTP}}), step.KindPassed},
		{"enrolled", StepEnrolled(FactorRecord{ID: "f1", Kind: FactorTOTP}), step.KindEnrolled},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := step.KindOf(c.made); got != c.want {
				t.Errorf("Step%s makes a %v answer, want %v", c.name, got, c.want)
			}
		})
	}

	var nobodyAnswered Step
	if got := step.KindOf(nobodyAnswered); got != step.KindUnanswered {
		t.Errorf("a step nobody answered reads as %v, want %v", got, step.KindUnanswered)
	}
	if got := step.KindOf(nobodyAnswered.WithSends(3)); got != step.KindUnanswered {
		t.Errorf("a send count made a step of nothing: it reads as %v, want %v", got, step.KindUnanswered)
	}
}

// The signatures of the seams, held where a change shows up as a compile
// failure of this file. The surface list pins names; these pin the shapes:
// Verify takes a context and a credential and no subject, Accepts is a method
// that returns the declaration, and the registrations take an endpoint, a
// method and a path.
var (
	_ func(Verifier, context.Context, Credential) Verdict = Verifier.Verify
	_ func(Verifier) Accepts                              = Verifier.Accepts
	_ func(Verifier) string                               = Verifier.Name
	_ func(Gate, string, string, string)                  = Gate.Exempt
	_ func(Gate, string, string, string)                  = Gate.Optional
	_ func(Gate, string, string, string)                  = Gate.Impersonable
	_ func(Challenger, string, bool) []string             = Challenger.Challenge
	_ func(CookieClearer, http.ResponseWriter)            = CookieClearer.ClearCookie
	_ func(ScopeGuard, string) bool                       = ScopeGuard.Covers

	_ func(Throttle, context.Context, ThrottleScope, string) (time.Duration, error) = Throttle.Check
	_ func(Throttle, context.Context, ThrottleScope, string) (bool, error)          = Throttle.Fail
	_ func(Throttle, context.Context, ThrottleScope, string) error                  = Throttle.Succeed

	_ = Credential{Scheme: "bearer", Value: "v", Cookie: "c", Peer: "192.0.2.1"}
	_ = Accepts{Schemes: []string{"basic"}, Cookies: []string{"sid"}, Accounts: "", Impersonation: true, SSO: true}
)

// TestTheClaimSentinelsDoNotSpeakOfOwnership holds the wording of the two
// sentinels that concern the carriers a backend takes. There is no ownership
// table, so a failure text that says a subject or an owner is owned would send
// an operator looking for one.
func TestTheClaimSentinelsDoNotSpeakOfOwnership(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
	}{
		{"ErrClaimConflict", ErrClaimConflict},
		{"ErrInvalidClaim", ErrInvalidClaim},
	} {
		text := strings.ToLower(c.err.Error())
		for _, word := range []string{"owner", "ownership", "subject", "separator", "归属", "主体"} {
			if strings.Contains(text, word) {
				t.Errorf("%s reads %q, which still speaks of %q", c.name, c.err, word)
			}
		}
		if !strings.HasPrefix(c.err.Error(), "authn: ") {
			t.Errorf("%s reads %q, want the authn: prefix every diagnostic of this directory carries", c.name, c.err)
		}
	}
}

// TestEverySentinelIsDistinctAndNamesItsDirectory holds the sentinel set apart:
// two that were the same error would make errors.Is answer yes for a failure
// the caller meant to treat differently, and a text without the prefix would be
// the one line of a startup log that does not say where it came from.
func TestEverySentinelIsDistinctAndNamesItsDirectory(t *testing.T) {
	all := []error{
		ErrNoRouter, ErrNoVerifier, ErrClaimConflict, ErrInvalidClaim, ErrInvalidConfig,
		ErrUnknownEndpoint, ErrExemptionUnguarded, ErrAccessConflict, ErrRoutesUnconfigured,
		ErrRoutesUnguarded, ErrInsecureConfig, ErrBusy, ErrDrainTimeout, ErrThrottled, ErrInvalidAccountID,
	}
	for i, a := range all {
		if !strings.HasPrefix(a.Error(), "authn: ") {
			t.Errorf("the sentinel %q does not begin with authn: ", a)
		}
		for _, b := range all[i+1:] {
			if errors.Is(a, b) || errors.Is(b, a) || a.Error() == b.Error() {
				t.Errorf("the sentinels %q and %q are not two failures", a, b)
			}
		}
	}
}

// TestAThrottledErrorReadsAsTheSentinelAndCarriesItsWait holds both halves of
// the carrying shape: errors.Is finds the sentinel through a wrap, and
// errors.As finds the wait, which is the only way a handler can write
// Retry-After. A carrier that did not match the sentinel would be answered as a
// server error, and one that did not give the wait back would send a client
// away with no idea when to return.
func TestAThrottledErrorReadsAsTheSentinelAndCarriesItsWait(t *testing.T) {
	carried := fmt.Errorf("checking the identifier: %w", &ThrottledError{RetryAfter: 7 * time.Second})

	if !errors.Is(carried, ErrThrottled) {
		t.Error("a wrapped ThrottledError does not read as ErrThrottled")
	}
	var te *ThrottledError
	if !errors.As(carried, &te) {
		t.Fatal("errors.As does not find the ThrottledError inside a wrap")
	}
	if te.RetryAfter != 7*time.Second {
		t.Errorf("RetryAfter reads %v, want 7s", te.RetryAfter)
	}
	if errors.Is(carried, ErrBusy) || errors.Is(carried, ErrInvalidClaim) {
		t.Error("a ThrottledError reads as a sentinel it is not")
	}
	if !errors.Is(&ThrottledError{}, ErrThrottled) {
		t.Error("a ThrottledError with no wait does not read as ErrThrottled: the wait is a payload, not what makes it one")
	}
	if got := (&ThrottledError{RetryAfter: 7 * time.Second}).Error(); got != "authn: throttled" {
		t.Errorf("the text is %q, want authn: throttled, which names neither the wait, the scope nor the key", got)
	}
	if errors.As(ErrThrottled, &te) {
		t.Error("the bare sentinel reads as a carrier: a caller would take a wait out of nothing")
	}
}

// TestTheThrottleScopesAreTheClosedSetUnderTheseNames pins the six values.
// They are the first half of the key a back-off is stored under, so renaming
// one orphans every counter kept under the old name.
func TestTheThrottleScopesAreTheClosedSetUnderTheseNames(t *testing.T) {
	want := map[ThrottleScope]string{
		ScopeIdentifier:     "identifier",
		ScopeAccount:        "account",
		ScopeFactor:         "factor",
		ScopePhone:          "phone",
		ScopeClient:         "client",
		ScopeSignupUsername: "signup-username",
	}
	for scope, text := range want {
		if string(scope) != text {
			t.Errorf("the scope %q is spelt %q, want %q", text, scope, text)
		}
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
//
// The packages inside the tree are admitted to that internal package too, and
// the compiler has nothing to say about them. What stops a backend or a login
// method from importing it is the writer list in deps_test.go: the root package,
// which re-exports the read side only, the gateway, the login engine and
// authntest, and no other package in a file that is not a test.
