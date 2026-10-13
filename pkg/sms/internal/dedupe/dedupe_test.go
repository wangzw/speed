package dedupe

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// The first Begin for an identifier owns it, and a later Begin reads the
// outcome the owner settled rather than taking it again.
func TestALaterBeginReadsTheOwnersOutcome(t *testing.T) {
	tab := New[string](8)
	owner, first := tab.Begin("id")
	if !first {
		t.Fatal("the first Begin did not take the identifier")
	}
	tab.Finish(owner, "receipt", nil)

	second, first := tab.Begin("id")
	if first {
		t.Fatal("the second Begin took an identifier already settled")
	}
	got, err := second.Wait()
	if err != nil {
		t.Fatalf("Wait reported %v, want the owner's nil", err)
	}
	if got != "receipt" {
		t.Errorf("Wait answered %q, want the owner's outcome receipt", got)
	}
}

// The outcome an owner settles with is what a waiter reads, error included.
func TestWaitCarriesTheOwnersError(t *testing.T) {
	tab := New[string](8)
	owner, _ := tab.Begin("id")
	tab.Finish(owner, "", errTest)

	waiter, first := tab.Begin("id")
	if first {
		t.Fatal("the second Begin took an identifier already settled")
	}
	if _, err := waiter.Wait(); !errors.Is(err, errTest) {
		t.Errorf("Wait reported %v, want the owner's %v", err, errTest)
	}
}

// Settled identifiers beyond the cap are forgotten, so the table does not grow
// without bound; an identifier forgotten this way can be taken again.
func TestSettledEntriesBeyondTheCapAreForgotten(t *testing.T) {
	tab := New[int](2)
	for i, id := range []string{"a", "b", "c"} {
		owner, first := tab.Begin(id)
		if !first {
			t.Fatalf("Begin(%q) did not take the identifier", id)
		}
		tab.Finish(owner, i, nil)
	}
	// With a cap of two, "a" is the oldest settled and was evicted.
	if _, first := tab.Begin("a"); !first {
		t.Error("the oldest settled identifier was not forgotten at the cap")
	}
}

// An identifier still in flight is never evicted: forgetting it would let a
// later call for the same identifier run again while the first was going, which
// is the one thing this table exists to prevent.
func TestAnInFlightEntryIsNotEvicted(t *testing.T) {
	tab := New[int](1)
	if _, first := tab.Begin("a"); !first {
		t.Fatal("Begin(a) did not take the identifier")
	}
	// "b" takes the table past its cap, but the entry at the front of the order,
	// "a", is in flight, so nothing is forgotten.
	if _, first := tab.Begin("b"); !first {
		t.Fatal("Begin(b) did not take the identifier")
	}
	if _, first := tab.Begin("a"); first {
		t.Error("an in-flight identifier was evicted at the cap")
	}
}

// Calls for one identifier that arrive together elect exactly one owner; every
// other caller waits and reads that owner's outcome.
func TestConcurrentBeginsTakeOneOwner(t *testing.T) {
	tab := New[int](0)
	const callers = 64
	var owners int64
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry, first := tab.Begin("id")
			if first {
				atomic.AddInt64(&owners, 1)
				tab.Finish(entry, 42, nil)
				return
			}
			if got, err := entry.Wait(); err != nil || got != 42 {
				t.Errorf("Wait answered (%d, %v), want the owner's (42, nil)", got, err)
			}
		}()
	}
	wg.Wait()
	if n := atomic.LoadInt64(&owners); n != 1 {
		t.Errorf("%d callers took the identifier, want exactly one owner", n)
	}
}

// errTest is a settled error the Wait cases read back.
var errTest = &testError{}

type testError struct{}

func (*testError) Error() string { return "test error" }
