package session

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/authn/internal/verdict"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/settings"
)

// epoch is where every fixture's clock starts. It is on a whole second so that a
// time written to PostgreSQL, which keeps microseconds, reads back equal.
var epoch = time.Date(2026, time.October, 11, 8, 0, 0, 0, time.UTC)

// fakeClock is a clock a test moves by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: epoch} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeAccounts is an account store a test writes by hand.
type fakeAccounts struct {
	mu       sync.Mutex
	accounts map[authn.AccountID]authn.Account
	err      error
	gets     atomic.Int64
	// onGet, when set, runs on each Get after the lookup, so that a case can
	// make something happen between a read of the session and its use.
	onGet func()
}

func newFakeAccounts() *fakeAccounts {
	return &fakeAccounts{accounts: make(map[authn.AccountID]authn.Account)}
}

// put adds or replaces an account.
func (f *fakeAccounts) put(a authn.Account) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accounts[a.ID] = a.Clone()
}

// add makes an enabled user account.
func (f *fakeAccounts) add(id authn.AccountID, attrs map[string]string) authn.Account {
	a := authn.Account{ID: id, Kind: authn.KindUser, Status: authn.StatusActive, Attributes: attrs, CreatedAt: epoch.Add(-time.Hour)}
	f.put(a)
	return a
}

// update changes an account in place.
func (f *fakeAccounts) update(id authn.AccountID, fn func(*authn.Account)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.accounts[id]
	fn(&a)
	f.accounts[id] = a
}

func (f *fakeAccounts) remove(id authn.AccountID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.accounts, id)
}

func (f *fakeAccounts) hook(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onGet = fn
}

func (f *fakeAccounts) failWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeAccounts) Get(_ context.Context, id authn.AccountID) (authn.Account, error) {
	f.gets.Add(1)
	a, err := f.lookup(id)
	f.mu.Lock()
	hook := f.onGet
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return a, err
}

func (f *fakeAccounts) lookup(id authn.AccountID) (authn.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return authn.Account{}, f.err
	}
	a, ok := f.accounts[id]
	if !ok {
		return authn.Account{}, authn.ErrAccountNotFound
	}
	return a.Clone(), nil
}

func (*fakeAccounts) Lookup(context.Context, authn.IdentifierKind, string) (authn.AccountID, error) {
	return "", authn.ErrAccountNotFound
}

func (*fakeAccounts) ByExternal(context.Context, string, string) (authn.AccountID, error) {
	return "", authn.ErrAccountNotFound
}

func (*fakeAccounts) Identifiers(context.Context, authn.AccountID) ([]authn.Identifier, error) {
	return nil, nil
}

func (*fakeAccounts) IdentifiersMany(context.Context, []authn.AccountID) (map[authn.AccountID][]authn.Identifier, error) {
	return nil, nil
}

func (*fakeAccounts) Externals(context.Context, authn.AccountID) ([]authn.ExternalBinding, error) {
	return nil, nil
}

func (*fakeAccounts) List(context.Context, authn.AccountQuery) ([]authn.Account, string, error) {
	return nil, "", nil
}

func (*fakeAccounts) ListServiceSecrets(context.Context, authn.AccountID) ([]authn.ServiceSecretInfo, error) {
	return nil, nil
}

// fakeSettings is a settings store holding the values a test wrote, and the
// declared default for anything else.
type fakeSettings struct {
	mu     sync.Mutex
	values map[string]any
	reads  atomic.Int64
	err    error
}

func newFakeSettings() *fakeSettings { return &fakeSettings{values: make(map[string]any)} }

func (s *fakeSettings) set(path string, v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[path] = v
}

func (s *fakeSettings) failWith(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *fakeSettings) Get(_ settings.Layer, key string, target any) error {
	s.reads.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	v, ok := s.values[key]
	if !ok {
		v = declaredDefault(key)
	}
	dst := reflect.ValueOf(target).Elem()
	src := reflect.ValueOf(v)
	if !src.Type().AssignableTo(dst.Type()) {
		return settings.ErrTypeMismatch
	}
	dst.Set(src)
	return nil
}

func (*fakeSettings) Set(context.Context, settings.Write) error { return errors.New("not supported") }

func (*fakeSettings) Delete(context.Context, settings.Layer, string, string) error {
	return errors.New("not supported")
}

func (*fakeSettings) List(context.Context, settings.Layer) ([]settings.Entry, error) { return nil, nil }
func (*fakeSettings) Declared() []settings.DeclaredItem                              { return nil }

func (*fakeSettings) WrittenAt(settings.Layer, string) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func (s *fakeSettings) Read(key string, reads ...settings.LayerRead) ([]settings.LayerReadResult, error) {
	results := make([]settings.LayerReadResult, len(reads))
	for i, read := range reads {
		if err := s.Get(read.Layer, key, read.Target); err != nil {
			return nil, err
		}
		results[i] = settings.LayerReadResult{}
	}
	return results, nil
}

// declaredDefault is the declared default of a setting path.
func declaredDefault(path string) any {
	switch path {
	case idleTimeoutPath:
		return defaults.IdleTimeout
	case absoluteTimeoutPath:
		return defaults.AbsoluteTimeout
	case tokenAbsolutePath:
		return defaults.TokenAbsoluteTimeout
	case ssoAbsolutePath:
		return defaults.SSOAbsoluteTimeout
	case touchIntervalPath:
		return defaults.TouchInterval
	case checkTTLPath:
		return defaults.CheckTTL
	case maxPerAccountPath:
		return defaults.MaxPerAccount
	case retentionPath:
		return defaults.Retention
	}
	return nil
}

// fakeImpersonation answers Active from a table a test writes.
type fakeImpersonation struct {
	mu     sync.Mutex
	grants map[string]authn.ImpersonationGrant
	err    error
	calls  atomic.Int64
}

func newFakeImpersonation() *fakeImpersonation {
	return &fakeImpersonation{grants: make(map[string]authn.ImpersonationGrant)}
}

func (f *fakeImpersonation) grant(g authn.ImpersonationGrant) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grants[g.ID] = g
}

func (f *fakeImpersonation) end(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.grants[id]
	g.EndedAt = epoch
	f.grants[id] = g
}

func (f *fakeImpersonation) failWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeImpersonation) Active(_ context.Context, id string) (authn.ImpersonationGrant, bool, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return authn.ImpersonationGrant{}, false, f.err
	}
	g, ok := f.grants[id]
	return g, ok && g.EndedAt.IsZero(), nil
}

func (*fakeImpersonation) Start(context.Context, authn.Identity, authn.ImpersonationRequest) (authn.ImpersonationGrant, error) {
	return authn.ImpersonationGrant{}, errors.New("not supported")
}

func (*fakeImpersonation) End(context.Context, authn.Identity, string) error {
	return errors.New("not supported")
}

func (*fakeImpersonation) Revoke(context.Context, string, authn.Identity, string) error {
	return errors.New("not supported")
}

func (*fakeImpersonation) List(context.Context, authn.GrantQuery) ([]authn.ImpersonationGrant, string, error) {
	return nil, "", nil
}

// recordingBus is an in-process bus that delivers on the publisher's goroutine
// and keeps what was published.
type recordingBus struct {
	mu       sync.Mutex
	handlers map[string][]event.Handler
	events   []event.Event
	err      error
}

func newRecordingBus() *recordingBus {
	return &recordingBus{handlers: make(map[string][]event.Handler)}
}

func (b *recordingBus) Subscribe(eventType string, _ string, h event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

func (b *recordingBus) Publish(ctx context.Context, evt event.Event) error {
	b.mu.Lock()
	if b.err != nil {
		err := b.err
		b.mu.Unlock()
		return err
	}
	if evt.ID == "" {
		evt.ID = event.NewID()
	}
	b.events = append(b.events, evt)
	hs := append([]event.Handler(nil), b.handlers[evt.Type]...)
	b.mu.Unlock()
	for _, h := range hs {
		_ = h(ctx, evt)
	}
	return nil
}

// deliver hands an event to the subscribers without recording it, as a replica
// that did not publish it would receive it.
func (b *recordingBus) deliver(evt event.Event) {
	b.mu.Lock()
	hs := append([]event.Handler(nil), b.handlers[evt.Type]...)
	b.mu.Unlock()
	for _, h := range hs {
		_ = h(context.Background(), evt)
	}
}

func (b *recordingBus) published(eventType string) []event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []event.Event
	for _, e := range b.events {
		if e.Type == eventType {
			out = append(out, e)
		}
	}
	return out
}

func (b *recordingBus) failWith(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.err = err
}

// sqlRecorder is a gorm logger that records the statements run through it.
type sqlRecorder struct {
	mu   sync.Mutex
	sqls []string
}

func (*sqlRecorder) LogMode(gormlogger.LogLevel) gormlogger.Interface { return &sqlRecorder{} }
func (*sqlRecorder) Info(context.Context, string, ...any)             {}
func (*sqlRecorder) Warn(context.Context, string, ...any)             {}
func (*sqlRecorder) Error(context.Context, string, ...any)            {}

func (r *sqlRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sqls = append(r.sqls, sql)
}

// count is how many recorded statements start with the prefix.
func (r *sqlRecorder) count(prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.sqls {
		if strings.HasPrefix(strings.TrimSpace(s), prefix) {
			n++
		}
	}
	return n
}

func (r *sqlRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sqls = nil
}

// logCapture is a log handler's output.
type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logCapture) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(b)
}

func (l *logCapture) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func (l *logCapture) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(l, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// dialectCase is one database dialect a case runs over.
type dialectCase struct {
	name    string
	dialect db.Dialect
	open    func(t *testing.T, dsn string) db.Database
	// newDSN is a locator for an empty database of the test's own.
	newDSN func(t *testing.T) string
}

// dialects lists the engines a case runs over, SQLite first.
//
// Only the engines whose fixture needs nothing installed belong here. The
// integration build appends the ones that reach a server, so an untagged run
// cannot name a dialect whose opener would fail for want of one.
var dialects = []dialectCase{
	{
		name:    "sqlite",
		dialect: db.SQLite,
		open:    func(t *testing.T, dsn string) db.Database { return dbtest.SQLite(t, dsn, nil) },
		newDSN: func(t *testing.T) string {
			return filepath.Join(t.TempDir(), "sessions.db") + "?_pragma=busy_timeout(5000)"
		},
	},
}

// forEachDialect runs a case over every dialect.
func forEachDialect(t *testing.T, run func(t *testing.T, d dialectCase)) {
	t.Helper()
	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) { run(t, d) })
	}
}

// fixture is a product over a real database with every collaborator a test
// moves by hand.
type fixture struct {
	t        *testing.T
	d        dialectCase
	dsn      string
	database db.Database
	p        *product
	clock    *fakeClock
	accounts *fakeAccounts
	settings *fakeSettings
	imp      *fakeImpersonation
	bus      *recordingBus
	mounter  *mounttest.Mounter
	sql      *sqlRecorder
	logs     *logCapture
}

// option adjusts a fixture before it is built.
type option func(*fixtureSpec)

type fixtureSpec struct {
	cookie  *cookieSpec
	noBus   bool
	noImp   bool
	noStore bool
	// noMigration leaves the table uncreated.
	noMigration bool
	shareWith   *fixture
}

func withCookie(c cookieSpec) option { return func(s *fixtureSpec) { s.cookie = &c } }
func withoutBus() option             { return func(s *fixtureSpec) { s.noBus = true } }
func withoutImpersonation() option   { return func(s *fixtureSpec) { s.noImp = true } }
func withoutSettings() option        { return func(s *fixtureSpec) { s.noStore = true } }
func withoutMigration() option       { return func(s *fixtureSpec) { s.noMigration = true } }

// sharingDatabaseWith makes the fixture another replica over the same database
// as other, with its own caches and its own bus.
func sharingDatabaseWith(other *fixture) option {
	return func(s *fixtureSpec) { s.shareWith = other }
}

// defaultCookie is the cookie a deployment on an https address gets.
func defaultCookie() cookieSpec {
	return cookieSpec{name: hostCookieName, sameSite: http.SameSiteLaxMode, secure: true}
}

func newFixture(t *testing.T, d dialectCase, opts ...option) *fixture {
	t.Helper()
	var spec fixtureSpec
	for _, o := range opts {
		o(&spec)
	}
	f := &fixture{
		t: t, d: d,
		clock:    newFakeClock(),
		accounts: newFakeAccounts(),
		settings: newFakeSettings(),
		imp:      newFakeImpersonation(),
		bus:      newRecordingBus(),
		sql:      &sqlRecorder{},
		logs:     &logCapture{},
	}
	if spec.shareWith != nil {
		f.dsn = spec.shareWith.dsn
		f.clock = spec.shareWith.clock
		f.accounts = spec.shareWith.accounts
		f.settings = spec.shareWith.settings
		f.imp = spec.shareWith.imp
	} else {
		f.dsn = d.newDSN(t)
	}
	f.database = d.open(t, f.dsn)
	handle := f.database.DB().Session(&gorm.Session{Logger: f.sql})
	if spec.shareWith == nil && !spec.noMigration {
		applyMigration(t, handle, d)
	}

	cookie := defaultCookie()
	if spec.cookie != nil {
		cookie = *spec.cookie
	}
	f.mounter = mounttest.New(t)
	f.p = newProduct(cookie, handle, f.accounts, f.mounter)
	f.p.clock = f.clock.Now
	f.p.logger = f.logs.logger()
	f.p.settings.logger = f.p.logger
	if !spec.noStore {
		f.p.settings.store = f.settings
	}
	if !spec.noImp {
		f.p.impersonation = f.imp
	}
	if !spec.noBus {
		f.p.bus = f.bus
	}
	f.p.subscribe()
	f.p.mountRoutes()
	return f
}

// applyMigration creates the table from the dialect's migration.
func applyMigration(t *testing.T, handle *gorm.DB, d dialectCase) {
	t.Helper()
	body, err := fs.ReadFile(migrationsFS, string(d.dialect)+"/0001_authn_sessions.sql")
	if err != nil {
		t.Fatalf("reading the migration: %v", err)
	}
	for statement := range strings.SplitSeq(string(body), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("applying the migration: %v", err)
		}
	}
}

// the accounts the cases use.
const (
	alice authn.AccountID = "acct-alice"
	bob   authn.AccountID = "acct-bob"
	admin authn.AccountID = "acct-admin"
)

// provider is an SSO provider identifier in the form the gateway admits.
const provider = "0198a3b4-7c5d-7e6f-8a9b-0c1d2e3f4a5b"

// newBrowser is a browser session request that passes the checks.
func newBrowser(account authn.AccountID) authn.NewSession {
	return authn.NewSession{
		Account:   account,
		Kind:      authn.SessionBrowser,
		Methods:   []authn.Method{authn.MethodPassword},
		Assurance: authn.AssuranceSingle,
		AuthTime:  epoch,
		Client:    authn.ClientInfo{IP: "192.0.2.7", UserAgent: "test-agent"},
	}
}

func newToken1(account authn.AccountID) authn.NewSession {
	in := newBrowser(account)
	in.Kind = authn.SessionToken
	return in
}

// create makes a session and fails the test when it cannot.
func (f *fixture) create(in authn.NewSession) authn.Issued {
	f.t.Helper()
	issued, err := f.p.Create(context.Background(), in)
	if err != nil {
		f.t.Fatalf("creating a session: %v", err)
	}
	return issued
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// cookieOf is the credential a request carries after a browser session was made.
func (f *fixture) cookieOf(token string) authn.Credential {
	return authn.Credential{Cookie: f.p.cookie.name, Value: token, Peer: "192.0.2.7:1234"}
}

// verify runs the verifying backend on a token.
func (f *fixture) verify(token string) authn.Verdict {
	return f.p.Verify(context.Background(), f.cookieOf(token))
}

// row reads a row straight from the table.
func (f *fixture) row(id authn.SessionID) sessionRow {
	f.t.Helper()
	r, err := f.p.byID(context.Background(), id)
	if err != nil {
		f.t.Fatalf("reading the row %s: %v", id, err)
	}
	return r
}

// count is how many rows the table holds for an account.
func (f *fixture) count(account authn.AccountID) int64 {
	f.t.Helper()
	var n int64
	if err := f.p.handle.Raw("SELECT COUNT(*) FROM "+tableName+" WHERE account_id = ?", string(account)).Scan(&n).Error; err != nil {
		f.t.Fatalf("counting rows: %v", err)
	}
	return n
}

// replica is another product over the same database, as a second process would be.
func (f *fixture) replica(opts ...option) *fixture {
	return newFixture(f.t, f.d, append(opts, sharingDatabaseWith(f))...)
}

// outcomeOf, principalOf and causeOf read a verdict, which the root package
// leaves unreadable to a backend and open to this capability's own code.
func outcomeOf(v authn.Verdict) verdict.Outcome   { return verdict.OutcomeOf(v) }
func principalOf(v authn.Verdict) authn.Principal { return verdict.PrincipalOf(v) }
func causeOf(v authn.Verdict) error               { return verdict.CauseOf(v) }

// exec runs a statement straight against the table.
func (f *fixture) exec(statement string, args ...any) {
	f.t.Helper()
	if err := f.p.handle.Exec(statement, args...).Error; err != nil {
		f.t.Fatalf("running %q: %v", statement, err)
	}
}

// status reads the status of a session, which must exist.
func (f *fixture) status(id authn.SessionID) authn.SessionStatus {
	f.t.Helper()
	st, err := f.p.Status(context.Background(), id)
	if err != nil {
		f.t.Fatalf("reading the status of %s: %v", id, err)
	}
	return st
}

// disable moves an account to disabled the way the account module does: the
// status and the instant after which sessions are void change together.
func (f *fixture) disable(id authn.AccountID) {
	f.accounts.update(id, func(a *authn.Account) {
		a.Status = authn.StatusDisabled
		a.ValidAfter = f.clock.Now()
	})
}

// enable moves an account back to active. The instant after which sessions are
// void stays where the disabling put it.
func (f *fixture) enable(id authn.AccountID) {
	f.accounts.update(id, func(a *authn.Account) { a.Status = authn.StatusActive })
}

// testRequest is a request a case sends, with the session cookie it carries.
type testRequest struct{ r *http.Request }

// newRequest is a request carrying a cookie of the given name; an empty name
// carries none.
func newRequest(name, value string) *testRequest {
	r, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://app.example.com/", nil)
	if name != "" {
		r.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	return &testRequest{r: r}
}
