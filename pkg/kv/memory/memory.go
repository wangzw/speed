// Package memory is the in-process implementation of the key-value capability pkg/kv declares.
package memory

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/kv/internal/counter"
)

const (
	// ModuleName is this implementation's name in the registry.
	ModuleName = "kv.memory"
	// ConfigNamespace is where this implementation's input items are mounted.
	ConfigNamespace = ModuleName
	replicasKey     = "replicas"
)

// Config is what this implementation accepts: the deployment fact its precondition is judged
// against.
type Config struct {
	// Replicas is how many replicas share this assembly's state.
	Replicas int `config:"replicas"`
}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not inherit the
// process-level registrations.
func Module() core.Module {
	return kv.NewModule(kv.Spec[Config]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &Config{Replicas: 1},
		Items: map[string]config.Item{
			replicasKey: {
				Description: "how many replicas share this assembly's state; this implementation " +
					"keeps every value in one process's memory, so any value above 1 fails the startup",
			},
		},
		Stance: stance,
		Open:   open,
	})
}

func stance(cfg *Config) (core.Enablement, error) {
	if cfg.Replicas > 1 {
		return core.Enablement{}, fmt.Errorf("%s.%s is %d, and this implementation keeps every value "+
			"in one process's memory: each replica would hold its own and never see the others' writes, "+
			"while every call still succeeded. Point the command at a backend that shares state across "+
			"replicas, or declare one replica", ConfigNamespace, replicasKey, cfg.Replicas)
	}
	return core.Enablement{State: core.StateAuto}, nil
}

func open(_ context.Context, _ *Config) (kv.KV, error) {
	return &store{entries: make(map[string]entry)}, nil
}

type entry struct {
	value   []byte
	expires time.Time
}

type store struct {
	mu      sync.Mutex
	entries map[string]entry
}

func (s *store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.load(key)
	if !ok {
		return nil, false, nil
	}
	return bytes.Clone(e.value), true, nil
}

func (s *store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.put(key, entry{value: bytes.Clone(value), expires: expiry(ttl)})
	return nil
}

func (s *store) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
	return nil
}

func (s *store) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	return s.increment(ctx, key, delta, 0, false)
}

func (s *store) IncrByFloatWithTTL(ctx context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	return s.increment(ctx, key, delta, ttl, true)
}

func (s *store) increment(ctx context.Context, key string, delta float64, ttl time.Duration, withTTL bool) (float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := counter.CheckDelta(ModuleName, key, delta); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	e, live := s.load(key)
	var current float64
	if live {
		parsed, err := strconv.ParseFloat(string(e.value), 64)
		if err != nil {
			return 0, fmt.Errorf("%w (key %q)", kv.ErrNotNumeric, key)
		}
		current = parsed
	}
	next := current + delta
	if err := counter.CheckResult(ModuleName, key, next); err != nil {
		return 0, err
	}
	encoded := []byte(strconv.FormatFloat(next, 'g', -1, 64))

	if live {
		s.put(key, entry{value: encoded, expires: e.expires})
		return next, nil
	}
	var expires time.Time
	if withTTL {
		expires = expiry(ttl)
	}
	s.put(key, entry{value: encoded, expires: expires})
	return next, nil
}

func (s *store) CompareAndSwap(ctx context.Context, key string, old, newVal []byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	e, live := s.load(key)
	var current []byte
	var expires time.Time
	if live {
		current, expires = e.value, e.expires
	}
	if !bytes.Equal(current, old) {
		return false, nil
	}
	s.put(key, entry{value: bytes.Clone(newVal), expires: expires})
	return true, nil
}

func (s *store) SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, live := s.load(key); live {
		return false, nil
	}
	s.put(key, entry{value: bytes.Clone(value), expires: expiry(ttl)})
	return true, nil
}

func (s *store) load(key string) (entry, bool) {
	e, ok := s.entries[key]
	if !ok {
		return entry{}, false
	}
	if !e.expires.IsZero() && !time.Now().Before(e.expires) {
		delete(s.entries, key)
		return entry{}, false
	}
	return e, true
}

func (s *store) put(key string, e entry) {
	s.entries[key] = e
	if !e.expires.IsZero() {
		s.sweep()
	}
}

const sweepSample = 32

func (s *store) sweep() {
	now := time.Now()
	seen := 0
	for key, e := range s.entries {
		if seen >= sweepSample {
			break
		}
		seen++
		if !e.expires.IsZero() && !now.Before(e.expires) {
			delete(s.entries, key)
		}
	}
}

func expiry(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}
