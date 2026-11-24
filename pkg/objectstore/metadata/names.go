package metadata

import (
	"strings"
	"unicode"
)

const nameMaxBytes = 255

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

func runeBoundaryAt(s string, limit int) int {
	cut := limit
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return cut
}
