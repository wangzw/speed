package github

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// beginRedirect runs Begin for a login and reads the redirect address, the
// method state and the query back.
func beginRedirect(t *testing.T, m *method) (private []byte, query url.Values) {
	t.Helper()
	s, err := m.Begin(t.Context(), authn.Start{Purpose: authn.PurposeLogin})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if step.KindOf(s) != step.KindRedirect {
		t.Fatalf("Begin answered %s, want a redirect", step.KindOf(s))
	}
	if name := step.NameOf(s); name != "redirect" {
		t.Errorf("the redirect step is named %q, want the published name redirect", name)
	}
	u, err := url.Parse(step.RedirectOf(s))
	if err != nil {
		t.Fatalf("the redirect address %q: %v", step.RedirectOf(s), err)
	}
	return step.PrivateOf(s), u.Query()
}

// callback is a step input carrying the code and the state a provider sends
// back.
func callback(code, state string) authn.StepInput {
	return authn.StepInput{Action: authn.InputCallback, Query: url.Values{"code": {code}, "state": {state}}}
}

// answer drives Answer for a login callback over the method state a Begin
// produced.
func answer(t *testing.T, m *method, private []byte, in authn.StepInput) authn.Step {
	t.Helper()
	s, err := m.Answer(t.Context(), authn.FlowState{Purpose: authn.PurposeLogin, Step: stepRedirect, Private: private}, in)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	return s
}

// TestBeginRedirectsWithStateAndS256PKCE holds the shape of the authorization
// address: the configured application, the callback address built from the
// public base, both scopes, a state, and an S256 challenge.
func TestBeginRedirectsWithStateAndS256PKCE(t *testing.T) {
	m := methodFor(newGitHubMock(t))
	_, q := beginRedirect(t, m)

	if got := q.Get("client_id"); got != testClientID {
		t.Errorf("client_id = %q, want %q", got, testClientID)
	}
	if got := q.Get("redirect_uri"); got != testRedirectURI {
		t.Errorf("redirect_uri = %q, want %q", got, testRedirectURI)
	}
	if got := q.Get("response_type"); got != "code" {
		t.Errorf("response_type = %q, want code", got)
	}
	if got := q.Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", got)
	}
	if q.Get("code_challenge") == "" {
		t.Error("the authorization address carries no code_challenge")
	}
	if q.Get("state") == "" {
		t.Error("the authorization address carries no state")
	}
	scope := q.Get("scope")
	for _, want := range []string{"read:user", "user:email"} {
		if !strings.Contains(scope, want) {
			t.Errorf("scope = %q, want it to carry %q", scope, want)
		}
	}
}

// TestAnswerProvesTheNumericIdentityAndAVerifiedAddress drives a whole flow
// against the mock and holds the proof: the numeric account number as the
// subject, the address that is primary and verified, the profile name, and the
// oauth method. It also holds that the verifier the exchange carried derives the
// challenge the authorization address carried, and that the read carried the
// access token as a bearer credential.
func TestAnswerProvesTheNumericIdentityAndAVerifiedAddress(t *testing.T) {
	mock := newGitHubMock(t)
	mock.setUser(githubUser{ID: 4242, Login: "ada", Name: "Ada Lovelace"}, "public@example.com", 0)
	mock.setEmails([]githubEmail{{Email: "ada@example.com", Primary: true, Verified: true}}, 0)
	m := methodFor(mock)

	private, q := beginRedirect(t, m)
	got := answer(t, m, private, callback("the-code", q.Get("state")))
	if step.KindOf(got) != step.KindProven {
		t.Fatalf("Answer answered %s, want a proof", step.KindOf(got))
	}
	p := step.ProofOf(got)
	if p.External == nil {
		t.Fatal("the proof names no external identity")
	}
	if p.External.Provider != "github" {
		t.Errorf("provider = %q, want github", p.External.Provider)
	}
	if p.External.Subject != "4242" {
		t.Errorf("subject = %q, want the numeric account number 4242", p.External.Subject)
	}
	if p.External.Email != "ada@example.com" || !p.External.EmailVerified {
		t.Errorf("address = %q verified=%v, want ada@example.com verified", p.External.Email, p.External.EmailVerified)
	}
	if p.External.DisplayName != "Ada Lovelace" {
		t.Errorf("display name = %q, want the profile name", p.External.DisplayName)
	}
	if len(p.Methods) != 1 || p.Methods[0] != authn.MethodOAuth {
		t.Errorf("methods = %v, want the single oauth method", p.Methods)
	}

	_, userCalls, _, verifier, authHeader := mock.seen()
	if userCalls != 1 {
		t.Errorf("the account was read %d times, want 1", userCalls)
	}
	if verifier == "" {
		t.Fatal("the exchange carried no PKCE verifier")
	}
	if derived := oauth2.S256ChallengeFromVerifier(verifier); derived != q.Get("code_challenge") {
		t.Errorf("the verifier sent derives challenge %q, want the %q the address carried", derived, q.Get("code_challenge"))
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		t.Errorf("the account read carried Authorization %q, want a bearer token", authHeader)
	}
}

// TestAMissingOrMismatchedStateIsRefused holds that a callback whose state does
// not hash to the recorded digest ends as a refusal and never reaches the token
// endpoint.
func TestAMissingOrMismatchedStateIsRefused(t *testing.T) {
	for name, state := range map[string]string{"missing": "", "mismatched": "not-the-state"} {
		t.Run(name, func(t *testing.T) {
			mock := newGitHubMock(t)
			m := methodFor(mock)
			private, _ := beginRedirect(t, m)

			got := answer(t, m, private, callback("the-code", state))

			if step.KindOf(got) != step.KindRefused {
				t.Fatalf("Answer answered %s, want a refusal", step.KindOf(got))
			}
			if tokenCalls, _, _, _, _ := mock.seen(); tokenCalls != 0 {
				t.Error("the exchange was attempted with a state that does not match the flow")
			}
		})
	}
}

// TestACallbackCarryingTheProvidersErrorIsRefused holds that a callback that
// carries the provider's own error ends as a refusal before any other parameter
// is read, so no request is made.
func TestACallbackCarryingTheProvidersErrorIsRefused(t *testing.T) {
	mock := newGitHubMock(t)
	m := methodFor(mock)
	private, _ := beginRedirect(t, m)

	in := authn.StepInput{Action: authn.InputCallback, Query: url.Values{
		"error": {"access_denied"}, "code": {"the-code"}, "state": {"the-state"},
	}}
	got := answer(t, m, private, in)

	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("Answer answered %s, want a refusal", step.KindOf(got))
	}
	if tokenCalls, userCalls, emailsCalls, _, _ := mock.seen(); tokenCalls != 0 || userCalls != 0 || emailsCalls != 0 {
		t.Errorf("the provider was reached (%d, %d, %d calls) though the callback reported an error",
			tokenCalls, userCalls, emailsCalls)
	}
}

// TestAnExchangeTheProviderRefusesIsRefused holds that an exchange the provider
// answers with an OAuth error — what a mismatched PKCE verifier or a spent code
// produces — is a refusal and not an outage, and that no read follows it.
func TestAnExchangeTheProviderRefusesIsRefused(t *testing.T) {
	mock := newGitHubMock(t)
	mock.setToken(400, "invalid_grant")
	m := methodFor(mock)

	private, q := beginRedirect(t, m)
	got := answer(t, m, private, callback("the-code", q.Get("state")))

	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("Answer answered %s, want a refusal", step.KindOf(got))
	}
	if _, userCalls, emailsCalls, _, _ := mock.seen(); userCalls != 0 || emailsCalls != 0 {
		t.Error("the provider's API was read though the exchange was refused")
	}
}

// TestAnAddressNeedsBothMarkers holds the email rule: only an entry that is both
// primary and verified produces an address, and an account with none is proven
// with an empty address and the marker false rather than being refused. The
// address /user carries is present in every case and is never the one used.
func TestAnAddressNeedsBothMarkers(t *testing.T) {
	cases := []struct {
		name         string
		emails       []githubEmail
		want         string
		wantVerified bool
	}{
		{"none", nil, "", false},
		{"primary only", []githubEmail{{Email: "a@example.com", Primary: true}}, "", false},
		{"verified only", []githubEmail{{Email: "a@example.com", Verified: true}}, "", false},
		{"neither", []githubEmail{{Email: "a@example.com"}}, "", false},
		{"both", []githubEmail{{Email: "a@example.com", Primary: true, Verified: true}}, "a@example.com", true},
		{"the second entry qualifies", []githubEmail{
			{Email: "a@example.com", Primary: true},
			{Email: "b@example.com", Primary: true, Verified: true},
		}, "b@example.com", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mock := newGitHubMock(t)
			// The address /user carries is not verified and must never be used.
			mock.setUser(githubUser{ID: 1, Login: "ada"}, "public@example.com", 0)
			mock.setEmails(c.emails, 0)
			m := methodFor(mock)

			private, q := beginRedirect(t, m)
			got := answer(t, m, private, callback("the-code", q.Get("state")))

			if step.KindOf(got) != step.KindProven {
				t.Fatalf("Answer answered %s, want a proof with the address %q", step.KindOf(got), c.want)
			}
			p := step.ProofOf(got)
			if p.External.Email != c.want || p.External.EmailVerified != c.wantVerified {
				t.Errorf("address = %q verified=%v, want %q verified=%v",
					p.External.Email, p.External.EmailVerified, c.want, c.wantVerified)
			}
		})
	}
}

// TestANonOKReadIsRefused holds that a read the provider answers with a status
// that is not 200 ends the flow as a refusal, whoever the read was for.
func TestANonOKReadIsRefused(t *testing.T) {
	t.Run("account", func(t *testing.T) {
		mock := newGitHubMock(t)
		mock.setUser(githubUser{}, "", 404)
		m := methodFor(mock)
		private, q := beginRedirect(t, m)
		if got := answer(t, m, private, callback("c", q.Get("state"))); step.KindOf(got) != step.KindRefused {
			t.Fatalf("Answer answered %s, want a refusal", step.KindOf(got))
		}
	})
	t.Run("addresses", func(t *testing.T) {
		mock := newGitHubMock(t)
		mock.setEmails(nil, 500)
		m := methodFor(mock)
		private, q := beginRedirect(t, m)
		if got := answer(t, m, private, callback("c", q.Get("state"))); step.KindOf(got) != step.KindRefused {
			t.Fatalf("Answer answered %s, want a refusal", step.KindOf(got))
		}
	})
}

// TestAnAccountWithNoNumberIsRefused holds that a /user body without a positive
// account number cannot be a subject, so the flow ends as a refusal.
func TestAnAccountWithNoNumberIsRefused(t *testing.T) {
	mock := newGitHubMock(t)
	mock.setUser(githubUser{Login: "ada"}, "public@example.com", 0)
	m := methodFor(mock)

	private, q := beginRedirect(t, m)
	got := answer(t, m, private, callback("c", q.Get("state")))

	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("Answer answered %s, want a refusal", step.KindOf(got))
	}
}

// TestTheOutboundClientRefusesANonPublicAddress holds that the reads go through
// the restricted client: with the production client the loopback mock is not
// reachable, which is the provider being unable to answer and not a refusal.
func TestTheOutboundClientRefusesANonPublicAddress(t *testing.T) {
	mock := newGitHubMock(t)
	m := methodWithoutLoopback(mock)

	private, q := beginRedirect(t, m)
	got := answer(t, m, private, callback("c", q.Get("state")))

	if step.KindOf(got) != step.KindUnavailable {
		t.Fatalf("Answer answered %s, want the provider being unreachable", step.KindOf(got))
	}
}

// TestTheMethodServesLoginLinkAndStepUpOnly holds the purposes: an external
// identity proves a login, a link and a step-up on an account with no factor,
// and an enrollment is not something it proves.
func TestTheMethodServesLoginLinkAndStepUpOnly(t *testing.T) {
	m := methodFor(newGitHubMock(t))
	for _, purpose := range []string{authn.PurposeLogin, authn.PurposeLink, authn.PurposeStepUp} {
		s, err := m.Begin(t.Context(), authn.Start{Purpose: purpose})
		if err != nil || step.KindOf(s) != step.KindRedirect {
			t.Errorf("Begin for purpose %q answered %s (error %v), want a redirect", purpose, step.KindOf(s), err)
		}
	}
	for _, purpose := range []string{authn.PurposeEnroll, ""} {
		if s, _ := m.Begin(t.Context(), authn.Start{Purpose: purpose}); step.KindOf(s) != step.KindRefused {
			t.Errorf("Begin for purpose %q answered %s, want a refusal", purpose, step.KindOf(s))
		}
	}
}

// TestAnswerRefusesASubmitAndAnUnknownStep holds that a callback route the
// method did not write into cannot be walked to a proof: a submitted answer and
// a step the method never named are refused.
func TestAnswerRefusesASubmitAndAnUnknownStep(t *testing.T) {
	m := methodFor(newGitHubMock(t))
	submit, err := m.Answer(t.Context(), authn.FlowState{Purpose: authn.PurposeLogin, Step: stepRedirect},
		authn.StepInput{Action: authn.InputSubmit})
	if err != nil || step.KindOf(submit) != step.KindRefused {
		t.Errorf("Answer to a submit answered %s (error %v), want a refusal", step.KindOf(submit), err)
	}
	unknown, err := m.Answer(t.Context(), authn.FlowState{Purpose: authn.PurposeLogin, Step: "other"}, callback("c", "s"))
	if err != nil || step.KindOf(unknown) != step.KindRefused {
		t.Errorf("Answer at an unknown step answered %s (error %v), want a refusal", step.KindOf(unknown), err)
	}
}

// TestTheMethodStateCarriesTheStateOnlyAsADigest holds that the anti-forgery
// state is kept as its SHA-256 digest, never in the clear.
func TestTheMethodStateCarriesTheStateOnlyAsADigest(t *testing.T) {
	m := methodFor(newGitHubMock(t))
	private, q := beginRedirect(t, m)
	state := q.Get("state")
	if state == "" {
		t.Fatal("the authorization address carries no state")
	}
	if strings.Contains(string(private), state) {
		t.Error("the method state carries the anti-forgery state in the clear, want its digest only")
	}
	var st flowState
	if err := json.Unmarshal(private, &st); err != nil {
		t.Fatalf("the method state does not decode: %v", err)
	}
	sum := sha256.Sum256([]byte(state))
	if st.State != hex.EncodeToString(sum[:]) {
		t.Errorf("the recorded digest is %q, want the SHA-256 of the state", st.State)
	}
}

// TestNoAnswerCarriesTheCodeOrTheState holds that a refusal's cause names
// nothing the client sent.
func TestNoAnswerCarriesTheCodeOrTheState(t *testing.T) {
	mock := newGitHubMock(t)
	mock.setToken(400, "invalid_grant")
	m := methodFor(mock)

	private, q := beginRedirect(t, m)
	const code = "SECRET-CODE"
	got := answer(t, m, private, callback(code, q.Get("state")))
	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("Answer answered %s, want a refusal", step.KindOf(got))
	}
	cause := step.CauseOf(got)
	if cause == nil {
		t.Fatal("the refusal carries no cause")
	}
	for _, secret := range []string{code, q.Get("state")} {
		if strings.Contains(cause.Error(), secret) {
			t.Errorf("the refusal's cause %q carries %q", cause.Error(), secret)
		}
	}
}

// TestReadStateRejectsWhatItDidNotWrite holds that the method state a callback
// is answered over is read strictly: a malformed one names no state.
func TestReadStateRejectsWhatItDidNotWrite(t *testing.T) {
	good, _, err := newState()
	if err != nil {
		t.Fatalf("newState: %v", err)
	}
	for name, in := range map[string][]byte{
		"empty":            nil,
		"not json":         []byte("nope"),
		"no state":         []byte(`{"v":1,"verifier":"x"}`),
		"no verifier":      []byte(`{"v":1,"state":"x"}`),
		"unknown field":    []byte(`{"v":1,"state":"x","verifier":"y","extra":1}`),
		"another version":  []byte(`{"v":2,"state":"x","verifier":"y"}`),
		"trailing content": []byte(`{"v":1,"state":"x","verifier":"y"}{}`),
	} {
		if _, ok := readState(in); ok {
			t.Errorf("readState accepted %s", name)
		}
	}
	if _, ok := readState(good.private()); !ok {
		t.Error("readState refused the state newState wrote")
	}
}

// TestAnswerRefusesAnEmptyCode holds that a callback carrying an empty code is
// refused locally, without an exchange: the token endpoint must never be reached
// with no code.
func TestAnswerRefusesAnEmptyCode(t *testing.T) {
	mock := newGitHubMock(t)
	m := methodFor(mock)
	private, q := beginRedirect(t, m)

	got := answer(t, m, private, callback("", q.Get("state")))
	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("Answer answered %s, want a refusal for an empty code", step.KindOf(got))
	}
	if tokenCalls, _, _, _, _ := mock.seen(); tokenCalls != 0 {
		t.Error("the exchange was attempted with an empty code")
	}
}

// TestAnswerRefusesEachGuardWithAValidRecord pins the four pre-guards of Answer
// separately: each negative vector carries a valid Begin record and an otherwise
// provable callback, so only the guard itself can refuse it.
func TestAnswerRefusesEachGuardWithAValidRecord(t *testing.T) {
	newRec := func(t *testing.T) (*method, *githubMock, []byte, string) {
		mock := newGitHubMock(t)
		m := methodFor(mock)
		private, q := beginRedirect(t, m)
		return m, mock, private, q.Get("state")
	}

	t.Run("not a callback", func(t *testing.T) {
		m, mock, private, state := newRec(t)
		in := authn.StepInput{Action: authn.InputSubmit, Query: url.Values{"code": {"c"}, "state": {state}}}
		got, err := m.Answer(t.Context(), authn.FlowState{Purpose: authn.PurposeLogin, Step: stepRedirect, Private: private}, in)
		if err != nil || step.KindOf(got) != step.KindRefused {
			t.Fatalf("a submit answered %s (error %v), want a refusal", step.KindOf(got), err)
		}
		if tokenCalls, _, _, _, _ := mock.seen(); tokenCalls != 0 {
			t.Error("the exchange ran for a submit")
		}
	})

	t.Run("a purpose the method does not serve", func(t *testing.T) {
		m, _, private, state := newRec(t)
		got, err := m.Answer(t.Context(), authn.FlowState{Purpose: authn.PurposeEnroll, Step: stepRedirect, Private: private}, callback("c", state))
		if err != nil || step.KindOf(got) != step.KindRefused {
			t.Fatalf("an unserved purpose answered %s (error %v), want a refusal", step.KindOf(got), err)
		}
	})

	t.Run("a step the method did not name", func(t *testing.T) {
		m, _, private, state := newRec(t)
		got, err := m.Answer(t.Context(), authn.FlowState{Purpose: authn.PurposeLogin, Step: "other", Private: private}, callback("c", state))
		if err != nil || step.KindOf(got) != step.KindRefused {
			t.Fatalf("an unknown step answered %s (error %v), want a refusal", step.KindOf(got), err)
		}
	})

	t.Run("the provider's error with a valid state", func(t *testing.T) {
		m, mock, private, state := newRec(t)
		in := authn.StepInput{Action: authn.InputCallback, Query: url.Values{
			"error": {"access_denied"}, "code": {"c"}, "state": {state},
		}}
		got, err := m.Answer(t.Context(), authn.FlowState{Purpose: authn.PurposeLogin, Step: stepRedirect, Private: private}, in)
		if err != nil || step.KindOf(got) != step.KindRefused {
			t.Fatalf("a provider error answered %s (error %v), want a refusal", step.KindOf(got), err)
		}
		if tokenCalls, _, _, _, _ := mock.seen(); tokenCalls != 0 {
			t.Error("the exchange ran for a callback carrying the provider's error")
		}
	})
}

// TestANegativeAccountNumberIsRefused holds that only a positive account number
// is a subject: a negative one is refused, not turned into a subject string.
func TestANegativeAccountNumberIsRefused(t *testing.T) {
	mock := newGitHubMock(t)
	mock.setUser(githubUser{ID: -1, Login: "ada"}, "public@example.com", 0)
	m := methodFor(mock)
	private, q := beginRedirect(t, m)

	if got := answer(t, m, private, callback("c", q.Get("state"))); step.KindOf(got) != step.KindRefused {
		t.Fatalf("Answer answered %s, want a refusal for a negative account number", step.KindOf(got))
	}
}

// TestTheDisplayNameFallsBackToTheLoginAndIsTrimmed holds that the display name
// is the trimmed profile name, or the trimmed login when there is none.
func TestTheDisplayNameFallsBackToTheLoginAndIsTrimmed(t *testing.T) {
	cases := []struct {
		name string
		user githubUser
		want string
	}{
		{"the profile name, trimmed", githubUser{ID: 1, Login: "ada", Name: "  Ada Lovelace  "}, "Ada Lovelace"},
		{"no profile name falls back to the login", githubUser{ID: 1, Login: "ada"}, "ada"},
		{"the login is trimmed too", githubUser{ID: 1, Login: "  ada  "}, "ada"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newGitHubMock(t)
			mock.setUser(tc.user, "", 0)
			m := methodFor(mock)
			private, q := beginRedirect(t, m)
			got := answer(t, m, private, callback("c", q.Get("state")))
			if step.KindOf(got) != step.KindProven {
				t.Fatalf("Answer answered %s, want a proof", step.KindOf(got))
			}
			if p := step.ProofOf(got); p.External.DisplayName != tc.want {
				t.Errorf("DisplayName = %q, want %q", p.External.DisplayName, tc.want)
			}
		})
	}
}

// TestFirstReadsTheFormOfAPostBinding holds that a callback parameter is read
// from the query of a redirect or from the form of a POST binding.
func TestFirstReadsTheFormOfAPostBinding(t *testing.T) {
	mock := newGitHubMock(t)
	m := methodFor(mock)
	private, q := beginRedirect(t, m)

	in := authn.StepInput{Action: authn.InputCallback, Form: url.Values{
		"code": {"the-code"}, "state": {q.Get("state")},
	}}
	if got := answer(t, m, private, in); step.KindOf(got) != step.KindProven {
		t.Fatalf("a POST binding answered %s, want a proof", step.KindOf(got))
	}
}

// TestAnEmptyPrimaryVerifiedAddressIsSkipped holds that an entry that is primary
// and verified but carries no address is skipped, so it cannot shadow a later
// entry that does.
func TestAnEmptyPrimaryVerifiedAddressIsSkipped(t *testing.T) {
	mock := newGitHubMock(t)
	mock.setUser(githubUser{ID: 1, Login: "ada"}, "", 0)
	mock.setEmails([]githubEmail{
		{Email: "   ", Primary: true, Verified: true},
		{Email: "b@example.com", Primary: true, Verified: true},
	}, 0)
	m := methodFor(mock)
	private, q := beginRedirect(t, m)

	got := answer(t, m, private, callback("c", q.Get("state")))
	if step.KindOf(got) != step.KindProven {
		t.Fatalf("Answer answered %s, want a proof", step.KindOf(got))
	}
	if p := step.ProofOf(got); p.External.Email != "b@example.com" {
		t.Errorf("Email = %q, want the non-empty qualifying entry", p.External.Email)
	}
}

// TestTheStateCarries32RandomBytes holds the anti-forgery state's entropy: 32
// random bytes in base64url.
func TestTheStateCarries32RandomBytes(t *testing.T) {
	m := methodFor(newGitHubMock(t))
	_, q := beginRedirect(t, m)
	raw, err := base64.RawURLEncoding.DecodeString(q.Get("state"))
	if err != nil {
		t.Fatalf("the state is not base64url: %v", err)
	}
	if stateBytes != 32 {
		t.Errorf("stateBytes = %d, want 32", stateBytes)
	}
	if len(raw) != stateBytes {
		t.Errorf("the state decodes to %d bytes, want %d", len(raw), stateBytes)
	}
}

// TestACancelledContextIsUnavailable holds that a call under a context that has
// already ended answers an outage rather than drawing or reading anything.
func TestACancelledContextIsUnavailable(t *testing.T) {
	mock := newGitHubMock(t)
	m := methodFor(mock)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if got, _ := m.Begin(ctx, authn.Start{Purpose: authn.PurposeLogin}); step.KindOf(got) != step.KindUnavailable {
		t.Errorf("Begin under a cancelled context answered %s, want an outage", step.KindOf(got))
	}
	private, q := beginRedirect(t, m)
	got, _ := m.Answer(ctx, authn.FlowState{Purpose: authn.PurposeLogin, Step: stepRedirect, Private: private},
		callback("c", q.Get("state")))
	if step.KindOf(got) != step.KindUnavailable {
		t.Errorf("Answer under a cancelled context answered %s, want an outage", step.KindOf(got))
	}
}

// TestAReadableButUndecodableAccountIsAnOutage holds that a read the provider
// answers 200 to whose body does not decode is the provider being unable to
// answer (an outage), not a refusal.
func TestAReadableButUndecodableAccountIsAnOutage(t *testing.T) {
	mock := newGitHubMock(t)
	mock.setUser(githubUser{ID: 1, Login: "ada"}, "", 0)
	mock.setUserRaw("<html>not json</html>")
	m := methodFor(mock)
	private, q := beginRedirect(t, m)

	if got := answer(t, m, private, callback("c", q.Get("state"))); step.KindOf(got) != step.KindUnavailable {
		t.Fatalf("Answer answered %s, want an outage for a body that does not decode", step.KindOf(got))
	}
}

// TestTheOutboundReadCarriesTheAgreedHeaders holds the shape of the API read: the
// pinned API version, the JSON media type and a user agent.
func TestTheOutboundReadCarriesTheAgreedHeaders(t *testing.T) {
	mock := newGitHubMock(t)
	m := methodFor(mock)
	private, q := beginRedirect(t, m)
	if got := answer(t, m, private, callback("c", q.Get("state"))); step.KindOf(got) != step.KindProven {
		t.Fatalf("Answer answered %s, want a proof", step.KindOf(got))
	}
	version, accept, agent := mock.seenHeaders()
	if version != "2022-11-28" {
		t.Errorf("X-GitHub-Api-Version = %q, want 2022-11-28", version)
	}
	if accept != "application/vnd.github+json" {
		t.Errorf("Accept = %q, want application/vnd.github+json", accept)
	}
	if agent == "" {
		t.Error("the read carried no User-Agent")
	}
}
