// Package dedupe remembers the outcome of what a carrier has already been
// asked to do, keyed by a stable identifier, so that the same identifier
// reaches the carrier at most once. A carrier is not idempotent — a second
// send delivers and bills a second message — so the sending capabilities'
// carriers hold one of these and take a message's DedupeID through it before
// calling the vendor.
//
// A caller begins with the identifier: the first caller owns the operation and
// settles it, and any caller that finds the identifier taken waits for the
// first to settle and is answered with the same outcome. The memory is in one
// process, which is the adapter's own; across replicas each keeps its own, so
// holding a vendor to a single call across replicas is the caller's, not this
// table's.
package dedupe

import "sync"

// DefaultCapacity is how many of the most recent identifiers a Table remembers
// when its caller names no capacity. Challenges are bounded by the sending
// capabilities' budgets, so this many identifiers is far more than any window's
// worth, while a cap
// keeps a long-running process from holding every identifier it has ever seen.
const DefaultCapacity = 4096

// Entry is one in-flight or settled operation. Wait blocks until the owner has
// settled it, then returns the outcome.
type Entry[T any] struct {
	done   chan struct{}
	result T
	err    error
}

// Wait blocks until the operation this entry stands for has settled, then
// returns its outcome. It is safe to call from several goroutines.
func (e *Entry[T]) Wait() (T, error) {
	<-e.done
	return e.result, e.err
}

// Table remembers the outcome of operations by identifier, keeping at most cap.
type Table[T any] struct {
	mu    sync.Mutex
	cap   int
	seen  map[string]*Entry[T]
	order []string
}

// New builds a table that remembers at most capacity identifiers. A
// non-positive capacity takes DefaultCapacity.
func New[T any](capacity int) *Table[T] {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Table[T]{cap: capacity, seen: make(map[string]*Entry[T])}
}

// Begin takes identifier id for an operation. When first is true the caller owns
// the operation and must settle it with Finish; otherwise the returned entry is
// the operation already made or in flight, whose outcome is readable through
// Wait.
func (t *Table[T]) Begin(id string) (entry *Entry[T], first bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if existing, taken := t.seen[id]; taken {
		return existing, false
	}
	entry = &Entry[T]{done: make(chan struct{})}
	t.seen[id] = entry
	t.order = append(t.order, id)
	t.evictLocked()
	return entry, true
}

// Finish writes the outcome of the operation that began identifier id and
// releases any caller waiting on it. It retries eviction, since the entry that
// just settled is the oldest one still open when the front of the order was in
// flight at Begin.
func (t *Table[T]) Finish(entry *Entry[T], result T, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry.result = result
	entry.err = err
	close(entry.done)
	t.evictLocked()
}

// evictLocked drops settled entries from the front of the order until the table
// is within its cap. It stops at the first entry still in flight: evicting one
// would let a later call for the same identifier run again while the first was
// still going, which is the case this table exists to prevent.
func (t *Table[T]) evictLocked() {
	for len(t.order) > t.cap {
		id := t.order[0]
		entry := t.seen[id]
		select {
		case <-entry.done:
			delete(t.seen, id)
			t.order = t.order[1:]
		default:
			return
		}
	}
}
