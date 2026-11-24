// Package expiry is the expiry header the key-value backends without native per-key TTL put in front of every stored value.
package expiry

import (
	"encoding/binary"
	"time"
)

// Width is the width in bytes of the header every stored value carries.
const Width = 8

// Wrap puts the expiry in front of the value.
// The header is eight big-endian bytes of Unix nanoseconds, and zero means no expiry.
func Wrap(value []byte, expires time.Time) []byte {
	out := make([]byte, Width+len(value))
	binary.BigEndian.PutUint64(out[:Width], stamp(expires))
	copy(out[Width:], value)
	return out
}

// Unwrap returns the value, reporting whether it is still live.
func Unwrap(stored []byte) ([]byte, bool) {
	value, _, live := UnwrapFull(stored)
	return value, live
}

// UnwrapFull returns the value, its expiry, and whether it is still live.
func UnwrapFull(stored []byte) (value []byte, expires time.Time, live bool) {
	if len(stored) < Width {
		return stored, time.Time{}, true
	}
	//nolint:gosec // the header is this package's own: it holds a Unix nanosecond stamp, which fits an int64 by construction
	nanos := int64(binary.BigEndian.Uint64(stored[:Width]))
	if nanos == 0 {
		return stored[Width:], time.Time{}, true
	}
	expires = time.Unix(0, nanos)
	return stored[Width:], expires, time.Now().Before(expires)
}

func stamp(expires time.Time) uint64 {
	if expires.IsZero() {
		return 0
	}
	return uint64(expires.UnixNano())
}

// Of turns a ttl into the instant a key stops being visible.
// Zero or less means no expiry, the convention the capability's writes share.
func Of(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}
