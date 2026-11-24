package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

func auditHas(t *testing.T, r *rig, action, outcome string) bool {
	t.Helper()
	for _, e := range r.audit.entries() {
		if e.Action == action && e.Outcome == outcome {
			return true
		}
	}
	return false
}

func auditNamesObject(t *testing.T, r *rig, action, object string) bool {
	t.Helper()
	for _, e := range r.audit.entries() {
		if e.Action == action && e.Object == object {
			return true
		}
	}
	return false
}

func identifiersIn(rows []audit.Entry, key string) map[string]bool {
	out := map[string]bool{}
	for _, row := range rows {
		out[row.Detail[key]] = true
	}
	return out
}

func problemCode(t *testing.T, body string) string {
	t.Helper()
	return codeOf(problemType(t, body))
}

func problemType(t *testing.T, body string) string {
	t.Helper()
	var p struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("decoding the problem body %q: %v", body, err)
	}
	return p.Type
}

func codeOf(problemType string) string {
	if i := strings.LastIndex(problemType, ":"); i >= 0 {
		return problemType[i+1:]
	}
	return problemType
}

// TestResettingTheFactorsOfAnAccountWithNoOtherWayInIsRefused checks the 409 last-login-method guard on mfa-reset.
func TestResettingTheFactorsOfAnAccountWithNoOtherWayInIsRefused(t *testing.T) {
	r := newRig(t)
	r.accountAdm.loginWay = false
	rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts/"+string(userID)+"/mfa-reset", nil, fresh()...)
	if rec.Code != http.StatusConflict {
		t.Fatalf("mfa-reset: status = %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	if code := problemCode(t, rec.Body.String()); code != "last-login-method" {
		t.Errorf("mfa-reset: problem code %q, want last-login-method", code)
	}
	if r.mfa.resets != 0 {
		t.Errorf("Reset ran %d times, want none", r.mfa.resets)
	}
	if !auditHas(t, r, actionMFAReset, outcomeFailed) {
		t.Error("the refused reset left no failed audit row")
	}
}

// TestResettingTheFactorsOfAnAccountWithAnotherWayInIsAllowed is the other side of the guard: an account with a non-factor way to log in is reset as before.
func TestResettingTheFactorsOfAnAccountWithAnotherWayInIsAllowed(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts/"+string(userID)+"/mfa-reset", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("mfa-reset: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if r.mfa.resets != 1 {
		t.Errorf("Reset ran %d times, want once", r.mfa.resets)
	}
}

// TestTheStrongRoutesNeedASeasonedAdministratorWithANonSMSSecond checks the strength and seasoned-factor gates on the two resets.
func TestTheStrongRoutesNeedASeasonedAdministratorWithANonSMSSecond(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts/"+string(userID)+"/mfa-reset", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("mfa-reset: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if r.mfa.resets != 1 {
		t.Errorf("Reset ran %d times, want once", r.mfa.resets)
	}

	r = newRig(t)
	rec = r.do(http.MethodPost, "/api/v1/system/auth/accounts/"+string(userID)+"/password",
		map[string]any{"password": "a-long-temporary-one"}, fresh()...)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("password reset: status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if r.passwords.calls != 1 {
		t.Errorf("Set ran %d times, want once", r.passwords.calls)
	}

	r = newRig(t)
	webauthn := fresh(authntest.WithMethods(authn.MethodPassword, authn.MethodWebAuthn))
	rec = r.do(http.MethodPost, "/api/v1/system/auth/accounts/"+string(userID)+"/mfa-reset", nil, webauthn...)
	if rec.Code != http.StatusOK {
		t.Fatalf("WebAuthn mfa-reset: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if r.mfa.resets != 1 {
		t.Errorf("Reset ran %d times for a WebAuthn administrator, want once", r.mfa.resets)
	}

	r = newRig(t)
	r.mfa.seasoned = false
	rec = r.do(http.MethodPost, "/api/v1/system/auth/accounts/"+string(userID)+"/mfa-reset", nil, fresh()...)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unseasoned mfa-reset: status = %d, want 403; body %s", rec.Code, rec.Body.String())
	}
	if code := problemCode(t, rec.Body.String()); code != "factor-not-seasoned" {
		t.Errorf("unseasoned mfa-reset: problem code %q, want factor-not-seasoned", code)
	}
	if r.mfa.resets != 0 {
		t.Error("Reset ran though the actor had no seasoned factor")
	}
	rec = r.do(http.MethodPost, "/api/v1/system/auth/accounts/"+string(userID)+"/password",
		map[string]any{"password": "a-long-temporary-one"}, fresh()...)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unseasoned password reset: status = %d, want 403; body %s", rec.Code, rec.Body.String())
	}
	if code := problemCode(t, rec.Body.String()); code != "factor-not-seasoned" {
		t.Errorf("unseasoned password reset: problem code %q, want factor-not-seasoned", code)
	}
	if r.passwords.calls != 0 {
		t.Error("Set ran though the actor had no seasoned factor")
	}

	weaker := []struct {
		name  string
		setup func(*rig)
		opts  func() []authntest.Option
	}{
		{"a stale authentication", func(*rig) {}, func() []authntest.Option {
			return fresh(authntest.WithAuthTime(time.Now().Add(-time.Hour)))
		}},
		{"SMS as the only second method", func(*rig) {}, func() []authntest.Option {
			return fresh(authntest.WithMethods(authn.MethodPassword, authn.MethodSMS))
		}},
	}
	for _, tc := range weaker {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			tc.setup(r)
			if rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts/"+string(userID)+"/mfa-reset", nil, tc.opts()...); rec.Code != http.StatusUnauthorized {
				t.Errorf("mfa-reset: status = %d, want 401", rec.Code)
			}
			if r.mfa.resets != 0 {
				t.Error("Reset ran though the actor was not strong enough")
			}
			if rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts/"+string(userID)+"/password",
				map[string]any{"password": "a-long-temporary-one"}, tc.opts()...); rec.Code != http.StatusUnauthorized {
				t.Errorf("password reset: status = %d, want 401", rec.Code)
			}
			if r.passwords.calls != 0 {
				t.Error("Set ran though the actor was not strong enough")
			}
		})
	}
}

// TestARefusalStopsEveryReadRoute checks that a Decider refusal stops every read before the capability is called.
func TestARefusalStopsEveryReadRoute(t *testing.T) {
	r := newRig(t)
	r.accounts.put("acct-t2", authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
	r.decider.allow = false

	cases := []struct {
		name string
		path string
		read func() int
	}{
		{"account list", "/api/v1/system/auth/accounts", func() int { return r.accounts.listCalls }},
		{"member list", "/api/v1/tenants/" + tenantA + "/members", func() int { return r.members.listCalls }},
		{"session list", "/api/v1/system/auth/accounts/acct-t2/sessions", func() int { return r.sessions.listCalls }},
		{"factor list", "/api/v1/system/auth/accounts/acct-t2/factors", func() int { return r.mfa.factorCalls }},
		{"secret list", "/api/v1/system/auth/accounts/acct-t2/secrets", func() int { return r.accounts.secretCalls }},
		{"provider list", "/api/v1/tenants/" + tenantA + "/sso/providers", func() int { return r.providers.listCalls }},
		{"impersonation list", "/api/v1/system/tenants/" + tenantA + "/impersonations", func() int { return r.records.listCalls }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := r.do(http.MethodGet, tc.path, nil, fresh()...)
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}
			if got := tc.read(); got != 0 {
				t.Errorf("the capability read ran %d times despite a refusal", got)
			}
		})
	}
}

// TestTheSSOExemptRoute checks the sso-exempt write and the 404 of a missing membership before the Decider.
func TestTheSSOExemptRoute(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPut, "/api/v1/tenants/"+tenantA+"/members/"+string(userID)+"/sso-exempt",
		map[string]any{"sso_exempt": true}, fresh()...)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if puts := r.members.puts; len(puts) == 0 || !puts[len(puts)-1].SSOExempt {
		t.Errorf("the stored membership's SSOExempt = %+v, want true", r.members.puts)
	}
	if !auditHas(t, r, actionMembershipSet, outcomeSuccess) {
		t.Error("the mandatory audit row was not written")
	}

	missing := newRig(t)
	rec = missing.do(http.MethodPut, "/api/v1/tenants/"+tenantA+"/members/absent/sso-exempt",
		map[string]any{"sso_exempt": true}, fresh()...)
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing membership: status = %d, want 404", rec.Code)
	}
	if missing.decider.count() != 0 {
		t.Error("the Decider was asked for a membership that is not there")
	}
}

// TestProviderWarnings checks that each loosening setting surfaces its warning and a default provider carries none.
func TestProviderWarnings(t *testing.T) {
	r := newRig(t)
	r.providers.put(authn.SSOProvider{
		ID: idpA, Tenant: tenantA, Protocol: authn.SSOProtocolSAML, Provisioning: authn.ProvisioningJIT,
		AllowIDPInitiated: true, MergeExisting: true, AllowSHA1: true,
	})

	want := []string{"idp-initiated", "SHA-1"}

	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/sso/providers/"+idpA, nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	assertWarnings(t, providerWarningsFrom(t, rec.Body.String()), want)

	rec = r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/sso/providers", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Providers []struct {
			Warnings []string `json:"warnings"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decoding the list body: %v", err)
	}
	if len(listed.Providers) != 1 {
		t.Fatalf("the list holds %d providers, want 1", len(listed.Providers))
	}
	assertWarnings(t, listed.Providers[0].Warnings, want)

	def := newRig(t)
	def.providers.put(authn.SSOProvider{ID: idpA, Tenant: tenantA, Protocol: authn.SSOProtocolSAML, Provisioning: authn.ProvisioningJIT})
	rec = def.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/sso/providers/"+idpA, nil, fresh()...)
	if got := providerWarningsFrom(t, rec.Body.String()); len(got) != 0 {
		t.Errorf("a default provider carries warnings %v, want none", got)
	}
}

func providerWarningsFrom(t *testing.T, body string) []string {
	t.Helper()
	var out struct {
		Provider struct {
			Warnings []string `json:"warnings"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decoding the provider body %q: %v", body, err)
	}
	return out.Provider.Warnings
}

func assertWarnings(t *testing.T, got, want []string) {
	t.Helper()
	for _, fragment := range want {
		found := false
		for _, w := range got {
			if strings.Contains(w, fragment) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the warnings %v hold no warning naming %q", got, fragment)
		}
	}
}

// TestTheStatusRouteMapsAndRefusesDeleted checks the status mapping and the refusal of a delete through the status route.
func TestTheStatusRouteMapsAndRefusesDeleted(t *testing.T) {
	r := newRig(t)
	for _, tc := range []struct {
		body string
		want authn.AccountStatus
	}{
		{"active", authn.StatusActive},
		{"disabled", authn.StatusDisabled},
	} {
		rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts/"+string(userID)+"/status",
			map[string]any{"status": tc.body}, fresh()...)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %q: status = %d, want 200; body %s", tc.body, rec.Code, rec.Body.String())
		}
		if r.accountAdm.status != tc.want {
			t.Errorf("status %q: SetStatus received %v, want %v", tc.body, r.accountAdm.status, tc.want)
		}
	}

	before := r.accountAdm.count("set-status")
	rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts/"+string(userID)+"/status",
		map[string]any{"status": "deleted"}, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %q: status = %d, want 400", "deleted", rec.Code)
	}
	if r.accountAdm.count("set-status") != before {
		t.Error("SetStatus ran for a delete through the status route")
	}

	rec = r.do(http.MethodPost, "/api/v1/system/auth/accounts/"+string(userID)+"/status",
		map[string]any{"status": "bogus"}, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %q: status = %d, want 400", "bogus", rec.Code)
	}
}

// TestIdentifierAddAssertsOnlyAsAnAdministrator checks that the identifier route accepts only an administrator's assertion.
func TestIdentifierAddAssertsOnlyAsAnAdministrator(t *testing.T) {
	r := newRig(t)
	base := "/api/v1/system/auth/accounts/" + string(userID) + "/identifiers"

	rec := r.do(http.MethodPost, base, map[string]any{"kind": "phone", "value": "+8613800000000"}, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a phone identifier: status = %d, want 400", rec.Code)
	}

	rec = r.do(http.MethodPost, base, map[string]any{"kind": "email", "value": "a@example.com", "verified_via": "sms-code"}, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a claimed sms-code: status = %d, want 400", rec.Code)
	}
	if r.accountAdm.count("add-identifier") != 0 {
		t.Error("AddIdentifier ran for a refused identifier")
	}

	rec = r.do(http.MethodPost, base, map[string]any{"kind": "email", "value": "a@example.com", "verified_via": "admin"}, fresh()...)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("an admin-asserted email: status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if r.accountAdm.addedVia.Via != authn.VerifiedViaAdmin {
		t.Errorf("the identifier was written as %q, want admin", r.accountAdm.addedVia.Via)
	}
}

// TestAccountCreateRefusesAProofVia checks that a create may assert an identifier or an import, not a proof.
func TestAccountCreateRefusesAProofVia(t *testing.T) {
	r := newRig(t)
	for _, body := range []map[string]any{
		{"kind": "user", "username": "a", "email": "a@example.com", "email_verified_via": "sms-code"},
		{"kind": "user", "username": "a", "phone": "+8613800000000", "phone_verified_via": "idp:some-idp"},
	} {
		rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts", body, fresh()...)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%+v: status = %d, want 400", body, rec.Code)
		}
	}
	if r.accountAdm.count("create") != 0 {
		t.Error("Create ran though a proof of ownership was claimed")
	}

	rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts",
		map[string]any{"kind": "user", "username": "a", "email": "a@example.com", "email_verified_via": "admin"}, fresh()...)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rec.Code, rec.Body.String())
	}
	if r.accountAdm.created.EmailVerifiedVia.Via != authn.VerifiedViaAdmin {
		t.Errorf("the email was written as %q, want admin", r.accountAdm.created.EmailVerifiedVia.Via)
	}
}

// TestAccountCreateRefusesAUserAccountWithNoIdentifier checks that a user account without a username is refused before any write.
func TestAccountCreateRefusesAUserAccountWithNoIdentifier(t *testing.T) {
	r := newRig(t)
	for _, body := range []map[string]any{
		{"kind": "user"},
		{"kind": "user", "display_name": "Nobody"},
		{"kind": "user", "attributes": map[string]string{"team": "core"}},
		{"kind": "user", "email": "nobody@example.com", "email_verified_via": "admin"},
		{"kind": "user", "phone": "+8613800000000", "phone_verified_via": "admin"},
		{"kind": "user", "username": "  "},
	} {
		rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts", body, fresh()...)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%v: status = %d, want 400; body %s", body, rec.Code, rec.Body.String())
			continue
		}
		if got := problemCode(t, rec.Body.String()); got != mount.CodeBadRequest {
			t.Errorf("%v: problem code = %q, want %q", body, got, mount.CodeBadRequest)
		}
	}
	if n := r.accountAdm.count("create"); n != 0 {
		t.Errorf("Create ran %d times for an account with no identifier", n)
	}

	rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts",
		map[string]any{"kind": "user", "display_name": "Grace", "username": "grace"}, fresh()...)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a user account with a username: status = %d, want 201; body %s", rec.Code, rec.Body.String())
	}

	rec = r.do(http.MethodPost, "/api/v1/system/auth/accounts",
		map[string]any{"kind": "service", "display_name": "Nightly"}, fresh()...)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a service account with no identifier: status = %d, want 201; body %s", rec.Code, rec.Body.String())
	}
}

// TestTheAnswerMapsEverySentinel checks that each capability sentinel maps to its own status and code, and an unnamed one to 503.
func TestTheAnswerMapsEverySentinel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"account", http.MethodGet, "/api/v1/system/auth/accounts/absent"},
		{"session", http.MethodDelete, "/api/v1/system/auth/sessions/0190a6f2-3b6c-7c2e-8a4d-5e6f7a8b9c99"},
		{"provider", http.MethodGet, "/api/v1/tenants/" + tenantA + "/sso/providers/absent"},
	} {
		t.Run("404 "+tc.name, func(t *testing.T) {
			r := newRig(t)
			if rec := r.do(tc.method, tc.path, nil, fresh()...); rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
		})
	}

	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid argument", authn.ErrInvalidArgument, http.StatusBadRequest, mount.CodeBadRequest},
		{"invalid identifier", authn.ErrInvalidIdentifier, http.StatusBadRequest, mount.CodeBadRequest},
		{"invalid username", authn.UsernameShape(authn.ErrInvalidIdentifier), http.StatusBadRequest, mount.CodeUsernameInvalid},
		{"reserved username", authn.ErrUsernameReserved, http.StatusBadRequest, mount.CodeUsernameReserved},
		{"password policy", authn.ErrPasswordPolicy, http.StatusBadRequest, mount.CodeBadRequest},
		{"invalid config", authn.ErrInvalidConfig, http.StatusBadRequest, mount.CodeBadRequest},
		{"identifier required", authn.ErrIdentifierRequired, http.StatusConflict, mount.CodeLoginIdentifierRequired},
		{"immutable username", authn.ErrUsernameImmutable, http.StatusConflict, mount.CodeUsernameImmutable},
		{"account exists", authn.ErrAccountExists, http.StatusConflict, mount.CodeConflict},
		{"contact taken", authn.ErrContactTaken, http.StatusConflict, mount.CodeEmailTaken},
		{"binding conflict", authn.ErrBindingConflict, http.StatusConflict, mount.CodeConflict},
		{"domain claimed", authn.ErrDomainClaimed, http.StatusConflict, mount.CodeConflict},
		{"last login method", authn.ErrLastLoginMethod, http.StatusConflict, "last-login-method"},
		{"cannot suspend self", authn.ErrCannotSuspendSelf, http.StatusConflict, "cannot-suspend-self"},
		{"factor limit", authn.ErrFactorLimit, http.StatusConflict, mount.CodeConflict},
		{"last factor", authn.ErrLastFactor, http.StatusConflict, mount.CodeConflict},
		{"unsupported region", authn.ErrUnsupportedRegion, http.StatusBadRequest, mount.CodeUnsupportedRegion},
		{"no sentinel", errors.New("an unnamed failure"), http.StatusServiceUnavailable, mount.CodeUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.accountAdm.err = tc.err
			rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts",
				map[string]any{"kind": "user", "username": "a"}, fresh()...)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.status, rec.Body.String())
			}
			if got := problemCode(t, rec.Body.String()); got != tc.code {
				t.Errorf("problem code = %q, want %q", got, tc.code)
			}
		})
	}
}

// TestCreatingAPasswordAccountWithOnlyAPhoneIsRefused checks that a user account whose only identifier is a phone is refused.
func TestCreatingAPasswordAccountWithOnlyAPhoneIsRefused(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts", map[string]any{
		"kind":               "user",
		"phone":              "+8613800000001",
		"phone_verified_via": "admin",
		"password":           "correct horse battery",
	}, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
	}
	if got := problemCode(t, rec.Body.String()); got != mount.CodeBadRequest {
		t.Errorf("problem code = %q, want %q", got, mount.CodeBadRequest)
	}
	if n := r.accountAdm.count("create"); n != 0 {
		t.Errorf("Create ran %d times, want none: the route refused before the module", n)
	}
}

// TestRemovingTheLastLoginNameIsA409 checks that removing the last login name is 409 login-identifier-required.
func TestRemovingTheLastLoginNameIsA409(t *testing.T) {
	r := newRig(t)
	r.accountAdm.err = authn.ErrIdentifierRequired
	rec := r.do(http.MethodDelete, "/api/v1/system/auth/accounts/"+string(userID)+"/identifiers/"+identifier, nil, fresh()...)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	if got := problemCode(t, rec.Body.String()); got != mount.CodeLoginIdentifierRequired {
		t.Errorf("problem code = %q, want %q", got, mount.CodeLoginIdentifierRequired)
	}
	if n := r.accountAdm.count("remove-identifier"); n != 1 {
		t.Errorf("RemoveIdentifier ran %d times, want once", n)
	}
}

// TestRemovingAUsernameIsA409UsernameImmutable checks that a username removal is 409 username-immutable and removes nothing.
func TestRemovingAUsernameIsA409UsernameImmutable(t *testing.T) {
	r := newRig(t)
	r.accountAdm.err = authn.ErrUsernameImmutable
	rec := r.do(http.MethodDelete, "/api/v1/system/auth/accounts/"+string(userID)+"/identifiers/"+identifier, nil, fresh()...)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	if got := problemCode(t, rec.Body.String()); got != mount.CodeUsernameImmutable {
		t.Errorf("problem code = %q, want %q", got, mount.CodeUsernameImmutable)
	}
	if n := r.accountAdm.count("remove-identifier"); n != 1 {
		t.Errorf("RemoveIdentifier ran %d times, want once", n)
	}
}

// TestTheAccountListQueryPairsPrefixAndKind holds the coupling rule: an identifier prefix and an identifier kind are given together or not at all.
func TestTheAccountListQueryPairsPrefixAndKind(t *testing.T) {
	r := newRig(t)
	for _, q := range []string{"?identifier_prefix=abc", "?identifier_kind=email"} {
		if rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts"+q, nil, fresh()...); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
	if rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts?identifier_prefix=abc&identifier_kind=email", nil, fresh()...); rec.Code != http.StatusOK {
		t.Errorf("a paired prefix and kind: status = %d, want 200", rec.Code)
	}
	if rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts?identifier_prefix=abc&identifier_kind=bogus", nil, fresh()...); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown kind: status = %d, want 400", rec.Code)
	}
}

// TestTheAccountListParsesTheDisplayNamePrefix checks that the display-name prefix reaches the account surface.
func TestTheAccountListParsesTheDisplayNamePrefix(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts?display_name_prefix=Ali&status=active&kind=user", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	q := r.accounts.lastList()
	if q.DisplayNamePrefix != "Ali" {
		t.Errorf("display name prefix = %q, want the one given", q.DisplayNamePrefix)
	}
	if q.Kind != authn.KindUser || q.Status == nil || *q.Status != authn.StatusActive {
		t.Errorf("the other filters read kind=%q status=%v, want user and active", q.Kind, q.Status)
	}
}

// TestTheAccountListRefusesANonUTF8DisplayNamePrefix checks that a display-name prefix that is not valid UTF-8 is a 400 before the read.
func TestTheAccountListRefusesANonUTF8DisplayNamePrefix(t *testing.T) {
	r := newRig(t)
	for _, q := range []string{"?display_name_prefix=%BF", "?display_name_prefix=caf%C3", "?display_name_prefix=%FF%FE"} {
		rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts"+q, nil, fresh()...)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
	if r.accounts.listCalls != 0 {
		t.Errorf("the account list read ran %d time(s) on a non-UTF-8 prefix, want no read", r.accounts.listCalls)
	}

	rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts?display_name_prefix=Ali", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("a valid prefix: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if q := r.accounts.lastList(); q.DisplayNamePrefix != "Ali" {
		t.Errorf("display name prefix = %q, want the one given", q.DisplayNamePrefix)
	}
}

// TestTheAccountListRefusesAQueryParameterItDoesNotAccept checks that a parameter the list does not declare is a 400.
func TestTheAccountListRefusesAQueryParameterItDoesNotAccept(t *testing.T) {
	r := newRig(t)
	for _, q := range []string{"?nope=1", "?displayname_prefix=Ali", "?statuss=active", "?user=alice"} {
		rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts"+q, nil, fresh()...)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
	if r.accounts.listCalls != 0 {
		t.Errorf("the account list read ran %d time(s) on a refused query, want no read", r.accounts.listCalls)
	}
}

// TestTheAccountListReadsAnEmptyFilterAsNoFilter checks that an accepted filter given empty narrows nothing.
func TestTheAccountListReadsAnEmptyFilterAsNoFilter(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts?display_name_prefix=&status=&after=", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	q := r.accounts.lastList()
	if q.DisplayNamePrefix != "" || q.Status != nil || q.After != "" {
		t.Errorf("an empty filter read as prefix=%q status=%v after=%q, want no filter", q.DisplayNamePrefix, q.Status, q.After)
	}
	if r.accounts.listCalls != 1 {
		t.Errorf("the read ran %d time(s), want once", r.accounts.listCalls)
	}
}

// TestPagedRoutesRefuseANegativeLimit checks that a negative limit is a 400 on every paged route and zero is accepted.
func TestPagedRoutesRefuseANegativeLimit(t *testing.T) {
	r := newRig(t)
	r.accounts.put("acct-t2", authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
	for _, tc := range []struct {
		name string
		path string
	}{
		{"account list", "/api/v1/system/auth/accounts"},
		{"member list", "/api/v1/tenants/" + tenantA + "/members"},
		{"impersonation list", "/api/v1/system/tenants/" + tenantA + "/impersonations"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sep := "?"
			if strings.Contains(tc.path, "?") {
				sep = ""
			}
			if rec := r.do(http.MethodGet, tc.path+sep+"limit=-1", nil, fresh()...); rec.Code != http.StatusBadRequest {
				t.Errorf("limit=-1: status = %d, want 400", rec.Code)
			}
			if rec := r.do(http.MethodGet, tc.path+sep+"limit=0", nil, fresh()...); rec.Code != http.StatusOK {
				t.Errorf("limit=0: status = %d, want 200", rec.Code)
			}
		})
	}
}

// TestAccountCreateRefusesAnUnknownKind holds the create's kind check.
func TestAccountCreateRefusesAnUnknownKind(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts", map[string]any{"kind": "bogus"}, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if r.accountAdm.count("create") != 0 {
		t.Error("Create ran for an unknown kind")
	}
}

// TestIdentifierAddShape holds the three shape checks of the identifier route: a non-empty value, the default way of admin, and a refusal of any other way.
func TestIdentifierAddShape(t *testing.T) {
	r := newRig(t)
	base := "/api/v1/system/auth/accounts/" + string(userID) + "/identifiers"

	if rec := r.do(http.MethodPost, base, map[string]any{"kind": "email", "value": ""}, fresh()...); rec.Code != http.StatusBadRequest {
		t.Errorf("an empty value: status = %d, want 400", rec.Code)
	}
	if r.accountAdm.count("add-identifier") != 0 {
		t.Error("AddIdentifier ran for an empty value")
	}

	if rec := r.do(http.MethodPost, base, map[string]any{"kind": "email", "value": "a@example.com"}, fresh()...); rec.Code != http.StatusNoContent {
		t.Fatalf("no verified_via: status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if r.accountAdm.addedVia != (authn.Verification{Via: authn.VerifiedViaAdmin}) {
		t.Errorf("the identifier was written as %+v, want admin", r.accountAdm.addedVia)
	}

	before := r.accountAdm.count("add-identifier")
	if rec := r.do(http.MethodPost, base, map[string]any{"kind": "email", "value": "a@example.com", "verified_via": "bogus"}, fresh()...); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown way: status = %d, want 400", rec.Code)
	}
	if r.accountAdm.count("add-identifier") != before {
		t.Error("AddIdentifier ran for an unknown way")
	}
}

// TestProviderWriteInheritsAndOverridesTheFlags checks that a partial update keeps stored flags and a create obeys an explicit one.
func TestProviderWriteInheritsAndOverridesTheFlags(t *testing.T) {
	r := newRig(t)
	r.providers.put(authn.SSOProvider{
		ID: idpA, Tenant: tenantA, Protocol: authn.SSOProtocolSAML, Name: "n", Provisioning: authn.ProvisioningJIT,
		UsernameStrategy: authn.UsernameStrategyClaim, UsernameClaim: authn.UsernameClaimPreferredUsername,
		Enabled: true, TrustIDPMFA: true, AllowIDPInitiated: true, AllowSHA1: true, MergeExisting: true,
	})
	rec := r.do(http.MethodPut, "/api/v1/tenants/"+tenantA+"/sso/providers/"+idpA,
		map[string]any{"protocol": "saml", "name": "n", "provisioning": "jit"}, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	got := r.providers.puts[len(r.providers.puts)-1]
	if !got.Enabled || !got.TrustIDPMFA || !got.AllowIDPInitiated || !got.AllowSHA1 || !got.MergeExisting {
		t.Errorf("a partial update reset the flags to %+v, want them carried", got)
	}
	if got.UsernameStrategy != authn.UsernameStrategyClaim || got.UsernameClaim != authn.UsernameClaimPreferredUsername {
		t.Errorf("a partial update reset the username strategy to %q/%q, want it carried",
			got.UsernameStrategy, got.UsernameClaim)
	}

	created := newRig(t)
	rec = created.do(http.MethodPost, "/api/v1/tenants/"+tenantA+"/sso/providers",
		map[string]any{
			"protocol": "saml", "name": "n", "provisioning": "jit", "enabled": false,
			"trust_idp_mfa": true, "username_strategy": "user-confirm",
		}, fresh()...)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201; body %s", rec.Code, rec.Body.String())
	}
	got = created.providers.puts[len(created.providers.puts)-1]
	if got.Enabled {
		t.Error("an explicit enabled:false was ignored")
	}
	if !got.TrustIDPMFA {
		t.Error("an explicit trust_idp_mfa:true was ignored")
	}
	if got.UsernameStrategy != authn.UsernameStrategyUserConfirm || got.UsernameClaim != "" {
		t.Errorf("the create wrote %q/%q, want user-confirm with no claim", got.UsernameStrategy, got.UsernameClaim)
	}
}

// TestProviderCreateRequiresItsFields checks that a create missing protocol, name or provisioning is refused before the capability.
func TestProviderCreateRequiresItsFields(t *testing.T) {
	full := map[string]any{"protocol": "saml", "name": "n", "provisioning": "jit"}
	for _, drop := range []string{"protocol", "name", "provisioning"} {
		t.Run("no "+drop, func(t *testing.T) {
			r := newRig(t)
			body := map[string]any{}
			for k, v := range full {
				body[k] = v
			}
			body[drop] = ""
			rec := r.do(http.MethodPost, "/api/v1/tenants/"+tenantA+"/sso/providers", body, fresh()...)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
			if len(r.providers.puts) != 0 {
				t.Error("Put ran though a required field was missing")
			}
		})
	}
}

// TestTheMetadataRefreshRouteDispatches holds that the route dispatches to the refresher of the provider's protocol and refuses a protocol with none.
func TestTheMetadataRefreshRouteDispatches(t *testing.T) {
	r := newRig(t)
	r.providers.put(authn.SSOProvider{ID: idpA, Tenant: tenantA, Protocol: authn.SSOProtocolSAML, Provisioning: authn.ProvisioningJIT})
	rec := r.do(http.MethodPost, "/api/v1/tenants/"+tenantA+"/sso/providers/"+idpA+"/metadata/refresh", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("SAML refresh: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if r.refresher.calls != 1 {
		t.Errorf("the refresher ran %d times, want once", r.refresher.calls)
	}

	r.providers.put(authn.SSOProvider{ID: "oidc-1", Tenant: tenantA, Protocol: authn.SSOProtocolOIDC, Provisioning: authn.ProvisioningJIT})
	rec = r.do(http.MethodPost, "/api/v1/tenants/"+tenantA+"/sso/providers/oidc-1/metadata/refresh", nil, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("refresh with no refresher: status = %d, want 400", rec.Code)
	}
}

// TestTheDomainRoutesRefuseAMissingProvider holds that a domain route resolves the provider before the Decider and answers a missing one with a 404.
func TestTheDomainRoutesRefuseAMissingProvider(t *testing.T) {
	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/api/v1/tenants/" + tenantA + "/sso/providers/absent/domains", map[string]any{"domain": "d.example"}},
		{http.MethodDelete, "/api/v1/tenants/" + tenantA + "/sso/providers/absent/domains/d.example", nil},
		{http.MethodPost, "/api/v1/tenants/" + tenantA + "/sso/providers/absent/domains/d.example/verify", nil},
		{http.MethodPost, "/api/v1/tenants/" + tenantA + "/sso/providers/absent/reverify-domains", nil},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := newRig(t)
			rec := r.do(tc.method, tc.path, tc.body, fresh()...)
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
			if r.decider.count() != 0 {
				t.Error("the Decider was asked for a provider that is not there")
			}
		})
	}
}

// TestTheAuditRowsCarryTheContract checks the action, outcome and detail keys of the rows a representative write records.
func TestTheAuditRowsCarryTheContract(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts", map[string]any{"kind": "user", "username": "a"}, fresh()...)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201; body %s", rec.Code, rec.Body.String())
	}
	entries := r.audit.entries()
	if len(entries) != 1 {
		t.Fatalf("the audit holds %d rows, want 1", len(entries))
	}
	e := entries[0]
	if e.Action != actionAccountCreate || e.Outcome != outcomeSuccess {
		t.Errorf("the row is %q/%q, want %q/%q", e.Action, e.Outcome, actionAccountCreate, outcomeSuccess)
	}
	if e.Detail[detailActionID] == "" {
		t.Error("the success row carries no action_id")
	}
	if e.Detail[detailKind] != "user" || e.Detail[detailSource] != authn.ReasonAdmin {
		t.Errorf("the detail is %+v, want the kind and the source", e.Detail)
	}

	failed := newRig(t)
	failed.accountAdm.err = authn.ErrAccountExists
	rec = failed.do(http.MethodPost, "/api/v1/system/auth/accounts", map[string]any{"kind": "user", "username": "a"}, fresh()...)
	if rec.Code != http.StatusConflict {
		t.Fatalf("failed create: status = %d, want 409", rec.Code)
	}
	rows := failed.audit.entries()
	if len(rows) != 2 {
		t.Fatalf("the audit holds %d rows, want the permitted and the failed", len(rows))
	}
	if rows[0].Outcome != outcomeSuccess || rows[1].Outcome != outcomeFailed {
		t.Errorf("the outcomes are %q then %q, want success then failed", rows[0].Outcome, rows[1].Outcome)
	}
	if rows[1].Action != actionAccountCreate {
		t.Errorf("the failed row's action = %q, want %q", rows[1].Action, actionAccountCreate)
	}
	if rows[0].Detail[detailActionID] == "" || rows[0].Detail[detailActionID] != rows[1].Detail[detailActionID] {
		t.Errorf("the rows are not linked by action_id: %q and %q",
			rows[0].Detail[detailActionID], rows[1].Detail[detailActionID])
	}

	mem := newRig(t)
	mem.do(http.MethodPut, "/api/v1/tenants/"+tenantA+"/members/"+string(userID), map[string]any{"active": true}, fresh()...)
	if !auditHas(t, mem, actionMembershipSet, outcomeSuccess) {
		t.Error("the membership write wrote no row named authn.membership.set")
	}
	if !auditNamesObject(t, mem, actionMembershipSet, audit.AccountObject(string(userID))) {
		t.Errorf("the membership row does not name the account object %q: %+v",
			audit.AccountObject(string(userID)), mem.audit.entries())
	}

	upd := newRig(t)
	upd.do(http.MethodPatch, "/api/v1/system/auth/accounts/"+string(userID),
		map[string]any{"display_name": "Ada"}, fresh()...)
	if !auditNamesObject(t, upd, actionAccountUpdate, audit.AccountObject(string(userID))) {
		t.Errorf("the account-update row does not name the account object %q: %+v",
			audit.AccountObject(string(userID)), upd.audit.entries())
	}
	sess := newRig(t)
	sess.sessions.put(authn.SessionInfo{ID: sessionA, Account: userID, Kind: authn.SessionBrowser})
	sess.do(http.MethodPost, "/api/v1/system/auth/accounts/"+string(userID)+"/revoke-sessions", nil, fresh()...)
	if !auditHas(t, sess, actionSessionRevoke, outcomeSuccess) {
		t.Error("the session revocation wrote no row named authn.session.revoke")
	}
}

// TestABulkRevocationNamesEverySessionItRevokes checks that a bulk revocation writes one row per session, linked by one action id.
func TestABulkRevocationNamesEverySessionItRevokes(t *testing.T) {
	const (
		revokeAction = "authn.session.revoke"
		actionIDKey  = "action_id"
		path         = "/api/v1/system/auth/accounts/" + string(userID) + "/revoke-sessions"
	)
	second := authn.SessionID("0190a6f2-3b6c-7c2e-8a4d-5e6f7a8b9c0e")
	seed := func(r *rig, sessions ...authn.SessionID) {
		r.accounts.put(userID, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
		for _, id := range sessions {
			r.sessions.put(authn.SessionInfo{ID: id, Account: userID, Kind: authn.SessionBrowser})
		}
	}
	want := []string{string(sessionA), string(second)}
	slices.Sort(want)
	namedWith := func(rows []audit.Entry, outcome string) []string {
		var named []string
		for _, row := range rows {
			if row.Outcome == outcome {
				named = append(named, row.Object)
			}
		}
		slices.Sort(named)
		return named
	}
	onlyAction := func(t *testing.T, rows []audit.Entry) {
		t.Helper()
		for _, row := range rows {
			if row.Action != revokeAction {
				t.Errorf("the row = %+v, want the action %s", row, revokeAction)
			}
		}
	}

	done := newRig(t)
	seed(done, sessionA, second)
	answer := done.do(http.MethodPost, path, nil, fresh()...)
	if answer.Code != http.StatusOK {
		t.Fatalf("the bulk revocation answered %d, want 200: %s", answer.Code, answer.Body.String())
	}
	if got := strings.TrimSpace(answer.Body.String()); got != `{"revoked":2}` {
		t.Errorf("the answer is %s, want the two sessions the rows name", got)
	}
	rows := done.audit.entries()
	if len(rows) != 2 {
		t.Fatalf("the audit holds %d rows, want one per session revoked: %+v", len(rows), rows)
	}
	onlyAction(t, rows)
	if named := namedWith(rows, "success"); !slices.Equal(named, want) {
		t.Errorf("the audit names %v, want the sessions revoked %v", named, want)
	}
	if got := identifiersIn(rows, actionIDKey); len(got) != 1 || got[""] {
		t.Errorf("the rows carry the action identifiers %v, want one shared identifier", got)
	}

	failed := newRig(t)
	seed(failed, sessionA, second)
	failed.sessions.revokeErr = errors.New("the revocation failed")
	if rec := failed.do(http.MethodPost, path, nil, fresh()...); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("the failed bulk revocation answered %d, want 503: %s", rec.Code, rec.Body.String())
	}
	rows = failed.audit.entries()
	if len(rows) != 4 {
		t.Fatalf("the audit holds %d rows, want the two permitted and the two failed: %+v", len(rows), rows)
	}
	onlyAction(t, rows)
	if named := namedWith(rows, "success"); !slices.Equal(named, want) {
		t.Errorf("the permitted rows name %v, want the sessions revoked %v", named, want)
	}
	if named := namedWith(rows, "failed"); !slices.Equal(named, want) {
		t.Errorf("the failed rows name %v, want the sessions the permitted rows name %v", named, want)
	}
	if got := identifiersIn(rows, actionIDKey); len(got) != 1 || got[""] {
		t.Errorf("the rows carry the action identifiers %v, want one shared identifier", got)
	}

	empty := newRig(t)
	seed(empty)
	rec := empty.do(http.MethodPost, path, nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("the bulk revocation with no revocable session answered %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"revoked":0}` {
		t.Errorf("the answer is %s, want no session revoked", got)
	}
	if rows := empty.audit.entries(); len(rows) != 0 {
		t.Errorf("the audit holds %v, want no row: no session was revoked", rows)
	}

	unreadable := newRig(t)
	seed(unreadable, sessionA, second)
	unreadable.sessions.err = errors.New("the store is unreachable")
	rec = unreadable.do(http.MethodPost, path, nil, fresh()...)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("the bulk revocation whose set could not be read answered %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if rows := unreadable.audit.entries(); len(rows) != 0 {
		t.Errorf("the audit holds %v, want no row: the action was not carried out", rows)
	}
	if unreadable.sessions.revokedAcct != 0 {
		t.Error("a revocation ran although the set it would end could not be read")
	}
}

// TestTheViewsCarryTheFields holds that each response view carries the fields it is built from, so a swapped or dropped assignment cannot pass unnoticed.
func TestTheViewsCarryTheFields(t *testing.T) {
	r := newRig(t)
	r.accounts.put(userID, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive, MFARequired: true})
	rec := r.do(http.MethodGet, "/api/v1/system/auth/accounts/"+string(userID), nil, fresh()...)
	var account struct {
		Account struct {
			ID          string `json:"id"`
			MFARequired bool   `json:"mfa_required"`
		} `json:"account"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &account); err != nil {
		t.Fatalf("decoding the account body: %v", err)
	}
	if account.Account.ID != string(userID) || !account.Account.MFARequired {
		t.Errorf("the account view is %+v, want the id and mfa_required", account.Account)
	}

	s := newRig(t)
	s.sessions.put(authn.SessionInfo{ID: sessionA, Account: userID, Kind: authn.SessionBrowser, ImpersonationID: "gr-1"})
	rec = s.do(http.MethodGet, "/api/v1/system/auth/accounts/"+string(userID)+"/sessions", nil, fresh()...)
	var sessions struct {
		Sessions []struct {
			ID              string `json:"id"`
			ImpersonationID string `json:"impersonation_id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("decoding the sessions body: %v", err)
	}
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].ID != string(sessionA) || sessions.Sessions[0].ImpersonationID != "gr-1" {
		t.Errorf("the session view is %+v, want the id and impersonation_id", sessions.Sessions)
	}

	sec := newRig(t)
	sec.accounts.put(userID, authn.Account{Kind: authn.KindService, Status: authn.StatusActive})
	sec.accounts.secrets = map[authn.AccountID][]authn.ServiceSecretInfo{
		userID: {{ID: "sec-1", Label: "ci", RevokedAt: time.Now()}},
	}
	rec = sec.do(http.MethodGet, "/api/v1/system/auth/accounts/"+string(userID)+"/secrets", nil, fresh()...)
	var secrets struct {
		Secrets []struct {
			ID      string `json:"id"`
			Label   string `json:"label"`
			Revoked bool   `json:"revoked"`
		} `json:"secrets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &secrets); err != nil {
		t.Fatalf("decoding the secrets body: %v", err)
	}
	if len(secrets.Secrets) != 1 || secrets.Secrets[0].ID != "sec-1" || secrets.Secrets[0].Label != "ci" || !secrets.Secrets[0].Revoked {
		t.Errorf("the secret view is %+v, want the id, label and revoked", secrets.Secrets)
	}

	p := newRig(t)
	p.providers.put(authn.SSOProvider{ID: idpA, Tenant: tenantA, Protocol: authn.SSOProtocolSAML, Provisioning: authn.ProvisioningJIT, Version: 5})
	rec = p.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/sso/providers/"+idpA, nil, fresh()...)
	var provider struct {
		Provider struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &provider); err != nil {
		t.Fatalf("decoding the provider body: %v", err)
	}
	if provider.Provider.ID != idpA || provider.Provider.Version != 5 {
		t.Errorf("the provider view is %+v, want the id and version", provider.Provider)
	}

	imp := newRig(t)
	ended := time.Now().UTC().Truncate(time.Second)
	imp.records.put(authn.ImpersonationGrant{
		ID: grantA, Actor: adminID, Target: "acct-t2", Tenant: tenantB,
		StartedAt: ended.Add(-time.Hour), EndsAt: ended.Add(time.Hour), EndedAt: ended, EndReason: authn.EndExplicit,
	})
	imp.records.put(authn.ImpersonationGrant{
		ID: "live", Actor: adminID, Target: "acct-t2", Tenant: tenantB,
		StartedAt: ended.Add(-time.Hour), EndsAt: ended.Add(time.Hour),
	})
	rec = imp.do(http.MethodGet, "/api/v1/system/tenants/"+tenantB+"/impersonations", nil, fresh()...)
	var grants struct {
		Impersonations []struct {
			ID        string `json:"id"`
			Actor     string `json:"actor"`
			Target    string `json:"target"`
			EndedAt   string `json:"ended_at"`
			EndReason string `json:"end_reason"`
		} `json:"impersonations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &grants); err != nil {
		t.Fatalf("decoding the impersonations body: %v", err)
	}
	if len(grants.Impersonations) != 2 {
		t.Fatalf("the list holds %d records, want 2", len(grants.Impersonations))
	}
	first := grants.Impersonations[0]
	if first.ID != grantA || first.Actor != string(adminID) || first.Target != "acct-t2" || first.EndReason != authn.EndExplicit {
		t.Errorf("the ended view is %+v, want the actor, target and reason", first)
	}
	if got := parseTime(t, first.EndedAt); !got.Equal(ended) {
		t.Errorf("the ended view's ended_at = %v, want %v", got, ended)
	}
	if live := grants.Impersonations[1]; live.ID != "live" {
		t.Fatalf("the second record is %q, want live", live.ID)
	}
}

func parseTime(t *testing.T, s string) time.Time {
	t.Helper()
	got, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return got
}

// TestRecentWindowBoundsDefaultAndReport checks the freshness window's bounds, default and one-time reporting.
func TestRecentWindowBoundsDefaultAndReport(t *testing.T) {
	logs := &logBuffer{}
	store := newStore()
	reader := newReader(store, logs.logger())

	store.values[recentWindowPath] = 2 * time.Hour
	if got := reader.recentWindow(); got != defaultRecentWindow {
		t.Errorf("an out-of-domain value read as %v, want the default %v", got, defaultRecentWindow)
	}
	if got := reader.recentWindow(); got != defaultRecentWindow {
		t.Errorf("the second read returned %v, want the default %v", got, defaultRecentWindow)
	}
	if n := strings.Count(logs.String(), recentWindowPath); n != 1 {
		t.Errorf("the setting was reported %d times, want once", n)
	}

	store.values[recentWindowPath] = 3 * time.Hour
	reader.recentWindow()
	if n := strings.Count(logs.String(), recentWindowPath); n != 2 {
		t.Errorf("a changed value was reported %d times in all, want two", n)
	}

	store.values[recentWindowPath] = 5 * time.Minute
	if got := reader.recentWindow(); got != 5*time.Minute {
		t.Errorf("a value within the domain read as %v, want 5m", got)
	}

	absent := newReader(newStore(), logs.logger())
	if got := absent.recentWindow(); got != defaultRecentWindow {
		t.Errorf("an unset store read as %v, want the default %v", got, defaultRecentWindow)
	}
}

// TestTheDeciderIsAskedBeforeTheStrengthCheck checks that a Decider refusal is answered before the strength check.
func TestTheDeciderIsAskedBeforeTheStrengthCheck(t *testing.T) {
	r := newRig(t)
	r.decider.allow = false
	stale := append(fresh(), authntest.WithAuthTime(time.Now().Add(-time.Hour)))
	rec := r.do(http.MethodDelete, "/api/v1/system/auth/accounts/"+string(userID), nil, stale...)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403: the Decider is asked before the strength check", rec.Code)
	}
	if n := r.decider.count(); n != 1 {
		t.Errorf("the Decider was asked %d times, want once", n)
	}
}

// TestTheMetadataRefreshRouteIsMountedWithARefresher holds that the metadata-refresh sub-route exists when a refresher is present.
func TestTheMetadataRefreshRouteIsMountedWithARefresher(t *testing.T) {
	r := newRig(t)
	if _, ok := r.mounter.Registered(http.MethodPost, "/api/v1/tenants/:tenant/sso/providers/:id/metadata/refresh"); !ok {
		t.Error("the metadata-refresh route is not mounted though a refresher is present")
	}
}

// TestMemberPutDefaultsTheSource holds that a membership written without a source is stored as an administrator's.
func TestMemberPutDefaultsTheSource(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPut, "/api/v1/tenants/"+tenantA+"/members/"+string(userID), map[string]any{"active": true}, fresh()...)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	puts := r.members.puts
	if len(puts) == 0 {
		t.Fatal("no membership was written")
	}
	if got := puts[len(puts)-1].Source; got != authn.ReasonAdmin {
		t.Errorf("the stored source = %q, want %q", got, authn.ReasonAdmin)
	}
}

// TestPasswordResetWithoutASessionStore checks that a password reset with no session store succeeds, revokes nothing and logs it.
func TestPasswordResetWithoutASessionStore(t *testing.T) {
	r := newRig(t, noSessions)
	rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts/"+string(userID)+"/password",
		map[string]any{"password": "a-long-temporary-one"}, fresh()...)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if r.passwords.calls != 1 {
		t.Errorf("Set ran %d times, want once", r.passwords.calls)
	}
	if !strings.Contains(r.logs.String(), "no session store") {
		t.Error("the module did not report a password reset with no session store")
	}
}

// TestBindingRefusalsCarryTheCapabilityProblemType checks that binding refusals carry this capability's problem types.
func TestBindingRefusalsCarryTheCapabilityProblemType(t *testing.T) {
	r := newRig(t)
	cases := []struct {
		name        string
		method      string
		path        string
		contentType string
		body        string
		wantType    string
	}{
		{
			"malformed JSON",
			http.MethodPatch, "/api/v1/system/auth/accounts/" + string(userID),
			"application/json", "{", "urn:speed:authn:" + mount.CodeBadRequest,
		},
		{
			"unknown member",
			http.MethodPatch, "/api/v1/system/auth/accounts/" + string(userID),
			"application/json", `{"smuggled":1}`, "urn:speed:authn:" + mount.CodeBadRequest,
		},
		{
			"non-integer query value",
			http.MethodGet, "/api/v1/system/auth/accounts?limit=abc",
			"", "", "urn:speed:authn:" + mount.CodeBadRequest,
		},
		{
			"wrong Content-Type",
			http.MethodPatch, "/api/v1/system/auth/accounts/" + string(userID),
			"text/plain", "{}", "urn:speed:authn:" + mount.CodeUnsupportedMediaType,
		},
		{
			"non-UTF-8 JSON charset",
			http.MethodPatch, "/api/v1/system/auth/accounts/" + string(userID),
			"application/json; charset=latin1", "{}", "urn:speed:authn:" + mount.CodeUnsupportedMediaType,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r.mounter.As(fresh()...)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			rec := r.mounter.Do(req)
			if got := problemType(t, rec.Body.String()); got != tc.wantType {
				t.Errorf("problem type = %q, want %q (status %d)", got, tc.wantType, rec.Code)
			}
		})
	}
}

// TestARequiredBodyMustBePresent checks that an empty body on a route that requires one is a 400 before the handler.
func TestARequiredBodyMustBePresent(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		called func(*rig) int
	}{
		{
			"account patch",
			http.MethodPatch, "/api/v1/system/auth/accounts/" + string(userID),
			func(r *rig) int { return r.accountAdm.count("set-display-name") },
		},
		{
			"mfa flag",
			http.MethodPost, "/api/v1/system/auth/accounts/" + string(userID) + "/mfa-required",
			func(r *rig) int { return r.accountAdm.count("set-mfa-required") },
		},
		{
			"membership write",
			http.MethodPut, "/api/v1/tenants/" + tenantA + "/members/" + string(userID),
			func(r *rig) int { return len(r.members.puts) },
		},
		{
			"membership exemption",
			http.MethodPut, "/api/v1/tenants/" + tenantA + "/members/" + string(userID) + "/sso-exempt",
			func(r *rig) int { return len(r.members.puts) },
		},
		{
			"service secret create",
			http.MethodPost, "/api/v1/system/auth/accounts/" + string(userID) + "/secrets",
			func(r *rig) int { return r.accountAdm.count("create-secret") },
		},
		{
			"provider update",
			http.MethodPut, "/api/v1/tenants/" + tenantA + "/sso/providers/" + idpA,
			func(r *rig) int { return len(r.providers.puts) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.mounter.As(fresh()...)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(""))
			req.Header.Set("Content-Type", "application/json")
			rec := r.mounter.Do(req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("an empty required body answered %d, want 400; body %s", rec.Code, rec.Body.String())
			}
			if got := problemType(t, rec.Body.String()); got != "urn:speed:authn:"+mount.CodeBadRequest {
				t.Errorf("problem type = %q, want %q", got, "urn:speed:authn:"+mount.CodeBadRequest)
			}
			if n := tc.called(r); n != 0 {
				t.Errorf("the capability was called %d times for an empty required body", n)
			}
			if n := len(r.audit.entries()); n != 0 {
				t.Errorf("%d audit rows were written for an empty required body", n)
			}
		})
	}
}

// TestEveryBodyCarryingRouteKeepsTheCapabilityBodyLimit holds the capability's 64 KiB request-body limit at every route that declares a body.
func TestEveryBodyCarryingRouteKeepsTheCapabilityBodyLimit(t *testing.T) {
	// 70 KiB is over the capability's 64 KiB limit but under the endpoint's
	// default, so the two limits are told apart.
	const overLimit = 70 << 10
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			"account create",
			http.MethodPost, "/api/v1/system/auth/accounts",
			`{"kind":"user","username":"body-limit"}`,
		},
		{
			"account patch",
			http.MethodPatch, "/api/v1/system/auth/accounts/" + string(userID),
			`{"display_name":"body-limit"}`,
		},
		{
			"account status",
			http.MethodPost, "/api/v1/system/auth/accounts/" + string(userID) + "/status",
			`{"status":"disabled"}`,
		},
		{
			"identifier add",
			http.MethodPost, "/api/v1/system/auth/accounts/" + string(userID) + "/identifiers",
			`{"kind":"email","value":"limit@example.com"}`,
		},
		{
			"password reset",
			http.MethodPost, "/api/v1/system/auth/accounts/" + string(userID) + "/password",
			`{"password":"a-long-temporary-one"}`,
		},
		{
			"mfa flag",
			http.MethodPost, "/api/v1/system/auth/accounts/" + string(userID) + "/mfa-required",
			`{"required":true}`,
		},
		{
			"service secret create",
			http.MethodPost, "/api/v1/system/auth/accounts/" + string(userID) + "/secrets",
			`{"label":"body-limit"}`,
		},
		{
			"membership write",
			http.MethodPut, "/api/v1/tenants/" + tenantA + "/members/" + string(userID),
			`{"active":true}`,
		},
		{
			"membership exemption",
			http.MethodPut, "/api/v1/tenants/" + tenantA + "/members/" + string(userID) + "/sso-exempt",
			`{"sso_exempt":true}`,
		},
		{
			"provider create",
			http.MethodPost, "/api/v1/tenants/" + tenantA + "/sso/providers",
			`{"protocol":"oidc","name":"Body limit","provisioning":"jit"}`,
		},
		{
			"provider update",
			http.MethodPut, "/api/v1/tenants/" + tenantA + "/sso/providers/" + idpA,
			`{"protocol":"oidc","name":"Body limit","provisioning":"jit"}`,
		},
		{
			"domain claim",
			http.MethodPost, "/api/v1/tenants/" + tenantA + "/sso/providers/" + idpA + "/domains",
			`{"domain":"example.com"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			body := tc.body + strings.Repeat(" ", overLimit-len(tc.body))
			if len(body) <= mount.MaxBodyBytes {
				t.Fatalf("the case body is %d bytes, not over the %d-byte limit", len(body), mount.MaxBodyBytes)
			}
			r.mounter.As(fresh()...)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := r.mounter.Do(req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("a %d-byte body answered %d, want 400; body %s", len(body), rec.Code, rec.Body.String())
			}
			if got := problemType(t, rec.Body.String()); got != "urn:speed:authn:"+mount.CodeBadRequest {
				t.Errorf("problem type = %q, want %q", got, "urn:speed:authn:"+mount.CodeBadRequest)
			}
		})
	}
}

// TestMemberRemoveMapsCapabilityFailuresToItsDeclaredAnswers holds that a failure mapping to 400, 404 or 409 gets that status rather than a 500.
func TestMemberRemoveMapsCapabilityFailuresToItsDeclaredAnswers(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid argument", authn.ErrInvalidArgument, http.StatusBadRequest, mount.CodeBadRequest},
		{"no such account", authn.ErrAccountNotFound, http.StatusNotFound, ""},
		{"conflict", authn.ErrAccountExists, http.StatusConflict, mount.CodeConflict},
		{"last tenant administrator", authn.ErrLastTenantAdmin, http.StatusConflict, mount.CodeConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.members.err = tc.err
			rec := r.do(http.MethodDelete,
				"/api/v1/tenants/"+tenantA+"/members/"+string(userID), nil, fresh()...)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.status, rec.Body.String())
			}
			if tc.code == "" {
				if rec.Body.Len() != 0 {
					t.Errorf("a bare 404 carries a body: %s", rec.Body.String())
				}
				return
			}
			if got := problemCode(t, rec.Body.String()); got != tc.code {
				t.Errorf("problem code = %q, want %q", got, tc.code)
			}
		})
	}
}

// TestAccountDeleteAnswersTheLastAdministratorWithItsOwnCode checks that the last-administrator deletion refusal has its own code and names the tenant.
func TestAccountDeleteAnswersTheLastAdministratorWithItsOwnCode(t *testing.T) {
	r := newRig(t)
	r.accountAdm.err = &authn.AccountLastTenantAdminError{Tenant: "acme"}

	rec := r.do(http.MethodDelete, "/api/v1/system/auth/accounts/"+string(userID), nil, fresh()...)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	if got := problemCode(t, rec.Body.String()); got != mount.CodeAccountLastTenantAdmin {
		t.Errorf("problem code = %q, want %q", got, mount.CodeAccountLastTenantAdmin)
	}
	var p struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decoding the problem body: %v", err)
	}
	if p.Detail != "acme" {
		t.Errorf("problem detail = %q, want the tenant acme", p.Detail)
	}
}
