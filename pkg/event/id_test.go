package event_test

import (
	"regexp"
	"testing"

	"github.com/vislake/speed/pkg/event"
)

// canonicalV7 is the canonical text form of an RFC 9562 UUID, with the version
// nibble fixed at 7 and the variant bits at 10.
var canonicalV7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// TestNewIDIsAVersion7UUID pins the form the identifier's documentation
// promises, so a consumer storing it in a UUID column can rely on what the bus
// assigns.
func TestNewIDIsAVersion7UUID(t *testing.T) {
	for range 100 {
		if id := event.NewID(); !canonicalV7.MatchString(id) {
			t.Fatalf("NewID returned %q, which is not a version 7 UUID in canonical form", id)
		}
	}
}

// TestNewIDIsUniqueAndOrderedByTime holds the two properties a de-duplicating
// consumer leans on: no two identifiers minted in a burst — many in one
// millisecond — are equal, and one minted in a later millisecond sorts after
// every one minted before it.
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
	// The leading 48 bits are the time in milliseconds; wait out the one the
	// first identifier was minted in.
	later := event.NewID()
	for later[:13] == earlier[:13] {
		later = event.NewID()
	}
	if later <= earlier {
		t.Fatalf("an identifier minted in a later millisecond, %q, does not sort after %q", later, earlier)
	}
}
