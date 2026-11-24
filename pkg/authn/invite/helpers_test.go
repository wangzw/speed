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

func quietly(handle *gorm.DB) *gorm.DB {
	return handle.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
}

func openDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	handle := quietly(dbtest.SQLite(t, filepath.Join(t.TempDir(), "invite.db"), nil).DB())
	// One connection, so SQLite serializes the writes and a race case sees one
	// winner rather than a busy database.
	if pool, err := handle.DB(); err == nil {
		pool.SetMaxOpenConns(1)
	}
	applyMigrations(t, handle)
	return handle
}

func applyMigrations(t testing.TB, handle *gorm.DB) {
	t.Helper()
	applyMigrationsFor(t, handle, string(db.SQLite))
}

func applyMigrationsFor(t testing.TB, handle *gorm.DB, dialect string) {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, dialect)
	if err != nil {
		t.Fatalf("listing the %s migrations: %v", dialect, err)
	}
	for _, entry := range entries {
		text, err := fs.ReadFile(migrationsFS, dialect+"/"+entry.Name())
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

// The embedded authn.Accounts is nil: only the methods this fake implements may be called.
type fakeAccounts struct {
	authn.Accounts
	mu        sync.Mutex
	byEmail   map[string]authn.AccountID
	kinds     map[authn.AccountID]authn.AccountKind
	names     map[authn.AccountID]string
	usernames map[authn.AccountID]string
	missing   map[authn.AccountID]bool
	statuses  map[authn.AccountID]authn.AccountStatus
	via       string
	lookupEr  error
	failID    authn.AccountID
	failIDErr error
	manyErr   error
	getsByID  map[authn.AccountID]int
}

func newAccounts() *fakeAccounts {
	return &fakeAccounts{
		byEmail:   map[string]authn.AccountID{},
		kinds:     map[authn.AccountID]authn.AccountKind{},
		names:     map[authn.AccountID]string{},
		usernames: map[authn.AccountID]string{},
		missing:   map[authn.AccountID]bool{},
		statuses:  map[authn.AccountID]authn.AccountStatus{},
		getsByID:  map[authn.AccountID]int{},
	}
}

func (a *fakeAccounts) named(id authn.AccountID, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.names[id] = name
}

func (a *fakeAccounts) user(id authn.AccountID, username string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.usernames[id] = username
}

func (a *fakeAccounts) gone(id authn.AccountID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.missing[id] = true
}

func (a *fakeAccounts) getCountOf(id authn.AccountID) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.getsByID[id]
}

func (a *fakeAccounts) disabled(id authn.AccountID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.statuses[id] = authn.StatusDisabled
}

func (a *fakeAccounts) failsFor(id authn.AccountID, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failID = id
	a.failIDErr = err
}

func (a *fakeAccounts) failsMany(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.manyErr = err
}

func (a *fakeAccounts) put(email string, id authn.AccountID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.byEmail[email] = id
}

func (a *fakeAccounts) Get(_ context.Context, id authn.AccountID) (authn.Account, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.getsByID[id]++
	if a.failIDErr != nil && id == a.failID {
		return authn.Account{}, a.failIDErr
	}
	if a.missing[id] {
		return authn.Account{}, authn.ErrAccountNotFound
	}
	kind := authn.KindUser
	if k, ok := a.kinds[id]; ok {
		kind = k
	}
	status := authn.StatusActive
	if s, ok := a.statuses[id]; ok {
		status = s
	}
	return authn.Account{ID: id, Kind: kind, Status: status, DisplayName: a.names[id]}, nil
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
	if username := a.usernames[id]; username != "" {
		out = append(out, authn.Identifier{Kind: authn.IdentifierUsername, Value: username, VerifiedVia: authn.VerifiedViaSelf})
	}
	return out, nil
}

func (a *fakeAccounts) IdentifiersMany(ctx context.Context, ids []authn.AccountID) (map[authn.AccountID][]authn.Identifier, error) {
	a.mu.Lock()
	manyErr := a.manyErr
	a.mu.Unlock()
	if manyErr != nil {
		return nil, manyErr
	}
	out := make(map[authn.AccountID][]authn.Identifier, len(ids))
	for _, id := range ids {
		a.mu.Lock()
		missing := a.missing[id]
		a.mu.Unlock()
		if missing {
			continue
		}
		list, err := a.Identifiers(ctx, id)
		if err != nil {
			return nil, err
		}
		out[id] = list
	}
	return out, nil
}

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

type addedIdentifier struct {
	Account authn.AccountID
	Kind    authn.IdentifierKind
	Value   string
	Via     authn.Verification
}

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

type fakeMemberships struct {
	authn.Memberships
	mu         sync.Mutex
	held       map[string]authn.Membership
	puts       []authn.Membership
	count      int
	err        error
	readErr    error
	duringRead func()
	duringJoin func()
}

func newMemberships() *fakeMemberships {
	return &fakeMemberships{held: map[string]authn.Membership{}}
}

func membershipKey(tenant string, id authn.AccountID) string {
	return tenant + "\x00" + string(id)
}

func (f *fakeMemberships) hold(m authn.Membership) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held == nil {
		f.held = make(map[string]authn.Membership)
	}
	f.held[membershipKey(m.Tenant, m.Account)] = m
}

func (f *fakeMemberships) Membership(_ context.Context, tenant string, id authn.AccountID) (authn.Membership, bool, error) {
	if f.duringRead != nil {
		f.duringRead()
	}
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

// The stand-in keeps rows in memory, not in the transaction handle: a rollback case needs the real account module.
func (f *fakeMemberships) MembershipTx(ctx context.Context, _ *gorm.DB, tenant string, id authn.AccountID) (authn.Membership, bool, error) {
	return f.Membership(ctx, tenant, id)
}

func (f *fakeMemberships) PutJoiningTx(ctx context.Context, _ *gorm.DB, m authn.Membership, wasActive bool) (bool, func(context.Context) error, error) {
	joined, err := f.PutJoining(ctx, m, wasActive)
	if err != nil || !joined {
		return joined, nil, err
	}
	return true, nil, nil
}

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

type roleJudgement struct {
	Tenant  string
	Account string
	Role    string
}

type fakeRolePolicy struct {
	mu          sync.Mutex
	grantErr    error
	stillErr    error
	granted     []roleJudgement
	stillAsked  []roleJudgement
	stillErrFor string
}

func (f *fakeRolePolicy) Grantable(_ context.Context, tenant, account, role string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.granted = append(f.granted, roleJudgement{Tenant: tenant, Account: account, Role: role})
	return f.grantErr
}

func (f *fakeRolePolicy) StillGrantable(_ context.Context, tenant, role, inviter string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stillAsked = append(f.stillAsked, roleJudgement{Tenant: tenant, Account: inviter, Role: role})
	if f.stillErrFor != "" && f.stillErrFor == inviter {
		return f.stillErr
	}
	return nil
}

func (f *fakeRolePolicy) issueCalls() []roleJudgement {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]roleJudgement(nil), f.granted...)
}

func (f *fakeRolePolicy) acceptCalls() []roleJudgement {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]roleJudgement(nil), f.stillAsked...)
}

type boundRole struct {
	Tenant  string
	Account string
	Role    string
}

type fakeRoleBinder struct {
	mu       sync.Mutex
	bindings []boundRole
	err      error
	txSeen   bool
}

func (f *fakeRoleBinder) BindRoleTx(_ context.Context, tx *gorm.DB, tenant, account, role string) (func(context.Context) error, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.txSeen = tx != nil
	if f.err != nil {
		return nil, f.err
	}
	f.bindings = append(f.bindings, boundRole{Tenant: tenant, Account: account, Role: role})
	return nil, nil
}

func (f *fakeRoleBinder) written() []boundRole {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]boundRole(nil), f.bindings...)
}

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

type fakeTenantDescriptions struct {
	mu   sync.Mutex
	byID map[string]authn.TenantDescription
	err  error
}

func (f *fakeTenantDescriptions) TenantDescriptionOf(_ context.Context, id string) (authn.TenantDescription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return authn.TenantDescription{}, f.err
	}
	if description, ok := f.byID[id]; ok {
		return description, nil
	}
	return authn.TenantDescription{Name: id, DisplayName: id}, nil
}

type auditRecord struct {
	entry   audit.Entry
	actor   authn.AccountID
	account authn.AccountID
}

type recordingAudit struct {
	mu      sync.Mutex
	rows    []audit.Entry
	records []auditRecord
	err     error
}

func (a *recordingAudit) Record(ctx context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.rows = append(a.rows, e)
	record := auditRecord{entry: e}
	if id, ok := authn.FromContext(ctx); ok {
		record.actor = id.Actor()
		record.account = id.Account()
	}
	a.records = append(a.records, record)
	return nil
}

func (a *recordingAudit) entries() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.rows...)
}

func (a *recordingAudit) recorded() []auditRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]auditRecord(nil), a.records...)
}

func (a *recordingAudit) fails(err error) { a.mu.Lock(); a.err = err; a.mu.Unlock() }

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
	tenantDescs  *fakeTenantDescriptions
	audit        *recordingAudit
	decider      *fakeDecider
	rolePolicy   *fakeRolePolicy
	roleBinder   *fakeRoleBinder
	mounter      *mounttest.Mounter
	store        *fakeSettingsStore
	counters     *fakeKV
	logs         *logBuffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessOn(t, openDatabase(t))
}

func newHarnessOn(t *testing.T, handle *gorm.DB) *harness {
	t.Helper()
	h := &harness{
		t:            t,
		handle:       handle,
		clock:        newTestClock(),
		accounts:     newAccounts(),
		verification: &fakeSignupVerification{},
		admin:        &fakeAccountAdmin{},
		members:      newMemberships(),
		tenantStates: &fakeTenantStates{states: map[string]authn.TenantState{}},
		tenantDescs:  &fakeTenantDescriptions{byID: map[string]authn.TenantDescription{}},
		audit:        &recordingAudit{},
		decider:      &fakeDecider{allow: true},
		rolePolicy:   &fakeRolePolicy{},
		roleBinder:   &fakeRoleBinder{},
		mounter:      mounttest.New(t),
		store:        &fakeSettingsStore{values: map[string]any{}, errs: map[string]error{}},
		counters:     newFakeKV(),
		logs:         &logBuffer{},
	}
	p := &product{
		handle:           handle,
		accounts:         h.accounts,
		verification:     h.verification,
		memberships:      h.members,
		membershipWriter: h.members,
		rolePolicy:       h.rolePolicy,
		roleBinder:       h.roleBinder,
		mounter:          h.mounter,
		audit:            h.audit,
		kv:               h.counters,
		store:            h.store,
		decider:          h.decider,
		tenantNames:      identityNames{},
		tenantDescs:      h.tenantDescs,
		tenantState:      h.tenantStates,
		logger:           h.logs.logger(),
	}
	p.now = h.clock.read
	h.p = p
	p.mountRoutes()
	return h
}

func (h *harness) issue(tenant, email string) Issued {
	h.t.Helper()
	return h.issueAs(adminID, tenant, email, "")
}

func (h *harness) issueRole(tenant, email, role string) Issued {
	h.t.Helper()
	return h.issueAs(adminID, tenant, email, role)
}

func (h *harness) issueAs(account authn.AccountID, tenant, email, role string) Issued {
	h.t.Helper()
	ctx := authntest.Context(h.t, context.Background(), authntest.WithAccount(account))
	issued, err := h.p.Issue(ctx, IssueInvite{Tenant: tenant, Email: email, Role: role})
	if err != nil {
		h.t.Fatalf("issuing an invitation for %s: %v", email, err)
	}
	return issued
}

func (h *harness) acceptCtx(id authn.AccountID, email string) context.Context {
	h.t.Helper()
	h.proveEmail(id, email)
	return authntest.Context(h.t, context.Background(), authntest.WithAccount(id))
}

func (h *harness) acceptAsHTTP(id authn.AccountID, email string) {
	h.t.Helper()
	h.proveEmail(id, email)
	h.mounter.As(authntest.WithAccount(id))
}

func (h *harness) proveEmail(id authn.AccountID, email string) {
	h.t.Helper()
	normalized, err := normalizeEmail(email)
	if err != nil {
		h.t.Fatalf("the case's address %q is not well formed: %v", email, err)
	}
	h.accounts.put(normalized, id)
}

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

func (h *harness) supersede(rowID string) {
	h.t.Helper()
	if err := h.handle.Model(&invitationRow{}).Where("id = ?", rowID).
		Update("superseded_at", h.clock.read()).Error; err != nil {
		h.t.Fatalf("superseding %s: %v", rowID, err)
	}
}

func (h *harness) budgetCount(kind, subject string) float64 {
	h.t.Helper()
	key := budgetKey(kind, subject, h.p.issueBudget().window, h.clock.read())
	h.counters.mu.Lock()
	defer h.counters.mu.Unlock()
	v, _ := h.counters.live(key)
	return v
}

const adminID = authn.AccountID("acct-admin")

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

func (v *fakeSignupVerification) readCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.stateReads
}

func (v *fakeSignupVerification) PendingAccountByContact(_ context.Context, _ string, _ string) (string, bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.contactReads++
	return v.owner, v.pending, v.pendingErr
}

type identityNames struct{}

func (identityNames) TenantNameOf(_ context.Context, id string) (string, error) {
	return id, nil
}

// The expiry is the wall clock, not the injected clock: moving the case's clock starts a new key rather than evicting.
type fakeKV struct {
	mu     sync.Mutex
	values map[string]float64
	expiry map[string]time.Time
	err    error
	onIncr func(key string, delta float64)
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
	if k.onIncr != nil {
		k.onIncr(key, delta)
	}
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
