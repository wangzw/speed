package password

import (
	"strings"
	"testing"
	"time"
)

// clockAt is a clock a test moves by hand.
type clockAt struct{ t time.Time }

func (c *clockAt) now() time.Time          { return c.t }
func (c *clockAt) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *clockAt { return &clockAt{t: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)} }

// TestACheckThatHeldIsRemembered holds the cache from the three sides a key is
// made of: the same subject, entry and password hit, and a change in any one of
// them does not.
func TestACheckThatHeldIsRemembered(t *testing.T) {
	clock := newClock()
	c := NewCache(16, clock.now)
	e := parsed(t, cheapPBKDF2, "secret")
	other := parsed(t, cheapPBKDF2, "secret")
	const ttl = time.Minute

	if c.Hit(ttl, "alice", e, "secret") {
		t.Fatal("a check nobody recorded hit")
	}
	c.Remember(ttl, "alice", e, "secret")

	if !c.Hit(ttl, "alice", e, "secret") {
		t.Error("a check that held did not hit")
	}
	if c.Hit(ttl, "alice", e, "secreT") {
		t.Error("another password hit: a wrong password would be accepted from the cache")
	}
	if c.Hit(ttl, "bob", e, "secret") {
		t.Error("another subject hit: one subject's check would stand for another's")
	}
	if c.Hit(ttl, "alice", other, "secret") {
		t.Error("an entry written again hit: a changed password would keep its cached checks")
	}
}

// TestTheKeyDoesNotRunSubjectAndPasswordTogether holds the framing of the key:
// two subject and password pairs whose concatenations are equal do not share a
// check.
func TestTheKeyDoesNotRunSubjectAndPasswordTogether(t *testing.T) {
	c := NewCache(16, newClock().now)
	e := parsed(t, cheapPBKDF2, "x")

	c.Remember(time.Minute, "a", e, "bc")
	if c.Hit(time.Minute, "ab", e, "c") {
		t.Error("the pair (ab, c) hit the check recorded for (a, bc)")
	}
}

// TestTheKeyHoldsNeitherThePasswordNorTheSubject holds that what is stored is
// not the password and not something reversible from it: the key is a fixed
// width digest and carries neither string, and two caches, which draw different
// secrets, do not share a hit.
func TestTheKeyHoldsNeitherThePasswordNorTheSubject(t *testing.T) {
	e := parsed(t, cheapPBKDF2, "hunter2-hunter2")
	first, second := NewCache(4, nil), NewCache(4, nil)

	key := first.key("alice-the-subject", e, "hunter2-hunter2")
	if len(key) != 64 {
		t.Errorf("the key is %d characters, want the 64 of a hex SHA-256", len(key))
	}
	if strings.Contains(key, "hunter2") || strings.Contains(key, "alice") {
		t.Errorf("the key %q carries the password or the subject", key)
	}
	if key != first.key("alice-the-subject", e, "hunter2-hunter2") {
		t.Error("one check has two keys in one cache")
	}
	if key == second.key("alice-the-subject", e, "hunter2-hunter2") {
		t.Error("two caches derive one key: the secret is not drawn per cache, so a key is something to be guessed offline")
	}
	first.Remember(time.Minute, "alice-the-subject", e, "hunter2-hunter2")
	if second.Hit(time.Minute, "alice-the-subject", e, "hunter2-hunter2") {
		t.Error("a check recorded in one cache hit in another")
	}
}

// TestACheckExpiresWithTheLifetimeInForce holds the lifetime from both sides at
// the boundary, and holds that a lifetime lowered while entries are stored takes
// effect on them: the lifetime is read when the check is looked up, not when it
// was stored.
func TestACheckExpiresWithTheLifetimeInForce(t *testing.T) {
	clock := newClock()
	c := NewCache(16, clock.now)
	e := parsed(t, cheapPBKDF2, "secret")

	c.Remember(time.Minute, "alice", e, "secret")
	clock.advance(time.Minute - time.Nanosecond)
	if !c.Hit(time.Minute, "alice", e, "secret") {
		t.Error("a check one nanosecond short of its lifetime did not hit")
	}
	clock.advance(time.Nanosecond)
	if c.Hit(time.Minute, "alice", e, "secret") {
		t.Error("a check at its lifetime hit: the lifetime is not honoured")
	}

	c.Remember(time.Minute, "alice", e, "secret")
	clock.advance(30 * time.Second)
	if !c.Hit(time.Minute, "alice", e, "secret") {
		t.Fatal("a check half way through its lifetime did not hit")
	}
	if c.Hit(10*time.Second, "alice", e, "secret") {
		t.Error("a lifetime lowered to ten seconds still served a check stored thirty seconds ago")
	}
	if !c.Hit(5*time.Minute, "alice", e, "secret") {
		t.Error("a lifetime raised while the entry is held did not serve it")
	}
}

// TestALifetimeOfZeroSwitchesTheCacheOff holds both halves of off: nothing is
// recorded and nothing hits, even for a check recorded while the cache was on.
func TestALifetimeOfZeroSwitchesTheCacheOff(t *testing.T) {
	c := NewCache(16, newClock().now)
	e := parsed(t, cheapPBKDF2, "secret")

	c.Remember(time.Minute, "alice", e, "secret")
	if c.Hit(0, "alice", e, "secret") {
		t.Error("a check hit with the cache switched off")
	}
	if c.Hit(-time.Second, "alice", e, "secret") {
		t.Error("a check hit with a negative lifetime")
	}

	off := NewCache(16, newClock().now)
	off.Remember(0, "alice", e, "secret")
	if off.Hit(time.Minute, "alice", e, "secret") {
		t.Error("a check recorded with the cache off hit once it was on: the off state stored something")
	}
}

// TestTheCacheHoldsNoMoreThanItsCapacity holds the bound and what it forgets: of
// a cache of two, the third check evicts the one used least recently.
func TestTheCacheHoldsNoMoreThanItsCapacity(t *testing.T) {
	c := NewCache(2, newClock().now)
	e := parsed(t, cheapPBKDF2, "secret")
	const ttl = time.Minute

	c.Remember(ttl, "a", e, "secret")
	c.Remember(ttl, "b", e, "secret")
	if !c.Hit(ttl, "a", e, "secret") { // a is now the most recently used
		t.Fatal("a recorded check did not hit")
	}
	c.Remember(ttl, "c", e, "secret")

	if c.Hit(ttl, "b", e, "secret") {
		t.Error("the least recently used check survived a full cache")
	}
	if !c.Hit(ttl, "a", e, "secret") || !c.Hit(ttl, "c", e, "secret") {
		t.Error("a check that should have survived was evicted")
	}
}
