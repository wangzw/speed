package token

import (
	"context"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/acctcache"
)

// cacheCapacity is the most entries each of the module's two caches holds. It is
// a size fixed at construction and no setting: how large a cache is is not a
// knob an operator turns, where how long an entry is believed, check-ttl, is.
const cacheCapacity = 10_000

type grantEntry struct {
	grant  authn.ImpersonationGrant
	active bool
}

// cacheSet is the two process-local caches the verifying backend reads through,
// built for one value of check-ttl. A change of the setting builds a new set and
// leaves the old one to whoever still holds it.
//
// The account cache is keyed by the account identifier and the impersonation
// cache by the identifier of the record; neither key holds a token. The session
// a token names is not cached here: Sessions.Status is itself cached by the
// session module for the same check-ttl, and the token backend reads it through
// that seam.
type cacheSet struct {
	ttl      time.Duration
	accounts *acctcache.Cache[authn.Account]
	grants   *acctcache.Cache[grantEntry]
}

func newCacheSet(ttl time.Duration, now func() time.Time) *cacheSet {
	return &cacheSet{
		ttl:      ttl,
		accounts: acctcache.New[authn.Account](cacheCapacity, ttl, now),
		grants:   acctcache.New[grantEntry](cacheCapacity, ttl, now),
	}
}

// caches is the cache set for the current value of check-ttl. A change of the
// setting replaces the set, so a lifetime that was lowered takes effect at once
// and one that was raised does not keep serving from entries made under the
// shorter one.
func (p *product) caches() *cacheSet {
	ttl := p.settings.checkTTL()
	for {
		cs := p.cacheSlot.Load()
		if cs != nil && cs.ttl == ttl {
			return cs
		}
		fresh := newCacheSet(ttl, p.clock.Now)
		if p.cacheSlot.CompareAndSwap(cs, fresh) {
			return fresh
		}
	}
}

// accountFor reads the account snapshot through the cache. A miss reads the
// store and keeps what it read with the epoch taken before the read, so a
// snapshot that predates a change is not stored. A failure is not cached, so a
// database that cannot be reached fails a cold read rather than serving nothing.
func (p *product) accountFor(ctx context.Context, id authn.AccountID) (authn.Account, error) {
	cs := p.caches()
	if a, ok := cs.accounts.Get(string(id)); ok {
		return a, nil
	}
	epoch := cs.accounts.Epoch()
	a, err := p.accounts.Get(ctx, id)
	if err != nil {
		return authn.Account{}, err
	}
	cs.accounts.PutIfEpoch(string(id), a, epoch)
	return a, nil
}

func (p *product) activeGrant(ctx context.Context, id string) (authn.ImpersonationGrant, bool, error) {
	cs := p.caches()
	if g, ok := cs.grants.Get(id); ok {
		return g.grant, g.active, nil
	}
	epoch := cs.grants.Epoch()
	grant, active, err := p.impersonation.Active(ctx, id)
	if err != nil {
		return authn.ImpersonationGrant{}, false, err
	}
	cs.grants.PutIfEpoch(id, grantEntry{grant: grant, active: active}, epoch)
	return grant, active, nil
}
