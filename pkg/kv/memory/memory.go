// Package memory is the in-process implementation of the key-value capability
// pkg/kv declares.
//
// It holds every value in this process's memory, so its state is neither shared
// with other replicas nor kept across a restart. Those are properties of the
// backend, not defects, and the way they surface is this implementation's own
// deployment precondition: a host that declares several replicas gets a startup
// that stops with an error naming the replica count, rather than a store each
// replica writes to alone.
//
// It is the capability's default implementation in the sense core's resolution
// gives that word. It claims the capability exclusively while stating
// StateAuto, so it runs until another provider is enabled and then stands down.
// The stand-down takes a host that names that provider in the command tree or
// supplies an instance for it: an exclusive provider is not pulled in by the
// dependency closure, so one that is neither named nor supplied enters no
// participant set and this implementation never yields — the assembly then runs
// on this one while the host believes it is running the other, and nothing
// reports the difference. It is also the test double every consistency run is
// written against, so it needs no third-party dependency and no container.
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
	// replicasKey is the input item stating how many replicas share this
	// assembly's state.
	replicasKey = "replicas"
)

// Config is what this implementation accepts: the deployment fact its
// precondition is judged against.
type Config struct {
	// Replicas is how many replicas share this assembly's state. It states a
	// fact about the deployment rather than a preference of this
	// implementation: the default of one is what makes an undeclared assembly
	// single-replica, and a host running several replicas that leaves it unset
	// has told this module nothing.
	Replicas int `config:"replicas"`
}

// init registers this module with the process registry, so a host that imports
// the package has it available.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
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

// stance states this implementation's stance, or reports the deployment it
// cannot serve.
//
// A declared replica count above one is a deployment this backend cannot serve:
// each replica would hold its own values and never see the others' writes, and
// every call would still succeed, so the failure would have no signal anywhere.
// Returning it as an error stops the startup and names the input it stopped on,
// which is the only place this can be noticed at all.
//
// Otherwise the stance is StateAuto rather than StateEnabled. This is the
// capability's zero-configuration implementation, and StateAuto is what lets a
// host import it alongside a real backend and have it stand down once that
// backend is enabled. The host naming that backend in the command tree, or
// supplying an instance for it, is what makes the stand-down possible at all: an
// exclusive provider the command tree leaves out, and the host supplied nothing
// for, enters no participant set, so there is nothing for this one to yield to.
func stance(cfg *Config) (core.Enablement, error) {
	if cfg.Replicas > 1 {
		return core.Enablement{}, fmt.Errorf("%s.%s is %d, and this implementation keeps every value "+
			"in one process's memory: each replica would hold its own and never see the others' writes, "+
			"while every call still succeeded. Point the command at a backend that shares state across "+
			"replicas, or declare one replica", ConfigNamespace, replicasKey, cfg.Replicas)
	}
	return core.Enablement{State: core.StateAuto}, nil
}

// open builds the store. Nothing is held open, so the product implements no
// closer and pkg/kv releases nothing for it.
func open(_ context.Context, _ *Config) (kv.KV, error) {
	return &store{entries: make(map[string]entry)}, nil
}

// entry is one stored value and when it stops being visible.
type entry struct {
	value   []byte
	expires time.Time // the zero time means no expiry
}

// store is the whole of this implementation's state.
//
// One mutex covers the map and every operation on it, which is what makes the
// atomic operations atomic: each runs to completion between the lock and the
// unlock, so no caller-side sequence can interleave with one. That is all a
// single process's memory can offer, and it is why the deployment precondition
// exists — across replicas this lock reaches nothing.
type store struct {
	mu      sync.Mutex
	entries map[string]entry
}

// Get returns a copy of the stored value. The copy matters: handing out the
// slice held in the map would let a caller write into this store's state
// without going through any of its methods.
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

// Set stores a copy of the value, for the same reason Get hands one out.
func (s *store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.put(key, entry{value: bytes.Clone(value), expires: expiry(ttl)})
	return nil
}

// Delete removes the key, whether or not it was there.
func (s *store) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
	return nil
}

// IncrByFloat adds to the number under key, leaving the key's expiry alone.
func (s *store) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	return s.increment(ctx, key, delta, 0, false)
}

// IncrByFloatWithTTL adds to the number under key and, when the key did not
// exist, gives it the expiry in the same step.
//
// The whole operation is one critical section, which is the property a pair of
// separate calls cannot have: an increment landing between a separate
// increment and a separate expiry attachment would be overwritten.
func (s *store) IncrByFloatWithTTL(ctx context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	return s.increment(ctx, key, delta, ttl, true)
}

// increment is both increment operations. withTTL says whether ttl applies to a
// key this call creates; a key that already exists keeps its expiry either way.
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

// CompareAndSwap writes only when the current value equals old, and never
// touches the expiry.
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
	// A missing or expired key reads as empty, so this doubles as set-if-absent.
	if !bytes.Equal(current, old) {
		return false, nil
	}
	s.put(key, entry{value: bytes.Clone(newVal), expires: expires})
	return true, nil
}

// SetIfAbsent stores a copy of the value with its expiry when the key is
// missing or expired, and leaves a live key alone.
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

// load returns the entry for key, treating one whose expiry has passed as
// absent and dropping it on the way out. It is the only reader of the map, so
// every operation shares one notion of what is still there.
//
// The caller holds the mutex.
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

// put stores an entry and gives the sweep a turn, so the sweep's pace follows
// the write rate without any writer having to remember it.
//
// The caller holds the mutex.
func (s *store) put(key string, e entry) {
	s.entries[key] = e
	// Only a write that carries an expiry can add to the population of entries
	// waiting to age out, and a deployment that sets no ttl anywhere — a lock
	// table, a long-lived session store — would pay the sweep on every write
	// for nothing.
	if !e.expires.IsZero() {
		s.sweep()
	}
}

// sweepSample is how many entries one sweep looks at.
//
// Go's map iteration starts at a random position, so a sweep that stops early
// is a random sample: successive writes look at different regions instead of
// paying a full pass each time. The sample looks at far more entries than one
// write adds, so it outpaces the writes, and a long-running process that gives
// every key its own expiry does not accumulate one per key it has ever seen.
const sweepSample = 32

// sweep drops up to sweepSample expired entries.
//
// Expiry is otherwise lazy — an entry past its time is dropped by load when
// someone asks for it — and a key nothing ever asks for again would sit in the
// map forever. This is the active half of that pair: the write path is the
// clock, so the population of expired-but-unvisited entries stays bounded
// rather than growing with the keys the process has written.
//
// The caller holds the mutex.
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

// expiry turns a ttl into the instant a key stops being visible. Zero or less
// means no expiry, matching Set's convention.
func expiry(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}
