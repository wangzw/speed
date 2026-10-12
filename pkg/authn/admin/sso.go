package admin

import (
	"encoding/json"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// providerView is one SSO provider as the management routes show it. The
// encrypted client secret never appears; the warnings name the settings that
// loosen a default, so an administrator sees them on the response that set them.
type providerView struct {
	ID                string          `json:"id"`
	Tenant            string          `json:"tenant"`
	Protocol          string          `json:"protocol"`
	Name              string          `json:"name"`
	Enabled           bool            `json:"enabled"`
	Provisioning      string          `json:"provisioning"`
	TrustIDPMFA       bool            `json:"trust_idp_mfa"`
	AllowIDPInitiated bool            `json:"allow_idp_initiated"`
	AllowSHA1         bool            `json:"allow_sha1"`
	MergeExisting     bool            `json:"merge_existing"`
	Config            json.RawMessage `json:"config,omitempty"`
	Version           int             `json:"version"`
	Warnings          []string        `json:"warnings,omitempty"`
}

func providerViewOf(p authn.SSOProvider) providerView {
	return providerView{
		ID:                p.ID,
		Tenant:            p.Tenant,
		Protocol:          p.Protocol,
		Name:              p.Name,
		Enabled:           p.Enabled,
		Provisioning:      p.Provisioning,
		TrustIDPMFA:       p.TrustIDPMFA,
		AllowIDPInitiated: p.AllowIDPInitiated,
		AllowSHA1:         p.AllowSHA1,
		MergeExisting:     p.MergeExisting,
		Config:            p.Config,
		Version:           p.Version,
		Warnings:          providerWarnings(p),
	}
}

// providerWarnings names each setting of a provider that loosens a default:
// IdP-initiated sign-in, merging an existing account, and accepting SHA-1.
func providerWarnings(p authn.SSOProvider) []string {
	var out []string
	if p.AllowIDPInitiated {
		out = append(out, "idp-initiated sign-in is enabled, which cannot defend against login CSRF")
	}
	if p.MergeExisting {
		out = append(out, "just-in-time sign-up may merge into an existing account")
	}
	if p.AllowSHA1 {
		out = append(out, "SHA-1 signed assertions are accepted")
	}
	return out
}

// providerBody is the request of a provider write.
type providerBody struct {
	Tenant            string          `json:"tenant"`
	Protocol          string          `json:"protocol"`
	Name              string          `json:"name"`
	Enabled           *bool           `json:"enabled"`
	Provisioning      string          `json:"provisioning"`
	TrustIDPMFA       *bool           `json:"trust_idp_mfa"`
	AllowIDPInitiated *bool           `json:"allow_idp_initiated"`
	AllowSHA1         *bool           `json:"allow_sha1"`
	MergeExisting     *bool           `json:"merge_existing"`
	Config            json.RawMessage `json:"config"`
	ClientSecret      *string         `json:"client_secret"`
}

// provider builds the provider value a write carries. It fills the boolean
// fields the body left out from the base, which is the stored provider for an
// update and the zero value for a create.
func (b providerBody) provider(id, tenant string, base authn.SSOProvider) authn.SSOProvider {
	p := authn.SSOProvider{
		ID:                id,
		Tenant:            tenant,
		Protocol:          b.Protocol,
		Name:              b.Name,
		Provisioning:      b.Provisioning,
		Config:            b.Config,
		Enabled:           base.Enabled,
		TrustIDPMFA:       base.TrustIDPMFA,
		AllowIDPInitiated: base.AllowIDPInitiated,
		AllowSHA1:         base.AllowSHA1,
		MergeExisting:     base.MergeExisting,
	}
	if b.Enabled != nil {
		p.Enabled = *b.Enabled
	}
	if b.TrustIDPMFA != nil {
		p.TrustIDPMFA = *b.TrustIDPMFA
	}
	if b.AllowIDPInitiated != nil {
		p.AllowIDPInitiated = *b.AllowIDPInitiated
	}
	if b.AllowSHA1 != nil {
		p.AllowSHA1 = *b.AllowSHA1
	}
	if b.MergeExisting != nil {
		p.MergeExisting = *b.MergeExisting
	}
	return p
}

// handleProviderList answers GET /admin/tenants/:tenant/sso/providers. The
// action is idp.manage.
func (m *product) handleProviderList(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	tenant := c.Param("tenant")
	if !m.asks(c, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: tenant}) {
		return
	}
	providers, err := m.ssoProviders.List(c.Request.Context(), tenant)
	if err != nil {
		m.cause(c, "listing providers", err)
		return
	}
	views := make([]providerView, len(providers))
	for i, p := range providers {
		views[i] = providerViewOf(p)
	}
	c.JSON(200, gin.H{"providers": views})
}

// handleProviderCreate answers POST /admin/tenants/:tenant/sso/providers. The
// path and the body must agree on the tenant. The action is idp.manage.
func (m *product) handleProviderCreate(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	tenant := c.Param("tenant")
	var body providerBody
	if !mount.Decode(c, &body) {
		return
	}
	if body.Tenant != "" && body.Tenant != tenant {
		m.bad(c)
		return
	}
	if body.Protocol == "" || body.Name == "" || body.Provisioning == "" {
		m.bad(c)
		return
	}
	if !m.asks(c, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: tenant}) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionIDPPut, "",
		map[string]string{detailActionID: actionID, detailTenant: tenant})) {
		return
	}
	provider, err := m.ssoProviders.Put(c.Request.Context(), body.provider("", tenant, authn.SSOProvider{}), body.ClientSecret)
	if err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionIDPPut, "", actionID))
		m.answer(c, "creating a provider", err)
		return
	}
	c.JSON(201, gin.H{"provider": providerViewOf(provider)})
}

// handleProviderGet answers GET /admin/sso/providers/:id. The decision's tenant
// is the provider's own, resolved from the store before the Decider is asked.
func (m *product) handleProviderGet(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	p, err := m.ssoProviders.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		m.answer(c, "reading a provider", err)
		return
	}
	if !m.asks(c, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: p.Tenant, Target: p.ID}) {
		return
	}
	c.JSON(200, gin.H{"provider": providerViewOf(p)})
}

// handleProviderUpdate answers PUT /admin/sso/providers/:id. The tenant is the
// provider's own and is immutable: a body that names another is refused. The
// action is idp.manage.
func (m *product) handleProviderUpdate(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	idpID := c.Param("id")
	existing, err := m.ssoProviders.Get(c.Request.Context(), idpID)
	if err != nil {
		m.answer(c, "reading a provider", err)
		return
	}
	var body providerBody
	if !mount.Decode(c, &body) {
		return
	}
	if body.Tenant != "" && body.Tenant != existing.Tenant {
		m.bad(c)
		return
	}
	if !m.asks(c, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: existing.Tenant, Target: idpID}) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionIDPPut, idpID,
		map[string]string{detailActionID: actionID, detailTenant: existing.Tenant})) {
		return
	}
	provider, err := m.ssoProviders.Put(c.Request.Context(), body.provider(idpID, existing.Tenant, existing), body.ClientSecret)
	if err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionIDPPut, idpID, actionID))
		m.answer(c, "updating a provider", err)
		return
	}
	c.JSON(200, gin.H{"provider": providerViewOf(provider)})
}

// handleProviderDelete answers DELETE /admin/sso/providers/:id. The action is
// idp.manage.
func (m *product) handleProviderDelete(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	idpID := c.Param("id")
	p, err := m.ssoProviders.Get(c.Request.Context(), idpID)
	if err != nil {
		m.answer(c, "reading a provider", err)
		return
	}
	if !m.asks(c, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: p.Tenant, Target: idpID}) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionIDPDelete, idpID,
		map[string]string{detailActionID: actionID, detailTenant: p.Tenant})) {
		return
	}
	if err := m.ssoProviders.Delete(c.Request.Context(), idpID); err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionIDPDelete, idpID, actionID))
		m.answer(c, "deleting a provider", err)
		return
	}
	c.Status(204)
	c.Writer.WriteHeaderNow()
}

// domainBody is the request of a domain claim.
type domainBody struct {
	Domain string `json:"domain"`
}

// handleDomainClaim answers POST /admin/sso/providers/:id/domains and returns
// the token to publish in DNS. The action is idp.manage.
func (m *product) handleDomainClaim(c *gin.Context) {
	id, provider, ok := m.providerFor(c)
	if !ok {
		return
	}
	var body domainBody
	if !mount.Decode(c, &body) {
		return
	}
	if body.Domain == "" {
		m.bad(c)
		return
	}
	if !m.asks(c, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: provider.Tenant, Target: provider.ID}) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionIDPDomain, provider.ID,
		map[string]string{detailActionID: actionID, detailChange: "claim"})) {
		return
	}
	token, err := m.ssoProviders.ClaimDomain(c.Request.Context(), provider.ID, body.Domain)
	if err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionIDPDomain, provider.ID, actionID))
		m.answer(c, "claiming a domain", err)
		return
	}
	c.JSON(200, gin.H{"verify_token": token})
}

// handleDomainRemove answers DELETE /admin/sso/providers/:id/domains/:domain.
// The action is idp.manage.
func (m *product) handleDomainRemove(c *gin.Context) {
	m.changeDomain(c, "remove", func(idpID, domain string) error {
		return m.ssoProviders.RemoveDomain(c.Request.Context(), idpID, domain)
	})
}

// handleDomainVerify answers POST
// /admin/sso/providers/:id/domains/:domain/verify. The action is idp.manage.
func (m *product) handleDomainVerify(c *gin.Context) {
	m.changeDomain(c, "verify", func(idpID, domain string) error {
		return m.ssoProviders.VerifyDomain(c.Request.Context(), idpID, domain)
	})
}

// handleReverifyDomains answers POST /admin/sso/providers/:id/reverify-domains.
// The action is idp.manage.
func (m *product) handleReverifyDomains(c *gin.Context) {
	id, provider, ok := m.providerFor(c)
	if !ok {
		return
	}
	if !m.asks(c, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: provider.Tenant, Target: provider.ID}) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionIDPDomain, provider.ID,
		map[string]string{detailActionID: actionID, detailChange: "reverify"})) {
		return
	}
	if err := m.ssoProviders.ReverifyDomains(c.Request.Context(), provider.ID); err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionIDPDomain, provider.ID, actionID))
		m.answer(c, "reverifying domains", err)
		return
	}
	c.Status(204)
	c.Writer.WriteHeaderNow()
}

// changeDomain runs a domain write the same way for the remove and verify
// routes: resolve the provider, decide, write the mandatory audit, call the
// capability.
func (m *product) changeDomain(c *gin.Context, change string, act func(idpID, domain string) error) {
	id, provider, ok := m.providerFor(c)
	if !ok {
		return
	}
	if !m.asks(c, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: provider.Tenant, Target: provider.ID}) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionIDPDomain, provider.ID,
		map[string]string{detailActionID: actionID, detailChange: change})) {
		return
	}
	if err := act(provider.ID, c.Param("domain")); err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionIDPDomain, provider.ID, actionID))
		m.answer(c, "writing a domain", err)
		return
	}
	c.Status(204)
	c.Writer.WriteHeaderNow()
}

// providerFor reads the provider a route's :id names, after the caller and
// admission checks. It answers a missing provider with a 404 and returns false.
func (m *product) providerFor(c *gin.Context) (authn.Identity, authn.SSOProvider, bool) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return authn.Identity{}, authn.SSOProvider{}, false
	}
	p, err := m.ssoProviders.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		m.answer(c, "reading a provider", err)
		return authn.Identity{}, authn.SSOProvider{}, false
	}
	return id, p, true
}

// handleMetadataRefresh answers POST
// /admin/sso/providers/:id/metadata/refresh: it dispatches to the refresher of
// the provider's protocol. The action is idp.manage.
func (m *product) handleMetadataRefresh(c *gin.Context) {
	id, provider, ok := m.providerFor(c)
	if !ok {
		return
	}
	refresher := m.refresherFor(provider.Protocol)
	if refresher == nil {
		// The provider's protocol has no refresher in this assembly; the request
		// asks for an operation nothing delivers.
		m.bad(c)
		return
	}
	if !m.asks(c, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: provider.Tenant, Target: provider.ID}) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionIDPPut, provider.ID,
		map[string]string{detailActionID: actionID, detailChange: "metadata-refresh"})) {
		return
	}
	refreshed, err := refresher.Refresh(c.Request.Context(), provider.ID)
	if err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionIDPPut, provider.ID, actionID))
		m.answer(c, "refreshing provider metadata", err)
		return
	}
	c.JSON(200, gin.H{"provider": providerViewOf(refreshed)})
}

// refresherFor returns the refresher of a protocol, or nil when none is there.
func (m *product) refresherFor(protocol string) authn.SSOMetadataRefresher {
	for _, r := range m.refreshers {
		if r.Protocol() == protocol {
			return r
		}
	}
	return nil
}
