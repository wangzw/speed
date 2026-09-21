// Package memcached is the Memcached implementation of the key-value capability
// pkg/kv declares.
//
// It delivers the capability, and it is the one implementation whose backend
// cannot be trusted with an expiry. Two facts force the design here:
//
//   - Memcached's own increment takes an unsigned integer, and the capability's
//     takes a float64 delta. Every increment therefore reads, adds and writes
//     under a compare-and-swap, retrying when another writer got there first.
//     The server arbitrates, which is what the capability requires; it is just
//     arbitrating a swap rather than an add.
//   - A compare-and-swap write carries an expiry, and there is no way to say
//     "keep the one that is there". So the expiry is not kept in Memcached's
//     expiration field at all. It travels inside the value as a header, and
//     this implementation decides from it whether a key is still there. The
//     expiration field is left unset.
//
// It is also the one implementation whose data can vanish without a restart: a
// Memcached server drops entries under memory pressure as a matter of normal
// operation, and it does not report that it has. Its stance says so, and a host
// that needs the data it puts here has the wrong backend.
package memcached

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/bradfitz/gomemcache/memcache"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
)

const (
	// ModuleName is this implementation's name in the registry.
	ModuleName = "kv.memcached"
	// ConfigNamespace is where this implementation's input items are mounted.
	ConfigNamespace = ModuleName
	// headerLen is the width of the expiry header every stored value carries.
	headerLen = 8
	// casAttempts bounds the retries one swap-driven operation makes. Every
	// increment of one key is a read, an add and a swap here — Memcached's own
	// increment takes an unsigned integer and the capability's takes a float64
	// — so a contended key retries a great many times, and the bound exists so
	// that a pathological one fails rather than spins.
	casAttempts = 5000
)

// Config is what this implementation accepts.
type Config struct {
	// Servers is the list of addresses to spread keys over, as host:port. An
	// absent list is how an assembly says it does not run this backend.
	Servers []string `config:"servers"`
	// Timeout bounds one round trip to a server.
	Timeout time.Duration `config:"timeout"`
}

const (
	serversKey = "servers"
	timeoutKey = "timeout"
)

const defaultTimeout = 3 * time.Second

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor.
func Module() core.Module {
	return kv.NewModule(kv.Spec[Config]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &Config{Timeout: defaultTimeout},
		Items: map[string]config.Item{
			serversKey: {
				Description: "the Memcached servers to spread keys over, as a list of host:port; " +
					"leaving it out runs the assembly without this backend",
			},
			timeoutKey: {
				Description: "how long one round trip to a server may take, written with a unit",
			},
		},
		Stance: stance,
		Open:   open,
	})
}

// stance states whether this implementation runs.
//
// A configured server list states StateEnabled, so a second configured backend
// fails the startup. The reason on the disabled stance names the deployment
// fact rather than a missing setting alone, because this is a backend a host
// might reach for when what it wanted was something that keeps its data.
func stance(cfg *Config) core.Enablement {
	if len(cfg.Servers) == 0 {
		return core.Enablement{
			State: core.StateDisabled,
			Reason: fmt.Sprintf("%s.%s names no server, so this assembly runs without a Memcached "+
				"key-value store; set it to run on Memcached", ConfigNamespace, serversKey),
		}
	}
	return core.Enablement{State: core.StateEnabled}
}

// open builds the client. There is no ping to make: gomemcache connects lazily,
// and a server that is down fails the first operation rather than the open.
func open(_ context.Context, cfg *Config) (kv.KV, error) {
	client := memcache.New(cfg.Servers...)
	client.Timeout = cfg.Timeout
	return &store{client: client}, nil
}

// store is the capability over one client.
type store struct {
	client *memcache.Client
}

// Close releases nothing: gomemcache keeps no connection pool of its own, so
// there is no closer to call. It is defined all the same, so that the module's
// release path is exercised rather than skipped.
func (s *store) Close(context.Context) error { return nil }

// Get returns a copy of the value, absent when its header says it has expired.
func (s *store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	item, err := s.client.Get(encodeKey(key))
	if errors.Is(err, memcache.ErrCacheMiss) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	value, live := unwrap(item.Value)
	if !live {
		// Past its expiry, and Memcached was never told about it. Dropping it
		// here is what makes the key read as absent; nothing else will.
		return nil, false, nil
	}
	return value, true, nil
}

// Set stores the value, replacing any existing one and its expiry.
func (s *store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.client.Set(&memcache.Item{
		Key:   encodeKey(key),
		Value: wrap(value, expiryOf(ttl)),
	})
}

// Delete removes the key, whether or not it was there.
func (s *store) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.client.Delete(encodeKey(key)); err != nil && !errors.Is(err, memcache.ErrCacheMiss) {
		return err
	}
	return nil
}

// IncrByFloat adds to the number under key, leaving the key's expiry alone.
func (s *store) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	return s.increment(ctx, key, delta, 0, false)
}

// IncrByFloatWithTTL adds to the number under key and, when the key did not
// exist, gives it the expiry in the same step.
func (s *store) IncrByFloatWithTTL(ctx context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	return s.increment(ctx, key, delta, ttl, true)
}

// increment reads, adds and writes under a compare-and-swap.
//
// The read-add-write is inside the swap rather than beside it: the value that
// goes back carries the cas id of the value that came out, so a write that
// would overwrite someone else's is refused by the server and this loop starts
// again. That is what makes the operation atomic here — not a lock in this
// process, which would reach no other writer.
func (s *store) increment(ctx context.Context, key string, delta float64, ttl time.Duration, withTTL bool) (float64, error) {
	encoded := encodeKey(key)
	for attempt := 0; attempt < casAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		item, err := s.client.Get(encoded)
		switch {
		case errors.Is(err, memcache.ErrCacheMiss):
			// Nothing is there, so this call creates the key — with the ttl
			// when it was given one, and without when it was not.
			next := delta
			expires := time.Time{}
			if withTTL {
				expires = expiryOf(ttl)
			}
			// Add rather than Set, so a writer that got there between the miss
			// and this write wins instead of being overwritten.
			addErr := s.client.Add(&memcache.Item{
				Key:   encoded,
				Value: wrap([]byte(formatFloat(next)), expires),
			})
			if errors.Is(addErr, memcache.ErrNotStored) {
				backoff(attempt)
				continue
			}
			if addErr != nil {
				return 0, addErr
			}
			return next, nil
		case err != nil:
			return 0, err
		}

		value, expires, live := unwrapFull(item.Value)
		var current float64
		if live {
			parsed, err := strconv.ParseFloat(string(value), 64)
			if err != nil {
				return 0, fmt.Errorf("%w (key %q)", kv.ErrNotNumeric, key)
			}
			current = parsed
		} else {
			// Expired. The write that follows creates it, so the ttl applies.
			expires = time.Time{}
			if withTTL {
				expires = expiryOf(ttl)
			}
		}
		next := current + delta
		item.Value = wrap([]byte(formatFloat(next)), expires)
		if err := s.client.CompareAndSwap(item); err != nil {
			if errors.Is(err, memcache.ErrCASConflict) || errors.Is(err, memcache.ErrNotStored) {
				backoff(attempt)
				continue
			}
			return 0, err
		}
		return next, nil
	}
	return 0, fmt.Errorf("%s: %q stayed contested across %d attempts", ModuleName, key, casAttempts)
}

// backoff waits a jittered moment before the next attempt.
//
// Without it the contenders retry in lockstep and keep colliding: each round
// produces one winner and the rest immediately try again, which is the shape
// that made this loop exhaust its budget under fifty writers. The wait grows
// with the attempt count and is capped, so a quiet key pays nothing and a hot
// one spreads its writers out.
func backoff(attempt int) {
	ceiling := time.Duration(attempt+1) * 200 * time.Microsecond
	if ceiling > 5*time.Millisecond {
		ceiling = 5 * time.Millisecond
	}
	if ceiling > 0 {
		//nolint:gosec // the jitter only has to spread contenders out; nothing in a retry delay needs a value an attacker cannot predict
		time.Sleep(time.Duration(rand.Int64N(int64(ceiling))))
	}
}

// CompareAndSwap writes only on a match, and never touches the expiry.
func (s *store) CompareAndSwap(ctx context.Context, key string, old, newVal []byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	encoded := encodeKey(key)
	item, err := s.client.Get(encoded)
	if errors.Is(err, memcache.ErrCacheMiss) {
		// Nothing there. An empty old matches, which is what makes this
		// set-if-absent; anything else is a mismatch.
		if len(old) > 0 {
			return false, nil
		}
		addErr := s.client.Add(&memcache.Item{Key: encoded, Value: wrap(newVal, time.Time{})})
		if errors.Is(addErr, memcache.ErrNotStored) {
			return false, nil
		}
		if addErr != nil {
			return false, addErr
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}
	value, expires, live := unwrapFull(item.Value)
	if !live {
		// Expired reads as absent, so only an empty old matches.
		if len(old) > 0 {
			return false, nil
		}
		// The entry is still there as far as the server is concerned — it is
		// this implementation, not Memcached, that decided the key aged out —
		// so Add would be refused as a duplicate. Removing it first is what
		// makes the create below a create; a writer landing between the two
		// simply wins the Add, and the swap reports that it did not happen,
		// which is the right answer.
		if delErr := s.client.Delete(encoded); delErr != nil && !errors.Is(delErr, memcache.ErrCacheMiss) {
			return false, delErr
		}
		addErr := s.client.Add(&memcache.Item{Key: encoded, Value: wrap(newVal, time.Time{})})
		if errors.Is(addErr, memcache.ErrNotStored) {
			return false, nil
		}
		if addErr != nil {
			return false, addErr
		}
		return true, nil
	}
	if !bytes.Equal(value, old) {
		return false, nil
	}
	// The expiry is carried over rather than reset: the swap never changes when
	// a key stops being visible.
	item.Value = wrap(newVal, expires)
	if err := s.client.CompareAndSwap(item); err != nil {
		if errors.Is(err, memcache.ErrCASConflict) || errors.Is(err, memcache.ErrNotStored) {
			// Someone else wrote in between, which is a swap that did not
			// happen rather than a failure.
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// wrap puts the expiry in front of the value.
//
// The header is eight big-endian bytes of Unix nanoseconds, and zero means no
// expiry. An expiry lives here rather than in Memcached's own expiration field
// because a compare-and-swap write cannot carry the old one forward, and the
// capability says a swap and an increment both leave it where it was.
func wrap(value []byte, expires time.Time) []byte {
	out := make([]byte, headerLen+len(value))
	binary.BigEndian.PutUint64(out[:headerLen], stamp(expires))
	copy(out[headerLen:], value)
	return out
}

// unwrap returns the value, reporting whether it is still live.
func unwrap(stored []byte) ([]byte, bool) {
	value, _, live := unwrapFull(stored)
	return value, live
}

// unwrapFull returns the value, its expiry, and whether it is still live.
//
// A stored value too short to hold a header is reported as a value with no
// expiry rather than as a failure: nothing this package wrote has that shape,
// and a caller is better served by the bytes than by an error it cannot act on.
func unwrapFull(stored []byte) (value []byte, expires time.Time, live bool) {
	if len(stored) < headerLen {
		return stored, time.Time{}, true
	}
	//nolint:gosec // the header is this package's own: it holds a Unix nanosecond stamp, which fits an int64 by construction
	nanos := int64(binary.BigEndian.Uint64(stored[:headerLen]))
	if nanos == 0 {
		return stored[headerLen:], time.Time{}, true
	}
	expires = time.Unix(0, nanos)
	return stored[headerLen:], expires, time.Now().Before(expires)
}

// stamp is the header value for an expiry, or zero for none.
func stamp(expires time.Time) uint64 {
	if expires.IsZero() {
		return 0
	}
	return uint64(expires.UnixNano())
}

// expiryOf turns a ttl into the instant a key stops being visible.
func expiryOf(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}

// formatFloat renders a counter as the shortest text that round-trips.
func formatFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// encodeKey makes an arbitrary key acceptable to Memcached.
//
// The capability's keys are opaque strings; Memcached's may not contain a space
// or a control character and may not exceed 250 bytes. Base64 is injective and
// produces only characters it accepts, so every key this capability allows has
// one spelling here.
//
// It is not truncated when it runs long. A truncated key would silently put two
// different keys on one entry, which is worse than a refusal: Memcached's own
// "key too long" reaches the caller, and a caller that did not mean to use a
// 187-byte key is told rather than quietly served someone else's value.
func encodeKey(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}
