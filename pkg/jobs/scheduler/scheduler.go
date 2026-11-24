// Package scheduler is the periodic-trigger module, registered as
// jobs.scheduler. On every window start it enqueues one job per window per
// tenant from the periodic declarations business modules place in their resources.
package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's name in the registry.
const ModuleName = "jobs.scheduler"

// ConfigNamespace is where this module's input items are mounted, which is the
// module name.
const ConfigNamespace = ModuleName

var (
	// ErrInvalidSchedule reports a periodic declaration whose shape is not valid:
	// an empty Key, an empty Type, or a non-positive Interval.
	ErrInvalidSchedule = errors.New("jobs.scheduler: a schedule declaration is not a valid shape")
	// ErrDuplicateSchedule reports two declarations in force that share a Key.
	ErrDuplicateSchedule = errors.New("jobs.scheduler: two schedule declarations share a key")
	// ErrMissingHandler reports a declaration whose Type has no registered
	// handler.
	ErrMissingHandler = errors.New("jobs.scheduler: a schedule's type has no registered handler")
)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It delivers no capability; the
// queue, the registration face and the tenant roster are required, and the
// logger is optional.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*jobs.Queue)(nil)},
			{Token: (*jobs.Handlers)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{schema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if _, err := readSettings(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newScheduler(reg)
		},

		Start: func(ctx context.Context, _ *core.Registry, instance any) error {
			s, ok := productOf(instance)
			if !ok {
				return nil
			}
			if err := s.checkHandlers(); err != nil {
				return err
			}
			s.start(ctx)
			return nil
		},

		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			if s, ok := productOf(instance); ok {
				s.stop()
			}
			return nil
		},

		Close: func(_ context.Context, _ *core.Registry, _ any) error { return nil },
	}
}

type scheduler struct {
	queue     jobs.Queue
	handlers  jobs.Handlers
	catalog   tenant.Catalog
	logger    *slog.Logger
	tick      time.Duration
	schedules []jobs.Schedule

	now func() time.Time

	cancel context.CancelFunc
	done   chan struct{}
}

func productOf(instance any) (*scheduler, bool) {
	s, ok := instance.(*scheduler)
	return s, ok && s != nil
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	return reader, nil
}

func newScheduler(reg *core.Registry) (*scheduler, error) {
	reader, err := readerOf(reg)
	if err != nil {
		return nil, err
	}
	settings, err := readSettings(reader)
	if err != nil {
		return nil, err
	}
	queue, err := core.Resolve[jobs.Queue](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the submission face: %w", ModuleName, err)
	}
	handlers, err := core.Resolve[jobs.Handlers](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the registration face: %w", ModuleName, err)
	}
	catalog, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the tenant roster: %w", ModuleName, err)
	}
	schedules, err := collectSchedules(reg)
	if err != nil {
		return nil, err
	}
	s := &scheduler{
		queue:     queue,
		handlers:  handlers,
		catalog:   catalog,
		logger:    log.Default(),
		tick:      settings.tick,
		schedules: schedules,
		now:       time.Now,
	}
	s.takeLogger(reg)
	return s, nil
}

func (s *scheduler) takeLogger(reg *core.Registry) {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		return
	}
	s.logger = delivered.Named(ModuleName)
}

func collectSchedules(reg *core.Registry) ([]jobs.Schedule, error) {
	declared := core.Resources[jobs.Schedule](reg)
	out := make([]jobs.Schedule, 0, len(declared))
	seen := make(map[string]string, len(declared))
	for _, res := range declared {
		if !inForce(reg, res.Module) {
			continue
		}
		s := res.Value
		if err := validate(s, res.Module); err != nil {
			return nil, err
		}
		if other, ok := seen[s.Key]; ok {
			return nil, fmt.Errorf("%w: %q is declared by both %s and %s; within one window and "+
				"one tenant the two would derive the same idempotency key", ErrDuplicateSchedule,
				s.Key, other, res.Module)
		}
		seen[s.Key] = res.Module
		out = append(out, s)
	}
	return out, nil
}

func inForce(reg *core.Registry, module string) bool {
	if reg.AllResourcesInForce(module) {
		return true
	}
	if !reg.Participating(module) {
		return false
	}
	if e, ok := reg.Enablement(module); ok && e.State == core.StateDisabled {
		return false
	}
	return true
}

func validate(s jobs.Schedule, module string) error {
	switch {
	case s.Key == "":
		return fmt.Errorf("%w: %s declares a schedule with an empty key", ErrInvalidSchedule, module)
	case s.Type == "":
		return fmt.Errorf("%w: %s declares the schedule %q with an empty type", ErrInvalidSchedule, module, s.Key)
	case s.Interval <= 0:
		return fmt.Errorf("%w: %s declares the schedule %q with a non-positive interval (%s)",
			ErrInvalidSchedule, module, s.Key, s.Interval)
	}
	return nil
}

func (s *scheduler) checkHandlers() error {
	registered := make(map[string]struct{})
	for _, t := range s.handlers.Registered() {
		registered[t] = struct{}{}
	}
	for _, sched := range s.schedules {
		if _, ok := registered[sched.Type]; !ok {
			return fmt.Errorf("%w: the schedule %q wants a job of type %q, and no handler is "+
				"registered for it", ErrMissingHandler, sched.Key, sched.Type)
		}
	}
	return nil
}

// start begins the ticker, unless the configured tick is at or below zero. The
// ticker carries this stage's context values but not its cancellation, so it
// runs until this module's own stop.
func (s *scheduler) start(ctx context.Context) {
	if s.tick <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	s.cancel, s.done = cancel, done
	go func() {
		defer close(done)
		// The first tick covers the window the process started in as well: that
		// window's start can lie up to one interval before now, beyond the
		// backscan a later tick reaches.
		s.tickOnce(ctx, true)
		ticker := time.NewTicker(s.tick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.tickOnce(ctx, false)
			}
		}
	}()
}

// stop cancels the ticker and waits for its goroutine, so no enqueue is in
// flight once Stop returns.
func (s *scheduler) stop() {
	if !s.running() {
		return
	}
	s.cancel()
	<-s.done
	s.cancel, s.done = nil, nil
}

func (s *scheduler) running() bool { return s.cancel != nil }

func (s *scheduler) tickOnce(ctx context.Context, startup bool) {
	if len(s.schedules) == 0 {
		return
	}
	tenants, err := s.catalog.List(ctx)
	if err != nil {
		s.logger.Warn("jobs.scheduler: listing the tenant roster failed; no window is expanded this tick",
			"error", err)
		return
	}
	now := s.now()
	for _, sched := range s.schedules {
		s.enqueueSchedule(ctx, now, tenants, sched, startup)
	}
}

// enqueueSchedule enqueues one job per (window, tenant) pair for the windows
// whose start falls in (now - tick, now]; the first tick also covers the window
// now falls in. Every replica derives the same windows and keys from the instant,
// the tick and the interval alone, so the queue's key uniqueness converges their
// enqueues to one job.
func (s *scheduler) enqueueSchedule(ctx context.Context, now time.Time, tenants []tenant.Tenant, sched jobs.Schedule, startup bool) {
	start := windowStart(now, sched.Interval)
	lower := now.Add(-s.tick)
	if startup && !start.After(lower) {
		lower = start.Add(-time.Nanosecond)
	}
	for w := start; w.After(lower); w = w.Add(-sched.Interval) {
		key := windowKey(sched.Key, w)
		for _, t := range tenants {
			s.enqueueOne(ctx, t, sched, w, key)
		}
	}
}

func (s *scheduler) enqueueOne(ctx context.Context, t tenant.Tenant, sched jobs.Schedule, window time.Time, key string) {
	tctx, err := s.catalog.Enter(ctx, t.ID)
	if err != nil {
		s.logger.Warn("jobs.scheduler: entering a tenant failed; that tenant's window is skipped",
			"tenant", string(t.ID), "schedule", sched.Key, "window", window, "error", err)
		return
	}
	if _, err := s.queue.Enqueue(tctx, jobs.Task{
		Type:           sched.Type,
		Payload:        sched.Payload,
		IdempotencyKey: key,
	}); err != nil {
		s.logger.Warn("jobs.scheduler: enqueuing a periodic job failed; the next tick retries the same window",
			"schedule", sched.Key, "type", sched.Type, "window", window, "error", err)
	}
}

// windowStart is the start of the window now falls in: the largest multiple of
// the interval from the Unix epoch that is not after now, so every replica
// derives the same start from the same instant.
func windowStart(now time.Time, interval time.Duration) time.Time {
	unit := interval.Nanoseconds()
	aligned := now.UnixNano() - floorMod(now.UnixNano(), unit)
	return time.Unix(0, aligned).UTC()
}

// windowKey is the idempotency key of one declaration's window: the SHA-256 of
// the declaration key and the window start. It carries no tenant, so each
// tenant's job in one window becomes its own row under the queue's
// (tenant_id, idempotency_key) uniqueness.
func windowKey(scheduleKey string, window time.Time) string {
	sum := sha256.Sum256([]byte(scheduleKey + strconv.FormatInt(window.UnixNano(), 10)))
	return hex.EncodeToString(sum[:])
}

// floorMod returns n modulo unit in [0, unit); Go's % truncates toward zero, so a
// negative n would otherwise round the alignment the wrong way.
func floorMod(n, unit int64) int64 {
	if unit == 0 {
		return 0
	}
	m := n % unit
	if m < 0 {
		m += unit
	}
	return m
}
