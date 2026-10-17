package metadata

import (
	"strings"
	"unicode"
)

// nameMaxBytes is the cap on a stored name, in bytes. A longer name is cut at a
// character boundary rather than inside one; the cap is a byte count because
// that is what the column and every transport carry.
const nameMaxBytes = 255

// normalizeName turns a declaration's name into the value the table stores.
//
// The name is a caller-controlled label that later reaches display surfaces and
// download filenames, so it is made safe once, here, and every reader
// afterwards gets the same stored value. The stored value:
//
//   - is valid UTF-8: bytes that are not part of a valid sequence are dropped;
//   - carries no character in the Unicode categories Cc, Cf, Zl or Zp: control
//     characters, format characters (including directional controls and
//     zero-width characters), line separators and paragraph separators are
//     dropped;
//   - carries no path separator: '/' and '\' become '_';
//   - has no leading or trailing whitespace;
//   - is at most nameMaxBytes bytes, cut at a character boundary, with
//     whitespace trimmed again after the cut;
//   - is empty when it consists only of dots.
//
// Quotes and semicolons are ordinary characters and are kept, and no extension
// is treated specially: the value is a label, not a path or a filename the
// module parses. Nothing here can fail a declaration: an empty, malformed or
// reduced-to-nothing name stores as the empty string.
func normalizeName(name string) string {
	name = strings.ToValidUTF8(name, "")

	var kept strings.Builder
	kept.Grow(len(name))
	for _, r := range name {
		switch {
		case unicode.Is(unicode.Cc, r), unicode.Is(unicode.Cf, r),
			unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r):
			continue
		case r == '/' || r == '\\':
			kept.WriteRune('_')
		default:
			kept.WriteRune(r)
		}
	}

	name = strings.TrimSpace(kept.String())
	if len(name) > nameMaxBytes {
		name = strings.TrimSpace(name[:runeBoundaryAt(name, nameMaxBytes)])
	}
	if strings.Trim(name, ".") == "" {
		return ""
	}
	return name
}

// runeBoundaryAt returns the largest offset at or below limit that does not
// split a UTF-8 sequence. The input is valid UTF-8, so the offset the scan
// stops on starts a character: a continuation byte carries the two leading bits
// 10.
func runeBoundaryAt(s string, limit int) int {
	cut := limit
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return cut
}
