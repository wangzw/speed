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

type route struct {
	name    string
	handler gin.HandlerFunc
	method  string
	path    string
	params  gin.Params
	domain  decisionDomain
}

func allRoutes(p *product) []route {
	return []route{
		{"account list", accountListOperation.Handle(p.handleAccountList), http.MethodGet, "/admin/accounts", nil, platformDomain},
		{"account create", accountCreateOperation.Handle(p.handleAccountCreate), http.MethodPost, "/admin/accounts", nil, platformDomain},
		{"account get", accountGetOperation.Handle(p.handleAccountGet), http.MethodGet, "/admin/accounts/x", gin.Params{{Key: "id", Value: string(userID)}}, platformDomain},
		{"account patch", accountPatchOperation.Handle(p.handleAccountPatch), http.MethodPatch, "/admin/accounts/x", gin.Params{{Key: "id", Value: string(userID)}}, platformDomain},
		{"account delete", accountDeleteOperation.Handle(p.handleAccountDelete), http.MethodDelete, "/admin/accounts/x", gin.Params{{Key: "id", Value: string(userID)}}, platformDomain},
		{"account status", accountStatusOperation.Handle(p.handleAccountStatus), http.MethodPost, "/admin/accounts/x/status", gin.Params{{Key: "id", Value: string(userID)}}, platformDomain},
		{"identifier add", identifierAddOperation.Handle(p.handleIdentifierAdd), http.MethodPost, "/admin/accounts/x/identifiers", gin.Params{{Key: "id", Value: string(userID)}}, platformDomain},
		{"identifier remove", identifierRemoveOperation.Handle(p.handleIdentifierRemove), http.MethodDelete, "/admin/accounts/x/identifiers/y", gin.Params{{Key: "id", Value: string(userID)}, {Key: "identifier", Value: identifier}}, platformDomain},
		{"password reset", passwordResetOperation.Handle(p.handlePasswordReset), http.MethodPost, "/admin/accounts/x/password", gin.Params{{Key: "id", Value: string(userID)}}, platformDomain},
		{"factor list", factorListOperation.Handle(p.handleFactorList), http.MethodGet, "/admin/accounts/x/factors", gin.Params{{Key: "id", Value: string(userID)}}, platformDomain},
		{"mfa required", mfaFlagOperation.Handle(p.handleMFARequired), http.MethodPost, "/admin/accounts/x/mfa-required", gin.Params{{Key: "id", Value: string(userID)}}, platformDomain},
		{"mfa reset", mfaResetOperation.Handle(p.handleMFAReset), http.MethodPost, "/admin/accounts/x/mfa-reset", gin.Params{{Key: "id", Value: string(userID)}}, platformDomain},
		{"account sessions", accountSessionsOperation.Handle(p.handleAccountSessions), http.MethodGet, "/admin/accounts/x/sessions", gin.Params{{Key: "id", Value: string(userID)}}, platformDomain},
		{"revoke sessions", revokeSessionsOperation.Handle(p.handleRevokeAccountSessions), http.MethodPost, "/admin/accounts/x/revoke-sessions", gin.Params{{Key: "id", Value: string(userID)}}, platformDomain},
		{"session revoke", sessionRevokeOperation.Handle(p.handleSessionRevoke), http.MethodDelete, "/admin/sessions/x", gin.Params{{Key: "id", Value: string(sessionA)}}, platformDomain},
		{"secret list", secretListOperation.Handle(p.handleSecretList), http.MethodGet, "/admin/accounts/x/secrets", gin.Params{{Key: "id", Value: string(userID)}}, platformDomain},
		{"secret create", secretCreateOperation.Handle(p.handleSecretCreate), http.MethodPost, "/admin/accounts/x/secrets", gin.Params{{Key: "id", Value: string(userID)}}, platformDomain},
		{"secret revoke", secretRevokeOperation.Handle(p.handleSecretRevoke), http.MethodDelete, "/admin/accounts/x/secrets/y", gin.Params{{Key: "id", Value: string(userID)}, {Key: "secret", Value: secretA}}, platformDomain},
		{"member list", memberListOperation.Handle(p.handleMemberList), http.MethodGet, "/admin/tenants/t/members", gin.Params{{Key: "tenant", Value: tenantA}}, tenantDomain},
		{"member put", memberWriteOperation.Handle(p.handleMemberPut), http.MethodPut, "/admin/tenants/t/members/x", gin.Params{{Key: "tenant", Value: tenantA}, {Key: "account", Value: string(userID)}}, tenantDomain},
		{"member remove", memberRemoveOperation.Handle(p.handleMemberRemove), http.MethodDelete, "/admin/tenants/t/members/x", gin.Params{{Key: "tenant", Value: tenantA}, {Key: "account", Value: string(userID)}}, tenantDomain},
		{"sso exempt", memberExemptOperation.Handle(p.handleMemberSSOExempt), http.MethodPut, "/admin/tenants/t/members/x/sso-exempt", gin.Params{{Key: "tenant", Value: tenantA}, {Key: "account", Value: string(userID)}}, tenantDomain},
		{"provider list", providerListOperation.Handle(p.handleProviderList), http.MethodGet, "/admin/tenants/t/sso/providers", gin.Params{{Key: "tenant", Value: tenantA}}, tenantDomain},
		{"provider create", providerCreateOperation.Handle(p.handleProviderCreate), http.MethodPost, "/admin/tenants/t/sso/providers", gin.Params{{Key: "tenant", Value: tenantA}}, tenantDomain},
		{"provider get", providerGetOperation.Handle(p.handleProviderGet), http.MethodGet, "/admin/sso/providers/x", gin.Params{{Key: "id", Value: idpA}}, tenantDomain},
		{"provider update", providerUpdateOperation.Handle(p.handleProviderUpdate), http.MethodPut, "/admin/sso/providers/x", gin.Params{{Key: "id", Value: idpA}}, tenantDomain},
		{"provider delete", providerDeleteOperation.Handle(p.handleProviderDelete), http.MethodDelete, "/admin/sso/providers/x", gin.Params{{Key: "id", Value: idpA}}, tenantDomain},
		{"domain claim", domainClaimOperation.Handle(p.handleDomainClaim), http.MethodPost, "/admin/sso/providers/x/domains", gin.Params{{Key: "id", Value: idpA}}, tenantDomain},
		{"domain remove", domainRemoveOperation.Handle(p.handleDomainRemove), http.MethodDelete, "/admin/sso/providers/x/domains/d", gin.Params{{Key: "id", Value: idpA}, {Key: "domain", Value: "d.example"}}, tenantDomain},
		{"domain verify", domainVerifyOperation.Handle(p.handleDomainVerify), http.MethodPost, "/admin/sso/providers/x/domains/d/verify", gin.Params{{Key: "id", Value: idpA}, {Key: "domain", Value: "d.example"}}, tenantDomain},
		{"reverify domains", reverifyDomainsOperation.Handle(p.handleReverifyDomains), http.MethodPost, "/admin/sso/providers/x/reverify-domains", gin.Params{{Key: "id", Value: idpA}}, tenantDomain},
		{"metadata refresh", metadataRefreshOperation.Handle(p.handleMetadataRefresh), http.MethodPost, "/admin/sso/providers/x/metadata/refresh", gin.Params{{Key: "id", Value: idpA}}, tenantDomain},
		{"impersonation list", impersonationListOperation.Handle(p.handleImpersonationList), http.MethodGet, "/tenants/t/impersonations", gin.Params{{Key: "tenant", Value: tenantA}}, platformDomain},
		{"impersonation revoke", impersonationRevokeOperation.Handle(p.handleImpersonationRevoke), http.MethodDelete, "/tenants/t/impersonations/x", gin.Params{{Key: "tenant", Value: tenantA}, {Key: "id", Value: grantA}}, platformDomain},
	}
}

// TestDecisionFill checks the Decision every management route sends to the Decider.
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
		{"account list", http.MethodGet, "/api/v1/system/auth/accounts", nil, authn.ActionAccountRead, "", "", ""},
		{"account get", http.MethodGet, "/api/v1/system/auth/accounts/acct-t2", nil, authn.ActionAccountRead, "", "acct-t2", "acct-t2"},
		{"account create", http.MethodPost, "/api/v1/system/auth/accounts", map[string]any{"kind": "user", "username": "new"}, authn.ActionAccountCreate, "", "", ""},
		{"account patch", http.MethodPatch, "/api/v1/system/auth/accounts/acct-t2", map[string]any{"display_name": "y"}, authn.ActionAccountUpdate, "", "acct-t2", "acct-t2"},
		{"account status", http.MethodPost, "/api/v1/system/auth/accounts/acct-t2/status", map[string]any{"status": "disabled"}, authn.ActionAccountDisable, "", "acct-t2", "acct-t2"},
		{"identifier add", http.MethodPost, "/api/v1/system/auth/accounts/acct-t2/identifiers", map[string]any{"kind": "username", "value": "z"}, authn.ActionAccountUpdate, "", "acct-t2", "acct-t2"},
		{"identifier remove", http.MethodDelete, "/api/v1/system/auth/accounts/acct-t2/identifiers/" + identifier, nil, authn.ActionAccountUpdate, "", "acct-t2", "acct-t2"},
		{"password reset", http.MethodPost, "/api/v1/system/auth/accounts/acct-t2/password", map[string]any{"password": "a-long-temporary-one"}, authn.ActionAccountUpdate, "", "acct-t2", "acct-t2"},
		{"factor list", http.MethodGet, "/api/v1/system/auth/accounts/acct-t2/factors", nil, authn.ActionAccountRead, "", "acct-t2", "acct-t2"},
		{"mfa required", http.MethodPost, "/api/v1/system/auth/accounts/acct-t2/mfa-required", map[string]any{"required": true}, authn.ActionAccountUpdate, "", "acct-t2", "acct-t2"},
		{"mfa reset", http.MethodPost, "/api/v1/system/auth/accounts/acct-t2/mfa-reset", nil, authn.ActionMFAReset, "", "acct-t2", "acct-t2"},
		{"account sessions", http.MethodGet, "/api/v1/system/auth/accounts/acct-t2/sessions", nil, authn.ActionSessionRead, "", "acct-t2", "acct-t2"},
		{"revoke sessions", http.MethodPost, "/api/v1/system/auth/accounts/acct-t2/revoke-sessions", nil, authn.ActionSessionRevoke, "", "acct-t2", "acct-t2"},
		{"secret list", http.MethodGet, "/api/v1/system/auth/accounts/acct-t2/secrets", nil, authn.ActionAccountRead, "", "acct-t2", "acct-t2"},
		{"secret create", http.MethodPost, "/api/v1/system/auth/accounts/acct-t2/secrets", map[string]any{"label": "ci"}, authn.ActionClientManage, "", "acct-t2", "acct-t2"},
		{"secret revoke", http.MethodDelete, "/api/v1/system/auth/accounts/acct-t2/secrets/" + secretA, nil, authn.ActionClientManage, "", "acct-t2", "acct-t2"},
		{"member list", http.MethodGet, "/api/v1/tenants/" + tenantB + "/members", nil, authn.ActionMembershipManage, tenantB, "", ""},
		{
			"member put", http.MethodPut, "/api/v1/tenants/" + tenantB + "/members/acct-t2",
			map[string]any{"active": true},
			authn.ActionMembershipManage, tenantB, "acct-t2", "acct-t2",
		},
		{
			"sso exempt", http.MethodPut, "/api/v1/tenants/" + tenantB + "/members/acct-t2/sso-exempt",
			map[string]any{"sso_exempt": true},
			authn.ActionMembershipManage, tenantB, "acct-t2", "acct-t2",
		},
		{"member remove", http.MethodDelete, "/api/v1/tenants/" + tenantB + "/members/acct-t2", nil, authn.ActionMembershipManage, tenantB, "acct-t2", "acct-t2"},
		{"provider list", http.MethodGet, "/api/v1/tenants/" + tenantB + "/sso/providers", nil, authn.ActionIDPManage, tenantB, "", ""},
		{
			"provider create", http.MethodPost, "/api/v1/tenants/" + tenantB + "/sso/providers",
			map[string]any{"protocol": "saml", "name": "x", "provisioning": "jit"},
			authn.ActionIDPManage, tenantB, "", "",
		},
		{"provider get", http.MethodGet, "/api/v1/tenants/" + tenantB + "/sso/providers/" + idpA, nil, authn.ActionIDPManage, tenantB, idpA, ""},
		{
			"provider update", http.MethodPut, "/api/v1/tenants/" + tenantB + "/sso/providers/" + idpA,
			map[string]any{"protocol": "saml", "name": "x", "provisioning": "jit"},
			authn.ActionIDPManage, tenantB, idpA, "",
		},
		{"domain claim", http.MethodPost, "/api/v1/tenants/" + tenantB + "/sso/providers/" + idpA + "/domains", map[string]any{"domain": "d.example"}, authn.ActionIDPManage, tenantB, idpA, ""},
		{"domain remove", http.MethodDelete, "/api/v1/tenants/" + tenantB + "/sso/providers/" + idpA + "/domains/d.example", nil, authn.ActionIDPManage, tenantB, idpA, ""},
		{"domain verify", http.MethodPost, "/api/v1/tenants/" + tenantB + "/sso/providers/" + idpA + "/domains/d.example/verify", nil, authn.ActionIDPManage, tenantB, idpA, ""},
		{"reverify domains", http.MethodPost, "/api/v1/tenants/" + tenantB + "/sso/providers/" + idpA + "/reverify-domains", nil, authn.ActionIDPManage, tenantB, idpA, ""},
		{"metadata refresh", http.MethodPost, "/api/v1/tenants/" + tenantB + "/sso/providers/" + idpA + "/metadata/refresh", nil, authn.ActionIDPManage, tenantB, idpA, ""},
		{"impersonation list", http.MethodGet, "/api/v1/system/tenants/" + tenantB + "/impersonations", nil, authn.ActionImpersonationRead, tenantB, "", ""},
		{"impersonation revoke", http.MethodDelete, "/api/v1/system/tenants/" + tenantB + "/impersonations/" + grantA, nil, authn.ActionImpersonationRevoke, tenantB, "acct-t2", "acct-t2"},
		{"session delete", http.MethodDelete, "/api/v1/system/auth/sessions/" + string(sessionA), nil, authn.ActionSessionRevoke, "", string(sessionA), "acct-t2"},
		{"account delete", http.MethodDelete, "/api/v1/system/auth/accounts/acct-t2", nil, authn.ActionAccountDelete, "", "acct-t2", "acct-t2"},
		{"provider delete", http.MethodDelete, "/api/v1/tenants/" + tenantB + "/sso/providers/" + idpA, nil, authn.ActionIDPManage, tenantB, idpA, ""},
	}
	accountObjects := map[string]struct{ action, object string }{
		"account patch":     {actionAccountUpdate, "account:acct-t2"},
		"account status":    {actionAccountDisable, "account:acct-t2"},
		"identifier add":    {actionAccountUpdate, "account:acct-t2"},
		"identifier remove": {actionAccountUpdate, "account:acct-t2"},
		"password reset":    {actionAccountAuthReset, "account:acct-t2"},
		"mfa required":      {actionAccountUpdate, "account:acct-t2"},
		"mfa reset":         {actionMFAReset, "account:acct-t2"},
		"secret create":     {actionServiceKeyCreate, "account:acct-t2"},
		"secret revoke":     {actionServiceKeyRevoke, "account:acct-t2"},
		"member put":        {actionMembershipSet, "account:acct-t2"},
		"sso exempt":        {actionMembershipSet, "account:acct-t2"},
		"member remove":     {actionMembershipRemove, "account:acct-t2"},
		"account delete":    {actionAccountDelete, "account:acct-t2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := len(r.audit.entries())
			rec := r.do(tc.method, tc.path, tc.body, fresh()...)
			if rec.Code < 200 || rec.Code >= 300 {
				t.Fatalf("status = %d, want a success; body %s", rec.Code, rec.Body.String())
			}
			if want, ok := accountObjects[tc.name]; ok {
				rows := r.audit.entries()[before:]
				found := false
				for _, entry := range rows {
					if entry.Action == want.action && entry.Object == want.object {
						found = true
					}
				}
				if !found {
					t.Errorf("the route wrote no %s row naming the account object %q: %+v",
						want.action, want.object, rows)
				}
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

// TestATenantMismatchIsRefusedBeforeTheDecider checks that a tenant mismatch is a 400 before the Decider is asked.
func TestATenantMismatchIsRefusedBeforeTheDecider(t *testing.T) {
	r := newRig(t)
	r.providers.put(authn.SSOProvider{
		ID: idpA, Tenant: tenantB, Protocol: authn.SSOProtocolSAML,
		Provisioning: authn.ProvisioningJIT,
	})

	rec := r.do(http.MethodPost, "/api/v1/tenants/"+tenantA+"/sso/providers",
		map[string]any{"tenant": tenantB, "protocol": "saml", "name": "x", "provisioning": "jit"}, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("create with a mismatched tenant: status = %d, want 400", rec.Code)
	}
	if r.decider.count() != 0 {
		t.Errorf("the Decider was asked %d times, want none", r.decider.count())
	}

	rec = r.do(http.MethodPut, "/api/v1/tenants/"+tenantB+"/sso/providers/"+idpA,
		map[string]any{"tenant": tenantA, "protocol": "saml", "name": "x", "provisioning": "jit"}, fresh()...)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("update changing the tenant: status = %d, want 400", rec.Code)
	}
	if r.decider.count() != 0 {
		t.Errorf("the Decider was asked %d times after a mismatch, want none", r.decider.count())
	}
}

// TestARecordAddressedUnderAnotherTenantIsNotFound checks that a provider or record addressed under a tenant it does not belong to is a 404.
func TestARecordAddressedUnderAnotherTenantIsNotFound(t *testing.T) {
	r := newRig(t)
	r.providers.put(authn.SSOProvider{
		ID: idpA, Tenant: tenantA, Protocol: authn.SSOProtocolSAML,
		Provisioning: authn.ProvisioningJIT,
	})
	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantB+"/sso/providers/"+idpA, nil, fresh()...)
	if rec.Code != http.StatusNotFound {
		t.Errorf("provider under another tenant: status = %d, want 404", rec.Code)
	}
	if r.decider.count() != 0 {
		t.Errorf("the Decider was asked for a provider of another tenant")
	}

	r = newRig(t)
	r.records.put(authn.ImpersonationGrant{ID: grantA, Actor: adminID, Target: userID, Tenant: tenantA})
	rec = r.do(http.MethodDelete, "/api/v1/system/tenants/"+tenantB+"/impersonations/"+grantA, nil, fresh()...)
	if rec.Code != http.StatusNotFound {
		t.Errorf("impersonation record under another tenant: status = %d, want 404", rec.Code)
	}
	if r.decider.count() != 0 {
		t.Errorf("the Decider was asked for an impersonation record of another tenant")
	}
}

// TestNoDeciderDeniesEveryRoute holds that an assembly with no Decider answers every management route with a 403 and never asks any capability.
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

// TestAnImpersonatingActorIsRefusedOnThePlatformDomain checks that an impersonating identity on a platform-domain handler is 403 before the Decider.
func TestAnImpersonatingActorIsRefusedOnThePlatformDomain(t *testing.T) {
	r := newRig(t)
	for _, rt := range allRoutes(r.p) {
		if rt.domain != platformDomain {
			continue
		}
		rec := r.invoke(rt.handler, rt.method, rt.path, rt.params, nil, impersonating()...)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s as an impersonating identity: status = %d, want 403", rt.name, rec.Code)
		}
	}
	if r.decider.count() != 0 {
		t.Errorf("the Decider was asked %d times for an impersonating identity, want none", r.decider.count())
	}
}

// TestAnImpersonatingActorIsAdmittedOnTheTenantDomain checks that an impersonating identity on a tenant-domain handler is not refused.
func TestAnImpersonatingActorIsAdmittedOnTheTenantDomain(t *testing.T) {
	r := newRig(t)
	for _, rt := range allRoutes(r.p) {
		if rt.domain != tenantDomain {
			continue
		}
		rec := r.invoke(rt.handler, rt.method, rt.path, rt.params, nil, impersonating()...)
		if rec.Code == http.StatusForbidden {
			t.Errorf("%s as an impersonating identity: status = %d, want the tenant domain's decision; body %s",
				rt.name, rec.Code, rec.Body.String())
		}
	}
}

// TestATenantDomainRouteDecidesAnImpersonatingActorByTheEffectiveAccount checks that an impersonating tenant-domain request is decided by the effective account.
func TestATenantDomainRouteDecidesAnImpersonatingActorByTheEffectiveAccount(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members", nil, impersonating()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("a tenant-domain member list as an impersonating identity: status = %d, want 200; body %s",
			rec.Code, rec.Body.String())
	}
	got, ok := r.decider.last()
	if !ok {
		t.Fatal("the Decider was not asked")
	}
	if got.Action != authn.ActionMembershipManage || got.Tenant != tenantA {
		t.Errorf("Decision = %+v, want membership.manage in %s", got, tenantA)
	}
	if !got.Actor.Impersonating() || got.Actor.Account() != userID || got.Actor.Actor() != adminID {
		t.Errorf("Decision.Actor = account %q actor %q impersonating %t; want the impersonated user decided through the operator",
			got.Actor.Account(), got.Actor.Actor(), got.Actor.Impersonating())
	}
}

// TestTenantDomainGroupsAreImpersonable checks that tenant-domain groups are registered Impersonable and platform-domain groups are not.
func TestTenantDomainGroupsAreImpersonable(t *testing.T) {
	r := newRig(t)
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"member list", "/api/v1/tenants/:tenant/members", true},
		{"member write", "/api/v1/tenants/:tenant/members/:account", true},
		{"member exempt", "/api/v1/tenants/:tenant/members/:account/sso-exempt", true},
		{"provider list", "/api/v1/tenants/:tenant/sso/providers", true},
		{"provider by id", "/api/v1/tenants/:tenant/sso/providers/:id", true},
		{"provider domains", "/api/v1/tenants/:tenant/sso/providers/:id/domains", true},
		{"provider domain", "/api/v1/tenants/:tenant/sso/providers/:id/domains/:domain", true},
		{"metadata refresh", "/api/v1/tenants/:tenant/sso/providers/:id/metadata/refresh", true},
		{"account list", "/api/v1/system/auth/accounts", false},
		{"account by id", "/api/v1/system/auth/accounts/:id", false},
		{"account sessions", "/api/v1/system/auth/accounts/:id/sessions", false},
		{"account secrets", "/api/v1/system/auth/accounts/:id/secrets", false},
		{"session revoke", "/api/v1/system/auth/sessions/:id", false},
		{"impersonation list", "/api/v1/system/tenants/:tenant/impersonations", false},
		{"impersonation revoke", "/api/v1/system/tenants/:tenant/impersonations/:id", false},
	}
	for _, tc := range cases {
		reg, ok := r.mounter.Registered(http.MethodGet, tc.path)
		if !ok {
			for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
				if reg, ok = r.mounter.Registered(method, tc.path); ok {
					break
				}
			}
		}
		if !ok {
			t.Errorf("%s: %s is not mounted", tc.name, tc.path)
			continue
		}
		if reg.Impersonable != tc.want {
			t.Errorf("%s: Impersonable = %t, want %t", tc.name, reg.Impersonable, tc.want)
		}
	}
}

// TestTheMandatoryAuditLandsBeforeTheWrite checks that a write whose mandatory audit fails is 503 and calls no capability.
func TestTheMandatoryAuditLandsBeforeTheWrite(t *testing.T) {
	r := newRig(t)
	r.audit.fails(audit.ErrWriteFailed)
	rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts",
		map[string]any{"kind": "user", "username": "alice"}, fresh()...)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("create with a failing audit: status = %d, want 503", rec.Code)
	}
	if r.accountAdm.count("create") != 0 {
		t.Error("Create was called though the mandatory audit failed")
	}

	r = newRig(t)
	r.audit.fails(audit.ErrWriteFailed)
	rec = r.do(http.MethodDelete, "/api/v1/system/auth/accounts/"+string(userID), nil, fresh()...)
	if rec.Code != http.StatusNoContent {
		t.Errorf("delete with a failing audit: status = %d, want 204", rec.Code)
	}
	if r.accountAdm.count("delete") != 1 {
		t.Error("Delete was not called: the deletion audit follows the commit")
	}

	r = newRig(t)
	r.accountAdm.err = errors.New("the deletion transaction rolled back")
	rec = r.do(http.MethodDelete, "/api/v1/system/auth/accounts/"+string(userID), nil, fresh()...)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a failed delete: status = %d, want 503", rec.Code)
	}
	var compensation bool
	for _, entry := range r.audit.entries() {
		if entry.Action == actionAccountDelete && entry.Outcome == outcomeFailed {
			compensation = true
		}
	}
	if !compensation {
		t.Errorf("a failed delete wrote no failed compensation row: %+v", r.audit.entries())
	}
}

// TestTheMandatoryAuditLandsBeforeASecretWrite checks that a service-secret write whose mandatory audit fails is 503 and calls no capability.
func TestTheMandatoryAuditLandsBeforeASecretWrite(t *testing.T) {
	r := newRig(t)
	r.audit.fails(audit.ErrWriteFailed)
	rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts/"+string(userID)+"/secrets",
		map[string]any{"label": "ci"}, fresh()...)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("secret create with a failing audit: status = %d, want 503", rec.Code)
	}
	if r.accountAdm.count("create-secret") != 0 {
		t.Error("CreateServiceSecret was called though the mandatory audit failed")
	}
}

// TestAStaticSubjectIsRefusedOnTheStrongRoutes checks that a static credential subject is refused 401 on the routes that need a seasoned administrator.
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
		{http.MethodPost, "/api/v1/system/auth/accounts/" + string(userID) + "/password", map[string]any{"password": "a-long-temporary-one"}},
		{http.MethodPost, "/api/v1/system/auth/accounts/" + string(userID) + "/mfa-reset", nil},
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

// TestADeciderThatFailsOrPanicsIsUnavailable holds that a returned error and a panic alike are a 503 and no action is carried out.
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
			rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts",
				map[string]any{"kind": "user", "username": "alice"}, fresh()...)
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 503", rec.Code)
			}
			if r.accountAdm.count("create") != 0 {
				t.Error("Create ran though the Decider failed")
			}
		})
	}

	r := newRig(t)
	r.p.decider = panicDecider{}
	rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts",
		map[string]any{"kind": "user", "username": "alice"}, fresh()...)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("panicking decider: status = %d, want 503", rec.Code)
	}
	if r.accountAdm.count("create") != 0 {
		t.Error("Create ran though the Decider panicked")
	}
}

type panicDecider struct{}

func (panicDecider) Decide(context.Context, authn.Decision) (bool, error) {
	panic("the decider panicked")
}

// TestARefusalIsForbiddenAndNothingHappens holds that a Decider which refuses is a 403 and the capability is not called.
func TestARefusalIsForbiddenAndNothingHappens(t *testing.T) {
	r := newRig(t)
	r.decider.allow = false
	rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts",
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

// TestACreateNeedsClientManageForAServiceAccount holds that a service account is asked about twice, and a user account once.
func TestACreateNeedsClientManageForAServiceAccount(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPost, "/api/v1/system/auth/accounts",
		map[string]any{"kind": "service", "username": "svc"}, fresh()...)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	calls := r.decider.calls
	if len(calls) != 2 || calls[0].Action != authn.ActionAccountCreate || calls[1].Action != authn.ActionClientManage {
		t.Errorf("a service-account create asked %+v, want account.create then client.manage", calls)
	}

	r2 := newRig(t)
	r2.do(http.MethodPost, "/api/v1/system/auth/accounts",
		map[string]any{"kind": "user", "username": "alice"}, fresh()...)
	if n := r2.decider.count(); n != 1 {
		t.Errorf("a user-account create asked the Decider %d times, want once", n)
	}
}

// TestTheImpersonationRecordGroup checks the record routes' tenant requirement, missing-record 404, best-effort revocation and impersonation refusal.
func TestTheImpersonationRecordGroup(t *testing.T) {
	r := newRig(t)
	r.records.put(authn.ImpersonationGrant{ID: grantA, Actor: adminID, Target: userID, Tenant: tenantB})
	r.records.put(authn.ImpersonationGrant{
		ID: "ended", Actor: adminID, Target: userID, Tenant: tenantB,
		EndedAt: time.Now(), EndReason: authn.EndExplicit,
	})

	rec := r.do(http.MethodGet, "/api/v1/system/tenants/"+tenantB+"/impersonations", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200", rec.Code)
	}

	missing := newRig(t)
	rec = missing.do(http.MethodDelete, "/api/v1/system/tenants/"+tenantB+"/impersonations/absent", nil, fresh()...)
	if rec.Code != http.StatusNotFound {
		t.Errorf("revoke of a missing record: status = %d, want 404", rec.Code)
	}
	if missing.decider.count() != 0 {
		t.Error("the Decider was asked for a record that is not there")
	}

	r.audit.fails(audit.ErrWriteFailed)
	rec = r.do(http.MethodDelete, "/api/v1/system/tenants/"+tenantB+"/impersonations/"+grantA, nil, fresh()...)
	if rec.Code != http.StatusNoContent {
		t.Errorf("revoke with a failing audit: status = %d, want 204", rec.Code)
	}
	if r.records.revokes != 1 {
		t.Errorf("the record was revoked %d times, want once", r.records.revokes)
	}
	rec = r.do(http.MethodDelete, "/api/v1/system/tenants/"+tenantB+"/impersonations/ended", nil, fresh()...)
	if rec.Code != http.StatusNoContent {
		t.Errorf("revoke of an ended record: status = %d, want 204", rec.Code)
	}
}

// TestRouteGroupsFollowTheirCapabilities holds that a group whose capability is absent is not mounted, and that the others are unaffected.
func TestRouteGroupsFollowTheirCapabilities(t *testing.T) {
	cases := []struct {
		omit    leaveOut
		absent  [][2]string
		present [][2]string
	}{
		{
			omit:    noSessions,
			absent:  [][2]string{{http.MethodGet, "/api/v1/system/auth/accounts/:id/sessions"}, {http.MethodDelete, "/api/v1/system/auth/sessions/:id"}},
			present: [][2]string{{http.MethodGet, "/api/v1/system/auth/accounts/:id/factors"}},
		},
		{
			omit:    noMFA,
			absent:  [][2]string{{http.MethodGet, "/api/v1/system/auth/accounts/:id/factors"}, {http.MethodPost, "/api/v1/system/auth/accounts/:id/password"}},
			present: [][2]string{{http.MethodGet, "/api/v1/system/auth/accounts/:id/sessions"}},
		},
		{
			omit:    noTokens,
			absent:  [][2]string{{http.MethodGet, "/api/v1/system/auth/accounts/:id/secrets"}},
			present: [][2]string{{http.MethodGet, "/api/v1/system/auth/accounts/:id/sessions"}},
		},
		{
			omit:    noProviders,
			absent:  [][2]string{{http.MethodGet, "/api/v1/tenants/:tenant/sso/providers/:id"}, {http.MethodPost, "/api/v1/tenants/:tenant/sso/providers/:id/metadata/refresh"}},
			present: [][2]string{{http.MethodPost, "/api/v1/system/auth/accounts/:id/revoke-sessions"}},
		},
		{
			omit:    noProviderDomains,
			absent:  [][2]string{{http.MethodGet, "/api/v1/tenants/:tenant/sso/providers/:id"}, {http.MethodPost, "/api/v1/tenants/:tenant/sso/providers/:id/domains"}},
			present: [][2]string{{http.MethodPost, "/api/v1/system/auth/accounts/:id/revoke-sessions"}},
		},
		{
			omit:    noRefresher,
			absent:  [][2]string{{http.MethodPost, "/api/v1/tenants/:tenant/sso/providers/:id/metadata/refresh"}},
			present: [][2]string{{http.MethodGet, "/api/v1/tenants/:tenant/sso/providers/:id"}},
		},
		{
			omit:    noRecords,
			absent:  [][2]string{{http.MethodGet, "/api/v1/system/tenants/:tenant/impersonations"}},
			present: [][2]string{{http.MethodGet, "/api/v1/system/auth/accounts"}},
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

// TestTheAccountAndMembershipGroupsAreAlwaysMounted holds that removing every optional capability leaves the required groups in place.
func TestTheAccountAndMembershipGroupsAreAlwaysMounted(t *testing.T) {
	r := newRig(t, noSessions, noMFA, noTokens, noProviders, noRefresher, noRecords)
	for _, want := range [][2]string{
		{http.MethodGet, "/api/v1/system/auth/accounts"},
		{http.MethodDelete, "/api/v1/system/auth/accounts/:id"},
		{http.MethodGet, "/api/v1/tenants/:tenant/members"},
		{http.MethodPut, "/api/v1/tenants/:tenant/members/:account"},
	} {
		if _, ok := r.mounter.Registered(want[0], want[1]); !ok {
			t.Errorf("%s %s is not mounted", want[0], want[1])
		}
	}
}

// TestAnUnseasonedActorIsForbiddenOnTheResets checks that a missing seasoned factor is 403 and a weak authentication is 401 on the two resets.
func TestAnUnseasonedActorIsForbiddenOnTheResets(t *testing.T) {
	routes := []struct {
		name string
		path string
		body any
	}{
		{"factor reset", "/api/v1/system/auth/accounts/" + string(userID) + "/mfa-reset", nil},
		{"password reset", "/api/v1/system/auth/accounts/" + string(userID) + "/password", map[string]any{"password": "a-long-temporary-one"}},
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

// TestAStrengthShortfallIsAStepUp checks that a stale authentication is refused 401 on the routes that ask for a fresh one.
func TestAStrengthShortfallIsAStepUp(t *testing.T) {
	r := newRig(t)
	stale := append(fresh(), authntest.WithAuthTime(time.Now().Add(-time.Hour)))
	rec := r.do(http.MethodDelete, "/api/v1/system/auth/accounts/"+string(userID), nil, stale...)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("stale authentication: status = %d, want 401", rec.Code)
	}
	if r.accountAdm.count("delete") != 0 {
		t.Error("Delete ran though the actor was not strong enough")
	}
}
