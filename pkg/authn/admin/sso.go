package admin

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// providerViewOf builds one SSO provider as the management routes show it. The
// encrypted client secret never appears; the warnings name the settings that
// loosen a default, so an administrator sees them on the response that set them.
func providerViewOf(p authn.SSOProvider) AuthnAdminProvider {
	return AuthnAdminProvider{
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

// providerWriteFields is the body of a provider write, apart from the two
// segments that address it. The create and update inputs carry the same body,
// and this is the one reading of it.
type providerWriteFields struct {
	Protocol          string
	Name              string
	Enabled           *bool
	Provisioning      string
	TrustIDPMFA       *bool
	AllowIDPInitiated *bool
	AllowSHA1         *bool
	MergeExisting     *bool
	Config            json.RawMessage
	ClientSecret      *string
}

// writeFields reads the create input's body.
func (in providerCreateInput) writeFields() providerWriteFields {
	return providerWriteFields{
		Protocol: in.Protocol, Name: in.Name, Enabled: in.Enabled, Provisioning: in.Provisioning,
		TrustIDPMFA: in.TrustIDPMFA, AllowIDPInitiated: in.AllowIDPInitiated, AllowSHA1: in.AllowSHA1,
		MergeExisting: in.MergeExisting, Config: configObject(in.Config), ClientSecret: in.ClientSecret,
	}
}

// writeFields reads the update input's body.
func (in providerUpdateInput) writeFields() providerWriteFields {
	return providerWriteFields{
		Protocol: in.Protocol, Name: in.Name, Enabled: in.Enabled, Provisioning: in.Provisioning,
		TrustIDPMFA: in.TrustIDPMFA, AllowIDPInitiated: in.AllowIDPInitiated, AllowSHA1: in.AllowSHA1,
		MergeExisting: in.MergeExisting, Config: configObject(in.Config), ClientSecret: in.ClientSecret,
	}
}

// configObject renders a bound configuration object as the raw JSON the
// capability validates. An absent config stays nil, which the capability reads
// as "not given"; a present one is re-encoded because the binding decodes it
// into a map. Every member came from a decoded body, so each is valid JSON and
// encoding the map cannot fail.
func configObject(config map[string]json.RawMessage) json.RawMessage {
	if config == nil {
		return nil
	}
	raw, _ := json.Marshal(config)
	return raw
}

// provider builds the provider value a write carries. It fills the boolean
// fields the body left out from the base, which is the stored provider for an
// update and the zero value for a create.
func (f providerWriteFields) provider(id, tenant string, base authn.SSOProvider) authn.SSOProvider {
	p := authn.SSOProvider{
		ID:                id,
		Tenant:            tenant,
		Protocol:          f.Protocol,
		Name:              f.Name,
		Provisioning:      f.Provisioning,
		Config:            f.Config,
		Enabled:           base.Enabled,
		TrustIDPMFA:       base.TrustIDPMFA,
		AllowIDPInitiated: base.AllowIDPInitiated,
		AllowSHA1:         base.AllowSHA1,
		MergeExisting:     base.MergeExisting,
	}
	if f.Enabled != nil {
		p.Enabled = *f.Enabled
	}
	if f.TrustIDPMFA != nil {
		p.TrustIDPMFA = *f.TrustIDPMFA
	}
	if f.AllowIDPInitiated != nil {
		p.AllowIDPInitiated = *f.AllowIDPInitiated
	}
	if f.AllowSHA1 != nil {
		p.AllowSHA1 = *f.AllowSHA1
	}
	if f.MergeExisting != nil {
		p.MergeExisting = *f.MergeExisting
	}
	return p
}

// handleProviderList answers GET /admin/tenants/:tenant/sso/providers. The
// action is idp.manage.
func (m *product) handleProviderList(ctx context.Context, in tenantInput) (AuthnAdminProviderList, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return AuthnAdminProviderList{}, err
	}
	if err = m.admit(id); err != nil {
		return AuthnAdminProviderList{}, err
	}
	if err = m.asks(ctx, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: in.Tenant}); err != nil {
		return AuthnAdminProviderList{}, err
	}
	providers, err := m.ssoProviders.List(ctx, in.Tenant)
	if err != nil {
		return AuthnAdminProviderList{}, m.unavailable("listing providers", err)
	}
	views := make([]AuthnAdminProvider, len(providers))
	for i, p := range providers {
		views[i] = providerViewOf(p)
	}
	return AuthnAdminProviderList{Providers: views}, nil
}

// handleProviderCreate answers POST /admin/tenants/:tenant/sso/providers. The
// path and the body must agree on the tenant. The action is idp.manage.
func (m *product) handleProviderCreate(ctx context.Context, in providerCreateInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id); err != nil {
		return nil, err
	}
	if in.BodyTenant != "" && in.BodyTenant != in.Tenant {
		return nil, m.bad()
	}
	if in.Protocol == "" || in.Name == "" || in.Provisioning == "" {
		return nil, m.bad()
	}
	if err = m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err = m.asks(ctx, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: in.Tenant}); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err = m.auditRequired(ctx, mandatory(actionIDPPut, "",
		map[string]string{detailActionID: actionID, detailTenant: in.Tenant})); err != nil {
		return nil, err
	}
	provider, err := m.ssoProviders.Put(ctx, in.writeFields().provider("", in.Tenant, authn.SSOProvider{}), in.ClientSecret)
	if err != nil {
		m.auditBestEffort(ctx, failedAudit(actionIDPPut, "", actionID))
		return m.replyFailure("creating a provider", err)
	}
	return speedhttp.JSONReply(201, AuthnAdminProviderEnvelope{Provider: providerViewOf(provider)}), nil
}

// handleProviderGet answers GET /admin/sso/providers/:id. The decision's tenant
// is the provider's own, resolved from the store before the Decider is asked.
func (m *product) handleProviderGet(ctx context.Context, in providerIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, p, err := m.providerFor(ctx, in.Tenant, in.ID)
	if err != nil {
		return m.lookupFailure(err)
	}
	if err := m.asks(ctx, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: p.Tenant, Target: p.ID}); err != nil {
		return nil, err
	}
	return speedhttp.JSONReply(200, AuthnAdminProviderEnvelope{Provider: providerViewOf(p)}), nil
}

// handleProviderUpdate answers PUT /admin/sso/providers/:id. The tenant is the
// provider's own and is immutable: a body that names another is refused. The
// action is idp.manage.
func (m *product) handleProviderUpdate(ctx context.Context, in providerUpdateInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, existing, err := m.providerFor(ctx, in.Tenant, in.ID)
	if err != nil {
		return m.lookupFailure(err)
	}
	if in.BodyTenant != "" && in.BodyTenant != existing.Tenant {
		return nil, m.bad()
	}
	if err = m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err = m.asks(ctx, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: existing.Tenant, Target: in.ID}); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err = m.auditRequired(ctx, mandatory(actionIDPPut, in.ID,
		map[string]string{detailActionID: actionID, detailTenant: existing.Tenant})); err != nil {
		return nil, err
	}
	provider, err := m.ssoProviders.Put(ctx, in.writeFields().provider(in.ID, existing.Tenant, existing), in.ClientSecret)
	if err != nil {
		m.auditBestEffort(ctx, failedAudit(actionIDPPut, in.ID, actionID))
		return m.replyFailure("updating a provider", err)
	}
	return speedhttp.JSONReply(200, AuthnAdminProviderEnvelope{Provider: providerViewOf(provider)}), nil
}

// handleProviderDelete answers DELETE /admin/sso/providers/:id. The action is
// idp.manage.
func (m *product) handleProviderDelete(ctx context.Context, in providerIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, p, err := m.providerFor(ctx, in.Tenant, in.ID)
	if err != nil {
		return m.lookupFailure(err)
	}
	if err := m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err := m.asks(ctx, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: p.Tenant, Target: in.ID}); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err := m.auditRequired(ctx, mandatory(actionIDPDelete, in.ID,
		map[string]string{detailActionID: actionID, detailTenant: p.Tenant})); err != nil {
		return nil, err
	}
	if err := m.ssoProviders.Delete(ctx, in.ID); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionIDPDelete, in.ID, actionID))
		return m.replyFailure("deleting a provider", err)
	}
	return speedhttp.EmptyReply(204), nil
}

// handleDomainClaim answers POST /admin/sso/providers/:id/domains and returns
// the token to publish in DNS. The action is idp.manage.
func (m *product) handleDomainClaim(ctx context.Context, in domainClaimInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, provider, err := m.providerFor(ctx, in.Tenant, in.ID)
	if err != nil {
		return m.lookupFailure(err)
	}
	if in.Domain == "" {
		return nil, m.bad()
	}
	if err = m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err = m.asks(ctx, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: provider.Tenant, Target: provider.ID}); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err = m.auditRequired(ctx, mandatory(actionIDPDomain, provider.ID,
		map[string]string{detailActionID: actionID, detailChange: "claim"})); err != nil {
		return nil, err
	}
	token, err := m.ssoProviders.ClaimDomain(ctx, provider.ID, in.Domain)
	if err != nil {
		m.auditBestEffort(ctx, failedAudit(actionIDPDomain, provider.ID, actionID))
		return m.replyFailure("claiming a domain", err)
	}
	return speedhttp.JSONReply(200, AuthnAdminVerifyToken{VerifyToken: token}), nil
}

// handleDomainRemove answers DELETE /admin/sso/providers/:id/domains/:domain.
// The action is idp.manage.
func (m *product) handleDomainRemove(ctx context.Context, in domainAddressInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	return m.changeDomain(ctx, in, "remove", func(idpID, domain string) error {
		return m.ssoProviders.RemoveDomain(ctx, idpID, domain)
	})
}

// handleDomainVerify answers POST
// /admin/sso/providers/:id/domains/:domain/verify. The action is idp.manage.
func (m *product) handleDomainVerify(ctx context.Context, in domainAddressInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	return m.changeDomain(ctx, in, "verify", func(idpID, domain string) error {
		return m.ssoProviders.VerifyDomain(ctx, idpID, domain)
	})
}

// handleReverifyDomains answers POST /admin/sso/providers/:id/reverify-domains.
// The action is idp.manage.
func (m *product) handleReverifyDomains(ctx context.Context, in providerIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, provider, err := m.providerFor(ctx, in.Tenant, in.ID)
	if err != nil {
		return m.lookupFailure(err)
	}
	if err := m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err := m.asks(ctx, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: provider.Tenant, Target: provider.ID}); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err := m.auditRequired(ctx, mandatory(actionIDPDomain, provider.ID,
		map[string]string{detailActionID: actionID, detailChange: "reverify"})); err != nil {
		return nil, err
	}
	if err := m.ssoProviders.ReverifyDomains(ctx, provider.ID); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionIDPDomain, provider.ID, actionID))
		return m.replyFailure("reverifying domains", err)
	}
	return speedhttp.EmptyReply(204), nil
}

// changeDomain runs a domain write the same way for the remove and verify
// routes: resolve the provider, decide, write the mandatory audit, call the
// capability.
func (m *product) changeDomain(ctx context.Context, in domainAddressInput, change string, act func(idpID, domain string) error) (speedhttp.Reply, error) {
	id, provider, err := m.providerFor(ctx, in.Tenant, in.ID)
	if err != nil {
		return m.lookupFailure(err)
	}
	if err := m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err := m.asks(ctx, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: provider.Tenant, Target: provider.ID}); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err := m.auditRequired(ctx, mandatory(actionIDPDomain, provider.ID,
		map[string]string{detailActionID: actionID, detailChange: change})); err != nil {
		return nil, err
	}
	if err := act(provider.ID, in.Domain); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionIDPDomain, provider.ID, actionID))
		return m.replyFailure("writing a domain", err)
	}
	return speedhttp.EmptyReply(204), nil
}

// providerFor reads the provider a route's :id names, after the caller and
// admission checks. Its error is either the handler's own refusal or
// errNotFound, which lookupFailure turns into the bare 404.
// providerFor reads one provider and holds it to the tenant the path names: a
// provider addressed under a tenant it does not belong to answers not found,
// the same answer a provider that does not exist gets, so the path cannot be
// used to probe another tenant's providers.
func (m *product) providerFor(ctx context.Context, tenant, providerID string) (authn.Identity, authn.SSOProvider, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return authn.Identity{}, authn.SSOProvider{}, err
	}
	if err = m.admit(id); err != nil {
		return authn.Identity{}, authn.SSOProvider{}, err
	}
	p, err := m.ssoProviders.Get(ctx, providerID)
	if err != nil {
		return authn.Identity{}, authn.SSOProvider{}, m.answer("reading a provider", err)
	}
	if p.Tenant != tenant {
		return authn.Identity{}, authn.SSOProvider{}, errNotFound
	}
	return id, p, nil
}

// lookupFailure writes providerFor's answer: a missing provider is a bare 404,
// and every other error is the fault the handler returns.
func (m *product) lookupFailure(err error) (speedhttp.Reply, error) {
	if errors.Is(err, errNotFound) {
		return speedhttp.EmptyReply(404), nil
	}
	return nil, err
}

// handleMetadataRefresh answers POST
// /admin/sso/providers/:id/metadata/refresh: it dispatches to the refresher of
// the provider's protocol. The action is idp.manage.
func (m *product) handleMetadataRefresh(ctx context.Context, in providerIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, provider, err := m.providerFor(ctx, in.Tenant, in.ID)
	if err != nil {
		return m.lookupFailure(err)
	}
	refresher := m.refresherFor(provider.Protocol)
	if refresher == nil {
		// The provider's protocol has no refresher in this assembly; the request
		// asks for an operation nothing delivers.
		return nil, m.bad()
	}
	if err = m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err = m.asks(ctx, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: provider.Tenant, Target: provider.ID}); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err = m.auditRequired(ctx, mandatory(actionIDPPut, provider.ID,
		map[string]string{detailActionID: actionID, detailChange: "metadata-refresh"})); err != nil {
		return nil, err
	}
	refreshed, err := refresher.Refresh(ctx, provider.ID)
	if err != nil {
		m.auditBestEffort(ctx, failedAudit(actionIDPPut, provider.ID, actionID))
		return m.replyFailure("refreshing provider metadata", err)
	}
	return speedhttp.JSONReply(200, AuthnAdminProviderEnvelope{Provider: providerViewOf(refreshed)}), nil
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
