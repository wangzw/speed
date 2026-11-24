// Package redis is the Redis implementation of the key-value capability pkg/kv declares.
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
	keyPrefix       = "speed:kv:"
)

func prefixed(key string) string { return keyPrefix + key }

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not inherit the
// process-level registrations.
func Module() core.Module {
	proto := redis.Defaults()
	return kv.NewModule(kv.Spec[redis.Config]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &proto,
		Items:           redis.Items(),
		Stance:          stance,
		Open:            open,
		Secrets:         func(cfg *redis.Config) []string { return []string{cfg.Password} },
	})
}

func stance(cfg *redis.Config) (core.Enablement, error) {
	if strings.TrimSpace(cfg.Addr) == "" {
		return core.Enablement{}, fmt.Errorf("%s.%s gives no server, and this implementation reaches "+
			"Redis at one: with none set there is nothing to start against. Set it to the server to run "+
			"on, or leave this implementation out of the command", ConfigNamespace, redis.AddrKey)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

func open(ctx context.Context, cfg *redis.Config) (kv.KV, error) {
	client, err := redis.Connect(ctx, *cfg)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ModuleName, err)
	}
	return &store{client: client}, nil
}

type store struct {
	client *goredis.Client
}

func (s *store) Close(_ context.Context) error {
	return s.client.Close()
}

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

func (s *store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return s.client.Set(ctx, prefixed(key), value, plainTTL(ttl)).Err()
}

func plainTTL(ttl time.Duration) time.Duration {
	return time.Duration(ttlMillis(ttl)) * time.Millisecond
}

func (s *store) Delete(ctx context.Context, key string) error {
	return s.client.Del(ctx, prefixed(key)).Err()
}

func (s *store) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	return s.increment(ctx, key, delta, 0)
}

func (s *store) IncrByFloatWithTTL(ctx context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	return s.increment(ctx, key, delta, ttl)
}

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

const outOfRangeReply = "KVRANGE"

func (s *store) CompareAndSwap(ctx context.Context, key string, old, newVal []byte) (bool, error) {
	swapped, err := compareAndSwap.Run(ctx, s.client, []string{prefixed(key)}, old, newVal).Int64()
	if err != nil {
		return false, err
	}
	return swapped == 1, nil
}

func (s *store) SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	return s.client.SetNX(ctx, prefixed(key), value, plainTTL(ttl)).Result()
}

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

func asNotNumeric(err error) error {
	if err != nil && strings.Contains(err.Error(), "not a valid float") {
		return fmt.Errorf("%w: %s", kv.ErrNotNumeric, err.Error())
	}
	return err
}

var (
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
