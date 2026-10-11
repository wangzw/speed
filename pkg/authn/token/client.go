package token

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// clientCredentials authenticates a service account from the client identifier
// and secret it presented and issues its access token. It is the
// client_credentials grant's whole body, and the token endpoint's handler is its
// only caller.
//
// The order is the design's: the back-off is checked first, keyed on the
// presented client_id and judged regardless of whether the account exists; the
// secret is verified next, and a failure counts against that same key and
// answers with one error, so an unknown client and a wrong secret are
// indistinguishable; then the service session of the secret is taken or made;
// and only an access token is issued, with no refresh token.
//
// The failures it returns are the ones the endpoint maps: a *authn.ThrottledError
// when the key is in back-off (which the endpoint answers invalid_client with
// Retry-After), authn.ErrInvalidCredentials when the credential did not verify,
// authn.ErrSessionRevoked when the secret's session was revoked, and
// authn.ErrUnavailable or authn.ErrNoSigningKey when a dependency or the signing
// key is missing.
func (p *product) clientCredentials(ctx context.Context, clientID, secret string) (authn.AccessToken, error) {
	retry, err := p.throttle.Check(ctx, authn.ScopeClient, clientID)
	if err != nil {
		p.recordOperation(ctx, operationClientCredentials, outcomeUnavailable)
		return authn.AccessToken{}, fmt.Errorf("%w: reading the client back-off: %w", authn.ErrUnavailable, err)
	}
	if retry > 0 {
		p.recordOperation(ctx, operationClientCredentials, outcomeInvalid)
		return authn.AccessToken{}, &authn.ThrottledError{RetryAfter: retry}
	}

	account, secretID, expires, err := p.passwords.VerifyServiceSecret(ctx, clientID, secret)
	if err != nil {
		if errors.Is(err, authn.ErrUnavailable) {
			p.recordOperation(ctx, operationClientCredentials, outcomeUnavailable)
			return authn.AccessToken{}, err
		}
		p.countClientFailure(ctx, clientID)
		p.recordOperation(ctx, operationClientCredentials, outcomeInvalid)
		return authn.AccessToken{}, fmt.Errorf("%w: a service credential did not verify", authn.ErrInvalidCredentials)
	}
	p.clearClientFailures(ctx, clientID)

	var expiry *time.Time
	if !expires.IsZero() {
		expiry = &expires
	}
	session, err := p.sessions.ForCredential(ctx, account, secretID, expiry)
	if err != nil {
		if errors.Is(err, authn.ErrSessionRevoked) {
			p.recordOperation(ctx, operationClientCredentials, outcomeInvalid)
			return authn.AccessToken{}, err
		}
		p.recordOperation(ctx, operationClientCredentials, outcomeUnavailable)
		return authn.AccessToken{}, err
	}

	now := p.clock.Now()
	access, err := p.signAccess(ctx, authn.IssueToken{
		Account:   account,
		Session:   session,
		Methods:   []authn.Method{authn.MethodClientSecret},
		Assurance: authn.AssuranceSingle,
		AuthTime:  now,
		ClientID:  account.String(),
	})
	if err != nil {
		p.recordOperation(ctx, operationClientCredentials, outcomeUnavailable)
		return authn.AccessToken{}, err
	}
	p.recordOperation(ctx, operationClientCredentials, outcomeOK)
	return access, nil
}

// countClientFailure records one failed service credential against the presented
// client identifier. A failure to record it is a log line and does not change
// the answer, which is the same invalid_client either way.
func (p *product) countClientFailure(ctx context.Context, clientID string) {
	if _, err := p.throttle.Fail(ctx, authn.ScopeClient, clientID); err != nil {
		p.logger.Warn(diagnosticPrefix+ModuleName+": a failed client credential could not be counted",
			slog.String("cause", err.Error()))
	}
}

// clearClientFailures clears the presented client identifier's failures after a
// credential verified. It is best-effort in the same way.
func (p *product) clearClientFailures(ctx context.Context, clientID string) {
	if err := p.throttle.Succeed(ctx, authn.ScopeClient, clientID); err != nil {
		p.logger.Warn(diagnosticPrefix+ModuleName+": a client's failures could not be cleared",
			slog.String("cause", err.Error()))
	}
}
