// Package db is the persistent job queue: it delivers jobs.Queue and
// jobs.Handlers over one table and drives the executions. It registers the
// module jobs.db, whose configuration namespace is its own name.
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's name in the registry.
const ModuleName = "jobs.db"

// ConfigNamespace is where this module's input items are mounted, which is the
// module name.
const ConfigNamespace = ModuleName

const (
	// SucceededEventType is published when a job's handler returned without
	// error.
	SucceededEventType = "jobs.job.succeeded"
	// DeadLetterEventType is published when a job failed past its retry limit.
	DeadLetterEventType = "jobs.job.dead_letter"
	// CancelledEventType is published when a job was cancelled.
	CancelledEventType = "jobs.job.cancelled"
)

// Terminal is the payload of a terminal event.
type Terminal struct {
	// ID identifies the job that reached the terminal status.
	ID jobs.JobID
	// Type names the kind of job.
	Type string
	// Status is the terminal status the job reached.
	Status jobs.Status
}

var (
	// ErrDuplicateHandler reports a Register naming a type another handler is
	// already registered under.
	ErrDuplicateHandler = errors.New("jobs.db: a handler is already registered for this type")
	// ErrInvalidHandler reports a Register handed a nil handler or one whose
	// Type is empty.
	ErrInvalidHandler = errors.New("jobs.db: the handler registration is not well formed")
	// ErrHandlerSetFrozen reports a Register made once the queue has started.
	ErrHandlerSetFrozen = errors.New("jobs.db: the handler set is fixed once the queue has started")
)

//go:embed migrations
var migrationRoot embed.FS

var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

func init() { core.ProcessRegistry.Register(Module()) }

var (
	_ jobs.Queue    = (*queue)(nil)
	_ jobs.Handlers = (*queue)(nil)
)

// Module is the descriptor a host registers. The database and the tenant roster
// are required; the event bus, the logging capability and the settings store are
// optional. Both capabilities are delivered exclusively.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*event.Bus)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
			{Token: (*settings.Store)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*jobs.Queue)(nil), Exclusive: true},
			{Token: (*jobs.Handlers)(nil), Exclusive: true},
		},
		Resources: []any{schema(), settingsSchema(), db.Migrations{FS: migrationsFS}},

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
			return newQueue(reg)
		},

		Start: func(ctx context.Context, _ *core.Registry, instance any) error {
			if q, ok := productOf(instance); ok {
				q.start(ctx)
			}
			return nil
		},

		Stop: func(ctx context.Context, _ *core.Registry, instance any) error {
			if q, ok := productOf(instance); ok {
				return q.stop(ctx)
			}
			return nil
		},

		Close: func(ctx context.Context, _ *core.Registry, instance any) error {
			if q, ok := productOf(instance); ok {
				return q.stop(ctx)
			}
			return nil
		},
	}
}

func productOf(instance any) (*queue, bool) {
	q, ok := instance.(*queue)
	return q, ok && q != nil
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	return reader, nil
}

func newQueue(reg *core.Registry) (*queue, error) {
	reader, err := readerOf(reg)
	if err != nil {
		return nil, err
	}
	set, err := readSettings(reader)
	if err != nil {
		return nil, err
	}
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the database: %w", ModuleName, err)
	}
	catalog, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the tenant roster: %w", ModuleName, err)
	}
	stmts, err := statementsFor(database.Dialect())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ModuleName, err)
	}
	store, err := takeSettings(reg)
	if err != nil {
		return nil, err
	}
	q := &queue{
		handle:   database.DB(),
		sql:      stmts,
		catalog:  catalog,
		logger:   log.Default(),
		set:      set,
		tuned:    newSettingsReader(store),
		handlers: map[string]jobs.Handler{},
		stopping: make(chan struct{}),
		done:     make(chan struct{}),
	}
	q.takeLogger(reg)
	q.takeBus(reg)
	q.work, q.cancelWork = context.WithCancel(context.Background())
	return q, nil
}

func (q *queue) takeLogger(reg *core.Registry) {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		return
	}
	q.logger = delivered.Named(ModuleName)
}

func (q *queue) takeBus(reg *core.Registry) {
	delivered, err := core.Resolve[event.Bus](reg)
	if err != nil {
		return
	}
	q.bus = delivered
}

func takeSettings(reg *core.Registry) (settings.Store, error) {
	store, err := core.Resolve[settings.Store](reg)
	switch {
	case err == nil:
		return store, nil
	case errors.Is(err, core.ErrMissingProvider):
		return nil, nil
	default:
		return nil, fmt.Errorf("%s: taking up the settings store: %w", ModuleName, err)
	}
}

type queue struct {
	handle  *gorm.DB
	sql     statements
	catalog tenant.Catalog
	bus     event.Bus
	logger  *slog.Logger
	set     construction
	tuned   *settingsReader

	mu       sync.Mutex
	handlers map[string]jobs.Handler
	started  bool
	stopped  bool

	running atomic.Bool

	stopping   chan struct{}
	done       chan struct{}
	work       context.Context
	cancelWork context.CancelFunc
}

func (q *queue) Register(h jobs.Handler) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.started {
		return fmt.Errorf("%w: Register was called after the queue started", ErrHandlerSetFrozen)
	}
	if h == nil {
		return fmt.Errorf("%w: the handler is nil", ErrInvalidHandler)
	}
	typ := h.Type()
	if typ == "" {
		return fmt.Errorf("%w: the handler names no type", ErrInvalidHandler)
	}
	if _, held := q.handlers[typ]; held {
		return fmt.Errorf("%w: %q", ErrDuplicateHandler, typ)
	}
	q.handlers[typ] = h
	return nil
}

func (q *queue) Registered() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, 0, len(q.handlers))
	for typ := range q.handlers {
		out = append(out, typ)
	}
	sort.Strings(out)
	return out
}

func (q *queue) handlerOf(typ string) (jobs.Handler, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	h, ok := q.handlers[typ]
	return h, ok
}

func (q *queue) start(ctx context.Context) {
	q.mu.Lock()
	if q.started || q.stopped {
		q.mu.Unlock()
		return
	}
	q.started = true
	q.cancelWork()
	q.work, q.cancelWork = context.WithCancel(context.WithoutCancel(ctx))
	q.running.Store(true)
	workers := q.set.workers
	q.mu.Unlock()

	go func() {
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			w := &worker{token: newToken()}
			go func() {
				defer wg.Done()
				q.runWorker(w)
			}()
		}
		wg.Wait()
		close(q.done)
	}()
}

func (q *queue) stop(ctx context.Context) error {
	q.mu.Lock()
	if q.stopped {
		q.mu.Unlock()
		return nil
	}
	q.stopped = true
	started, cancelWork := q.started, q.cancelWork
	q.running.Store(false)
	close(q.stopping)
	q.mu.Unlock()
	if !started {
		cancelWork()
		return nil
	}

	grace := time.NewTimer(q.set.grace)
	defer grace.Stop()
	select {
	case <-q.done:
		return nil
	case <-grace.C:
		q.logger.Warn("jobs.db: the stop stage's grace ran out; the executions under way are cancelled and "+
			"the rows they held are given back", "grace", q.set.grace)
	case <-ctx.Done():
	}
	cancelWork()
	wait := time.NewTimer(cutoff)
	defer wait.Stop()
	select {
	case <-q.done:
		return nil
	case <-wait.C:
		return fmt.Errorf("%s: the queue did not end within %v of its executions being cancelled; the rows "+
			"it holds are taken over once their claim runs out", ModuleName, cutoff)
	}
}

func newToken() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return host + "/" + strconv.Itoa(os.Getpid()) + "/" + uuid.NewString()
}
