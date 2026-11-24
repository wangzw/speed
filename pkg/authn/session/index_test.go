package session

import (
	"testing"
)

// The session index is bounded like the cache beside it: past its capacity
// the least recently used identifier is dropped, and an identifier it no
// longer holds is a no-op to remove.
func TestTheSessionIndexIsBounded(t *testing.T) {
	x := newSessionIndex(2)
	x.add("s1", "k1", alice)
	x.add("s2", "k2", alice)
	if x.size() != 2 {
		t.Fatalf("size = %d, want 2", x.size())
	}

	x.add("s3", "k3", alice)
	if x.size() != 2 {
		t.Errorf("size = %d after overflowing, want the capacity 2", x.size())
	}
	if _, ok := x.remove("s1"); ok {
		t.Error("the oldest identifier was still recorded, so the index is not bounded")
	}
	if key, ok := x.remove("s3"); !ok || key != "k3" {
		t.Errorf("remove(s3) = %q, %v, want k3 and true", key, ok)
	}

	x.add("s2", "k2b", bob)
	if key, _ := x.remove("s2"); key != "k2b" {
		t.Errorf("remove(s2) = %q, want the updated key k2b", key)
	}

	x.add("s4", "k4", bob)
	x.add("s5", "k5", alice)
	taken := x.takeAccount(bob)
	if len(taken) != 1 || taken[0].id != "s4" || taken[0].key != "k4" || taken[0].account != bob {
		t.Errorf("takeAccount(bob) = %+v, want the one entry for s4", taken)
	}
	if _, ok := x.remove("s4"); ok {
		t.Error("the taken entry is still recorded")
	}
	if key, ok := x.remove("s5"); !ok || key != "k5" {
		t.Errorf("another account's entry was dropped by takeAccount: remove(s5) = %q, %v", key, ok)
	}
}
