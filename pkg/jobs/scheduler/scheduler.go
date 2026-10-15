// Package scheduler is the periodic-trigger module, registered as
// jobs.scheduler. It reads the periodic declarations business modules place in
// their own resources and, on every window start, enqueues one job per window
// per tenant, so that one run of the platform enqueues exactly one job per
// window however many replicas are running.
//
// It only ticks and enqueues; it does not execute jobs. Claiming, executing,
// the lease, backoff and dead-lettering belong to jobs.db. This module knows no
// job type: a Schedule's Type is a string another module defines, and it is
// handed to Queue.Enqueue unchanged.
//
// It has no table of its own. The window idempotency key lands on the job row's
// idempotency_key, and "one enqueue per window" is given by the queue's
// (tenant_id, idempotency_key) uniqueness: a repeated enqueue of one window
// returns the existing job's identifier rather than writing a second row. No
// leader election and no distributed lock is involved — every replica runs its
// own ticker and the window key converges their enqueues.
//
// It does not run per-replica loops. A loop that each replica runs for itself —
// refreshing an in-process snapshot, sweeping expired rows, rotating a file,
// polling an external state — belongs to the module that needs it, as its own
// ticker. This module carries only the triggers whose effect must converge
// across replicas.
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

// ModuleName is this module's name in the registry. A host writes it into the
// command that drives the periodic triggers.
const ModuleName = "jobs.scheduler"

// ConfigNamespace is where this module's input items are mounted, which is the
// module name.
const ConfigNamespace = ModuleName

// The sentinels a caller decides on with errors.Is.
var (
	// ErrInvalidSchedule reports a periodic declaration whose shape is not
	// valid: an empty Key, an empty Type, or a non-positive Interval. New
	// refuses to build the module and names the declaration.
	ErrInvalidSchedule = errors.New("jobs.scheduler: a schedule declaration is not a valid shape")
	// ErrDuplicateSchedule reports two declarations in force that share a Key.
	// Within one window and one tenant they would derive the same idempotency
	// key, so one job would swallow the other. New refuses to build the module
	// and names both.
	ErrDuplicateSchedule = errors.New("jobs.scheduler: two schedule declarations share a key")
	// ErrMissingHandler reports a declaration whose Type has no registered
	// handler. Start refuses to start and names the declaration.
	ErrMissingHandler = errors.New("jobs.scheduler: a schedule's type has no registered handler")
)

// init registers this module with the process registry. Importing the package
// is what puts the periodic trigger in a binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// It delivers no capability: it is a member of the assembly that drives a
// ticker, taken up only by being named in a command, and it appears in no
// core.Resolve[T] call site. The queue and the registration face are required
// and hard — this module enqueues through the one and validates its declaration
// types through the other — and the tenant roster is required too, because
// every declaration is expanded per the tenants the roster holds. The logging
// capability is optional: this module's records are its own diagnostics, and a
// host with no logging still gets a working trigger whose failures reach the
// process default logger. The configuration reader is not declared here; the
// host supplies it before the drive begins, the way every module that reads its
// own section takes it up.
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

		// Prepare decodes this module's own configuration. A tick at or below
		// zero is a host turning the trigger off, which is a legal
		// configuration rather than a failure.
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

		// New takes up what the descriptor requires and freezes the set of
		// declarations this run will tick. The declarations are collected, and
		// filtered by the participating set, once: they are fixed at
		// registration time and do not change for the life of the process.
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newScheduler(reg)
		},

		// Start checks that every declaration's type has a registered handler,
		// then starts the ticker. It fails the startup when a type is missing,
		// rather than letting a window go by with no handler to run it.
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

		// Nothing to release: this module holds no table, no handle and no
		// other resource. The ticker is stopped by Stop.
		Close: func(_ context.Context, _ *core.Registry, _ any) error { return nil },
	}
}

// scheduler is the module's product. Its declaration set and its dependencies
// are read-only once New has built it, so the tick goroutine shares no mutable
// state between ticks.
type scheduler struct {
	queue     jobs.Queue
	handlers  jobs.Handlers
	catalog   tenant.Catalog
	logger    *slog.Logger
	tick      time.Duration
	schedules []jobs.Schedule

	// now reads the current time. It is time.Now in production and is replaced
	// in a case that has to pin a window.
	now func() time.Time

	// cancel and done hold the running ticker's stop mechanism. They are nil
	// while the ticker is off: before Start, after Stop, and whenever the
	// configured tick is at or below zero.
	cancel context.CancelFunc
	done   chan struct{}
}

// productOf narrows an instance to this module's product. An instance of
// another shape is one the host supplied for this module, such as a stand-in
// built for a case, and the callbacks leave it alone.
func productOf(instance any) (*scheduler, bool) {
	s, ok := instance.(*scheduler)
	return s, ok && s != nil
}

// readerOf takes up the configuration reader.
//
// A reader that is not there means the host never supplied one as the instance
// of the config module, which is the host's omission rather than something to
// read as "nothing was configured".
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	return reader, nil
}

// newScheduler takes up what the descriptor requires, collects the
// declarations this run will tick, and assembles the product.
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

// takeLogger picks up the logging capability if the assembly has one.
//
// It is optional on purpose: this module's records are its own diagnostics — an
// enqueue that failed, a roster read that failed — and a host that never
// assembled logging still gets a working trigger whose records reach the
// process default logger.
func (s *scheduler) takeLogger(reg *core.Registry) {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		return
	}
	s.logger = delivered.Named(ModuleName)
}

// collectSchedules gathers every schedule declaration in reach and freezes it
// into the set this run ticks, refusing the declarations that cannot be
// honoured.
//
// The set is filtered by the participating set once. core.Resources walks every
// registered module and filters nothing — core interprets no resource and so
// cannot know which set this one is drawn from — so the filter is this module's
// own. A declaration describes work to do, and its effect depends on the
// declaring module being driven and its handler being registered: a module that
// was only named statically is not built, has no handler, and its declaration
// must not fire. A module the host put every declaration in force for is
// collected as though it were running.
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

// inForce reports whether a module's declarations are within reach of this
// consumption point, whose domain is the participating set.
//
// The host's override comes first: a module it took in with every declaration
// in force is collected whether or not it runs. Otherwise the module has to be
// participating, and a participant resolution stood down over an exclusive
// claim is the assembly's own denial of it, so its declarations drop out.
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

// validate refuses a declaration whose shape cannot be honoured, naming the
// module that declared it.
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

// checkHandlers reports the first enabled declaration whose Type has no
// registered handler. The handler set is fixed before the queue starts, and a
// declaration whose type nobody runs would go by silently every window, so the
// startup fails with the declaration named.
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

// start begins the ticker, unless the configured tick is at or below zero.
//
// The ticker carries the values of this stage's context but not its
// cancellation: it runs until this module's own stop. A tick at or below zero
// turns the ticker off — no window is ever enqueued, and the process does not
// report the periodic declarations as failing, it simply does not drive them.
func (s *scheduler) start(ctx context.Context) {
	if s.tick <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	s.cancel, s.done = cancel, done
	go func() {
		defer close(done)
		// The first tick runs at once rather than after one interval, and it
		// covers the window the process started in as well. That window's start
		// can lie up to one interval before now, further back than the backscan
		// reaches, so a later tick could only pick it up once the next window
		// begins.
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

// stop cancels the ticker and waits for its goroutine to return.
//
// The wait is what keeps the trigger from outliving Stop: the queue this module
// enqueues through stops after it does, so a tick left running would enqueue
// after this module had stopped, into a queue that is about to go down.
// Waiting keeps no enqueue in flight once Stop has returned, and no window past
// it. The wait is as long as that tick takes to finish.
func (s *scheduler) stop() {
	if !s.running() {
		return
	}
	s.cancel()
	<-s.done
	s.cancel, s.done = nil, nil
}

// running reports whether the ticker is up. It is false before Start, after
// Stop, and whenever the configured tick is at or below zero.
func (s *scheduler) running() bool { return s.cancel != nil }

// tickOnce runs one tick: for every frozen declaration, enqueue one job for
// every window whose start has fallen since the previous tick, for every tenant
// in the roster.
//
// startup marks the tick Start runs, which covers the window now falls in on
// top of that backscan. Every later tick reaches back one tick alone.
//
// It reads the roster once. A roster read that fails leaves this round
// unexpanded and is retried on the next tick; the failure is one replica's to
// retry, not a condition to record against any declaration.
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

// enqueueSchedule enqueues one job per (window, tenant) pair for one
// declaration.
//
// The windows are those whose start falls in (now - tick, now]: the backscan
// reaches back exactly one tick, so the set is derived from the current instant,
// the tick and the interval alone, and every replica derives the same set
// without reading any record of what was already scheduled. A tick denser than
// the interval re-derives the same window start and the same key, which the
// queue converges to one job; a tick sparser than the interval crosses several
// windows and enqueues each of them.
//
// The tick Start runs covers the window now falls in as well, whatever the
// interval: that window's start can lie up to one interval before now, and an
// interval longer than the tick would otherwise leave it out until the next
// window began. The window key makes the extra coverage idempotent, so a window
// another replica has already enqueued is not run twice. It reaches back no
// further than that one window — a window a process crossed while stopped is
// not enqueued.
func (s *scheduler) enqueueSchedule(ctx context.Context, now time.Time, tenants []tenant.Tenant, sched jobs.Schedule, startup bool) {
	start := windowStart(now, sched.Interval)
	lower := now.Add(-s.tick)
	// The lower bound is exclusive, so admitting a window whose start sits at
	// or before it takes a bound just ahead of that start.
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

// enqueueOne enters one tenant and enqueues one job for one window.
//
// Each enqueue is independent: a tenant that cannot be entered, or an enqueue
// the queue refuses, is logged and skipped, and the round carries on with the
// next pair. The next tick derives the same window start and the same key, so a
// window whose enqueue failed is retried for as long as the ticker runs.
func (s *scheduler) enqueueOne(ctx context.Context, t tenant.Tenant, sched jobs.Schedule, window time.Time, key string) {
	tctx, err := s.catalog.Enter(ctx, t.ID)
	if err != nil {
		// A disabled tenant is skipped for this window; the error is that
		// tenant's own answer, not a failure of the round.
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

// windowStart is the start of the window now falls in, for a declaration of the
// given interval: the largest multiple of the interval, measured from the Unix
// epoch, that is not after now. Windows are aligned to absolute multiples so
// that every replica derives the same window start from the same instant, which
// is what makes their idempotency keys agree.
func windowStart(now time.Time, interval time.Duration) time.Time {
	unit := interval.Nanoseconds()
	aligned := now.UnixNano() - floorMod(now.UnixNano(), unit)
	return time.Unix(0, aligned).UTC()
}

// windowKey is the idempotency key of one declaration's window: the SHA-256 of
// the declaration key and the window start, hex-encoded. It does not carry the
// tenant, so every tenant's job in one window shares one key while each becomes
// its own row under the queue's (tenant_id, idempotency_key) uniqueness.
func windowKey(scheduleKey string, window time.Time) string {
	sum := sha256.Sum256([]byte(scheduleKey + strconv.FormatInt(window.UnixNano(), 10)))
	return hex.EncodeToString(sum[:])
}

// floorMod returns n modulo unit in [0, unit), which is the remainder a floor
// division leaves. Go's % truncates toward zero, so a negative n would give a
// non-positive remainder and the alignment would round the wrong way.
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
