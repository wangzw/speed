package admin

import (
	"net/http"
)

// mountRoutes registers every management route whose group has its capability.
// All are guarded by the gateway and none is registered impersonable: the
// gateway answers an impersonating identity with a 401, and the handler checks
// the same condition again. The account and membership groups are always
// mounted; each other group follows the capability it needs.
func (m *product) mountRoutes() {
	m.mountAccountRoutes()
	m.mountMembershipRoutes()
	if m.sessions != nil {
		m.mountSessionRoutes()
		m.groups = append(m.groups, "sessions")
		m.postFeature(featureSessions)
	}
	if m.mfa != nil {
		m.groups = append(m.groups, "factors")
	}
	if m.tokens != nil {
		m.mountSecretRoutes()
		m.groups = append(m.groups, "secrets")
		m.postFeature(featureServiceKeys)
	}
	if m.ssoProviders != nil {
		m.mountSSORoutes()
		m.groups = append(m.groups, "sso-providers")
		m.postFeature(featureSSO)
		if len(m.refreshers) > 0 {
			m.mounter.Guarded(http.MethodPost, "/admin/sso/providers/:id/metadata/refresh", m.handleMetadataRefresh)
			m.groups = append(m.groups, "sso-metadata-refresh")
			m.postFeature(featureSSOMetadata)
		}
	}
	if m.records != nil {
		m.mountImpersonationRoutes()
		m.groups = append(m.groups, "impersonations")
		m.postFeature(featureImpersonations)
	}
}

// mountAccountRoutes registers the account group, which is always mounted: the
// account read and write surfaces are required.
func (m *product) mountAccountRoutes() {
	g := m.mounter.Guarded
	g(http.MethodGet, "/admin/accounts", m.handleAccountList)
	g(http.MethodPost, "/admin/accounts", m.handleAccountCreate)
	g(http.MethodGet, "/admin/accounts/:id", m.handleAccountGet)
	g(http.MethodPatch, "/admin/accounts/:id", m.handleAccountPatch)
	g(http.MethodDelete, "/admin/accounts/:id", m.handleAccountDelete)
	g(http.MethodPost, "/admin/accounts/:id/status", m.handleAccountStatus)
	g(http.MethodPost, "/admin/accounts/:id/identifiers", m.handleIdentifierAdd)
	g(http.MethodDelete, "/admin/accounts/:id/identifiers/:identifier", m.handleIdentifierRemove)
	m.groups = append(m.groups, "accounts")

	if m.mfa != nil {
		if m.passwords != nil {
			g(http.MethodPost, "/admin/accounts/:id/password", m.handlePasswordReset)
		}
		g(http.MethodGet, "/admin/accounts/:id/factors", m.handleFactorList)
		g(http.MethodPost, "/admin/accounts/:id/mfa-required", m.handleMFARequired)
		g(http.MethodPost, "/admin/accounts/:id/mfa-reset", m.handleMFAReset)
		m.postFeature(featureFactors)
	}
}

// mountSessionRoutes registers the session group, mounted with the session
// store.
func (m *product) mountSessionRoutes() {
	g := m.mounter.Guarded
	g(http.MethodGet, "/admin/accounts/:id/sessions", m.handleAccountSessions)
	g(http.MethodPost, "/admin/accounts/:id/revoke-sessions", m.handleRevokeAccountSessions)
	g(http.MethodDelete, "/admin/sessions/:id", m.handleSessionRevoke)
}

// mountSecretRoutes registers the service-secret group, mounted with the token
// module.
func (m *product) mountSecretRoutes() {
	g := m.mounter.Guarded
	g(http.MethodGet, "/admin/accounts/:id/secrets", m.handleSecretList)
	g(http.MethodPost, "/admin/accounts/:id/secrets", m.handleSecretCreate)
	g(http.MethodDelete, "/admin/accounts/:id/secrets/:secret", m.handleSecretRevoke)
}

// mountMembershipRoutes registers the membership group, which is always mounted:
// the membership reads are required.
func (m *product) mountMembershipRoutes() {
	g := m.mounter.Guarded
	g(http.MethodGet, "/admin/tenants/:tenant/members", m.handleMemberList)
	g(http.MethodPut, "/admin/tenants/:tenant/members/:account", m.handleMemberPut)
	g(http.MethodDelete, "/admin/tenants/:tenant/members/:account", m.handleMemberRemove)
	g(http.MethodPut, "/admin/tenants/:tenant/members/:account/sso-exempt", m.handleMemberSSOExempt)
	m.groups = append(m.groups, "memberships")
}

// mountSSORoutes registers the SSO provider group, mounted with the SSO module.
func (m *product) mountSSORoutes() {
	g := m.mounter.Guarded
	g(http.MethodGet, "/admin/tenants/:tenant/sso/providers", m.handleProviderList)
	g(http.MethodPost, "/admin/tenants/:tenant/sso/providers", m.handleProviderCreate)
	g(http.MethodGet, "/admin/sso/providers/:id", m.handleProviderGet)
	g(http.MethodPut, "/admin/sso/providers/:id", m.handleProviderUpdate)
	g(http.MethodDelete, "/admin/sso/providers/:id", m.handleProviderDelete)
	g(http.MethodPost, "/admin/sso/providers/:id/domains", m.handleDomainClaim)
	g(http.MethodDelete, "/admin/sso/providers/:id/domains/:domain", m.handleDomainRemove)
	g(http.MethodPost, "/admin/sso/providers/:id/domains/:domain/verify", m.handleDomainVerify)
	g(http.MethodPost, "/admin/sso/providers/:id/reverify-domains", m.handleReverifyDomains)
}

// mountImpersonationRoutes registers the impersonation record group, mounted
// with an impersonation capability that answers the extended shape the routes
// read a record through.
func (m *product) mountImpersonationRoutes() {
	g := m.mounter.Guarded
	g(http.MethodGet, "/admin/impersonations", m.handleImpersonationList)
	g(http.MethodDelete, "/admin/impersonations/:id", m.handleImpersonationRevoke)
}
