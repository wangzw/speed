package metadata

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/objectstore"
	"github.com/vislake/speed/pkg/tenant"
)

// TestInternalKeyGrammar holds that the key is the two segments joined by the
// separator, tenant first.
func TestInternalKeyGrammar(t *testing.T) {
	key, err := internalKey(tenant.ID("acme"), "0a1b2c3d")
	if err != nil {
		t.Fatalf("minting a key: %v", err)
	}
	if key != "acme/0a1b2c3d" {
		t.Errorf("the key is %q, want acme/0a1b2c3d", key)
	}
	if strings.Count(key, keySeparator) != 1 {
		t.Errorf("the key %q holds %d separators, want one", key, strings.Count(key, keySeparator))
	}
}

// TestInternalKeyCaps holds the per-segment and whole-key caps: a segment at the
// cap is admitted, one above it is refused, and a whole key above the column's
// width is refused.
func TestInternalKeyCaps(t *testing.T) {
	t.Run("a segment at the cap", func(t *testing.T) {
		segment := strings.Repeat("a", keySegmentMax)
		if _, err := internalKey(tenant.ID(segment), "id"); err != nil {
			t.Errorf("a segment at the cap was refused: %v", err)
		}
	})
	t.Run("a segment above the cap", func(t *testing.T) {
		segment := strings.Repeat("a", keySegmentMax+1)
		if _, err := internalKey(tenant.ID(segment), "id"); !errors.Is(err, objectstore.ErrInvalidRequest) {
			t.Errorf("a segment above the cap gave %v, want ErrInvalidRequest", err)
		}
	})
	t.Run("an empty segment", func(t *testing.T) {
		if _, err := internalKey(tenant.ID(""), "id"); !errors.Is(err, objectstore.ErrInvalidRequest) {
			t.Errorf("an empty tenant segment gave %v, want ErrInvalidRequest", err)
		}
	})
	t.Run("a segment carrying the separator", func(t *testing.T) {
		if _, err := internalKey(tenant.ID("ac/me"), "id"); !errors.Is(err, objectstore.ErrInvalidRequest) {
			t.Errorf("a segment carrying the separator gave %v, want ErrInvalidRequest", err)
		}
	})
	t.Run("a whole key above the column width", func(t *testing.T) {
		// Two segments each just under the cap, whose sum leaves the whole-key
		// cap.
		a := strings.Repeat("a", keySegmentMax)
		b := strings.Repeat("b", keySegmentMax)
		key := a + keySeparator + b
		if len(key) <= keyMaxLen {
			t.Fatalf("the fixture key is %d bytes, not above the %d cap", len(key), keyMaxLen)
		}
		if _, err := internalKey(tenant.ID(a), b); !errors.Is(err, objectstore.ErrInvalidRequest) {
			t.Errorf("a whole key above the cap gave %v, want ErrInvalidRequest", err)
		}
	})
}

// TestCursorRoundTrips holds that a cursor decodes back to the instant and
// identifier it was encoded from, and that a cursor this module did not write is
// refused.
func TestCursorRoundTrips(t *testing.T) {
	cursor := encodeCursor(1728000000000, "obj-1")
	ms, id, err := decodeCursor(cursor)
	if err != nil {
		t.Fatalf("decoding a cursor this module wrote: %v", err)
	}
	if ms != 1728000000000 || id != "obj-1" {
		t.Errorf("the cursor decoded to (%d, %q), want (1728000000000, obj-1)", ms, id)
	}
	for _, bad := range []string{"not base64!", "bm9waXBl"} {
		if _, _, err := decodeCursor(bad); !errors.Is(err, objectstore.ErrInvalidRequest) {
			t.Errorf("decoding %q gave %v, want ErrInvalidRequest", bad, err)
		}
	}
}
