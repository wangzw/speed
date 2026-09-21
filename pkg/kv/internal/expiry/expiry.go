// Package expiry is the expiry header that the key-value backends unable to
// carry per-key time-to-live natively put in front of every stored value.
//
// Both users — nats and memcached — store values as header-plus-value bytes,
// and each for its own reason: JetStream's time-to-live is one value for a
// whole bucket, and Memcached's expiration field cannot be carried forward by
// the write shapes the capability needs. Either way the bytes of a stored value
// are a contract between what was written and what is read back, and it lives
// here so that the two backends cannot drift apart on what those bytes mean.
package expiry

import (
	"encoding/binary"
	"time"
)

// Width is the width in bytes of the header every stored value carries.
const Width = 8

// Wrap puts the expiry in front of the value.
//
// The header is eight big-endian bytes of Unix nanoseconds, and zero means no
// expiry.
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
//
// A stored value too short to hold a header is reported as a value with no
// expiry rather than as a failure: nothing either backend wrote has that shape,
// and a caller is better served by the bytes than by an error it cannot act on.
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

// stamp is the header value for an expiry, or zero for none.
func stamp(expires time.Time) uint64 {
	if expires.IsZero() {
		return 0
	}
	return uint64(expires.UnixNano())
}

// Of turns a ttl into the instant a key stops being visible. Zero or less means
// no expiry, the convention the capability's writes share.
func Of(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}
