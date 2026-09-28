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
// Keys are respelled on the way in. JetStream's own key grammar excludes
// characters and lengths the capability's keys may have, so a key is stored
// under its hex spelling, or under a digest spelling when that would be empty
// or too long for a request to carry; encodeKey says why no two keys share a
// bucket entry either way.
package nats

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/kv/internal/counter"
	"github.com/vislake/speed/pkg/kv/internal/expiry"
	"github.com/vislake/speed/pkg/kv/internal/retry"
)

const (
	// ModuleName is this implementation's name in the registry.
	ModuleName = "kv.nats"
	// ConfigNamespace is where this implementation's input items are mounted.
	ConfigNamespace = ModuleName
	// defaultBucket is the bucket entries live in when the configuration names
	// none.
	defaultBucket = "speed_kv"
	// casAttempts bounds the retries one swap-driven operation makes.
	casAttempts = 5000
)

// Config is what this implementation accepts.
type Config struct {
	// URL is the server to connect to. A host that names this implementation
	// has to give it one. Marked sensitive: it carries the credentials.
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
				// A NATS URL carries the credentials to connect with — a user
				// and password or a token in its authority — so it must not
				// reach logs, diagnostics or the help output.
				Sensitive: true,
				Description: "the NATS server to connect to, with any credentials in the URL; an " +
					"assembly that names this implementation has to give it one",
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

// stance states this implementation's stance, or reports what it cannot start
// without.
//
// An absent server is a startup failure rather than a stand-down: a module named
// in the command tree has been committed to, so one that reaches NATS at a
// server nobody gave it stops the startup and says which input is missing. A
// configured server states StateEnabled so a second configured backend fails the
// startup.
func stance(cfg *Config) (core.Enablement, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return core.Enablement{}, fmt.Errorf("%s.%s gives no server, and this implementation reaches "+
			"NATS at one: with none set there is nothing to start against. Set it, or leave this "+
			"implementation out of the command", ConfigNamespace, urlKey)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// open connects and makes sure the bucket is there.
//
// The bucket is created here rather than assumed: JetStream's bucket is a
// server-side object that has to exist before a key can be written to it, so a
// host that pointed this module at a fresh server would otherwise fail on its
// first write rather than on startup.
func open(ctx context.Context, cfg *Config) (kv.KV, error) {
	nc, err := nats.Connect(cfg.URL,
		nats.Timeout(cfg.Timeout),
		// The connection reconnects for as long as the store is open. The
		// client's default gives up after sixty attempts two seconds apart
		// and closes the connection for good, so an outage of a few minutes
		// would leave every operation failing until the process restarted,
		// long after the server was back. An operation during the outage
		// fails on its own deadline either way.
		nats.MaxReconnects(-1))
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
	value, live := expiry.Unwrap(entry.Value())
	if !live {
		return nil, false, nil
	}
	return value, true, nil
}

// Set stores the value, replacing any existing one and its expiry.
func (s *store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	_, err := s.bucket.Put(ctx, encodeKey(key), expiry.Wrap(value, expiry.Of(ttl)))
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
	if err := counter.CheckDelta(ModuleName, key, delta); err != nil {
		return 0, err
	}
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
				expires = expiry.Of(ttl)
			}
			if _, createErr := s.bucket.Create(ctx, encoded, expiry.Wrap([]byte(formatFloat(next)), expires)); createErr != nil {
				if errors.Is(createErr, jetstream.ErrKeyExists) {
					retry.Backoff(attempt)
					continue
				}
				return 0, createErr
			}
			return next, nil
		}
		if err != nil {
			return 0, err
		}
		value, expires, live := expiry.UnwrapFull(entry.Value())
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
				expires = expiry.Of(ttl)
			}
		}
		next := current + delta
		if err := counter.CheckResult(ModuleName, key, next); err != nil {
			return 0, err
		}
		if _, updateErr := s.bucket.Update(ctx, encoded, expiry.Wrap([]byte(formatFloat(next)), expires), entry.Revision()); updateErr != nil {
			// A revision that no longer matches is what a lost race looks like
			// here, and JetStream reports it as ErrKeyRevisionMismatch rather
			// than as anything the key-exists sentinel covers.
			if errors.Is(updateErr, jetstream.ErrKeyNotFound) ||
				errors.Is(updateErr, jetstream.ErrKeyRevisionMismatch) {
				retry.Backoff(attempt)
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
		if _, createErr := s.bucket.Create(ctx, encoded, expiry.Wrap(newVal, time.Time{})); createErr != nil {
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
	value, expires, live := expiry.UnwrapFull(entry.Value())
	if !live {
		// Expired reads as absent, so only an empty old matches, and the write
		// that follows creates it without an expiry.
		if len(old) > 0 {
			return false, nil
		}
		if _, err := s.bucket.Update(ctx, encoded, expiry.Wrap(newVal, time.Time{}), entry.Revision()); err != nil {
			// Someone else wrote or removed it in between — both are a swap
			// that did not happen rather than a failure, and JetStream reports
			// the write case with a sentinel of its own.
			if errors.Is(err, jetstream.ErrKeyNotFound) ||
				errors.Is(err, jetstream.ErrKeyRevisionMismatch) {
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
	if _, updateErr := s.bucket.Update(ctx, encoded, expiry.Wrap(newVal, expires), entry.Revision()); updateErr != nil {
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

func formatFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// maxPlainKeyLen is the longest plain spelling a key keeps. A key's spelling is
// part of the subject every request carries, and a request line past the
// server's control line limit — 4096 bytes by default, bucket name and reply
// subject included — makes the server drop the client, closing the connection
// for every key at once. The bound keeps the subject far below that.
const maxPlainKeyLen = 1024

// digestPrefix starts the spelling of a key whose plain spelling JetStream
// would refuse or the server's control line could not carry. Its letters and
// its dot are outside the hex alphabet, so no plain spelling starts with it.
const digestPrefix = "sha256."

// encodeKey makes an arbitrary key acceptable to JetStream.
//
// Its key grammar admits a limited character set and no empty key, and the
// capability's keys are opaque strings of any length. The plain spelling is
// the key in hex, which is injective and uses only characters the grammar
// admits. A key whose plain spelling is empty or longer than maxPlainKeyLen is
// spelled instead as the prefix and the hex of its SHA-256 digest, which fits
// whatever the key's length.
//
// Neither spelling puts two keys on one entry. The plain spellings are
// injective among themselves, the digest spellings are as collision-resistant
// as SHA-256, and the two never meet, because no plain spelling contains the
// prefix's letters. A key is never cut short to fit: two keys sharing their
// first bytes would then share an entry.
func encodeKey(key string) string {
	if key != "" && 2*len(key) <= maxPlainKeyLen {
		return hex.EncodeToString([]byte(key))
	}
	sum := sha256.Sum256([]byte(key))
	return digestPrefix + hex.EncodeToString(sum[:])
}
