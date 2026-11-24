// Package memcached is the Memcached implementation of the key-value capability pkg/kv declares.
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
	casAttempts     = 5000
)

// Config is what this implementation accepts.
type Config struct {
	// Servers is the list of addresses to spread keys over, as host:port.
	// A host that names this implementation has to give it at least one.
	Servers []string `config:"servers"`
	// Timeout bounds one round trip to a server.
	Timeout time.Duration `config:"timeout"`
	// MaxIdleConns is how many idle connections the client keeps per server.
	// A burst of concurrent operations wider than this closes the surplus when it ends and dials it
	// again for the next burst, and every close leaves a local port in TIME_WAIT.
	MaxIdleConns int `config:"max-idle-conns"`
	// RequiresPersistence is the deployment fact this implementation's precondition is judged against:
	// whether the data written here has to survive a restart.
	// It states a fact about the deployment rather than a preference of this implementation.
	RequiresPersistence bool `config:"requires-persistence"`
}

const (
	serversKey      = "servers"
	timeoutKey      = "timeout"
	maxIdleConnsKey = "max-idle-conns"
	persistenceKey  = "requires-persistence"
)

const defaultTimeout = 3 * time.Second

// defaultMaxIdleConns is how many idle connections the client keeps per server; the library
// default of two makes a replica serving a few dozen concurrent requests dial anew each wave.
const defaultMaxIdleConns = 32

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

func open(ctx context.Context, cfg *Config) (kv.KV, error) {
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

type store struct {
	client *memcache.Client
	space  string
}

func (s *store) spell(key string) string {
	return s.space + encodeKey(key, maxKeyLen-len(s.space))
}

func (s *store) Close(context.Context) error { return s.client.Close() }

func (s *store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	item, err := s.client.Get(s.spell(key))
	if errors.Is(err, memcache.ErrCacheMiss) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	value, live := expiry.Unwrap(item.Value)
	if !live {
		return nil, false, nil
	}
	return value, true, nil
}

func (s *store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.client.Set(&memcache.Item{
		Key:   s.spell(key),
		Value: expiry.Wrap(value, expiry.Of(ttl)),
	})
}

func (s *store) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.client.Delete(s.spell(key)); err != nil && !errors.Is(err, memcache.ErrCacheMiss) {
		return err
	}
	return nil
}

func (s *store) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	return s.increment(ctx, key, delta, 0, false)
}

func (s *store) IncrByFloatWithTTL(ctx context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	return s.increment(ctx, key, delta, ttl, true)
}

func (s *store) increment(ctx context.Context, key string, delta float64, ttl time.Duration, withTTL bool) (float64, error) {
	if err := counter.CheckDelta(ModuleName, key, delta); err != nil {
		return 0, err
	}
	encoded := s.spell(key)
	for attempt := 0; attempt < casAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		item, err := s.client.Get(encoded)
		switch {
		case errors.Is(err, memcache.ErrCacheMiss):
			next := delta
			expires := time.Time{}
			if withTTL {
				expires = expiry.Of(ttl)
			}
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

func (s *store) CompareAndSwap(ctx context.Context, key string, old, newVal []byte) (bool, error) {
	return s.writeIf(ctx, key, func(value []byte, expires time.Time, live bool) ([]byte, time.Time, bool) {
		if !live {
			value, expires = nil, time.Time{}
		}
		if !bytes.Equal(value, old) {
			return nil, time.Time{}, false
		}
		return newVal, expires, true
	})
}

func (s *store) SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	return s.writeIf(ctx, key, func(_ []byte, _ time.Time, live bool) ([]byte, time.Time, bool) {
		if live {
			return nil, time.Time{}, false
		}
		return value, expiry.Of(ttl), true
	})
}

type decision func(value []byte, expires time.Time, live bool) ([]byte, time.Time, bool)

func (s *store) writeIf(ctx context.Context, key string, decide decision) (bool, error) {
	encoded := s.spell(key)
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

func formatFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

const maxKeyLen = 250

const digestPrefix = "sha256:"

func encodeKey(key string, limit int) string {
	plain := base64.RawURLEncoding.EncodeToString([]byte(key))
	if plain != "" && len(plain) <= limit {
		return plain
	}
	sum := sha256.Sum256([]byte(key))
	return digestPrefix + base64.RawURLEncoding.EncodeToString(sum[:])
}
