package metadata

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/objectstore"
)

// TestInternalKeyIsTwoRandomSegments holds that the key is a shard and a name of fixed widths,
// joined by the separator, both lowercase hexadecimal.
func TestInternalKeyIsTwoRandomSegments(t *testing.T) {
	key, err := internalKey()
	if err != nil {
		t.Fatalf("minting a key: %v", err)
	}
	shard, name, ok := strings.Cut(key, keySeparator)
	if !ok {
		t.Fatalf("the key %q carries no separator", key)
	}
	if len(shard) != shardBytes*2 {
		t.Errorf("the shard is %d characters, want %d", len(shard), shardBytes*2)
	}
	if len(name) != nameBytes*2 {
		t.Errorf("the name is %d characters, want %d", len(name), nameBytes*2)
	}
	for _, segment := range []string{shard, name} {
		if _, err := hex.DecodeString(segment); err != nil {
			t.Errorf("the segment %q is not lowercase hexadecimal: %v", segment, err)
		}
	}
	if strings.Count(key, keySeparator) != 1 {
		t.Errorf("the key %q holds %d separators, want one", key, strings.Count(key, keySeparator))
	}
	if len(key) > keyMaxLen {
		t.Errorf("the key is %d bytes, above the %d cap", len(key), keyMaxLen)
	}
}

// TestInternalKeysDifferAndCarryNoIdentifier checks that 64 minted keys are all distinct.
func TestInternalKeysDifferAndCarryNoIdentifier(t *testing.T) {
	seen := make(map[string]struct{}, 64)
	for i := 0; i < 64; i++ {
		key, err := internalKey()
		if err != nil {
			t.Fatalf("minting a key: %v", err)
		}
		if _, twice := seen[key]; twice {
			t.Fatalf("the key %q was minted twice", key)
		}
		seen[key] = struct{}{}
	}
}

// TestSegmentCaps holds the caps a minted key is held to: a segment at the cap is admitted and one
// above it, an empty one, and one carrying the separator are refused.
func TestSegmentCaps(t *testing.T) {
	t.Run("a segment at the cap", func(t *testing.T) {
		if err := checkSegment(strings.Repeat("a", keySegmentMax)); err != nil {
			t.Errorf("a segment at the cap was refused: %v", err)
		}
	})
	t.Run("a segment above the cap", func(t *testing.T) {
		if err := checkSegment(strings.Repeat("a", keySegmentMax+1)); !errors.Is(err, objectstore.ErrInvalidRequest) {
			t.Errorf("a segment above the cap gave %v, want ErrInvalidRequest", err)
		}
	})
	t.Run("an empty segment", func(t *testing.T) {
		if err := checkSegment(""); !errors.Is(err, objectstore.ErrInvalidRequest) {
			t.Errorf("an empty segment gave %v, want ErrInvalidRequest", err)
		}
	})
	t.Run("a segment carrying the separator", func(t *testing.T) {
		if err := checkSegment("ac/me"); !errors.Is(err, objectstore.ErrInvalidRequest) {
			t.Errorf("a segment carrying the separator gave %v, want ErrInvalidRequest", err)
		}
	})
}

// TestCursorRoundTrips holds that a cursor decodes back to the instant and identifier it was
// encoded from, and that a cursor this module did not write is refused.
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
