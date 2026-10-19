package admin

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// route is one management route as a case drives it: its handler, the method
// and path, and the path parameters the handler reads. The handler is the one
// the operation's declaration binds, so a case drives the same entry point the
// mounter registers: the input is bound from the request before the handler
// runs.
type route struct {
	name    string
	handler gin.HandlerFunc
	method  string
	path    string
	params  gin.Params
}

// allRoutes lists every management route of the module. A case drives them to
// hold a property that must hold of each: the impersonation refusal and the
// refusal of a Decider that is not there. The bodies are left out, because the
// checks those cases exercise come before a body is read.
func allRoutes(p *product) []route {
	return []route{
		{"account list", accountListOperation.Handle(p.handleAccountList), http.MethodGet, "/admin/accounts", nil},
		{"account create", accountCreateOperation.Handle(p.handleAccountCreate), http.MethodPost, "/admin/accounts", nil},
		{"account get", accountGetOperation.Handle(p.handleAccountGet), http.MethodGet, "/admin/accounts/x", gin.Params{{Key: "id", Value: string(userID)}}},
		{"account patch", accountPatchOperation.Handle(p.handleAccountPatch), http.MethodPatch, "/admin/accounts/x", gin.Params{{Key: "id", Value: string(userID)}}},
		{"account delete", accountDeleteOperation.Handle(p.handleAccountDelete), http.MethodDelete, "/admin/accounts/x", gin.Params{{Key: "id", Value: string(userID)}}},
		{"account status", accountStatusOperation.Handle(p.handleAccountStatus), http.MethodPost, "/admin/accounts/x/status", gin.Params{{Key: "id", Value: string(userID)}}},
		{"identifier add", identifierAddOperation.Handle(p.handleIdentifierAdd), http.MethodPost, "/admin/accounts/x/identifiers", gin.Params{{Key: "id", Value: string(userID)}}},
		{"identifier remove", identifierRemoveOperation.Handle(p.handleIdentifierRemove), http.MethodDelete, "/admin/accounts/x/identifiers/y", gin.Params{{Key: "id", Value: string(userID)}, {Key: "identifier", Value: identifier}}},
		{"password reset", passwordResetOperation.Handle(p.handlePasswordReset), http.MethodPost, "/admin/accounts/x/password", gin.Params{{Key: "id", Value: string(userID)}}},
		{"factor list", factorListOperation.Handle(p.handleFactorList), http.MethodGet, "/admin/accounts/x/factors", gin.Params{{Key: "id", Value: string(userID)}}},
		{"mfa required", mfaFlagOperation.Handle(p.handleMFARequired), http.MethodPost, "/admin/accounts/x/mfa-required", gin.Params{{Key: "id", Value: string(userID)}}},
		{"mfa reset", mfaResetOperation.Handle(p.handleMFAReset), http.MethodPost, "/admin/accounts/x/mfa-reset", gin.Params{{Key: "id", Value: string(userID)}}},
		{"account sessions", accountSessionsOperation.Handle(p.handleAccountSessions), http.MethodGet, "/admin/accounts/x/sessions", gin.Params{{Key: "id", Value: string(userID)}}},
		{"revoke sessions", revokeSessionsOperation.Handle(p.handleRevokeAccountSessions), http.MethodPost, "/admin/accounts/x/revoke-sessions", gin.Params{{Key: "id", Value: string(userID)}}},
		{"session revoke", sessionRevokeOperation.Handle(p.handleSessionRevoke), http.MethodDelete, "/admin/sessions/x", gin.Params{{Key: "id", Value: string(sessionA)}}},
		{"secret list", secretListOperation.Handle(p.handleSecretList), http.MethodGet, "/admin/accounts/x/secrets", gin.Params{{Key: "id", Value: string(userID)}}},
		{"secret create", secretCreateOperation.Handle(p.handleSecretCreate), http.MethodPost, "/admin/accounts/x/secrets", gin.Params{{Key: "id", Value: string(userID)}}},
		{"secret revoke", secretRevokeOperation.Handle(p.handleSecretRevoke), http.MethodDelete, "/admin/accounts/x/secrets/y", gin.Params{{Key: "id", Value: string(userID)}, {Key: "secret", Value: secretA}}},
		{"member list", memberListOperation.Handle(p.handleMemberList), http.MethodGet, "/admin/tenants/t/members", gin.Params{{Key: "tenant", Value: tenantA}}},
		{"member put", memberWriteOperation.Handle(p.handleMemberPut), http.MethodPut, "/admin/tenants/t/members/x", gin.Params{{Key: "tenant", Value: tenantA}, {Key: "account", Value: string(userID)}}},
		{"member remove", memberRemoveOperation.Handle(p.handleMemberRemove), http.MethodDelete, "/admin/tenants/t/members/x", gin.Params{{Key: "tenant", Value: tenantA}, {Key: "account", Value: string(userID)}}},
		{"sso exempt", memberExemptOperation.Handle(p.handleMemberSSOExempt), http.MethodPut, "/admin/tenants/t/members/x/sso-exempt", gin.Params{{Key: "tenant", Value: tenantA}, {Key: "account", Value: string(userID)}}},
		{"provider list", providerListOperation.Handle(p.handleProviderList), http.MethodGet, "/admin/tenants/t/sso/providers", gin.Params{{Key: "tenant", Value: tenantA}}},
		{"provider create", providerCreateOperation.Handle(p.handleProviderCreate), http.MethodPost, "/admin/tenants/t/sso/providers", gin.Params{{Key: "tenant", Value: tenantA}}},
		{"provider get", providerGetOperation.Handle(p.handleProviderGet), http.MethodGet, "/admin/sso/providers/x", gin.Params{{Key: "id", Value: idpA}}},
		{"provider update", providerUpdateOperation.Handle(p.handleProviderUpdate), http.MethodPut, "/admin/sso/providers/x", gin.Params{{Key: "id", Value: idpA}}},
		{"provider delete", providerDeleteOperation.Handle(p.handleProviderDelete), http.MethodDelete, "/admin/sso/providers/x", gin.Params{{Key: "id", Value: idpA}}},
		{"domain claim", domainClaimOperation.Handle(p.handleDomainClaim), http.MethodPost, "/admin/sso/providers/x/domains", gin.Params{{Key: "id", Value: idpA}}},
		{"domain remove", domainRemoveOperation.Handle(p.handleDomainRemove), http.MethodDelete, "/admin/sso/providers/x/domains/d", gin.Params{{Key: "id", Value: idpA}, {Key: "domain", Value: "d.example"}}},
		{"domain verify", domainVerifyOperation.Handle(p.handleDomainVerify), http.MethodPost, "/admin/sso/providers/x/domains/d/verify", gin.Params{{Key: "id", Value: idpA}, {Key: "domain", Value: "d.example"}}},
		{"reverify domains", reverifyDomainsOperation.Handle(p.handleReverifyDomains), http.MethodPost, "/admin/sso/providers/x/reverify-domains", gin.Params{{Key: "id", Value: idpA}}},
		{"metadata refresh", metadataRefreshOperation.Handle(p.handleMetadataRefresh), http.MethodPost, "/admin/sso/providers/x/metadata/refresh", gin.Params{{Key: "id", Value: idpA}}},
		{"impersonation list", impersonationListOperation.Handle(p.handleImpersonationList), http.MethodGet, "/admin/impersonations", nil},
		{"impersonation revoke", impersonationRevokeOperation.Handle(p.handleImpersonationRevoke), http.MethodDelete, "/admin/impersonations/x", gin.Params{{Key: "id", Value: grantA}}},
	}
}

// TestDecisionFill holds the Decision the Decider receives for every management
// route: the Action the route decides under, and — for the routes that act on
// another account's or another tenant's object — the tenant, target and target
// account. A tenant T1 administrator acting on a tenant T2 object must hand the
// Decider T2 as the tenant on the tenant-level routes, and the object's account
// as the target account on the account-level ones, so a Decider that decides by
// tenant cannot be told the wrong one and one that decides by action cannot be
// told the wrong one either.
func TestDecisionFill(t *testing.T) {
	r := newRig(t)
	r.accounts.put("acct-t2", authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
	r.sessions.put(authn.SessionInfo{ID: sessionA, Account: "acct-t2", Kind: authn.SessionBrowser})
	r.providers.put(authn.SSOProvider{
		ID: idpA, Tenant: tenantB, Protocol: authn.SSOProtocolSAML,
		Provisioning: authn.ProvisioningJIT,
	})
	r.records.put(authn.ImpersonationGrant{ID: grantA, Actor: adminID, Target: "acct-t2", Tenant: tenantB})
	r.members.put(authn.Membership{Account: "acct-t2", Tenant: tenantB, Active: true, Source: authn.ReasonAdmin})

	cases := []struct {
		name        string
		method      string
		path        string
		body        any
		wantAction  authn.Action
		wantTenant  string
		wantTarget  string
		wantAccount string
	}{
		{"account list", http.MethodGet, "/api/v1/auth/admin/accounts", nil, authn.ActionAccountRead, "", "", ""},
		{"account get", http.MethodGet, "/api/v1/auth/admin/accounts/acct-t2", nil, authn.ActionAccountRead, "", "acct-t2", "acct-t2"},
		{"account create", http.MethodPost, "/api/v1/auth/admin/accounts", map[string]any{"kind": "user", "username": "new"}, authn.ActionAccountCreate, "", "", ""},
		{"account patch", http.MethodPatch, "/api/v1/auth/admin/accounts/acct-t2", map[string]any{"display_name": "y"}, authn.ActionAccountUpdate, "", "acct-t2", "acct-t2"},
		{"account status", http.MethodPost, "/api/v1/auth/admin/accounts/acct-t2/status", map[string]any{"status": "disabled"}, authn.ActionAccountDisable, "", "acct-t2", "acct-t2"},
		{"identifier add", http.MethodPost, "/api/v1/auth/admin/accounts/acct-t2/identifiers", map[string]any{"kind": "username", "value": "z"}, authn.ActionAccountUpdate, "", "acct-t2", "acct-t2"},
		{"identifier remove", http.MethodDelete, "/api/v1/auth/admin/accounts/acct-t2/identifiers/" + identifier, nil, authn.ActionAccountUpdate, "", "acct-t2", "acct-t2"},
		{"password reset", http.MethodPost, "/api/v1/auth/admin/accounts/acct-t2/password", map[string]any{"password": "a-long-temporary-one"}, authn.ActionAccountUpdate, "", "acct-t2", "acct-t2"},
		{"factor list", http.MethodGet, "/api/v1/auth/admin/accounts/acct-t2/factors", nil, authn.ActionAccountRead, "", "acct-t2", "acct-t2"},
		{"mfa required", http.MethodPost, "/api/v1/auth/admin/accounts/acct-t2/mfa-required", map[string]any{"required": true}, authn.ActionAccountUpdate, "", "acct-t2", "acct-t2"},
		{"mfa reset", http.MethodPost, "/api/v1/auth/admin/accounts/acct-t2/mfa-reset", nil, authn.ActionMFAReset, "", "acct-t2", "acct-t2"},
		{"account sessions", http.MethodGet, "/api/v1/auth/admin/accounts/acct-t2/sessions", nil, authn.ActionSessionRead, "", "acct-t2", "acct-t2"},
		{"revoke sessions", http.MethodPost, "/api/v1/auth/admin/accounts/acct-t2/revoke-sessions", nil, authn.ActionSessionRevoke, "", "acct-t2", "acct-t2"},
		{"secret list", http.MethodGet, "/api/v1/auth/admin/accounts/acct-t2/secrets", nil, authn.ActionAccountRead, "", "acct-t2", "acct-t2"},
		{"secret create", http.MethodPost, "/api/v1/auth/admin/accounts/acct-t2/secrets", map[string]any{"label": "ci"}, authn.ActionClientManage, "", "acct-t2", "acct-t2"},
		{"secret revoke", http.MethodDelete, "/api/v1/auth/admin/accounts/acct-t2/secrets/" + secretA, nil, authn.ActionClientManage, "", "acct-t2", "acct-t2"},
		{"member list", http.MethodGet, "/api/v1/auth/admin/tenants/" + tenantB + "/members", nil, authn.ActionMembershipManage, tenantB, "", ""},
		{
			"member put", http.MethodPut, "/api/v1/auth/admin/tenants/" + tenantB + "/members/acct-t2",
			map[string]any{"active": true},
			authn.ActionMembershipManage, tenantB, "acct-t2", "acct-t2",
		},
		{
			"sso exempt", http.MethodPut, "/api/v1/auth/admin/tenants/" + tenantB + "/members/acct-t2/sso-exempt",
			map[string]any{"sso_exempt": true},
			authn.ActionMembershipManage, tenantB, "acct-t2", "acct-t2",
		},
		{"member remove", http.MethodDelete, "/api/v1/auth/admin/tenants/" + tenantB + "/members/acct-t2", nil, authn.ActionMembershipManage, tenantB, "acct-t2", "acct-t2"},
		{"provider list", http.MethodGet, "/api/v1/auth/admin/tenants/" + tenantB + "/sso/providers", nil, authn.ActionIDPManage, tenantB, "", ""},
		{
			"provider create", http.MethodPost, "/api/v1/auth/admin/tenants/" + tenantB + "/sso/providers",
			map[string]any{"protocol": "saml", "name": "x", "provisioning": "jit"},
			authn.ActionIDPManage, tenantB, "", "",
		},
		{"provider get", http.MethodGet, "/api/v1/auth/admin/sso/providers/" + idpA, nil, authn.ActionIDPManage, tenantB, idpA, ""},
		{
			"provider update", http.MethodPut, "/api/v1/auth/admin/sso/providers/" + idpA,
			map[string]any{"protocol": "saml", "name": "x", "provisioning": "jit"},
			authn.ActionIDPManage, tenantB, idpA, "",
		},
		{"domain claim", http.MethodPost, "/api/v1/auth/admin/sso/providers/" + idpA + "/domains", map[string]any{"domain": "d.example"}, authn.ActionIDPManage, tenantB, idpA, ""},
		{"domain remove", http.MethodDelete, "/api/v1/auth/admin/sso/providers/" + idpA + "/domains/d.example", nil, authn.ActionIDPManage, tenantB, idpA, ""},
		{"domain verify", http.MethodPost, "/api/v1/auth/admin/sso/providers/" + idpA + "/domains/d.example/verify", nil, authn.ActionIDPManage, tenantB, idpA, ""},
		{"reverify domains", http.MethodPost, "/api/v1/auth/admin/sso/providers/" + idpA + "/reverify-domains", nil, authn.ActionIDPManage, tenantB, idpA, ""},
		{"metadata refresh", http.MethodPost, "/api/v1/auth/admin/sso/providers/" + idpA + "/metadata/refresh", nil, authn.ActionIDPManage, tenantB, idpA, ""},
		{"impersonation list", http.MethodGet, "/api/v1/auth/admin/impersonations?tenant=" + tenantB, nil, authn.ActionImpersonationRead, tenantB, "", ""},
		{"impersonation revoke", http.MethodDelete, "/api/v1/auth/admin/impersonations/" + grantA, nil, authn.ActionImpersonationRevoke, tenantB, "acct-t2", "acct-t2"},
		{"session delete", http.MethodDelete, "/api/v1/auth/admin/sessions/" + string(sessionA), nil, authn.ActionSessionRevoke, "", string(sessionA), "acct-t2"},
		{"account delete", http.MethodDelete, "/api/v1/auth/admin/accounts/acct-t2", nil, authn.ActionAccountDelete, "", "acct-t2", "acct-t2"},
		{"provider delete", http.MethodDelete, "/api/v1/auth/admin/sso/providers/" + idpA, nil, authn.ActionIDPManage, tenantB, idpA, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := r.do(tc.method, tc.path, tc.body, fresh()...)
			if rec.Code < 200 || rec.Code >= 300 {
				t.Fatalf("status = %d, want a success; body %s", rec.Code, rec.Body.String())
			}
			got, ok := r.decider.last()
			if !ok {
				t.Fatal("the Decider was not asked")
			}
			if got.Action != tc.wantAction {
				t.Errorf("Decision.Action = %q, want %q", got.Action, tc.wantAction)
			}
			if got.Tenant != tc.wantTenant {
				t.Errorf("Decision.Tenant = %q, want %q", got.Tenant, tc.wantTenant)
			}
			if got.Target != tc.wantTarget {
				t.Errorf("Decision.Target = %q, want %q", got.Target, tc.wantTarget)
			}
			if got.TargetAccount != tc.wantAccount {
				t.Errorf("Decision.TargetAccount = %q, want %q", got.TargetAccount, tc.wantAccount)
			}
			r.decider.calls = nil
		})
	}
}

// TestATenantMismatchIsRefusedBeforeTheDecider holds that a path, a body and a
// stored row that disagree on the tenant are a 400 and the Decider is never
// asked.
func TestATenantMismatchIsRefusedBeforeTheDecider(t *testing.T) {
	r := newRig(t)
	r.providers.put(authn.SSOProvider{
		ID: idpA, Tenant: tenantB, Protocol: authn.SSOProtocolSAML,
		Provisioning: authn.ProvisioningJIT,
	})

	// The body names a tenant other than the path's.
	rec := r.do(http.MethodPost, "/api/v1/auth/admin/tenants/"+tenantA+"/sso/providers",
		map[string]any{"tenant": tenantB, "protocol": "saml", "name": "x", "provisioning": "jit"}, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("create with a mismatched tenant: status = %d, want 400", rec.Code)
	}
	if r.decider.count() != 0 {
		t.Errorf("the Decider was asked %d times, want none", r.decider.count())
	}

	// The body names a tenant other than the stored one, which PUT cannot change.
	rec = r.do(http.MethodPut, "/api/v1/auth/admin/sso/providers/"+idpA,
		map[string]any{"tenant": tenantA, "protocol": "saml", "name": "x", "provisioning": "jit"}, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("update changing the tenant: status = %d, want 400", rec.Code)
	}
	if r.decider.count() != 0 {
		t.Errorf("the Decider was asked %d times after a mismatch, want none", r.decider.count())
	}
}

// TestNoDeciderDeniesEveryRoute holds that an assembly with no Decider answers
// every management route with a 403 and never asks any capability.
func TestNoDeciderDeniesEveryRoute(t *testing.T) {
	r := newRig(t, noDecider)
	for _, rt := range allRoutes(r.p) {
		rec := r.invoke(rt.handler, rt.method, rt.path, rt.params, nil, fresh()...)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s with no Decider: status = %d, want 403", rt.name, rec.Code)
		}
	}
	if r.accountAdm.count("create") != 0 || r.accountAdm.count("delete") != 0 {
		t.Error("a capability was written with no Decider present")
	}
}

// TestAnImpersonatingActorIsRefusedAtTheHandler holds that an impersonating
// identity reaching each handler directly is answered 403
// impersonation-restricted, before the Decider is asked.
func TestAnImpersonatingActorIsRefusedAtTheHandler(t *testing.T) {
	r := newRig(t)
	for _, rt := range allRoutes(r.p) {
		rec := r.invoke(rt.handler, rt.method, rt.path, rt.params, nil, impersonating()...)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s as an impersonating identity: status = %d, want 403", rt.name, rec.Code)
		}
	}
	if r.decider.count() != 0 {
		t.Errorf("the Decider was asked %d times for an impersonating identity, want none", r.decider.count())
	}
}

// TestTheMandatoryAuditLandsBeforeTheWrite holds that a write whose mandatory
// audit row cannot be written is answered 503 and the capability is not called,
// so that an unrecorded management action cannot be taken.
func TestTheMandatoryAuditLandsBeforeTheWrite(t *testing.T) {
	// A create: the row is written before Create.
	r := newRig(t)
	r.audit.fails(audit.ErrWriteFailed)
	rec := r.do(http.MethodPost, "/api/v1/auth/admin/accounts",
		map[string]any{"kind": "user", "username": "alice"}, fresh()...)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("create with a failing audit: status = %d, want 503", rec.Code)
	}
	if r.accountAdm.count("create") != 0 {
		t.Error("Create was called though the mandatory audit failed")
	}

	// A delete: the row is written before Delete.
	r = newRig(t)
	r.audit.fails(audit.ErrWriteFailed)
	rec = r.do(http.MethodDelete, "/api/v1/auth/admin/accounts/"+string(userID), nil, fresh()...)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("delete with a failing audit: status = %d, want 503", rec.Code)
	}
	if r.accountAdm.count("delete") != 0 {
		t.Error("Delete was called though the mandatory audit failed")
	}
}

// TestTheMandatoryAuditLandsBeforeASecretWrite holds the same ordering for the
// service-secret group, whose route needs client.manage and a fresh
// authentication.
func TestTheMandatoryAuditLandsBeforeASecretWrite(t *testing.T) {
	r := newRig(t)
	r.audit.fails(audit.ErrWriteFailed)
	rec := r.do(http.MethodPost, "/api/v1/auth/admin/accounts/"+string(userID)+"/secrets",
		map[string]any{"label": "ci"}, fresh()...)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("secret create with a failing audit: status = %d, want 503", rec.Code)
	}
	if r.accountAdm.count("create-secret") != 0 {
		t.Error("CreateServiceSecret was called though the mandatory audit failed")
	}
}

// TestAStaticSubjectIsRefusedOnTheStrongRoutes holds that a subject of the
// static credential table, which has no factors and no account to answer for,
// is refused on the routes that ask for a seasoned administrator.
func TestAStaticSubjectIsRefusedOnTheStrongRoutes(t *testing.T) {
	r := newRig(t)
	static := []authntest.Option{
		authntest.WithAccount("static.ops"),
		authntest.WithBackend("static"),
		authntest.WithMethods(authn.MethodPassword, authn.MethodTOTP),
		authntest.WithAssurance(authn.AssuranceMulti),
		authntest.WithAuthTime(time.Now()),
	}
	for _, rt := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/api/v1/auth/admin/accounts/" + string(userID) + "/password", map[string]any{"password": "a-long-temporary-one"}},
		{http.MethodPost, "/api/v1/auth/admin/accounts/" + string(userID) + "/mfa-reset", nil},
	} {
		rec := r.do(rt.method, rt.path, rt.body, static...)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s by a static subject: status = %d, want 401", rt.method, rt.path, rec.Code)
		}
	}
	if r.mfa.resets != 0 {
		t.Error("Reset ran for a static subject")
	}
}

// TestADeciderThatFailsOrPanicsIsUnavailable holds that a returned error and a
// panic alike are a 503 and no action is carried out.
func TestADeciderThatFailsOrPanicsIsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*fakeDecider)
	}{
		{"error", func(d *fakeDecider) { d.err = errors.New("no answer") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			tc.setup(r.decider)
			rec := r.do(http.MethodPost, "/api/v1/auth/admin/accounts",
				map[string]any{"kind": "user", "username": "alice"}, fresh()...)
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 503", rec.Code)
			}
			if r.accountAdm.count("create") != 0 {
				t.Error("Create ran though the Decider failed")
			}
		})
	}

	// A Decider that panics is caught and answered the same way.
	r := newRig(t)
	r.p.decider = panicDecider{}
	rec := r.do(http.MethodPost, "/api/v1/auth/admin/accounts",
		map[string]any{"kind": "user", "username": "alice"}, fresh()...)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("panicking decider: status = %d, want 503", rec.Code)
	}
	if r.accountAdm.count("create") != 0 {
		t.Error("Create ran though the Decider panicked")
	}
}

// panicDecider is a Decider that panics, to drive the handler's recover.
type panicDecider struct{}

func (panicDecider) Decide(context.Context, authn.Decision) (bool, error) {
	panic("the decider panicked")
}

// TestARefusalIsForbiddenAndNothingHappens holds that a Decider which refuses
// is a 403 and the capability is not called.
func TestARefusalIsForbiddenAndNothingHappens(t *testing.T) {
	r := newRig(t)
	r.decider.allow = false
	rec := r.do(http.MethodPost, "/api/v1/auth/admin/accounts",
		map[string]any{"kind": "user", "username": "alice"}, fresh()...)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if r.accountAdm.count("create") != 0 {
		t.Error("Create ran though the Decider refused")
	}
	if len(r.audit.entries()) != 0 {
		t.Error("an audit row was written for a refusal")
	}
}

// TestACreateNeedsClientManageForAServiceAccount holds that a service account
// is asked about twice, and a user account once.
func TestACreateNeedsClientManageForAServiceAccount(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPost, "/api/v1/auth/admin/accounts",
		map[string]any{"kind": "service", "username": "svc"}, fresh()...)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	calls := r.decider.calls
	if len(calls) != 2 || calls[0].Action != authn.ActionAccountCreate || calls[1].Action != authn.ActionClientManage {
		t.Errorf("a service-account create asked %+v, want account.create then client.manage", calls)
	}

	r2 := newRig(t)
	r2.do(http.MethodPost, "/api/v1/auth/admin/accounts",
		map[string]any{"kind": "user", "username": "alice"}, fresh()...)
	if n := r2.decider.count(); n != 1 {
		t.Errorf("a user-account create asked the Decider %d times, want once", n)
	}
}

// TestTheImpersonationRecordGroup holds the record routes: the required tenant
// parameter, the pre-Decider 404 for a missing record, the best-effort tier of
// the revocation, and the 403 for an impersonating actor.
func TestTheImpersonationRecordGroup(t *testing.T) {
	r := newRig(t)
	r.records.put(authn.ImpersonationGrant{ID: grantA, Actor: adminID, Target: userID, Tenant: tenantB})
	r.records.put(authn.ImpersonationGrant{
		ID: "ended", Actor: adminID, Target: userID, Tenant: tenantB,
		EndedAt: time.Now(), EndReason: authn.EndExplicit,
	})

	// The tenant query parameter is required.
	rec := r.do(http.MethodGet, "/api/v1/auth/admin/impersonations", nil, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("list with no tenant: status = %d, want 400", rec.Code)
	}
	if r.decider.count() != 0 {
		t.Errorf("the Decider was asked for a list with no tenant")
	}

	// A list shows only the tenant's records.
	rec = r.do(http.MethodGet, "/api/v1/auth/admin/impersonations?tenant="+tenantB, nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200", rec.Code)
	}

	// A missing record is a 404 before the Decider.
	missing := newRig(t)
	rec = missing.do(http.MethodDelete, "/api/v1/auth/admin/impersonations/absent", nil, fresh()...)
	if rec.Code != http.StatusNotFound {
		t.Errorf("revoke of a missing record: status = %d, want 404", rec.Code)
	}
	if missing.decider.count() != 0 {
		t.Error("the Decider was asked for a record that is not there")
	}

	// The revocation is best-effort: a failing audit does not stop it, and an
	// already-ended record gets the same answer.
	r.audit.fails(audit.ErrWriteFailed)
	rec = r.do(http.MethodDelete, "/api/v1/auth/admin/impersonations/"+grantA, nil, fresh()...)
	if rec.Code != http.StatusNoContent {
		t.Errorf("revoke with a failing audit: status = %d, want 204", rec.Code)
	}
	if r.records.revokes != 1 {
		t.Errorf("the record was revoked %d times, want once", r.records.revokes)
	}
	rec = r.do(http.MethodDelete, "/api/v1/auth/admin/impersonations/ended", nil, fresh()...)
	if rec.Code != http.StatusNoContent {
		t.Errorf("revoke of an ended record: status = %d, want 204", rec.Code)
	}
}

// TestRouteGroupsFollowTheirCapabilities holds that a group whose capability is
// absent is not mounted, and that the others are unaffected.
func TestRouteGroupsFollowTheirCapabilities(t *testing.T) {
	cases := []struct {
		omit    leaveOut
		absent  [][2]string
		present [][2]string
	}{
		{
			omit:    noSessions,
			absent:  [][2]string{{http.MethodGet, "/api/v1/auth/admin/accounts/:id/sessions"}, {http.MethodDelete, "/api/v1/auth/admin/sessions/:id"}},
			present: [][2]string{{http.MethodGet, "/api/v1/auth/admin/accounts/:id/factors"}},
		},
		{
			omit:    noMFA,
			absent:  [][2]string{{http.MethodGet, "/api/v1/auth/admin/accounts/:id/factors"}, {http.MethodPost, "/api/v1/auth/admin/accounts/:id/password"}},
			present: [][2]string{{http.MethodGet, "/api/v1/auth/admin/accounts/:id/sessions"}},
		},
		{
			omit:    noTokens,
			absent:  [][2]string{{http.MethodGet, "/api/v1/auth/admin/accounts/:id/secrets"}},
			present: [][2]string{{http.MethodGet, "/api/v1/auth/admin/accounts/:id/sessions"}},
		},
		{
			omit:    noProviders,
			absent:  [][2]string{{http.MethodGet, "/api/v1/auth/admin/sso/providers/:id"}, {http.MethodPost, "/api/v1/auth/admin/sso/providers/:id/metadata/refresh"}},
			present: [][2]string{{http.MethodPost, "/api/v1/auth/admin/accounts/:id/revoke-sessions"}},
		},
		{
			omit:    noRefresher,
			absent:  [][2]string{{http.MethodPost, "/api/v1/auth/admin/sso/providers/:id/metadata/refresh"}},
			present: [][2]string{{http.MethodGet, "/api/v1/auth/admin/sso/providers/:id"}},
		},
		{
			omit:    noRecords,
			absent:  [][2]string{{http.MethodGet, "/api/v1/auth/admin/impersonations"}},
			present: [][2]string{{http.MethodGet, "/api/v1/auth/admin/accounts"}},
		},
	}
	for _, tc := range cases {
		t.Run(string(tc.omit), func(t *testing.T) {
			r := newRig(t, tc.omit)
			for _, want := range tc.absent {
				if _, ok := r.mounter.Registered(want[0], want[1]); ok {
					t.Errorf("%s %s is mounted though %s is absent", want[0], want[1], tc.omit)
				}
			}
			for _, want := range tc.present {
				if _, ok := r.mounter.Registered(want[0], want[1]); !ok {
					t.Errorf("%s %s is not mounted", want[0], want[1])
				}
			}
		})
	}
}

// TestTheAccountAndMembershipGroupsAreAlwaysMounted holds that removing every
// optional capability leaves the required groups in place.
func TestTheAccountAndMembershipGroupsAreAlwaysMounted(t *testing.T) {
	r := newRig(t, noSessions, noMFA, noTokens, noProviders, noRefresher, noRecords)
	for _, want := range [][2]string{
		{http.MethodGet, "/api/v1/auth/admin/accounts"},
		{http.MethodDelete, "/api/v1/auth/admin/accounts/:id"},
		{http.MethodGet, "/api/v1/auth/admin/tenants/:tenant/members"},
		{http.MethodPut, "/api/v1/auth/admin/tenants/:tenant/members/:account"},
	} {
		if _, ok := r.mounter.Registered(want[0], want[1]); !ok {
			t.Errorf("%s %s is not mounted", want[0], want[1])
		}
	}
}

// TestAnUnseasonedActorIsForbiddenOnTheResets holds the split of the two
// refusals the factor and password resets make of a failing actor: a missing
// seasoned factor is the 403 a step-up cannot clear and no write or audit row
// follows, while a weak authentication ahead of it stays the step-up 401 even
// when the seasoned answer is true.
func TestAnUnseasonedActorIsForbiddenOnTheResets(t *testing.T) {
	routes := []struct {
		name string
		path string
		body any
	}{
		{"factor reset", "/api/v1/auth/admin/accounts/" + string(userID) + "/mfa-reset", nil},
		{"password reset", "/api/v1/auth/admin/accounts/" + string(userID) + "/password", map[string]any{"password": "a-long-temporary-one"}},
	}

	for _, c := range routes {
		t.Run(c.name+", no seasoned factor", func(t *testing.T) {
			r := newRig(t)
			r.mfa.seasoned = false
			rec := r.do(http.MethodPost, c.path, c.body, fresh()...)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
			}
			if got := problemCode(t, rec.Body.String()); got != "factor-not-seasoned" {
				t.Errorf("the problem code is %q, want factor-not-seasoned", got)
			}
			if r.mfa.resets != 0 || r.passwords.calls != 0 {
				t.Errorf("the refused write ran: %d factor resets, %d password sets", r.mfa.resets, r.passwords.calls)
			}
			if rows := r.audit.entries(); len(rows) != 0 {
				t.Errorf("the refusal wrote %d audit rows, want none: %+v", len(rows), rows)
			}
		})
	}

	t.Run("a weak authentication with a seasoned factor", func(t *testing.T) {
		for _, c := range routes {
			r := newRig(t)
			rec := r.do(http.MethodPost, c.path, c.body,
				fresh(authntest.WithAssurance(authn.AssuranceSingle))...)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s: status = %d, want 401: %s", c.name, rec.Code, rec.Body.String())
			}
			if got := problemCode(t, rec.Body.String()); got != "insufficient_user_authentication" {
				t.Errorf("%s: the problem code is %q, want insufficient_user_authentication", c.name, got)
			}
		}
	})
}

// TestAStrengthShortfallIsAStepUp holds that an actor whose authentication is
// not a fresh local multi-factor one is answered 401 on the routes that ask for
// one, and the write does not happen.
func TestAStrengthShortfallIsAStepUp(t *testing.T) {
	r := newRig(t)
	stale := append(fresh(), authntest.WithAuthTime(time.Now().Add(-time.Hour)))
	rec := r.do(http.MethodDelete, "/api/v1/auth/admin/accounts/"+string(userID), nil, stale...)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("stale authentication: status = %d, want 401", rec.Code)
	}
	if r.accountAdm.count("delete") != 0 {
		t.Error("Delete ran though the actor was not strong enough")
	}
}
