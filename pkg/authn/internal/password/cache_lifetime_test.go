package password

import (
	"testing"
	"time"
)

// TestACheckIsRememberedForTheTopOfTheLifetimeRange: a check is remembered for the longest lifetime the setting allows.
func TestACheckIsRememberedForTheTopOfTheLifetimeRange(t *testing.T) {
	clock := newClock()
	c := NewCache(4, clock.now)
	e := parsed(t, cheapPBKDF2, "secret")
	const top = 5 * time.Minute

	c.Remember(top, "alice", e, "secret")
	clock.advance(top - time.Second)
	if !c.Hit(top, "alice", e, "secret") {
		t.Error("a check one second short of the longest lifetime did not hit: the store forgets it earlier than the setting allows")
	}
	clock.advance(time.Second)
	if c.Hit(top, "alice", e, "secret") {
		t.Error("a check at the longest lifetime hit")
	}
}

// TestTheDigestSeparatesEntriesThatShareASaltAndAnAlgorithm: the digest stands for the whole entry, not for its salt or its algorithm.
func TestTheDigestSeparatesEntriesThatShareASaltAndAnAlgorithm(t *testing.T) {
	one, err := Parse(pbkdf2Literal(t, "one", salt16, 5, 32))
	if err != nil {
		t.Fatal(err)
	}
	two, err := Parse(pbkdf2Literal(t, "two", salt16, 5, 32))
	if err != nil {
		t.Fatal(err)
	}
	more, err := Parse(pbkdf2Literal(t, "one", salt16, 6, 32))
	if err != nil {
		t.Fatal(err)
	}
	if one.Digest() == two.Digest() {
		t.Error("two entries with one salt and two passwords share a digest")
	}
	if one.Digest() == more.Digest() {
		t.Error("two entries with one salt and two counts share a digest")
	}
	c := NewCache(4, newClock().now)
	c.Remember(time.Minute, "alice", one, "one")
	if c.Hit(time.Minute, "alice", two, "one") {
		t.Error("a check recorded against one entry hit against an entry that shares its salt: a changed password would keep its cached checks")
	}
}
