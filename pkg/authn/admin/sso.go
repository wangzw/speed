package admin

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

type domainClaims interface {
	Domains(ctx context.Context, idpID string) ([]authn.SSOProviderDomain, error)
	DomainsMany(ctx context.Context, ids []string) (map[string][]authn.SSOProviderDomain, error)
}

func providerViewOf(p authn.SSOProvider, domains []authn.SSOProviderDomain, tenantName string) AuthnAdminProvider {
	claims := make([]AuthnAdminProviderDomain, len(domains))
	for i, d := range domains {
		claims[i] = AuthnAdminProviderDomain{Domain: d.Domain, Verified: d.Verified}
	}
	return AuthnAdminProvider{
		ID:                p.ID,
		Tenant:            tenantName,
		Protocol:          p.Protocol,
		Name:              p.Name,
		Enabled:           p.Enabled,
		Provisioning:      p.Provisioning,
		UsernameStrategy:  p.UsernameStrategy,
		UsernameClaim:     p.UsernameClaim,
		TrustIDPMFA:       p.TrustIDPMFA,
		AllowIDPInitiated: p.AllowIDPInitiated,
		AllowSHA1:         p.AllowSHA1,
		MergeExisting:     p.MergeExisting,
		Config:            p.Config,
		Version:           p.Version,
		Warnings:          providerWarnings(p),
		Domains:           claims,
	}
}

func (m *product) readDomains(ctx context.Context, idpID string) ([]authn.SSOProviderDomain, error) {
	domains, err := m.domainClaims.Domains(ctx, idpID)
	if err != nil {
		return nil, m.unavailable("reading a provider's domains", err)
	}
	return domains, nil
}

func providerWarnings(p authn.SSOProvider) []string {
	var out []string
	if p.AllowIDPInitiated {
		out = append(out, "idp-initiated sign-in is enabled, which cannot defend against login CSRF")
	}
	if p.AllowSHA1 {
		out = append(out, "SHA-1 signed assertions are accepted")
	}
	return out
}

type providerWriteFields struct {
	Protocol          string
	Name              string
	Enabled           *bool
	Provisioning      string
	UsernameStrategy  *string
	UsernameClaim     *string
	TrustIDPMFA       *bool
	AllowIDPInitiated *bool
	AllowSHA1         *bool
	MergeExisting     *bool
	Config            json.RawMessage
	ClientSecret      *string
}

func (in providerCreateInput) writeFields() providerWriteFields {
	return providerWriteFields{
		Protocol: in.Protocol, Name: in.Name, Enabled: in.Enabled, Provisioning: in.Provisioning,
		UsernameStrategy: in.UsernameStrategy, UsernameClaim: in.UsernameClaim,
		TrustIDPMFA: in.TrustIDPMFA, AllowIDPInitiated: in.AllowIDPInitiated, AllowSHA1: in.AllowSHA1,
		MergeExisting: in.MergeExisting, Config: configObject(in.Config), ClientSecret: in.ClientSecret,
	}
}

func (in providerUpdateInput) writeFields() providerWriteFields {
	return providerWriteFields{
		Protocol: in.Protocol, Name: in.Name, Enabled: in.Enabled, Provisioning: in.Provisioning,
		UsernameStrategy: in.UsernameStrategy, UsernameClaim: in.UsernameClaim,
		TrustIDPMFA: in.TrustIDPMFA, AllowIDPInitiated: in.AllowIDPInitiated, AllowSHA1: in.AllowSHA1,
		MergeExisting: in.MergeExisting, Config: configObject(in.Config), ClientSecret: in.ClientSecret,
	}
}

func configObject(config map[string]json.RawMessage) json.RawMessage {
	if config == nil {
		return nil
	}
	raw, _ := json.Marshal(config)
	return raw
}

func (f providerWriteFields) provider(id, tenant string, base authn.SSOProvider) authn.SSOProvider {
	p := authn.SSOProvider{
		ID:                id,
		Tenant:            tenant,
		Protocol:          f.Protocol,
		Name:              f.Name,
		Provisioning:      f.Provisioning,
		UsernameStrategy:  base.UsernameStrategy,
		UsernameClaim:     base.UsernameClaim,
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
	if f.UsernameStrategy != nil {
		p.UsernameStrategy = *f.UsernameStrategy
	}
	if f.UsernameClaim != nil {
		p.UsernameClaim = *f.UsernameClaim
	}
	return p
}

func (m *product) handleProviderList(ctx context.Context, in tenantInput) (AuthnAdminProviderList, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return AuthnAdminProviderList{}, err
	}
	if err = m.admit(id, tenantDomain); err != nil {
		return AuthnAdminProviderList{}, err
	}
	if err = m.asks(ctx, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: in.Tenant}); err != nil {
		return AuthnAdminProviderList{}, err
	}
	providers, err := m.ssoProviders.List(ctx, in.Tenant)
	if err != nil {
		return AuthnAdminProviderList{}, m.unavailable("listing providers", err)
	}
	ids := make([]string, len(providers))
	for i, p := range providers {
		ids[i] = p.ID
	}
	claims, err := m.domainClaims.DomainsMany(ctx, ids)
	if err != nil {
		return AuthnAdminProviderList{}, m.unavailable("reading provider domains", err)
	}
	tenantName, nameErr := m.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return AuthnAdminProviderList{}, m.unavailable("reading the tenant's name", nameErr)
	}
	views := make([]AuthnAdminProvider, len(providers))
	for i, p := range providers {
		views[i] = providerViewOf(p, claims[p.ID], tenantName)
	}
	return AuthnAdminProviderList{Providers: views}, nil
}

func (m *product) handleProviderCreate(ctx context.Context, in providerCreateInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id, tenantDomain); err != nil {
		return nil, err
	}
	if in.Protocol == "" || in.Name == "" || in.Provisioning == "" {
		return nil, m.bad()
	}
	tenantName, nameErr := m.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return nil, m.unavailable("reading the tenant's name", nameErr)
	}
	if in.BodyTenant != "" && in.BodyTenant != tenantName {
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
		map[string]string{detailActionID: actionID, detailTenant: tenantName})); err != nil {
		return nil, err
	}
	provider, err := m.ssoProviders.Put(ctx, in.writeFields().provider("", in.Tenant, authn.SSOProvider{}), in.ClientSecret)
	if err != nil {
		m.auditBestEffort(ctx, failedAudit(actionIDPPut, "", actionID))
		return m.replyFailure(ctx, "creating a provider", err)
	}
	return speedhttp.JSONReply(201, AuthnAdminProviderEnvelope{Provider: providerViewOf(provider, nil, tenantName)}), nil
}

func (m *product) handleProviderGet(ctx context.Context, in providerIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, p, err := m.providerFor(ctx, in.Tenant, in.ID)
	if err != nil {
		return m.lookupFailure(err)
	}
	if err = m.asks(ctx, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: p.Tenant, Target: p.ID}); err != nil {
		return nil, err
	}
	domains, err := m.readDomains(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	tenantName, nameErr := m.nameOf(ctx, p.Tenant)
	if nameErr != nil {
		return nil, m.unavailable("reading the tenant's name", nameErr)
	}
	return speedhttp.JSONReply(200, AuthnAdminProviderEnvelope{Provider: providerViewOf(p, domains, tenantName)}), nil
}

func (m *product) handleProviderUpdate(ctx context.Context, in providerUpdateInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, existing, err := m.providerFor(ctx, in.Tenant, in.ID)
	if err != nil {
		return m.lookupFailure(err)
	}
	tenantName, nameErr := m.nameOf(ctx, existing.Tenant)
	if nameErr != nil {
		return nil, m.unavailable("reading the tenant's name", nameErr)
	}
	if in.BodyTenant != "" && in.BodyTenant != tenantName {
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
		map[string]string{detailActionID: actionID, detailTenant: tenantName})); err != nil {
		return nil, err
	}
	provider, err := m.ssoProviders.Put(ctx, in.writeFields().provider(in.ID, existing.Tenant, existing), in.ClientSecret)
	if err != nil {
		m.auditBestEffort(ctx, failedAudit(actionIDPPut, in.ID, actionID))
		return m.replyFailure(ctx, "updating a provider", err)
	}
	domains, err := m.readDomains(ctx, provider.ID)
	if err != nil {
		return nil, err
	}
	return speedhttp.JSONReply(200, AuthnAdminProviderEnvelope{Provider: providerViewOf(provider, domains, tenantName)}), nil
}

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
	tenantName, nameErr := m.nameOf(ctx, p.Tenant)
	if nameErr != nil {
		return nil, m.unavailable("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := m.auditRequired(ctx, mandatory(actionIDPDelete, in.ID,
		map[string]string{detailActionID: actionID, detailTenant: tenantName})); err != nil {
		return nil, err
	}
	if err := m.ssoProviders.Delete(ctx, in.ID); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionIDPDelete, in.ID, actionID))
		return m.replyFailure(ctx, "deleting a provider", err)
	}
	return speedhttp.EmptyReply(204), nil
}

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
	tenantName, nameErr := m.nameOf(ctx, provider.Tenant)
	if nameErr != nil {
		return nil, m.unavailable("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if err = m.auditRequired(ctx, mandatory(actionIDPDomain, provider.ID,
		map[string]string{detailActionID: actionID, detailTenant: tenantName, detailChange: "claim"})); err != nil {
		return nil, err
	}
	token, err := m.ssoProviders.ClaimDomain(ctx, provider.ID, in.Domain)
	if err != nil {
		m.auditBestEffort(ctx, failedAudit(actionIDPDomain, provider.ID, actionID))
		return m.replyFailure(ctx, "claiming a domain", err)
	}
	return speedhttp.JSONReply(200, AuthnAdminVerifyToken{VerifyToken: token}), nil
}

func (m *product) handleDomainRemove(ctx context.Context, in domainAddressInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	return m.changeDomain(ctx, in, "remove", func(idpID, domain string) error {
		return m.ssoProviders.RemoveDomain(ctx, idpID, domain)
	})
}

func (m *product) handleDomainVerify(ctx context.Context, in domainAddressInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	return m.changeDomain(ctx, in, "verify", func(idpID, domain string) error {
		return m.ssoProviders.VerifyDomain(ctx, idpID, domain)
	})
}

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
		return m.replyFailure(ctx, "reverifying domains", err)
	}
	return speedhttp.EmptyReply(204), nil
}

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
	tenantName, nameErr := m.nameOf(ctx, provider.Tenant)
	if nameErr != nil {
		return nil, m.unavailable("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := m.auditRequired(ctx, mandatory(actionIDPDomain, provider.ID,
		map[string]string{detailActionID: actionID, detailTenant: tenantName, detailChange: change})); err != nil {
		return nil, err
	}
	if err := act(provider.ID, in.Domain); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionIDPDomain, provider.ID, actionID))
		return m.replyFailure(ctx, "writing a domain", err)
	}
	return speedhttp.EmptyReply(204), nil
}

func (m *product) providerFor(ctx context.Context, tenant, providerID string) (authn.Identity, authn.SSOProvider, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return authn.Identity{}, authn.SSOProvider{}, err
	}
	if err = m.admit(id, tenantDomain); err != nil {
		return authn.Identity{}, authn.SSOProvider{}, err
	}
	p, err := m.ssoProviders.Get(ctx, providerID)
	if err != nil {
		return authn.Identity{}, authn.SSOProvider{}, m.answer(ctx, "reading a provider", err)
	}
	if p.Tenant != tenant {
		return authn.Identity{}, authn.SSOProvider{}, errNotFound
	}
	return id, p, nil
}

func (m *product) lookupFailure(err error) (speedhttp.Reply, error) {
	if errors.Is(err, errNotFound) {
		return speedhttp.EmptyReply(404), nil
	}
	return nil, err
}

func (m *product) handleMetadataRefresh(ctx context.Context, in providerIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, provider, err := m.providerFor(ctx, in.Tenant, in.ID)
	if err != nil {
		return m.lookupFailure(err)
	}
	refresher := m.refresherFor(provider.Protocol)
	if refresher == nil {
		return nil, m.bad()
	}
	if err = m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err = m.asks(ctx, authn.Decision{Action: authn.ActionIDPManage, Actor: id, Tenant: provider.Tenant, Target: provider.ID}); err != nil {
		return nil, err
	}
	tenantName, nameErr := m.nameOf(ctx, provider.Tenant)
	if nameErr != nil {
		return nil, m.unavailable("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if err = m.auditRequired(ctx, mandatory(actionIDPPut, provider.ID,
		map[string]string{detailActionID: actionID, detailTenant: tenantName, detailChange: "metadata-refresh"})); err != nil {
		return nil, err
	}
	refreshed, err := refresher.Refresh(ctx, provider.ID)
	if err != nil {
		m.auditBestEffort(ctx, failedAudit(actionIDPPut, provider.ID, actionID))
		return m.replyFailure(ctx, "refreshing provider metadata", err)
	}
	domains, err := m.readDomains(ctx, refreshed.ID)
	if err != nil {
		return nil, err
	}
	return speedhttp.JSONReply(200, AuthnAdminProviderEnvelope{Provider: providerViewOf(refreshed, domains, tenantName)}), nil
}

func (m *product) refresherFor(protocol string) authn.SSOMetadataRefresher {
	for _, r := range m.refreshers {
		if r.Protocol() == protocol {
			return r
		}
	}
	return nil
}
