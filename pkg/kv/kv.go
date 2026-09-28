// Package kv declares the key-value capability: opaque bytes stored under an
// opaque key, with an expiry and three atomic operations.
//
// This is the interface package. It registers no module: what registers are the
// implementation subpackages, each binding one backend and each delivering this
// capability exclusively. A module that needs key-value storage imports this
// package and takes the capability up with core.Resolve[kv.KV](reg).
//
// The surface is designed against the weakest backend it must support, and that
// is the constraint the rest of it follows. It exposes no server-side scripting,
// no pipelines, no ordered structures and no data type beyond opaque bytes.
// Atomicity is expressed through the increment pair and the compare-and-swap,
// which every backend can honour; a caller that needs a read-modify-write cycle
// builds it from those rather than from a Get followed by a Set.
//
// Values are opaque and the module never interprets one, with the single
// exception of the numeric encoding the increment operations read and write.
package kv

import (
	"context"
	"errors"
	"time"
)

// ErrNotNumeric is returned by the increment operations when the key already
// holds a value that is not a valid float64 encoding. The stored value is left
// untouched.
//
// The offending value is deliberately left out of the error text: a key-value
// store may hold sensitive data.
var ErrNotNumeric = errors.New("kv: value is not a valid float")

// KV is the key-value contract every implementation delivers.
//
// Implementations must be safe for concurrent use by multiple goroutines, must
// not retain or hand out the caller's slices, and must honour a cancelled
// context by returning its error instead of performing the operation.
//
// Atomicity is the backend's, not this package's: across replicas, two
// increments of one key must be arbitrated by the backend. A lock in the
// implementation would not reach that far — and for a backend that is one
// process's memory, the absence is what makes the deployment precondition in
// its own documentation load-bearing.
type KV interface {
	// Get returns the value stored under key. The boolean reports whether the
	// key was present; a key whose expiry has passed counts as absent. A
	// missing key is not an error, so the returned error is non-nil only when
	// the store itself failed.
	Get(ctx context.Context, key string) (value []byte, present bool, err error)

	// Set stores value under key, replacing any existing value and expiry. A
	// ttl greater than zero expires the key after that duration; a ttl of zero
	// or less stores it without an expiry.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// Delete removes key. Deleting a key that does not exist is not an error.
	Delete(ctx context.Context, key string) error

	// IncrByFloat adds delta to the number stored under key and returns the
	// result. A missing or expired key starts from zero and is stored without
	// an expiry; a key that already exists keeps the expiry it has, so counters
	// under a rolling window are not extended by being incremented. The value
	// is stored as its shortest exact decimal encoding, which callers should
	// parse with strconv.ParseFloat rather than compare as text. A key holding
	// a non-numeric value fails with ErrNotNumeric and is left unchanged.
	//
	// A counter holds a finite number. A delta that is not finite, and an
	// increment whose sum no float64 holds, fail with an error that is not
	// ErrNotNumeric and write nothing. A value put in place by Set is read as a
	// number by every implementation when it is plain decimal text — an
	// optional sign, digits, an optional fraction and exponent, which is the
	// form the increments write — and other spellings (hexadecimal, digit
	// separators, surrounding space, NaN and the infinities) are read as a
	// number by some implementations and refused by others.
	//
	// The arithmetic is not guaranteed bit-identical across implementations for
	// a delta not exactly representable in binary float64 (0.1, for instance):
	// some backends round after every accumulation, others accumulate at a
	// wider precision and round once at the end, and the two paths can land on
	// different, equally valid, adjacent float64 values. Callers that need
	// cross-backend-identical results should keep to integer-valued deltas,
	// which are exactly representable.
	IncrByFloat(ctx context.Context, key string, delta float64) (float64, error)

	// IncrByFloatWithTTL adds delta to the number stored under key and returns
	// the result, exactly like IncrByFloat, but atomically attaches ttl as the
	// key's expiry on the same call that creates it: a missing or expired key
	// starts from zero and is stored with ttl as its expiry (a ttl of zero or
	// less stores it without one, matching Set's convention); a key that
	// already exists is incremented and keeps the expiry it already has — ttl
	// is ignored for a live key, never extending it, so a rolling-window
	// counter under sustained traffic still ages out on schedule.
	//
	// This exists to close a concurrency gap IncrByFloat alone cannot:
	// attaching an expiry to a freshly created key otherwise needs a caller-side
	// Get-then-Set after IncrByFloat, and any concurrent increment landing in
	// that Get-to-Set gap is silently overwritten by the Set, undercounting the
	// key. Every implementation must perform this as one atomic step, never as
	// two calls a caller could still race between.
	IncrByFloatWithTTL(ctx context.Context, key string, delta float64, ttl time.Duration) (float64, error)

	// CompareAndSwap replaces the value under key with newVal only if the
	// current value equals old, and reports whether the swap happened. A
	// missing or expired key matches an old of nil or zero length, which makes
	// this usable as set-if-absent. A mismatch is an expected outcome and
	// returns false with a nil error. The swap never changes the key's expiry.
	CompareAndSwap(ctx context.Context, key string, old, newVal []byte) (bool, error)
}
