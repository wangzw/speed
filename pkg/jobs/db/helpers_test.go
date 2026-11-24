package db

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/memory"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/catalog"
)

type engine struct {
	name     string
	database core.Module
	section  string
	locate   func(t *testing.T) string
}

var engines = []engine{
	{
		name:     "sqlite",
		database: sqlite.Module(),
		section:  "sqlite",
		locate:   func(t *testing.T) string { return filepath.Join(t.TempDir(), "delivered.db") },
	},
}

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

func withJobs(overrides map[string]any) map[string]any {
	out := quickJobs()
	maps.Copy(out, overrides)
	return out
}

func constructionSection(items map[string]any) map[string]any {
	out := map[string]any{}
	for item, value := range items {
		switch item {
		case workersItem, shutdownGraceItem:
			out[item] = value
		}
	}
	return out
}

func runtimeRows(t *testing.T, items map[string]any) map[string]any {
	t.Helper()
	rows := map[string]any{}
	for item, value := range items {
		switch item {
		case pollIntervalItem, leaseItem, retryBaseItem, retryMaxItem:
			text, ok := value.(string)
			if !ok {
				t.Fatalf("the runtime tunable %s is a %T, and a case writes a duration as a string",
					item, value)
			}
			parsed, err := time.ParseDuration(text)
			if err != nil {
				t.Fatalf("parsing the runtime tunable %s = %q: %v", item, text, err)
			}
			rows[ConfigNamespace+"."+item] = parsed
		case claimBatchItem:
			rows[ConfigNamespace+"."+item] = value
		}
	}
	return rows
}

func deliversSettings(modules []core.Module) bool {
	for _, module := range modules {
		for _, provision := range module.Provides {
			if provision.Token == (*settings.Store)(nil) {
				return true
			}
		}
	}
	return false
}

func standIn(name string, instance any, tokens ...core.Token) core.Module {
	provides := make([]core.Provision, len(tokens))
	for i, token := range tokens {
		provides[i] = core.Provision{Token: token, Exclusive: true}
	}
	return core.Module{
		Name:     name,
		Provides: provides,
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

type queueStore struct {
	mu     sync.Mutex
	values map[string]any
}

func newQueueStore(values map[string]any) *queueStore {
	if values == nil {
		values = map[string]any{}
	}
	return &queueStore{values: values}
}

func (s *queueStore) Get(_ settings.Layer, key string, target any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[key]
	if !ok {
		return settings.ErrUnknownKey
	}
	out := reflect.ValueOf(target)
	if out.Kind() != reflect.Pointer || out.IsNil() {
		panic("jobs.db tests: a settings read target has to be a non-nil pointer")
	}
	stored := reflect.ValueOf(value)
	if stored.Type() != out.Elem().Type() {
		return settings.ErrTypeMismatch
	}
	out.Elem().Set(stored)
	return nil
}

func (s *queueStore) Set(_ context.Context, w settings.Write) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[w.Key] = w.Value
	return nil
}

func (s *queueStore) Delete(_ context.Context, _ settings.Layer, key, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	return nil
}

func (s *queueStore) List(context.Context, settings.Layer) ([]settings.Entry, error) { return nil, nil }

func (s *queueStore) Declared() []settings.DeclaredItem { return nil }

func (s *queueStore) WrittenAt(settings.Layer, string) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func (s *queueStore) Read(key string, reads ...settings.LayerRead) ([]settings.LayerReadResult, error) {
	results := make([]settings.LayerReadResult, len(reads))
	for i, read := range reads {
		if err := s.Get(read.Layer, key, read.Target); err != nil {
			return nil, err
		}
		results[i] = settings.LayerReadResult{}
	}
	return results, nil
}

type host struct {
	database core.Module
	section  string
	dsn      string
	bus      bool
	jobs     map[string]any
	extra    []core.Module
	depth    core.Stage
}

func (e engine) on(t *testing.T, h host) host {
	h.database = e.database
	h.section = e.section
	h.dsn = e.locate(t)
	return h
}

func (h host) drive(t *testing.T, action func(ctx context.Context, reg *core.Registry) error) {
	t.Helper()
	dir := t.TempDir()
	document := map[string]any{
		"db": map[string]any{h.section: map[string]any{"dsn": h.dsn}},
		"log": map[string]any{"outputs": []any{
			map[string]any{"to": "file", "format": "json", "path": filepath.Join(dir, "log.json")},
		}},
		"jobs": map[string]any{"db": constructionSection(h.jobs)},
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
	if !deliversSettings(modules) {
		modules = append(modules, standIn("stand-in.settings",
			newQueueStore(runtimeRows(t, h.jobs)), (*settings.Store)(nil)))
	}

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

var errCaseFailed = errors.New("the case failed")

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

type sink struct {
	mu       sync.Mutex
	received []event.Event
}

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
				bus.Subscribe(typ, "jobs.test", func(_ context.Context, evt event.Event) error {
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

func (s *sink) events() []event.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]event.Event(nil), s.received...)
}

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

type recorder struct {
	typ   string
	mu    sync.Mutex
	runs  []run
	fail  func(call int, job *jobs.Job) error
	hold  func(call int) <-chan struct{}
	sleep time.Duration
}

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

func (r *recorder) called() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.runs)
}

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

func (r *recorder) payloads() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.runs))
	for _, run := range r.runs {
		out = append(out, run.payload)
	}
	return out
}

func (r *recorder) order() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.runs))
	for _, run := range r.runs {
		out = append(out, run.tenant)
	}
	return out
}

func tenantCtx(t *testing.T, reg *core.Registry, id string) context.Context {
	t.Helper()
	cam, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		t.Fatalf("resolving the tenant roster: %v", err)
	}
	if putErr := cam.Put(t.Context(), tenant.Tenant{
		ID: tenant.ID(id), Name: id, Status: tenant.StatusActive,
	}); putErr != nil {
		t.Fatalf("registering the tenant %q: %v", id, putErr)
	}
	ctx, err := cam.Enter(t.Context(), tenant.ID(id))
	if err != nil {
		t.Fatalf("entering the tenant %q: %v", id, err)
	}
	return ctx
}

func queueOf(t *testing.T, reg *core.Registry) jobs.Queue {
	t.Helper()
	q, err := core.Resolve[jobs.Queue](reg)
	if err != nil {
		t.Fatalf("resolving the queue: %v", err)
	}
	return q
}

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

func dbNowMilli(t *testing.T, reg *core.Registry) int64 {
	t.Helper()
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		t.Fatalf("resolving the database: %v", err)
	}
	var now int64
	if err := database.DB().WithContext(t.Context()).Raw("SELECT " + product(t, reg).sql.now).Row().Scan(&now); err != nil {
		t.Fatalf("reading the database's clock: %v", err)
	}
	return now
}

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

const settle = 10 * time.Second

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
