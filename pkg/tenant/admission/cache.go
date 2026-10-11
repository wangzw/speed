package admission

import (
	"sync"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/tenant"
)

// kind is which answer a cache entry holds. The three are kept apart because
// they answer different questions about one key: whether the account is a
// member, whether a member is exempt from the SSO requirement, and whether the
// permission model lets a non-member in.
type kind int

const (
	kindMember kind = iota
	kindExempt
	kindAdmit
	kindCount
)

// maxEntries bounds each kind's table. A key is an (account, tenant) pair a
// request named, so the number of keys is chosen by callers, and an entry lives
// at most the lifetime ceiling: the bound is what keeps a sweep of accounts
// across tenants from growing the process.
const maxEntries = 1 << 16

// cacheKey is what an entry is about.
type cacheKey struct {
	account authn.AccountID
	tenant  tenant.ID
}

// entry is one remembered answer and the instant it was read.
type entry struct {
	value bool
	at    time.Time
}

// decisionCache remembers the answers the layer reads from other modules, in the
// process, for the lifetime the settings give.
//
// It is the only state the layer keeps between requests, and a request that
// finds an answer here reads nothing. Errors are never put in: a failed read
// is not an answer, and remembering one would turn an outage of a second into
// a refusal of the lifetime.
//
// A change event, or a settings change, only makes entries go away sooner; the
// lifetime bounds how stale an entry can be whether or not anything arrives.
type decisionCache struct {
	now func() time.Time

	mu     sync.Mutex
	tables [kindCount]map[cacheKey]entry
	epoch  uint64
}

func newDecisionCache(now func() time.Time) *decisionCache {
	c := &decisionCache{now: now}
	for k := range c.tables {
		c.tables[k] = make(map[cacheKey]entry)
	}
	return c
}

// get returns the remembered answer, when there is one that was read less than
// ttl ago. A lifetime that is not positive remembers nothing.
//
// The lifetime is judged on every read against the setting as it stands now, so
// shortening it takes effect on the entries already held.
func (c *decisionCache) get(k kind, key cacheKey, ttl time.Duration) (value, found bool) {
	if ttl <= 0 {
		return false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, held := c.tables[k][key]
	if !held || c.now().Sub(e.at) >= ttl {
		return false, false
	}
	return e.value, true
}

// begin returns the epoch a read starts in. A read that straddles an
// invalidation must not leave its answer behind: the event said the answer it
// was reading may already be out of date, and the answer arriving after the
// event would sit in the table for the whole lifetime.
func (c *decisionCache) begin() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epoch
}

// put remembers an answer read in the epoch begin returned. It does nothing when
// the lifetime is not positive, when an invalidation has happened since, or when
// the table is full of entries that are all still current.
func (c *decisionCache) put(k kind, key cacheKey, value bool, ttl time.Duration, epoch uint64) {
	if ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if epoch != c.epoch {
		return
	}
	now := c.now()
	table := c.tables[k]
	if _, replace := table[key]; !replace && len(table) >= maxEntries {
		for old, e := range table {
			if now.Sub(e.at) >= ttl {
				delete(table, old)
			}
		}
		if len(table) >= maxEntries {
			return
		}
	}
	table[key] = entry{value: value, at: now}
}

// forget drops every answer about one account in one tenant. It is idempotent.
func (c *decisionCache) forget(key cacheKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	for k := range c.tables {
		delete(c.tables[k], key)
	}
}

// clear drops everything. It is what a change event that cannot be read down to
// a key causes: dropping too much costs a read, and dropping too little keeps a
// removed member in.
func (c *decisionCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	for k := range c.tables {
		clear(c.tables[k])
	}
}

// size is how many entries the tables hold, for the tests that hold the bound.
func (c *decisionCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for k := range c.tables {
		n += len(c.tables[k])
	}
	return n
}
