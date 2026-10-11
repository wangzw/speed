package authn

import "context"

// ClaimsContributor appends claims to an access token as it is issued. It is not
// exclusive: the token module resolves all contributors of the command, so a
// host may register more than one, and a process that takes in none issues
// tokens with nothing beyond the standard claim set.
//
// Claims is called with the principal the token is issued for, assembled from
// the issue's own facts alone. It is not the principal a gateway stamps:
// Account, Actor, Session, Methods, Assurance, AuthTime and Scope carry the
// issue's values — Scope being the tenant an impersonation is confined to, the
// same fact the imp_tenant claim is written from — and IDPAssured, Provider and
// Attributes are left at their zero value, because the issuing path does not
// fill them. A contributor reads such a field as not stated, never as a value it
// may trust. Claims returns a map merged into the token
// before it is signed; a nil map adds nothing. The reserved claim names are a
// closed set: iss, sub, aud, exp, nbf, iat, jti, sid, amr, acr, auth_time, act,
// imp, imp_tenant, client_id, typ. Returning any of them is ErrReservedClaim,
// which fails the issue as a whole with no token signed; returning an error
// fails it the same way, and a caller on the issuing path answers 503. Neither
// failure carries out a partial claim set.
//
// A contributor that puts a role into the token chooses to leave that role
// unrevocable for as long as the token lives. That consequence is the host's to
// weigh, not this module's.
type ClaimsContributor interface {
	// Claims returns the claims this contributor adds to the token of p, or an
	// error. It is called on the issuing path alone.
	Claims(ctx context.Context, p Principal) (map[string]any, error)
}
