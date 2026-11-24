// Package dedupe remembers what a carrier has already been asked to do, keyed by a stable identifier, so one identifier reaches it at most once.
package dedupe

import "sync"

// DefaultCapacity is how many of the most recent identifiers a Table remembers when its caller
// names no capacity.
const DefaultCapacity = 4096

// Entry is one in-flight or settled operation.
type Entry[T any] struct {
	done   chan struct{}
	result T
	err    error
}

// Wait blocks until the operation has settled and returns its outcome. It is
// safe to call from several goroutines.
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

// New builds a table that remembers at most capacity identifiers.
// A non-positive capacity takes DefaultCapacity.
func New[T any](capacity int) *Table[T] {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Table[T]{cap: capacity, seen: make(map[string]*Entry[T])}
}

// Begin takes identifier id for an operation. When first is true the caller
// owns the operation and must settle it with Finish; otherwise entry is the
// operation already made or in flight, whose outcome Wait gives.
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

// Finish writes the outcome of the operation and releases any caller waiting on
// it. The caller must have received entry from Begin with first set to true.
func (t *Table[T]) Finish(entry *Entry[T], result T, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry.result = result
	entry.err = err
	close(entry.done)
	t.evictLocked()
}

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
