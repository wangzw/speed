package invite

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
)

// quietly is a session of a delivered handle that writes no record of a failed
// statement, several of which these cases fail on purpose.
func quietly(handle *gorm.DB) *gorm.DB {
	return handle.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
}

// openDatabase hands back the handle the engine's own module delivers over a
// SQLite database of this test's own, with this module's migrations applied.
func openDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	handle := quietly(dbtest.SQLite(t, filepath.Join(t.TempDir(), "invite.db"), nil).DB())
	// One connection, so that SQLite serializes the writes and a case that
	// races two acceptances sees one winner rather than a busy database.
	if pool, err := handle.DB(); err == nil {
		pool.SetMaxOpenConns(1)
	}
	applyMigrations(t, handle)
	return handle
}

// applyMigrations runs every migration file this module ships for SQLite, in
// the order the database module applies them, a statement at a time.
func applyMigrations(t testing.TB, handle *gorm.DB) {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, string(db.SQLite))
	if err != nil {
		t.Fatalf("listing the migrations: %v", err)
	}
	for _, entry := range entries {
		text, err := fs.ReadFile(migrationsFS, string(db.SQLite)+"/"+entry.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		for statement := range strings.SplitSeq(string(text), ";") {
			if strings.TrimSpace(statement) == "" {
				continue
			}
			if err := handle.Exec(statement).Error; err != nil {
				t.Fatalf("applying %s: %v", entry.Name(), err)
			}
		}
	}
}

// testClock is a clock a case moves by hand.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2031, 3, 4, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) read() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// logBuffer collects what the module logs.
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

// fakeAccounts is the account read surface. Only Lookup is exercised; the rest
// of the interface is embedded nil, which a case must not reach.
type fakeAccounts struct {
	authn.Accounts
	mu       sync.Mutex
	byEmail  map[string]authn.AccountID
	lookupEr error
}

func newAccounts() *fakeAccounts {
	return &fakeAccounts{byEmail: map[string]authn.AccountID{}}
}

func (a *fakeAccounts) put(email string, id authn.AccountID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.byEmail[email] = id
}

func (a *fakeAccounts) Lookup(_ context.Context, _ authn.IdentifierKind, value string) (authn.AccountID, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lookupEr != nil {
		return "", a.lookupEr
	}
	id, ok := a.byEmail[value]
	if !ok {
		return "", authn.ErrAccountNotFound
	}
	return id, nil
}

// fakeAccountAdmin is the account write surface, counting each call and
// recording the input it was given.
type fakeAccountAdmin struct {
	authn.AccountAdmin
	mu      sync.Mutex
	creates int
	created authn.CreateAccount
	newID   authn.AccountID
	err     error
}

func (a *fakeAccountAdmin) Create(_ context.Context, in authn.CreateAccount) (authn.Account, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.creates++
	a.created = in
	if a.err != nil {
		return authn.Account{}, a.err
	}
	id := a.newID
	if id == "" {
		id = "acct-created"
	}
	return authn.Account{ID: id, Kind: authn.KindUser, Status: authn.StatusActive}, nil
}

// fakeMemberships is the membership write surface.
type fakeMemberships struct {
	authn.Memberships
	mu    sync.Mutex
	puts  []authn.Membership
	count int
	err   error
}

func (f *fakeMemberships) Put(_ context.Context, m authn.Membership) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.puts = append(f.puts, m)
	f.count++
	return nil
}

// recordingAudit keeps the rows it was asked to write and can be told to fail.
type recordingAudit struct {
	mu   sync.Mutex
	rows []audit.Entry
	err  error
}

func (a *recordingAudit) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.rows = append(a.rows, e)
	return nil
}

func (a *recordingAudit) entries() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.rows...)
}

func (a *recordingAudit) fails(err error) { a.mu.Lock(); a.err = err; a.mu.Unlock() }

// fakeDecider answers what a case scripts and counts its calls.
type fakeDecider struct {
	mu    sync.Mutex
	allow bool
	err   error
	calls []authn.Decision
}

func (d *fakeDecider) Decide(_ context.Context, dec authn.Decision) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, dec)
	return d.allow, d.err
}

func (d *fakeDecider) count() int { d.mu.Lock(); defer d.mu.Unlock(); return len(d.calls) }

func (d *fakeDecider) last() (authn.Decision, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.calls) == 0 {
		return authn.Decision{}, false
	}
	return d.calls[len(d.calls)-1], true
}

// harness is the module's product over a migrated SQLite database, with the
// capabilities replaced by ones a case can read.
type harness struct {
	t        *testing.T
	p        *product
	handle   *gorm.DB
	clock    *testClock
	accounts *fakeAccounts
	admin    *fakeAccountAdmin
	members  *fakeMemberships
	audit    *recordingAudit
	decider  *fakeDecider
	mounter  *mounttest.Mounter
	logs     *logBuffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	handle := openDatabase(t)
	h := &harness{
		t:        t,
		handle:   handle,
		clock:    newTestClock(),
		accounts: newAccounts(),
		admin:    &fakeAccountAdmin{},
		members:  &fakeMemberships{},
		audit:    &recordingAudit{},
		decider:  &fakeDecider{allow: true},
		mounter:  mounttest.New(t),
		logs:     &logBuffer{},
	}
	p := &product{
		handle:      handle,
		accounts:    h.accounts,
		accountAdm:  h.admin,
		memberships: h.members,
		mounter:     h.mounter,
		audit:       h.audit,
		decider:     h.decider,
		logger:      h.logs.logger(),
	}
	p.now = h.clock.read
	h.p = p
	p.mountRoutes()
	return h
}

// issue issues an invitation as the administrator would, over a context that
// carries the administrator's identity.
func (h *harness) issue(tenant, email string) Issued {
	h.t.Helper()
	ctx := authntest.Context(h.t, context.Background(), authntest.WithAccount(adminID))
	issued, err := h.p.Issue(ctx, IssueInvite{Tenant: tenant, Email: email})
	if err != nil {
		h.t.Fatalf("issuing an invitation for %s: %v", email, err)
	}
	return issued
}

// row reads one row straight from the table, for a case to inspect the stored
// state.
func (h *harness) row(id string) invitationRow {
	h.t.Helper()
	var rows []invitationRow
	if err := h.handle.Where("id = ?", id).Find(&rows).Error; err != nil {
		h.t.Fatalf("reading %s: %v", id, err)
	}
	if len(rows) == 0 {
		h.t.Fatalf("no row %s", id)
	}
	return rows[0]
}

const adminID = authn.AccountID("acct-admin")
