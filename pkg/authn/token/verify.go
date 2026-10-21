package token

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// schemeBearer is the one Authorization scheme this backend takes whole. A token
// arrives only under it, and the design fixes the value.
const schemeBearer = "bearer"

// maxTokenLen is the longest an access token may be, in bytes. It is checked
// before the token is parsed, so the arbitrary bytes an unauthenticated caller
// controls reach the parser bounded: go-jose has a disclosed malformed-input
// panic, and this limit together with the gateway's recover is what contains it.
const maxTokenLen = 8192

// maxTokenLifetime is the longest interval an access token may claim between its
// issue and its expiry. It is the access-ttl setting's own upper bound, and the
// backend refuses a token whose exp - iat is past it whatever was configured.
const maxTokenLifetime = accessTTLUpperBound

// verifyClaims is the claim set of an access token as the backend reads it. It
// is decoded by signedToken.validate, which checks the signature and the
// library's standard claims first, so a value of it was carried by a token that
// verifies.
//
// The standard claims are plain fields rather than a library type: this file may
// not import the JOSE library (jose.go is the package's one importer). The
// library's own claim rules are applied by the bridge, signedToken.validate; the
// rules the library leaves out are the ones checkRequiredClaims writes here.
type verifyClaims struct {
	Issuer    string   `json:"iss"`
	Subject   string   `json:"sub"`
	Audience  []string `json:"aud"`
	Expiry    *int64   `json:"exp"`
	NotBefore *int64   `json:"nbf"`
	IssuedAt  *int64   `json:"iat"`
	ID        string   `json:"jti"`
	Session   string   `json:"sid"`

	Methods   []string `json:"amr"`
	Assurance string   `json:"acr"`
	AuthTime  *int64   `json:"auth_time"`

	ClientID string `json:"client_id"`

	// The impersonation claims, present only on an impersonation token. The
	// imp_tenant claim is not read here: this process takes the scope from the
	// impersonation record, and the claim is only for an external verifier that
	// reads the token offline.
	Actor *actorClaim `json:"act"`
	Imp   string      `json:"imp"`
}

// Name reports the name this backend answers under. It is a constant of the code
// and not an input: the gateway stamps it on every identity this backend vouches
// for.
func (p *product) Name() string { return backendName }

// Accepts declares what this backend takes and what it may hand back: the bearer
// scheme, accounts of the library namespace, and impersonation and SSO facts an
// access token can carry. It claims no cookie.
func (p *product) Accepts() authn.Accepts {
	return authn.Accepts{
		Schemes:       []string{schemeBearer},
		Accounts:      "",
		Impersonation: true,
		SSO:           true,
	}
}

// Challenge writes this backend's part of a 401's challenge. A token backend adds
// error="invalid_token" when the request carried a token this backend refused, as
// RFC 6750 and RFC 9068 require; otherwise the token is not refused on this
// backend's account and the challenge names the scheme alone. realm is already
// escaped by the gateway.
func (p *product) Challenge(realm string, rejected bool) []string {
	value := `Bearer realm="` + realm + `"`
	if rejected {
		value += `, error="invalid_token"`
	}
	return []string{value}
}

// Verify checks one bearer access token, in the order the design fixes: length
// and shape, header and algorithm, local key, signature, standard claims, the
// account snapshot, the session and the impersonation record, and the principal
// drawn from all of them last. A step that does not hold ends the request in a
// Denied whose cause reaches the log; a store that cannot be reached is
// Unavailable, never a pass.
//
// The input is arbitrary bytes: the steps are ordered so the cheap, state-free
// checks run before anything reads a store, and the token's kid, typ and
// algorithm are held to closed sets before the signature is checked at all.
func (p *product) Verify(ctx context.Context, c authn.Credential) authn.Verdict {
	// Step 1: bound the input and require the three segments of a compact JWS.
	if len(c.Value) > maxTokenLen {
		return p.deny(ctx, slog.LevelInfo, "the token is longer than the limit")
	}
	if strings.Count(c.Value, ".") != 2 {
		return p.deny(ctx, slog.LevelInfo, "the token is not three segments")
	}

	// Step 2: parse under the configured algorithms only; require a kid and an
	// access-token typ, and refuse any critical header extension. A header that
	// points at key material elsewhere (jku, x5u, jwk, x5c) is never used and
	// never fetched: the key is taken from the local set below alone.
	st, err := parseSigned(c.Value, p.keys.algorithms)
	if err != nil {
		return p.deny(ctx, slog.LevelInfo, "the token does not parse under the configured algorithms",
			slog.Any("error", err))
	}
	kid := st.headerKID()
	if kid == "" {
		return p.deny(ctx, slog.LevelInfo, "the token carries no kid")
	}
	typ, present := st.headerType()
	if !present || !isAccessTokenType(typ) {
		return p.deny(ctx, slog.LevelInfo, "the token's typ is not an access token", slog.String("typ", typ))
	}
	if st.criticalHeader() {
		return p.deny(ctx, slog.LevelInfo, "the token names a critical header extension")
	}

	// Step 3: take the key from the local set by its kid and require the key's
	// registered algorithm to be the one in the header.
	key, ok := p.keys.lookup(kid)
	if !ok {
		return p.deny(ctx, slog.LevelInfo, "the token's kid is not in the configured key set")
	}
	if st.headerAlgorithm() != key.alg {
		return p.deny(ctx, slog.LevelInfo, "the token's algorithm is not the one its key carries")
	}

	// Steps 4 and the library's half of step 5: check the signature and the
	// standard claims the library validates — the issuer, the audience and the
	// times. The claims come back only from this call.
	now := p.clock.Now()
	var vc verifyClaims
	if verr := st.validate(key.pub, &vc, p.cfg.issuer, p.cfg.audience, now, p.settings.leeway()); verr != nil {
		return p.deny(ctx, slog.LevelInfo, "the token's signature or standard claims do not hold",
			slog.Any("error", verr))
	}

	// Step 5's own rules: the claims the library treats as optional and the
	// lifetime bound.
	if refused := p.checkRequiredClaims(ctx, vc); refused != nil {
		return *refused
	}
	sub := authn.AccountID(vc.Subject)

	// Step 6: the account snapshot, through the cache so that a database which
	// cannot be reached still serves an identity read within check-ttl. The
	// account of sub must be enabled, and the token not older than the instant
	// after which the account's tokens are void, compared by the second the
	// token's iat carries.
	acct, err := p.accountFor(ctx, sub)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return p.deny(ctx, slog.LevelInfo, "the account does not exist")
	case err != nil:
		return p.denyUnavailable(ctx, "reading the account snapshot", err)
	}
	if acct.Status != authn.StatusActive {
		return p.deny(ctx, slog.LevelInfo, "the account is not enabled")
	}
	if time.Unix(*vc.IssuedAt, 0).Unix() < acct.ValidAfter.Unix() {
		return p.deny(ctx, slog.LevelInfo, "the token was issued before the account's valid-after instant")
	}

	// Steps 7 and 8: the session, the impersonation record, and the principal.
	return p.authenticate(ctx, vc, sub, acct)
}

// checkRequiredClaims applies the standard-claim rules the library does not: the
// claims it treats as optional — exp, iat, jti, sub, sid, client_id — are
// required, the lifetime is bounded, and the subject is held to a well-formed
// account identifier. The issuer, the audience and the times are the bridge's,
// checked in signedToken.validate before this runs.
//
// A claim the library treats as optional but this backend requires is a defect
// the bridge is there to catch, and an over-long lifetime is the same, so both
// are logged at Error.
func (p *product) checkRequiredClaims(ctx context.Context, vc verifyClaims) *authn.Verdict {
	if vc.Expiry == nil || vc.IssuedAt == nil {
		v := p.deny(ctx, slog.LevelError, "the token carries no exp or no iat, which the bridge requires")
		return &v
	}
	if vc.ID == "" {
		v := p.deny(ctx, slog.LevelError, "the token carries no jti")
		return &v
	}
	if vc.Session == "" {
		v := p.deny(ctx, slog.LevelError, "the token carries no sid")
		return &v
	}
	if vc.Subject == "" {
		v := p.deny(ctx, slog.LevelError, "the token carries no sub")
		return &v
	}
	if exp, iat := time.Unix(*vc.Expiry, 0), time.Unix(*vc.IssuedAt, 0); exp.Sub(iat) > maxTokenLifetime {
		v := p.deny(ctx, slog.LevelError, "the token's lifetime is longer than the limit")
		return &v
	}
	if _, err := authn.ParseAccountID(vc.Subject); err != nil {
		v := p.deny(ctx, slog.LevelError, "the sub is not a well-formed account identifier")
		return &v
	}
	if vc.ClientID == "" {
		v := p.deny(ctx, slog.LevelError, "the token carries no client_id")
		return &v
	}
	return nil
}

// authenticate runs the session and impersonation steps and builds the principal
// the token's kind calls for.
//
// The session step runs only when check-session is on; the impersonation step
// runs whenever the token names a record, whatever check-session says, so an
// impersonation the switch would otherwise drop still takes effect, bounded by
// the impersonation cache's check-ttl. A token that names an impersonation record
// or a session whose capability is absent is a refusal, never a pass.
func (p *product) authenticate(ctx context.Context, vc verifyClaims, sub authn.AccountID, acct authn.Account) authn.Verdict {
	sid := authn.SessionID(vc.Session)
	service := slices.Contains(vc.Methods, string(authn.MethodClientSecret))
	impersonation := vc.Imp != ""
	var actor authn.AccountID
	if vc.Actor != nil {
		actor = authn.AccountID(vc.Actor.Subject)
	}

	var status authn.SessionStatus
	haveSession := false
	if p.settings.checkSession() {
		if p.sessions == nil {
			return p.deny(ctx, slog.LevelError, "the session check is on and no session capability is present")
		}
		s, err := p.sessions.Status(ctx, sid)
		switch {
		case errors.Is(err, authn.ErrSessionNotFound):
			return p.deny(ctx, slog.LevelInfo, "the session does not exist")
		case err != nil:
			return p.denyUnavailable(ctx, "reading the session", err)
		}
		if !s.Active {
			return p.deny(ctx, slog.LevelInfo, "the session is not valid")
		}
		owner := sub
		if impersonation {
			owner = actor
		}
		if s.Account != owner {
			return p.deny(ctx, slog.LevelInfo, "the session belongs to another account")
		}
		if service && s.Kind != authn.SessionService {
			return p.deny(ctx, slog.LevelInfo, "a service-account token names a session that is not a service session")
		}
		status = s
		haveSession = true
	}

	var grant authn.ImpersonationGrant
	if impersonation {
		if p.impersonation == nil {
			return p.deny(ctx, slog.LevelError,
				"the token names an impersonation record and no impersonation capability is present")
		}
		g, active, err := p.activeGrant(ctx, vc.Imp)
		if err != nil {
			return p.denyUnavailable(ctx, "asking whether the impersonation record holds", err)
		}
		if !active {
			return p.deny(ctx, slog.LevelInfo, "the impersonation record is not valid")
		}
		if g.Actor != actor {
			return p.deny(ctx, slog.LevelInfo, "the impersonation record's actor is not the token's actor")
		}
		if g.Target != sub {
			return p.deny(ctx, slog.LevelInfo, "the impersonation record's target is not the token's subject")
		}
		grant = g
	}

	principal := authn.Principal{Account: sub, Session: sid}
	switch {
	case impersonation:
		// The identity is the target's; the actor and the tenant come from the
		// record, never from the imp_tenant claim. The level is capped at a
		// single factor, because the target did not authenticate.
		principal.Actor = actor
		principal.Scope = grant.Tenant
		if haveSession {
			principal.Methods = slices.Clone(status.Methods)
			principal.AuthTime = status.AuthTime
			principal.Assurance = min(status.Assurance, authn.AssuranceSingle)
		} else {
			principal.Methods = methodsOf(vc.Methods)
			principal.AuthTime = authTimeOf(vc)
			principal.Assurance = authn.AssuranceSingle
		}
		principal.Attributes = principalAttributes(acct.Attributes, nil)
	case service:
		// A service-account token's facts come from the token: the session row
		// records them once at creation and they do not change per issuance.
		principal.Methods = []authn.Method{authn.MethodClientSecret}
		principal.Assurance = authn.AssuranceSingle
		principal.AuthTime = time.Unix(*vc.IssuedAt, 0)
		principal.Attributes = principalAttributes(acct.Attributes, nil)
	default:
		if haveSession {
			principal.Methods = slices.Clone(status.Methods)
			principal.Assurance = status.Assurance
			principal.IDPAssured = status.IDPAssured
			principal.AuthTime = status.AuthTime
			principal.Provider = status.Provider
			principal.Attributes = principalAttributes(acct.Attributes, status.Attributes)
		} else {
			principal.Methods = methodsOf(vc.Methods)
			principal.Assurance = assuranceOf(vc.Assurance)
			principal.AuthTime = authTimeOf(vc)
			principal.Attributes = principalAttributes(acct.Attributes, nil)
		}
	}

	// The gateway stamps AssuranceNone as a single factor, but a principal this
	// backend hands over carries a stated level and a method set.
	if principal.Assurance == authn.AssuranceNone {
		principal.Assurance = authn.AssuranceSingle
	}
	if len(principal.Methods) == 0 {
		return p.deny(ctx, slog.LevelError, "the token yields no authentication method")
	}
	return authn.Authenticated(principal)
}

// deny writes the cause of a refusal at the level it belongs to and answers
// Denied. The token, its claims and any secret are never written.
func (p *product) deny(ctx context.Context, level slog.Level, reason string, args ...any) authn.Verdict {
	p.logger.Log(ctx, level, diagnosticPrefix+ModuleName+": "+reason, args...)
	return authn.Denied(fmt.Errorf("%s: %w: %s", ModuleName, authn.ErrTokenInvalid, reason))
}

// denyUnavailable answers Unavailable for a store that cannot be reached, which
// is a 503 and never a pass. The cause wraps the dependency's own error.
func (p *product) denyUnavailable(ctx context.Context, op string, err error) authn.Verdict {
	p.logger.Warn(diagnosticPrefix+ModuleName+": "+op+" failed, so the token is neither accepted nor refused",
		slog.Any("error", err))
	return authn.Unavailable(fmt.Errorf("%w: %s: %w", authn.ErrUnavailable, op, err))
}

// isAccessTokenType reports whether typ, compared case-insensitively, is one of
// the two spellings RFC 9068 fixes for an access token.
func isAccessTokenType(typ string) bool {
	return strings.EqualFold(typ, accessTokenType) || strings.EqualFold(typ, accessTokenTypeAlt)
}

// lookup finds the key with the given kid in the local set, which the design
// keeps small: a linear scan is the whole of it.
func (ks *keySet) lookup(kid string) (keyEntry, bool) {
	for i := range ks.entries {
		if ks.entries[i].kid == kid {
			return ks.entries[i], true
		}
	}
	return keyEntry{}, false
}

// methodsOf reads the amr claim as the method set of a principal. What the claims
// hold is trusted here because the token verified; the gateway holds the set to
// its own closed set on the way out.
func methodsOf(raw []string) []authn.Method {
	out := make([]authn.Method, len(raw))
	for i, m := range raw {
		out[i] = authn.Method(m)
	}
	return out
}

// authTimeOf is the authentication time taken from a token's own claims, for the
// stateless mode: auth_time when it is there, the issue instant otherwise.
func authTimeOf(vc verifyClaims) time.Time {
	if vc.AuthTime != nil {
		return time.Unix(*vc.AuthTime, 0)
	}
	return time.Unix(*vc.IssuedAt, 0)
}

// assuranceOf reads the acr claim as an assurance level. The value is the
// decimal text of Assurance; anything but "2" reads as a single factor, which is
// the conservative side.
func assuranceOf(acr string) authn.Assurance {
	if acr == "2" {
		return authn.AssuranceMulti
	}
	return authn.AssuranceSingle
}

// principalAttributes is the attribute set of an identity: the account's, and the
// idp.* entries the session carried. An account attribute under a reserved prefix
// is dropped, because idp.* and authn.* belong to whoever writes them, which is
// the IdP mapping and this capability.
func principalAttributes(account, idp map[string]string) map[string]string {
	out := make(map[string]string, len(account)+len(idp))
	for k, v := range account {
		if strings.HasPrefix(k, authn.ReservedPrefixIDP) || strings.HasPrefix(k, authn.ReservedPrefixAuthn) {
			continue
		}
		out[k] = v
	}
	maps.Copy(out, idp)
	if len(out) == 0 {
		return nil
	}
	return out
}
