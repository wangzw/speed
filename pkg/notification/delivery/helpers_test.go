package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/kv"
	kvmemory "github.com/vislake/speed/pkg/kv/memory"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/mail"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/notification/preferences"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/sms/budget"
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
	_ mail.Sender              = (*stubMailSender)(nil)
	_ authn.Accounts           = (*stubAccounts)(nil)
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

// stubMailSender is a stand-in mail seam. It records every message it was
// handed, so a case can hold what reached the carrier: which fields the module
// filled, which it deliberately left empty, and how many sends one delivery made.
type stubMailSender struct {
	mu      sync.Mutex
	sends   int
	sendErr error
	sent    []mail.Message
}

func (s *stubMailSender) Name() string { return "stub" }

func (s *stubMailSender) Send(_ context.Context, m mail.Message) (mail.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sends++
	if s.sendErr != nil {
		return mail.Receipt{}, s.sendErr
	}
	s.sent = append(s.sent, m)
	return mail.Receipt{ProviderID: "receipt-1"}, nil
}

// failWith makes every later send answer the given error.
func (s *stubMailSender) failWith(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendErr = err
}

// messages lists the messages the carrier accepted, in order.
func (s *stubMailSender) messages() []mail.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mail.Message(nil), s.sent...)
}

// sendCount is how many times a case reached the carrier, including the calls
// that failed.
func (s *stubMailSender) sendCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sends
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

func (b *stubBus) Subscribe(string, string, event.Handler) {}

func (b *stubBus) events() []event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]event.Event(nil), b.published...)
}

// stubAccounts is a stand-in account store. It answers the two reads the
// pipeline makes — the recipient's identifiers and the owner of a destination —
// and leaves every other method of the interface unused. An account an
// identifier is asked about that was never bound reads as absent, the way the
// real store reports an unknown or tombstoned account.
type stubAccounts struct {
	mu      sync.Mutex
	byID    map[authn.AccountID][]authn.Identifier
	idsErr  error
	lookErr error
}

func newStubAccounts() *stubAccounts {
	return &stubAccounts{byID: map[authn.AccountID][]authn.Identifier{}}
}

// register records a live account that carries no identifier.
func (s *stubAccounts) register(id authn.AccountID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[id]; !ok {
		s.byID[id] = nil
	}
}

// bind records one identifier for an account. A value is stored normalized, the
// way the account store keeps it.
func (s *stubAccounts) bind(id authn.AccountID, kind authn.IdentifierKind, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[id]; !ok {
		s.byID[id] = nil
	}
	s.byID[id] = append(s.byID[id], authn.Identifier{
		ID:    string(id) + ":" + value,
		Kind:  kind,
		Value: value,
	})
}

func (s *stubAccounts) Identifiers(_ context.Context, id authn.AccountID) ([]authn.Identifier, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idsErr != nil {
		return nil, s.idsErr
	}
	ids, ok := s.byID[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", authn.ErrAccountNotFound, id)
	}
	return append([]authn.Identifier(nil), ids...), nil
}

func (s *stubAccounts) Lookup(_ context.Context, kind authn.IdentifierKind, value string) (authn.AccountID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lookErr != nil {
		return "", s.lookErr
	}
	normalized, err := stubNormalize(kind, value)
	if err != nil {
		return "", err
	}
	for id, ids := range s.byID {
		for _, identifier := range ids {
			if identifier.Kind == kind && identifier.Value == normalized {
				return id, nil
			}
		}
	}
	return "", fmt.Errorf("%w: the %s identifier belongs to no account", authn.ErrAccountNotFound, kind)
}

// stubNormalize is the account store's normalization for the two kinds an
// external channel addresses: an email address is trimmed and lower-cased, and a
// phone number must already be E.164. It mirrors the shapes the store writes so
// a case can hold that the pipeline compares normalized values.
func stubNormalize(kind authn.IdentifierKind, value string) (string, error) {
	switch kind {
	case authn.IdentifierEmail:
		normalized := strings.ToLower(strings.TrimSpace(value))
		if !strings.Contains(normalized, "@") {
			return "", fmt.Errorf("%w: an email address holds an @", authn.ErrInvalidIdentifier)
		}
		return normalized, nil
	case authn.IdentifierPhone:
		if !strings.HasPrefix(value, "+") || len(value) < 8 {
			return "", fmt.Errorf("%w: a phone number is E.164", authn.ErrInvalidIdentifier)
		}
		return value, nil
	}
	return "", fmt.Errorf("%w: the kind is not one of the two an external channel addresses", authn.ErrInvalidIdentifier)
}

func (s *stubAccounts) Get(context.Context, authn.AccountID) (authn.Account, error) {
	return authn.Account{}, authn.ErrAccountNotFound
}

func (s *stubAccounts) ByExternal(context.Context, string, string) (authn.AccountID, error) {
	return "", authn.ErrAccountNotFound
}

func (s *stubAccounts) IdentifiersMany(context.Context, []authn.AccountID) (map[authn.AccountID][]authn.Identifier, error) {
	return nil, nil
}

func (s *stubAccounts) Externals(context.Context, authn.AccountID) ([]authn.ExternalBinding, error) {
	return nil, nil
}

func (s *stubAccounts) List(context.Context, authn.AccountQuery) ([]authn.Account, string, error) {
	return nil, "", nil
}

func (s *stubAccounts) ListServiceSecrets(context.Context, authn.AccountID) ([]authn.ServiceSecretInfo, error) {
	return nil, nil
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

// logBuffer collects what the module logs, as text lines, so a case can read the
// level and the fields of a line and look for what must not be in any of them.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *logBuffer) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// lines returns the lines that carry every one of the substrings.
func (b *logBuffer) lines(substrings ...string) []string {
	var out []string
	for _, line := range strings.Split(b.String(), "\n") {
		ok := line != ""
		for _, s := range substrings {
			ok = ok && strings.Contains(line, s)
		}
		if ok {
			out = append(out, line)
		}
	}
	return out
}

// bufferLogger is a logging capability whose Named logger writes into buf with
// the module's name bound. A case uses it to read what the module reported
// through its own logger, without a configured destination and without the
// logger a context carries.
type bufferLogger struct{ buf *logBuffer }

func (l bufferLogger) Named(name string) *slog.Logger {
	return l.buf.logger().With("module", name)
}

func (bufferLogger) Redaction() log.Redaction { return noRedaction{} }

// host is one assembled run: the delivery module over a real SQLite database and
// the tenant roster, with the seams a case drives stood in for and the type
// declarations a case wants.
type host struct {
	// specs are extra type declarations, each by its own module.
	specs map[string]notification.TypeSpec
	// mail adds the mail seam when set.
	mail bool
	// sms adds the two sms seams when set.
	sms bool
	// noInbox and noLocalizer drop a required seam, which fails resolution.
	noInbox, noLocalizer bool
	// withPreferences drives the taxonomy's other consumption point —
	// notification.preferences — instead of this module, over the same
	// declarations, so a case can hold both points to one criterion.
	withPreferences bool
	// logger replaces the real logging module with a stand-in delivering this
	// capability, so a case can read what the module writes without going
	// through a configured destination.
	logger log.Logger
	// meter delivers the metric capability when set, so a case can read the
	// utilization gauge the module reports.
	meter observability.MeterProvider
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
	modules := []core.Module{sqlite.Module(), log.Module(), catalog.Module(), kvmemory.Module(), budget.Module(), consumer}
	for name, spec := range specs {
		modules = append(modules, declareType(name, spec))
	}
	if !h.withPreferences {
		modules = append(modules, standIn("stand-in.queue", newStubQueue(), (*jobs.Queue)(nil), (*jobs.Handlers)(nil)))
		modules = append(modules, standIn("stand-in.prefs", newDebugPrefs(), (*notification.Preferences)(nil)))
	}
	modules = append(modules, standIn("stand-in.accounts", newStubAccounts(), (*authn.Accounts)(nil)))
	if !h.noInbox {
		modules = append(modules, standIn("stand-in.inbox", newStubInbox(), (*notification.Inbox)(nil)))
	}
	if !h.noLocalizer {
		modules = append(modules, standIn("stand-in.localizer", newStubLocalizer(), (*i18n.Localizer)(nil)))
	}
	if h.mail {
		modules = append(modules, standIn("stand-in.mail", &stubMailSender{}, (*mail.Sender)(nil)))
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
	// accounts is the account store stub the ownership rule reads.
	accounts *stubAccounts
	// settings is the settings store the assembly's settings module delivers, so
	// a case can write a budget limit and hold that the pipeline reads it.
	settings settings.Store
	// kv is the key-value capability the assembly delivers. It holds the email
	// budgets; the sms budgets are counted over the shared ledger instead. A case
	// also uses it to hold that a spent counter left there is not consulted by
	// the sms path.
	kv kv.KV
	// mailSender is the mail seam stub, nil when the run did not take the email
	// channel in.
	mailSender *stubMailSender
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
	accounts := newStubAccounts()
	// The default accounts carry the two identifiers a dispatch in these cases
	// addresses, so a destination that names one passes the ownership rule.
	accounts.bind("u1", authn.IdentifierEmail, "u1@example.test")
	accounts.bind("u1", authn.IdentifierPhone, "+15550001")
	accounts.bind("u2", authn.IdentifierEmail, "u2@example.test")

	modules := []core.Module{sqlite.Module(), log.Module(), catalog.Module(), kvmemory.Module(), settings.Module(), budget.Module(), Module()}
	for name, spec := range h.specs {
		modules = append(modules, declareType(name, spec))
	}
	modules = append(modules,
		standIn("stand-in.queue", queue, (*jobs.Queue)(nil), (*jobs.Handlers)(nil)),
		standIn("stand-in.prefs", prefs, (*notification.Preferences)(nil)),
		standIn("stand-in.inbox", inbox, (*notification.Inbox)(nil)),
		standIn("stand-in.localizer", local, (*i18n.Localizer)(nil)),
		standIn("stand-in.bus", bus, (*event.Bus)(nil)),
		standIn("stand-in.accounts", accounts, (*authn.Accounts)(nil)),
	)
	if h.meter != nil {
		modules = append(modules, standIn("stand-in.meter", h.meter, (*observability.MeterProvider)(nil)))
	}
	var mailSender *stubMailSender
	if h.mail {
		mailSender = &stubMailSender{}
		modules = append(modules, standIn("stand-in.mail", mailSender, (*mail.Sender)(nil)))
	}
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

	// The configuration loader above always sees the real logging module, so the
	// document's log section still names a declared item; a case that wants to
	// read the module's own lines replaces the provider in the registry alone.
	registered := modules
	if h.logger != nil {
		registered = make([]core.Module, 0, len(modules))
		for _, m := range modules {
			if m.Name == log.ModuleName {
				registered = append(registered, standIn("stand-in.logging", h.logger, (*log.Logger)(nil)))
				continue
			}
			registered = append(registered, m)
		}
	}

	reg := core.New()
	reg.Register(config.Module())
	names := make([]string, 0, len(registered))
	seen := map[string]bool{}
	for _, m := range registered {
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
		accounts:   accounts,
		mailSender: mailSender, smsRegistry: smsRegistry, smsSender: smsSender,
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
				store, err := core.Resolve[settings.Store](reg)
				if err != nil {
					return err
				}
				counters, err := core.Resolve[kv.KV](reg)
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
				r.db, r.catalog, r.settings, r.kv, r.p = database, cam, store, counters, p
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

// capturingLogger attaches a logger to ctx and returns the buffer that collects
// what the module writes through it, so a case can read the external-send lines.
func (r *rig) capturingLogger(ctx context.Context) (*logBuffer, context.Context) {
	buf := &logBuffer{}
	return buf, log.WithLogger(ctx, buf.logger())
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

// jobOf wraps a dispatch in a delivery job the handler consumes. It carries no
// charged sms attempt, which is the shape a job built by a caller that did not
// go through Dispatch has.
func jobOf(t *testing.T, d notification.Dispatch) *jobs.Job {
	t.Helper()
	payload, err := json.Marshal(deliveryPayload{Dispatch: d})
	if err != nil {
		t.Fatalf("encoding the dispatch: %v", err)
	}
	return &jobs.Job{ID: "job", Type: JobType, Payload: payload}
}

// budgetRow is one row read back from the shared sms ledger.
type budgetRow struct {
	channel     string
	purpose     string
	destination string
	tenant      string
	outcome     string
	refunded    bool
	attemptedAt time.Time
}

// budgetRows reads the shared ledger's rows for a tenant, past every filter,
// ordered by attempt. Reading raw SQL is deliberate: it holds what actually
// landed rather than what a scoped read would report.
func (r *rig) budgetRows(tenantID string) []budgetRow {
	r.t.Helper()
	var found []struct {
		Channel        string     `gorm:"column:channel"`
		Purpose        string     `gorm:"column:purpose"`
		DestinationMAC string     `gorm:"column:destination_mac"`
		TenantID       string     `gorm:"column:tenant_id"`
		Outcome        string     `gorm:"column:outcome"`
		RefundedAt     *time.Time `gorm:"column:refunded_at"`
		AttemptedAt    time.Time  `gorm:"column:attempted_at"`
	}
	err := r.db.DB().WithContext(r.t.Context()).Raw(
		"SELECT channel, purpose, destination_mac, tenant_id, outcome, refunded_at, attempted_at "+
			"FROM sms_budget_events WHERE tenant_id = ? ORDER BY attempted_at", tenantID).Scan(&found).Error
	if err != nil {
		r.t.Fatalf("reading the sms budget rows: %v", err)
	}
	out := make([]budgetRow, 0, len(found))
	for _, row := range found {
		out = append(out, budgetRow{
			channel: row.Channel, purpose: row.Purpose, destination: row.DestinationMAC,
			tenant: row.TenantID, outcome: row.Outcome, refunded: row.RefundedAt != nil,
			attemptedAt: row.AttemptedAt,
		})
	}
	return out
}

// seedLedger writes one attempt straight into the shared ledger, the way the
// other sending channel or an earlier dispatch would have, and answers its
// identifier. A case uses it to fill the cross-channel ceiling without a send.
func (r *rig) seedLedger(t *testing.T, a budget.Attempt) budget.ID {
	t.Helper()
	if a.AttemptedAt.IsZero() {
		a.AttemptedAt = time.Now().UTC()
	}
	id, err := budget.New(r.db.DB()).Record(context.Background(), a)
	if err != nil {
		r.t.Fatalf("seeding the shared ledger: %v", err)
	}
	return id
}

// refundLedger marks one seeded attempt refunded, the way a definitive failure
// does.
func (r *rig) refundLedger(t *testing.T, id budget.ID) {
	t.Helper()
	if err := budget.New(r.db.DB()).MarkRefunded(context.Background(), id, time.Now().UTC()); err != nil {
		r.t.Fatalf("refunding a seeded attempt: %v", err)
	}
}

// ceilingCount reads the cross-channel ceiling over the last hour, which is the
// same window a dispatch is checked against.
func (r *rig) ceilingCount(t *testing.T) int64 {
	t.Helper()
	n, err := budget.New(r.db.DB()).CountCeiling(context.Background(), time.Now().UTC().Add(-time.Hour))
	if err != nil {
		r.t.Fatalf("counting the shared ceiling: %v", err)
	}
	return n
}

// stubBudgetStore is a stand-in ledger with every window empty. A case uses it
// where the assembly cannot carry the real ledger; a test that inspects rows
// drives the real one through newRig.
type stubBudgetStore struct{}

func (stubBudgetStore) Record(context.Context, budget.Attempt) (budget.ID, error) { return 1, nil }
func (stubBudgetStore) MarkRefunded(context.Context, budget.ID, time.Time) error  { return nil }
func (stubBudgetStore) Count(context.Context, budget.Query) (int64, error)        { return 0, nil }
func (stubBudgetStore) CountDistinctDestinations(context.Context, budget.Query) (int64, error) {
	return 0, nil
}

func (stubBudgetStore) Oldest(context.Context, budget.Query) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func (stubBudgetStore) Latest(context.Context, budget.Query) (time.Time, bool, error) {
	return time.Time{}, false, nil
}
func (stubBudgetStore) CountCeiling(context.Context, time.Time) (int64, error) { return 0, nil }
func (stubBudgetStore) PruneBefore(context.Context, time.Time) (int64, error)  { return 0, nil }

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

// failWith makes every later send answer the given error.
func (s *stubSender) failWith(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendErr = err
}

// sendCount is how many times a case reached the sms carrier, including the
// calls that failed.
func (s *stubSender) sendCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sends
}
