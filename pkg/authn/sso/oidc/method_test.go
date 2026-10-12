package oidc

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"testing"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// TestPositiveLogin proves the whole flow: Begin answers a redirect with PKCE
// and a nonce, Answer exchanges the code, verifies the token and hands back a
// proof keyed in the provider's namespace with the mapped address, the display
// name and the provider's tenant.
func TestPositiveLogin(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)

	got := r.run("idp-1", m)
	if step.KindOf(got) != step.KindProven {
		t.Fatalf("Answer answered %s, want proven", step.KindOf(got))
	}
	p := step.ProofOf(got)
	if p.Provider != "idp-1" {
		t.Errorf("Proof.Provider = %q, want idp-1", p.Provider)
	}
	if p.External == nil {
		t.Fatal("Proof.External is nil")
	}
	if p.External.Provider != "oidc:idp-1" {
		t.Errorf("External.Provider = %q, want oidc:idp-1", p.External.Provider)
	}
	if p.External.Subject != "subject-1" {
		t.Errorf("External.Subject = %q, want subject-1", p.External.Subject)
	}
	if p.External.Email != "user@example.com" || !p.External.EmailVerified {
		t.Errorf("External email = %q verified=%v, want user@example.com verified", p.External.Email, p.External.EmailVerified)
	}
	if p.External.DisplayName != "Test User" {
		t.Errorf("External.DisplayName = %q, want Test User", p.External.DisplayName)
	}
	if p.External.Tenant != "tenant-a" {
		t.Errorf("External.Tenant = %q, want tenant-a", p.External.Tenant)
	}
	if len(p.Methods) != 1 || p.Methods[0] != authn.MethodOIDC {
		t.Errorf("Methods = %v, want [oidc]", p.Methods)
	}
	if p.IDPAsserted {
		t.Error("IDPAsserted = true with no trust and no acr")
	}
	if n := r.assertionCount(protocolOIDC, outcomeAccepted); n != 1 {
		t.Errorf("accepted assertions = %d, want 1", n)
	}
	if len(r.audit.all()) != 0 {
		t.Errorf("audit rows = %v, want none for an accepted login", r.audit.all())
	}
}

// TestStateMismatchIsRefused changes the state the provider returned, so the
// digest no longer matches the one the flow drew.
func TestStateMismatchIsRefused(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)

	private, query := r.start("idp-1", m)
	query.Set("state", "not-the-drawn-state")
	assertRefused(t, r, private, query, outcomeOther, "idp-1")
}

// TestIssuerParameterMismatchIsRefused gives the redirect an iss parameter that
// is not the configured issuer verbatim.
func TestIssuerParameterMismatchIsRefused(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)

	private, query := r.start("idp-1", m)
	query.Set("iss", m.issuer()+"/")
	assertRefused(t, r, private, query, outcomeIssuer, "idp-1")
}

// TestTokenIssuerMismatchIsRefused signs a token whose issuer is another one, so
// the verifier refuses it as an issuer failure.
func TestTokenIssuerMismatchIsRefused(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)
	m.override = map[string]any{"iss": "https://other.example"}

	private, query := r.start("idp-1", m)
	assertRefused(t, r, private, query, outcomeIssuer, "idp-1")
}

// TestMissingClientIDInAudienceIsRefused signs a token whose audience is not
// this client.
func TestMissingClientIDInAudienceIsRefused(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)
	m.override = map[string]any{"aud": []string{"another-client"}}

	private, query := r.start("idp-1", m)
	assertRefused(t, r, private, query, outcomeAudience, "idp-1")
}

// TestAuthorizedPartyMismatchIsRefused signs a token with more than one audience
// and an authorized party that is not this client.
func TestAuthorizedPartyMismatchIsRefused(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)
	m.override = map[string]any{"aud": []string{testClientID, "another-client"}, "azp": "another-client"}

	private, query := r.start("idp-1", m)
	assertRefused(t, r, private, query, outcomeAudience, "idp-1")
}

// TestExpiredTokenIsRefused signs a token that expired an hour ago.
func TestExpiredTokenIsRefused(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)
	m.override = map[string]any{"exp": int64(1)}

	private, query := r.start("idp-1", m)
	assertRefused(t, r, private, query, outcomeTime, "idp-1")
}

// TestEmptySubjectIsRefused signs a token with no subject.
func TestEmptySubjectIsRefused(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)
	m.override = map[string]any{"sub": ""}

	private, query := r.start("idp-1", m)
	assertRefused(t, r, private, query, outcomeOther, "idp-1")
}

// TestMissingNonceIsRefused signs a token with no nonce.
func TestMissingNonceIsRefused(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)
	m.dropNonce = true

	private, query := r.start("idp-1", m)
	assertRefused(t, r, private, query, outcomeOther, "idp-1")
}

// TestChangedNonceIsRefused changes the nonce the flow keeps, so the token's
// nonce no longer matches.
func TestChangedNonceIsRefused(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)

	private, query := r.start("idp-1", m)
	st, ok := readState(private)
	if !ok {
		t.Fatal("the flow's private state is not the method's")
	}
	tampered := writeState(st.Provider, st.State, st.Verifier, "a-different-nonce")
	assertRefused(t, r, tampered, query, outcomeOther, "idp-1")
}

// TestAlgorithmNoneIsRefused serves a token with the unsigned "none" algorithm.
func TestAlgorithmNoneIsRefused(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)
	m.signRaw = func(claims map[string]any) (string, error) {
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
		payload, err := json.Marshal(claims)
		if err != nil {
			return "", err
		}
		return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".", nil
	}

	private, query := r.start("idp-1", m)
	assertRefused(t, r, private, query, outcomeSignature, "idp-1")
}

// TestSymmetricAlgorithmIsRefused serves a token signed with a symmetric
// algorithm, which the provider's configured algorithms do not admit.
func TestSymmetricAlgorithmIsRefused(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)
	m.signRaw = func(claims map[string]any) (string, error) {
		payload, err := json.Marshal(claims)
		if err != nil {
			return "", err
		}
		key := []byte("0123456789abcdef0123456789abcdef")
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT"))
		if err != nil {
			return "", err
		}
		obj, err := signer.Sign(payload)
		if err != nil {
			return "", err
		}
		return obj.CompactSerialize()
	}

	private, query := r.start("idp-1", m)
	assertRefused(t, r, private, query, outcomeSignature, "idp-1")
}

// TestMissingCodeIsRefused drops the authorization code from the redirect.
func TestMissingCodeIsRefused(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)

	private, query := r.start("idp-1", m)
	query.Del("code")
	assertRefused(t, r, private, query, outcomeOther, "idp-1")
}

// TestRefusedVerifierIsRefused rewrites the PKCE verifier, so the token
// endpoint refuses the code.
func TestRefusedVerifierIsRefused(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)

	private, query := r.start("idp-1", m)
	st, ok := readState(private)
	if !ok {
		t.Fatal("the flow's private state is not the method's")
	}
	tampered := writeState(st.Provider, st.State, "a-different-verifier-0123456789abcdefghij", st.Nonce)
	assertRefused(t, r, tampered, query, outcomeOther, "idp-1")
}

// TestProviderErrorIsRefusedWithoutAnOutcome holds that a provider that reported
// the request was refused (the user declined) is refused and records no
// assertion outcome.
func TestProviderErrorIsRefusedWithoutAnOutcome(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)

	private, _ := r.begin("idp-1")
	query := url.Values{"error": {"access_denied"}, "state": {"x"}}
	got := r.answer(private, query)
	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("Answer answered %s, want refused", step.KindOf(got))
	}
	if n := counterValue(t, r.metrics, assertionsName, protocolAttribute, protocolOIDC); n != 0 {
		t.Errorf("assertions recorded = %d, want none for a cancelled flow", n)
	}
}

// TestMissingProviderIsRefused holds that a start input that names no provider
// is refused before anything is drawn.
func TestMissingProviderIsRefused(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	got, err := r.p.Begin(t.Context(), authn.Start{Purpose: authn.PurposeLogin})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("Begin answered %s, want refused", step.KindOf(got))
	}
}

// TestDisabledProviderIsAnOutage holds that a disabled provider row is answered
// as an outage, not a refusal.
func TestDisabledProviderIsAnOutage(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	row := r.putProvider("idp-1", "tenant-a", m, nil)
	row.Enabled = false
	r.providers.put(row, testClientSecret)

	got, err := r.p.Begin(t.Context(), authn.Start{Purpose: authn.PurposeLogin, Fields: map[string]string{fieldProvider: "idp-1"}})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if step.KindOf(got) != step.KindUnavailable {
		t.Fatalf("Begin answered %s, want unavailable", step.KindOf(got))
	}
}

// TestDiscoveryDownIsAnOutage holds that a provider whose discovery document
// cannot be reached answers an outage at Begin.
func TestDiscoveryDownIsAnOutage(t *testing.T) {
	m := newMockIDP(t)
	m.discoveryStatus = 500
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)

	got, err := r.p.Begin(t.Context(), authn.Start{Purpose: authn.PurposeLogin, Fields: map[string]string{fieldProvider: "idp-1"}})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if step.KindOf(got) != step.KindUnavailable {
		t.Fatalf("Begin answered %s, want unavailable", step.KindOf(got))
	}
}

// TestTokenEndpointOutageIsAnOutage holds that a token endpoint that answers a
// server error is an outage and not a refusal.
func TestTokenEndpointOutageIsAnOutage(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)
	m.tokenStatus = 503

	private, query := r.start("idp-1", m)
	got := r.answer(private, query)
	if step.KindOf(got) != step.KindUnavailable {
		t.Fatalf("Answer answered %s, want unavailable", step.KindOf(got))
	}
}

// TestProviderConfigIssuerNotVerbatimIsAnOutage holds that a provider whose
// configured issuer is not the one its discovery document names cannot be used.
func TestProviderConfigIssuerNotVerbatimIsAnOutage(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	raw, err := json.Marshal(map[string]any{"issuer": m.issuer() + "/x", "client_id": testClientID})
	if err != nil {
		t.Fatal(err)
	}
	r.providers.put(authn.SSOProvider{
		ID: "idp-1", Tenant: "tenant-a", Protocol: authn.SSOProtocolOIDC,
		Enabled: true, Provisioning: authn.ProvisioningJIT, Config: raw, Version: 1,
	}, testClientSecret)

	got, err := r.p.Begin(t.Context(), authn.Start{Purpose: authn.PurposeLogin, Fields: map[string]string{fieldProvider: "idp-1"}})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if step.KindOf(got) != step.KindUnavailable {
		t.Fatalf("Begin answered %s, want unavailable", step.KindOf(got))
	}
}

// TestDiscoveryFailureTouchesOneProviderOnly holds that a provider whose
// discovery document is unreachable is an outage while another provider's login
// is unaffected.
func TestDiscoveryFailureTouchesOneProviderOnly(t *testing.T) {
	broken := newMockIDP(t)
	broken.discoveryStatus = 500
	healthy := newMockIDP(t)
	r := newRig(t, broken, healthy)
	r.putProvider("broken", "tenant-a", broken, nil)
	r.putProvider("healthy", "tenant-b", healthy, nil)

	got, err := r.p.Begin(t.Context(), authn.Start{Purpose: authn.PurposeLogin, Fields: map[string]string{fieldProvider: "broken"}})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if step.KindOf(got) != step.KindUnavailable {
		t.Fatalf("Begin on the broken provider answered %s, want unavailable", step.KindOf(got))
	}
	got = r.run("healthy", healthy)
	if step.KindOf(got) != step.KindProven {
		t.Fatalf("Answer on the healthy provider answered %s, want proven", step.KindOf(got))
	}
}

// TestIdPAssertsMultiFactorWhenTheACRMatches holds that a provider that trusts
// its IdP's declaration and whose token carries a trusted acr value sets the
// assertion.
func TestIdPAssertsMultiFactorWhenTheACRMatches(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	row := r.putProvider("idp-1", "tenant-a", m, nil)
	row.TrustIDPMFA = true
	raw, err := json.Marshal(map[string]any{
		"issuer":         m.issuer(),
		"client_id":      testClientID,
		"claims":         map[string]string{"email": "email", "name": "display_name"},
		"mfa_acr_values": []string{"mfa", "urn:mace:incommon:iap:silver"},
	})
	if err != nil {
		t.Fatal(err)
	}
	row.Config = raw
	r.providers.put(row, testClientSecret)
	m.override = map[string]any{"acr": "mfa"}

	got := r.run("idp-1", m)
	if step.KindOf(got) != step.KindProven {
		t.Fatalf("Answer answered %s, want proven", step.KindOf(got))
	}
	if !step.ProofOf(got).IDPAsserted {
		t.Error("IDPAsserted = false, want true: acr is a trusted value")
	}
}

// TestACRNotInTheTrustedSetIsNotAsserted holds that an acr outside the
// configured set leaves the assertion unset.
func TestACRNotInTheTrustedSetIsNotAsserted(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	row := r.putProvider("idp-1", "tenant-a", m, nil)
	row.TrustIDPMFA = true
	raw, err := json.Marshal(map[string]any{
		"issuer":         m.issuer(),
		"client_id":      testClientID,
		"claims":         map[string]string{"email": "email"},
		"mfa_acr_values": []string{"mfa"},
	})
	if err != nil {
		t.Fatal(err)
	}
	row.Config = raw
	r.providers.put(row, testClientSecret)
	m.override = map[string]any{"acr": "urn:unknown"}

	got := r.run("idp-1", m)
	if step.KindOf(got) != step.KindProven {
		t.Fatalf("Answer answered %s, want proven", step.KindOf(got))
	}
	if step.ProofOf(got).IDPAsserted {
		t.Error("IDPAsserted = true, want false: acr is not a trusted value")
	}
}

// assertRefused drives Answer and holds that it is refused under the outcome,
// logging an authn.sso.reject audit row for the provider.
func assertRefused(t *testing.T, r *rig, private []byte, query url.Values, outcome, idpID string) {
	t.Helper()
	got := r.answer(private, query)
	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("Answer answered %s, want refused", step.KindOf(got))
	}
	if n := r.assertionCount(protocolOIDC, outcome); n != 1 {
		t.Errorf("assertions for outcome %q = %d, want 1", outcome, n)
	}
	rows := r.audit.all()
	if len(rows) != 1 {
		t.Fatalf("audit rows = %v, want one", rows)
	}
	if rows[0].Action != actionReject || rows[0].Object != idpID || rows[0].Outcome != outcome {
		t.Errorf("audit row = %+v, want action=%s object=%s outcome=%s", rows[0], actionReject, idpID, outcome)
	}
}
