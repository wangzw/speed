// Package acctcache is a bounded in-process cache keyed by a string. It holds
// the account snapshots a backend reads, keyed by account identifier, and it is
// also the container of the password verification cache in internal/password,
// whose value is the time a check held and whose key is a keyed hash of the
// subject, the entry and the password presented.
//
// A backend that verifies a credential by reading the account (the session
// backend checks that the account is still active and that the session was not
// created before the account's valid-after instant) reads it through this cache
// so that a request does not cost a database read. The cache is a way of
// reading less, never a way of deciding: an entry stops being served when its
// lifetime ends, and the module that owns the account clears an entry as soon
// as it learns the account changed. The lifetime is therefore the longest a
// change can go unseen by a replica that did not hear of it, and a lifetime of
// zero turns the cache off for a deployment that cannot accept any.
//
// A read-through has a race the lifetime does not cover: a reader that missed,
// and read the source of truth before a change, can store its snapshot after the
// owner cleared the entry, and the stale snapshot would then be served for a
// whole lifetime. A reader that stores what it read takes [Cache.Epoch] before
// the read and stores with [Cache.PutIfEpoch], which drops the value when any
// [Cache.Invalidate] or [Cache.Purge] happened in between.
//
// A value is shared by every reader that hits it: Get returns what Put stored,
// not a copy. A value that holds a map, slice or pointer is therefore read-only
// to its users, and a user that merges anything into it copies it first.
//
// The package does not define what it stores. The value type is a parameter, and
// each consumer states what it keeps and what its key is made of.
package acctcache

import (
	"container/list"
	"sync"
	"time"
)

// Cache holds up to a fixed number of values for a fixed lifetime each. It is
// safe for concurrent use.
type Cache[V any] struct {
	mu       sync.Mutex
	capacity int
	ttl      time.Duration
	now      func() time.Time
	entries  map[string]*list.Element
	// epoch counts the changes Invalidate and Purge have announced.
	epoch uint64
	// recency runs from the most recently used at the front to the least at the
	// back; the back is what is evicted.
	recency *list.List
}

type entry[V any] struct {
	key     string
	value   V
	expires time.Time
}

// New is a cache of at most capacity entries, each served for ttl after it was
// stored. The storage is allocated here and does not grow past capacity.
//
// A capacity below 1 is raised to 1 rather than refused: a cache that holds one
// entry is a valid and harmless size, and a configuration that asked for less
// has not asked for anything a refusal would protect. A ttl of zero or less
// disables the cache: Put stores nothing and Get never hits, so every read goes
// to the source of truth. now reads the time; nil reads the system clock.
func New[V any](capacity int, ttl time.Duration, now func() time.Time) *Cache[V] {
	if capacity < 1 {
		capacity = 1
	}
	if now == nil {
		now = time.Now
	}
	return &Cache[V]{
		capacity: capacity,
		ttl:      ttl,
		now:      now,
		entries:  make(map[string]*list.Element, capacity),
		recency:  list.New(),
	}
}

// Get is the value stored under key and whether there is one. An entry whose
// lifetime has ended, that is, one stored ttl or longer ago, is not returned and
// is removed. A hit makes the entry the most recently used. The value is the
// stored one, shared with every other reader and not to be modified.
func (c *Cache[V]) Get(key string) (V, bool) {
	var zero V
	if c.ttl <= 0 {
		return zero, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.entries[key]
	if !ok {
		return zero, false
	}
	e, _ := el.Value.(*entry[V])
	if !c.now().Before(e.expires) {
		c.remove(el)
		return zero, false
	}
	c.recency.MoveToFront(el)
	return e.value, true
}

// Put stores value under key, replacing any value and restarting its lifetime,
// and makes it the most recently used. When the cache is full, the least
// recently used entry is evicted to make room. With the cache disabled it
// stores nothing. A value read from the source of truth is stored with
// [Cache.PutIfEpoch] instead.
func (c *Cache[V]) Put(key string, value V) {
	if c.ttl <= 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.put(key, value)
}

// Epoch is the count of changes announced so far. A reader takes it before it
// reads the source of truth and hands it to [Cache.PutIfEpoch] afterwards.
func (c *Cache[V]) Epoch() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.epoch
}

// PutIfEpoch is Put for a value read after epoch was taken: it stores the value
// and reports true only when no Invalidate or Purge, of any key, happened since.
// When one did, the value may predate the change and is dropped, and the reader
// uses it for its own request and leaves the next one to read again. With the
// cache disabled it stores nothing and reports false.
func (c *Cache[V]) PutIfEpoch(key string, value V, epoch uint64) bool {
	if c.ttl <= 0 {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.epoch != epoch {
		return false
	}
	c.put(key, value)
	return true
}

// put stores the value. The caller holds the lock.
func (c *Cache[V]) put(key string, value V) {
	expires := c.now().Add(c.ttl)
	if el, ok := c.entries[key]; ok {
		e, _ := el.Value.(*entry[V])
		e.value = value
		e.expires = expires
		c.recency.MoveToFront(el)
		return
	}
	for c.recency.Len() >= c.capacity {
		c.remove(c.recency.Back())
	}
	c.entries[key] = c.recency.PushFront(&entry[V]{key: key, value: value, expires: expires})
}

// Invalidate removes the entry under key, if there is one, and no other, and
// moves the epoch so that a read already in flight is not stored. The epoch moves
// whether or not an entry was there: the reader that is racing missed.
func (c *Cache[V]) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.epoch++
	if el, ok := c.entries[key]; ok {
		c.remove(el)
	}
}

// Purge removes every entry and moves the epoch. The cache is usable afterwards.
func (c *Cache[V]) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.epoch++
	clear(c.entries)
	c.recency.Init()
}

// Len is the number of entries held, expired ones not yet removed included.
func (c *Cache[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.recency.Len()
}

// remove drops el from both structures. The caller holds the lock.
func (c *Cache[V]) remove(el *list.Element) {
	e, _ := el.Value.(*entry[V])
	delete(c.entries, e.key)
	c.recency.Remove(el)
}
