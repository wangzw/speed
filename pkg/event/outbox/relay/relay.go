// Package relay is the transactional outbox: it keeps the events transactions
// stage in a table of the delivered database, and relays each to the bus once
// its transaction commits.
package relay

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/dispatch"
	"github.com/vislake/speed/pkg/event/internal/tracing"
	"github.com/vislake/speed/pkg/event/outbox"
	"github.com/vislake/speed/pkg/log"
)

// ModuleName is this module's name in the registry.
const ModuleName = "event.outbox.relay"

//go:embed migrations
var migrationRoot embed.FS

var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// The embed directive above is what makes this unreachable: the
		// directory is part of the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*event.Bus)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides:  []core.Provision{{Token: (*outbox.Outbox)(nil), Exclusive: true}},
		Resources: []any{db.Migrations{FS: migrationsFS}},

		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			bus, err := core.Resolve[event.Bus](reg)
			if err != nil {
				return nil, err
			}
			logger, err := loggerOf(reg)
			if err != nil {
				return nil, err
			}
			return newRelay(database, bus, logger, defaultTiming)
		},

		Start: func(ctx context.Context, _ *core.Registry, instance any) error {
			if r, ok := instance.(*relay); ok {
				r.start(ctx)
			}
			return nil
		},

		Stop: func(ctx context.Context, _ *core.Registry, instance any) error {
			if r, ok := instance.(*relay); ok {
				return r.stop(ctx)
			}
			return nil
		},

		// Close stops a relay the stop stage never reached, which is what a
		// rollback hands it. The handle belongs to the database module, which
		// closes it.
		Close: func(ctx context.Context, _ *core.Registry, instance any) error {
			if r, ok := instance.(*relay); ok {
				return r.stop(ctx)
			}
			return nil
		},
	}
}

// loggerOf takes up the logging capability when the assembly delivers one, and
// falls back to the log module's process default when it does not. Anything
// but "no provider" is the lookup itself failing, and it ends the startup.
func loggerOf(reg *core.Registry) (*slog.Logger, error) {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			return log.Default(), nil
		}
		return nil, fmt.Errorf("%s: taking up the logger this module reports through: %w", ModuleName, err)
	}
	return delivered.Named(ModuleName), nil
}

type relay struct {
	handle *gorm.DB
	pool   *sql.DB
	sql    statements
	bus    event.Bus
	logger *slog.Logger
	timing timing
	token  string
	// nudges carries word that a row was staged here. It holds one, so a
	// staging never waits and a burst of them wakes the loop once.
	nudges chan struct{}
	// passes counts the passes the loop has completed. It is what lets a case
	// wait for the loop to have read the table since it staged a row, rather
	// than pausing for a while and hoping it did. Only the loop writes it.
	passes atomic.Int64

	mu       sync.Mutex
	started  bool
	stopped  bool
	stopping chan struct{}
	done     chan struct{}
	// work is the context the loop's statements and publishes run on, and
	// cancelWork ends it once the stop stage's grace is spent.
	work       context.Context
	cancelWork context.CancelFunc
}

func newRelay(database db.Database, bus event.Bus, logger *slog.Logger, t timing) (*relay, error) {
	stmts, err := statementsFor(database.Dialect())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ModuleName, err)
	}
	handle := database.DB()
	pool, err := handle.DB()
	if err != nil {
		return nil, fmt.Errorf("%s: the delivered handle has no connection pool: %w", ModuleName, err)
	}
	r := &relay{
		handle:   handle,
		pool:     pool,
		sql:      stmts,
		bus:      bus,
		logger:   logger,
		timing:   t,
		token:    newToken(),
		nudges:   make(chan struct{}, 1),
		stopping: make(chan struct{}),
		done:     make(chan struct{}),
	}
	// Until the start stage replaces it, the loop's context is one of its
	// own, so a relay that never starts can still be stopped and closed.
	r.work, r.cancelWork = context.WithCancel(context.Background())
	return r, nil
}

func newToken() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return host + "/" + strconv.Itoa(os.Getpid()) + "/" + event.NewID()
}

func (r *relay) Stage(tx *gorm.DB, evt event.Event) error {
	if err := r.judge(tx); err != nil {
		return err
	}
	payload, err := json.Marshal(evt.Payload)
	if err != nil {
		return fmt.Errorf("outbox: event %q carries a payload that cannot be serialized: %w", evt.Type, err)
	}
	evt = dispatch.Stamp(evt)
	// The tracing context is fixed here, in the caller's transaction, so the
	// subscriber's span is a child of the span this write was in rather than
	// of the relay's later publish.
	evt.TraceContext = dispatch.StampTraceContext(evt.TraceContext, tx.Statement.Context, tracing.Propagator())
	session := tx.Session(&gorm.Session{NewDB: true, Context: tx.Statement.Context})
	if err := session.Exec(r.sql.stage, evt.ID, evt.Type, evt.TenantID, string(payload),
		dispatch.EncodeTraceContext(evt.TraceContext)).Error; err != nil {
		return fmt.Errorf("outbox: staging event %q: %w", evt.Type, err)
	}
	r.nudge()
	return nil
}

func (r *relay) judge(tx *gorm.DB) error {
	if tx == nil || tx.Statement == nil {
		return fmt.Errorf("%w: Stage was handed no session", outbox.ErrNoTransaction)
	}
	committer, ok := tx.Statement.ConnPool.(gorm.TxCommitter)
	if !ok || committer == nil || isNilPointer(committer) {
		return fmt.Errorf("%w: stage the event on the session the transaction hands its closure, not on "+
			"the handle the transaction was opened on", outbox.ErrNoTransaction)
	}
	pool, err := tx.DB()
	if err != nil || pool != r.pool {
		return fmt.Errorf("%w: the relay reads the staged events of the database the database "+
			"capability delivered, and this transaction was begun on another connection pool",
			outbox.ErrOtherDatabase)
	}
	return nil
}

func isNilPointer(v any) bool {
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

func (r *relay) nudge() {
	select {
	case r.nudges <- struct{}{}:
	default:
	}
}

// start begins the loop on a context that carries ctx's values without its
// cancellation: the drive's context is cancelled as soon as the host begins
// to shut down, before any stop stage, and the relay runs until its own.
func (r *relay) start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.stopped {
		return
	}
	r.started = true
	r.cancelWork()
	r.work, r.cancelWork = context.WithCancel(context.WithoutCancel(ctx))
	go r.run()
}

// stop ends the loop: nothing more is claimed, a publish under way is
// given the grace to finish, and every row the relay still holds is given
// back, due at once, for the next relay to take.
func (r *relay) stop(ctx context.Context) error {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return nil
	}
	r.stopped = true
	started, cancelWork := r.started, r.cancelWork
	close(r.stopping)
	r.mu.Unlock()
	if !started {
		cancelWork()
		return nil
	}

	grace := time.NewTimer(r.timing.grace)
	defer grace.Stop()
	select {
	case <-r.done:
		return nil
	case <-grace.C:
	case <-ctx.Done():
	}
	// The grace is spent: the publish under way is cancelled, and the loop
	// gives back what it held on its way out.
	cancelWork()
	select {
	case <-r.done:
		return fmt.Errorf("%s: a publish was still under way when the stop stage's grace of %v ran out "+
			"and was cancelled; its event stays staged and is published again", ModuleName, r.timing.grace)
	case <-time.After(r.timing.cutoff):
		return fmt.Errorf("%s: the relay did not end within %v of its publish being cancelled; the rows "+
			"it holds are given back when their claim runs out", ModuleName, r.timing.cutoff)
	}
}
