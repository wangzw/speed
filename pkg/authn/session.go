package authn

import (
	"context"
	"net/http"
	"time"
)

// Sessions is the login session store and the writer of the session cookie. The
// session module delivers it exclusively, and it is a capability a host may
// supply an implementation of: adding a method or changing a signature breaks
// such a host.
//
// A session is one of three kinds. A browser session is a cookie, a token
// session stands behind an access token and a refresh token, and a service
// session is one row per service-account secret, made only by ForCredential.
// SessionID is public and is no credential; the credential of a browser session
// is Issued.Token, which is handed over once. Revocation does not depend on any
// clock: it is the row's revoked_at, and the validity of a session is
// decided by Status in one place for the cookie, the access token and the refresh
// token alike.
type Sessions interface {
	// Create makes a browser or a token session. A service session is refused,
	// and so is an attribute set beyond the bounds of SessionIDPAttributesMax and
	// the constants beside it, or a payload over 8 KiB; each is
	// ErrInvalidArgument. The expiry is computed here from the kind's settings.
	// When the account would exceed the per-account limit, its least recently
	// used browser or token session is revoked.
	Create(ctx context.Context, in NewSession) (Issued, error)
	// Get reads a session row by its public identifier, a revoked or expired one
	// included. A session that is not there is ErrSessionNotFound.
	Get(ctx context.Context, id SessionID) (SessionInfo, error) // ErrSessionNotFound
	// List lists the account's live browser and token sessions. Service sessions
	// are not listed.
	List(ctx context.Context, account AccountID) ([]SessionInfo, error)
	// Revoke revokes a session, idempotently. A service session is revoked as
	// well, after which ForCredential for its secret is ErrSessionRevoked. reason
	// is one of the Reason constants that ValidPayloadReasonClass accepts, and
	// any other is ErrInvalidArgument.
	Revoke(ctx context.Context, id SessionID, reason string) error
	// RevokeAccount revokes the account's browser and token sessions in one
	// statement, keeping the one named by except when it is not empty, and returns
	// how many it revoked. Service sessions are not among them. reason is read as
	// Revoke reads it.
	RevokeAccount(ctx context.Context, account AccountID, except SessionID, reason string) (int, error)
	// Rotate gives a browser session a new token and keeps its identifier, level
	// and expiry. It has nothing to do for the other kinds.
	Rotate(ctx context.Context, id SessionID) (Issued, error)
	// Elevate records a completed step-up: the level, the time of authentication,
	// the methods and whether the level is an IdP's declaration. A browser session
	// gets a new token; the identifier and the absolute expiry stay, and no new
	// session is made.
	Elevate(ctx context.Context, id SessionID, e Elevation) (Issued, error)
	// SetImpersonation sets the identifier of the impersonation record attached
	// to the session, the empty string ending it, and gives a browser session a
	// new token.
	SetImpersonation(ctx context.Context, id SessionID, grantID string) (Issued, error)
	// Status says whether a session is still valid and returns what a backend
	// needs to build an identity. It is cached for the check-ttl setting. A
	// session that is not there is ErrSessionNotFound; one that exists and is
	// invalid, being revoked, expired, of a disabled account or older than the
	// account's valid-after instant, is a SessionStatus whose Active is false.
	Status(ctx context.Context, id SessionID) (SessionStatus, error) // cached by check-ttl
	// StatusFresh is Status judged by the same rule and read from the store: a
	// refresh token's rotation and the completion of a login flow use it.
	StatusFresh(ctx context.Context, id SessionID) (SessionStatus, error) // reads the store
	// ForCredential gets or makes the service session of a secret in one atomic
	// step against the uniqueness of credentialID. A live row has its expiry
	// updated to expires, nil meaning the secret does not expire, and is returned
	// as it is; a row older than the account's valid-after instant is renewed; a
	// revoked row is ErrSessionRevoked.
	ForCredential(ctx context.Context, account AccountID, credentialID string, expires *time.Time) (SessionID, error)
	// RevokeCredential revokes the service session of a secret, idempotently.
	// reason is read as Revoke reads it.
	RevokeCredential(ctx context.Context, credentialID, reason string) error

	// SetCookie writes the Set-Cookie header with the value of in.Token. The
	// caller makes this call after the last step of its handler that can fail.
	SetCookie(w http.ResponseWriter, in Issued)
	// ClearCookie writes a Set-Cookie header with Max-Age=0. CookieClearer has the
	// same method with the same signature, so one method satisfies both.
	ClearCookie(w http.ResponseWriter)
	// Current reads only the session cookie of the request and finds the session
	// by the token's digest. ok is false when there is no cookie or no row. A
	// login that completes uses it to revoke the session the request came with; it
	// is no defence and has nothing to do with authentication.
	Current(r *http.Request) (id SessionID, ok bool, err error)
}
