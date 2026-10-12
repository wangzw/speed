package saml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/vislake/speed/pkg/authn"
)

// The causes a metadata refresh can fail with. None carries a value the
// administrator wrote beyond the reason class.
var (
	errNoIDPDescriptor = errors.New("authn.sso.saml: the metadata holds no identity provider descriptor")
	errNoEntityID      = errors.New("authn.sso.saml: the metadata holds no entity identifier")
	errNoSSOService    = errors.New("authn.sso.saml: the metadata holds no single sign-on service")
	errNoCertificate   = errors.New("authn.sso.saml: the metadata holds no signing certificate")
	errNoMetadataURL   = errors.New("authn.sso.saml: the provider carries no metadata URL to refresh from")
	errNotSAMLProvider = errors.New("authn.sso.saml: the provider is not a SAML provider")
)

// Protocol names the provider protocol this refresher refreshes.
func (p *product) Protocol() string { return providerProtocol }

// Refresh fetches one provider's metadata through the restricted egress client,
// parses the identity provider's entity identifier, single sign-on URL and
// signing certificates out of it, and writes them back through the register's
// Put, raising the provider's version. A parse or validation failure leaves the
// stored configuration as it was, logs a warning and returns the error, so that
// a certificate an IdP has withdrawn stays accepted only until an administrator
// triggers a successful refresh.
func (p *product) Refresh(ctx context.Context, idpID string) (authn.SSOProvider, error) {
	saved, err := p.refresh(ctx, idpID)
	if err != nil {
		p.logger.Warn(diagnosticPrefix+ModuleName+": refreshing a provider's metadata failed, so the "+
			"stored configuration is kept", slog.String("idp", idpID), slog.String("cause", err.Error()))
	}
	return saved, err
}

// refresh is Refresh without the warning, so that the one place a failure is
// reported is the exported method.
func (p *product) refresh(ctx context.Context, idpID string) (authn.SSOProvider, error) {
	row, err := p.providers.Get(ctx, idpID)
	if err != nil {
		return authn.SSOProvider{}, unavailable("reading the provider", err)
	}
	if row.Protocol != providerProtocol {
		return authn.SSOProvider{}, errNotSAMLProvider
	}
	cfg, err := parseProviderConfig(row.Config)
	if err != nil {
		return authn.SSOProvider{}, err
	}
	if cfg.MetadataURL == "" {
		return authn.SSOProvider{}, errNoMetadataURL
	}
	meta, err := p.fetchMetadata(ctx, cfg.MetadataURL)
	if err != nil {
		return authn.SSOProvider{}, err
	}
	merged, err := mergeMetadata(row.Config, meta)
	if err != nil {
		return authn.SSOProvider{}, err
	}
	updated := row.Clone()
	updated.Config = merged
	saved, err := p.providers.Put(ctx, updated, nil)
	if err != nil {
		return authn.SSOProvider{}, unavailable("writing the refreshed metadata", err)
	}
	p.cache.drop(idpID)
	return saved, nil
}

// fetchMetadata fetches a metadata document through the restricted egress
// client, bounded by the configured timeout and the client's own body cap. A
// response that is not a 200, or one that cannot be read, is an outage.
func (p *product) fetchMetadata(ctx context.Context, metadataURL string) (metadataRefresh, error) {
	callCtx, cancel := context.WithTimeout(ctx, p.settings.egressTimeout())
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return metadataRefresh{}, unavailable("building the metadata request", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return metadataRefresh{}, unavailable("fetching the metadata", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return metadataRefresh{}, fmt.Errorf("%w: the metadata endpoint answered %d", authn.ErrUnavailable, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return metadataRefresh{}, unavailable("reading the metadata", err)
	}
	meta, err := parseIDPMetadata(body)
	if err != nil {
		return metadataRefresh{}, fmt.Errorf("%s: parsing the metadata: %w", ModuleName, err)
	}
	return meta, nil
}

// mergeMetadata writes the parsed entity identifier, single sign-on URL and
// certificates into a provider's config, leaving every other key as it was.
func mergeMetadata(raw json.RawMessage, meta metadataRefresh) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("%w: the provider's config does not decode: %w", authn.ErrInvalidConfig, err)
	}
	if fields == nil {
		fields = make(map[string]json.RawMessage)
	}
	entity, err := json.Marshal(meta.entityID)
	if err != nil {
		return nil, fmt.Errorf("%s: encoding the refreshed entity identifier: %w", ModuleName, err)
	}
	ssoURL, err := json.Marshal(meta.ssoURL)
	if err != nil {
		return nil, fmt.Errorf("%s: encoding the refreshed single sign-on URL: %w", ModuleName, err)
	}
	certs, err := json.Marshal(meta.certs)
	if err != nil {
		return nil, fmt.Errorf("%s: encoding the refreshed certificates: %w", ModuleName, err)
	}
	fields["idp_entity_id"] = entity
	fields["idp_sso_url"] = ssoURL
	fields["idp_certs"] = certs
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("%s: re-encoding the provider's config: %w", ModuleName, err)
	}
	return out, nil
}

// unavailable wraps a dependency failure so that the register and an
// administrator see an outage, and the cause names the step and nothing the
// administrator wrote.
func unavailable(doing string, err error) error {
	return fmt.Errorf("%w: %s: %w", authn.ErrUnavailable, ModuleName+" "+doing, err)
}
