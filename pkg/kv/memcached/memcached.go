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
// operation, and it does not report that it has. Its stance says so — an
// assembly that declares its data has to survive a restart stops the startup,
// naming that declaration — rather than letting a host put data here and lose it
// silently.
package memcached

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bradfitz/gomemcache/memcache"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/kv/internal/counter"
	"github.com/vislake/speed/pkg/kv/internal/expiry"
	"github.com/vislake/speed/pkg/kv/internal/retry"
)

const (
	// ModuleName is this implementation's name in the registry.
	ModuleName = "kv.memcached"
	// ConfigNamespace is where this implementation's input items are mounted.
	ConfigNamespace = ModuleName
	// casAttempts bounds the retries one swap-driven operation makes. Every
	// increment of one key is a read, an add and a swap here — Memcached's own
	// increment takes an unsigned integer and the capability's takes a float64
	// — so a contended key retries a great many times, and the bound exists so
	// that a pathological one fails rather than spins.
	casAttempts = 5000
)

// Config is what this implementation accepts.
type Config struct {
	// Servers is the list of addresses to spread keys over, as host:port. A
	// host that names this implementation has to give it at least one.
	Servers []string `config:"servers"`
	// Timeout bounds one round trip to a server.
	Timeout time.Duration `config:"timeout"`
	// MaxIdleConns is how many idle connections the client keeps per server.
	// A burst of concurrent operations wider than this closes the surplus
	// when it ends and dials it again for the next burst, and every close
	// leaves a local port in TIME_WAIT. Zero or less takes the default.
	MaxIdleConns int `config:"max-idle-conns"`
	// RequiresPersistence is the deployment fact this implementation's
	// precondition is judged against: whether the data written here has to
	// survive a restart.
	//
	// It states a fact about the deployment rather than a preference of this
	// implementation. The default of false is the precondition holding — an
	// assembly that declares nothing is taken to accept the loss — so a host
	// that does need this data kept has to say so.
	RequiresPersistence bool `config:"requires-persistence"`
}

const (
	serversKey      = "servers"
	timeoutKey      = "timeout"
	maxIdleConnsKey = "max-idle-conns"
	persistenceKey  = "requires-persistence"
)

const defaultTimeout = 3 * time.Second

// defaultMaxIdleConns is how many idle connections the client keeps per server
// when the configuration names no number. The client library's own default is
// two, which has a replica serving a few dozen concurrent requests dial most
// of its connections anew for every wave of them.
const defaultMaxIdleConns = 32

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor.
func Module() core.Module {
	return kv.NewModule(kv.Spec[Config]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &Config{Timeout: defaultTimeout, MaxIdleConns: defaultMaxIdleConns},
		Items: map[string]config.Item{
			serversKey: {
				Description: "the Memcached servers to spread keys over, as a list of host:port; an " +
					"assembly that names this implementation has to give it at least one",
			},
			timeoutKey: {
				Description: "how long one round trip to a server may take, written with a unit",
			},
			maxIdleConnsKey: {
				Description: "how many idle connections the client keeps per server; a burst of " +
					"concurrent operations wider than this dials the surplus again every time",
			},
			persistenceKey: {
				Description: "whether the data this assembly writes has to survive a restart; this " +
					"implementation's server keeps nothing across one, so declaring that it must fails " +
					"the startup",
			},
		},
		Stance: stance,
		Open:   open,
	})
}

// stance states this implementation's stance, or reports what it cannot start
// without.
//
// A server list nobody gave it and a deployment that declares it needs its data
// kept are both startup failures rather than stand-downs: a module named in the
// command tree has been committed to, so one that cannot serve the assembly it
// found itself in stops the startup and names the input it stopped on. The
// second is the deployment precondition — this backend loses everything the
// server held without a restart, so a host that needs it kept has the wrong
// backend.
//
// A configured server list otherwise states StateEnabled, so a second configured
// backend fails the startup.
func stance(cfg *Config) (core.Enablement, error) {
	if _, ok := usableServers(cfg.Servers); !ok {
		return core.Enablement{}, fmt.Errorf("%s.%s names no usable server, and this implementation "+
			"reaches Memcached at one: with none set, or with an entry that is empty or only "+
			"whitespace, there is nothing to start against. Set it, or leave this implementation out "+
			"of the command", ConfigNamespace, serversKey)
	}
	if cfg.RequiresPersistence {
		return core.Enablement{}, fmt.Errorf("%s.%s is true, and this implementation's server keeps "+
			"nothing across a restart: a process restart, a rolling upgrade and a normal eviction under "+
			"memory pressure each drop the whole of it, and the server does not report that it has. "+
			"Point the command at a backend that keeps its data, or stop declaring that this assembly "+
			"needs it kept", ConfigNamespace, persistenceKey)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// usableServers returns the addresses this implementation reaches Memcached
// at, each with its surrounding whitespace trimmed, and reports whether the
// list holds at least one.
//
// Trimming here rather than in the judgement alone is what keeps the guard and
// the client on one value. Every entry goes straight to gomemcache, which
// resolves the whole list at once and keeps none of it when one address fails
// to resolve — so an entry this guard accepted and the client then refused
// would leave a process running on a client with no server at all, failing
// every operation with "no servers configured or available" and naming no
// input. An entry that is empty or only whitespace is still refused outright
// rather than skipped: dropping it would start against a list the host did not
// write.
func usableServers(servers []string) ([]string, bool) {
	if len(servers) == 0 {
		return nil, false
	}
	usable := make([]string, 0, len(servers))
	for _, s := range servers {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			return nil, false
		}
		usable = append(usable, trimmed)
	}
	return usable, true
}

// open builds the client and confirms every server answers it.
//
// The confirmation is what puts a connection failure where the capability
// says it goes, in the startup. gomemcache connects lazily, so without it a
// mistyped or unreachable server would pass the open and fail whichever request
// used the store first. Every server is asked, not one: keys are spread over
// the whole list, and an unreachable entry fails the keys that land on it.
func open(ctx context.Context, cfg *Config) (kv.KV, error) {
	// The stance has already refused a list holding no usable entry, so the
	// list here has one; trimming the addresses is what keeps the client on
	// the values the guard judged rather than on the text it was handed.
	servers, _ := usableServers(cfg.Servers)
	client := memcache.New(servers...)
	client.Timeout = cfg.Timeout
	client.MaxIdleConns = cfg.MaxIdleConns
	if client.MaxIdleConns <= 0 {
		client.MaxIdleConns = defaultMaxIdleConns
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := client.Ping(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("%s: the servers %s did not all answer: %w",
			ModuleName, strings.Join(servers, ", "), err)
	}
	return &store{client: client}, nil
}

// store is the capability over one client.
type store struct {
	client *memcache.Client
}

// Close releases the idle connections the client keeps between operations.
func (s *store) Close(context.Context) error { return s.client.Close() }

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
	value, live := expiry.Unwrap(item.Value)
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
		Value: expiry.Wrap(value, expiry.Of(ttl)),
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
	if err := counter.CheckDelta(ModuleName, key, delta); err != nil {
		return 0, err
	}
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
				expires = expiry.Of(ttl)
			}
			// Add rather than Set, so a writer that got there between the miss
			// and this write wins instead of being overwritten.
			addErr := s.client.Add(&memcache.Item{
				Key:   encoded,
				Value: expiry.Wrap([]byte(formatFloat(next)), expires),
			})
			if errors.Is(addErr, memcache.ErrNotStored) {
				retry.Backoff(attempt)
				continue
			}
			if addErr != nil {
				return 0, addErr
			}
			return next, nil
		case err != nil:
			return 0, err
		}

		value, expires, live := expiry.UnwrapFull(item.Value)
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
				expires = expiry.Of(ttl)
			}
		}
		next := current + delta
		if err := counter.CheckResult(ModuleName, key, next); err != nil {
			return 0, err
		}
		item.Value = expiry.Wrap([]byte(formatFloat(next)), expires)
		if err := s.client.CompareAndSwap(item); err != nil {
			// A conflict, or the entry is gone — deleted or evicted between
			// the read and the write, which a compare-and-swap reports as a
			// miss. Both are contention rather than failure: the retry reads
			// it again, and a miss with nothing to read creates the key.
			if errors.Is(err, memcache.ErrCASConflict) ||
				errors.Is(err, memcache.ErrNotStored) ||
				errors.Is(err, memcache.ErrCacheMiss) {
				retry.Backoff(attempt)
				continue
			}
			return 0, err
		}
		return next, nil
	}
	return 0, fmt.Errorf("%s: %q stayed contested across %d attempts", ModuleName, key, casAttempts)
}

// CompareAndSwap writes only on a match, and never touches the expiry.
func (s *store) CompareAndSwap(ctx context.Context, key string, old, newVal []byte) (bool, error) {
	return s.writeIf(ctx, key, func(value []byte, expires time.Time, live bool) ([]byte, time.Time, bool) {
		if !live {
			// Missing or expired reads as empty, so only an empty old
			// matches, and the write creates the key without an expiry.
			value, expires = nil, time.Time{}
		}
		if !bytes.Equal(value, old) {
			return nil, time.Time{}, false
		}
		// The expiry is carried over rather than reset: the swap never
		// changes when a key stops being visible.
		return newVal, expires, true
	})
}

// SetIfAbsent stores the value with its expiry when the key is missing or
// expired, and leaves a live key alone. It is a conditional write like the
// swap, so an expired entry several writers race for has exactly one winner.
func (s *store) SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	return s.writeIf(ctx, key, func(_ []byte, _ time.Time, live bool) ([]byte, time.Time, bool) {
		if live {
			return nil, time.Time{}, false
		}
		return value, expiry.Of(ttl), true
	})
}

// decision is what a conditional write makes of the entry it found: the value
// it read, that value's expiry, and whether the key is live. It returns the
// value and expiry to write, or false to leave the entry alone.
type decision func(value []byte, expires time.Time, live bool) ([]byte, time.Time, bool)

// writeIf writes what decide asks for, only if the entry decide judged is
// still the one there, and reports whether it wrote.
//
// Every write here is conditional on the server: a missing key is created with
// Add, which fails when someone else created it first, and an entry that is
// there — live or past its expiry — is replaced with a compare-and-swap
// carrying the cas id it was read with, which fails when anyone else has
// written it since. An expired entry goes through the second path like a live
// one. It is still an entry to the server, because it is this implementation
// and not Memcached that decided it aged out, and replacing it under its cas
// id is what gives several writers racing for it exactly one winner.
//
// A write refused because the entry changed is not an answer yet: the entry is
// read again and judged afresh, and the loop ends when decide refuses what is
// now there or a write lands. Answering false on the refusal alone would be
// wrong whenever the writer that got in first left the entry still matching.
func (s *store) writeIf(ctx context.Context, key string, decide decision) (bool, error) {
	encoded := encodeKey(key)
	for attempt := 0; attempt < casAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		item, err := s.client.Get(encoded)
		if errors.Is(err, memcache.ErrCacheMiss) {
			value, expires, ok := decide(nil, time.Time{}, false)
			if !ok {
				return false, nil
			}
			addErr := s.client.Add(&memcache.Item{Key: encoded, Value: expiry.Wrap(value, expires)})
			if errors.Is(addErr, memcache.ErrNotStored) {
				retry.Backoff(attempt)
				continue
			}
			if addErr != nil {
				return false, addErr
			}
			return true, nil
		}
		if err != nil {
			return false, err
		}
		current, currentExpires, live := expiry.UnwrapFull(item.Value)
		value, expires, ok := decide(current, currentExpires, live)
		if !ok {
			return false, nil
		}
		item.Value = expiry.Wrap(value, expires)
		if casErr := s.client.CompareAndSwap(item); casErr != nil {
			// Someone else wrote or removed it in between — the miss included,
			// which is what a delete or an eviction between the read and the
			// write reports. Each is contention, not failure.
			if errors.Is(casErr, memcache.ErrCASConflict) ||
				errors.Is(casErr, memcache.ErrNotStored) ||
				errors.Is(casErr, memcache.ErrCacheMiss) {
				retry.Backoff(attempt)
				continue
			}
			return false, casErr
		}
		return true, nil
	}
	return false, fmt.Errorf("%s: %q stayed contested across %d attempts", ModuleName, key, casAttempts)
}

// formatFloat renders a counter as the shortest text that round-trips.
func formatFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// maxKeyLen is the longest key Memcached accepts.
const maxKeyLen = 250

// digestPrefix starts the spelling of a key whose plain spelling Memcached
// would refuse. The colon is outside the base64url alphabet, so no plain
// spelling starts with it.
const digestPrefix = "sha256:"

// encodeKey makes an arbitrary key acceptable to Memcached.
//
// The capability's keys are opaque strings of any length; Memcached's may not
// contain a space or a control character, may not be empty and may not exceed
// 250 bytes. The plain spelling is the key in base64url, which is injective and
// uses only characters Memcached accepts. A key whose plain spelling is empty
// or too long is spelled instead as the prefix and the base64url of its
// SHA-256 digest, which fits whatever the key's length.
//
// Neither spelling puts two keys on one entry. The plain spellings are
// injective among themselves, the digest spellings are as collision-resistant
// as SHA-256, and the two never meet, because no plain spelling contains the
// prefix's colon. A key is never cut short to fit: two keys sharing their first
// 250 bytes would then share an entry.
func encodeKey(key string) string {
	plain := base64.RawURLEncoding.EncodeToString([]byte(key))
	if plain != "" && len(plain) <= maxKeyLen {
		return plain
	}
	sum := sha256.Sum256([]byte(key))
	return digestPrefix + base64.RawURLEncoding.EncodeToString(sum[:])
}
