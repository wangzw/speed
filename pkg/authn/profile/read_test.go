package profile

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/i18n"
)

func decodeProfile(t *testing.T, rec *httptest.ResponseRecorder) AuthnProfileProfile {
	t.Helper()
	var v AuthnProfileProfile
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decoding the profile: %v (body %s)", err, rec.Body.String())
	}
	return v
}

// TestGetReturnsTheCallersOwnProfile holds the read face: the profile is built
// from the account the identity names, with the identifiers, the external
// bindings and the three preferences read out of the attribute set.
func TestGetReturnsTheCallersOwnProfile(t *testing.T) {
	r := newRig(t)
	created := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)
	r.accounts.put(meAccount, authn.Account{
		Kind:        authn.KindUser,
		Status:      authn.StatusActive,
		DisplayName: "Me",
		MFARequired: true,
		PasswordSet: true,
		CreatedAt:   created,
		Attributes: map[string]string{
			"dept":                    "eng",
			i18n.LocaleAttributeKey:   "zh-CN",
			i18n.TimeZoneAttributeKey: "Asia/Shanghai",
			ColorModeAttributeKey:     "dark",
		},
	})
	r.accounts.identifiers[meAccount] = []authn.Identifier{{
		ID: "id-1", Kind: authn.IdentifierEmail, Value: "me@example.com",
		VerifiedAt: created, VerifiedVia: authn.VerifiedViaAdmin,
	}}
	r.accounts.links[meAccount] = []authn.ExternalBinding{{
		ID: "link-1", Provider: "idp-1", Subject: "the-subject", Email: "me@example.com",
		CreatedAt: created,
	}}

	rec := r.do(http.MethodGet, "/api/v1/auth/me", nil, me()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got := decodeProfile(t, rec)
	if got.AccountID != string(meAccount) {
		t.Errorf("account_id = %q, want %q", got.AccountID, meAccount)
	}
	if got.DisplayName != "Me" || got.Kind != "user" || !got.MFARequired || !got.PasswordSet {
		t.Errorf("the account fields are wrong: %+v", got)
	}
	if got.CreatedAt != created.Format(time.RFC3339) {
		t.Errorf("created_at = %q, want %q", got.CreatedAt, created.Format(time.RFC3339))
	}
	if got.Locale != "zh-CN" || got.TimeZone != "Asia/Shanghai" || got.ColorMode != "dark" {
		t.Errorf("locale/time_zone/color_mode = %q/%q/%q, want zh-CN/Asia/Shanghai/dark",
			got.Locale, got.TimeZone, got.ColorMode)
	}
	if got.Attributes["dept"] != "eng" {
		t.Errorf("attributes = %v, want dept=eng", got.Attributes)
	}
	if len(got.Identifiers) != 1 || got.Identifiers[0].Value != "me@example.com" {
		t.Errorf("identifiers = %+v, want the one seeded", got.Identifiers)
	}
	if len(got.Links) != 1 || got.Links[0].Provider != "idp-1" {
		t.Errorf("links = %+v, want the one seeded", got.Links)
	}
}

// TestGetCarriesNoSubjectInALink holds the view's shape: the provider's own name
// for the person reaches no answer.
func TestGetCarriesNoSubjectInALink(t *testing.T) {
	r := newRig(t)
	r.accounts.links[meAccount] = []authn.ExternalBinding{{
		ID: "link-1", Provider: "idp-1", Subject: "the-subject-secret",
	}}
	rec := r.do(http.MethodGet, "/api/v1/auth/me", nil, me()...)
	if body := rec.Body.String(); strings.Contains(body, "the-subject-secret") {
		t.Errorf("the answer carries the binding's subject: %s", body)
	}
}

// TestGetZeroCreatedAtIsEmpty holds the instant rule: a zero time is written as
// the empty string.
func TestGetZeroCreatedAtIsEmpty(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodGet, "/api/v1/auth/me", nil, me()...)
	got := decodeProfile(t, rec)
	if got.CreatedAt != "" {
		t.Errorf("created_at = %q, want the empty string for a zero time", got.CreatedAt)
	}
}

// TestGetIsReachableWhileImpersonating holds that an impersonating identity
// reads the impersonated account.
func TestGetIsReachableWhileImpersonating(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodGet, "/api/v1/auth/me", nil, impersonating()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got := decodeProfile(t, rec)
	if got.AccountID != string(meAccount) {
		t.Errorf("account_id = %q, want the impersonated account %q", got.AccountID, meAccount)
	}
}

// TestGetWithoutAnIdentityIs401 holds the read face's refusal: a request that
// reaches the handler with no identity is the gateway's own 401.
func TestGetWithoutAnIdentityIs401(t *testing.T) {
	r := newRig(t)
	if rec := r.doAnonymous(http.MethodGet, "/api/v1/auth/me", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("through the gateway: status = %d, want 401", rec.Code)
	}
	if rec := r.invokeAnonymous(getOperation.Handle(r.p.handleGetMe), http.MethodGet, "/api/v1/auth/me", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("the handler with no identity: status = %d, want 401", rec.Code)
	}
}

// TestTheHandlersOwn401CarriesTheCapabilityChallenge holds the header a 401 of
// the read handler itself carries: the endpoint's Bearer and Cookie challenge,
// in the header the declared 401 response declares. RFC 9110 requires a 401 to
// carry one, and the declared header is what lets the handler's fault write it.
func TestTheHandlersOwn401CarriesTheCapabilityChallenge(t *testing.T) {
	const want = `Bearer realm="main", Cookie realm="main"`
	cases := []struct {
		name string
		arm  func(*rig)
		call func(*rig) *httptest.ResponseRecorder
	}{
		{
			name: "no identity",
			arm:  func(*rig) {},
			call: func(r *rig) *httptest.ResponseRecorder {
				return r.invokeAnonymous(getOperation.Handle(r.p.handleGetMe), http.MethodGet, "/api/v1/auth/me", nil)
			},
		},
		{
			name: "the account is gone",
			arm:  func(r *rig) { r.accounts.getErr = authn.ErrAccountNotFound },
			call: func(r *rig) *httptest.ResponseRecorder {
				return r.invoke(getOperation.Handle(r.p.handleGetMe), http.MethodGet, "/api/v1/auth/me", nil, me()...)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			tc.arm(r)
			rec := tc.call(r)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if got := rec.Header().Get("WWW-Authenticate"); got != want {
				t.Errorf("WWW-Authenticate = %q, want %q", got, want)
			}
		})
	}
}

// TestGetOfAGoneAccountIs401 holds the mapping: the identity's own account gone
// is the 401 a read of it gets, not a 404.
func TestGetOfAGoneAccountIs401(t *testing.T) {
	r := newRig(t)
	r.accounts.getErr = authn.ErrAccountNotFound
	if rec := r.do(http.MethodGet, "/api/v1/auth/me", nil, me()...); rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// TestGetDependencyFailureIs503 holds the fail-closed default: a failure no
// sentinel names is a 503.
func TestGetDependencyFailureIs503(t *testing.T) {
	cases := map[string]func(*rig){
		"the account read":       func(r *rig) { r.accounts.getErr = errors.New("boom") },
		"the identifier listing": func(r *rig) { r.accounts.idErr = errors.New("boom") },
		"the binding listing":    func(r *rig) { r.accounts.extErr = errors.New("boom") },
	}
	for name, arm := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			arm(r)
			if rec := r.do(http.MethodGet, "/api/v1/auth/me", nil, me()...); rec.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 503", rec.Code)
			}
		})
	}
}

// TestGetVerificationShowsEachState holds the three states the read face
// publishes and the field each one carries: none with no address and no
// instant, pending with the contact awaiting verification, verified with the
// address the account module reports and the instant the account first reached
// it.
func TestGetVerificationShowsEachState(t *testing.T) {
	since := time.Date(2026, time.April, 5, 6, 7, 8, 0, time.UTC)
	for _, c := range []struct {
		name   string
		status authn.SignupStatus
		want   AuthnProfileVerification
	}{
		{
			name:   "none",
			status: authn.SignupStatus{State: authn.SignupNone},
			want:   AuthnProfileVerification{State: "none"},
		},
		{
			name:   "pending",
			status: authn.SignupStatus{State: authn.SignupPending, Email: "me@example.com"},
			want:   AuthnProfileVerification{State: "pending", Email: "me@example.com"},
		},
		{
			name:   "verified",
			status: authn.SignupStatus{State: authn.SignupVerified, Email: "first@example.com", Since: since},
			want: AuthnProfileVerification{
				State: "verified", Email: "first@example.com", Since: since.Format(time.RFC3339),
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.verification.state = c.status
			rec := r.do(http.MethodGet, "/api/v1/auth/me", nil, me()...)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
			if got := decodeProfile(t, rec).Verification; got != c.want {
				t.Errorf("verification = %+v, want %+v", got, c.want)
			}
			if r.verification.readCount() != 1 {
				t.Errorf("the state was read %d times, want once per request", r.verification.readCount())
			}
		})
	}
}

// TestGetPendingContactIsNotAnIdentifier holds the boundary between the two
// read surfaces: the address awaiting verification is reported only by
// verification.email and never appears among the identifiers, which carry
// proved facts alone.
func TestGetPendingContactIsNotAnIdentifier(t *testing.T) {
	r := newRig(t)
	r.accounts.identifiers[meAccount] = []authn.Identifier{{
		ID: "id-1", Kind: authn.IdentifierUsername, Value: "me",
		VerifiedAt: time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC), VerifiedVia: authn.VerifiedViaSelf,
	}}
	r.verification.state = authn.SignupStatus{State: authn.SignupPending, Email: "awaiting@example.com"}
	rec := r.do(http.MethodGet, "/api/v1/auth/me", nil, me()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got := decodeProfile(t, rec)
	if got.Verification.Email != "awaiting@example.com" {
		t.Errorf("verification.email = %q, want the pending contact", got.Verification.Email)
	}
	for _, identifier := range got.Identifiers {
		if identifier.Value == "awaiting@example.com" {
			t.Errorf("the pending contact appears among the identifiers: %+v", got.Identifiers)
		}
	}
}

// TestGetIdentifierProvenFollowsTheVerificationChannel holds the derivation of
// proven over the closed set of channels: the four proofs read true and the
// three assertions read false.
func TestGetIdentifierProvenFollowsTheVerificationChannel(t *testing.T) {
	channels := []struct {
		via    string
		proven bool
	}{
		{authn.VerifiedViaSMSCode, true},
		{authn.VerifiedViaEmailCode, true},
		{authn.VerifiedViaInvite, true},
		{authn.VerifiedViaIDPPrefix + "0190a6f2-3b6c-7c2e-8a4d-5e6f7a8b9c0d", true},
		{authn.VerifiedViaAdmin, false},
		{authn.VerifiedViaImportUnverified, false},
		{authn.VerifiedViaSelf, false},
	}
	r := newRig(t)
	for i, c := range channels {
		r.accounts.identifiers[meAccount] = append(r.accounts.identifiers[meAccount], authn.Identifier{
			ID: "id-" + string(rune('a'+i)), Kind: authn.IdentifierEmail,
			Value: "channel" + string(rune('a'+i)) + "@example.com", VerifiedAt: time.Now(), VerifiedVia: c.via,
		})
	}
	rec := r.do(http.MethodGet, "/api/v1/auth/me", nil, me()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got := decodeProfile(t, rec)
	if len(got.Identifiers) != len(channels) {
		t.Fatalf("identifiers = %+v, want one per channel", got.Identifiers)
	}
	for i, c := range channels {
		if got.Identifiers[i].Proven != c.proven {
			t.Errorf("%q reads proven=%v, want %v", c.via, got.Identifiers[i].Proven, c.proven)
		}
	}
}

// TestGetIdentifierCarriesTheTwoAxesAndTheDefaultContact holds that the profile
// exposes the stored flags as they are, independently of how ownership was
// established.
func TestGetIdentifierCarriesTheTwoAxesAndTheDefaultContact(t *testing.T) {
	r := newRig(t)
	r.accounts.identifiers[meAccount] = []authn.Identifier{
		{
			ID: "id-email", Kind: authn.IdentifierEmail, Value: "me@example.com",
			VerifiedAt: time.Now(), VerifiedVia: authn.VerifiedViaEmailCode,
			SignIn: true, Contact: true, DefaultContact: true,
		},
		{
			ID: "id-name", Kind: authn.IdentifierUsername, Value: "me",
			VerifiedAt: time.Now(), VerifiedVia: authn.VerifiedViaAdmin,
		},
	}
	rec := r.do(http.MethodGet, "/api/v1/auth/me", nil, me()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got := decodeProfile(t, rec)
	if len(got.Identifiers) != 2 {
		t.Fatalf("identifiers = %+v, want the two seeded", got.Identifiers)
	}
	email, name := got.Identifiers[0], got.Identifiers[1]
	if !email.SignIn || !email.Contact || !email.DefaultContact {
		t.Errorf("the email reads sign_in=%v contact=%v default_contact=%v, want all true",
			email.SignIn, email.Contact, email.DefaultContact)
	}
	if name.SignIn || name.Contact || name.DefaultContact {
		t.Errorf("the username reads sign_in=%v contact=%v default_contact=%v, want all false",
			name.SignIn, name.Contact, name.DefaultContact)
	}
}

// TestGetSignupStateReadFailureIs503 holds the read failure's direction: a
// state that cannot be read is answered as unavailable and never as none.
func TestGetSignupStateReadFailureIs503(t *testing.T) {
	r := newRig(t)
	r.verification.err = authn.ErrUnavailable
	rec := r.do(http.MethodGet, "/api/v1/auth/me", nil, me()...)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"state"`) {
		t.Errorf("a failed read was answered as a profile: %s", rec.Body.String())
	}
}
