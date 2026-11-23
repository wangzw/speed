package google

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// TestTheHappyPathProvesAGoogleIdentity holds the whole flow: Begin draws the
// state, the PKCE challenge and the nonce and answers the authorization URL, the
// provider sends the user agent back with a code, and Answer exchanges it and
// verifies the identity token.
func TestTheHappyPathProvesAGoogleIdentity(t *testing.T) {
	r := newRig(t, resolved{})
	private, authURL := r.begin(authn.PurposeLogin)
	if len(private) == 0 {
		t.Fatal("Begin kept no private state")
	}
	q, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("the authorization URL: %v", err)
	}
	aq := q.Query()
	for _, key := range []string{"state", "code_challenge", "nonce"} {
		if aq.Get(key) == "" {
			t.Errorf("the authorization URL carries no %s", key)
		}
	}
	if aq.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", aq.Get("code_challenge_method"))
	}
	if got := aq.Get("client_id"); got != testClientID {
		t.Errorf("client_id = %q, want %q", got, testClientID)
	}
	if got := aq.Get("redirect_uri"); got != r.idp.server.URL+"/api/v1/auth/callback/google" {
		t.Errorf("redirect_uri = %q, want the built callback", got)
	}
	if got := aq.Get("scope"); !strings.Contains(got, "openid") || !strings.Contains(got, "email") {
		t.Errorf("scope = %q, want it to carry openid and email", got)
	}

	callback := r.callback(authURL)
	got := r.answer(private, callback, authn.PurposeLogin)
	proof := proven(t, got)
	if proof.External == nil {
		t.Fatal("the proof carries no external identity")
	}
	if proof.External.Provider != "google" {
		t.Errorf("Provider = %q, want google", proof.External.Provider)
	}
	if proof.External.Subject != "subject-1" {
		t.Errorf("Subject = %q, want the sub claim", proof.External.Subject)
	}
	if proof.External.Email != "user@example.com" || !proof.External.EmailVerified {
		t.Errorf("email = %q verified = %v, want the verified address", proof.External.Email, proof.External.EmailVerified)
	}
	if proof.External.DisplayName != "Test User" {
		t.Errorf("DisplayName = %q, want the name claim", proof.External.DisplayName)
	}
	if len(proof.Methods) != 1 || proof.Methods[0] != authn.MethodOAuth {
		t.Errorf("Methods = %v, want the oauth method", proof.Methods)
	}
}

// TestTheMethodServesLoginLinkAndStepUp holds the purposes the method answers,
// and that an enrollment is refused before anything is drawn.
func TestTheMethodServesLoginLinkAndStepUp(t *testing.T) {
	for _, purpose := range []string{authn.PurposeLogin, authn.PurposeLink, authn.PurposeStepUp} {
		t.Run(purpose, func(t *testing.T) {
			r := newRig(t, resolved{})
			if got := r.run(purpose); step.KindOf(got) != step.KindProven {
				t.Errorf("a %s flow answered %s, want a proof", purpose, step.KindOf(got))
			}
		})
	}
	for _, purpose := range []string{authn.PurposeEnroll, ""} {
		t.Run("refused "+purpose, func(t *testing.T) {
			r := newRig(t, resolved{})
			got, err := r.m.Begin(t.Context(), authn.Start{Purpose: purpose})
			if err != nil {
				t.Fatalf("Begin: %v", err)
			}
			if step.KindOf(got) != step.KindRefused {
				t.Errorf("a %q flow answered %s, want a refusal", purpose, step.KindOf(got))
			}
		})
	}
}

// TestBeginKeepsTheSecretsInThePrivateState holds that the private state carries
// the PKCE verifier and the nonce but the state only as its digest, so a read of
// the flow record yields no value an attacker could present as the state.
func TestBeginKeepsTheSecretsInThePrivateState(t *testing.T) {
	r := newRig(t, resolved{})
	private, authURL := r.begin(authn.PurposeLogin)
	q, _ := url.Parse(authURL)
	state := q.Query().Get("state")
	st, ok := readState(private)
	if !ok {
		t.Fatal("the private state does not parse")
	}
	if st.Verifier == "" || st.Nonce == "" {
		t.Errorf("the private state keeps no verifier or nonce: %+v", st)
	}
	if strings.Contains(string(private), state) || strings.Contains(st.State, state) {
		t.Error("the private state holds the state itself, not only its digest")
	}
	sum := sha256.Sum256([]byte(state))
	if st.State != hex.EncodeToString(sum[:]) {
		t.Error("the stored state is not the SHA-256 of the drawn state")
	}
}

// TestTheAuthorizationEndpointAcceptsTheS256Challenge holds the assumption: the
// provider's authorization endpoint accepts a code_challenge
// with the S256 method. The mock refuses any other request, so a redirect back
// is the acceptance.
func TestTheAuthorizationEndpointAcceptsTheS256Challenge(t *testing.T) {
	r := newRig(t, resolved{})
	_, authURL := r.begin(authn.PurposeLogin)
	resp, err := r.idp.browser().Get(authURL)
	if err != nil {
		t.Fatalf("the authorization endpoint: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 302 {
		t.Fatalf("the authorization endpoint answered %d, want the redirect that accepts the challenge", resp.StatusCode)
	}
}

// TestTheHostedDomainIsSentAndChecked holds that a configured hosted domain is
// sent as the hd parameter and that the token's hd claim must equal it.
func TestTheHostedDomainIsSentAndChecked(t *testing.T) {
	r := newRig(t, resolved{hostedDomain: "example.com"})
	private, authURL := r.begin(authn.PurposeLogin)
	q, _ := url.Parse(authURL)
	if got := q.Query().Get("hd"); got != "example.com" {
		t.Errorf("the authorization URL hd = %q, want example.com", got)
	}
	if got := r.answer(private, r.callback(authURL), authn.PurposeLogin); step.KindOf(got) != step.KindRefused {
		t.Errorf("a token without the hd claim answered %s, want a refusal", step.KindOf(got))
	}

	r2 := newRig(t, resolved{hostedDomain: "example.com"})
	r2.idp.override = map[string]any{"hd": "example.com"}
	if got := r2.run(authn.PurposeLogin); step.KindOf(got) != step.KindProven {
		t.Errorf("a matching hd answered %s, want a proof", step.KindOf(got))
	}

	r3 := newRig(t, resolved{hostedDomain: "example.com"})
	r3.idp.override = map[string]any{"hd": "other.example"}
	if got := r3.run(authn.PurposeLogin); step.KindOf(got) != step.KindRefused {
		t.Errorf("a mismatched hd answered %s, want a refusal", step.KindOf(got))
	}
}

// TestTheIdentityTokenChecks holds the checks on the token and the redirect:
// the state, the issuer, the audience, the expiry,
// the nonce, the verifier and the subject. Each case starts a valid flow and
// then breaks one thing; script may adjust the callback query and return a
// replacement for the flow's private state.
func TestTheIdentityTokenChecks(t *testing.T) {
	past := time.Now().Add(-time.Hour).Unix()

	cases := []struct {
		name   string
		script func(r *rig, private []byte, query url.Values) []byte
	}{
		{
			name: "a missing state",
			script: func(_ *rig, _ []byte, q url.Values) []byte {
				q.Del("state")
				return nil
			},
		},
		{
			name: "a changed state",
			script: func(_ *rig, _ []byte, q url.Values) []byte {
				q.Set("state", "not-the-drawn-state")
				return nil
			},
		},
		{
			name: "a mismatched issuer parameter",
			script: func(_ *rig, _ []byte, q url.Values) []byte {
				q.Set("iss", "https://accounts.example.com")
				return nil
			},
		},
		{
			name: "a missing code",
			script: func(_ *rig, _ []byte, q url.Values) []byte {
				q.Del("code")
				return nil
			},
		},
		{
			name: "a verifier the token endpoint refuses",
			script: func(_ *rig, private []byte, _ url.Values) []byte {
				return tamperedVerifier(t, private)
			},
		},
		{
			name: "an audience that is not this client",
			script: func(r *rig, _ []byte, _ url.Values) []byte {
				r.idp.override = map[string]any{"aud": []string{"another-client"}}
				return nil
			},
		},
		{
			name: "an expired token",
			script: func(r *rig, _ []byte, _ url.Values) []byte {
				r.idp.override = map[string]any{"exp": past}
				return nil
			},
		},
		{
			name: "a missing nonce",
			script: func(r *rig, _ []byte, _ url.Values) []byte {
				r.idp.dropNonce = true
				return nil
			},
		},
		{
			name: "a mismatched nonce",
			script: func(r *rig, _ []byte, _ url.Values) []byte {
				r.idp.override = map[string]any{"nonce": "another-nonce"}
				return nil
			},
		},
		{
			name: "a foreign issuer claim",
			script: func(r *rig, _ []byte, _ url.Values) []byte {
				r.idp.override = map[string]any{"iss": "https://accounts.example.com"}
				return nil
			},
		},
		{
			name: "a missing subject",
			script: func(r *rig, _ []byte, _ url.Values) []byte {
				r.idp.override = map[string]any{"sub": nil}
				return nil
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, resolved{})
			private, query := r.start(authn.PurposeLogin)
			if replaced := tc.script(r, private, query); replaced != nil {
				private = replaced
			}
			refused(t, r.answer(private, query, authn.PurposeLogin))
		})
	}
}

// TestTheMultiAudienceAuthorizedPartyIsChecked holds that a token carrying more
// than one audience must name this client as the authorized party.
func TestTheMultiAudienceAuthorizedPartyIsChecked(t *testing.T) {
	r := newRig(t, resolved{})
	r.idp.override = map[string]any{"aud": []string{testClientID, "another-client"}, "azp": "another-client"}
	if got := r.run(authn.PurposeLogin); step.KindOf(got) != step.KindRefused {
		t.Errorf("a mismatched azp answered %s, want a refusal", step.KindOf(got))
	}

	r2 := newRig(t, resolved{})
	r2.idp.override = map[string]any{"aud": []string{testClientID, "another-client"}, "azp": testClientID}
	if got := r2.run(authn.PurposeLogin); step.KindOf(got) != step.KindProven {
		t.Errorf("a matching azp answered %s, want a proof", step.KindOf(got))
	}
}

// TestTheAlgorithmConfusionIsRefused holds that a token signed with none or with
// HS256 is refused: the provider's key set is what verifies the signature, and
// the symmetric algorithm could otherwise be confused with a bare secret.
func TestTheAlgorithmConfusionIsRefused(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		r := newRig(t, resolved{})
		private, query := r.start(authn.PurposeLogin)
		r.idp.signRaw = func(claims map[string]any) (string, error) {
			payload, _ := json.Marshal(claims)
			h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
			c := base64.RawURLEncoding.EncodeToString(payload)
			return h + "." + c + ".", nil
		}
		refused(t, r.answer(private, query, authn.PurposeLogin))
	})
	t.Run("HS256", func(t *testing.T) {
		r := newRig(t, resolved{})
		private, query := r.start(authn.PurposeLogin)
		r.idp.signRaw = func(claims map[string]any) (string, error) {
			payload, _ := json.Marshal(claims)
			// A key long enough for HS256, so the only reason the token is
			// refused is that the provider's key set is what verifies the
			// signature and no symmetric algorithm is allowed.
			signer, err := jose.NewSigner(
				jose.SigningKey{Algorithm: jose.HS256, Key: []byte("0123456789abcdef0123456789abcdef")},
				(&jose.SignerOptions{}).WithType("JWT"),
			)
			if err != nil {
				return "", err
			}
			obj, err := signer.Sign(payload)
			if err != nil {
				return "", err
			}
			return obj.CompactSerialize()
		}
		refused(t, r.answer(private, query, authn.PurposeLogin))
	})
}

// TestTheEmailVerifiedFlagIsReadAsABoolean holds that only a boolean true, or
// the string "true", marks the address verified.
func TestTheEmailVerifiedFlagIsReadAsABoolean(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want bool
	}{
		{"boolean true", true, true},
		{"string true", "true", true},
		{"boolean false", false, false},
		{"string false", "false", false},
		{"absent", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, resolved{})
			r.idp.override = map[string]any{"email_verified": tc.v}
			proof := proven(t, r.run(authn.PurposeLogin))
			if proof.External.EmailVerified != tc.want {
				t.Errorf("EmailVerified = %v, want %v", proof.External.EmailVerified, tc.want)
			}
		})
	}
}

// TestTheProviderErrorParameterIsRefusedBeforeAnythingElse holds the
// first step: a redirect carrying an error is refused before the state, the code
// or the token endpoint is reached, so a callback that would otherwise prove is
// still refused.
func TestTheProviderErrorParameterIsRefusedBeforeAnythingElse(t *testing.T) {
	r := newRig(t, resolved{})
	private, query := r.start(authn.PurposeLogin)
	// The callback the provider actually sent would prove; adding its error must
	// refuse it, and the token endpoint must never be reached.
	query.Set("error", "access_denied")
	refused(t, r.answer(private, query, authn.PurposeLogin))
	if n := r.idp.tokens(); n != 0 {
		t.Errorf("the token endpoint was reached %d times though the callback reported an error", n)
	}
}

// TestAMissingCodeIsRefusedWithoutTouchingTheTokenEndpoint holds that a callback
// with no authorization code is refused locally: the guard refuses it rather than
// letting an empty code reach the token endpoint and come back rejected.
func TestAMissingCodeIsRefusedWithoutTouchingTheTokenEndpoint(t *testing.T) {
	r := newRig(t, resolved{})
	private, query := r.start(authn.PurposeLogin)
	query.Del("code")
	refused(t, r.answer(private, query, authn.PurposeLogin))
	if n := r.idp.tokens(); n != 0 {
		t.Errorf("the token endpoint was reached %d times for a callback with no code", n)
	}
}

// TestAnAbsentIssuerParameterIsAccepted holds that the issuer comparison is made
// only when the redirect carries an iss parameter (RFC 9207): a callback without
// one is accepted, and the token's own issuer is what the library verifies.
func TestAnAbsentIssuerParameterIsAccepted(t *testing.T) {
	r := newRig(t, resolved{})
	private, query := r.start(authn.PurposeLogin)
	query.Del("iss")
	if got := r.answer(private, query, authn.PurposeLogin); step.KindOf(got) != step.KindProven {
		t.Errorf("a callback with no iss parameter answered %s, want a proof", step.KindOf(got))
	}
}

// TestAnOutageIsAnOutageAndNotARefusal holds the failure direction: discovery
// that fails, a token endpoint that fails and a token response without an
// identity token are all answered as an outage, never as a refusal.
func TestAnOutageIsAnOutageAndNotARefusal(t *testing.T) {
	t.Run("discovery fails", func(t *testing.T) {
		r := newRig(t, resolved{})
		r.idp.discoveryStatus = 500
		got, err := r.m.Begin(t.Context(), loginStart())
		if err != nil {
			t.Fatalf("Begin: %v", err)
		}
		wantUnavailable(t, got)
	})
	t.Run("the token endpoint fails", func(t *testing.T) {
		r := newRig(t, resolved{})
		private, query := r.start(authn.PurposeLogin)
		r.idp.tokenStatus = 500
		wantUnavailable(t, r.answer(private, query, authn.PurposeLogin))
	})
	t.Run("the token response has no identity token", func(t *testing.T) {
		r := newRig(t, resolved{})
		private, query := r.start(authn.PurposeLogin)
		r.idp.omitIDToken = true
		wantUnavailable(t, r.answer(private, query, authn.PurposeLogin))
	})
	t.Run("the key set cannot be reached is answered as a refusal", func(t *testing.T) {
		// The library fetches the key set under the provider's own context and
		// flattens the failure into the same error as a signature failure, so
		// this path cannot tell the provider being unreachable apart from a
		// token that does not verify, and answers a refusal. The test records
		// the behaviour rather than a wished-for one.
		r := newRig(t, resolved{})
		r.idp.jwksURI = "https://127.0.0.1:1/jwks"
		private, query := r.start(authn.PurposeLogin)
		refused(t, r.answer(private, query, authn.PurposeLogin))
	})
	t.Run("the token endpoint refuses the code", func(t *testing.T) {
		r := newRig(t, resolved{})
		private, query := r.start(authn.PurposeLogin)
		r.idp.tokenStatus = 400
		refused(t, r.answer(private, query, authn.PurposeLogin))
	})
}

// TestAMisroutedAnswerIsRefused holds that an answer that is not a redirect
// back, a step the method did not name and a private state the method did not
// write are all refused.
func TestAMisroutedAnswerIsRefused(t *testing.T) {
	r := newRig(t, resolved{})
	private, query := r.start(authn.PurposeLogin)

	got, err := r.m.Answer(t.Context(), authn.FlowState{Purpose: authn.PurposeLogin, Step: stepAuthorize, Private: private},
		authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{}})
	if err != nil || step.KindOf(got) != step.KindRefused {
		t.Fatalf("a submitted answer = %s (error %v), want a refusal", step.KindOf(got), err)
	}
	got, err = r.m.Answer(t.Context(), authn.FlowState{Purpose: authn.PurposeLogin, Step: "other", Private: private},
		authn.StepInput{Action: authn.InputCallback, Query: query})
	if err != nil || step.KindOf(got) != step.KindRefused {
		t.Fatalf("an answer at a step the method did not name = %s (error %v), want a refusal", step.KindOf(got), err)
	}
	got, err = r.m.Answer(t.Context(), authn.FlowState{Purpose: authn.PurposeLogin, Step: stepAuthorize, Private: []byte("junk")},
		authn.StepInput{Action: authn.InputCallback, Query: query})
	if err != nil || step.KindOf(got) != step.KindRefused {
		t.Fatalf("an answer whose private state is not the method's = %s (error %v), want a refusal", step.KindOf(got), err)
	}
}

// TestNoCauseNamesAValueTheClientSent holds the secret rule: no cause of a
// refusal carries the state, the code, the verifier or the nonce.
func TestNoCauseNamesAValueTheClientSent(t *testing.T) {
	r := newRig(t, resolved{})
	private, authURL := r.begin(authn.PurposeLogin)
	q, _ := url.Parse(authURL)
	drawn := q.Query()
	st, _ := readState(private)

	callback := r.callback(authURL)
	callback.Set("state", "not-the-drawn-state")
	got := r.answer(private, callback, authn.PurposeLogin)
	refused(t, got)
	cause := step.CauseOf(got).Error()
	for _, secret := range []string{drawn.Get("state"), drawn.Get("nonce"), drawn.Get("code_challenge"), st.Verifier, callback.Get("code")} {
		if secret != "" && strings.Contains(cause, secret) {
			t.Errorf("the cause names a value the client sent: %q", cause)
		}
	}
}

// proven asserts a proof and returns it.
func proven(t *testing.T, got authn.Step) authn.Proof {
	t.Helper()
	if k := step.KindOf(got); k != step.KindProven {
		t.Fatalf("the answer is %s, want a proof", k)
	}
	return step.ProofOf(got)
}

// refused asserts a refusal.
func refused(t *testing.T, got authn.Step) {
	t.Helper()
	if k := step.KindOf(got); k != step.KindRefused {
		t.Fatalf("the answer is %s, want a refusal", k)
	}
}

// unavailable asserts an outage.
func wantUnavailable(t *testing.T, got authn.Step) {
	t.Helper()
	if k := step.KindOf(got); k != step.KindUnavailable {
		t.Fatalf("the answer is %s, want an outage", k)
	}
}

// TestBeginDrawsFreshRandomSecrets holds that each Begin draws a fresh, random
// state, PKCE verifier and nonce: two calls yield different values, the state and
// nonce are 32 random bytes in base64url, and the verifier is a 43..128 character
// URL-safe PKCE verifier.
func TestBeginDrawsFreshRandomSecrets(t *testing.T) {
	r := newRig(t, resolved{})
	priv1, authURL1 := r.begin(authn.PurposeLogin)
	priv2, authURL2 := r.begin(authn.PurposeLogin)
	q1, _ := url.Parse(authURL1)
	q2, _ := url.Parse(authURL2)

	for _, key := range []string{"state", "nonce", "code_challenge"} {
		if q1.Query().Get(key) == q2.Query().Get(key) {
			t.Errorf("two Begins drew the same %s", key)
		}
	}
	for _, q := range []url.Values{q1.Query(), q2.Query()} {
		for _, key := range []string{"state", "nonce"} {
			v := q.Get(key)
			if strings.ContainsAny(v, "+/=") {
				t.Errorf("%s %q carries a non-url-safe character", key, v)
			}
			raw, err := base64.RawURLEncoding.DecodeString(v)
			if err != nil {
				t.Fatalf("%s is not base64url: %v", key, err)
			}
			if len(raw) != 32 {
				t.Errorf("%s decodes to %d bytes, want 32", key, len(raw))
			}
		}
	}

	s1, ok := readState(priv1)
	if !ok {
		t.Fatal("the first private state does not parse")
	}
	s2, _ := readState(priv2)
	if s1.Verifier == s2.Verifier {
		t.Error("two Begins drew the same PKCE verifier")
	}
	for _, v := range []string{s1.Verifier, s2.Verifier} {
		if len(v) < 43 || len(v) > 128 {
			t.Errorf("the PKCE verifier is %d characters, want 43..128", len(v))
		}
		if strings.ContainsAny(v, "+/=") {
			t.Errorf("the PKCE verifier %q carries a non-url-safe character", v)
		}
	}
}

// TestABooleanClaimThatIsNeitherRefusesTheToken holds that an email_verified
// value that is neither a boolean nor a string boolean — a number included —
// refuses the whole token.
func TestABooleanClaimThatIsNeitherRefusesTheToken(t *testing.T) {
	r := newRig(t, resolved{})
	r.idp.override = map[string]any{"email_verified": 1}
	refused(t, r.run(authn.PurposeLogin))
}

// TestTheBooleanClaimReadsOnlyTheFormsItAccepts pins the branches of the boolean
// claim: a bare or string true/false and JSON null are read; a number, the string
// "null" and an object are errors.
func TestTheBooleanClaimReadsOnlyTheFormsItAccepts(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
		ok   bool
	}{
		{"a bare true", "true", true, true},
		{"a string true", `"true"`, true, true},
		{"a bare false", "false", false, true},
		{"a string false", `"false"`, false, true},
		{"null", "null", false, true},
		{"a number", "1", false, false},
		{"a string null", `"null"`, false, false},
		{"an object", `{}`, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b boolClaim
			err := b.UnmarshalJSON([]byte(tc.in))
			if !tc.ok {
				if err == nil {
					t.Errorf("UnmarshalJSON(%s) accepted a value it should refuse", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("UnmarshalJSON(%s) = %v, want it read", tc.in, err)
			}
			if bool(b) != tc.want {
				t.Errorf("UnmarshalJSON(%s) = %v, want %v", tc.in, bool(b), tc.want)
			}
		})
	}
}

// TestReadStateRefusesWhatItDidNotWrite pins every guard of readState: the
// version, the three non-empty fields, the disallowed unknown fields and the
// trailing content.
func TestReadStateRefusesWhatItDidNotWrite(t *testing.T) {
	if _, ok := readState(writeState(digest("s"), "v", "n")); !ok {
		t.Fatal("readState refused what writeState wrote")
	}
	bad := map[string][]byte{
		"empty":           nil,
		"not json":        []byte("junk"),
		"another version": []byte(`{"v":2,"state":"s","verifier":"v","nonce":"n"}`),
		"no state":        []byte(`{"v":1,"state":"","verifier":"v","nonce":"n"}`),
		"no verifier":     []byte(`{"v":1,"state":"s","verifier":"","nonce":"n"}`),
		"no nonce":        []byte(`{"v":1,"state":"s","verifier":"v","nonce":""}`),
		"trailing":        []byte(`{"v":1,"state":"s","verifier":"v","nonce":"n"}{}`),
		"unknown field":   []byte(`{"v":1,"state":"s","verifier":"v","nonce":"n","extra":1}`),
	}
	for name, in := range bad {
		if _, ok := readState(in); ok {
			t.Errorf("readState accepted %s", name)
		}
	}
}

// TestThePublishedNamesAreLiterals holds the stable values of the code: the
// method name, the provider name and the callback path are published and never
// change.
func TestThePublishedNamesAreLiterals(t *testing.T) {
	if methodName != "google" {
		t.Errorf("methodName = %q, want google", methodName)
	}
	if providerName != "google" {
		t.Errorf("providerName = %q, want google", providerName)
	}
	if callbackPath != "/callback/google" {
		t.Errorf("callbackPath = %q, want /callback/google", callbackPath)
	}
	if defaultIssuer != "https://accounts.google.com" {
		t.Errorf("defaultIssuer = %q, want https://accounts.google.com", defaultIssuer)
	}
}

// TestNewMethodUsesTheResolvedIssuer holds that newMethod builds the provider
// cache over the issuer the configuration resolved, so a mistyped issuer would
// be caught.
func TestNewMethodUsesTheResolvedIssuer(t *testing.T) {
	m := newMethod(resolved{clientID: "id", issuer: defaultIssuer}, nil)
	if m.provider.issuer != defaultIssuer {
		t.Errorf("the provider cache issuer = %q, want %q", m.provider.issuer, defaultIssuer)
	}
}

// TestAnswerDiscoversTheProviderWhenTheCacheIsEmpty holds the Answer-side
// discovery path: with an empty cache and a discovery that fails, the answer is
// an outage rather than a nil provider dereference.
func TestAnswerDiscoversTheProviderWhenTheCacheIsEmpty(t *testing.T) {
	r := newRig(t, resolved{})
	const state = "the-drawn-state"
	private := writeState(digest(state), "the-verifier", "the-nonce")
	r.idp.discoveryStatus = 500
	query := url.Values{"state": {state}, "code": {"the-code"}}
	wantUnavailable(t, r.answer(private, query, authn.PurposeLogin))
}

// TestTheProviderIsDiscoveredOnceAndCached holds the lazy cache: a second call
// that needs the provider does not discover it again.
func TestTheProviderIsDiscoveredOnceAndCached(t *testing.T) {
	r := newRig(t, resolved{})
	r.begin(authn.PurposeLogin)
	r.begin(authn.PurposeLogin)
	if n := r.idp.discoveries(); n != 1 {
		t.Errorf("discovery was requested %d times, want once", n)
	}
}

// TestTheProviderCacheIsConcurrencySafe drives get from many goroutines at once,
// so the race detector sees any unsynchronized access to the cached provider.
func TestTheProviderCacheIsConcurrencySafe(t *testing.T) {
	r := newRig(t, resolved{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.m.Begin(t.Context(), loginStart()); err != nil {
				t.Errorf("Begin: %v", err)
			}
		}()
	}
	wg.Wait()
	if n := r.idp.discoveries(); n < 1 {
		t.Errorf("discovery was requested %d times, want at least once", n)
	}
}
