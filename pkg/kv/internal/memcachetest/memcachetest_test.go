package memcachetest

import (
	"strings"
	"testing"
)

// TestASpaceEndsAtItsOnlyDot pins the shape the isolation rests on. An entry's
// name says which space it is in only because a space's one dot is its last
// byte: a dot earlier in a space, or none at all, would let a longer space
// start with a shorter one, and two cases' stores could then name one entry.
// Every byte is one Memcached accepts in a key, and two calls never return the
// same space.
func TestASpaceEndsAtItsOnlyDot(t *testing.T) {
	seen := make(map[string]bool)
	for range 1000 {
		space := Space(t)
		if strings.Index(space, spaceEnd) != len(space)-len(spaceEnd) {
			t.Fatalf("the space %q does not end at its only dot", space)
		}
		for _, b := range []byte(space) {
			if b <= ' ' || b == 0x7f {
				t.Fatalf("the space %q holds the byte %#x, which Memcached refuses in a key", space, b)
			}
		}
		if seen[space] {
			t.Fatalf("the space %q was handed out twice", space)
		}
		seen[space] = true
	}
}
