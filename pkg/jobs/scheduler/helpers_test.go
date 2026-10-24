package scheduler

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/tenant"
)

// tenantID names a tenant identifier in the cases.
type tenantID = tenant.ID

// tenantKey is the context key the fake roster marks a tenant with. It stands in
// for the marking the real roster does, so a fake queue can read back which
// tenant an enqueue was scoped to.
type tenantKey struct{}

func tenantOf(ctx context.Context) tenant.ID {
	id, _ := ctx.Value(tenantKey{}).(tenant.ID)
	return id
}

// fakeQueue stands in for the queue. It records every accepted enqueue and,
// like the real queue's (tenant_id, idempotency_key) unique index, converges a
// repeated (tenant, key) to one job.
type fakeQueue struct {
	mu       sync.Mutex
	accepted []enqueued
	attempts int
	seen     map[string]bool
	fail     error
}

type enqueued struct {
	tenant tenant.ID
	task   jobs.Task
}

func newQueue() *fakeQueue { return &fakeQueue{seen: map[string]bool{}} }

func (q *fakeQueue) Enqueue(ctx context.Context, t jobs.Task, _ ...jobs.EnqueueOption) (jobs.JobID, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.attempts++
	if q.fail != nil {
		return "", q.fail
	}
	if t.IdempotencyKey != "" {
		key := string(tenantOf(ctx)) + "\x00" + t.IdempotencyKey
		if q.seen[key] {
			return jobs.JobID("existing"), nil
		}
		q.seen[key] = true
	}
	q.accepted = append(q.accepted, enqueued{tenant: tenantOf(ctx), task: t})
	return jobs.JobID(fmt.Sprintf("job-%d", len(q.accepted))), nil
}

func (q *fakeQueue) Get(context.Context, jobs.JobID) (*jobs.Job, error) {
	return nil, jobs.ErrJobNotFound
}
func (q *fakeQueue) Cancel(context.Context, jobs.JobID) error { return nil }

func (q *fakeQueue) count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.accepted)
}

func (q *fakeQueue) tried() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.attempts
}

func (q *fakeQueue) acceptedJobs() []enqueued {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]enqueued(nil), q.accepted...)
}

// fakeHandlers stands in for the registration face with a fixed type set.
type fakeHandlers struct {
	types []string
}

func handlersFor(types ...string) *fakeHandlers { return &fakeHandlers{types: types} }

func (h *fakeHandlers) Register(jobs.Handler) error { return nil }
func (h *fakeHandlers) Registered() []string        { return append([]string(nil), h.types...) }

// fakeCatalog stands in for the roster: it holds the tenants and marks an active
// one on the context, refusing a disabled one the way the real roster does.
type fakeCatalog struct {
	tenants []tenant.Tenant
	listErr error
}

func newCatalog(tenants ...tenant.Tenant) *fakeCatalog { return &fakeCatalog{tenants: tenants} }

func active(id tenant.ID) tenant.Tenant {
	return tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusActive}
}

func disabled(id tenant.ID) tenant.Tenant {
	return tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusDisabled}
}

func (c *fakeCatalog) List(context.Context) ([]tenant.Tenant, error) {
	if c.listErr != nil {
		return nil, c.listErr
	}
	return append([]tenant.Tenant(nil), c.tenants...), nil
}

func (c *fakeCatalog) Enter(ctx context.Context, id tenant.ID) (context.Context, error) {
	for _, t := range c.tenants {
		if t.ID != id {
			continue
		}
		if t.Status != tenant.StatusActive {
			return ctx, tenant.ErrTenantDisabled
		}
		return context.WithValue(ctx, tenantKey{}, id), nil
	}
	return ctx, tenant.ErrInvalidTenant
}

func (c *fakeCatalog) Get(context.Context, tenant.ID) (tenant.Tenant, bool, error) {
	return tenant.Tenant{}, false, nil
}

func (c *fakeCatalog) ByAlias(context.Context, string) (tenant.Tenant, bool, error) {
	return tenant.Tenant{}, false, nil
}

func (c *fakeCatalog) Put(context.Context, tenant.Tenant) error    { return nil }
func (c *fakeCatalog) Create(context.Context, tenant.Tenant) error { return nil }
func (c *fakeCatalog) SetStatus(context.Context, tenant.ID, tenant.Status) error {
	return nil
}
func (c *fakeCatalog) SetStatusByAccount(context.Context, tenant.ID, tenant.Status) error {
	return nil
}
func (c *fakeCatalog) Delete(context.Context, tenant.ID) error           { return nil }
func (c *fakeCatalog) SetAlias(context.Context, tenant.ID, string) error { return nil }

func (c *fakeCatalog) EnterRegistered(ctx context.Context, id tenant.ID) (context.Context, error) {
	return context.WithValue(ctx, tenantKey{}, id), nil
}

// discardLogger is a logger that writes nothing, so a case's output stays clean.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// waitFor polls cond until it holds, failing the case if it does not within a
// second. A case that drives start has to wait this way: the ticker runs on its
// own goroutine.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// newTestScheduler builds a scheduler over the fakes with a pinned clock, so a
// case derives a window without waiting for a ticker.
func newTestScheduler(q jobs.Queue, h jobs.Handlers, c tenant.Catalog, tick time.Duration, now time.Time, schedules ...jobs.Schedule) *scheduler {
	at := now
	return &scheduler{
		queue:     q,
		handlers:  h,
		catalog:   c,
		logger:    discardLogger(),
		tick:      tick,
		schedules: schedules,
		now:       func() time.Time { return at },
	}
}

// The core modules the descriptor cases drive.

// noConfig is the reader a host supplies when nothing is configured. It decodes
// nothing, which leaves the module's zero configuration in place — a zero tick,
// which is the trigger off.
type noConfig struct{}

func (noConfig) Decode(string, any) error { return nil }

// configWith is a reader that hands the module a fixed tick.
type configWith struct{ tick time.Duration }

func (c configWith) Decode(_ string, target any) error {
	if cfg, ok := target.(*moduleConfig); ok {
		cfg.Tick = c.tick
	}
	return nil
}

func providingQueue(q jobs.Queue) core.Module {
	return core.Module{
		Name:     "test.queue",
		Provides: []core.Provision{{Token: (*jobs.Queue)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return q, nil },
	}
}

// providingQueueExplicitly delivers the queue and states StateEnabled, so that
// another exclusive provider stating StateAuto stands down over it.
func providingQueueExplicitly(q jobs.Queue) core.Module {
	return core.Module{
		Name:     "test.queue",
		Provides: []core.Provision{{Token: (*jobs.Queue)(nil), Exclusive: true}},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) { return q, nil },
	}
}

// alsoProvidesQueue delivers the queue exclusively without stating a stance, and
// declares schedules. Paired with providingQueueExplicitly it stands down, which
// makes it a participant that resolution disables.
func alsoProvidesQueue(name string, schedules ...jobs.Schedule) core.Module {
	res := make([]any, 0, len(schedules))
	for _, s := range schedules {
		res = append(res, s)
	}
	return core.Module{
		Name:      name,
		Provides:  []core.Provision{{Token: (*jobs.Queue)(nil), Exclusive: true}},
		Resources: res,
		New:       func(context.Context, *core.Registry) (any, error) { return newQueue(), nil },
	}
}

func providingHandlers(h jobs.Handlers) core.Module {
	return core.Module{
		Name:     "test.handlers",
		Provides: []core.Provision{{Token: (*jobs.Handlers)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return h, nil },
	}
}

func providingCatalog(c tenant.Catalog) core.Module {
	return core.Module{
		Name:     "test.catalog",
		Provides: []core.Provision{{Token: (*tenant.Catalog)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return c, nil },
	}
}

// declaringSchedules is a module that only declares the schedules, which is what
// a business module does with the periodic work it owns.
func declaringSchedules(name string, schedules ...jobs.Schedule) core.Module {
	res := make([]any, 0, len(schedules))
	for _, s := range schedules {
		res = append(res, s)
	}
	return core.Module{Name: name, Resources: res}
}
