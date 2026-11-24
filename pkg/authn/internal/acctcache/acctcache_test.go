package acctcache

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// stepClock is a clock a case moves by hand.
type stepClock struct {
	mu sync.Mutex
	at time.Time
}

func newStepClock() *stepClock {
	return &stepClock{at: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
}

func (c *stepClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *stepClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func mustHit(t *testing.T, c *Cache[string], key, want string) {
	t.Helper()
	got, ok := c.Get(key)
	if !ok || got != want {
		t.Errorf("Get(%q) = (%q, %v), want (%q, true)", key, got, ok, want)
	}
}

func mustMiss(t *testing.T, c *Cache[string], key string) {
	t.Helper()
	if got, ok := c.Get(key); ok {
		t.Errorf("Get(%q) = (%q, true), want a miss", key, got)
	}
}

// TestAnEntryIsServedUntilItsLifetimeIsFullyElapsed holds the edge on both
// sides: one tick before the lifetime ends the entry is served, at the
// instant it ends it is not, and the miss removes the entry rather than
// leaving it to occupy a slot.
func TestAnEntryIsServedUntilItsLifetimeIsFullyElapsed(t *testing.T) {
	clk := newStepClock()
	c := New[string](4, time.Minute, clk.now)

	c.Put("a", "snapshot")

	clk.advance(time.Minute - time.Nanosecond)
	mustHit(t, c, "a", "snapshot")

	clk.advance(time.Nanosecond)
	mustMiss(t, c, "a")
	if got := c.Len(); got != 0 {
		t.Errorf("Len() = %d after the expiring miss, want 0", got)
	}

	clk.advance(time.Hour)
	mustMiss(t, c, "a")
}

// TestAHitDoesNotExtendTheLifetime holds that the lifetime runs from the store,
// not from the last read: a cache that renewed on a hit would let a busy
// account keep a stale snapshot for ever.
func TestAHitDoesNotExtendTheLifetime(t *testing.T) {
	clk := newStepClock()
	c := New[string](4, time.Minute, clk.now)

	c.Put("a", "v")
	clk.advance(40 * time.Second)
	mustHit(t, c, "a", "v")
	clk.advance(20 * time.Second)

	mustMiss(t, c, "a")
}

// TestTheLeastRecentlyUsedEntryIsEvictedFirst holds the bound and the order of
// eviction: the N+1st entry pushes out the one untouched longest.
func TestTheLeastRecentlyUsedEntryIsEvictedFirst(t *testing.T) {
	c := New[string](3, time.Hour, newStepClock().now)

	c.Put("a", "1")
	c.Put("b", "2")
	c.Put("c", "3")
	c.Put("d", "4")

	mustMiss(t, c, "a")
	mustHit(t, c, "b", "2")
	mustHit(t, c, "c", "3")
	mustHit(t, c, "d", "4")
	if got := c.Len(); got != 3 {
		t.Errorf("Len() = %d, want 3", got)
	}
}

// TestAReadRefreshesRecency holds that Get counts as use: the entry read is
// evicted after the ones that were not.
func TestAReadRefreshesRecency(t *testing.T) {
	c := New[string](3, time.Hour, newStepClock().now)

	c.Put("a", "1")
	c.Put("b", "2")
	c.Put("c", "3")
	mustHit(t, c, "a", "1")
	c.Put("d", "4")

	mustMiss(t, c, "b")
	mustHit(t, c, "a", "1")
	mustHit(t, c, "c", "3")
	mustHit(t, c, "d", "4")
}

// TestAMissDoesNotRefreshAnything holds that a read of an absent key leaves the
// order alone.
func TestAMissDoesNotRefreshAnything(t *testing.T) {
	c := New[string](2, time.Hour, newStepClock().now)

	c.Put("a", "1")
	c.Put("b", "2")
	mustMiss(t, c, "absent")
	c.Put("c", "3")

	mustMiss(t, c, "a")
	mustHit(t, c, "b", "2")
}

// TestAZeroLifetimeDisablesTheCache holds the strict mode: nothing is stored
// and nothing hits, however soon after the Put the Get comes.
func TestAZeroLifetimeDisablesTheCache(t *testing.T) {
	for _, ttl := range []time.Duration{0, -time.Second} {
		t.Run(ttl.String(), func(t *testing.T) {
			c := New[string](4, ttl, newStepClock().now)

			c.Put("a", "v")

			mustMiss(t, c, "a")
			if got := c.Len(); got != 0 {
				t.Errorf("Len() = %d, want 0 with the cache disabled", got)
			}
		})
	}
}

// TestInvalidateRemovesOnlyTheNamedKey holds that clearing one account does not
// clear its neighbours, and that clearing an absent key is not an error.
func TestInvalidateRemovesOnlyTheNamedKey(t *testing.T) {
	c := New[string](4, time.Hour, newStepClock().now)
	c.Put("a", "1")
	c.Put("b", "2")

	c.Invalidate("a")
	c.Invalidate("absent")

	mustMiss(t, c, "a")
	mustHit(t, c, "b", "2")
}

// TestPurgeRemovesEverythingAndTheCacheStaysUsable holds that a purge leaves a
// cache that stores again, within the same bound.
func TestPurgeRemovesEverythingAndTheCacheStaysUsable(t *testing.T) {
	c := New[string](2, time.Hour, newStepClock().now)
	c.Put("a", "1")
	c.Put("b", "2")

	c.Purge()

	mustMiss(t, c, "a")
	mustMiss(t, c, "b")
	if got := c.Len(); got != 0 {
		t.Errorf("Len() = %d after Purge, want 0", got)
	}

	c.Put("c", "3")
	c.Put("d", "4")
	c.Put("e", "5")
	mustMiss(t, c, "c")
	mustHit(t, c, "d", "4")
	mustHit(t, c, "e", "5")
}

// TestPutOnAKnownKeyReplacesTheValueAndRestartsTheLifetime holds both halves of
// a refresh: the value read afterwards is the new one, the lifetime counts from
// the second store, and the entry is not duplicated in the bound.
func TestPutOnAKnownKeyReplacesTheValueAndRestartsTheLifetime(t *testing.T) {
	clk := newStepClock()
	c := New[string](2, time.Minute, clk.now)

	c.Put("a", "old")
	clk.advance(50 * time.Second)
	c.Put("a", "new")
	clk.advance(50 * time.Second) // 100s after the first store, 50s after the second

	mustHit(t, c, "a", "new")
	if got := c.Len(); got != 1 {
		t.Errorf("Len() = %d after a repeated Put, want 1", got)
	}

	clk.advance(10 * time.Second)
	mustMiss(t, c, "a")
}

// TestARepeatedPutRefreshesRecency holds that storing a key again makes it the
// newest, as storing it for the first time does.
func TestARepeatedPutRefreshesRecency(t *testing.T) {
	c := New[string](2, time.Hour, newStepClock().now)
	c.Put("a", "1")
	c.Put("b", "2")
	c.Put("a", "1'")
	c.Put("c", "3")

	mustMiss(t, c, "b")
	mustHit(t, c, "a", "1'")
}

// TestACapacityBelowOneIsRaisedToOne holds the documented disposition of an
// impossible size: the cache holds exactly one entry, rather than none or an
// unbounded number.
func TestACapacityBelowOneIsRaisedToOne(t *testing.T) {
	for _, capacity := range []int{0, -5} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			c := New[string](capacity, time.Hour, newStepClock().now)

			c.Put("a", "1")
			mustHit(t, c, "a", "1")
			c.Put("b", "2")

			mustMiss(t, c, "a")
			mustHit(t, c, "b", "2")
			if got := c.Len(); got != 1 {
				t.Errorf("Len() = %d, want 1", got)
			}
		})
	}
}

// TestAMissOnTheValueTypeReturnsItsZeroValue holds that a miss never hands back
// a stale value alongside false, for a value type that is a pointer.
func TestAMissOnTheValueTypeReturnsItsZeroValue(t *testing.T) {
	c := New[*string](2, time.Hour, newStepClock().now)

	if got, ok := c.Get("a"); ok || got != nil {
		t.Errorf("Get on an empty cache = (%v, %v), want (nil, false)", got, ok)
	}
}

// TestTheZeroClockReadsTheSystemClock holds that a nil clock is accepted and
// serves an entry that was just stored.
func TestTheZeroClockReadsTheSystemClock(t *testing.T) {
	c := New[string](2, time.Hour, nil)

	c.Put("a", "1")

	mustHit(t, c, "a", "1")
}

// TestConcurrentUseIsRaceFree drives every operation from many goroutines.
func TestConcurrentUseIsRaceFree(t *testing.T) {
	clk := newStepClock()
	c := New[int](16, time.Second, clk.now)

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 500 {
				key := fmt.Sprintf("k%d", (g+i)%32)
				switch i % 5 {
				case 0:
					c.Put(key, i)
				case 1, 2:
					c.Get(key)
				case 3:
					c.Invalidate(key)
				default:
					if i%50 == 4 {
						c.Purge()
					}
					clk.advance(time.Millisecond)
				}
			}
		}()
	}
	wg.Wait()

	if got := c.Len(); got > 16 {
		t.Errorf("Len() = %d, want at most 16", got)
	}
}

// TestAReadInFlightDuringAnInvalidationIsNotStored holds the fence a read-through
// needs: a reader that took its epoch, read the source of truth, and then lost
// the race to an Invalidate must not store what it read, or the change it
// missed would be served until the lifetime ends.
func TestAReadInFlightDuringAnInvalidationIsNotStored(t *testing.T) {
	for name, change := range map[string]func(c *Cache[string]){
		"Invalidate of the key":   func(c *Cache[string]) { c.Invalidate("acct") },
		"Invalidate of a sibling": func(c *Cache[string]) { c.Invalidate("other") },
		"Purge":                   func(c *Cache[string]) { c.Purge() },
	} {
		t.Run(name, func(t *testing.T) {
			c := New[string](4, time.Minute, nil)
			mustMiss(t, c, "acct")
			epoch := c.Epoch()
			snapshot := "active" // read from the database before the change
			change(c)
			if c.PutIfEpoch("acct", snapshot, epoch) {
				t.Error("PutIfEpoch stored a value read before the change")
			}
			mustMiss(t, c, "acct")
		})
	}
}

// TestAReadWithNoInvalidationInBetweenIsStored is the other side: with the
// epoch unmoved the conditional put is a put, and a later read takes the epoch
// anew and stores.
func TestAReadWithNoInvalidationInBetweenIsStored(t *testing.T) {
	c := New[string](4, time.Minute, nil)
	if !c.PutIfEpoch("acct", "active", c.Epoch()) {
		t.Fatal("PutIfEpoch refused with no change in between")
	}
	mustHit(t, c, "acct", "active")

	c.Invalidate("acct")
	mustMiss(t, c, "acct")
	if !c.PutIfEpoch("acct", "disabled", c.Epoch()) {
		t.Fatal("PutIfEpoch refused a read that started after the change")
	}
	mustHit(t, c, "acct", "disabled")
}

// TestAnInvalidateOfAKeyThatIsNotThereStillMovesTheEpoch holds that the fence
// does not depend on the entry having been stored: the racing reader's miss is
// exactly the case where there was nothing to remove.
func TestAnInvalidateOfAKeyThatIsNotThereStillMovesTheEpoch(t *testing.T) {
	c := New[string](4, time.Minute, nil)
	before := c.Epoch()
	c.Invalidate("never-stored")
	if c.Epoch() == before {
		t.Error("Invalidate of an absent key left the epoch where it was")
	}
	before = c.Epoch()
	c.Purge()
	if c.Epoch() == before {
		t.Error("Purge of an empty cache left the epoch where it was")
	}
}

// TestADisabledCacheStoresNothingEvenWithTheEpochCurrent holds that the
// conditional put honours the switched-off cache.
func TestADisabledCacheStoresNothingEvenWithTheEpochCurrent(t *testing.T) {
	c := New[string](4, 0, nil)
	if c.PutIfEpoch("acct", "active", c.Epoch()) {
		t.Error("PutIfEpoch reported a store on a disabled cache")
	}
	mustMiss(t, c, "acct")
}
