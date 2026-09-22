package ratelimit

import (
	"strconv"
	"strings"
)

// keyPrefix starts every counting key. The key-value primitives partition
// nothing, so a consumer that did not name its own keys would collide with
// another one silently.
const keyPrefix = "ratelimit:"

// countKey builds the key one identity string's count for one window lives
// under, inside one tenant.
//
// The key is not a contract: nothing outside this package reads it, and
// changing it only restarts the counting.
func countKey(tenantSegment, subject string, window int64) string {
	var b strings.Builder
	b.WriteString(keyPrefix)
	b.WriteString(escape(tenantSegment))
	b.WriteByte(':')
	b.WriteString(escape(subject))
	b.WriteByte(':')
	b.WriteString(strconv.FormatInt(window, 10))
	return b.String()
}

// escape percent-encodes the two characters that would otherwise be read as
// separators, so no two distinct pairs of segments can build one key.
//
// The tenant identifier's domain excludes only whitespace and control
// characters, so a colon is legal in it; an identity string is two halves
// joined by a separator and nothing keeps a colon out of either. Left
// unescaped, tenant "a:b" with identity "c" and tenant "a" with identity
// "b:c" would build the same key and share one count across two tenants.
//
// The percent sign is replaced first, so the escape of a colon is not escaped
// again.
func escape(segment string) string {
	if !strings.ContainsAny(segment, ":%") {
		return segment
	}
	segment = strings.ReplaceAll(segment, "%", "%25")
	return strings.ReplaceAll(segment, ":", "%3A")
}
