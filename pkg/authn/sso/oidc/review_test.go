package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// putRawProvider registers an OIDC provider row with a config a case builds,
// rather than the fixed shape putProvider writes.
func (r *rig) putRawProvider(id, tenant string, cfg map[string]any) authn.SSOProvider {
	r.t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		r.t.Fatalf("marshalling a provider config: %v", err)
	}
	row := authn.SSOProvider{
		ID: id, Tenant: tenant, Protocol: authn.SSOProtocolOIDC,
		Name: "Test IdP", Enabled: true, Provisioning: authn.ProvisioningJIT, Config: raw, Version: 1,
	}
	r.providers.put(row, testClientSecret)
	return row
}

// runPurpose drives a whole Begin and Answer round under the given purpose.
func (r *rig) runPurpose(id string, m *mockIDP, purpose string) authn.Step {
	r.t.Helper()
	got, err := r.p.Begin(context.Background(), authn.Start{Purpose: purpose, Fields: map[string]string{fieldProvider: id}})
	if err != nil {
		r.t.Fatalf("Begin: %v", err)
	}
	if step.KindOf(got) != step.KindRedirect {
		r.t.Fatalf("Begin answered %s, want a redirect (%v)", step.KindOf(got), step.CauseOf(got))
	}
	query := r.callback(m, step.RedirectOf(got))
	ans, err := r.p.Answer(context.Background(), authn.FlowState{
		Purpose: purpose, Step: stepAuthorize, Private: step.PrivateOf(got),
	}, authn.StepInput{Action: authn.InputCallback, Query: query})
	if err != nil {
		r.t.Fatalf("Answer: %v", err)
	}
	return ans
}

// TestThePrivateStateMustBeIntact holds that the flow's private state is checked
// whole: a wrong version, any empty field and trailing data after the object all
// name no drawn flow and are refused before anything is exchanged.
func TestThePrivateStateMustBeIntact(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)

	valid := writeState("idp-1", digest("s"), "verifier", "nonce")
	if _, ok := readState(valid); !ok {
		t.Fatal("the valid state did not read")
	}
	cases := map[string][]byte{
		"empty":       {},
		"bad version": []byte(`{"v":99,"provider":"idp-1","state":"s","verifier":"v","nonce":"n"}`),
		"no provider": writeState("", digest("s"), "verifier", "nonce"),
		"no state":    writeState("idp-1", "", "verifier", "nonce"),
		"no verifier": writeState("idp-1", digest("s"), "", "nonce"),
		"no nonce":    writeState("idp-1", digest("s"), "verifier", ""),
		"trailing":    append(append([]byte(nil), valid...), []byte("{}")...),
	}
	query := url.Values{"state": {"s"}, "code": {"c"}, "iss": {m.issuer()}}
	for name, priv := range cases {
		if _, ok := readState(priv); ok {
			t.Errorf("%s: readState accepted a corrupted state", name)
		}
		if got := r.answer(priv, query); step.KindOf(got) != step.KindRefused {
			t.Errorf("%s: Answer answered %s, want refused", name, step.KindOf(got))
		}
	}
}

// TestIdPAssertedNeedsTheProvidersTrust holds that a provider whose row does not
// trust its IdP's declaration never gets IDPAsserted set, whatever the token's
// acr is.
func TestIdPAssertedNeedsTheProvidersTrust(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putRawProvider("idp-1", "tenant-a", map[string]any{
		"issuer":         m.issuer(),
		"client_id":      testClientID,
		"claims":         map[string]string{"email": "email", "name": "display_name"},
		"mfa_acr_values": []string{"mfa"},
	})
	m.override = map[string]any{"acr": "mfa"}

	got := r.run("idp-1", m)
	if step.KindOf(got) != step.KindProven {
		t.Fatalf("Answer answered %s, want proven", step.KindOf(got))
	}
	if step.ProofOf(got).IDPAsserted {
		t.Error("IDPAsserted = true for a provider that does not trust its IdP")
	}
}

// TestEmailVerifiedFalseIsCarried holds that a false or absent email_verified
// claim reads as not verified.
func TestEmailVerifiedFalseIsCarried(t *testing.T) {
	cases := map[string]any{
		"false":  false,
		"absent": nil,
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			m := newMockIDP(t)
			r := newRig(t, m)
			r.putProvider("idp-1", "tenant-a", m, nil)
			m.override = map[string]any{"email_verified": value}

			got := r.run("idp-1", m)
			if step.KindOf(got) != step.KindProven {
				t.Fatalf("Answer answered %s, want proven", step.KindOf(got))
			}
			if step.ProofOf(got).External.EmailVerified {
				t.Errorf("EmailVerified = true for a %s email_verified claim", name)
			}
		})
	}
}

// TestBeginRefusesAnUnservedPurpose holds that Begin refuses an enrollment (and
// an unnamed purpose) before it reads any provider.
func TestBeginRefusesAnUnservedPurpose(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)

	for _, purpose := range []string{authn.PurposeEnroll, ""} {
		got, err := r.p.Begin(t.Context(), authn.Start{Purpose: purpose, Fields: map[string]string{fieldProvider: "idp-1"}})
		if err != nil {
			t.Fatalf("Begin: %v", err)
		}
		if step.KindOf(got) != step.KindRefused {
			t.Errorf("Begin under purpose %q answered %s, want refused", purpose, step.KindOf(got))
		}
	}
}

// TestAnswerRefusesAnUnservedPurpose holds the same gate on the callback path.
func TestAnswerRefusesAnUnservedPurpose(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)

	private, _ := r.begin("idp-1")
	got, err := r.p.Answer(t.Context(), authn.FlowState{
		Purpose: authn.PurposeEnroll, Step: stepAuthorize, Private: private,
	}, authn.StepInput{Action: authn.InputCallback})
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if step.KindOf(got) != step.KindRefused {
		t.Errorf("Answer under an enrollment purpose answered %s, want refused", step.KindOf(got))
	}
}

// TestServesLinkAndStepUp holds that the method answers a link and a step-up
// flow, not only a login.
func TestServesLinkAndStepUp(t *testing.T) {
	for _, purpose := range []string{authn.PurposeLink, authn.PurposeStepUp} {
		t.Run(purpose, func(t *testing.T) {
			m := newMockIDP(t)
			r := newRig(t, m)
			r.putProvider("idp-1", "tenant-a", m, nil)
			if got := r.runPurpose("idp-1", m, purpose); step.KindOf(got) != step.KindProven {
				t.Fatalf("a %s flow answered %s, want proven", purpose, step.KindOf(got))
			}
		})
	}
}

// TestProtocolMismatchIsAnOutage holds that a provider of another protocol is an
// outage on both the start and the callback path.
func TestProtocolMismatchIsAnOutage(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	row := r.putProvider("idp-1", "tenant-a", m, nil)
	row.Protocol = "saml"
	r.providers.put(row, testClientSecret)

	begin, err := r.p.Begin(t.Context(), authn.Start{Purpose: authn.PurposeLogin, Fields: map[string]string{fieldProvider: "idp-1"}})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if step.KindOf(begin) != step.KindUnavailable {
		t.Errorf("Begin on a non-OIDC provider answered %s, want unavailable", step.KindOf(begin))
	}

	state := "the-state"
	private := writeState("idp-1", digest(state), "verifier", "nonce")
	query := url.Values{"state": {state}, "code": {"c"}, "iss": {m.issuer()}}
	ans, err := r.p.Answer(t.Context(), authn.FlowState{
		Purpose: authn.PurposeLogin, Step: stepAuthorize, Private: private,
	}, authn.StepInput{Action: authn.InputCallback, Query: query})
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if step.KindOf(ans) != step.KindUnavailable {
		t.Errorf("Answer on a non-OIDC provider answered %s, want unavailable", step.KindOf(ans))
	}
}

// TestMissingIdTokenIsAnOutage holds that a token response with no identity token
// is an outage, not a refusal.
func TestMissingIdTokenIsAnOutage(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)
	m.omitIDToken = true

	private, query := r.start("idp-1", m)
	if got := r.answer(private, query); step.KindOf(got) != step.KindUnavailable {
		t.Fatalf("Answer answered %s, want unavailable", step.KindOf(got))
	}
}

// TestARedirectWithoutAnIssParameterIsAccepted holds that the RFC 9207 iss
// parameter is optional: a provider that does not send one still logs in.
func TestARedirectWithoutAnIssParameterIsAccepted(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)
	m.omitIss = true

	if got := r.run("idp-1", m); step.KindOf(got) != step.KindProven {
		t.Fatalf("a redirect without iss answered %s, want proven", step.KindOf(got))
	}
}

// TestAnswerChecksTheActionAndTheStep holds that a callback that is not an input
// callback, or that arrives at a step the method did not name, is refused.
func TestAnswerChecksTheActionAndTheStep(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)
	private, _ := r.begin("idp-1")

	got, err := r.p.Answer(t.Context(), authn.FlowState{Purpose: authn.PurposeLogin, Step: stepAuthorize, Private: private}, authn.StepInput{Action: "start"})
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if step.KindOf(got) != step.KindRefused {
		t.Errorf("an input that is not a callback answered %s, want refused", step.KindOf(got))
	}

	got, err = r.p.Answer(t.Context(), authn.FlowState{Purpose: authn.PurposeLogin, Step: "other", Private: private}, authn.StepInput{Action: authn.InputCallback})
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if step.KindOf(got) != step.KindRefused {
		t.Errorf("a callback at another step answered %s, want refused", step.KindOf(got))
	}
}

// TestSubjectMappedToAnAbsentClaimIsRefused holds that a token whose sub is
// present but whose configured subject claim is absent is refused, so an empty
// subject is never bound.
func TestSubjectMappedToAnAbsentClaimIsRefused(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, map[string]string{"sub_alt": "subject"})

	private, query := r.start("idp-1", m)
	if got := r.answer(private, query); step.KindOf(got) != step.KindRefused {
		t.Fatalf("Answer answered %s, want refused: the mapped subject claim is absent", step.KindOf(got))
	}
}

// TestSecretReadFailureIsAnOutage holds that a client secret that cannot be read
// is an outage.
func TestSecretReadFailureIsAnOutage(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)
	r.providers.secretErr = errors.New("the secret store is down")

	got, err := r.p.Begin(t.Context(), authn.Start{Purpose: authn.PurposeLogin, Fields: map[string]string{fieldProvider: "idp-1"}})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if step.KindOf(got) != step.KindUnavailable {
		t.Fatalf("Begin answered %s, want unavailable", step.KindOf(got))
	}
}

// TestAProviderConfigThatCannotBeReadIsAnOutage holds that a stored config with
// no issuer, no client identifier or an unknown key is an outage, not a refusal.
func TestAProviderConfigThatCannotBeReadIsAnOutage(t *testing.T) {
	m := newMockIDP(t)
	cases := map[string]map[string]any{
		"no issuer":    {"client_id": testClientID},
		"no client id": {"issuer": m.issuer()},
		"unknown key":  {"issuer": m.issuer(), "client_id": testClientID, "bogus": 1},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, m)
			r.putRawProvider("idp-1", "tenant-a", cfg)
			got, err := r.p.Begin(t.Context(), authn.Start{Purpose: authn.PurposeLogin, Fields: map[string]string{fieldProvider: "idp-1"}})
			if err != nil {
				t.Fatalf("Begin: %v", err)
			}
			if step.KindOf(got) != step.KindUnavailable {
				t.Fatalf("Begin answered %s, want unavailable", step.KindOf(got))
			}
		})
	}
}

// TestConfiguredScopesAreUsed holds that the provider's configured scopes reach
// the authorization URL.
func TestConfiguredScopesAreUsed(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putRawProvider("idp-1", "tenant-a", map[string]any{
		"issuer": m.issuer(), "client_id": testClientID,
		"scopes": []string{"openid", "groups"},
	})

	_, authURL := r.begin("idp-1")
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("the authorization URL does not parse: %v", err)
	}
	scope := u.Query().Get("scope")
	if !strings.Contains(scope, "groups") || strings.Contains(scope, "email") {
		t.Errorf("scope = %q, want the configured openid and groups", scope)
	}
}

// TestConfiguredAlgsAreUsed holds that a token signed with an algorithm the
// provider's configured set does not admit is refused.
func TestConfiguredAlgsAreUsed(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putRawProvider("idp-1", "tenant-a", map[string]any{
		"issuer": m.issuer(), "client_id": testClientID,
		"algs": []string{"RS256"},
	})

	// The mock signs with ES256, which the configured set excludes.
	private, query := r.start("idp-1", m)
	got := r.answer(private, query)
	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("Answer answered %s, want refused: ES256 is not a configured algorithm", step.KindOf(got))
	}
}

// TestAnUnreachableKeySetIsARefusal holds the one place an outage and a refusal
// cannot be told apart: the library's key-set fetch runs under the provider's own
// context and its failure is flattened into a refusal.
func TestAnUnreachableKeySetIsARefusal(t *testing.T) {
	m := newMockIDP(t)
	m.jwksURI = m.server.URL + "/missing-jwks"
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)

	private, query := r.start("idp-1", m)
	if got := r.answer(private, query); step.KindOf(got) != step.KindRefused {
		t.Fatalf("a token whose key set cannot be reached answered %s, want refused", step.KindOf(got))
	}
}

// TestNoUserinfoRequestIsMade holds that the method takes everything it needs
// from the identity token and never calls the userinfo endpoint.
func TestNoUserinfoRequestIsMade(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)

	if got := r.run("idp-1", m); step.KindOf(got) != step.KindProven {
		t.Fatalf("Answer answered %s, want proven", step.KindOf(got))
	}
	if n := m.userinfoRequests(); n != 0 {
		t.Errorf("the userinfo endpoint was called %d times, want none", n)
	}
}

// failTransport is a RoundTripper that always fails, so a case can prove the
// method does not fall back to the process's default transport.
type failTransport struct{}

func (failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("the default transport must not be used")
}

// TestTheDefaultTransportIsNotUsed holds that every outbound call goes through
// the restricted egress client: with the process's default transport replaced by
// one that always fails, the whole login still succeeds.
func TestTheDefaultTransportIsNotUsed(t *testing.T) {
	saved := http.DefaultTransport
	http.DefaultTransport = failTransport{}
	t.Cleanup(func() { http.DefaultTransport = saved })

	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)

	if got := r.run("idp-1", m); step.KindOf(got) != step.KindProven {
		t.Fatalf("with the default transport failing the login answered %s, want proven", step.KindOf(got))
	}
}

// TestCacheDoesNotReuseAnEntryOfALaterVersion holds that the cache entry is keyed
// by an exact version: an entry built for a later version is not reused for an
// earlier one.
func TestCacheDoesNotReuseAnEntryOfALaterVersion(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	row := r.putProvider("idp-1", "tenant-a", m, nil)

	row.Version = 2
	r.providers.put(row, testClientSecret)
	r.begin("idp-1")
	if n := m.discoveries(); n != 1 {
		t.Fatalf("discoveries = %d, want 1", n)
	}
	row.Version = 1
	r.providers.put(row, testClientSecret)
	r.begin("idp-1")
	if n := m.discoveries(); n != 2 {
		t.Fatalf("discoveries after a lower version = %d, want 2", n)
	}
}

// TestCacheTreatsTheExpiryInstantAsExpired holds the strict boundary: an entry
// whose expiry instant is now is already expired.
func TestCacheTreatsTheExpiryInstantAsExpired(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)
	now := time.Now()
	r.p.cache.now = func() time.Time { return now }

	r.begin("idp-1")
	if n := m.discoveries(); n != 1 {
		t.Fatalf("discoveries = %d, want 1", n)
	}
	now = now.Add(defaultProviderCacheTTL)
	r.begin("idp-1")
	if n := m.discoveries(); n != 2 {
		t.Fatalf("discoveries at the expiry instant = %d, want 2", n)
	}
}

// TestAnUnreadableChangePayloadKeepsTheCache holds that an idp-changed event
// whose payload cannot be read leaves the cached provider in place.
func TestAnUnreadableChangePayloadKeepsTheCache(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)
	r.begin("idp-1")

	if err := r.p.onIDPChanged(t.Context(), event.Event{Payload: "not-a-payload"}); err != nil {
		t.Fatalf("onIDPChanged: %v", err)
	}
	r.p.cache.mu.Lock()
	n := len(r.p.cache.entries)
	r.p.cache.mu.Unlock()
	if n != 1 {
		t.Errorf("the cache holds %d entries after an unreadable change, want the one kept", n)
	}
}

// TestBoolClaimReadsTheStringForm holds that a claim sent as the string "true"
// reads as true and "false" reads as false.
func TestBoolClaimReadsTheStringForm(t *testing.T) {
	if !boolClaim("true") {
		t.Error("boolClaim(\"true\") = false, want true")
	}
	if boolClaim("false") {
		t.Error("boolClaim(\"false\") = true, want false")
	}
	if boolClaim(1) {
		t.Error("boolClaim(1) = true, want false")
	}
	if boolClaim(nil) {
		t.Error("boolClaim(nil) = true, want false")
	}
}

// TestClassifyVerifyStringAudienceAndShortToken holds the two labels a token
// whose audience is a bare string that is not the client, and one with too few
// segments, are counted under.
func TestClassifyVerifyStringAudienceAndShortToken(t *testing.T) {
	cfg := providerConfig{Issuer: "https://idp.example", ClientID: "client-1"}
	now := time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour).Unix()

	bareString := rawWithClaims(t, map[string]any{"iss": cfg.Issuer, "aud": "another", "exp": future})
	if got := classifyVerify(bareString, cfg, now); got != outcomeAudience {
		t.Errorf("classifyVerify of a foreign bare-string audience = %q, want %q", got, outcomeAudience)
	}

	parts := strings.Split(rawWithClaims(t, map[string]any{"iss": cfg.Issuer, "aud": "another", "exp": future}), ".")
	twoSegment := parts[0] + "." + parts[1]
	if got := classifyVerify(twoSegment, cfg, now); got != outcomeSignature {
		t.Errorf("classifyVerify of a two-segment token = %q, want %q", got, outcomeSignature)
	}
}

// fakeStore is a settings store a case scripts, keyed by the full path.
type fakeStore struct {
	settings.Store
	values map[string]string
}

func (s *fakeStore) Get(_ settings.Layer, key string, target any) error {
	v, ok := s.values[key]
	if !ok {
		return errors.New("undeclared")
	}
	switch p := target.(type) {
	case *time.Duration:
		d, err := time.ParseDuration(v)
		if err != nil {
			return err
		}
		*p = d
	case *int:
		n, err := strconv.Atoi(v)
		if err != nil {
			return err
		}
		*p = n
	case *string:
		*p = v
	}
	return nil
}

// TestTheSettingsReaderReadsTheStore holds that a setting the store carries is
// read rather than replaced by the declared default, and that a non-positive
// value falls back.
func TestTheSettingsReaderReadsTheStore(t *testing.T) {
	r := newReader(&fakeStore{values: map[string]string{
		providerCacheTTLPath: "45s",
		egressTimeoutPath:    "3s",
	}}, nil)
	if got := r.providerCacheTTL(); got != 45*time.Second {
		t.Errorf("providerCacheTTL = %s, want 45s", got)
	}
	if got := r.egressTimeout(); got != 3*time.Second {
		t.Errorf("egressTimeout = %s, want 3s", got)
	}

	zero := newReader(&fakeStore{values: map[string]string{providerCacheTTLPath: "0s"}}, nil)
	if got := zero.providerCacheTTL(); got != defaultProviderCacheTTL {
		t.Errorf("providerCacheTTL for a zero value = %s, want the declared default", got)
	}
}

// TestTheDefaultDurationsAreLiteral holds the declared defaults against literal
// values, so a changed constant is caught.
func TestTheDefaultDurationsAreLiteral(t *testing.T) {
	r := newReader(nil, nil)
	if got := r.providerCacheTTL(); got != 60*time.Second {
		t.Errorf("providerCacheTTL = %s, want 1m0s", got)
	}
	if got := r.egressTimeout(); got != 10*time.Second {
		t.Errorf("egressTimeout = %s, want 10s", got)
	}
}

// TestThePublicURLMustBeAnOrigin holds that a public address that is not an
// origin fails the start.
func TestThePublicURLMustBeAnOrigin(t *testing.T) {
	err := driveWith(t, &fakeReader{
		gm:      gateMount{PublicURL: "not a url"},
		enabled: moduleConfig{Enabled: true},
	}, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with a public address that is not an origin")
		return nil
	})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("drive error = %v, want authn.ErrInvalidConfig", err)
	}
}
