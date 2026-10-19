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
// bindings and the two preferences read out of the attribute set.
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
	if got.Locale != "zh-CN" || got.TimeZone != "Asia/Shanghai" {
		t.Errorf("locale/time_zone = %q/%q, want zh-CN/Asia/Shanghai", got.Locale, got.TimeZone)
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

// TestGetIsReachableWhileImpersonating holds the one route an impersonating
// identity may reach: it reads the impersonated account.
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
