package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/authn/internal/model"
	"github.com/vislake/speed/pkg/event"
)

// The metric the operations are reported on, and the meter it is created on. A
// dashboard reads the names, so they are fixed.
const (
	meterName            = "authn"
	operationsInstrument = "authn.session.operations"
	operationAttribute   = "operation"
)

// The values of the operation attribute.
const (
	opCreated    = "created"
	opElevated   = "elevated"
	opRevoked    = "revoked"
	opRevokedAll = "revoked-all"
	opPurged     = "purged"
)

// The longest a client address is kept: an IPv6 address with a zone is shorter.
const maxClientIP = 64

const maxGrantIDLen = 128

// retryLimit is how many times a conditional update that lost a race is read
// back and tried again before the call gives up.
const retryLimit = 3

// errChanged is what a call reports when its conditional update lost to a
// concurrent change more often than retryLimit allows.
var errChanged = fmt.Errorf("%w: the session kept changing under the update", authn.ErrUnavailable)

// Create makes a browser or a token session.
func (p *product) Create(ctx context.Context, in authn.NewSession) (authn.Issued, error) {
	in = in.Clone()
	if err := checkNew(in); err != nil {
		return authn.Issued{}, err
	}
	tm := p.settings.timing()
	ttl, err := period(in, tm)
	if err != nil {
		return authn.Issued{}, err
	}

	now := p.now()
	id := authn.SessionID(event.NewID())
	pl := payload{
		SID:        string(id),
		Account:    string(in.Account),
		Kind:       string(in.Kind),
		Methods:    in.Methods,
		Assurance:  in.Assurance,
		IDPAssured: in.IDPAssured,
		AuthTime:   clock.UTC(in.AuthTime),
		Provider:   in.Provider,
		Attributes: in.Attributes,
		Expires:    now.Add(ttl),
	}
	if in.Kind == authn.SessionBrowser {
		pl.Idle = now.Add(tm.idle)
	}
	blob, err := encodePayload(pl)
	if err != nil {
		return authn.Issued{}, fmt.Errorf("%w: the payload cannot be written", authn.ErrInvalidArgument)
	}
	if len(blob) > maxDataBytes {
		return authn.Issued{}, fmt.Errorf("%w: the payload is longer than %d bytes", authn.ErrInvalidArgument, maxDataBytes)
	}

	issued := authn.Issued{ID: id, ExpiresAt: pl.Expires}
	c := client{ip: model.TruncateUTF8(in.Client.IP, maxClientIP), userAgent: model.TruncateUTF8(in.Client.UserAgent, maxUserAgent)}
	if in.Kind == authn.SessionBrowser {
		issued.Token, err = p.commitBrowser(ctx, c, pl)
	} else {
		err = p.insert(ctx, sessionRow{
			ID: pl.SID, AccountID: pl.Account, Kind: pl.Kind, Data: string(blob),
			ClientIP: c.ip, UserAgent: c.userAgent, CreatedAt: now, LastSeenAt: now,
			ExpiresAt: nullTime(pl.Expires),
		})
	}
	if err != nil {
		return authn.Issued{}, err
	}

	p.evict(ctx, in.Account, id)
	p.count(ctx, opCreated)
	return issued, nil
}

const maxUserAgent = 200

func (p *product) commitBrowser(ctx context.Context, c client, pl payload) (string, error) {
	sctx, err := p.sm.Load(context.WithValue(ctx, clientKey, c), "")
	if err != nil {
		return "", unavailable("starting a session", err)
	}
	p.sm.Put(sctx, payloadKey, pl)
	token, _, err := p.sm.Commit(sctx)
	if err != nil {
		if errors.Is(err, authn.ErrInvalidArgument) || errors.Is(err, authn.ErrUnavailable) {
			return "", err
		}
		return "", unavailable("committing a session", err)
	}
	return token, nil
}

// checkNew refuses what a session is not made from: a kind Create does not make,
// an account no session may be for, a method set, level or provider the gateway
// would refuse at the exit check, and attributes beyond the bounds.
func checkNew(in authn.NewSession) error {
	switch in.Kind {
	case authn.SessionBrowser, authn.SessionToken:
	case authn.SessionService:
		return fmt.Errorf("%w: a service session is made through ForCredential", authn.ErrInvalidArgument)
	default:
		return fmt.Errorf("%w: the session kind is not browser or token", authn.ErrInvalidArgument)
	}
	if err := checkAccount(in.Account); err != nil {
		return err
	}
	if err := checkMethods(in.Methods); err != nil {
		return err
	}
	if err := checkAssurance(in.Assurance); err != nil {
		return err
	}
	if err := checkProvider(in.Provider); err != nil {
		return err
	}
	if in.IDPAssured && (in.Assurance != authn.AssuranceMulti || in.Provider == "") {
		return fmt.Errorf("%w: an IdP's multi-factor declaration needs the multi-factor level and a provider", authn.ErrInvalidArgument)
	}
	if in.AuthTime.IsZero() {
		return fmt.Errorf("%w: the authentication time is not set", authn.ErrInvalidArgument)
	}
	if in.AbsoluteTTL < 0 {
		return fmt.Errorf("%w: the absolute period is negative", authn.ErrInvalidArgument)
	}
	return model.CheckIDPAttributes(in.Attributes)
}

func checkAccount(id authn.AccountID) error {
	if _, err := authn.ParseAccountID(string(id)); err != nil {
		return fmt.Errorf("%w: the account identifier is not well formed", authn.ErrInvalidArgument)
	}
	if id.IsStatic() {
		return fmt.Errorf("%w: a subject of the static table has no session", authn.ErrInvalidArgument)
	}
	return nil
}

// period computes the absolute period of a new session: the setting of its kind,
// or the shorter the caller asked for, which may not exceed it, and for a session
// that came from SSO the shorter of that and the SSO setting.
func period(in authn.NewSession, tm timing) (time.Duration, error) {
	limit := tm.absolute
	if in.Kind == authn.SessionToken {
		limit = tm.tokenAbsolute
	}
	ttl := limit
	if in.AbsoluteTTL > 0 {
		if in.AbsoluteTTL > limit {
			return 0, fmt.Errorf("%w: the absolute period is longer than the setting for the kind", authn.ErrInvalidArgument)
		}
		ttl = in.AbsoluteTTL
	}
	if in.Provider != "" && tm.ssoAbsolute < ttl {
		ttl = tm.ssoAbsolute
	}
	return ttl, nil
}

// evict revokes the account's least recently used sessions while it holds more
// than the setting allows, never the one just made. The session was made, so a
// failure here is written and does not undo the call.
func (p *product) evict(ctx context.Context, account authn.AccountID, keep authn.SessionID) {
	limit := p.settings.maxPerAccount()
	rows, err := p.liveRows(ctx, account, p.now(), "last_seen_at ASC, id ASC")
	if err != nil {
		p.logger.Warn(diagnosticPrefix+ModuleName+": counting an account's sessions failed, so the limit was not enforced",
			slog.String("account", string(account)), slog.Any("error", err))
		return
	}
	excess := len(rows) - limit
	for _, r := range rows {
		if excess <= 0 {
			break
		}
		if r.ID == string(keep) {
			continue
		}
		if err := p.Revoke(ctx, authn.SessionID(r.ID), authn.ReasonEvicted); err != nil {
			p.logger.Warn(diagnosticPrefix+ModuleName+": revoking the least recently used session failed",
				slog.String("account", string(account)), slog.Any("error", err))
			return
		}
		excess--
	}
}

// Get reads one session row by its public identifier, a revoked or expired one
// included.
func (p *product) Get(ctx context.Context, id authn.SessionID) (authn.SessionInfo, error) {
	row, err := p.byID(ctx, id)
	if errors.Is(err, errNoRow) {
		return authn.SessionInfo{}, authn.ErrSessionNotFound
	}
	if err != nil {
		return authn.SessionInfo{}, err
	}
	return p.info(row), nil
}

func (p *product) info(r sessionRow) authn.SessionInfo {
	info := authn.SessionInfo{
		ID:              authn.SessionID(r.ID),
		Account:         authn.AccountID(r.AccountID),
		Kind:            authn.SessionKind(r.Kind),
		ImpersonationID: r.ImpersonationID.String,
		CredentialID:    r.CredentialID.String,
		CreatedAt:       r.CreatedAt,
		LastSeenAt:      r.LastSeenAt,
		Revoked:         r.RevokedAt.Valid,
	}
	if r.ExpiresAt.Valid {
		info.ExpiresAt = r.ExpiresAt.Time
	}
	if pl, ok, err := decodePayload([]byte(r.Data)); err == nil && ok {
		info.Assurance = pl.Assurance
		info.AuthTime = pl.AuthTime
	}
	return info
}

// List lists the account's live browser and token sessions, the most recently
// made first.
func (p *product) List(ctx context.Context, account authn.AccountID) ([]authn.SessionInfo, error) {
	rows, err := p.liveRows(ctx, account, p.now(), "created_at DESC, id DESC")
	if err != nil {
		return nil, err
	}
	out := make([]authn.SessionInfo, len(rows))
	for i, r := range rows {
		out[i] = p.info(r)
	}
	return out, nil
}

// Revoke revokes a session, idempotently, and clears what this replica cached
// for it. The row's revoked_at is the only thing that makes a session revoked.
func (p *product) Revoke(ctx context.Context, id authn.SessionID, reason string) error {
	if !authn.ValidPayloadReasonClass(reason) {
		return fmt.Errorf("%w: the reason is not one of the closed classes", authn.ErrInvalidArgument)
	}
	row, err := p.byID(ctx, id)
	if errors.Is(err, errNoRow) {
		p.caches().forgetSession(id)
		return nil
	}
	if err != nil {
		return err
	}
	n, err := p.revokeRow(ctx, id, reason, p.now())
	if err != nil {
		return err
	}
	p.caches().forgetSession(id, row.TokenHash.String)
	if n > 0 {
		p.count(ctx, opRevoked)
		p.publishRevoked(ctx, authn.SessionRevoked{
			SessionID: string(id), AccountID: row.AccountID, Scope: "one", ReasonClass: reason,
		})
	}
	return nil
}

// RevocableSessions lists the identifiers RevokeAccount ends: the account's
// unrevoked browser and token rows, in the order the rows come back.
func (p *product) RevocableSessions(ctx context.Context, account authn.AccountID) ([]authn.SessionID, error) {
	return p.returnedIDs(ctx, "listing an account's revocable sessions",
		"SELECT id FROM "+tableName+" WHERE "+revocableCondition, string(account))
}

// RevokeAccount revokes the account's browser and token sessions in one
// statement, keeping the one named by except, and reports the identifiers it
// revoked. The statement returns them itself, so what a caller is told and what
// the statement changed are one set rather than two readings of the table.
func (p *product) RevokeAccount(ctx context.Context, account authn.AccountID, except authn.SessionID, reason string) ([]authn.SessionID, error) {
	if !authn.ValidPayloadReasonClass(reason) {
		return nil, fmt.Errorf("%w: the reason is not one of the closed classes", authn.ErrInvalidArgument)
	}
	statement := "UPDATE " + tableName + " SET revoked_at = ?, revoke_reason = ? " +
		"WHERE " + revocableCondition
	args := []any{clock.UTC(p.now()), reason, string(account)}
	if except != "" {
		statement += " AND id <> ?"
		args = append(args, string(except))
	}
	revoked, err := p.returnedIDs(ctx, "revoking an account's sessions", statement+" RETURNING id", args...)
	if err != nil {
		return nil, err
	}
	p.caches().forgetAccount(account)
	if len(revoked) > 0 {
		p.count(ctx, opRevokedAll)
		p.publishRevoked(ctx, authn.SessionRevoked{AccountID: string(account), Scope: "all", ReasonClass: reason})
	}
	return revoked, nil
}

// Rotate gives a browser session a new token. The other kinds have no token to
// change and the call leaves them as they are.
func (p *product) Rotate(ctx context.Context, id authn.SessionID) (authn.Issued, error) {
	return p.change(ctx, id, change{rotate: true})
}

// Elevate records a completed step-up on the session and gives a browser session
// a new token.
func (p *product) Elevate(ctx context.Context, id authn.SessionID, e authn.Elevation) (authn.Issued, error) {
	e = e.Clone()
	if err := checkMethods(e.Methods); err != nil {
		return authn.Issued{}, err
	}
	if err := checkAssurance(e.Assurance); err != nil {
		return authn.Issued{}, err
	}
	if e.AuthTime.IsZero() {
		return authn.Issued{}, fmt.Errorf("%w: the authentication time is not set", authn.ErrInvalidArgument)
	}
	issued, err := p.change(ctx, id, change{rotate: true, elevation: &e})
	if err == nil {
		p.count(ctx, opElevated)
	}
	return issued, err
}

// SetImpersonation sets the impersonation record attached to the session, the
// empty string ending it. Attaching a record gives a browser session a new
// token; ending one only clears the marker, and the token and its expiry stay,
// so a request still in flight with the token when the record ends is not
// refused.
func (p *product) SetImpersonation(ctx context.Context, id authn.SessionID, grantID string) (authn.Issued, error) {
	if len(grantID) > maxGrantIDLen || strings.IndexFunc(grantID, unicode.IsControl) >= 0 {
		return authn.Issued{}, fmt.Errorf("%w: the record identifier is not well formed", authn.ErrInvalidArgument)
	}
	return p.change(ctx, id, change{rotate: grantID != "", impersonation: &grantID})
}

// change is one conditional update of a session row, with a new token for a
// browser session when it asks for one. The update names the token it replaces
// and the row's not being revoked, so a revocation or another change that comes
// first leaves the row as it was and the call reads the row again to say why.
type change struct {
	rotate        bool
	elevation     *authn.Elevation
	impersonation *string
}

// apply is what the change comes to for the row it is applied to.
func (c change) changesRow() bool { return c.elevation != nil || c.impersonation != nil }

func (p *product) change(ctx context.Context, id authn.SessionID, c change) (authn.Issued, error) {
	for range retryLimit {
		row, err := p.byID(ctx, id)
		if errors.Is(err, errNoRow) {
			return authn.Issued{}, authn.ErrSessionNotFound
		}
		if err != nil {
			return authn.Issued{}, err
		}
		if row.RevokedAt.Valid {
			return authn.Issued{}, authn.ErrSessionRevoked
		}
		now := p.now()
		if !row.live(now) {
			return authn.Issued{}, authn.ErrSessionNotFound
		}
		issued := authn.Issued{ID: id, ExpiresAt: row.ExpiresAt.Time}
		if authn.SessionKind(row.Kind) == authn.SessionService {
			if c.changesRow() {
				return authn.Issued{}, fmt.Errorf("%w: a service session has no step-up and no impersonation", authn.ErrInvalidArgument)
			}
			return issued, nil
		}
		if !c.changesRow() && authn.SessionKind(row.Kind) != authn.SessionBrowser {
			return issued, nil
		}

		var sets []string
		var args []any
		if c.elevation != nil {
			data, dataErr := elevatedData(row, *c.elevation)
			if dataErr != nil {
				return authn.Issued{}, dataErr
			}
			sets = append(sets, "data = ?")
			args = append(args, data)
		}
		if c.impersonation != nil {
			sets = append(sets, "impersonation_id = ?")
			args = append(args, optionalText(*c.impersonation))
		}
		browser := authn.SessionKind(row.Kind) == authn.SessionBrowser
		if browser && c.rotate {
			token, tokenErr := newToken()
			if tokenErr != nil {
				return authn.Issued{}, tokenErr
			}
			sets = append(sets, "token_hash = ?")
			args = append(args, storeKey(token))
			issued.Token = token
		}
		if len(sets) == 0 {
			return issued, nil
		}

		statement := "UPDATE " + tableName + " SET " + strings.Join(sets, ", ") + " WHERE id = ? AND revoked_at IS NULL"
		args = append(args, string(id))
		if browser && c.rotate {
			if row.TokenHash.Valid {
				statement += " AND token_hash = ?"
				args = append(args, row.TokenHash.String)
			} else {
				statement += " AND token_hash IS NULL"
			}
		}
		n, err := p.exec(ctx, "changing a session", statement, args...)
		if err != nil {
			return authn.Issued{}, err
		}
		if n == 1 {
			p.caches().forgetSession(id, row.TokenHash.String)
			return issued, nil
		}
	}
	return authn.Issued{}, errChanged
}

func elevatedData(row sessionRow, e authn.Elevation) (string, error) {
	pl, ok, err := decodePayload([]byte(row.Data))
	if err != nil || !ok || pl.SID != row.ID {
		return "", errCorrupt
	}
	if e.IDPAssured && (e.Assurance != authn.AssuranceMulti || pl.Provider == "") {
		return "", fmt.Errorf("%w: an IdP's multi-factor declaration needs the multi-factor level and a provider", authn.ErrInvalidArgument)
	}
	pl.Methods = e.Methods
	pl.Assurance = e.Assurance
	pl.IDPAssured = e.IDPAssured
	pl.AuthTime = clock.UTC(e.AuthTime)
	blob, err := encodePayload(pl)
	if err != nil {
		return "", fmt.Errorf("%w: the payload cannot be written", authn.ErrInvalidArgument)
	}
	if len(blob) > maxDataBytes {
		return "", fmt.Errorf("%w: the payload is longer than %d bytes", authn.ErrInvalidArgument, maxDataBytes)
	}
	return string(blob), nil
}

// ForCredential gets or makes the service session of a secret in one atomic
// step against the uniqueness of the credential identifier.
func (p *product) ForCredential(ctx context.Context, account authn.AccountID, credentialID string, expires *time.Time) (authn.SessionID, error) {
	if err := checkAccount(account); err != nil {
		return "", err
	}
	if credentialID == "" || len(credentialID) > maxGrantIDLen || strings.IndexFunc(credentialID, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%w: the credential identifier is not well formed", authn.ErrInvalidArgument)
	}
	// The account is read from the store and not from the cache: the instant
	// after which a session is void decides whether the row is renewed, and a
	// snapshot older than that would leave a restored client with a row that
	// still fails the check.
	acct, err := p.accounts.Get(ctx, account)
	if err != nil {
		return "", accountError(err)
	}

	var exp time.Time
	if expires != nil {
		exp = clock.UTC(*expires)
	}
	for range retryLimit {
		now := p.now()
		id := authn.SessionID(event.NewID())
		pl := payload{
			SID: string(id), Account: string(account), Kind: string(authn.SessionService),
			Methods: []authn.Method{authn.MethodClientSecret}, Assurance: authn.AssuranceSingle,
			AuthTime: now, Expires: exp,
		}
		blob, err := encodePayload(pl)
		if err != nil {
			return "", fmt.Errorf("%w: the payload cannot be written", authn.ErrInvalidArgument)
		}
		created, err := p.insertService(ctx, sessionRow{
			ID: pl.SID, AccountID: pl.Account, Kind: pl.Kind, CredentialID: textOf(credentialID),
			Data: string(blob), CreatedAt: now, LastSeenAt: now, ExpiresAt: nullTime(exp),
		})
		if err != nil {
			return "", err
		}
		if created {
			return id, nil
		}

		row, err := p.byCredential(ctx, credentialID)
		if errors.Is(err, errNoRow) {
			continue
		}
		if err != nil {
			return "", err
		}
		if row.AccountID != string(account) {
			return "", fmt.Errorf("%w: the credential belongs to another account", authn.ErrInvalidArgument)
		}
		if row.RevokedAt.Valid {
			return "", authn.ErrSessionRevoked
		}
		n, err := p.renewService(ctx, row.ID, exp, acct.ValidAfter, now)
		if err != nil {
			return "", err
		}
		if n == 1 {
			p.caches().forgetSession(authn.SessionID(row.ID))
			return authn.SessionID(row.ID), nil
		}
	}
	return "", errChanged
}

// renewService updates the expiry of a live service row to the secret's current
// one and, when the row is older than the instant after which a session is void,
// sets its creation time to now. It never touches revoked_at, and a revoked row
// is not changed.
func (p *product) renewService(ctx context.Context, id string, exp, validAfter, now time.Time) (int64, error) {
	if validAfter.IsZero() {
		return p.exec(ctx, "renewing a service session",
			"UPDATE "+tableName+" SET expires_at = ? WHERE id = ? AND revoked_at IS NULL", optionalTime(exp), id)
	}
	return p.exec(ctx, "renewing a service session",
		"UPDATE "+tableName+" SET expires_at = ?, created_at = CASE WHEN created_at < ? THEN ? ELSE created_at END "+
			"WHERE id = ? AND revoked_at IS NULL",
		optionalTime(exp), clock.UTC(validAfter), clock.UTC(now), id)
}

// RevokeCredential revokes the service session of a secret, idempotently.
func (p *product) RevokeCredential(ctx context.Context, credentialID, reason string) error {
	if !authn.ValidPayloadReasonClass(reason) {
		return fmt.Errorf("%w: the reason is not one of the closed classes", authn.ErrInvalidArgument)
	}
	row, err := p.byCredential(ctx, credentialID)
	if errors.Is(err, errNoRow) {
		return nil
	}
	if err != nil {
		return err
	}
	return p.Revoke(ctx, authn.SessionID(row.ID), reason)
}

// SetCookie writes the Set-Cookie header of a browser session's token. An Issued
// with no token has no cookie to set and the call writes nothing.
func (p *product) SetCookie(w http.ResponseWriter, in authn.Issued) {
	if in.Token == "" {
		return
	}
	expires := in.ExpiresAt
	if expires.IsZero() {
		// The library reads a zero expiry as a request to delete the cookie.
		expires = p.now().Add(time.Hour)
	}
	p.sm.WriteSessionCookie(p.cookieCtx, w, in.Token, expires)
}

// ClearCookie writes a Set-Cookie header that has the browser drop the cookie.
func (p *product) ClearCookie(w http.ResponseWriter) {
	p.sm.WriteSessionCookie(p.cookieCtx, w, "", time.Time{})
}

// clearingCookie is the cookie a reply operation attaches with WithCookies, built
// as a value because a reply has no response writer to hand. cookiename.Cookie
// builds it from the same configuration the manager's cookie comes from, so it
// names the cookie the manager wrote, with no value and Max-Age=0, which a
// browser acts on ahead of any Expires, as it does for the flow and reset
// cookies' clears.
func (p *product) clearingCookie() *http.Cookie {
	return p.cookie.cookie("", -1)
}

// Current finds the live session the request's cookie belongs to, by the digest
// of the token. It is no defence and has nothing to do with authentication: a
// login that completes uses it to revoke the session the request came with.
func (p *product) Current(r *http.Request) (authn.SessionID, bool, error) {
	c, err := r.Cookie(p.cookie.name())
	if err != nil || c.Value == "" || len(c.Value) > maxTokenLen {
		return "", false, nil
	}
	row, err := p.byStoreKey(r.Context(), storeKey(c.Value), p.now())
	if errors.Is(err, errNoRow) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return authn.SessionID(row.ID), true, nil
}

func accountError(err error) error {
	if errors.Is(err, authn.ErrAccountNotFound) || errors.Is(err, authn.ErrUnavailable) {
		return err
	}
	return fmt.Errorf("%w: reading the account: %w", authn.ErrUnavailable, err)
}

func textOf(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func nullTime(t time.Time) sql.NullTime { return sql.NullTime{Time: clock.UTC(t), Valid: !t.IsZero()} }
