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
// the session index tracks.
const cacheCapacity = 10_000

// sessionEntry is what the session cache holds for a session: the status and
// account read from the row, the session identifier, and for a browser session
// its idle period's end. It is shared and read-only but for lastSeen.
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

func (e *sessionEntry) lapsed(now time.Time) bool {
	return !e.idleExpires.IsZero() && !now.Before(e.idleExpires)
}

type grantEntry struct {
	grant  authn.ImpersonationGrant
	active bool
}

// cacheSet is the three process-local caches built for one value of check-ttl,
// keyed by token digest, session identifier, account identifier and grant
// identifier; no key holds a token.
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

func digestKey(key string) string { return "t:" + key }

func sessionKey(id authn.SessionID) string { return "s:" + string(id) }

func accountKey(account authn.AccountID) string { return "a:" + string(account) }

// putSession stores a session entry under its identifier and, when key is set,
// under the token digest too; nothing is stored when an invalidation has
// happened since epoch.
func (c *cacheSet) putSession(e *sessionEntry, key string, epoch uint64) {
	stored := c.sessions.PutIfEpoch(sessionKey(e.id), e, epoch)
	if key != "" && c.sessions.PutIfEpoch(digestKey(key), e, epoch) {
		stored = true
	}
	if stored {
		c.index.add(e.id, key, e.status.Account)
	}
}

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

func (c *cacheSet) forgetGrant(id string) { c.grants.Invalidate(id) }

func (c *cacheSet) purge() {
	c.sessions.Purge()
	c.accounts.Purge()
	c.grants.Purge()
	c.index.clear()
}

// sessionIndex maps a session's public identifier to the digest key its entry is
// cached under and to its account, so a lookup by token is reachable by
// identifier or account; removing an identifier it does not hold is a no-op.
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

func (x *sessionIndex) size() int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.recency.Len()
}
