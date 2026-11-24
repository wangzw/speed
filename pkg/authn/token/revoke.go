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

// Revoke handles a presented token as the kind it is: a refresh token revokes
// the session it belongs to, an ordinary access token whose signature holds
// revokes its session, an impersonation token ends its record and leaves the
// administrator's session alone, and a service token is a no-op. A token that
// cannot be recognised is a no-op too, and none of these outcomes tells the
// caller whether the token was valid or even existed, so a presenter cannot use
// this endpoint to probe a token's state.
//
// A dependency that cannot answer is ErrUnavailable instead: the revocation did
// not happen, and the endpoint refuses rather than reporting a success the
// session does not back. The direction is the design's — refuse, do not answer
// the 200 a presenter would read as "the token is gone".
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

// endImpersonation ends the impersonation record an impersonation token names,
// by force and with the reason revoked. It is a no-op when the capability is
// absent, which is what an assembly without impersonation gets: the token is
// already refused wherever it would be read. A store that cannot be reached is
// ErrUnavailable, so the endpoint does not report a success the still-live
// record contradicts.
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
// holds. It is the verifying path's first steps only — length, structure, the
// header and the signature against the local key set — because a revocation
// judges no lifetime and no session of its own. Anything that does not hold is
// reported as "not a token I can act on".
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
