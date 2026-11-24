package admin

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func guardedIn(m mount.Mounter, scope speedhttp.Scope) func(speedhttp.Declared, string, gin.HandlerFunc, ...mount.Option) {
	return func(operation speedhttp.Declared, rel string, h gin.HandlerFunc, opts ...mount.Option) {
		mount.Declared(m, operation, rel, h, append(opts, mount.InScope(scope))...)
	}
}

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
	if m.ssoProviders != nil && m.domainClaims != nil && m.ssoGroupEnabled {
		m.mountSSORoutes()
		m.groups = append(m.groups, "sso-providers")
		m.postFeature(featureSSO)
		if len(m.refreshers) > 0 {
			mount.Declared(m.mounter, metadataRefreshOperation, mountRel(metadataRefreshOperation.Path),
				metadataRefreshOperation.Handle(m.handleMetadataRefresh), mount.InScope(speedhttp.ScopeTenants), mount.Impersonable())
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

func (m *product) mountSessionRoutes() {
	g := guardedIn(m.mounter, speedhttp.ScopeSystem)
	g(accountSessionsOperation, mountRel(accountSessionsOperation.Path), accountSessionsOperation.Handle(m.handleAccountSessions))
	g(revokeSessionsOperation, mountRel(revokeSessionsOperation.Path), revokeSessionsOperation.Handle(m.handleRevokeAccountSessions))
	g(sessionRevokeOperation, mountRel(sessionRevokeOperation.Path), sessionRevokeOperation.Handle(m.handleSessionRevoke))
}

func (m *product) mountSecretRoutes() {
	g := guardedIn(m.mounter, speedhttp.ScopeSystem)
	g(secretListOperation, mountRel(secretListOperation.Path), secretListOperation.Handle(m.handleSecretList))
	g(secretCreateOperation, mountRel(secretCreateOperation.Path),
		mount.GuardBody(nil, secretCreateOperation.Handle(m.handleSecretCreate)))
	g(secretRevokeOperation, mountRel(secretRevokeOperation.Path), secretRevokeOperation.Handle(m.handleSecretRevoke))
}

func (m *product) mountMembershipRoutes() {
	g := guardedIn(m.mounter, speedhttp.ScopeTenants)
	g(memberListOperation, mountRel(memberListOperation.Path), memberListOperation.Handle(m.handleMemberList), mount.Impersonable())
	g(memberWriteOperation, mountRel(memberWriteOperation.Path),
		mount.GuardBody(nil, memberWriteOperation.Handle(m.handleMemberPut)), mount.Impersonable())
	g(memberRemoveOperation, mountRel(memberRemoveOperation.Path), memberRemoveOperation.Handle(m.handleMemberRemove), mount.Impersonable())
	g(memberExemptOperation, mountRel(memberExemptOperation.Path),
		mount.GuardBody(nil, memberExemptOperation.Handle(m.handleMemberSSOExempt)), mount.Impersonable())
	m.groups = append(m.groups, "memberships")
}

func (m *product) mountSSORoutes() {
	g := guardedIn(m.mounter, speedhttp.ScopeTenants)
	g(providerListOperation, mountRel(providerListOperation.Path), providerListOperation.Handle(m.handleProviderList), mount.Impersonable())
	g(providerCreateOperation, mountRel(providerCreateOperation.Path),
		mount.GuardBody(nil, providerCreateOperation.Handle(m.handleProviderCreate)), mount.Impersonable())
	g(providerGetOperation, mountRel(providerGetOperation.Path), providerGetOperation.Handle(m.handleProviderGet), mount.Impersonable())
	g(providerUpdateOperation, mountRel(providerUpdateOperation.Path),
		mount.GuardBody(nil, providerUpdateOperation.Handle(m.handleProviderUpdate)), mount.Impersonable())
	g(providerDeleteOperation, mountRel(providerDeleteOperation.Path), providerDeleteOperation.Handle(m.handleProviderDelete), mount.Impersonable())
	g(domainClaimOperation, mountRel(domainClaimOperation.Path),
		mount.GuardBody(nil, domainClaimOperation.Handle(m.handleDomainClaim)), mount.Impersonable())
	g(domainRemoveOperation, mountRel(domainRemoveOperation.Path), domainRemoveOperation.Handle(m.handleDomainRemove), mount.Impersonable())
	g(domainVerifyOperation, mountRel(domainVerifyOperation.Path), domainVerifyOperation.Handle(m.handleDomainVerify), mount.Impersonable())
	g(reverifyDomainsOperation, mountRel(reverifyDomainsOperation.Path), reverifyDomainsOperation.Handle(m.handleReverifyDomains), mount.Impersonable())
}

func (m *product) mountImpersonationRoutes() {
	g := guardedIn(m.mounter, speedhttp.ScopeSystem)
	g(impersonationListOperation, mountRel(impersonationListOperation.Path), impersonationListOperation.Handle(m.handleImpersonationList))
	g(impersonationRevokeOperation, mountRel(impersonationRevokeOperation.Path), impersonationRevokeOperation.Handle(m.handleImpersonationRevoke))
}

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
