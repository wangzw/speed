package session

import (
	"container/list"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/acctcache"
)

// cacheCapacity is the most entries each of the three caches holds, and the most
// the session index tracks. It is a size fixed at construction and no setting:
// how large a cache is is not a knob an operator turns, where how long an entry
// is believed, check-ttl, is.
const cacheCapacity = 10_000

// sessionEntry is what the session cache holds for a session: the status read
// from the row and the account, the identifier the status belongs to and, for a
// browser session, when its idle period was last extended. The entry is shared
// by every request that hits it and is read-only, but for the one atomic
// field.
type sessionEntry struct {
	id     authn.SessionID
	status authn.SessionStatus
	// idleExpires is the idle period's end as the row had it when it was read,
	// zero for a kind that has none.
	idleExpires time.Time
	// lastSeen is the instant, in Unix nanoseconds, the row's last_seen_at was
	// last written or read. A request that finds the touch interval run out
	// since swaps it forward and is the one that writes.
	lastSeen atomic.Int64
}

// lapsed reports whether the idle period the entry was read with has run out. An
// entry that has is not served: the row is read again, which finds the period
// extended when a request touched it since, and ended when none did.
func (e *sessionEntry) lapsed(now time.Time) bool {
	return !e.idleExpires.IsZero() && !now.Before(e.idleExpires)
}

// grantEntry is what the impersonation cache holds: what Impersonation.Active
// answered for a record.
type grantEntry struct {
	grant  authn.ImpersonationGrant
	active bool
}

// cacheSet is the three process-local caches and the index beside the first,
// built for one value of check-ttl. A change of the setting builds a new set and
// leaves the old one to whoever still holds it.
//
// Each cache is an instance of internal/acctcache with its own keys and values,
// and none shares an entry with another. The session cache is keyed by the
// digest of a token and, for Status, by the public identifier of a session; the
// account cache by the account identifier; the impersonation cache by the
// identifier of the record. No key holds a token.
type cacheSet struct {
	ttl      time.Duration
	sessions *acctcache.Cache[*sessionEntry]
	accounts *acctcache.Cache[authn.Account]
	grants   *acctcache.Cache[grantEntry]
	index    *sessionIndex
}

func newCacheSet(ttl time.Duration, now func() time.Time) *cacheSet {
	return &cacheSet{
		ttl:      ttl,
		sessions: acctcache.New[*sessionEntry](cacheCapacity, ttl, now),
		accounts: acctcache.New[authn.Account](cacheCapacity, ttl, now),
		grants:   acctcache.New[grantEntry](cacheCapacity, ttl, now),
		index:    newSessionIndex(cacheCapacity),
	}
}

// digestKey is the session cache key of a token whose store key is key.
func digestKey(key string) string { return "t:" + key }

// sessionKey is the session cache key of a session by its public identifier.
func sessionKey(id authn.SessionID) string { return "s:" + string(id) }

// accountKey is the key an invalidation by account moves the epoch of the
// session cache under, whether or not an entry is held for the account.
func accountKey(account authn.AccountID) string { return "a:" + string(account) }

// putSession stores a session entry under its identifier and, for a browser
// session read by its token, under the digest as well. epoch is the session
// cache's epoch taken before the row was read, and nothing is stored when an
// invalidation has happened since.
func (c *cacheSet) putSession(e *sessionEntry, key string, epoch uint64) {
	stored := c.sessions.PutIfEpoch(sessionKey(e.id), e, epoch)
	if key != "" && c.sessions.PutIfEpoch(digestKey(key), e, epoch) {
		stored = true
	}
	if stored {
		c.index.add(e.id, key, e.status.Account)
	}
}

// forgetSession drops what is cached for one session, under both keys, and moves
// the epoch so that a read already in flight is not stored.
func (c *cacheSet) forgetSession(id authn.SessionID, knownKeys ...string) {
	key, _ := c.index.remove(id)
	c.sessions.Invalidate(sessionKey(id))
	if key != "" {
		c.sessions.Invalidate(digestKey(key))
	}
	for _, k := range knownKeys {
		if k != "" && k != key {
			c.sessions.Invalidate(digestKey(k))
		}
	}
}

// forgetAccount drops what is cached for an account: its snapshot and the
// entries of every session of it the index knows, and moves the epochs.
func (c *cacheSet) forgetAccount(account authn.AccountID) {
	for _, e := range c.index.takeAccount(account) {
		c.sessions.Invalidate(sessionKey(e.id))
		if e.key != "" {
			c.sessions.Invalidate(digestKey(e.key))
		}
	}
	c.sessions.Invalidate(accountKey(account))
	c.accounts.Invalidate(string(account))
}

// forgetGrant drops the cached answer for an impersonation record.
func (c *cacheSet) forgetGrant(id string) { c.grants.Invalidate(id) }

// purge empties the three caches and the index.
func (c *cacheSet) purge() {
	c.sessions.Purge()
	c.accounts.Purge()
	c.grants.Purge()
	c.index.clear()
}

// sessionIndex maps a session's public identifier to the digest key its entry is
// cached under and to its account, so that an invalidation by identifier or by
// account reaches the entry a lookup by token made. It is held outside the cache
// because the cache has one key per entry, and it is bounded as the cache is:
// when it is full the least recently used identifier is dropped, and an entry it
// pointed at is no longer reachable by identifier, which a cache of the same
// size has evicted or is about to. Removing an identifier the index does not hold
// is a no-op.
type sessionIndex struct {
	mu       sync.Mutex
	capacity int
	byID     map[authn.SessionID]*list.Element
	recency  *list.List
}

type indexEntry struct {
	id      authn.SessionID
	key     string
	account authn.AccountID
}

func newSessionIndex(capacity int) *sessionIndex {
	return &sessionIndex{capacity: capacity, byID: make(map[authn.SessionID]*list.Element), recency: list.New()}
}

// add records the session, replacing what was recorded for it, and makes it the
// most recently used.
func (x *sessionIndex) add(id authn.SessionID, key string, account authn.AccountID) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if el, ok := x.byID[id]; ok {
		e, _ := el.Value.(*indexEntry)
		if key != "" {
			e.key = key
		}
		e.account = account
		x.recency.MoveToFront(el)
		return
	}
	for x.recency.Len() >= x.capacity {
		oldest := x.recency.Back()
		old, _ := oldest.Value.(*indexEntry)
		delete(x.byID, old.id)
		x.recency.Remove(oldest)
	}
	x.byID[id] = x.recency.PushFront(&indexEntry{id: id, key: key, account: account})
}

// remove drops the record of a session and returns the digest key it held.
func (x *sessionIndex) remove(id authn.SessionID) (string, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	el, ok := x.byID[id]
	if !ok {
		return "", false
	}
	e, _ := el.Value.(*indexEntry)
	delete(x.byID, id)
	x.recency.Remove(el)
	return e.key, true
}

// takeAccount removes and returns the records of an account's sessions.
func (x *sessionIndex) takeAccount(account authn.AccountID) []indexEntry {
	x.mu.Lock()
	defer x.mu.Unlock()
	var out []indexEntry
	for el := x.recency.Front(); el != nil; {
		next := el.Next()
		if e, _ := el.Value.(*indexEntry); e.account == account {
			out = append(out, *e)
			delete(x.byID, e.id)
			x.recency.Remove(el)
		}
		el = next
	}
	return out
}

func (x *sessionIndex) clear() {
	x.mu.Lock()
	defer x.mu.Unlock()
	clear(x.byID)
	x.recency.Init()
}

// size is the number of sessions recorded.
func (x *sessionIndex) size() int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.recency.Len()
}
