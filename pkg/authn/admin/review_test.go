package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// auditHas reports whether the recorded audit holds a row with action and
// outcome.
func auditHas(t *testing.T, r *rig, action, outcome string) bool {
	t.Helper()
	for _, e := range r.audit.entries() {
		if e.Action == action && e.Outcome == outcome {
			return true
		}
	}
	return false
}

// identifiersIn collects the distinct values the rows carry under key, so a case
// can tell rows linked by one identifier from rows carrying several.
func identifiersIn(rows []audit.Entry, key string) map[string]bool {
	out := map[string]bool{}
	for _, row := range rows {
		out[row.Detail[key]] = true
	}
	return out
}

// problemCode returns the machine-readable code of a problem response, the part
// after the last colon of its type.
func problemCode(t *testing.T, body string) string {
	t.Helper()
	var p struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("decoding the problem body %q: %v", body, err)
	}
	if i := strings.LastIndex(p.Type, ":"); i >= 0 {
		return p.Type[i+1:]
	}
	return p.Type
}

// TestTheStrongRoutesNeedASeasonedAdministratorWithANonSMSSecond holds the two
// higher gates of the strong routes: a fresh, local, multi-factor
// authentication with a non-SMS second method and a seasoned factor of the
// actor's own. A successful privileged action drives both gates, and each
// weaker case is answered with the step-up 401 and the capability is not
// called.
func TestTheStrongRoutesNeedASeasonedAdministratorWithANonSMSSecond(t *testing.T) {
	// A TOTP administrator passes both strong routes.
	r := newRig(t)
	rec := r.do(http.MethodPost, "/auth/admin/accounts/"+string(userID)+"/mfa-reset", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("mfa-reset: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if r.mfa.resets != 1 {
		t.Errorf("Reset ran %d times, want once", r.mfa.resets)
	}

	r = newRig(t)
	rec = r.do(http.MethodPost, "/auth/admin/accounts/"+string(userID)+"/password",
		map[string]any{"password": "a-long-temporary-one"}, fresh()...)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("password reset: status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if r.passwords.calls != 1 {
		t.Errorf("Set ran %d times, want once", r.passwords.calls)
	}

	// A passkey is the other non-SMS second method, so a WebAuthn administrator
	// passes too.
	r = newRig(t)
	webauthn := fresh(authntest.WithMethods(authn.MethodPassword, authn.MethodWebAuthn))
	rec = r.do(http.MethodPost, "/auth/admin/accounts/"+string(userID)+"/mfa-reset", nil, webauthn...)
	if rec.Code != http.StatusOK {
		t.Fatalf("WebAuthn mfa-reset: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if r.mfa.resets != 1 {
		t.Errorf("Reset ran %d times for a WebAuthn administrator, want once", r.mfa.resets)
	}

	weaker := []struct {
		name  string
		setup func(*rig)
		opts  func() []authntest.Option
	}{
		{"no seasoned factor of the actor", func(r *rig) { r.mfa.seasoned = false }, func() []authntest.Option { return fresh() }},
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
			if rec := r.do(http.MethodPost, "/auth/admin/accounts/"+string(userID)+"/mfa-reset", nil, tc.opts()...); rec.Code != http.StatusUnauthorized {
				t.Errorf("mfa-reset: status = %d, want 401", rec.Code)
			}
			if r.mfa.resets != 0 {
				t.Error("Reset ran though the actor was not strong enough")
			}
			if rec := r.do(http.MethodPost, "/auth/admin/accounts/"+string(userID)+"/password",
				map[string]any{"password": "a-long-temporary-one"}, tc.opts()...); rec.Code != http.StatusUnauthorized {
				t.Errorf("password reset: status = %d, want 401", rec.Code)
			}
			if r.passwords.calls != 0 {
				t.Error("Set ran though the actor was not strong enough")
			}
		})
	}
}

// TestARefusalStopsEveryReadRoute holds that a list route asks the Decider
// before it calls the capability: a Decider that refuses is a 403 and the
// capability's read is never made.
func TestARefusalStopsEveryReadRoute(t *testing.T) {
	r := newRig(t)
	r.accounts.put("acct-t2", authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
	r.decider.allow = false

	cases := []struct {
		name string
		path string
		read func() int
	}{
		{"account list", "/auth/admin/accounts", func() int { return r.accounts.listCalls }},
		{"member list", "/auth/admin/tenants/" + tenantA + "/members", func() int { return r.members.listCalls }},
		{"session list", "/auth/admin/accounts/acct-t2/sessions", func() int { return r.sessions.listCalls }},
		{"factor list", "/auth/admin/accounts/acct-t2/factors", func() int { return r.mfa.factorCalls }},
		{"secret list", "/auth/admin/accounts/acct-t2/secrets", func() int { return r.accounts.secretCalls }},
		{"provider list", "/auth/admin/tenants/" + tenantA + "/sso/providers", func() int { return r.providers.listCalls }},
		{"impersonation list", "/auth/admin/impersonations?tenant=" + tenantA, func() int { return r.records.listCalls }},
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

// TestTheSSOExemptRoute holds the sso-exempt write: an existing membership is
// updated and the mandatory audit lands, and a missing membership is a 404
// before the Decider is asked.
func TestTheSSOExemptRoute(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPut, "/auth/admin/tenants/"+tenantA+"/members/"+string(userID)+"/sso-exempt",
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

	// A missing membership is a 404 and the Decider is never asked.
	missing := newRig(t)
	rec = missing.do(http.MethodPut, "/auth/admin/tenants/"+tenantA+"/members/absent/sso-exempt",
		map[string]any{"sso_exempt": true}, fresh()...)
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing membership: status = %d, want 404", rec.Code)
	}
	if missing.decider.count() != 0 {
		t.Error("the Decider was asked for a membership that is not there")
	}
}

// TestProviderWarnings holds that each setting that loosens a default surfaces
// its warning on the get and list responses, and that a provider that sets none
// carries no warning.
func TestProviderWarnings(t *testing.T) {
	r := newRig(t)
	r.providers.put(authn.SSOProvider{
		ID: idpA, Tenant: tenantA, Protocol: authn.SSOProtocolSAML, Provisioning: authn.ProvisioningJIT,
		AllowIDPInitiated: true, MergeExisting: true, AllowSHA1: true,
	})

	want := []string{"idp-initiated", "merge", "SHA-1"}

	rec := r.do(http.MethodGet, "/auth/admin/sso/providers/"+idpA, nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	assertWarnings(t, providerWarningsFrom(t, rec.Body.String()), want)

	rec = r.do(http.MethodGet, "/auth/admin/tenants/"+tenantA+"/sso/providers", nil, fresh()...)
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

	// A provider that sets none carries no warning.
	def := newRig(t)
	def.providers.put(authn.SSOProvider{ID: idpA, Tenant: tenantA, Protocol: authn.SSOProtocolSAML, Provisioning: authn.ProvisioningJIT})
	rec = def.do(http.MethodGet, "/auth/admin/sso/providers/"+idpA, nil, fresh()...)
	if got := providerWarningsFrom(t, rec.Body.String()); len(got) != 0 {
		t.Errorf("a default provider carries warnings %v, want none", got)
	}
}

// providerWarningsFrom reads the warnings of the provider in a get response.
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

// assertWarnings holds that each fragment appears in one of the warnings.
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

// TestTheStatusRouteMapsAndRefusesDeleted holds the status name mapping and the
// refusal of a delete through this route: an account is disabled or restored,
// never deleted.
func TestTheStatusRouteMapsAndRefusesDeleted(t *testing.T) {
	r := newRig(t)
	for _, tc := range []struct {
		body string
		want authn.AccountStatus
	}{
		{"active", authn.StatusActive},
		{"disabled", authn.StatusDisabled},
	} {
		rec := r.do(http.MethodPost, "/auth/admin/accounts/"+string(userID)+"/status",
			map[string]any{"status": tc.body}, fresh()...)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %q: status = %d, want 200; body %s", tc.body, rec.Code, rec.Body.String())
		}
		if r.accountAdm.status != tc.want {
			t.Errorf("status %q: SetStatus received %v, want %v", tc.body, r.accountAdm.status, tc.want)
		}
	}

	before := r.accountAdm.count("set-status")
	rec := r.do(http.MethodPost, "/auth/admin/accounts/"+string(userID)+"/status",
		map[string]any{"status": "deleted"}, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %q: status = %d, want 400", "deleted", rec.Code)
	}
	if r.accountAdm.count("set-status") != before {
		t.Error("SetStatus ran for a delete through the status route")
	}

	rec = r.do(http.MethodPost, "/auth/admin/accounts/"+string(userID)+"/status",
		map[string]any{"status": "bogus"}, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %q: status = %d, want 400", "bogus", rec.Code)
	}
}

// TestIdentifierAddAssertsOnlyAsAnAdministrator holds the boundary of the
// identifier route: a login name or an email address an administrator asserts,
// with the way fixed to admin. A phone number and a proof of ownership are
// refused and the capability is not called.
func TestIdentifierAddAssertsOnlyAsAnAdministrator(t *testing.T) {
	r := newRig(t)
	base := "/auth/admin/accounts/" + string(userID) + "/identifiers"

	// A phone number changes only through an SMS code.
	rec := r.do(http.MethodPost, base, map[string]any{"kind": "phone", "value": "+8613800000000"}, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a phone identifier: status = %d, want 400", rec.Code)
	}
	// A proof of ownership is not the administrator's to state.
	rec = r.do(http.MethodPost, base, map[string]any{"kind": "email", "value": "a@example.com", "verified_via": "sms-code"}, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a claimed sms-code: status = %d, want 400", rec.Code)
	}
	if r.accountAdm.count("add-identifier") != 0 {
		t.Error("AddIdentifier ran for a refused identifier")
	}

	// The administrator's assertion itself is accepted, written as admin.
	rec = r.do(http.MethodPost, base, map[string]any{"kind": "email", "value": "a@example.com", "verified_via": "admin"}, fresh()...)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("an admin-asserted email: status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if r.accountAdm.addedVia.Via != authn.VerifiedViaAdmin {
		t.Errorf("the identifier was written as %q, want admin", r.accountAdm.addedVia.Via)
	}
}

// TestAccountCreateRefusesAProofVia holds the same boundary on the create
// route: an administrator asserts an identifier or records an import, and may
// not state a proof of ownership.
func TestAccountCreateRefusesAProofVia(t *testing.T) {
	r := newRig(t)
	for _, body := range []map[string]any{
		{"kind": "user", "username": "a", "email": "a@example.com", "email_verified_via": "sms-code"},
		{"kind": "user", "username": "a", "phone": "+8613800000000", "phone_verified_via": "idp:some-idp"},
	} {
		rec := r.do(http.MethodPost, "/auth/admin/accounts", body, fresh()...)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%+v: status = %d, want 400", body, rec.Code)
		}
	}
	if r.accountAdm.count("create") != 0 {
		t.Error("Create ran though a proof of ownership was claimed")
	}

	// An administrator's assertion is accepted.
	rec := r.do(http.MethodPost, "/auth/admin/accounts",
		map[string]any{"kind": "user", "username": "a", "email": "a@example.com", "email_verified_via": "admin"}, fresh()...)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rec.Code, rec.Body.String())
	}
	if r.accountAdm.created.EmailVerifiedVia.Via != authn.VerifiedViaAdmin {
		t.Errorf("the email was written as %q, want admin", r.accountAdm.created.EmailVerifiedVia.Via)
	}
}

// TestTheAnswerMapsEverySentinel holds the sentinel-to-answer mapping: each
// sentinel a capability returns gets its own status and code, and one no case
// names is a 503, so a class dropped from its case cannot pass unnoticed.
func TestTheAnswerMapsEverySentinel(t *testing.T) {
	// A read of an object that is not there is a 404.
	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"account", http.MethodGet, "/auth/admin/accounts/absent"},
		{"session", http.MethodDelete, "/auth/admin/sessions/0190a6f2-3b6c-7c2e-8a4d-5e6f7a8b9c99"},
		{"provider", http.MethodGet, "/auth/admin/sso/providers/absent"},
	} {
		t.Run("404 "+tc.name, func(t *testing.T) {
			r := newRig(t)
			if rec := r.do(tc.method, tc.path, nil, fresh()...); rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
		})
	}

	// A failure a write returns, driven through the create route.
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid argument", authn.ErrInvalidArgument, http.StatusBadRequest, mount.CodeBadRequest},
		{"invalid identifier", authn.ErrInvalidIdentifier, http.StatusBadRequest, mount.CodeBadRequest},
		{"password policy", authn.ErrPasswordPolicy, http.StatusBadRequest, mount.CodeBadRequest},
		{"invalid config", authn.ErrInvalidConfig, http.StatusBadRequest, mount.CodeBadRequest},
		{"account exists", authn.ErrAccountExists, http.StatusConflict, mount.CodeConflict},
		{"binding conflict", authn.ErrBindingConflict, http.StatusConflict, mount.CodeConflict},
		{"domain claimed", authn.ErrDomainClaimed, http.StatusConflict, mount.CodeConflict},
		{"last login method", authn.ErrLastLoginMethod, http.StatusConflict, mount.CodeConflict},
		{"factor limit", authn.ErrFactorLimit, http.StatusConflict, mount.CodeConflict},
		{"last factor", authn.ErrLastFactor, http.StatusConflict, mount.CodeConflict},
		{"unsupported region", authn.ErrUnsupportedRegion, http.StatusBadRequest, mount.CodeUnsupportedRegion},
		{"no sentinel", errors.New("an unnamed failure"), http.StatusServiceUnavailable, mount.CodeUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.accountAdm.err = tc.err
			rec := r.do(http.MethodPost, "/auth/admin/accounts",
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

// TestTheAccountListQueryPairsPrefixAndKind holds the coupling rule: an
// identifier prefix and an identifier kind are given together or not at all.
func TestTheAccountListQueryPairsPrefixAndKind(t *testing.T) {
	r := newRig(t)
	for _, q := range []string{"?identifier_prefix=abc", "?identifier_kind=email"} {
		if rec := r.do(http.MethodGet, "/auth/admin/accounts"+q, nil, fresh()...); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
	if rec := r.do(http.MethodGet, "/auth/admin/accounts?identifier_prefix=abc&identifier_kind=email", nil, fresh()...); rec.Code != http.StatusOK {
		t.Errorf("a paired prefix and kind: status = %d, want 200", rec.Code)
	}
	if rec := r.do(http.MethodGet, "/auth/admin/accounts?identifier_prefix=abc&identifier_kind=bogus", nil, fresh()...); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown kind: status = %d, want 400", rec.Code)
	}
}

// TestTheAccountListParsesTheDisplayNamePrefix holds that the display-name
// prefix reaches the account surface as written, beside the identifier prefix,
// status and kind the list already narrows by.
func TestTheAccountListParsesTheDisplayNamePrefix(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodGet, "/auth/admin/accounts?display_name_prefix=Ali&status=active&kind=user", nil, fresh()...)
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

// TestTheAccountListRefusesANonUTF8DisplayNamePrefix holds that the display-name
// prefix is text: a byte sequence that is not valid UTF-8 is a 400 and the read
// is never made, rather than a filter the read hands to the database, which
// rejects it and answers an outage instead of the shape error the caller caused.
func TestTheAccountListRefusesANonUTF8DisplayNamePrefix(t *testing.T) {
	r := newRig(t)
	for _, q := range []string{"?display_name_prefix=%BF", "?display_name_prefix=caf%C3", "?display_name_prefix=%FF%FE"} {
		rec := r.do(http.MethodGet, "/auth/admin/accounts"+q, nil, fresh()...)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
	if r.accounts.listCalls != 0 {
		t.Errorf("the account list read ran %d time(s) on a non-UTF-8 prefix, want no read", r.accounts.listCalls)
	}

	// A prefix that is valid UTF-8 text reaches the read as written.
	rec := r.do(http.MethodGet, "/auth/admin/accounts?display_name_prefix=Ali", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("a valid prefix: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if q := r.accounts.lastList(); q.DisplayNamePrefix != "Ali" {
		t.Errorf("display name prefix = %q, want the one given", q.DisplayNamePrefix)
	}
}

// TestTheAccountListRefusesAQueryParameterItDoesNotAccept holds the list's
// closed parameter set: a parameter the route does not know is a 400, so a
// misspelled filter is reported rather than swallowed by a read that ignores it
// and answers an unfiltered page.
func TestTheAccountListRefusesAQueryParameterItDoesNotAccept(t *testing.T) {
	r := newRig(t)
	for _, q := range []string{"?nope=1", "?displayname_prefix=Ali", "?statuss=active", "?user=alice"} {
		rec := r.do(http.MethodGet, "/auth/admin/accounts"+q, nil, fresh()...)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
	if r.accounts.listCalls != 0 {
		t.Errorf("the account list read ran %d time(s) on a refused query, want no read", r.accounts.listCalls)
	}
}

// TestTheAccountListReadsAnEmptyFilterAsNoFilter holds that an accepted filter
// given empty — including an empty display_name_prefix — is no filter: the read
// still runs, with nothing narrowed by it.
func TestTheAccountListReadsAnEmptyFilterAsNoFilter(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodGet, "/auth/admin/accounts?display_name_prefix=&status=&after=", nil, fresh()...)
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

// TestPagedRoutesRefuseANegativeLimit holds that a negative limit is a 400 on
// every paged route and that zero is accepted: zero asks for no explicit bound,
// which the capability reads as its default page size.
func TestPagedRoutesRefuseANegativeLimit(t *testing.T) {
	r := newRig(t)
	r.accounts.put("acct-t2", authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
	for _, tc := range []struct {
		name string
		path string
	}{
		{"account list", "/auth/admin/accounts"},
		{"member list", "/auth/admin/tenants/" + tenantA + "/members"},
		{"impersonation list", "/auth/admin/impersonations?tenant=" + tenantA + "&"},
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
	rec := r.do(http.MethodPost, "/auth/admin/accounts", map[string]any{"kind": "bogus"}, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if r.accountAdm.count("create") != 0 {
		t.Error("Create ran for an unknown kind")
	}
}

// TestIdentifierAddShape holds the three shape checks of the identifier route:
// a non-empty value, the default way of admin, and a refusal of any other way.
func TestIdentifierAddShape(t *testing.T) {
	r := newRig(t)
	base := "/auth/admin/accounts/" + string(userID) + "/identifiers"

	if rec := r.do(http.MethodPost, base, map[string]any{"kind": "email", "value": ""}, fresh()...); rec.Code != http.StatusBadRequest {
		t.Errorf("an empty value: status = %d, want 400", rec.Code)
	}
	if r.accountAdm.count("add-identifier") != 0 {
		t.Error("AddIdentifier ran for an empty value")
	}

	// A body without a way defaults to admin.
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

// TestProviderWriteInheritsAndOverridesTheFlags holds both directions of the
// boolean handling: a partial update keeps the stored flags, and an explicit
// flag on a create is obeyed.
func TestProviderWriteInheritsAndOverridesTheFlags(t *testing.T) {
	r := newRig(t)
	r.providers.put(authn.SSOProvider{
		ID: idpA, Tenant: tenantA, Protocol: authn.SSOProtocolSAML, Name: "n", Provisioning: authn.ProvisioningJIT,
		Enabled: true, TrustIDPMFA: true, AllowIDPInitiated: true, AllowSHA1: true, MergeExisting: true,
	})
	rec := r.do(http.MethodPut, "/auth/admin/sso/providers/"+idpA,
		map[string]any{"protocol": "saml", "name": "n", "provisioning": "jit"}, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	got := r.providers.puts[len(r.providers.puts)-1]
	if !got.Enabled || !got.TrustIDPMFA || !got.AllowIDPInitiated || !got.AllowSHA1 || !got.MergeExisting {
		t.Errorf("a partial update reset the flags to %+v, want them carried", got)
	}

	// An explicit enabled:false on a create is obeyed.
	created := newRig(t)
	rec = created.do(http.MethodPost, "/auth/admin/tenants/"+tenantA+"/sso/providers",
		map[string]any{"protocol": "saml", "name": "n", "provisioning": "jit", "enabled": false, "trust_idp_mfa": true}, fresh()...)
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
}

// TestProviderCreateRequiresItsFields holds that a create missing any of the
// protocol, the name and the provisioning is refused and the capability is not
// called.
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
			rec := r.do(http.MethodPost, "/auth/admin/tenants/"+tenantA+"/sso/providers", body, fresh()...)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
			if len(r.providers.puts) != 0 {
				t.Error("Put ran though a required field was missing")
			}
		})
	}
}

// TestTheMetadataRefreshRouteDispatches holds that the route dispatches to the
// refresher of the provider's protocol and refuses a protocol with none.
func TestTheMetadataRefreshRouteDispatches(t *testing.T) {
	r := newRig(t)
	r.providers.put(authn.SSOProvider{ID: idpA, Tenant: tenantA, Protocol: authn.SSOProtocolSAML, Provisioning: authn.ProvisioningJIT})
	rec := r.do(http.MethodPost, "/auth/admin/sso/providers/"+idpA+"/metadata/refresh", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("SAML refresh: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if r.refresher.calls != 1 {
		t.Errorf("the refresher ran %d times, want once", r.refresher.calls)
	}

	// A protocol with no refresher in this assembly is a 400.
	r.providers.put(authn.SSOProvider{ID: "oidc-1", Tenant: tenantA, Protocol: authn.SSOProtocolOIDC, Provisioning: authn.ProvisioningJIT})
	rec = r.do(http.MethodPost, "/auth/admin/sso/providers/oidc-1/metadata/refresh", nil, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("refresh with no refresher: status = %d, want 400", rec.Code)
	}
}

// TestTheDomainRoutesRefuseAMissingProvider holds that a domain route resolves
// the provider before the Decider and answers a missing one with a 404.
func TestTheDomainRoutesRefuseAMissingProvider(t *testing.T) {
	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/auth/admin/sso/providers/absent/domains", map[string]any{"domain": "d.example"}},
		{http.MethodDelete, "/auth/admin/sso/providers/absent/domains/d.example", nil},
		{http.MethodPost, "/auth/admin/sso/providers/absent/domains/d.example/verify", nil},
		{http.MethodPost, "/auth/admin/sso/providers/absent/reverify-domains", nil},
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

// TestTheAuditRowsCarryTheContract holds the audit content this module writes:
// the action name, the outcome and the detail keys of a representative write,
// and the failed row linked to the permitted one by the action identifier.
func TestTheAuditRowsCarryTheContract(t *testing.T) {
	// A successful create writes one success row with the action, the object and
	// the detail the contract names.
	r := newRig(t)
	rec := r.do(http.MethodPost, "/auth/admin/accounts", map[string]any{"kind": "user", "username": "a"}, fresh()...)
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

	// A write that fails is closed with a failed row linked by the action id.
	failed := newRig(t)
	failed.accountAdm.err = authn.ErrAccountExists
	rec = failed.do(http.MethodPost, "/auth/admin/accounts", map[string]any{"kind": "user", "username": "a"}, fresh()...)
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

	// The membership write and the session revocation carry their names.
	mem := newRig(t)
	mem.do(http.MethodPut, "/auth/admin/tenants/"+tenantA+"/members/"+string(userID), map[string]any{"active": true}, fresh()...)
	if !auditHas(t, mem, actionMembershipSet, outcomeSuccess) {
		t.Error("the membership write wrote no row named authn.membership.set")
	}
	sess := newRig(t)
	sess.sessions.put(authn.SessionInfo{ID: sessionA, Account: userID, Kind: authn.SessionBrowser})
	sess.do(http.MethodPost, "/auth/admin/accounts/"+string(userID)+"/revoke-sessions", nil, fresh()...)
	if !auditHas(t, sess, actionSessionRevoke, outcomeSuccess) {
		t.Error("the session revocation wrote no row named authn.session.revoke")
	}
}

// TestABulkRevocationNamesEverySessionItRevokes holds that the audit row of a
// revocation names the session it revoked, so revoking an account's sessions
// writes one mandatory row per session, read before the action — and, when the
// action fails, one failed row per session, with every row of the action linked
// by the one action identifier. With nothing to name, nothing is revoked and the
// answer says so.
//
// The action, the outcomes and the detail key are asserted as the literals the
// capability's closed tables state, so the case does not move with a production
// constant.
func TestABulkRevocationNamesEverySessionItRevokes(t *testing.T) {
	const (
		revokeAction = "authn.session.revoke"
		actionIDKey  = "action_id"
		path         = "/auth/admin/accounts/" + string(userID) + "/revoke-sessions"
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
	// namedWith is the sessions the rows of one outcome name, sorted. Every row
	// is held to the one action by the caller.
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

	// The action goes through: one permitted row per session, all of them
	// carrying the one action identifier.
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

	// The action fails: the permitted rows are closed by failed rows under the
	// same identifier, one per session, naming the same sessions.
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

	// No revocable session: nothing is named, nothing is revoked, and the answer
	// says so rather than reporting a revocation no row accounts for.
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

	// The set cannot be read: no row could name what the action would end, so
	// the action is not carried out and the answer is the outage.
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

// TestTheViewsCarryTheFields holds that each response view carries the fields it
// is built from, so a swapped or dropped assignment cannot pass unnoticed.
func TestTheViewsCarryTheFields(t *testing.T) {
	// The account view carries mfa_required.
	r := newRig(t)
	r.accounts.put(userID, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive, MFARequired: true})
	rec := r.do(http.MethodGet, "/auth/admin/accounts/"+string(userID), nil, fresh()...)
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

	// The session view carries impersonation_id, id and the ended flag.
	s := newRig(t)
	s.sessions.put(authn.SessionInfo{ID: sessionA, Account: userID, Kind: authn.SessionBrowser, ImpersonationID: "gr-1"})
	rec = s.do(http.MethodGet, "/auth/admin/accounts/"+string(userID)+"/sessions", nil, fresh()...)
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

	// The secret view carries the id and the revoked flag.
	sec := newRig(t)
	sec.accounts.put(userID, authn.Account{Kind: authn.KindService, Status: authn.StatusActive})
	sec.accounts.secrets = map[authn.AccountID][]authn.ServiceSecretInfo{
		userID: {{ID: "sec-1", Label: "ci", RevokedAt: time.Now()}},
	}
	rec = sec.do(http.MethodGet, "/auth/admin/accounts/"+string(userID)+"/secrets", nil, fresh()...)
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

	// The provider view carries the version.
	p := newRig(t)
	p.providers.put(authn.SSOProvider{ID: idpA, Tenant: tenantA, Protocol: authn.SSOProtocolSAML, Provisioning: authn.ProvisioningJIT, Version: 5})
	rec = p.do(http.MethodGet, "/auth/admin/sso/providers/"+idpA, nil, fresh()...)
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

	// The impersonation view carries the actor, the target and the end.
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
	rec = imp.do(http.MethodGet, "/auth/admin/impersonations?tenant="+tenantB, nil, fresh()...)
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

// parseTime reads an RFC 3339 timestamp the views write.
func parseTime(t *testing.T, s string) time.Time {
	t.Helper()
	got, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return got
}

// TestRecentWindowBoundsDefaultAndReport holds the freshness window's domain and
// default: a value outside its domain reads as the declared default and is
// reported once, a change is reported again, a value within the domain is used,
// and a store with nothing set reads as the default.
func TestRecentWindowBoundsDefaultAndReport(t *testing.T) {
	logs := &logBuffer{}
	store := newStore()
	reader := newReader(store, logs.logger())

	// A value above the upper bound reads as the default and is reported once,
	// however many times it is read.
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

	// A change of the offending value is reported again.
	store.values[recentWindowPath] = 3 * time.Hour
	reader.recentWindow()
	if n := strings.Count(logs.String(), recentWindowPath); n != 2 {
		t.Errorf("a changed value was reported %d times in all, want two", n)
	}

	// A value within the domain is used.
	store.values[recentWindowPath] = 5 * time.Minute
	if got := reader.recentWindow(); got != 5*time.Minute {
		t.Errorf("a value within the domain read as %v, want 5m", got)
	}

	// A store with nothing set reads as the default.
	absent := newReader(newStore(), logs.logger())
	if got := absent.recentWindow(); got != defaultRecentWindow {
		t.Errorf("an unset store read as %v, want the default %v", got, defaultRecentWindow)
	}
}

// TestTheDeciderIsAskedBeforeTheStrengthCheck holds the pipeline order: the
// Decider is asked before the actor's strength is judged, so an unauthorized
// actor is answered 403 and never learns whether their authentication would
// have sufficed.
func TestTheDeciderIsAskedBeforeTheStrengthCheck(t *testing.T) {
	r := newRig(t)
	r.decider.allow = false
	stale := append(fresh(), authntest.WithAuthTime(time.Now().Add(-time.Hour)))
	rec := r.do(http.MethodDelete, "/auth/admin/accounts/"+string(userID), nil, stale...)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403: the Decider is asked before the strength check", rec.Code)
	}
	if n := r.decider.count(); n != 1 {
		t.Errorf("the Decider was asked %d times, want once", n)
	}
}

// TestTheMetadataRefreshRouteIsMountedWithARefresher holds that the
// metadata-refresh sub-route exists when a refresher is present.
func TestTheMetadataRefreshRouteIsMountedWithARefresher(t *testing.T) {
	r := newRig(t)
	if _, ok := r.mounter.Registered(http.MethodPost, "/auth/admin/sso/providers/:id/metadata/refresh"); !ok {
		t.Error("the metadata-refresh route is not mounted though a refresher is present")
	}
}

// TestMemberPutDefaultsTheSource holds that a membership written without a
// source is stored as an administrator's.
func TestMemberPutDefaultsTheSource(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPut, "/auth/admin/tenants/"+tenantA+"/members/"+string(userID), map[string]any{"active": true}, fresh()...)
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

// TestPasswordResetWithoutASessionStore holds the run of a password reset in an
// assembly with no session store: the write succeeds, nothing is revoked, and
// the module says so.
func TestPasswordResetWithoutASessionStore(t *testing.T) {
	r := newRig(t, noSessions)
	rec := r.do(http.MethodPost, "/auth/admin/accounts/"+string(userID)+"/password",
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
