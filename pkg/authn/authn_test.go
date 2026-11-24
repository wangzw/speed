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

// TestAVerdictNobodyAnsweredReadsAsSaidNothing reads an unanswered verdict as
// unspoken and without a principal.
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

// TestEachConstructorSaysWhatItWasCalledFor holds the three answers apart.
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

// TestAnAuthenticatedVerdictCarriesItsPrincipal carries the principal without
// sharing the backend's copies.
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

// TestWhatARefusalCarriesDoesNotChangeThatItIsARefusal stays a refusal whatever
// cause it carries.
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

// TestAnIdentityIsAccountedForByItsActor accounts an impersonating identity to
// its actor.
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

// TestAContextThatWentThroughNoGatewayCarriesNobody reads a context with no
// gateway as carrying nobody.
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

// TestTheAccessLevelTravelsAlongsideTheIdentity reads back the access level the
// context carries.
func TestTheAccessLevelTravelsAlongsideTheIdentity(t *testing.T) {
	for _, level := range []Access{AccessOpen, AccessOptional, AccessRequired} {
		ctx := identity.WithAccess(context.Background(), level)
		if got := AccessFromContext(ctx); got != level {
			t.Errorf("a context carrying %v reads as %v", level, got)
		}
	}
}

// TestAnAccountIdentifierIsCheckedAgainstTheFormat parses a well-formed
// identifier and refuses a malformed one.
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

// TestEachStepConstructorMakesTheKindItIsNamedFor gives each Step constructor
// its kind.
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

// Compile-time pins of the seam signatures: a change to any shows up as a
// failure in this file.
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

// TestTheClaimSentinelsDoNotSpeakOfOwnership keeps the claim sentinels' wording
// free of ownership terms.
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

// TestEverySentinelIsDistinctAndNamesItsDirectory makes each sentinel distinct
// and prefixed with authn:.
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

// TestAThrottledErrorReadsAsTheSentinelAndCarriesItsWait finds the sentinel and
// the wait through a wrap.
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
