package session

import (
	"context"
	"errors"
	"maps"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/model"
)

// Status says whether a session is valid and returns what a backend needs to
// build an identity. What is served from the cache is at most check-ttl old.
func (p *product) Status(ctx context.Context, id authn.SessionID) (authn.SessionStatus, error) {
	cs := p.caches()
	if e, ok := p.cached(cs, sessionKey(id)); ok {
		return p.served(e), nil
	}
	epoch := cs.sessions.Epoch()
	e, err := p.readStatus(ctx, cs, id, false)
	if err != nil {
		return authn.SessionStatus{}, err
	}
	cs.putSession(e, "", epoch)
	return p.served(e), nil
}

// StatusFresh is Status read from the store, the account included.
func (p *product) StatusFresh(ctx context.Context, id authn.SessionID) (authn.SessionStatus, error) {
	e, err := p.readStatus(ctx, p.caches(), id, true)
	if err != nil {
		return authn.SessionStatus{}, err
	}
	return e.status.Clone(), nil
}

// cached is the entry the session cache holds under key, unless its idle period
// has run out.
func (p *product) cached(cs *cacheSet, key string) (*sessionEntry, bool) {
	e, ok := cs.sessions.Get(key)
	if !ok || e.lapsed(p.now()) {
		return nil, false
	}
	return e, true
}

// served is the status of a cached entry for a caller: a copy, with the absolute
// period judged again at this instant, the one condition a cached answer can be
// told stale by without a read.
func (p *product) served(e *sessionEntry) authn.SessionStatus {
	st := e.status.Clone()
	if st.Active && !st.ExpiresAt.IsZero() && !p.now().Before(st.ExpiresAt) {
		return inactive(st)
	}
	return st
}

func (p *product) readStatus(ctx context.Context, cs *cacheSet, id authn.SessionID, fresh bool) (*sessionEntry, error) {
	row, err := p.byID(ctx, id)
	if errors.Is(err, errNoRow) {
		return nil, authn.ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	return p.entryOf(ctx, cs, row, fresh)
}

// entryOf builds the cache entry of a row: the status the row and the account's
// state come to. A payload that cannot be read, or that is not the row's, makes
// the session inactive: such a row authenticates nobody.
func (p *product) entryOf(ctx context.Context, cs *cacheSet, row sessionRow, fresh bool) (*sessionEntry, error) {
	e := &sessionEntry{id: authn.SessionID(row.ID)}
	e.lastSeen.Store(row.LastSeenAt.UnixNano())
	e.status = authn.SessionStatus{
		Account:         authn.AccountID(row.AccountID),
		Kind:            authn.SessionKind(row.Kind),
		ImpersonationID: row.ImpersonationID.String,
		CredentialID:    row.CredentialID.String,
		CreatedAt:       row.CreatedAt,
	}
	if row.ExpiresAt.Valid {
		e.status.ExpiresAt = row.ExpiresAt.Time
	}
	if row.IdleExpiresAt.Valid {
		e.idleExpires = row.IdleExpiresAt.Time
	}

	pl, err := rowPayload(row)
	if err != nil {
		return e, nil
	}
	acct, err := p.accountFor(ctx, cs, authn.AccountID(row.AccountID), fresh)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return e, nil
	case err != nil:
		return nil, err
	}

	e.status.Methods = pl.Methods
	e.status.Assurance = pl.Assurance
	e.status.IDPAssured = pl.IDPAssured
	e.status.AuthTime = pl.AuthTime
	e.status.Provider = pl.Provider
	e.status.Attributes = pl.Attributes
	e.status.Active = row.live(p.now()) && accountPermits(acct, row.CreatedAt)
	if !e.status.Active {
		e.status = inactive(e.status)
	}
	return e, nil
}

// inactive is a status with what identifies the session kept and what an identity
// would be built from dropped, so that a caller that reads past Active builds
// nothing it could be admitted with.
func inactive(st authn.SessionStatus) authn.SessionStatus {
	return authn.SessionStatus{
		Account:      st.Account,
		Kind:         st.Kind,
		CredentialID: st.CredentialID,
		CreatedAt:    st.CreatedAt,
		ExpiresAt:    st.ExpiresAt,
	}
}

func accountPermits(a authn.Account, created time.Time) bool {
	return a.Status == authn.StatusActive && !created.Before(a.ValidAfter)
}

func rowPayload(row sessionRow) (payload, error) {
	pl, ok, err := decodePayload([]byte(row.Data))
	if err != nil || !ok {
		return payload{}, errCorrupt
	}
	if pl.SID != row.ID || pl.Account != row.AccountID || pl.Kind != row.Kind {
		return payload{}, errCorrupt
	}
	if model.CheckIDPAttributes(pl.Attributes) != nil {
		return payload{}, errCorrupt
	}
	return pl, nil
}

func (p *product) accountFor(ctx context.Context, cs *cacheSet, id authn.AccountID, fresh bool) (authn.Account, error) {
	if fresh {
		a, err := p.accounts.Get(ctx, id)
		if err != nil {
			return authn.Account{}, accountError(err)
		}
		return a, nil
	}
	if a, ok := cs.accounts.Get(string(id)); ok {
		return a, nil
	}
	epoch := cs.accounts.Epoch()
	a, err := p.accounts.Get(ctx, id)
	if err != nil {
		return authn.Account{}, accountError(err)
	}
	a = a.Clone()
	cs.accounts.PutIfEpoch(string(id), a, epoch)
	return a, nil
}

// mergeAttributes is the attribute set of an identity: the account's, and the
// idp.* entries of the session payload. An entry of the account's under a
// reserved prefix is dropped.
func mergeAttributes(account, idp map[string]string) map[string]string {
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
