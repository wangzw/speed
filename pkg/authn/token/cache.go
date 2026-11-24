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
// built for one value of check-ttl. The session a token names is not cached
// here; the token backend reads it through the session module's own cache.
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

// caches is the cache set for the current value of check-ttl; a change of the
// setting replaces the set, so a lowered lifetime takes effect at once.
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
// store and keeps what it read with the epoch taken before the read; a failure
// is not cached.
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
