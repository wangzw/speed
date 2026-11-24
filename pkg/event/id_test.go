package event_test

import (
	"regexp"
	"testing"

	"github.com/vislake/speed/pkg/event"
)

var canonicalV7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// TestNewIDIsAVersion7UUID pins the UUID version 7 form the identifier's
// documentation promises.
func TestNewIDIsAVersion7UUID(t *testing.T) {
	for range 100 {
		if id := event.NewID(); !canonicalV7.MatchString(id) {
			t.Fatalf("NewID returned %q, which is not a version 7 UUID in canonical form", id)
		}
	}
}

// TestNewIDIsUniqueAndOrderedByTime holds the two properties a
// de-duplicating consumer leans on.
func TestNewIDIsUniqueAndOrderedByTime(t *testing.T) {
	const n = 10000
	seen := make(map[string]bool, n)
	for range n {
		id := event.NewID()
		if seen[id] {
			t.Fatalf("NewID returned %q twice in %d calls", id, n)
		}
		seen[id] = true
	}

	earlier := event.NewID()
	later := event.NewID()
	for later[:13] == earlier[:13] {
		later = event.NewID()
	}
	if later <= earlier {
		t.Fatalf("an identifier minted in a later millisecond, %q, does not sort after %q", later, earlier)
	}
}
