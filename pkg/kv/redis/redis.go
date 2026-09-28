// Package redis is the Redis implementation of the key-value capability pkg/kv
// declares.
//
// It binds the Redis client, names the configuration namespace this backend's
// input items hang under, and delivers the capability exclusively. The client
// itself comes from pkg/internal/redis, which the event module's Redis backend uses too,
// so the two share one notion of what connecting means.
//
// Every operation is one command or one script. The increments and the swap
// cannot be a plain command: attaching an expiry only to a key an increment
// creates, refusing a sum no float64 holds without having written it, and
// swapping a value only when it matches. Redis has no verb for any of them,
// and doing them as two commands would leave the gap this module exists to
// close — an increment landing between the two would be lost, and a swap would
// not be one. They run as scripts, which Redis executes atomically.
package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/redis"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/kv/internal/counter"
)

const (
	// ModuleName is this implementation's name in the registry.
	ModuleName = "kv.redis"
	// ConfigNamespace is where this implementation's input items are mounted.
	ConfigNamespace = ModuleName
	// keyPrefix namespaces every key this implementation touches. The key-value
	// backend and the event module's Redis backend both talk to whatever server
	// and database the configuration names, and an assembly may point them at
	// the same one; without this, a key here that reads like one of the event
	// streams — "speed:events:" and a type name — would let a SET replace the
	// stream, and the events accumulated in it would be gone with no trace.
	// The event backend carries its own prefix for the same reason.
	keyPrefix = "speed:kv:"
)

// prefixed returns the key this implementation stores under.
func prefixed(key string) string { return keyPrefix + key }

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
func Module() core.Module {
	proto := redis.Defaults()
	return kv.NewModule(kv.Spec[redis.Config]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &proto,
		Items:           redis.Items(),
		Stance:          stance,
		Open:            open,
	})
}

// stance states this implementation's stance, or reports what it cannot start
// without.
//
// An absent address is a startup failure rather than a stand-down: a module
// named in the command tree has been committed to, so one that reaches Redis at
// a server nobody gave it stops the startup and says which input is missing. A
// configured address states StateEnabled rather than StateAuto, so that a second
// configured backend collides here at startup instead of one of them winning
// silently.
func stance(cfg *redis.Config) (core.Enablement, error) {
	if strings.TrimSpace(cfg.Addr) == "" {
		return core.Enablement{}, fmt.Errorf("%s.%s gives no server, and this implementation reaches "+
			"Redis at one: with none set there is nothing to start against. Set it to the server to run "+
			"on, or leave this implementation out of the command", ConfigNamespace, redis.AddrKey)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// open builds the store.
func open(ctx context.Context, cfg *redis.Config) (kv.KV, error) {
	client, err := redis.Connect(ctx, *cfg)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ModuleName, err)
	}
	return &store{client: client}, nil
}

// store is the capability over one client.
type store struct {
	client *goredis.Client
}

// Close releases the client, and with it the pool.
func (s *store) Close(_ context.Context) error {
	return s.client.Close()
}

// Get returns a copy of the value. The client allocates it, so this module
// never holds a slice the caller can write into and never hands out one of its
// own; Redis values are binary-safe, so no encoding stands between the two.
func (s *store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	value, err := s.client.Get(ctx, prefixed(key)).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

// Set stores the value with the given expiry.
func (s *store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return s.client.Set(ctx, prefixed(key), value, plainTTL(ttl)).Err()
}

// plainTTL turns the capability's ttl into the duration the client library
// sends: zero for no expiry, and otherwise the whole milliseconds ttlMillis
// gives.
//
// The library does not agree with the capability on what a negative duration
// means: it carries a KeepTTL constant of -1, and a SET given it leaves the
// key's existing expiry alone instead of clearing it. Passing the caller's
// duration straight through therefore makes `Set(k, v, -1)` — a caller saying
// "no expiry, from now on" — keep the expiry the key already had. A duration
// under a millisecond the library rounds up itself, but it prints a warning
// to the standard error each time, outside the logging the assembly set up.
func plainTTL(ttl time.Duration) time.Duration {
	return time.Duration(ttlMillis(ttl)) * time.Millisecond
}

// Delete removes the key. Redis reports how many keys it removed, and removing
// an absent one is not an error.
func (s *store) Delete(ctx context.Context, key string) error {
	return s.client.Del(ctx, prefixed(key)).Err()
}

// IncrByFloat uses the server's own counter. Redis leaves an existing key's
// expiry alone when it increments, which is the semantics the capability
// requires.
func (s *store) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	return s.increment(ctx, key, delta, 0)
}

// IncrByFloatWithTTL attaches the expiry only to a key this call creates.
//
// The script reads the key before incrementing, rather than reading the ttl
// afterwards: an existing key with no expiry and a fresh key both report no
// ttl, so the ttl alone cannot tell the two apart, and extending an existing
// key is exactly what the capability forbids.
func (s *store) IncrByFloatWithTTL(ctx context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	return s.increment(ctx, key, delta, ttl)
}

// increment is both increment operations, run as one script. A ttl of zero
// attaches nothing, which is the plain increment.
//
// It is a script rather than the bare INCRBYFLOAT because the server adds at a
// wider precision than float64: a sum past float64's range is a value it
// stores happily and the client then cannot parse, so the caller would be told
// an increment failed that had in fact been written — and would retry it. The
// script checks the sum it stored and puts the previous value back when no
// float64 holds it, all inside one atomic step.
func (s *store) increment(ctx context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	if err := counter.CheckDelta(ModuleName, key, delta); err != nil {
		return 0, err
	}
	got, err := incrWithTTL.Run(ctx, s.client, []string{prefixed(key)}, delta, ttlMillis(ttl)).Float64()
	if err != nil && strings.Contains(err.Error(), outOfRangeReply) {
		return 0, counter.OutOfRange(ModuleName, key)
	}
	return got, asNotNumeric(err)
}

// outOfRangeReply is the error the increment script answers when the sum is
// past float64's range.
const outOfRangeReply = "KVRANGE"

// CompareAndSwap compares and writes in one script, so no other command can
// land between the comparison and the write.
//
// An absent key reads as the empty string, which makes a nil or empty old a
// match and turns this into set-if-absent. The write carries KEEPTTL, so a
// successful swap leaves the expiry where it was; a key that did not exist is
// created without one.
func (s *store) CompareAndSwap(ctx context.Context, key string, old, newVal []byte) (bool, error) {
	swapped, err := compareAndSwap.Run(ctx, s.client, []string{prefixed(key)}, old, newVal).Int64()
	if err != nil {
		return false, err
	}
	return swapped == 1, nil
}

// ttlMillis turns a duration into the whole milliseconds the script's PEXPIRE
// takes.
//
// Rounding up rather than truncating is what keeps a positive ttl positive: a
// ttl under a millisecond truncates to zero, and the script's own "greater than
// zero" guard then attaches no expiry at all — so a caller asking for half a
// millisecond gets a key that never ages out, the opposite of what it asked
// for. A ttl of zero or less stays zero, which the guard reads as "no expiry".
func ttlMillis(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 0
	}
	ms := ttl.Milliseconds()
	if ms == 0 {
		return 1
	}
	return ms
}

// asNotNumeric maps the server's complaint about a non-numeric value onto this
// capability's sentinel.
//
// It matches on the server's own wording, which is the only signal Redis gives:
// INCRBYFLOAT answers a generic error for this and nothing distinguishes it
// structurally. The match is narrow on purpose — a value that is present but
// not a number is what the sentinel means, and anything else stays as it came,
// so a caller still sees a connection failure as a connection failure.
func asNotNumeric(err error) error {
	if err != nil && strings.Contains(err.Error(), "not a valid float") {
		return fmt.Errorf("%w: %s", kv.ErrNotNumeric, err.Error())
	}
	return err
}

var (
	// incrWithTTL increments, refuses a sum no float64 holds by putting the
	// previous value back, and attaches the expiry in the same script when the
	// key was not there. Lua reads a number past float64's range as an
	// infinity, which is how the sum is judged.
	incrWithTTL = goredis.NewScript(`
local previous = redis.call('GET', KEYS[1])
local value = redis.call('INCRBYFLOAT', KEYS[1], ARGV[1])
local sum = tonumber(value)
if sum == nil or sum ~= sum or sum == math.huge or sum == -math.huge then
  if previous then
    redis.call('SET', KEYS[1], previous, 'KEEPTTL')
  else
    redis.call('DEL', KEYS[1])
  end
  return redis.error_reply('KVRANGE the sum is past what a float64 holds')
end
if not previous and tonumber(ARGV[2]) > 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return value
`)

	// compareAndSwap writes only on a match, and keeps whatever expiry the key
	// already had.
	compareAndSwap = goredis.NewScript(`
local current = redis.call('GET', KEYS[1])
if not current then current = '' end
if current ~= ARGV[1] then
  return 0
end
redis.call('SET', KEYS[1], ARGV[2], 'KEEPTTL')
return 1
`)
)
