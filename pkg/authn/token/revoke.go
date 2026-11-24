package token

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
)

// maxAccessTokenBytes bounds the length of a token before it is parsed. The
// verifying path holds the same limit, and it is applied here so that a
// malformed token reaches no parser.
const maxAccessTokenBytes = 8192

// Revoke revokes the session or impersonation record a presented token names;
// a service or unrecognized token is a no-op. No outcome reveals whether the
// token existed, and a dependency that cannot answer is ErrUnavailable.
func (p *product) Revoke(ctx context.Context, presented string) error {
	if presented == "" {
		return nil
	}
	row, err := p.refresh.byHash(ctx, digestRefreshToken(presented))
	switch {
	case err == nil:
		return p.revokeSession(ctx, authn.SessionID(row.SessionID))
	case errors.Is(err, errNoRefreshRow):
		// Not a refresh token this table holds; the access-token path judges it.
	default:
		return p.revokeUnavailable(ctx, "reading a refresh token", err)
	}

	claims, ok := p.revokeClaims(presented)
	if !ok {
		return nil
	}
	if claims.Impersonation != "" {
		return p.endImpersonation(ctx, claims.Impersonation)
	}
	if claims.Session == "" {
		return nil
	}
	status, err := p.sessions.StatusFresh(ctx, authn.SessionID(claims.Session))
	switch {
	case errors.Is(err, authn.ErrSessionNotFound):
		// The session the token names is already gone; there is nothing to revoke.
		return nil
	case err != nil:
		return p.revokeUnavailable(ctx, "reading the session", err)
	}
	if status.Kind == authn.SessionService {
		return nil
	}
	return p.revokeSession(ctx, authn.SessionID(claims.Session))
}

func (p *product) revokeSession(ctx context.Context, session authn.SessionID) error {
	if err := p.sessions.Revoke(ctx, session, authn.ReasonSelf); err != nil {
		return p.revokeUnavailable(ctx, "revoking a session", err)
	}
	p.recordOperation(ctx, operationRevoked, outcomeOK)
	return nil
}

// endImpersonation ends the record an impersonation token names, by force with
// reason revoked. Absent capability is a no-op; an unreachable store is
// ErrUnavailable.
func (p *product) endImpersonation(ctx context.Context, id string) error {
	if p.impersonation == nil {
		return nil
	}
	if err := p.impersonation.Revoke(ctx, id, authn.Identity{}, authn.EndRevoked); err != nil {
		return p.revokeUnavailable(ctx, "ending an impersonation record", err)
	}
	p.recordOperation(ctx, operationRevoked, outcomeOK)
	return nil
}

// revokeUnavailable records the outcome and wraps a failure of a dependency the
// revocation needs in ErrUnavailable. The text names the operation and carries
// the dependency's own error, which holds no credential.
func (p *product) revokeUnavailable(ctx context.Context, op string, err error) error {
	p.recordOperation(ctx, operationRevoked, outcomeUnavailable)
	return fmt.Errorf("%w: %s: %w", authn.ErrUnavailable, op, err)
}

// revokeClaims reads the claims of a presented access token whose signature
// holds: length, structure, header and signature only, judging no lifetime or
// session; anything that fails reports the token unusable.
func (p *product) revokeClaims(presented string) (tokenClaims, bool) {
	if p.keys == nil || len(presented) > maxAccessTokenBytes {
		return tokenClaims{}, false
	}
	parsed, err := parseSigned(presented, p.keys.algorithms)
	if err != nil {
		return tokenClaims{}, false
	}
	if typ, ok := parsed.headerType(); !ok || !isAccessTokenType(typ) {
		return tokenClaims{}, false
	}
	key, ok := p.keys.lookup(parsed.headerKID())
	if !ok || key.alg != parsed.headerAlgorithm() {
		return tokenClaims{}, false
	}
	var claims tokenClaims
	if err := parsed.verify(key.pub, &claims); err != nil {
		return tokenClaims{}, false
	}
	return claims, true
}
