// Package nats is the NATS JetStream implementation of the key-value capability
// pkg/kv declares.
//
// It stores entries in a JetStream key-value bucket. Two of the capability's
// requirements meet a backend that was not built for them, and both are handled
// the same way the other backends without the feature handle them:
//
//   - JetStream has no arithmetic on a stored value. Every increment is a read,
//     an add and a write that carries the revision it read, retried when
//     another writer got there first. The server arbitrates the write, which is
//     what the capability requires.
//   - A bucket's time-to-live is one setting for the whole bucket, and the
//     capability needs one per key and needs an increment to leave it alone. So
//     the expiry is not the bucket's: it travels in front of the value, and
//     this package decides from it whether a key is still there.
//
// Keys are hex-encoded on the way in. JetStream's own key grammar excludes
// characters the capability's keys may contain, and the encoding is injective,
// so no two of its keys share a bucket entry.
package nats

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
)

const (
	// ModuleName is this implementation's name in the registry.
	ModuleName = "kv.nats"
	// ConfigNamespace is where this implementation's input items are mounted.
	ConfigNamespace = ModuleName
	// defaultBucket is the bucket entries live in when the configuration names
	// none.
	defaultBucket = "speed_kv"
	// headerLen is the width of the expiry header every stored value carries.
	headerLen = 8
	// casAttempts bounds the retries one swap-driven operation makes.
	casAttempts = 5000
)

// Config is what this implementation accepts.
type Config struct {
	// URL is the server to connect to. An absent one is how an assembly says it
	// does not run this backend.
	URL string `config:"url"`
	// Bucket is the key-value bucket to use.
	Bucket string `config:"bucket"`
	// Timeout bounds connecting and one operation.
	Timeout time.Duration `config:"timeout"`
}

const (
	urlKey     = "url"
	bucketKey  = "bucket"
	timeoutKey = "timeout"
)

const defaultTimeout = 5 * time.Second

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor.
func Module() core.Module {
	return kv.NewModule(kv.Spec[Config]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &Config{Bucket: defaultBucket, Timeout: defaultTimeout},
		Items: map[string]config.Item{
			urlKey: {
				Description: "the NATS server to connect to; leaving it out runs the assembly " +
					"without this backend",
			},
			bucketKey: {
				Description: "the JetStream key-value bucket entries are kept in",
			},
			timeoutKey: {
				Description: "how long connecting and one operation may take, written with a unit",
			},
		},
		Stance: stance,
		Open:   open,
	})
}

// stance states whether this implementation runs.
func stance(cfg *Config) core.Enablement {
	if cfg.URL == "" {
		return core.Enablement{
			State: core.StateDisabled,
			Reason: fmt.Sprintf("%s.%s gives no server, so this assembly runs without a NATS "+
				"key-value store; set it to run on NATS", ConfigNamespace, urlKey),
		}
	}
	return core.Enablement{State: core.StateEnabled}
}

// open connects and makes sure the bucket is there.
//
// The bucket is created here rather than assumed: JetStream's bucket is a
// server-side object that has to exist before a key can be written to it, so a
// host that pointed this module at a fresh server would otherwise fail on its
// first write rather than on startup.
func open(ctx context.Context, cfg *Config) (kv.KV, error) {
	nc, err := nats.Connect(cfg.URL, nats.Timeout(cfg.Timeout))
	if err != nil {
		return nil, fmt.Errorf("%s: connecting: %w", ModuleName, err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("%s: opening JetStream: %w", ModuleName, err)
	}
	bucket, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: cfg.Bucket})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("%s: opening the bucket %q: %w", ModuleName, cfg.Bucket, err)
	}
	return &store{nc: nc, bucket: bucket}, nil
}

// store is the capability over one bucket.
type store struct {
	nc     *nats.Conn
	bucket jetstream.KeyValue
}

// Close releases the connection.
func (s *store) Close(context.Context) error {
	s.nc.Close()
	return nil
}

// Get returns a copy of the value, absent when its header says it has expired.
func (s *store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	entry, err := s.bucket.Get(ctx, encodeKey(key))
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	value, live := unwrap(entry.Value())
	if !live {
		return nil, false, nil
	}
	return value, true, nil
}

// Set stores the value, replacing any existing one and its expiry.
func (s *store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	_, err := s.bucket.Put(ctx, encodeKey(key), wrap(value, expiryOf(ttl)))
	return err
}

// Delete removes the key, whether or not it was there.
func (s *store) Delete(ctx context.Context, key string) error {
	err := s.bucket.Delete(ctx, encodeKey(key))
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil
	}
	return err
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

// increment reads, adds and writes under the revision it read.
//
// The revision is what the server arbitrates on: an Update carrying a stale
// revision is refused, and this loop starts again. A Create for the absent case
// plays the same role — it fails when someone else got there first, rather than
// overwriting them.
func (s *store) increment(ctx context.Context, key string, delta float64, ttl time.Duration, withTTL bool) (float64, error) {
	encoded := encodeKey(key)
	for attempt := 0; attempt < casAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		entry, err := s.bucket.Get(ctx, encoded)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			next := delta
			expires := time.Time{}
			if withTTL {
				expires = expiryOf(ttl)
			}
			if _, createErr := s.bucket.Create(ctx, encoded, wrap([]byte(formatFloat(next)), expires)); createErr != nil {
				if errors.Is(createErr, jetstream.ErrKeyExists) {
					backoff(attempt)
					continue
				}
				return 0, createErr
			}
			return next, nil
		}
		if err != nil {
			return 0, err
		}
		value, expires, live := unwrapFull(entry.Value())
		var current float64
		if live {
			parsed, err := strconv.ParseFloat(string(value), 64)
			if err != nil {
				return 0, fmt.Errorf("%w (key %q)", kv.ErrNotNumeric, key)
			}
			current = parsed
		} else {
			// Expired: the write that follows creates it, so the ttl applies.
			expires = time.Time{}
			if withTTL {
				expires = expiryOf(ttl)
			}
		}
		next := current + delta
		if _, updateErr := s.bucket.Update(ctx, encoded, wrap([]byte(formatFloat(next)), expires), entry.Revision()); updateErr != nil {
			// A revision that no longer matches is what a lost race looks like
			// here, and JetStream reports it as ErrKeyRevisionMismatch rather
			// than as anything the key-exists sentinel covers.
			if errors.Is(updateErr, jetstream.ErrKeyNotFound) ||
				errors.Is(updateErr, jetstream.ErrKeyRevisionMismatch) {
				backoff(attempt)
				continue
			}
			return 0, updateErr
		}
		return next, nil
	}
	return 0, fmt.Errorf("%s: %q stayed contested across %d attempts", ModuleName, key, casAttempts)
}

// CompareAndSwap writes only on a match, and never touches the expiry.
func (s *store) CompareAndSwap(ctx context.Context, key string, old, newVal []byte) (bool, error) {
	encoded := encodeKey(key)
	entry, err := s.bucket.Get(ctx, encoded)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		if len(old) > 0 {
			return false, nil
		}
		if _, createErr := s.bucket.Create(ctx, encoded, wrap(newVal, time.Time{})); createErr != nil {
			if errors.Is(createErr, jetstream.ErrKeyExists) {
				return false, nil
			}
			return false, createErr
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}
	value, expires, live := unwrapFull(entry.Value())
	if !live {
		// Expired reads as absent, so only an empty old matches, and the write
		// that follows creates it without an expiry.
		if len(old) > 0 {
			return false, nil
		}
		if _, err := s.bucket.Update(ctx, encoded, wrap(newVal, time.Time{}), entry.Revision()); err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	if !bytes.Equal(value, old) {
		return false, nil
	}
	// The expiry is carried over rather than reset.
	if _, updateErr := s.bucket.Update(ctx, encoded, wrap(newVal, expires), entry.Revision()); updateErr != nil {
		// Someone else wrote or removed it in between — both are a swap that
		// did not happen rather than a failure, and JetStream reports the write
		// case with a sentinel of its own.
		if errors.Is(updateErr, jetstream.ErrKeyNotFound) ||
			errors.Is(updateErr, jetstream.ErrKeyRevisionMismatch) {
			return false, nil
		}
		return false, updateErr
	}
	return true, nil
}

// backoff waits a jittered moment before the next attempt, so that contenders
// do not retry in lockstep and keep colliding.
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

// wrap puts the expiry in front of the value.
//
// The header is eight big-endian bytes of Unix nanoseconds, and zero means no
// expiry. It is here rather than in the bucket's own time-to-live because that
// setting is one value for the whole bucket, and the capability needs one per
// key and needs an increment to leave it alone.
func wrap(value []byte, expires time.Time) []byte {
	out := make([]byte, headerLen+len(value))
	binary.BigEndian.PutUint64(out[:headerLen], stamp(expires))
	copy(out[headerLen:], value)
	return out
}

func unwrap(stored []byte) ([]byte, bool) {
	value, _, live := unwrapFull(stored)
	return value, live
}

// unwrapFull returns the value, its expiry, and whether it is still live.
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

func stamp(expires time.Time) uint64 {
	if expires.IsZero() {
		return 0
	}
	return uint64(expires.UnixNano())
}

func expiryOf(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}

func formatFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// encodeKey makes an arbitrary key acceptable to JetStream.
//
// Its key grammar admits a limited character set, and the capability's keys are
// opaque strings. Hex is injective, so no two of the capability's keys share a
// bucket entry — which a truncated or lossy encoding would allow silently.
func encodeKey(key string) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 0, len(key)*2)
	for i := 0; i < len(key); i++ {
		out = append(out, hex[key[i]>>4], hex[key[i]&0x0f])
	}
	return string(out)
}
