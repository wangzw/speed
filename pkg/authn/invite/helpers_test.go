package invite

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strconv"
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
	"github.com/vislake/speed/pkg/kv"
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

// fakeAccounts is the account read surface. Lookup and Identifiers are
// exercised; the rest of the interface is embedded nil, which a case must not
// reach. via is the way the held address was established, email-code unless a
// case says otherwise.
type fakeAccounts struct {
	authn.Accounts
	mu       sync.Mutex
	byEmail  map[string]authn.AccountID
	kinds    map[authn.AccountID]authn.AccountKind
	via      string
	lookupEr error
	getEr    error
}

func newAccounts() *fakeAccounts {
	return &fakeAccounts{byEmail: map[string]authn.AccountID{}, kinds: map[authn.AccountID]authn.AccountKind{}}
}

func (a *fakeAccounts) put(email string, id authn.AccountID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.byEmail[email] = id
}

// Get answers the acting account's kind: every account is a user unless a case
// says otherwise, and a service account has no self-registration to gate.
func (a *fakeAccounts) Get(_ context.Context, id authn.AccountID) (authn.Account, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.getEr != nil {
		return authn.Account{}, a.getEr
	}
	kind := authn.KindUser
	if k, ok := a.kinds[id]; ok {
		kind = k
	}
	return authn.Account{ID: id, Kind: kind, Status: authn.StatusActive}, nil
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

// Identifiers answers the email identifiers the account holds. The way each was
// established is the case's via, email-code by default, so an existing account
// in the ordinary case already proves the address and acceptance adds only the
// membership.
func (a *fakeAccounts) Identifiers(_ context.Context, id authn.AccountID) ([]authn.Identifier, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lookupEr != nil {
		return nil, a.lookupEr
	}
	via := a.via
	if via == "" {
		via = authn.VerifiedViaEmailCode
	}
	var out []authn.Identifier
	for email, account := range a.byEmail {
		if account == id {
			out = append(out, authn.Identifier{Kind: authn.IdentifierEmail, Value: email, VerifiedVia: via})
		}
	}
	return out, nil
}

// fakeAccountAdmin is the account write surface, counting each call and
// recording the input it was given.
type fakeAccountAdmin struct {
	authn.AccountAdmin
	mu       sync.Mutex
	creates  int
	created  authn.CreateAccount
	newID    authn.AccountID
	err      error
	added    []addedIdentifier
	addedErr error
}

// addedIdentifier is one AddIdentifier call the fake recorded.
type addedIdentifier struct {
	Account authn.AccountID
	Kind    authn.IdentifierKind
	Value   string
	Via     authn.Verification
}

// AddIdentifier records the write the acceptance makes when it finds a pending
// owner for the invited address.
func (a *fakeAccountAdmin) AddIdentifier(_ context.Context, id authn.AccountID, kind authn.IdentifierKind, value string, via authn.Verification) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.addedErr != nil {
		return a.addedErr
	}
	a.added = append(a.added, addedIdentifier{Account: id, Kind: kind, Value: value, Via: via})
	return nil
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

// fakeMemberships is the membership read and write surface. err is the write
// failure a case scripts; readErr is the read failure, kept separate because an
// acceptance reads before it writes.
type fakeMemberships struct {
	authn.Memberships
	mu      sync.Mutex
	held    map[string]authn.Membership
	puts    []authn.Membership
	count   int
	err     error
	readErr error
	// duringJoin runs after the acceptance read the membership and before the
	// conditional write, so a case can interleave a suspension or a resend.
	duringJoin func()
}

// newMemberships builds an empty membership stand-in.
func newMemberships() *fakeMemberships {
	return &fakeMemberships{held: map[string]authn.Membership{}}
}

// membershipKey identifies one membership in the fake's held map.
func membershipKey(tenant string, id authn.AccountID) string {
	return tenant + "\x00" + string(id)
}

// hold writes a membership the case wants the read to answer.
func (f *fakeMemberships) hold(m authn.Membership) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held == nil {
		f.held = make(map[string]authn.Membership)
	}
	f.held[membershipKey(m.Tenant, m.Account)] = m
}

func (f *fakeMemberships) Membership(_ context.Context, tenant string, id authn.AccountID) (authn.Membership, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return authn.Membership{}, false, f.readErr
	}
	m, ok := f.held[membershipKey(tenant, id)]
	return m, ok, nil
}

func (f *fakeMemberships) Put(_ context.Context, m authn.Membership) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.puts = append(f.puts, m)
	f.count++
	f.held[membershipKey(m.Tenant, m.Account)] = m
	return nil
}

// PutJoining models the account module's conditional write: an inactive row is
// never resumed, and a row the caller read as active but that is gone is not
// written again.
func (f *fakeMemberships) PutJoining(_ context.Context, m authn.Membership, wasActive bool) (bool, error) {
	if f.duringJoin != nil {
		f.duringJoin()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return false, f.err
	}
	held, ok := f.held[membershipKey(m.Tenant, m.Account)]
	if ok && !held.Active {
		return false, nil
	}
	if !ok && wasActive {
		return false, nil
	}
	f.puts = append(f.puts, m)
	f.count++
	f.held[membershipKey(m.Tenant, m.Account)] = m
	return true, nil
}

// fakeTenantStates answers the tenant state the authentication face reads. A
// tenant a case did not name is serving.
type fakeTenantStates struct {
	mu     sync.Mutex
	states map[string]authn.TenantState
	err    error
	asked  []string
}

func (f *fakeTenantStates) TenantStateOf(_ context.Context, id string) (authn.TenantState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, id)
	if f.err != nil {
		return authn.TenantDeleted, f.err
	}
	if state, ok := f.states[id]; ok {
		return state, nil
	}
	return authn.TenantServing, nil
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
	t            *testing.T
	p            *product
	handle       *gorm.DB
	clock        *testClock
	accounts     *fakeAccounts
	verification *fakeSignupVerification
	admin        *fakeAccountAdmin
	members      *fakeMemberships
	tenantStates *fakeTenantStates
	audit        *recordingAudit
	decider      *fakeDecider
	mounter      *mounttest.Mounter
	store        *fakeSettingsStore
	counters     *fakeKV
	logs         *logBuffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	handle := openDatabase(t)
	h := &harness{
		t:            t,
		handle:       handle,
		clock:        newTestClock(),
		accounts:     newAccounts(),
		verification: &fakeSignupVerification{},
		admin:        &fakeAccountAdmin{},
		members:      newMemberships(),
		tenantStates: &fakeTenantStates{states: map[string]authn.TenantState{}},
		audit:        &recordingAudit{},
		decider:      &fakeDecider{allow: true},
		mounter:      mounttest.New(t),
		store:        &fakeSettingsStore{values: map[string]any{}, errs: map[string]error{}},
		counters:     newFakeKV(),
		logs:         &logBuffer{},
	}
	p := &product{
		handle:       handle,
		accounts:     h.accounts,
		verification: h.verification,
		memberships:  h.members,
		mounter:      h.mounter,
		audit:        h.audit,
		kv:           h.counters,
		store:        h.store,
		decider:      h.decider,
		tenantNames:  identityNames{},
		tenantState:  h.tenantStates,
		logger:       h.logs.logger(),
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

// acceptCtx records that the account proves the address and returns a context
// carrying that account, which is what an acceptance takes.
func (h *harness) acceptCtx(id authn.AccountID, email string) context.Context {
	h.t.Helper()
	h.proveEmail(id, email)
	return authntest.Context(h.t, context.Background(), authntest.WithAccount(id))
}

// acceptAsHTTP does the same for a request through the Mounter: it records the
// proof and sets the identity the following requests carry.
func (h *harness) acceptAsHTTP(id authn.AccountID, email string) {
	h.t.Helper()
	h.proveEmail(id, email)
	h.mounter.As(authntest.WithAccount(id))
}

// proveEmail records that the account holds the address as a proven email
// identifier in the account read stand-in.
func (h *harness) proveEmail(id authn.AccountID, email string) {
	h.t.Helper()
	normalized, err := normalizeEmail(email)
	if err != nil {
		h.t.Fatalf("the case's address %q is not well formed: %v", email, err)
	}
	h.accounts.put(normalized, id)
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

// fakeSignupVerification reports the self-registration state a case scripts and
// answers the pending-contact reverse lookup. states overrides state for the
// accounts it names, so a case can tell an unproved operator from a proved
// target.
type fakeSignupVerification struct {
	mu           sync.Mutex
	state        authn.SignupStatus
	states       map[authn.AccountID]authn.SignupStatus
	asked        []authn.AccountID
	err          error
	owner        string
	pending      bool
	pendingErr   error
	stateReads   int
	contactReads int
}

// SignupStateOf answers the state scripted for the account, falling back to
// state, or the scripted failure.
func (v *fakeSignupVerification) SignupStateOf(_ context.Context, id authn.AccountID) (authn.SignupStatus, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.stateReads++
	v.asked = append(v.asked, id)
	if status, ok := v.states[id]; ok {
		return status, v.err
	}
	return v.state, v.err
}

// readCount is how many state reads the fake answered.
func (v *fakeSignupVerification) readCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.stateReads
}

// PendingAccountByContact answers the scripted owner.
func (v *fakeSignupVerification) PendingAccountByContact(_ context.Context, _ string, _ string) (string, bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.contactReads++
	return v.owner, v.pending, v.pendingErr
}

// identityNames stands in for the roster's id-to-name seam over an assembly
// whose fixture identifiers double as their names.
type identityNames struct{}

func (identityNames) TenantNameOf(_ context.Context, id string) (string, error) {
	return id, nil
}

// fakeKV is an in-memory kv.KV for the budget counters. It implements the whole
// contract, but the budgets only use IncrByFloatWithTTL; the expiry is the wall
// clock, so a case that moves the injected clock to a new window gets a new key
// rather than relying on an eviction.
type fakeKV struct {
	mu     sync.Mutex
	values map[string]float64
	expiry map[string]time.Time
	err    error
}

var _ kv.KV = (*fakeKV)(nil)

func newFakeKV() *fakeKV {
	return &fakeKV{values: map[string]float64{}, expiry: map[string]time.Time{}}
}

func (k *fakeKV) live(key string) (float64, bool) {
	v, ok := k.values[key]
	if !ok {
		return 0, false
	}
	if at, has := k.expiry[key]; has && !at.IsZero() && !at.After(time.Now()) {
		delete(k.values, key)
		delete(k.expiry, key)
		return 0, false
	}
	return v, true
}

func (k *fakeKV) Get(_ context.Context, key string) ([]byte, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return nil, false, k.err
	}
	v, ok := k.live(key)
	if !ok {
		return nil, false, nil
	}
	return []byte(strconv.FormatFloat(v, 'g', -1, 64)), true, nil
}

func (k *fakeKV) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return k.err
	}
	v, err := strconv.ParseFloat(string(value), 64)
	if err != nil {
		return kv.ErrNotNumeric
	}
	k.values[key] = v
	k.setExpiry(key, ttl)
	return nil
}

func (k *fakeKV) Delete(_ context.Context, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return k.err
	}
	delete(k.values, key)
	delete(k.expiry, key)
	return nil
}

func (k *fakeKV) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	return k.IncrByFloatWithTTL(ctx, key, delta, 0)
}

func (k *fakeKV) IncrByFloatWithTTL(_ context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return 0, k.err
	}
	v, ok := k.live(key)
	v += delta
	k.values[key] = v
	if !ok {
		k.setExpiry(key, ttl)
	}
	return v, nil
}

func (k *fakeKV) CompareAndSwap(_ context.Context, key string, old, newVal []byte) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return false, k.err
	}
	current, ok := k.live(key)
	var text string
	if ok {
		text = strconv.FormatFloat(current, 'g', -1, 64)
	}
	if text != string(old) {
		return false, nil
	}
	v, err := strconv.ParseFloat(string(newVal), 64)
	if err != nil {
		return false, kv.ErrNotNumeric
	}
	k.values[key] = v
	return true, nil
}

func (k *fakeKV) SetIfAbsent(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return false, k.err
	}
	if _, ok := k.live(key); ok {
		return false, nil
	}
	v, err := strconv.ParseFloat(string(value), 64)
	if err != nil {
		return false, kv.ErrNotNumeric
	}
	k.values[key] = v
	k.setExpiry(key, ttl)
	return true, nil
}

func (k *fakeKV) setExpiry(key string, ttl time.Duration) {
	if ttl > 0 {
		k.expiry[key] = time.Now().Add(ttl)
		return
	}
	delete(k.expiry, key)
}
