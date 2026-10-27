package token

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/vislake/speed/pkg/authn"
)

// The assertion that this module delivers the token surface. It is the module's
// one deliverable: an equivalent module a host puts in its place implements the
// same interface, so a change here reaches a host.
var _ authn.Tokens = (*product)(nil)

// The operation names and outcomes of the one counter this module reports
// through, authn.token.operations. They are a closed set a dashboard reads, so
// the values are fixed. The two attribute names are the ones the instrument is
// reported under.
const (
	operationIssued            = "issued"
	operationRefreshed         = "refreshed"
	operationRevoked           = "revoked"
	operationClientCredentials = "client-credentials"

	outcomeOK            = "ok"
	outcomeInvalid       = "invalid"
	outcomeReuseDetected = "reuse-detected"
	outcomeUnavailable   = "unavailable"

	operationAttribute = "operation"
	outcomeAttribute   = "outcome"
)

// Issue signs an access token from the facts of in and, for an impersonation
// token, the remaining time of the record it names. The token's header carries
// the signing key's kid and typ=at+jwt, and its claim set is the fixed one the
// design states: the registered claims, the session and authentication facts of
// the request, and whatever the command's ClaimsContributor implementations add.
// With no signing key configured it is ErrNoSigningKey.
func (p *product) Issue(ctx context.Context, in authn.IssueToken) (authn.AccessToken, error) {
	token, err := p.signAccess(ctx, in)
	if err != nil {
		if errors.Is(err, authn.ErrNoSigningKey) {
			p.recordOperation(ctx, operationIssued, outcomeUnavailable)
		} else {
			p.recordOperation(ctx, operationIssued, outcomeInvalid)
		}
		return authn.AccessToken{}, err
	}
	p.recordOperation(ctx, operationIssued, outcomeOK)
	return token, nil
}

// signAccess builds and signs the access token for in. It is the issuing path
// without the counter, so that a rotation and a client credential issuance can
// report the operation they stand for rather than every one reporting an issue.
func (p *product) signAccess(ctx context.Context, in authn.IssueToken) (authn.AccessToken, error) {
	if p.keys == nil || p.keys.signing == nil {
		return authn.AccessToken{}, authn.ErrNoSigningKey
	}
	now := p.clock.Now()
	ttl, err := p.issueTTL(in.TTL)
	if err != nil {
		return authn.AccessToken{}, err
	}
	if in.ImpersonationID != "" {
		ttl, err = p.recordTTL(ctx, in.ImpersonationID, now, ttl)
		if err != nil {
			return authn.AccessToken{}, err
		}
	}
	extra, err := p.contributorClaims(ctx, in)
	if err != nil {
		return authn.AccessToken{}, err
	}
	jti, err := uuid.NewV7()
	if err != nil {
		return authn.AccessToken{}, fmt.Errorf("%s: making a token identifier: %w", ModuleName, err)
	}
	expiry := now.Add(ttl)
	claims := tokenClaims{
		Issuer:    p.cfg.issuer,
		Subject:   in.Account.String(),
		Audience:  p.cfg.audience,
		Expiry:    expiry.Unix(),
		NotBefore: now.Unix(),
		IssuedAt:  now.Unix(),
		ID:        jti.String(),
		Session:   string(in.Session),
		Methods:   methodNames(in.Methods),
		Assurance: assuranceText(in.Assurance),
		AuthTime:  in.AuthTime.Unix(),
		ClientID:  p.clientIDFor(in),
		Extra:     extra,
	}
	if in.Actor != "" {
		claims.Actor = &actorClaim{Subject: in.Actor.String()}
		claims.Impersonation = in.ImpersonationID
		claims.ImpersonationTenant = in.Scope
	}
	payload, err := claims.marshal()
	if err != nil {
		return authn.AccessToken{}, err
	}
	raw, err := sign(*p.keys.signing, accessTokenType, payload)
	if err != nil {
		return authn.AccessToken{}, fmt.Errorf("%s: signing an access token: %w", ModuleName, err)
	}
	return authn.AccessToken{Value: raw, ExpiresAt: expiry}, nil
}

// issueTTL settles the lifetime of an access token. A zero request takes the
// access-ttl setting; any other value is used as it is and has to be over zero
// and at most one hour, the bound the design puts on exp - iat. A value outside
// it is the caller's mistake, not a request's, and no token is signed.
func (p *product) issueTTL(requested time.Duration) (time.Duration, error) {
	ttl := requested
	if ttl == 0 {
		ttl = p.settings.accessTTL()
	}
	if ttl <= 0 || ttl > accessTTLUpperBound {
		return 0, fmt.Errorf("%w: an access token's lifetime is %s, and it must be over zero and at "+
			"most %s", authn.ErrInvalidArgument, ttl, accessTTLUpperBound)
	}
	return ttl, nil
}

// recordTTL caps an impersonation token's lifetime at the time the record has
// left, so exp does not outlive the record even for a verifier that reads exp
// alone. The record must still hold: with no impersonation capability there is
// no remaining time to read, and a record that has lapsed issues no token. The
// remaining time is read through the same cache the verifying backend uses; the
// record's end is fixed, so a cached answer bounds the lifetime correctly.
func (p *product) recordTTL(ctx context.Context, id string, now time.Time, ttl time.Duration) (time.Duration, error) {
	if p.impersonation == nil {
		return 0, fmt.Errorf("%w: an impersonation token was asked for and no impersonation capability is present",
			authn.ErrUnavailable)
	}
	grant, active, err := p.activeGrant(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("%w: asking whether the impersonation record holds: %w", authn.ErrUnavailable, err)
	}
	if !active {
		return 0, fmt.Errorf("%w: the impersonation record %s is not active", authn.ErrNotImpersonating, id)
	}
	remaining := grant.EndsAt.Sub(now)
	if remaining <= 0 {
		return 0, fmt.Errorf("%w: the impersonation record %s has expired", authn.ErrNotImpersonating, id)
	}
	if remaining < ttl {
		ttl = remaining
	}
	return ttl, nil
}

// contributorClaims calls every ClaimsContributor the command registered and
// merges what each returned. A contributor that returns a reserved claim name
// or an error fails the whole issue, so no token is signed from a partial set.
func (p *product) contributorClaims(ctx context.Context, in authn.IssueToken) (map[string]any, error) {
	if len(p.claims) == 0 {
		return nil, nil
	}
	principal := authn.Principal{
		Account:   in.Account,
		Actor:     in.Actor,
		Session:   in.Session,
		Methods:   slices.Clone(in.Methods),
		Assurance: in.Assurance,
		AuthTime:  in.AuthTime,
		Scope:     in.Scope,
	}
	var merged map[string]any
	for _, contributor := range p.claims {
		added, err := contributor.Claims(ctx, principal)
		if err != nil {
			return nil, fmt.Errorf("%s: a claims contributor failed: %w", ModuleName, err)
		}
		if err := checkContributorClaims(added); err != nil {
			return nil, err
		}
		for name, value := range added {
			if merged == nil {
				merged = make(map[string]any)
			}
			merged[name] = value
		}
	}
	return merged, nil
}

// clientIDFor is the client_id claim of a token: the identifier the caller
// named, or the configured one for a token issued for a login session.
func (p *product) clientIDFor(in authn.IssueToken) string {
	if in.ClientID != "" {
		return in.ClientID
	}
	return p.cfg.clientID
}

// methodNames writes the method set as the amr claim: the value names of the
// closed set, in order. An empty set yields nil, so amr is left out.
func methodNames(methods []authn.Method) []string {
	if len(methods) == 0 {
		return nil
	}
	out := make([]string, len(methods))
	for i, m := range methods {
		out[i] = string(m)
	}
	return out
}

// assuranceText is the acr claim: the level's decimal text. It is one of the
// closed set "1" or "2". An impersonation token carries the operator session's
// level, as a token of the operator's own would: the operator is the one who
// authenticated. A zero level, which is what a caller that did not state one
// hands over, is written as a single factor too, so the acr never falls outside
// the set.
func assuranceText(level authn.Assurance) string {
	if level == authn.AssuranceNone {
		return "1"
	}
	return strconv.Itoa(int(level))
}

// recordOperation counts one token operation on the module's meter. When the
// assembly carries no meter provider the counter is nil and nothing is
// recorded.
func (p *product) recordOperation(ctx context.Context, operation, outcome string) {
	if p.meters.operations == nil {
		return
	}
	p.meters.operations.Add(context.WithoutCancel(ctx), 1, metric.WithAttributes(
		attribute.String(operationAttribute, operation),
		attribute.String(outcomeAttribute, outcome)))
}
