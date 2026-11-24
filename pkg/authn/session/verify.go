package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
)

func (p *product) Name() string { return backendName }

// Accepts declares what this backend takes and what it may hand back: the session
// cookie, accounts of the library namespace, and the impersonation and SSO facts
// a session carries. It claims no Authorization scheme.
func (p *product) Accepts() authn.Accepts {
	return authn.Accepts{
		Cookies:       []string{p.cookie.name()},
		Accounts:      "",
		Impersonation: true,
		SSO:           true,
	}
}

// Verify checks one session cookie and returns Denied, Unavailable or
// Authenticated. An unreachable database is Unavailable and leaves the cookie
// alone; a cookie that does not hold is Denied and the gateway clears it.
func (p *product) Verify(ctx context.Context, c authn.Credential) authn.Verdict {
	if c.Value == "" || len(c.Value) > maxTokenLen {
		return authn.Denied(fmt.Errorf("%s: the cookie value cannot be a session token", ModuleName))
	}
	key := storeKey(c.Value)
	cs := p.caches()

	e, ok := p.cached(cs, digestKey(key))
	if !ok {
		var verdict *authn.Verdict
		e, verdict = p.readCookie(ctx, cs, c.Value, key)
		if verdict != nil {
			return *verdict
		}
	}
	st := p.served(e)
	if !st.Active {
		return authn.Denied(fmt.Errorf("%s: the session is not valid", ModuleName))
	}

	acct, err := p.accountFor(ctx, cs, st.Account, false)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return authn.Denied(fmt.Errorf("%s: the account does not exist", ModuleName))
	case err != nil:
		return authn.Unavailable(err)
	case !accountPermits(acct, st.CreatedAt):
		return authn.Denied(fmt.Errorf("%s: the account is not enabled or the session is older than the account's valid-after instant", ModuleName))
	}

	principal := authn.Principal{
		Account:    st.Account,
		Session:    e.id,
		Methods:    st.Methods,
		Assurance:  st.Assurance,
		IDPAssured: st.IDPAssured,
		AuthTime:   st.AuthTime,
		Provider:   st.Provider,
		Attributes: mergeAttributes(acct.Attributes, st.Attributes),
	}

	if st.ImpersonationID != "" {
		impersonated, verdict := p.impersonate(ctx, cs, st, principal)
		if verdict != nil {
			return *verdict
		}
		principal = impersonated
	}

	p.touch(ctx, e)
	return authn.Authenticated(principal)
}

func (p *product) readCookie(ctx context.Context, cs *cacheSet, token, key string) (*sessionEntry, *authn.Verdict) {
	epoch := cs.sessions.Epoch()
	sink := &rowSink{}
	sctx, err := p.sm.Load(context.WithValue(ctx, sinkKey, sink), token)
	if err != nil {
		var v authn.Verdict
		if errors.Is(err, errCorrupt) {
			v = authn.Denied(fmt.Errorf("%s: the stored session is not readable", ModuleName))
		} else {
			v = authn.Unavailable(unavailable("reading the session", err))
		}
		return nil, &v
	}
	// A token the store does not know makes the library start an empty session
	// instead, which has no payload: a token a client chose is no session.
	pl, held := p.sm.Get(sctx, payloadKey).(payload)
	if !held || sink.row == nil || pl.SID == "" {
		v := authn.Denied(fmt.Errorf("%s: no such session", ModuleName))
		return nil, &v
	}

	e, err := p.entryOf(ctx, cs, *sink.row, false)
	if err != nil {
		v := authn.Unavailable(err)
		return nil, &v
	}
	cs.putSession(e, key, epoch)
	return e, nil
}

// impersonate turns the principal into the impersonation's when the record is
// still valid; a record that has ended leaves the principal as it is, which is
// no refusal.
func (p *product) impersonate(ctx context.Context, cs *cacheSet, st authn.SessionStatus, own authn.Principal) (authn.Principal, *authn.Verdict) {
	if p.impersonation == nil {
		v := authn.Denied(fmt.Errorf("%s: the session names an impersonation record and this command has no "+
			"impersonation capability to ask", ModuleName))
		return authn.Principal{}, &v
	}
	g, err := p.activeGrant(ctx, cs, st.ImpersonationID)
	if err != nil {
		v := authn.Unavailable(fmt.Errorf("%s: asking whether the impersonation holds: %w", ModuleName, err))
		return authn.Principal{}, &v
	}
	if !g.active {
		return own, nil
	}

	target, err := p.accountFor(ctx, cs, g.grant.Target, false)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		v := authn.Denied(fmt.Errorf("%s: the impersonated account does not exist", ModuleName))
		return authn.Principal{}, &v
	case err != nil:
		v := authn.Unavailable(err)
		return authn.Principal{}, &v
	case target.Status != authn.StatusActive:
		v := authn.Denied(fmt.Errorf("%s: the impersonated account is not enabled", ModuleName))
		return authn.Principal{}, &v
	}

	// The identity is the target's: its account and attributes. The
	// authentication facts — level, IdP declaration and provider — stay the
	// operator's own, since the operator is the one who authenticated.
	imp := own
	imp.Account = g.grant.Target
	imp.Actor = st.Account
	imp.Scope = g.grant.Tenant
	imp.Attributes = mergeAttributes(target.Attributes, nil)
	return imp, nil
}

func (p *product) activeGrant(ctx context.Context, cs *cacheSet, id string) (grantEntry, error) {
	if g, ok := cs.grants.Get(id); ok {
		return g, nil
	}
	epoch := cs.grants.Epoch()
	grant, active, err := p.impersonation.Active(ctx, id)
	if err != nil {
		return grantEntry{}, err
	}
	g := grantEntry{grant: grant, active: active}
	cs.grants.PutIfEpoch(id, g, epoch)
	return g, nil
}

// touch extends a browser session's idle period when the touch interval has run
// out, letting one of the concurrent requests write. A failed write is logged at
// Debug only: it costs precision of the idle period, nothing else.
func (p *product) touch(ctx context.Context, e *sessionEntry) {
	if e.status.Kind != authn.SessionBrowser {
		return
	}
	now := p.now()
	interval := p.touchInterval()
	last := e.lastSeen.Load()
	if now.Sub(time.Unix(0, last)) < interval || !e.lastSeen.CompareAndSwap(last, now.UnixNano()) {
		return
	}
	if _, err := p.extend(ctx, e.id, now); err != nil {
		p.logger.Debug(diagnosticPrefix+ModuleName+": extending a session's idle period failed",
			slog.Any("error", err))
	}
}

// extend writes the new idle period of a browser session: last_seen_at is now
// and the idle expiry is the idle timeout from now. The write is conditional on
// the session not being revoked, so it cannot bring one back.
func (p *product) extend(ctx context.Context, id authn.SessionID, now time.Time) (int64, error) {
	idle := p.settings.timing().idle
	return p.touchRow(ctx, id, clock.UTC(now), clock.UTC(now).Add(idle))
}

// touchInterval is how seldom a session is written for being used: the setting,
// never more than half the idle timeout, so the period cannot end under a user
// who never stopped.
func (p *product) touchInterval() time.Duration {
	return min(p.settings.touchInterval(), p.settings.timing().idle/2)
}
