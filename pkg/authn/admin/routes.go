package admin

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// guardedIn is the group's registration primitive: every route of the group is
// guarded and published under the group's scope, so a group's level is written
// once rather than beside each route.
func guardedIn(m mount.Mounter, scope speedhttp.Scope) func(speedhttp.Declared, string, gin.HandlerFunc, ...mount.Option) {
	return func(operation speedhttp.Declared, rel string, h gin.HandlerFunc, opts ...mount.Option) {
		mount.Declared(m, operation, rel, h, append(opts, mount.InScope(scope))...)
	}
}

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
			mount.Declared(m.mounter, metadataRefreshOperation, mountRel(metadataRefreshOperation.Path),
				metadataRefreshOperation.Handle(m.handleMetadataRefresh), mount.InScope(speedhttp.ScopeTenants))
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
	g := guardedIn(m.mounter, speedhttp.ScopeSystem)
	g(accountListOperation, mountRel(accountListOperation.Path),
		closedQuery(accountListParams, accountListOperation.Handle(m.handleAccountList)))
	g(accountCreateOperation, mountRel(accountCreateOperation.Path),
		mount.GuardBody(nil, accountCreateOperation.Handle(m.handleAccountCreate)))
	g(accountGetOperation, mountRel(accountGetOperation.Path), accountGetOperation.Handle(m.handleAccountGet))
	g(accountPatchOperation, mountRel(accountPatchOperation.Path),
		mount.GuardBody(nil, accountPatchOperation.Handle(m.handleAccountPatch)))
	g(accountDeleteOperation, mountRel(accountDeleteOperation.Path), accountDeleteOperation.Handle(m.handleAccountDelete))
	g(accountStatusOperation, mountRel(accountStatusOperation.Path),
		mount.GuardBody(nil, accountStatusOperation.Handle(m.handleAccountStatus)))
	g(identifierAddOperation, mountRel(identifierAddOperation.Path),
		mount.GuardBody(nil, identifierAddOperation.Handle(m.handleIdentifierAdd)))
	g(identifierRemoveOperation, mountRel(identifierRemoveOperation.Path), identifierRemoveOperation.Handle(m.handleIdentifierRemove))
	m.groups = append(m.groups, "accounts")

	if m.mfa != nil {
		if m.passwords != nil {
			g(passwordResetOperation, mountRel(passwordResetOperation.Path),
				mount.GuardBody(nil, passwordResetOperation.Handle(m.handlePasswordReset)))
		}
		g(factorListOperation, mountRel(factorListOperation.Path), factorListOperation.Handle(m.handleFactorList))
		g(mfaFlagOperation, mountRel(mfaFlagOperation.Path),
			mount.GuardBody(nil, mfaFlagOperation.Handle(m.handleMFARequired)))
		g(mfaResetOperation, mountRel(mfaResetOperation.Path), mfaResetOperation.Handle(m.handleMFAReset))
		m.postFeature(featureFactors)
	}
}

// mountSessionRoutes registers the session group, mounted with the session
// store.
func (m *product) mountSessionRoutes() {
	g := guardedIn(m.mounter, speedhttp.ScopeSystem)
	g(accountSessionsOperation, mountRel(accountSessionsOperation.Path), accountSessionsOperation.Handle(m.handleAccountSessions))
	g(revokeSessionsOperation, mountRel(revokeSessionsOperation.Path), revokeSessionsOperation.Handle(m.handleRevokeAccountSessions))
	g(sessionRevokeOperation, mountRel(sessionRevokeOperation.Path), sessionRevokeOperation.Handle(m.handleSessionRevoke))
}

// mountSecretRoutes registers the service-secret group, mounted with the token
// module.
func (m *product) mountSecretRoutes() {
	g := guardedIn(m.mounter, speedhttp.ScopeSystem)
	g(secretListOperation, mountRel(secretListOperation.Path), secretListOperation.Handle(m.handleSecretList))
	g(secretCreateOperation, mountRel(secretCreateOperation.Path),
		mount.GuardBody(nil, secretCreateOperation.Handle(m.handleSecretCreate)))
	g(secretRevokeOperation, mountRel(secretRevokeOperation.Path), secretRevokeOperation.Handle(m.handleSecretRevoke))
}

// mountMembershipRoutes registers the membership group, which is always mounted:
// the membership reads are required.
func (m *product) mountMembershipRoutes() {
	g := guardedIn(m.mounter, speedhttp.ScopeTenants)
	g(memberListOperation, mountRel(memberListOperation.Path), memberListOperation.Handle(m.handleMemberList))
	g(memberWriteOperation, mountRel(memberWriteOperation.Path),
		mount.GuardBody(nil, memberWriteOperation.Handle(m.handleMemberPut)))
	g(memberRemoveOperation, mountRel(memberRemoveOperation.Path), memberRemoveOperation.Handle(m.handleMemberRemove))
	g(memberExemptOperation, mountRel(memberExemptOperation.Path),
		mount.GuardBody(nil, memberExemptOperation.Handle(m.handleMemberSSOExempt)))
	m.groups = append(m.groups, "memberships")
}

// mountSSORoutes registers the SSO provider group, mounted with the SSO module.
func (m *product) mountSSORoutes() {
	g := guardedIn(m.mounter, speedhttp.ScopeTenants)
	g(providerListOperation, mountRel(providerListOperation.Path), providerListOperation.Handle(m.handleProviderList))
	g(providerCreateOperation, mountRel(providerCreateOperation.Path),
		mount.GuardBody(nil, providerCreateOperation.Handle(m.handleProviderCreate)))
	g(providerGetOperation, mountRel(providerGetOperation.Path), providerGetOperation.Handle(m.handleProviderGet))
	g(providerUpdateOperation, mountRel(providerUpdateOperation.Path),
		mount.GuardBody(nil, providerUpdateOperation.Handle(m.handleProviderUpdate)))
	g(providerDeleteOperation, mountRel(providerDeleteOperation.Path), providerDeleteOperation.Handle(m.handleProviderDelete))
	g(domainClaimOperation, mountRel(domainClaimOperation.Path),
		mount.GuardBody(nil, domainClaimOperation.Handle(m.handleDomainClaim)))
	g(domainRemoveOperation, mountRel(domainRemoveOperation.Path), domainRemoveOperation.Handle(m.handleDomainRemove))
	g(domainVerifyOperation, mountRel(domainVerifyOperation.Path), domainVerifyOperation.Handle(m.handleDomainVerify))
	g(reverifyDomainsOperation, mountRel(reverifyDomainsOperation.Path), reverifyDomainsOperation.Handle(m.handleReverifyDomains))
}

// mountImpersonationRoutes registers the impersonation record group, mounted
// with an impersonation capability that answers the extended shape the routes
// read a record through.
func (m *product) mountImpersonationRoutes() {
	g := guardedIn(m.mounter, speedhttp.ScopeTenants)
	g(impersonationListOperation, mountRel(impersonationListOperation.Path), impersonationListOperation.Handle(m.handleImpersonationList))
	g(impersonationRevokeOperation, mountRel(impersonationRevokeOperation.Path), impersonationRevokeOperation.Handle(m.handleImpersonationRevoke))
}

// closedQuery refuses a query parameter outside the accepted set before the
// bound handler reads the request. The account list's query set is closed by
// contract — a misspelled filter is refused rather than silently ignored — and
// the entry point's binder reads only the names the declaration carries, so
// this guard is the module's own check of the same set. It is a route wrapper,
// not a handler: the handler still receives only its declared input.
func closedQuery(accepted map[string]bool, next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		for key := range c.Request.URL.Query() {
			if !accepted[key] {
				mount.Fail(c, mount.CodeBadRequest)
				return
			}
		}
		next(c)
	}
}

// mountRel turns a declared route into the relative path the mounter joins to
// its base. The declaration writes the route against the module's API root, and
// the fragment is a static declaration fixed before configuration, so the mounter
// path is derived from the scope's default base rather than the configured one: a
// configured base moves the routes without moving the contract, which is the
// split the Spec already accepts. The scope follows from the declaration: a
// route that names the tenant first is the tenant-domain surface, every other
// route is the platform-domain one. A declared route that is not under its base
// is a declaration defect, not something to mount at a guessed path.
func mountRel(declared string) string {
	spec, scope := adminSpec, speedhttp.ScopeSystem
	if strings.HasPrefix(declared, "/:tenant/") {
		spec, scope = adminTenantsSpec, speedhttp.ScopeTenants
	}
	base := mount.DefaultBaseFor(scope)
	relative, err := mount.RelativePath(base, spec.ResolvePath(declared))
	if err != nil {
		panic(diagnosticPrefix + ModuleName + ": the declared route " + declared +
			" is not under the capability's mounted path " + base)
	}
	return relative
}
