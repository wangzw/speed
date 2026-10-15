package db

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	dbpostgres "github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/memory"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/catalog"
)

// engine is one database capability this module is exercised on. The PostgreSQL
// one skips on a machine without docker, unless CI is set.
type engine struct {
	name     string
	database core.Module
	// section is the key the engine's input items sit under in the db namespace.
	section string
	locate  func(t *testing.T) string
}

var engines = []engine{
	{
		name:     "sqlite",
		database: sqlite.Module(),
		section:  "sqlite",
		locate:   func(t *testing.T) string { return filepath.Join(t.TempDir(), "delivered.db") },
	},
	{
		name:     "postgres",
		database: dbpostgres.Module(),
		section:  "postgres",
		locate:   pgtest.Acquire,
	},
}

// quickJobs is the configuration the cases run the queue by: the module's own
// shape, on a scale a case can wait out.
func quickJobs() map[string]any {
	return map[string]any{
		pollIntervalItem:  "15ms",
		workersItem:       1,
		claimBatchItem:    4,
		leaseItem:         "10s",
		shutdownGraceItem: "200ms",
		retryBaseItem:     "20ms",
		retryMaxItem:      "100ms",
	}
}

// withJobs overrides one or more items of the quick configuration.
func withJobs(overrides map[string]any) map[string]any {
	out := quickJobs()
	maps.Copy(out, overrides)
	return out
}

// host is one assembled run of this module: the engine and the database it sits
// on, whether an event bus takes part, the configuration section of jobs.db,
// whatever extra modules a case takes in, and the stage the run reaches.
type host struct {
	database core.Module
	section  string
	dsn      string
	bus      bool
	jobs     map[string]any
	extra    []core.Module
	depth    core.Stage
}

// on builds a host on an engine over a fresh database of that engine's own.
func (e engine) on(t *testing.T, h host) host {
	h.database = e.database
	h.section = e.section
	h.dsn = e.locate(t)
	return h
}

// drive loads the configuration the way a host loads it, drives a registry to
// the run's depth, and runs action there.
//
// A configuration that cannot be loaded fails the case, because that is the
// case's own mistake. The action reports through t.Errorf and returns an error
// to end the drive, so the drive still shuts down and the pools it opened are
// released.
func (h host) drive(t *testing.T, action func(ctx context.Context, reg *core.Registry) error) {
	t.Helper()
	dir := t.TempDir()
	document := map[string]any{
		"db": map[string]any{h.section: map[string]any{"dsn": h.dsn}},
		"log": map[string]any{"outputs": []any{
			map[string]any{"to": "file", "format": "json", "path": filepath.Join(dir, "log.json")},
		}},
		"jobs": map[string]any{"db": h.jobs},
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the configuration: %v", err)
	}
	locator := filepath.Join(dir, "config.json")
	if writeErr := os.WriteFile(locator, body, 0o600); writeErr != nil {
		t.Fatalf("writing the primary config source: %v", writeErr)
	}

	modules := []core.Module{h.database, log.Module(), catalog.Module(), Module()}
	if h.bus {
		modules = append(modules, memory.Module())
	}
	modules = append(modules, h.extra...)

	loading := core.New()
	loading.Register(jsonformat.Module())
	loading.Register(filesource.Module())
	loading.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDJOBSTEST", DefaultLocator: "file://" + locator}},
	})
	for _, m := range modules {
		loading.Register(m)
	}
	reader, err := config.Load(t.Context(), loading, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}

	reg := core.New()
	reg.Register(config.Module())
	names := make([]string, 0, len(modules))
	seen := map[string]bool{}
	for _, m := range modules {
		if m.Name == "" || seen[m.Name] {
			continue
		}
		seen[m.Name] = true
		reg.Register(m)
		names = append(names, m.Name)
	}
	reg.Supply(reader, config.ModuleName)
	depth := h.depth
	if depth == 0 {
		depth = core.StageStart
	}
	if err := reg.Drive(t.Context(), core.Drive{
		Depth:        depth,
		Participants: names,
		Action:       action,
	}); err != nil {
		t.Fatalf("driving the assembly: %v", err)
	}
}

// errCaseFailed ends a drive whose case has already reported what went wrong.
var errCaseFailed = errors.New("the case failed")

// consumer is a module standing in for a business module that has jobs to run:
// it registers the handlers a case gives it in its own Init, so the handler set
// is fixed before the queue starts.
func consumer(handlers ...jobs.Handler) core.Module {
	return core.Module{
		Name:     "test.consumer",
		Requires: []core.Requirement{{Token: (*jobs.Handlers)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			handlersFace, err := core.Resolve[jobs.Handlers](reg)
			if err != nil {
				return err
			}
			for _, h := range handlers {
				if err := handlersFace.Register(h); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// sink is a subscriber that records the terminal events it receives.
type sink struct {
	mu       sync.Mutex
	received []event.Event
}

// module subscribes this sink to every terminal event type in its Init.
func (s *sink) module() core.Module {
	return core.Module{
		Name:     "test.sink",
		Requires: []core.Requirement{{Token: (*event.Bus)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return s, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			bus, err := core.Resolve[event.Bus](reg)
			if err != nil {
				return err
			}
			for _, typ := range []string{SucceededEventType, DeadLetterEventType, CancelledEventType} {
				bus.Subscribe(typ, func(_ context.Context, evt event.Event) error {
					s.mu.Lock()
					s.received = append(s.received, evt)
					s.mu.Unlock()
					return nil
				})
			}
			return nil
		},
	}
}

// events lists what the sink received.
func (s *sink) events() []event.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]event.Event(nil), s.received...)
}

// ofType lists the identifiers of the events of one type the sink received.
func (s *sink) ofType(typ string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, evt := range s.received {
		if evt.Type == typ {
			out = append(out, evt.ID)
		}
	}
	return out
}

// recorder is a handler that counts its calls and records the runs, optionally
// blocking or failing on a call the case steers.
type recorder struct {
	typ string
	mu  sync.Mutex
	// runs is what each call saw, in order.
	runs []run
	// fail reports whether this call fails; nil accepts every call.
	fail func(call int, job *jobs.Job) error
	// hold, when set, names the channel each call waits on before it returns,
	// or until its context ends. A nil channel lets the call through.
	hold func(call int) <-chan struct{}
	// sleep, when set, is how long each call takes regardless of its context,
	// standing in for a handler that does not heed the deadline.
	sleep time.Duration
}

// run is one recorded execution.
type run struct {
	job     jobs.JobID
	kind    string
	tenant  string
	payload string
}

func (r *recorder) Type() string { return r.typ }

func (r *recorder) Handle(ctx context.Context, job *jobs.Job, _ jobs.ProgressFn) (jobs.Result, error) {
	r.mu.Lock()
	call := len(r.runs)
	tenantID, _ := tenant.FromContext(ctx)
	r.runs = append(r.runs, run{job: job.ID, kind: job.Type, tenant: string(tenantID), payload: string(job.Payload)})
	fail, hold, sleep := r.fail, r.hold, r.sleep
	r.mu.Unlock()
	if sleep > 0 {
		time.Sleep(sleep)
	}
	if hold != nil {
		if wait := hold(call); wait != nil {
			select {
			case <-wait:
			case <-ctx.Done():
				return jobs.Result{}, ctx.Err()
			}
		}
	}
	if fail != nil {
		if err := fail(call, job); err != nil {
			return jobs.Result{}, err
		}
	}
	return jobs.Result{Data: []byte("done")}, nil
}

// called is how many times the handler ran.
func (r *recorder) called() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.runs)
}

// callsOf is how many times one job's handler ran.
func (r *recorder) callsOf(id jobs.JobID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, run := range r.runs {
		if run.job == id {
			n++
		}
	}
	return n
}

// payloads lists the payloads of the runs, in execution order.
func (r *recorder) payloads() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.runs))
	for _, run := range r.runs {
		out = append(out, run.payload)
	}
	return out
}

// order lists the tenants of the runs, in execution order.
func (r *recorder) order() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.runs))
	for _, run := range r.runs {
		out = append(out, run.tenant)
	}
	return out
}

// tenantCtx registers a tenant in the roster and returns a context that
// belongs to it.
func tenantCtx(t *testing.T, reg *core.Registry, id string) context.Context {
	t.Helper()
	cam, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		t.Fatalf("resolving the tenant roster: %v", err)
	}
	if putErr := cam.Put(t.Context(), tenant.Tenant{
		ID: tenant.ID(id), Alias: id, Status: tenant.StatusActive,
	}); putErr != nil {
		t.Fatalf("registering the tenant %q: %v", id, putErr)
	}
	ctx, err := cam.Enter(t.Context(), tenant.ID(id))
	if err != nil {
		t.Fatalf("entering the tenant %q: %v", id, err)
	}
	return ctx
}

// queueOf resolves the queue face.
func queueOf(t *testing.T, reg *core.Registry) jobs.Queue {
	t.Helper()
	q, err := core.Resolve[jobs.Queue](reg)
	if err != nil {
		t.Fatalf("resolving the queue: %v", err)
	}
	return q
}

// productOf resolves the module's own product, for a case that drives the stop
// stage itself.
func product(t *testing.T, reg *core.Registry) *queue {
	t.Helper()
	q, err := core.Resolve[jobs.Queue](reg)
	if err != nil {
		t.Fatalf("resolving the queue: %v", err)
	}
	typed, ok := q.(*queue)
	if !ok {
		t.Fatalf("the queue is a %T, not this module's product", q)
	}
	return typed
}

// state is what a job row holds, read directly.
type state struct {
	status    string
	attempts  int
	claimedBy string
	error     string
	result    string
	available int64
	published *int64
	cancel    bool
}

// stateOf reads one job row past every filter.
func stateOf(t *testing.T, reg *core.Registry, id jobs.JobID) state {
	t.Helper()
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		t.Fatalf("resolving the database: %v", err)
	}
	var row struct {
		Status      string  `gorm:"column:status"`
		Attempts    int     `gorm:"column:attempts"`
		ClaimedBy   *string `gorm:"column:claimed_by"`
		Error       *string `gorm:"column:error"`
		Result      []byte  `gorm:"column:result"`
		AvailableAt int64   `gorm:"column:available_at"`
		Published   *int64  `gorm:"column:terminal_published_at"`
		Cancel      bool    `gorm:"column:cancel_requested"`
	}
	err = database.DB().WithContext(t.Context()).
		Raw("SELECT status, attempts, claimed_by, error, result, available_at, terminal_published_at, "+
			"cancel_requested FROM jobs WHERE id = ?", string(id)).Scan(&row).Error
	if err != nil {
		t.Fatalf("reading the job row %s: %v", id, err)
	}
	out := state{
		status: row.Status, attempts: row.Attempts, available: row.AvailableAt,
		published: row.Published, cancel: row.Cancel,
	}
	if row.ClaimedBy != nil {
		out.claimedBy = *row.ClaimedBy
	}
	if row.Error != nil {
		out.error = *row.Error
	}
	out.result = string(row.Result)
	return out
}

// rowCount is how many rows the jobs table holds.
func rowCount(t *testing.T, reg *core.Registry) int64 {
	t.Helper()
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		t.Fatalf("resolving the database: %v", err)
	}
	var n int64
	if err := database.DB().WithContext(t.Context()).
		Raw("SELECT count(*) FROM jobs").Row().Scan(&n); err != nil {
		t.Fatalf("counting the job rows: %v", err)
	}
	return n
}

// update runs one raw statement against the jobs table, for a case steering a
// row directly.
func update(t *testing.T, reg *core.Registry, statement string, args ...any) {
	t.Helper()
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		t.Fatalf("resolving the database: %v", err)
	}
	if err := database.DB().WithContext(t.Context()).Exec(statement, args...).Error; err != nil {
		t.Fatalf("running %q: %v", statement, err)
	}
}

// settle bounds how long a case waits for the queue to reach a state.
const settle = 10 * time.Second

// eventually polls cond until it holds or the settle bound runs out.
func eventually(cond func() bool) bool {
	deadline := time.Now().Add(settle)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}
