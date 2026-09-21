// Package redis is the Redis implementation of the key-value capability pkg/kv
// declares.
//
// It binds the Redis client, names the configuration namespace this backend's
// input items hang under, and delivers the capability exclusively. The client
// itself comes from pkg/redis, which the event module's Redis backend uses too,
// so the two share one notion of what connecting means.
//
// Every operation is one command or one script. Two of them cannot be a plain
// command: attaching an expiry only to a key an increment creates, and swapping
// a value only when it matches. Redis has no verb for either, and doing them as
// two commands would leave the gap this module exists to close — an increment
// landing between the two would be lost, and a swap would not be one. Both run
// as scripts, which Redis executes atomically.
package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/redis"
)

const (
	// ModuleName is this implementation's name in the registry.
	ModuleName = "kv.redis"
	// ConfigNamespace is where this implementation's input items are mounted.
	ConfigNamespace = ModuleName
)

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

// stance states whether this implementation runs.
//
// An absent address disables it with a reason rather than failing the startup:
// whether an assembly needs this backend is the assembly's decision, and a host
// that does need one finds out through whatever required the capability, with
// this reason attached. A configured address states StateEnabled rather than
// StateAuto, so that a second configured backend collides here at startup
// instead of one of them winning silently.
func stance(cfg *redis.Config) core.Enablement {
	if strings.TrimSpace(cfg.Addr) == "" {
		return core.Enablement{
			State: core.StateDisabled,
			Reason: fmt.Sprintf("%s.addr gives no server, so this assembly runs without a Redis "+
				"key-value store; set it to run on Redis", ConfigNamespace),
		}
	}
	return core.Enablement{State: core.StateEnabled}
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
	value, err := s.client.Get(ctx, key).Bytes()
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
	return s.client.Set(ctx, key, value, plainTTL(ttl)).Err()
}

// plainTTL turns the capability's "zero or less means no expiry" into the
// duration the client library reads that way.
//
// The library does not agree with the capability on what a negative duration
// means: it carries a KeepTTL constant of -1, and a SET given it leaves the
// key's existing expiry alone instead of clearing it. Passing the caller's
// duration straight through therefore makes `Set(k, v, -1)` — a caller saying
// "no expiry, from now on" — keep the expiry the key already had.
func plainTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return 0
	}
	return ttl
}

// Delete removes the key. Redis reports how many keys it removed, and removing
// an absent one is not an error.
func (s *store) Delete(ctx context.Context, key string) error {
	return s.client.Del(ctx, key).Err()
}

// IncrByFloat uses the server's own counter. Redis leaves an existing key's
// expiry alone when it increments, which is the semantics the capability
// requires and the reason this is a plain command rather than a script.
func (s *store) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	got, err := s.client.IncrByFloat(ctx, key, delta).Result()
	return got, asNotNumeric(err)
}

// IncrByFloatWithTTL attaches the expiry only to a key this call creates.
//
// The script asks whether the key exists before incrementing, rather than
// reading the ttl afterwards: an existing key with no expiry and a fresh key
// both report no ttl, so the ttl alone cannot tell the two apart, and extending
// an existing key is exactly what the capability forbids.
func (s *store) IncrByFloatWithTTL(ctx context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	got, err := incrWithTTL.Run(ctx, s.client, []string{key}, delta, ttlMillis(ttl)).Float64()
	return got, asNotNumeric(err)
}

// CompareAndSwap compares and writes in one script, so no other command can
// land between the comparison and the write.
//
// An absent key reads as the empty string, which makes a nil or empty old a
// match and turns this into set-if-absent. The write carries KEEPTTL, so a
// successful swap leaves the expiry where it was; a key that did not exist is
// created without one.
func (s *store) CompareAndSwap(ctx context.Context, key string, old, newVal []byte) (bool, error) {
	swapped, err := compareAndSwap.Run(ctx, s.client, []string{key}, old, newVal).Int64()
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
	// incrWithTTL increments, and attaches the expiry in the same script when
	// the key was not there.
	incrWithTTL = goredis.NewScript(`
local existed = redis.call('EXISTS', KEYS[1])
local value = redis.call('INCRBYFLOAT', KEYS[1], ARGV[1])
if existed == 0 and tonumber(ARGV[2]) > 0 then
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
