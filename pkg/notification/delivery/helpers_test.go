package delivery

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/notification/preferences"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/catalog"
)

// The stubs implement the seams the module takes up, so a stub and an interface
// cannot drift apart without failing the build.
var (
	_ jobs.Queue               = (*stubQueue)(nil)
	_ jobs.Handlers            = (*stubQueue)(nil)
	_ notification.Preferences = (*stubPrefs)(nil)
	_ notification.Inbox       = (*stubInbox)(nil)
	_ i18n.Localizer           = (*stubLocalizer)(nil)
	_ event.Bus                = (*stubBus)(nil)
)

// stubQueue is a stand-in submission and registration face. It records every
// enqueued task and converges two tasks under one idempotency key into one job,
// the way the real queue's (tenant_id, idempotency_key) uniqueness does, so a
// case can hold what a dispatch derived as a key.
type stubQueue struct {
	mu       sync.Mutex
	tasks    []jobs.Task
	byKey    map[string]jobs.JobID
	handlers map[string]jobs.Handler
	seq      int
}

func newStubQueue() *stubQueue {
	return &stubQueue{byKey: map[string]jobs.JobID{}, handlers: map[string]jobs.Handler{}}
}

func (q *stubQueue) Enqueue(_ context.Context, t jobs.Task, opts ...jobs.EnqueueOption) (jobs.JobID, error) {
	options := jobs.NewEnqueueOptions()
	for _, opt := range opts {
		opt(options)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.tasks = append(q.tasks, t)
	if t.IdempotencyKey != "" {
		if held, ok := q.byKey[t.IdempotencyKey]; ok {
			return held, nil
		}
	}
	q.seq++
	id := jobs.JobID(fmt.Sprintf("job-%d", q.seq))
	if t.IdempotencyKey != "" {
		q.byKey[t.IdempotencyKey] = id
	}
	return id, nil
}

func (q *stubQueue) Get(context.Context, jobs.JobID) (*jobs.Job, error) {
	return nil, jobs.ErrJobNotFound
}

func (q *stubQueue) Cancel(context.Context, jobs.JobID) error { return nil }

func (q *stubQueue) Register(h jobs.Handler) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.handlers[h.Type()] = h
	return nil
}

func (q *stubQueue) Registered() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, 0, len(q.handlers))
	for typ := range q.handlers {
		out = append(out, typ)
	}
	return out
}

// enqueued lists the tasks the queue was handed, in order.
func (q *stubQueue) enqueued() []jobs.Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]jobs.Task(nil), q.tasks...)
}

// handlerOf returns the registered handler for a type.
func (q *stubQueue) handlerOf(typ string) (jobs.Handler, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	h, ok := q.handlers[typ]
	return h, ok
}

// stubPrefs is a stand-in preferences seam. It answers Effective from a fixed
// map and records the recipients it was asked about.
type stubPrefs struct {
	mu       sync.Mutex
	channels map[string][]notification.Channel
	asked    []string
	err      error
}

func (p *stubPrefs) Types() []notification.TypeSpec { return nil }

func (p *stubPrefs) Type(string) (notification.TypeSpec, bool) { return notification.TypeSpec{}, false }

func (p *stubPrefs) Effective(_ context.Context, recipient, typeKey string) ([]notification.Channel, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked = append(p.asked, recipient)
	if p.err != nil {
		return nil, p.err
	}
	return append([]notification.Channel(nil), p.channels[typeKey]...), nil
}

func (p *stubPrefs) Set(context.Context, string, string, []notification.Channel) ([]notification.Channel, error) {
	return nil, nil
}

// stubInbox is a stand-in inbox seam. It is idempotent on the dedupe key the
// way the real inbox is: the same key lands one message and a repeat returns it
// without a second write.
type stubInbox struct {
	mu      sync.Mutex
	writes  int
	byKey   map[string]notification.Message
	err     error
	written []notification.Message
}

func newStubInbox() *stubInbox {
	return &stubInbox{byKey: map[string]notification.Message{}}
}

func (s *stubInbox) Write(_ context.Context, recipient string, m notification.Message, dedupeKey string) (notification.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return notification.Message{}, s.err
	}
	if held, ok := s.byKey[dedupeKey]; ok {
		return held, nil
	}
	s.writes++
	m.ID = fmt.Sprintf("msg-%d", s.writes)
	m.CreatedAt = time.Now().UTC()
	s.byKey[dedupeKey] = m
	s.written = append(s.written, m)
	return m, nil
}

func (s *stubInbox) List(context.Context, string, notification.ListQuery) (notification.MessagePage, error) {
	return notification.MessagePage{}, nil
}

func (s *stubInbox) UnreadCount(context.Context, string) (int, error) { return 0, nil }

func (s *stubInbox) MarkRead(context.Context, string, string) error { return nil }

func (s *stubInbox) ReadAll(context.Context, string) (int, error) { return 0, nil }

func (s *stubInbox) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

func (s *stubInbox) last() notification.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.written) == 0 {
		return notification.Message{}
	}
	return s.written[len(s.written)-1]
}

// localizeCall is one Localize request a case can inspect.
type localizeCall struct {
	key  i18n.Key
	args i18n.Args
}

// stubLocalizer is a stand-in render seam. It answers from a map a case sets and
// reports the key itself for anything else, the way the engine falls back to the
// key.
type stubLocalizer struct {
	mu    sync.Mutex
	text  map[i18n.Key]string
	calls []localizeCall
}

func newStubLocalizer() *stubLocalizer {
	return &stubLocalizer{text: map[i18n.Key]string{}}
}

func (l *stubLocalizer) set(key string, text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.text[i18n.Key(key)] = text
}

func (l *stubLocalizer) Localize(_ context.Context, key i18n.Key, args i18n.Args) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, localizeCall{key: key, args: args})
	if text, ok := l.text[key]; ok {
		return text
	}
	return string(key)
}

func (l *stubLocalizer) Translate(_ i18n.Locale, key i18n.Key, args i18n.Args) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, localizeCall{key: key, args: args})
	text, ok := l.text[key]
	return text, ok
}

func (l *stubLocalizer) asked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, call := range l.calls {
		if string(call.key) == key {
			return true
		}
	}
	return false
}

// stubBus is a stand-in event bus that records what was published.
type stubBus struct {
	mu        sync.Mutex
	published []event.Event
}

func (b *stubBus) Publish(_ context.Context, evt event.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, evt)
	return nil
}

func (b *stubBus) Subscribe(string, event.Handler) {}

func (b *stubBus) events() []event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]event.Event(nil), b.published...)
}

// standIn is a module that delivers instance for the tokens named, the way
// another module delivers its capability exclusively.
func standIn(name string, instance any, tokens ...core.Token) core.Module {
	provides := make([]core.Provision, len(tokens))
	for i, tk := range tokens {
		provides[i] = core.Provision{Token: tk, Exclusive: true}
	}
	return core.Module{
		Name:     name,
		Provides: provides,
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

// declareType is a module that declares one notification type as a resource.
func declareType(name string, spec notification.TypeSpec) core.Module {
	return core.Module{
		Name:      name,
		Resources: []any{spec},
		New:       func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
	}
}

// typeName is the type every case declares unless it declares another.
const typeName = "billing.invoice_paid"

// defaultSpec is the type every case declares unless it declares another: an
// unsubscribable type whose default channels are in_app and email, with a
// parameter whitelist.
func defaultSpec() notification.TypeSpec {
	return notification.TypeSpec{
		Key:                    typeName,
		Group:                  "billing",
		DefaultChannels:        []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
		Unsubscribable:         true,
		RecipientVisibleParams: []string{"invoice_id", "amount"},
	}
}

// host is one assembled run: the delivery module over a real SQLite database and
// the tenant roster, with the seams a case drives stood in for and the type
// declarations a case wants.
type host struct {
	// specs are extra type declarations, each by its own module.
	specs map[string]notification.TypeSpec
	// sms adds the two sms seams when set.
	sms bool
	// noInbox and noLocalizer drop a required seam, which fails resolution.
	noInbox, noLocalizer bool
	// withPreferences drives the taxonomy's other consumption point —
	// notification.preferences — instead of this module, over the same
	// declarations, so a case can hold both points to one criterion.
	withPreferences bool
}

// drive loads the configuration the way a host loads it, drives a registry to
// the start stage, and runs action there. It reports the error the drive ended
// with, so a case can hold the failure a missing seam produces.
func (h host) drive(t *testing.T, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()
	dir := t.TempDir()
	dsn := filepath.Join(dir, "notification.db")
	document := map[string]any{
		"db": map[string]any{"sqlite": map[string]any{"dsn": dsn}},
		"log": map[string]any{"outputs": []any{
			map[string]any{"to": "file", "format": "json", "path": filepath.Join(dir, "log.json")},
		}},
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the configuration: %v", err)
	}
	locator := filepath.Join(dir, "config.json")
	if writeErr := os.WriteFile(locator, body, 0o600); writeErr != nil {
		t.Fatalf("writing the primary config source: %v", writeErr)
	}

	specs := h.specs
	if specs == nil {
		specs = map[string]notification.TypeSpec{"test.declarer": defaultSpec()}
	}
	consumer := Module()
	if h.withPreferences {
		consumer = preferences.Module()
	}
	modules := []core.Module{sqlite.Module(), log.Module(), catalog.Module(), consumer}
	for name, spec := range specs {
		modules = append(modules, declareType(name, spec))
	}
	if !h.withPreferences {
		modules = append(modules, standIn("stand-in.queue", newStubQueue(), (*jobs.Queue)(nil), (*jobs.Handlers)(nil)))
		modules = append(modules, standIn("stand-in.prefs", newDebugPrefs(), (*notification.Preferences)(nil)))
	}
	if !h.noInbox {
		modules = append(modules, standIn("stand-in.inbox", newStubInbox(), (*notification.Inbox)(nil)))
	}
	if !h.noLocalizer {
		modules = append(modules, standIn("stand-in.localizer", newStubLocalizer(), (*i18n.Localizer)(nil)))
	}
	if h.sms {
		modules = append(modules,
			standIn("stand-in.sms.registry", &stubRegistry{}, (*sms.Registry)(nil)),
			standIn("stand-in.sms.sender", &stubSender{}, (*sms.Sender)(nil)),
		)
	}

	loading := core.New()
	loading.Register(jsonformat.Module())
	loading.Register(filesource.Module())
	loading.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDDELIVERYTEST", DefaultLocator: "file://" + locator}},
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
	if action == nil {
		action = func(context.Context, *core.Registry) error { return nil }
	}
	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action:       action,
	})
}

// rig is an assembled run a case keeps: the product, a real database with the
// ledger table, and every seam stub the module took up, so a case can drive the
// product directly and inspect what reached each seam.
type rig struct {
	t       *testing.T
	p       *pipeline
	db      db.Database
	queue   *stubQueue
	prefs   *stubPrefs
	inbox   *stubInbox
	local   *stubLocalizer
	bus     *stubBus
	catalog tenant.Catalog
	// smsRegistry and smsSender are the sms seam stubs, nil when the run did not
	// take the sms channel in.
	smsRegistry *stubRegistry
	smsSender   *stubSender
}

// newDebugPrefs is the preferences stub a rig keeps, distinct from the throwaway
// one host.prefs builds for a descriptor-only drive.
func newDebugPrefs() *stubPrefs { return &stubPrefs{channels: map[string][]notification.Channel{}} }

// newRig assembles the module over a real database with every seam a case
// drives, and hands back the product and the stubs.
func newRig(t *testing.T, configure func(*host)) *rig {
	t.Helper()
	h := host{}
	if configure != nil {
		configure(&h)
	}
	if h.specs == nil {
		h.specs = map[string]notification.TypeSpec{"test.declarer": defaultSpec()}
	}
	dir := t.TempDir()
	dsn := filepath.Join(dir, "notification.db")
	document := map[string]any{
		"db": map[string]any{"sqlite": map[string]any{"dsn": dsn}},
		"log": map[string]any{"outputs": []any{
			map[string]any{"to": "file", "format": "json", "path": filepath.Join(dir, "log.json")},
		}},
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the configuration: %v", err)
	}
	locator := filepath.Join(dir, "config.json")
	if writeErr := os.WriteFile(locator, body, 0o600); writeErr != nil {
		t.Fatalf("writing the primary config source: %v", writeErr)
	}

	queue := newStubQueue()
	prefs := newDebugPrefs()
	inbox := newStubInbox()
	local := newStubLocalizer()
	bus := &stubBus{}

	modules := []core.Module{sqlite.Module(), log.Module(), catalog.Module(), Module()}
	for name, spec := range h.specs {
		modules = append(modules, declareType(name, spec))
	}
	modules = append(modules,
		standIn("stand-in.queue", queue, (*jobs.Queue)(nil), (*jobs.Handlers)(nil)),
		standIn("stand-in.prefs", prefs, (*notification.Preferences)(nil)),
		standIn("stand-in.inbox", inbox, (*notification.Inbox)(nil)),
		standIn("stand-in.localizer", local, (*i18n.Localizer)(nil)),
		standIn("stand-in.bus", bus, (*event.Bus)(nil)),
	)
	var smsRegistry *stubRegistry
	var smsSender *stubSender
	if h.sms {
		smsRegistry = &stubRegistry{}
		smsSender = &stubSender{}
		modules = append(modules,
			standIn("stand-in.sms.registry", smsRegistry, (*sms.Registry)(nil)),
			standIn("stand-in.sms.sender", smsSender, (*sms.Sender)(nil)),
		)
	}

	loading := core.New()
	loading.Register(jsonformat.Module())
	loading.Register(filesource.Module())
	loading.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDDELIVERYTEST", DefaultLocator: "file://" + locator}},
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

	r := &rig{
		t: t, queue: queue, prefs: prefs, inbox: inbox, local: local, bus: bus,
		smsRegistry: smsRegistry, smsSender: smsSender,
	}

	// The drive is held open for the whole case: the database module closes the
	// handle it opened once the drive returns, so a case that asserted after the
	// drive would be reading a closed pool. The action blocks until the case is
	// done, and the cleanup releases it.
	delivered := make(chan struct{}, 1)
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- reg.Drive(context.Background(), core.Drive{
			Depth:        core.StageStart,
			Participants: names,
			Action: func(_ context.Context, reg *core.Registry) error {
				database, err := core.Resolve[db.Database](reg)
				if err != nil {
					return err
				}
				cam, err := core.Resolve[tenant.Catalog](reg)
				if err != nil {
					return err
				}
				dispatcher, err := core.Resolve[notification.Dispatcher](reg)
				if err != nil {
					return err
				}
				p, ok := dispatcher.(*pipeline)
				if !ok {
					return fmt.Errorf("the delivered dispatcher is a %T, not this module's product", dispatcher)
				}
				r.db, r.catalog, r.p = database, cam, p
				delivered <- struct{}{}
				<-release
				return nil
			},
		})
	}()
	select {
	case <-delivered:
		t.Cleanup(func() {
			close(release)
			if err := <-done; err != nil {
				t.Errorf("shutting the assembly down: %v", err)
			}
		})
		return r
	case err := <-done:
		if err == nil {
			err = fmt.Errorf("the drive ended without delivering the dispatcher")
		}
		t.Fatalf("driving the assembly: %v", err)
		return nil
	}
}

// tenantCtx registers a tenant in the roster and returns a context that belongs
// to it.
func (r *rig) tenantCtx(id string) context.Context {
	r.t.Helper()
	if err := r.catalog.Put(r.t.Context(), tenant.Tenant{ID: tenant.ID(id), Alias: id, Status: tenant.StatusActive}); err != nil {
		r.t.Fatalf("registering the tenant %q: %v", id, err)
	}
	ctx, err := r.catalog.Enter(r.t.Context(), tenant.ID(id))
	if err != nil {
		r.t.Fatalf("entering the tenant %q: %v", id, err)
	}
	return ctx
}

// ledgerRow is one row read back from the ledger past every filter.
type ledgerRow struct {
	channel string
	status  string
	err     string
	receipt string
	key     string
}

// rows reads every ledger row of a tenant, past the statement filter, ordered by
// channel. Reading raw SQL is deliberate: it holds what actually landed rather
// than what a scoped read would report.
func (r *rig) rows(tenantID string) []ledgerRow {
	r.t.Helper()
	var found []struct {
		Channel     string  `gorm:"column:channel"`
		Status      string  `gorm:"column:status"`
		Err         *string `gorm:"column:error"`
		Receipt     *string `gorm:"column:provider_receipt_id"`
		Idempotency string  `gorm:"column:idempotency_key"`
	}
	err := r.db.DB().WithContext(r.t.Context()).Raw(
		"SELECT channel, status, error, provider_receipt_id, idempotency_key FROM "+tableName+
			" WHERE tenant_id = ? ORDER BY channel", tenantID).Scan(&found).Error
	if err != nil {
		r.t.Fatalf("reading the ledger rows: %v", err)
	}
	out := make([]ledgerRow, 0, len(found))
	for _, row := range found {
		entry := ledgerRow{channel: row.Channel, status: row.Status, key: row.Idempotency}
		if row.Err != nil {
			entry.err = *row.Err
		}
		if row.Receipt != nil {
			entry.receipt = *row.Receipt
		}
		out = append(out, entry)
	}
	return out
}

// rowCount is how many rows the ledger holds for a tenant.
func (r *rig) rowCount(tenantID string) int {
	r.t.Helper()
	return len(r.rows(tenantID))
}

// dispatch is one delivery intent a case builds and hands to the seam.
func dispatch(userID, occurrence string, params map[string]any) notification.Dispatch {
	return notification.Dispatch{
		TypeKey:      typeName,
		Recipient:    notification.Recipient{Class: notification.RecipientUser, UserID: userID},
		Locale:       "zh-CN",
		Params:       params,
		OccurrenceID: occurrence,
	}
}

// jobOf wraps a dispatch in a delivery job the handler consumes.
func jobOf(t *testing.T, d notification.Dispatch) *jobs.Job {
	t.Helper()
	payload, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("encoding the dispatch: %v", err)
	}
	return &jobs.Job{ID: "job", Type: JobType, Payload: payload}
}

// stubRegistry and stubSender are the sms seams a case drives when it turns the
// sms channel on. The registry resolves every message to a code and the sender
// records every send.
type stubRegistry struct {
	mu         sync.Mutex
	resolveErr error
}

func (s *stubRegistry) Declared() []sms.Template { return nil }

func (s *stubRegistry) Resolve(m sms.Message) (sms.Resolved, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resolveErr != nil {
		return sms.Resolved{}, s.resolveErr
	}
	return sms.Resolved{To: m.To, Template: m.Template, Locale: m.Locale, Code: "code", Params: m.Params, DedupeID: m.DedupeID}, nil
}

func (s *stubRegistry) List(context.Context) ([]sms.Record, error) { return nil, nil }

func (s *stubRegistry) Register(context.Context, string, sms.TemplateSpec) (sms.Record, error) {
	return sms.Record{}, nil
}

func (s *stubRegistry) Offer(context.Context, sms.Submission) (sms.Record, error) {
	return sms.Record{}, nil
}

func (s *stubRegistry) SetEnabled(context.Context, string, bool) error { return nil }

func (s *stubRegistry) Remove(context.Context, string) error { return nil }

type stubSender struct {
	mu      sync.Mutex
	sends   int
	sendErr error
}

func (s *stubSender) Name() string { return "stub" }

func (s *stubSender) Send(_ context.Context, _ sms.Resolved) (sms.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sends++
	if s.sendErr != nil {
		return sms.Receipt{}, s.sendErr
	}
	return sms.Receipt{ProviderID: "receipt-1"}, nil
}
