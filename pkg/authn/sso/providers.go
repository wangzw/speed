package sso

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/model"
	"github.com/vislake/speed/pkg/event"
)

// ErrProviderNotFound reports that no identity provider row answers an
// identifier. It is the root package's sentinel, so the management routes,
// which call the root interface, answer 404 for it without importing this
// package.
var ErrProviderNotFound = authn.ErrProviderNotFound

// errInvalidDomain reports a domain that is not well formed: an email domain
// that cannot be reduced to a lower-case ASCII (punycode) name.
var errInvalidDomain = errors.New("authn.sso: the email domain is not well formed")

// errNoDomainClaim reports a domain verification asked for with no claim on it.
// A claim is written by ClaimDomain before VerifyDomain is called.
var errNoDomainClaim = errors.New("authn.sso: the provider holds no such domain claim")

// errTXTNotFound reports that the TXT record naming the verification token was
// not found. It carries no value: the token and the domain are not in it.
var errTXTNotFound = errors.New("authn.sso: the verification record was not found")

// maxNameChars is the longest a provider name may be, which the table's CHECK
// also holds.
const maxNameChars = 64

// maxConfigBytes is the longest the config JSON may be, which the table's CHECK
// also holds.
const maxConfigBytes = 65536

// maxAttrTargets is how many attr.<name> targets one claims mapping may carry.
const maxAttrTargets = 8

// maxAttrNameBytes is the longest an attr.<name> name may be: the key idp.<name>
// in a session payload is at most 32 bytes, and idp. is four of them.
const maxAttrNameBytes = 28

// verifyTokenBytes is how many random bytes a domain verification token carries.
const verifyTokenBytes = 32

// unavailable wraps a database failure as the one the interface reports for a
// dependency that is down.
func (p *product) unavailable(err error) error {
	return fmt.Errorf("%w: %w", authn.ErrUnavailable, err)
}

// now is the instant this module reads the time at, in UTC.
func (p *product) now() time.Time { return p.clock.Now() }

// Get reads one provider row without its secret.
func (p *product) Get(ctx context.Context, id string) (authn.SSOProvider, error) {
	row, ok, err := p.register.readIDP(ctx, id)
	if err != nil {
		return authn.SSOProvider{}, p.unavailable(err)
	}
	if !ok {
		return authn.SSOProvider{}, ErrProviderNotFound
	}
	return toProvider(row), nil
}

// Secret reads the client secret of a provider. Only the OIDC module calls it,
// and only for an OIDC provider: another protocol has no secret.
func (p *product) Secret(ctx context.Context, id string) (string, error) {
	secret, ok, err := p.register.readSecret(ctx, id)
	if err != nil {
		return "", p.unavailable(err)
	}
	if !ok {
		return "", ErrProviderNotFound
	}
	return secret, nil
}

// List reads the providers of a tenant, the disabled ones included.
func (p *product) List(ctx context.Context, tenant string) ([]authn.SSOProvider, error) {
	rows, err := p.register.listIDPs(ctx, tenant)
	if err != nil {
		return nil, p.unavailable(err)
	}
	out := make([]authn.SSOProvider, 0, len(rows))
	for _, row := range rows {
		out = append(out, toProvider(row))
	}
	return out, nil
}

// Tenant answers which tenant a provider belongs to.
func (p *product) Tenant(ctx context.Context, id string) (string, error) {
	row, ok, err := p.register.readIDP(ctx, id)
	if err != nil {
		return "", p.unavailable(err)
	}
	if !ok {
		return "", ErrProviderNotFound
	}
	return row.TenantID, nil
}

// Discover finds the enabled providers of a verified email domain. The domain is
// normalized here; a request carrying one that cannot be is refused. The answer
// names only what is public about a domain, and the email's local part is never
// looked at.
func (p *product) Discover(ctx context.Context, emailDomain string) ([]authn.SSOProvider, error) {
	domain, err := normalizeDomain(emailDomain)
	if err != nil {
		return nil, err
	}
	rows, err := p.register.discover(ctx, domain)
	if err != nil {
		return nil, p.unavailable(err)
	}
	out := make([]authn.SSOProvider, 0, len(rows))
	for _, row := range rows {
		out = append(out, toProvider(row))
	}
	return out, nil
}

// Put creates or updates a provider. A nil secret keeps the stored one. The
// config is validated and an unknown key refused; an update may not change the
// tenant. Every write raises the version by one and, once committed, publishes
// authn.idp.changed.
func (p *product) Put(ctx context.Context, in authn.SSOProvider, secret *string) (authn.SSOProvider, error) {
	cfg, err := validateConfig(in.Protocol, in.Config, p.spKeyConfigured())
	if err != nil {
		return authn.SSOProvider{}, err
	}
	if err := p.checkPutShape(in, secret); err != nil {
		return authn.SSOProvider{}, err
	}

	publish := changeCreated
	now := p.now()
	row := idpRow{
		ID:                in.ID,
		TenantID:          in.Tenant,
		Protocol:          in.Protocol,
		Name:              in.Name,
		Status:            boolStatus(in.Enabled),
		Provisioning:      in.Provisioning,
		TrustIDPMFA:       in.TrustIDPMFA,
		MergeExisting:     in.MergeExisting,
		AllowIDPInitiated: in.AllowIDPInitiated,
		AllowSHA1:         in.AllowSHA1,
		Config:            string(cfg),
		Version:           1,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if in.ID == "" {
		row.ID = event.NewID()
		if secret != nil {
			s := *secret
			row.ClientSecret = &s
		}
		if err := p.register.insertIDP(ctx, row); err != nil {
			return authn.SSOProvider{}, p.unavailable(err)
		}
	} else {
		existing, ok, err := p.register.readIDPWithSecret(ctx, in.ID)
		if err != nil {
			return authn.SSOProvider{}, p.unavailable(err)
		}
		if !ok {
			return authn.SSOProvider{}, ErrProviderNotFound
		}
		if existing.TenantID != in.Tenant {
			return authn.SSOProvider{}, fmt.Errorf("%w: an update may not move an identity provider from "+
				"the tenant it belongs to", authn.ErrInvalidConfig)
		}
		publish = updatedChange(existing.Status, row.Status)
		row.Version = existing.Version + 1
		row.CreatedAt = existing.CreatedAt
		row.ClientSecret = existing.ClientSecret
		if secret != nil {
			s := *secret
			row.ClientSecret = &s
		}
		if err := p.register.saveIDP(ctx, row); err != nil {
			return authn.SSOProvider{}, p.unavailable(err)
		}
	}

	p.warnOnRelaxation(in)
	p.publishChange(ctx, row.ID, publish)
	return toProvider(row), nil
}

// checkPutShape holds a Put to the shape rules that do not depend on the stored
// row: the name and the protocol, an explicit provisioning, and a secret only
// for OIDC.
func (p *product) checkPutShape(in authn.SSOProvider, secret *string) error {
	if in.Name == "" || utf8.RuneCountInString(in.Name) > maxNameChars {
		return fmt.Errorf("%w: a provider name has to be 1 to %d characters", authn.ErrInvalidConfig, maxNameChars)
	}
	switch in.Protocol {
	case model.SSOProtocolOIDC, model.SSOProtocolSAML:
	default:
		return fmt.Errorf("%w: the protocol is not one of %s and %s",
			authn.ErrInvalidConfig, model.SSOProtocolOIDC, model.SSOProtocolSAML)
	}
	switch in.Provisioning {
	case model.ProvisioningJIT, model.ProvisioningPreprovisioned:
	default:
		return fmt.Errorf("%w: provisioning has to be %s or %s and has no default",
			authn.ErrInvalidConfig, model.ProvisioningJIT, model.ProvisioningPreprovisioned)
	}
	if secret != nil && *secret != "" && in.Protocol != model.SSOProtocolOIDC {
		return fmt.Errorf("%w: only an OIDC provider carries a client secret", authn.ErrInvalidConfig)
	}
	return nil
}

// updatedChange names the change of an update from the status it had to the
// status it has: a transition to enabled or disabled is named as such, and every
// other update is updated.
func updatedChange(from, to int) string {
	switch {
	case from == to:
		return changeUpdated
	case to != 0:
		return changeEnabled
	default:
		return changeDisabled
	}
}

// warnOnRelaxation writes one Warn for each protection a row gives up.
func (p *product) warnOnRelaxation(in authn.SSOProvider) {
	if in.MergeExisting {
		p.logger.Warn(diagnosticPrefix + ModuleName + ": merge_existing is on for an identity provider, so a " +
			"just-in-time login may merge into an existing account and that IdP gains a lasting login on it")
	}
	if in.AllowIDPInitiated {
		p.logger.Warn(diagnosticPrefix + ModuleName + ": allow_idp_initiated is on for an identity provider, so a " +
			"login it starts carries no login-CSRF binding")
	}
	if in.AllowSHA1 {
		p.logger.Warn(diagnosticPrefix + ModuleName + ": allow_sha1 is on for an identity provider, so it may " +
			"assert a SHA-1 signature")
	}
}

// Delete deletes a provider row and its domain claims, and publishes the change.
func (p *product) Delete(ctx context.Context, id string) error {
	if _, ok, err := p.register.readIDP(ctx, id); err != nil {
		return p.unavailable(err)
	} else if !ok {
		return ErrProviderNotFound
	}
	if err := p.register.deleteIDP(ctx, id); err != nil {
		return p.unavailable(err)
	}
	p.publishChange(ctx, id, changeDeleted)
	return nil
}

// ClaimDomain records an unverified claim of an email domain for a provider and
// returns the token to publish in the TXT record. A domain another provider has
// verified is ErrDomainClaimed; an unverified claim takes no place and may stand
// beside others.
func (p *product) ClaimDomain(ctx context.Context, idpID, domain string) (string, error) {
	normalized, err := normalizeDomain(domain)
	if err != nil {
		return "", err
	}
	row, ok, err := p.register.readIDP(ctx, idpID)
	if err != nil {
		return "", p.unavailable(err)
	}
	if !ok {
		return "", ErrProviderNotFound
	}
	taken, err := p.register.domainVerifiedElsewhere(ctx, normalized, idpID)
	if err != nil {
		return "", p.unavailable(err)
	}
	if taken {
		return "", authn.ErrDomainClaimed
	}
	token, err := newVerifyToken()
	if err != nil {
		return "", err
	}
	if err := p.register.claimDomain(ctx, row.TenantID, idpID, normalized, token, p.now()); err != nil {
		return "", p.unavailable(err)
	}
	return token, nil
}

// VerifyDomain looks up the TXT record _speed-authn.<domain> and, when it holds
// speed-authn=<token>, marks the claim verified. A domain another provider has
// verified, or one lost to the uniqueness constraint in a race, is
// ErrDomainClaimed. A lookup that finds nothing, and one that fails, change
// nothing.
func (p *product) VerifyDomain(ctx context.Context, idpID, domain string) error {
	normalized, err := normalizeDomain(domain)
	if err != nil {
		return err
	}
	claim, ok, err := p.register.claim(ctx, idpID, normalized)
	if err != nil {
		return p.unavailable(err)
	}
	if !ok {
		return errNoDomainClaim
	}
	records, err := p.lookup(ctx, txtName(normalized))
	if err != nil {
		return fmt.Errorf("%w: %w", authn.ErrUnavailable, err)
	}
	if !holdsToken(records, claim.VerifyToken) {
		return errTXTNotFound
	}
	if err := p.register.markVerified(ctx, idpID, normalized, p.now()); err != nil {
		if errors.Is(err, errNoDomainClaim) {
			return errNoDomainClaim
		}
		return p.markVerifiedOutcome(err)
	}
	p.publishChange(ctx, idpID, changeDomainVerified)
	return nil
}

// markVerifiedOutcome reads the failure of the verification write: a domain
// another provider verified makes the unique index refuse it, which is
// ErrDomainClaimed; anything else is an unavailable dependency.
func (p *product) markVerifiedOutcome(err error) error {
	if duplicated(err) {
		return authn.ErrDomainClaimed
	}
	return p.unavailable(err)
}

// RemoveDomain removes a claim, verified or not, and publishes the change when
// there was one.
func (p *product) RemoveDomain(ctx context.Context, idpID, domain string) error {
	normalized, err := normalizeDomain(domain)
	if err != nil {
		return err
	}
	had, ok, err := p.register.claim(ctx, idpID, normalized)
	if err != nil {
		return p.unavailable(err)
	}
	if err := p.register.removeDomain(ctx, idpID, normalized); err != nil {
		return p.unavailable(err)
	}
	if ok && had.VerifiedAt != nil {
		p.publishChange(ctx, idpID, changeDomainRemoved)
	}
	return nil
}

// ReverifyDomains looks up again every verified domain of a provider and
// withdraws the verification of those no longer found, publishing the change for
// each. A domain whose lookup failed keeps its verification, and the call
// returns the failure.
func (p *product) ReverifyDomains(ctx context.Context, idpID string) error {
	rows, err := p.register.verifiedDomains(ctx, idpID)
	if err != nil {
		return p.unavailable(err)
	}
	var failures []error
	for _, row := range rows {
		records, lookupErr := p.lookup(ctx, txtName(row.Domain))
		if lookupErr != nil {
			failures = append(failures, fmt.Errorf("resolving %s: %w", row.Domain, lookupErr))
			continue
		}
		if holdsToken(records, row.VerifyToken) {
			continue
		}
		if err := p.register.withdrawVerification(ctx, idpID, row.Domain); err != nil {
			return p.unavailable(err)
		}
		p.publishChange(ctx, idpID, changeDomainUnverified)
	}
	return errors.Join(failures...)
}

// publishChange publishes authn.idp.changed after a committed write. A failure
// is a Warn and never fails the write: the event is a hint that lets another
// replica drop a cached provider, and the row is the authority.
func (p *product) publishChange(ctx context.Context, idpID, change string) {
	if p.bus == nil {
		return
	}
	payload := authn.IDPChanged{IDPID: idpID, Change: change}
	evt := event.Event{Type: authn.EventIDPChanged, Payload: payload}
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publishTimeout)
	defer cancel()
	if err := p.bus.Publish(pubCtx, evt); err != nil {
		p.logger.Warn(diagnosticPrefix+ModuleName+": an idp.changed event could not be published; it is a "+
			"hint and the write stands", slog.String("cause", err.Error()))
	}
}

// publishTimeout bounds how long a published hint may take.
const publishTimeout = 5 * time.Second

// newVerifyToken mints the random token a domain owner publishes. It fails only
// if the system's randomness is unavailable.
func newVerifyToken() (string, error) {
	buf := make([]byte, verifyTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("authn.sso: minting a domain verification token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// txtName is the DNS name a domain's verification token is published at.
func txtName(domain string) string { return "_speed-authn." + domain }

// holdsToken reports whether any TXT record is the verification token line.
func holdsToken(records []string, token string) bool {
	want := "speed-authn=" + token
	for _, r := range records {
		if strings.TrimSpace(r) == want {
			return true
		}
	}
	return false
}

// boolStatus is the stored form of the enabled flag: 1 when enabled, 0 (the
// zero value, read as disabled) otherwise.
func boolStatus(enabled bool) int {
	if enabled {
		return 1
	}
	return 0
}

// normalizeDomain reduces an email domain to the lower-case ASCII (punycode)
// form the tables store and compare. A name that is not printable ASCII, or is
// not a well-formed domain, cannot be reduced and is refused: a caller that
// asked with one is told the value is not a domain, which never depends on
// whether any provider holds it.
func normalizeDomain(domain string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(domain))
	if trimmed == "" || len(trimmed) > 253 {
		return "", fmt.Errorf("%w: a domain is 1 to 253 bytes", errInvalidDomain)
	}
	for i := 0; i < len(trimmed); i++ {
		if c := trimmed[i]; c < 0x21 || c > 0x7e {
			return "", fmt.Errorf("%w: a domain is printable ASCII", errInvalidDomain)
		}
	}
	if strings.ContainsAny(trimmed, "@ \t") {
		return "", fmt.Errorf("%w: a domain carries no separator such as @ or a space", errInvalidDomain)
	}
	for _, label := range strings.Split(trimmed, ".") {
		if label == "" || len(label) > 63 {
			return "", fmt.Errorf("%w: a domain's labels are 1 to 63 bytes", errInvalidDomain)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("%w: a domain label may not begin or end with a hyphen", errInvalidDomain)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return "", fmt.Errorf("%w: a domain is made of letters, digits and hyphens", errInvalidDomain)
			}
		}
	}
	return trimmed, nil
}

// isConcreteIssuer reports whether an issuer is a concrete one rather than a
// multi-tenant aggregator. The well-known aggregator placeholders are refused so
// that an IdP configured with one is refused at write time, before the exact
// match against the discovery document is ever reached.
func isConcreteIssuer(u *url.URL) bool {
	for _, segment := range strings.Split(u.Path, "/") {
		switch strings.ToLower(segment) {
		case "common", "organizations", "consumers":
			return false
		}
	}
	return true
}
