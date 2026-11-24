package admission

import (
	"sync"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/tenant"
)

type kind int

const (
	kindMember kind = iota
	kindExempt
	kindAdmit
	kindCount
)

const maxEntries = 1 << 16

type cacheKey struct {
	account authn.AccountID
	tenant  tenant.ID
}

type entry struct {
	value bool
	at    time.Time
}

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

// begin returns the epoch a read starts in; put drops an answer read in an earlier epoch.
func (c *decisionCache) begin() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epoch
}

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

func (c *decisionCache) forget(key cacheKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	for k := range c.tables {
		delete(c.tables[k], key)
	}
}

func (c *decisionCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	for k := range c.tables {
		clear(c.tables[k])
	}
}

func (c *decisionCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for k := range c.tables {
		n += len(c.tables[k])
	}
	return n
}
