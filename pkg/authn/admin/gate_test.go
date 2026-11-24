package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

type gateRoute struct {
	name  string
	write bool
	seed  func(r *rig)
	req   func(r *rig) *httptest.ResponseRecorder
}

func seedObjects(r *rig) {
	r.accounts.put("acct-t2", authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
	r.sessions.put(authn.SessionInfo{ID: sessionA, Account: "acct-t2", Kind: authn.SessionBrowser})
	r.members.put(authn.Membership{Account: "acct-t2", Tenant: tenantB, Active: true, Source: authn.ReasonAdmin})
	r.providers.put(authn.SSOProvider{
		ID: idpA, Tenant: tenantB, Protocol: authn.SSOProtocolSAML, Provisioning: authn.ProvisioningJIT,
	})
	r.records.put(authn.ImpersonationGrant{ID: grantA, Actor: adminID, Target: "acct-t2", Tenant: tenantB})
}

func managementRoutes() []gateRoute {
	seed := func(r *rig) { seedObjects(r) }
	return []gateRoute{
		{"account list", false, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodGet, "/api/v1/system/auth/accounts", nil, fresh()...)
		}},
		{"account get", false, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodGet, "/api/v1/system/auth/accounts/acct-t2", nil, fresh()...)
		}},
		{"account create", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodPost, "/api/v1/system/auth/accounts", map[string]any{"kind": "user", "username": "new"}, fresh()...)
		}},
		{"account patch", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodPatch, "/api/v1/system/auth/accounts/acct-t2", map[string]any{"display_name": "y"}, fresh()...)
		}},
		{"account status", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodPost, "/api/v1/system/auth/accounts/acct-t2/status", map[string]any{"status": "disabled"}, fresh()...)
		}},
		{"identifier add", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodPost, "/api/v1/system/auth/accounts/acct-t2/identifiers",
				map[string]any{"kind": "username", "value": "z"}, fresh()...)
		}},
		{"identifier remove", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodDelete, "/api/v1/system/auth/accounts/acct-t2/identifiers/"+identifier, nil, fresh()...)
		}},
		{"password reset", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodPost, "/api/v1/system/auth/accounts/acct-t2/password",
				map[string]any{"password": "a-long-temporary-one"}, fresh()...)
		}},
		{"factor list", false, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodGet, "/api/v1/system/auth/accounts/acct-t2/factors", nil, fresh()...)
		}},
		{"mfa required", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodPost, "/api/v1/system/auth/accounts/acct-t2/mfa-required",
				map[string]any{"required": true}, fresh()...)
		}},
		{"mfa reset", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodPost, "/api/v1/system/auth/accounts/acct-t2/mfa-reset", nil, fresh()...)
		}},
		{"account sessions", false, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodGet, "/api/v1/system/auth/accounts/acct-t2/sessions", nil, fresh()...)
		}},
		{"revoke sessions", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodPost, "/api/v1/system/auth/accounts/acct-t2/revoke-sessions", nil, fresh()...)
		}},
		{"secret list", false, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodGet, "/api/v1/system/auth/accounts/acct-t2/secrets", nil, fresh()...)
		}},
		{"secret create", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodPost, "/api/v1/system/auth/accounts/acct-t2/secrets", map[string]any{"label": "ci"}, fresh()...)
		}},
		{"secret revoke", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodDelete, "/api/v1/system/auth/accounts/acct-t2/secrets/"+secretA, nil, fresh()...)
		}},
		{"member list", false, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodGet, "/api/v1/tenants/"+tenantB+"/members", nil, fresh()...)
		}},
		{"member put", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodPut, "/api/v1/tenants/"+tenantB+"/members/acct-t2",
				map[string]any{"active": true}, fresh()...)
		}},
		{"sso exempt", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodPut, "/api/v1/tenants/"+tenantB+"/members/acct-t2/sso-exempt",
				map[string]any{"sso_exempt": true}, fresh()...)
		}},
		{"member remove", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodDelete, "/api/v1/tenants/"+tenantB+"/members/acct-t2", nil, fresh()...)
		}},
		{"provider list", false, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodGet, "/api/v1/tenants/"+tenantB+"/sso/providers", nil, fresh()...)
		}},
		{"provider create", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodPost, "/api/v1/tenants/"+tenantB+"/sso/providers",
				map[string]any{"protocol": "saml", "name": "x", "provisioning": "jit"}, fresh()...)
		}},
		{"provider get", false, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodGet, "/api/v1/tenants/"+tenantB+"/sso/providers/"+idpA, nil, fresh()...)
		}},
		{"provider update", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodPut, "/api/v1/tenants/"+tenantB+"/sso/providers/"+idpA,
				map[string]any{"protocol": "saml", "name": "x", "provisioning": "jit"}, fresh()...)
		}},
		{"provider delete", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodDelete, "/api/v1/tenants/"+tenantB+"/sso/providers/"+idpA, nil, fresh()...)
		}},
		{"domain claim", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodPost, "/api/v1/tenants/"+tenantB+"/sso/providers/"+idpA+"/domains",
				map[string]any{"domain": "d.example"}, fresh()...)
		}},
		{"domain remove", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodDelete, "/api/v1/tenants/"+tenantB+"/sso/providers/"+idpA+"/domains/d.example", nil, fresh()...)
		}},
		{"domain verify", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodPost, "/api/v1/tenants/"+tenantB+"/sso/providers/"+idpA+"/domains/d.example/verify", nil, fresh()...)
		}},
		{"reverify domains", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodPost, "/api/v1/tenants/"+tenantB+"/sso/providers/"+idpA+"/reverify-domains", nil, fresh()...)
		}},
		{"metadata refresh", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodPost, "/api/v1/tenants/"+tenantB+"/sso/providers/"+idpA+"/metadata/refresh", nil, fresh()...)
		}},
		{"impersonation list", false, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodGet, "/api/v1/system/tenants/"+tenantB+"/impersonations", nil, fresh()...)
		}},
		{"impersonation revoke", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodDelete, "/api/v1/system/tenants/"+tenantB+"/impersonations/"+grantA, nil, fresh()...)
		}},
		{"session delete", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodDelete, "/api/v1/system/auth/sessions/"+string(sessionA), nil, fresh()...)
		}},
		{"account delete", true, seed, func(r *rig) *httptest.ResponseRecorder {
			return r.do(http.MethodDelete, "/api/v1/system/auth/accounts/acct-t2", nil, fresh()...)
		}},
	}
}

// TestAPendingRegistrationCannotWrite checks that every management write by a pending registration is refused contact-required without deciding, auditing or writing.
func TestAPendingRegistrationCannotWrite(t *testing.T) {
	for _, tc := range managementRoutes() {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			tc.seed(r)
			r.verification.state = authn.SignupStatus{State: authn.SignupPending}
			rec := tc.req(r)
			if tc.write {
				if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "contact-required") {
					t.Fatalf("status = %d, want 403 contact-required: %s", rec.Code, rec.Body.String())
				}
				if r.decider.count() != 0 {
					t.Errorf("the Decider was asked %d times, want none", r.decider.count())
				}
				if len(r.audit.entries()) != 0 {
					t.Errorf("an audit row was written for a refused write: %+v", r.audit.entries())
				}
				if got := r.accountAdm.count("create") + r.accountAdm.count("delete") +
					r.accountAdm.count("set-display-name") + r.accountAdm.count("set-status") +
					r.accountAdm.count("add-identifier") + r.accountAdm.count("remove-identifier") +
					r.accountAdm.count("set-mfa-required") + r.accountAdm.count("create-secret") +
					r.accountAdm.count("revoke-secret"); got != 0 {
					t.Errorf("a refused write called the account writes %d times, want none", got)
				}
				if r.passwords.calls != 0 || r.mfa.resets != 0 || len(r.members.puts) != 0 ||
					r.members.rmd != 0 || r.sessions.revoked+r.sessions.revokedAcct != 0 ||
					len(r.providers.puts) != 0 || r.providers.deletes != 0 ||
					len(r.providers.domains) != 0 || r.providers.reverify != 0 ||
					r.refresher.calls != 0 || r.records.revokes != 0 {
					t.Error("a refused write called a capability that writes")
				}
			} else {
				if rec.Code < 200 || rec.Code >= 300 {
					t.Fatalf("a read answered %d, want a success: %s", rec.Code, rec.Body.String())
				}
				if r.verification.readCount() != 0 {
					t.Errorf("a read read the signup state %d times, want none", r.verification.readCount())
				}
			}
		})
	}
}

// TestTheSignupGateIsAnsweredBeforeTheStrengthCheck checks that a pending registration is answered contact-required before the strength check.
func TestTheSignupGateIsAnsweredBeforeTheStrengthCheck(t *testing.T) {
	weak := fresh(authntest.WithAssurance(authn.AssuranceSingle))

	t.Run("a pending registration", func(t *testing.T) {
		r := newRig(t)
		seedObjects(r)
		r.verification.state = authn.SignupStatus{State: authn.SignupPending}
		rec := r.do(http.MethodDelete, "/api/v1/system/auth/accounts/acct-t2", nil, weak...)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "contact-required") {
			t.Fatalf("status = %d, want 403 contact-required: %s", rec.Code, rec.Body.String())
		}
		if r.decider.count() != 0 {
			t.Errorf("the Decider was asked %d times, want none", r.decider.count())
		}
		if len(r.audit.entries()) != 0 {
			t.Errorf("an audit row was written for a refused write: %+v", r.audit.entries())
		}
		if r.accountAdm.count("delete") != 0 {
			t.Error("a refused write called the account write")
		}
	})

	t.Run("a never-registered account", func(t *testing.T) {
		r := newRig(t)
		seedObjects(r)
		rec := r.do(http.MethodDelete, "/api/v1/system/auth/accounts/acct-t2", nil, weak...)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "insufficient_user_authentication") {
			t.Fatalf("status = %d, want 401 insufficient_user_authentication: %s", rec.Code, rec.Body.String())
		}
	})
}

// TestTheSignupGateReadsTheOperatorsOwnState checks that the gate reads the impersonating operator's state, not the impersonated account's.
func TestTheSignupGateReadsTheOperatorsOwnState(t *testing.T) {
	r := newRig(t)
	seedObjects(r)
	r.verification.states = map[authn.AccountID]authn.SignupStatus{
		adminID: {State: authn.SignupPending},
		userID:  {State: authn.SignupVerified},
	}
	rec := r.do(http.MethodPut, "/api/v1/tenants/"+tenantA+"/members/"+string(userID),
		map[string]any{"active": true}, impersonating()...)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "contact-required") {
		t.Fatalf("status = %d, want 403 contact-required: %s", rec.Code, rec.Body.String())
	}
	if got := r.verification.asked; len(got) != 1 || got[0] != adminID {
		t.Errorf("the gate read the signup state of %v, want the operator %q", got, adminID)
	}
	if r.decider.count() != 0 {
		t.Errorf("the Decider was asked %d times, want none", r.decider.count())
	}
	if len(r.audit.entries()) != 0 {
		t.Errorf("an audit row was written for a refused write: %+v", r.audit.entries())
	}
	if len(r.members.puts) != 0 {
		t.Errorf("a refused write called the membership write: %+v", r.members.puts)
	}
}

// TestAnUnreadableSignupStateIs503 checks that a write whose signup state cannot be read is a 503 that asks no Decider and writes nothing.
func TestAnUnreadableSignupStateIs503(t *testing.T) {
	r := newRig(t)
	seedObjects(r)
	r.verification.err = authn.ErrUnavailable
	rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts", map[string]any{"kind": "user", "username": "new"}, fresh()...)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if r.decider.count() != 0 {
		t.Errorf("the Decider was asked %d times, want none", r.decider.count())
	}
	if r.accountAdm.count("create") != 0 {
		t.Errorf("the account write was called")
	}
}

// TestAServiceAccountAndAnUnregisteredAccountWrite checks that service accounts and never-registered accounts are not gated.
func TestAServiceAccountAndAnUnregisteredAccountWrite(t *testing.T) {
	t.Run("a service account", func(t *testing.T) {
		r := newRig(t)
		seedObjects(r)
		r.accounts.put(adminID, authn.Account{Kind: authn.KindService, Status: authn.StatusActive})
		r.verification.state = authn.SignupStatus{State: authn.SignupPending}
		rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts", map[string]any{"kind": "user", "username": "new"}, fresh()...)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
		}
		if got := r.verification.readCount(); got != 0 {
			t.Errorf("a service account's signup state was read %d times, want none", got)
		}
	})
	t.Run("a never-registered account", func(t *testing.T) {
		r := newRig(t)
		seedObjects(r)
		r.verification.state = authn.SignupStatus{State: authn.SignupNone}
		rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts", map[string]any{"kind": "user", "username": "new"}, fresh()...)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
		}
	})
}
