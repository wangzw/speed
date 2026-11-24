package ratelimit

import (
	"strconv"
	"strings"
)

const keyPrefix = "ratelimit:"

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

// escape percent-encodes ":" and "%" so no two distinct segment pairs build one
// key; "%" is replaced first so the escape of a colon is not escaped again.
func escape(segment string) string {
	if !strings.ContainsAny(segment, ":%") {
		return segment
	}
	segment = strings.ReplaceAll(segment, "%", "%25")
	return strings.ReplaceAll(segment, ":", "%3A")
}
