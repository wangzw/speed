// Package kv declares the key-value capability: opaque bytes under an opaque key, with an expiry and atomic operations.
// It registers no module; the implementation subpackages each deliver the capability exclusively.
package kv

import (
	"context"
	"errors"
	"time"
)

// ErrNotNumeric is returned by the increment operations when the key already holds a value that is
// not a valid float64 encoding.
// The stored value is left untouched.
var ErrNotNumeric = errors.New("kv: value is not a valid float")

// KV is the key-value contract every implementation delivers.
type KV interface {
	// Get returns the value stored under key.
	// The boolean reports whether the key was present; a key whose expiry has passed counts as absent.
	Get(ctx context.Context, key string) (value []byte, present bool, err error)

	// Set stores value under key, replacing any existing value and expiry.
	// A ttl greater than zero expires the key after that duration; a ttl of zero or less stores it
	// without an expiry.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// Delete removes key.
	// Deleting a key that does not exist is not an error.
	Delete(ctx context.Context, key string) error

	// IncrByFloat adds delta to the number stored under key and returns the result.
	IncrByFloat(ctx context.Context, key string, delta float64) (float64, error)

	// IncrByFloatWithTTL is IncrByFloat that atomically attaches ttl as the expiry of the key it creates; an existing key keeps its expiry and ignores ttl.
	IncrByFloatWithTTL(ctx context.Context, key string, delta float64, ttl time.Duration) (float64, error)

	// CompareAndSwap replaces the value under key with newVal only if the current value equals old,
	// and reports whether the swap happened.
	CompareAndSwap(ctx context.Context, key string, old, newVal []byte) (bool, error)

	// SetIfAbsent stores value under key with ttl as its expiry, only if the key is missing or
	// expired, and reports whether it stored.
	// A live key is left as it is, value and expiry both, and the call returns false with a nil error.
	SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
}
