package authn

import "context"

// Tokens issues and checks the access tokens and refresh tokens of this
// capability. The token module delivers it exclusively; a host may supply an
// equivalent module. Adding a method or changing a signature breaks a host that
// did.
//
// The login engine takes it as optional, and an assembly without it can only
// deliver cookies.
type Tokens interface {
	// Issue signs an access token from the facts of in alone: it reads nothing out
	// of the request's context. With no signing key configured it is
	// ErrNoSigningKey, which the caller answers with a 503.
	Issue(ctx context.Context, in IssueToken) (AccessToken, error) // ErrNoSigningKey
	// NewRefresh makes the first refresh token of a token session. A browser
	// session and a service session are ErrInvalidGrant, and an impersonation
	// session or a client credential has no refresh token.
	NewRefresh(ctx context.Context, session SessionID) (RefreshToken, error)
	// Refresh rotates a refresh token and issues a new access token. A token that
	// is not valid is ErrInvalidGrant, and one that was used already past the
	// grace is ErrRefreshReuse, which has revoked the whole family.
	Refresh(ctx context.Context, presented string) (AccessToken, RefreshToken, error) // ErrInvalidGrant, ErrRefreshReuse
	// Revoke is RFC 7009: it handles the presented token as the kind it is, and
	// whether the token was valid or even existed is not told.
	Revoke(ctx context.Context, presented string) error
	// JWKS returns the public keys that verify the tokens, as a JWK Set. It holds
	// no private field.
	JWKS(ctx context.Context) ([]byte, error)
}
