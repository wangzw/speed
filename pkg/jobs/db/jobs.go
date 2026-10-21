// Package db is the persistent job queue: it delivers jobs.Queue and
// jobs.Handlers over one table, and drives the executions.
//
// The submitting side calls Enqueue in a tenant's context and a row lands in
// the jobs table. The executing side is a set of coroutines, one per worker,
// that read the due rows, claim up to a batch of them, enter each row's tenant,
// hand it to the handler registered for its type, and settle the row by the
// result — succeeded, retried after a backoff, or sent to dead_letter. A job
// therefore runs at least once; several replicas share the one table by taking
// time-limited claims of it, with no leader and no separate queue middleware.
//
// This module is the only database-backed queue, and it delivers both seams
// exclusively: a process holds exactly one queue. pkg/jobs is the interface
// package and registers nothing; this subpackage registers the module jobs.db,
// whose configuration namespace is its own name.
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

// ModuleName is this module's name in the registry. A host writes it into the
// command tree and into the commands that apply migrations.
const ModuleName = "jobs.db"

// ConfigNamespace is where this module's input items are mounted, which is the
// module name.
const ConfigNamespace = ModuleName

// The types of the events a job publishes as it reaches a terminal status.
const (
	// SucceededEventType is published when a job's handler returned without
	// error.
	SucceededEventType = "jobs.job.succeeded"
	// DeadLetterEventType is published when a job failed past its retry limit.
	DeadLetterEventType = "jobs.job.dead_letter"
	// CancelledEventType is published when a job was cancelled.
	CancelledEventType = "jobs.job.cancelled"
)

// Terminal is the payload of a terminal event. A subscriber that decodes it
// learns which job finished, of which type, and how it ended.
type Terminal struct {
	// ID identifies the job that reached the terminal status.
	ID jobs.JobID
	// Type names the kind of job.
	Type string
	// Status is the terminal status the job reached.
	Status jobs.Status
}

// The failures this module judges for itself. A caller tells them apart with
// errors.Is, and every wrap on the way out uses %w.
var (
	// ErrDuplicateHandler reports a Register naming a type another handler is
	// already registered under.
	ErrDuplicateHandler = errors.New("jobs.db: a handler is already registered for this type")
	// ErrInvalidHandler reports a Register handed a nil handler or one whose
	// Type is empty.
	ErrInvalidHandler = errors.New("jobs.db: the handler registration is not well formed")
	// ErrHandlerSetFrozen reports a Register made once the queue has started:
	// the handler set is fixed by then.
	ErrHandlerSetFrozen = errors.New("jobs.db: the handler set is fixed once the queue has started")
)

//go:embed migrations
var migrationRoot embed.FS

// migrationsFS is migrationRoot with the wrapping directory removed, so the
// tree the database module sees has one directory per dialect at its root,
// which is the shape it reads.
var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// The embed directive above is what makes this unreachable: the
		// directory is part of the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

// init registers this module with the process registry. Importing the package
// is what puts the database-backed queue in a binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// The product must deliver both declared capabilities.
var (
	_ jobs.Queue    = (*queue)(nil)
	_ jobs.Handlers = (*queue)(nil)
)

// Module is the descriptor a host registers.
//
// The database and the tenant roster are hard requirements: the table lives in
// the delivered database, and every job row belongs to a tenant whose roster
// carries it, which is also what installs the statement filter the scoped reads
// and writes go through. The event bus, the logging capability and the settings
// store are optional; without a bus no terminal event is published while the
// jobs run exactly as they otherwise would, without logging the records reach
// the process default logger, and without a settings store the loop's tunables
// read as their declared defaults.
//
// Both capabilities are delivered exclusively. A process has exactly one queue,
// and two of them would be two answers to which row a job went into, so a
// second provider fails the startup rather than one silently winning.
//
// An instance the host supplies for this module stands where the queue would
// be, and the start, stop and close callbacks act on a queue and on nothing
// else: any other instance is left alone.
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

		// New builds the product and reads no table of its own: this stage runs
		// before the migrations are applied, so on a first start the jobs table
		// is not there yet. The table is first read when the executors start.
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

		// Close stops a queue the stop stage never reached, which is what a
		// rollback hands it. The handle belongs to the database module, which
		// closes it.
		Close: func(ctx context.Context, _ *core.Registry, instance any) error {
			if q, ok := productOf(instance); ok {
				return q.stop(ctx)
			}
			return nil
		},
	}
}

// productOf narrows an instance to this module's product. An instance of
// another shape is one the host supplied for this module, and the callbacks
// leave it alone.
func productOf(instance any) (*queue, bool) {
	q, ok := instance.(*queue)
	return q, ok && q != nil
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

// newQueue takes up what the descriptor requires and assembles the product. It
// reads no table: the jobs table is first read when the executors start.
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
		// Starting without the statements of the running dialect would mean an
		// executor that cannot read the table it is meant to claim from, so the
		// module is refused here rather than coming up and doing nothing.
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
	// Until the start stage replaces it, the loop's context is one of its own,
	// so a queue that never starts can still be stopped and closed.
	q.work, q.cancelWork = context.WithCancel(context.Background())
	return q, nil
}

// takeLogger picks up the logging capability if the assembly has one.
func (q *queue) takeLogger(reg *core.Registry) {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		return
	}
	q.logger = delivered.Named(ModuleName)
}

// takeBus picks up the event bus if the assembly has one. Without it no
// terminal event is published; the jobs run exactly as they otherwise would.
func (q *queue) takeBus(reg *core.Registry) {
	delivered, err := core.Resolve[event.Bus](reg)
	if err != nil {
		return
	}
	q.bus = delivered
}

// takeSettings picks up the settings store if the assembly has one. A store
// that is not there is a legal assembly: the loop's tunables then read as their
// declared defaults. Any other failure to take it up ends the start, because a
// capability that is there and cannot be taken up is not an absence.
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

// queue is the module's product. It delivers jobs.Queue and jobs.Handlers, and
// runs the executions.
type queue struct {
	handle  *gorm.DB
	sql     statements
	catalog tenant.Catalog
	bus     event.Bus
	logger  *slog.Logger
	// set is what the queue is constructed with: the worker count and the stop
	// grace.
	set construction
	// tuned reads the loop's runtime tunables at each use; with no settings
	// store assembled, every read is the declared default.
	tuned *settingsReader

	mu       sync.Mutex
	handlers map[string]jobs.Handler
	started  bool
	stopped  bool

	// running is what the submission face reports: true between Start and Stop.
	running atomic.Bool

	// stopping is closed at the stop stage: the executors claim nothing more.
	stopping chan struct{}
	// done is closed once every executor has returned.
	done chan struct{}
	// work is the context the executors' statements and executions run on, and
	// cancelWork ends it once the stop stage's grace is spent. It carries the
	// start context's values without its cancellation, so the drive beginning
	// to shut down does not end the executors before this module's own Stop.
	work       context.Context
	cancelWork context.CancelFunc
}

// Register binds a handler under its type; see jobs.Handlers.
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

// Registered lists the registered types, in a stable order; see jobs.Handlers.
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

// handlerOf looks up the handler for one type.
func (q *queue) handlerOf(typ string) (jobs.Handler, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	h, ok := q.handlers[typ]
	return h, ok
}

// start begins the executors. The first round reads the table at once, which is
// how a start takes over the due jobs a previous run left behind.
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

// stop ends the queue: nothing more is claimed, an execution under way is given
// the shutdown grace to finish, and every row an executor still holds is given
// back so another executor or the next start may claim it without waiting out
// the lease.
//
// It is idempotent, and it tolerates a queue that never started.
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
	// The grace is spent: the executions under way are cancelled, and the
	// executors give back what they held on their way out.
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

// newToken names one executor: the host and the process it runs in, and an
// identifier no other executor carries. It is what a row's claimed_by holds, so
// an executor recognises its own rows and no other's, and a release gives back
// only its own.
func newToken() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return host + "/" + strconv.Itoa(os.Getpid()) + "/" + uuid.NewString()
}
