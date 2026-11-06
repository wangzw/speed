package oidc

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// TestBuildProfileMapsClaims holds the attribute mapping: subject, address and
// display name from the claims a mapping names, the subject falling back to the
// token's own subject only when no mapping names one, and every attr.<name>
// target becoming a read-only idp.* attribute.
func TestBuildProfileMapsClaims(t *testing.T) {
	cfg := providerConfig{Claims: map[string]string{
		"preferred_username": "subject",
		"mail":               "email",
		"cn":                 "display_name",
		"department":         "attr.department",
	}}
	claims := map[string]any{
		"preferred_username": "alice",
		"mail":               "alice@example.com",
		"cn":                 "Alice",
		"department":         "engineering",
		"email_verified":     true,
	}
	got := buildProfile(cfg, "ignored", claims)
	if got.subject != "alice" || got.email != "alice@example.com" || got.displayName != "Alice" {
		t.Errorf("profile = %+v, want the mapped values", got)
	}
	// preferred_username is read as the standard claim itself, not through the
	// mapping: a claim-strategy just-in-time login names it as the source of the
	// username it creates the account with.
	if got.preferredUsername != "alice" {
		t.Errorf("preferredUsername = %q, want the standard claim's value", got.preferredUsername)
	}
	if !got.emailVerified {
		t.Error("emailVerified = false, want true")
	}
	if got.attributes["idp.department"] != "engineering" {
		t.Errorf("attributes = %v, want idp.department=engineering", got.attributes)
	}
}

// TestBuildProfileDefaultsSubjectToSub holds that the subject is the token's own
// subject when no mapping names one.
func TestBuildProfileDefaultsSubjectToSub(t *testing.T) {
	got := buildProfile(providerConfig{}, "the-sub", map[string]any{"email": "x@y.z"})
	if got.subject != "the-sub" {
		t.Errorf("subject = %q, want the-sub", got.subject)
	}
	if got.email != "" {
		t.Errorf("email = %q, want empty: no mapping names one", got.email)
	}
}

// TestBuildProfileEmptyWhenMappedClaimIsAbsent holds that a mapping naming a
// claim the token does not carry leaves the target empty rather than filling it
// from elsewhere.
func TestBuildProfileEmptyWhenMappedClaimIsAbsent(t *testing.T) {
	cfg := providerConfig{Claims: map[string]string{"sub_alt": "subject"}}
	got := buildProfile(cfg, "the-sub", map[string]any{"email": "x@y.z"})
	if got.subject != "" {
		t.Errorf("subject = %q, want empty: the mapped claim is absent and the default does not apply", got.subject)
	}
}

// TestClassifyVerify holds the metric label of a token the verifier refused: the
// token's own unverified claims tell an expiry, an audience and an issuer apart,
// and everything else reads as a signature failure.
func TestClassifyVerify(t *testing.T) {
	cfg := providerConfig{Issuer: "https://idp.example", ClientID: "client-1"}
	now := time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour).Unix()
	past := now.Add(-time.Hour).Unix()

	cases := []struct {
		name   string
		claims map[string]any
		want   string
	}{
		{"valid claims but a bad signature", map[string]any{"iss": cfg.Issuer, "aud": []string{"client-1"}, "exp": future}, outcomeSignature},
		{"expired", map[string]any{"iss": cfg.Issuer, "aud": []string{"client-1"}, "exp": past}, outcomeTime},
		{"foreign audience", map[string]any{"iss": cfg.Issuer, "aud": []string{"other"}, "exp": future}, outcomeAudience},
		{"foreign issuer", map[string]any{"iss": "https://other.example", "aud": []string{"client-1"}, "exp": future}, outcomeIssuer},
		{"audience as a bare string", map[string]any{"iss": cfg.Issuer, "aud": "client-1", "exp": future}, outcomeSignature},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyVerify(rawWithClaims(t, tc.claims), cfg, now); got != tc.want {
				t.Errorf("classifyVerify = %q, want %q", got, tc.want)
			}
		})
	}
	if got := classifyVerify("not-a-jwt", cfg, now); got != outcomeSignature {
		t.Errorf("classifyVerify of a non-JWT = %q, want %q", got, outcomeSignature)
	}
}

// rawWithClaims builds a compact JWT string carrying the claims, with a header
// and signature segment that are never read: the classifier reads the payload
// alone.
func rawWithClaims(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256"}`))
	body := base64.RawURLEncoding.EncodeToString(payload)
	return head + "." + body + ".signature"
}
