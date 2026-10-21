package token

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/settings"
)

// epoch is where every fixture's clock starts.
var epoch = time.Date(2026, time.October, 12, 8, 0, 0, 0, time.UTC)

// The issuer and audience the rig's configuration carries, and the accounts the
// cases act as.
const (
	issuer  = "https://auth.example.com"
	account = authn.AccountID("acct-alice")
)

// fakeClock is a clock a test moves by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeSettings is a settings store a test writes by hand. It carries every item
// the token module reads, so a case changes one and the rest stay at their
// declared defaults, exactly as a real store without rows behaves.
type fakeSettings struct {
	settings.Store
	mu     sync.Mutex
	values map[string]any
}

func newFakeSettings() *fakeSettings {
	return &fakeSettings{values: map[string]any{
		accessTTLPath:      defaults.AccessTTL,
		leewayPath:         defaults.Leeway,
		refreshIdleTTLPath: defaults.RefreshIdleTTL,
		refreshGracePath:   defaults.RefreshGrace,
		checkTTLPath:       defaultCheckTTL,
		checkSessionPath:   defaults.CheckSession,
	}}
}

func (f *fakeSettings) set(key string, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[key] = d
}

// setBool writes a boolean setting, which is how a case turns the session check
// off.
func (f *fakeSettings) setBool(key string, b bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[key] = b
}

func (f *fakeSettings) Get(_ settings.Layer, key string, target any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.values[key]
	if !ok {
		return settings.ErrUnknownKey
	}
	switch ptr := target.(type) {
	case *time.Duration:
		d, ok := value.(time.Duration)
		if !ok {
			return fmt.Errorf("token test: the setting %q holds a %T, not a duration", key, value)
		}
		*ptr = d
	case *bool:
		b, ok := value.(bool)
		if !ok {
			return fmt.Errorf("token test: the setting %q holds a %T, not a boolean", key, value)
		}
		*ptr = b
	default:
		return fmt.Errorf("token test: the setting %q was read into a %T", key, target)
	}
	return nil
}

// fakeSessions is a session store a test writes by hand. statuses holds the
// answer StatusFresh gives for each identifier; an identifier that is not there
// is ErrSessionNotFound. It records the identifiers Revoke and ForCredential
// were called with, and whether Status (the cached read) was ever called, which
// is what a case asserts against when it holds that a rotation reads fresh.
type fakeSessions struct {
	authn.Sessions
	mu           sync.Mutex
	statuses     map[authn.SessionID]authn.SessionStatus
	revoked      []authn.SessionID
	revokeReason map[authn.SessionID]string
	freshCalls   int
	cachedCalls  int
	freshErr     error
	freshHook    func()
	revokeErr    error
	forResult    authn.SessionID
	forErr       error
	forCalls     int
	forExpiry    *time.Time
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{
		statuses:     map[authn.SessionID]authn.SessionStatus{},
		revokeReason: map[authn.SessionID]string{},
	}
}

func (f *fakeSessions) put(id authn.SessionID, s authn.SessionStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses[id] = s
}

func (f *fakeSessions) StatusFresh(_ context.Context, id authn.SessionID) (authn.SessionStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.freshCalls++
	if f.freshErr != nil {
		return authn.SessionStatus{}, f.freshErr
	}
	s, ok := f.statuses[id]
	if !ok {
		return authn.SessionStatus{}, authn.ErrSessionNotFound
	}
	if f.freshHook != nil {
		f.freshHook()
	}
	return s.Clone(), nil
}

func (f *fakeSessions) Status(_ context.Context, _ authn.SessionID) (authn.SessionStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cachedCalls++
	return authn.SessionStatus{}, errors.New("token test: the cached read must not be used")
}

func (f *fakeSessions) Revoke(ctx context.Context, id authn.SessionID, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// A cancelled context is a failure here, as it would be for the real store's
	// SQL, so a case can hold that a write does not depend on the caller's.
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.revokeErr != nil {
		return f.revokeErr
	}
	f.revoked = append(f.revoked, id)
	f.revokeReason[id] = reason
	return nil
}

func (f *fakeSessions) ForCredential(_ context.Context, _ authn.AccountID, _ string, expiry *time.Time) (authn.SessionID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forCalls++
	if expiry != nil {
		e := *expiry
		f.forExpiry = &e
	} else {
		f.forExpiry = nil
	}
	if f.forErr != nil {
		return "", f.forErr
	}
	return f.forResult, nil
}

// fakePasswords is a password-class credential checker a test answers with.
type fakePasswords struct {
	authn.Passwords
	mu     sync.Mutex
	verify func(clientID, secret string) (authn.AccountID, string, time.Time, error)
	calls  int
}

func newFakePasswords() *fakePasswords { return &fakePasswords{} }

func (f *fakePasswords) VerifyServiceSecret(_ context.Context, clientID, secret string) (authn.AccountID, string, time.Time, error) {
	f.mu.Lock()
	f.calls++
	fn := f.verify
	f.mu.Unlock()
	if fn == nil {
		return "", "", time.Time{}, authn.ErrInvalidCredentials
	}
	return fn(clientID, secret)
}

// fakeThrottle is a failure back-off a test drives.
type fakeThrottle struct {
	authn.Throttle
	mu          sync.Mutex
	retry       time.Duration
	checkErr    error
	checkKeys   []string
	failedKeys  []string
	succeeded   []string
	failErr     error
	succeedErr  error
	failEngaged bool
}

func newFakeThrottle() *fakeThrottle { return &fakeThrottle{} }

func (f *fakeThrottle) Check(_ context.Context, _ authn.ThrottleScope, key string) (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkKeys = append(f.checkKeys, key)
	return f.retry, f.checkErr
}

func (f *fakeThrottle) Fail(_ context.Context, _ authn.ThrottleScope, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failedKeys = append(f.failedKeys, key)
	return f.failEngaged, f.failErr
}

func (f *fakeThrottle) Succeed(_ context.Context, _ authn.ThrottleScope, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.succeeded = append(f.succeeded, key)
	return f.succeedErr
}

// fakeImpersonation records the records ended by force.
type fakeImpersonation struct {
	authn.Impersonation
	mu      sync.Mutex
	revoked []string
	err     error
}

func newFakeImpersonation() *fakeImpersonation { return &fakeImpersonation{} }

func (f *fakeImpersonation) Revoke(_ context.Context, id string, _ authn.Identity, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, id)
	return f.err
}

// recordingBus records published events and the handlers subscribed to it.
type recordingBus struct {
	event.Bus
	mu     sync.Mutex
	events []event.Event
	subs   map[string][]event.Handler
}

func newRecordingBus() *recordingBus {
	return &recordingBus{subs: make(map[string][]event.Handler)}
}

func (b *recordingBus) Publish(_ context.Context, e event.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
	return nil
}

func (b *recordingBus) Subscribe(eventType string, _ string, h event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[eventType] = append(b.subs[eventType], h)
}

// subscribedTypes is the set of event types a handler was registered for.
func (b *recordingBus) subscribedTypes() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.subs))
	for t := range b.subs {
		out = append(out, t)
	}
	return out
}

// deliver hands the payload to every handler subscribed to eventType, as a
// transport would.
func (b *recordingBus) deliver(eventType string, payload any) {
	b.mu.Lock()
	handlers := append([]event.Handler(nil), b.subs[eventType]...)
	b.mu.Unlock()
	for _, h := range handlers {
		_ = h(context.Background(), event.Event{Type: eventType, Payload: payload})
	}
}

func (b *recordingBus) types() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.events))
	for i, e := range b.events {
		out[i] = e.Type
	}
	return out
}

// recordingAudit records the audit entries a case writes. When err is set every
// write fails with it, standing for an audit store that cannot be reached.
type recordingAudit struct {
	audit.Audit
	mu      sync.Mutex
	entries []audit.Entry
	err     error
}

func newRecordingAudit() *recordingAudit { return &recordingAudit{} }

func (a *recordingAudit) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.entries = append(a.entries, e)
	return nil
}

func (a *recordingAudit) actions() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.entries))
	for i, e := range a.entries {
		out[i] = e.Action
	}
	return out
}

// logBuffer collects the log lines a module writes.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logBuffer) logger() *slog.Logger { return slog.New(slog.NewTextHandler(l, nil)) }

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// rig is a token product over a real SQLite database with every collaborator a
// test moves by hand.
type rig struct {
	t         *testing.T
	p         *product
	keys      *keySet
	clock     *fakeClock
	settings  *fakeSettings
	sessions  *fakeSessions
	passwords *fakePasswords
	throttle  *fakeThrottle
	imp       *fakeImpersonation
	bus       *recordingBus
	audit     *recordingAudit
	logs      *logBuffer
}

// newRig builds the product: one signing key, a real refresh table, and the
// stand-in collaborators at their defaults. The caller changes a field of the
// rig and then drives the product.
func newRig(t *testing.T) *rig {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "tokens.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	database := dbtest.SQLite(t, dsn, nil)
	applyTokenMigration(t, database.DB())

	r := &rig{
		t:         t,
		keys:      loadedSet(t, algES256),
		clock:     &fakeClock{t: epoch},
		settings:  newFakeSettings(),
		sessions:  newFakeSessions(),
		passwords: newFakePasswords(),
		throttle:  newFakeThrottle(),
		imp:       newFakeImpersonation(),
		bus:       newRecordingBus(),
		audit:     newRecordingAudit(),
		logs:      &logBuffer{},
	}
	cfg := resolved{
		issuer:    issuer,
		audience:  []string{issuer},
		clientID:  clientIDDefault,
		algorithm: algES256,
	}
	p := newProduct(cfg, r.keys, database, mounttest.New(t), &fakeAccounts{}, r.sessions, r.passwords, r.throttle)
	p.clock = r.clock.Now
	p.refresh.now = r.clock.Now
	p.logger = r.logs.logger()
	p.settings.logger = p.logger
	p.settings.store = r.settings
	p.bus = r.bus
	p.audit = r.audit
	p.impersonation = r.imp
	r.p = p
	return r
}

// fakeAccounts is the account store the module requires but the issuing and
// rotation paths never read.
type fakeAccounts struct {
	authn.Accounts
}

// tokenSession puts a live token session in the session store and returns its
// identifier.
func (r *rig) tokenSession() authn.SessionID {
	id := authn.SessionID("sess-token-1")
	r.sessions.put(id, authn.SessionStatus{
		Active:    true,
		Account:   account,
		Kind:      authn.SessionToken,
		Methods:   []authn.Method{authn.MethodPassword},
		Assurance: authn.AssuranceSingle,
		AuthTime:  epoch,
	})
	return id
}

// issueRefresh makes the first refresh token of a fresh token session.
func (r *rig) issueRefresh() (authn.SessionID, authn.RefreshToken) {
	r.t.Helper()
	id := r.tokenSession()
	refresh, err := r.p.NewRefresh(context.Background(), id)
	if err != nil {
		r.t.Fatalf("NewRefresh: %v", err)
	}
	return id, refresh
}

// applyTokenMigration creates the refresh table from the module's migration.
func applyTokenMigration(t *testing.T, handle *gorm.DB) {
	t.Helper()
	body, err := fs.ReadFile(migrationsFS, "sqlite/0001_authn_refresh_tokens.sql")
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

// rowByDigest reads one row of the refresh table by the digest of a token.
func (r *rig) rowByDigest(value string) refreshRow {
	r.t.Helper()
	var out []refreshRow
	if err := r.p.handle.Raw("SELECT "+refreshColumns+" FROM "+tableName+" WHERE token_hash = ?",
		digestRefreshToken(value)).Scan(&out).Error; err != nil {
		r.t.Fatalf("reading the row: %v", err)
	}
	if len(out) == 0 {
		r.t.Fatalf("no row for the token")
	}
	out[0].normalize()
	return out[0]
}

// rowsForSession lists the rows of one session.
func (r *rig) rowsForSession(id authn.SessionID) []refreshRow {
	r.t.Helper()
	var out []refreshRow
	if err := r.p.handle.Raw("SELECT "+refreshColumns+" FROM "+tableName+" WHERE session_id = ? ORDER BY issued_at",
		string(id)).Scan(&out).Error; err != nil {
		r.t.Fatalf("listing rows: %v", err)
	}
	for i := range out {
		out[i].normalize()
	}
	return out
}

func TestNewRefreshOnlyForTokenSessions(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []authn.SessionKind{authn.SessionBrowser, authn.SessionService} {
		t.Run(string(kind), func(t *testing.T) {
			r := newRig(t)
			id := authn.SessionID("sess-x")
			r.sessions.put(id, authn.SessionStatus{Active: true, Account: account, Kind: kind})
			if _, err := r.p.NewRefresh(ctx, id); !errors.Is(err, authn.ErrInvalidGrant) {
				t.Fatalf("NewRefresh for a %s session gave %v, want ErrInvalidGrant", kind, err)
			}
			if rows := r.rowsForSession(id); len(rows) != 0 {
				t.Errorf("a %s session got %d refresh rows", kind, len(rows))
			}
		})
	}
	t.Run("an impersonation session", func(t *testing.T) {
		r := newRig(t)
		id := authn.SessionID("sess-imp")
		r.sessions.put(id, authn.SessionStatus{
			Active: true, Account: account, Kind: authn.SessionToken, ImpersonationID: "grant-1",
		})
		if _, err := r.p.NewRefresh(ctx, id); !errors.Is(err, authn.ErrInvalidGrant) {
			t.Fatalf("NewRefresh for an impersonation session gave %v, want ErrInvalidGrant", err)
		}
	})
}

func TestNewRefreshForATokenSessionStoresOnlyTheDigest(t *testing.T) {
	r := newRig(t)
	id, refresh := r.issueRefresh()
	if !strings.HasPrefix(refresh.Value, refreshTokenPrefix) {
		t.Errorf("the refresh token %q does not carry the prefix", refresh.Value)
	}
	row := r.rowByDigest(refresh.Value)
	if row.TokenHash != digestRefreshToken(refresh.Value) || strings.Contains(row.TokenHash, refresh.Value) {
		t.Errorf("the stored hash %q is not the digest of the token", row.TokenHash)
	}
	if row.SessionID != string(id) || row.UsedAt.Valid {
		t.Errorf("the new row is %+v, want it on the session and unused", row)
	}
	if !row.IdleExpiresAt.Equal(epoch.Add(defaults.RefreshIdleTTL)) {
		t.Errorf("the idle expiry is %v, want %v", row.IdleExpiresAt, epoch.Add(defaults.RefreshIdleTTL))
	}
}

func TestRefreshRotatesIntoASiblingAndReadsTheSessionFresh(t *testing.T) {
	r := newRig(t)
	id, first := r.issueRefresh()

	access, second, err := r.p.Refresh(context.Background(), first.Value)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if second.Value == first.Value || !strings.HasPrefix(second.Value, refreshTokenPrefix) {
		t.Errorf("the rotated token is %q, want a different refresh token", second.Value)
	}
	if access.Value == "" {
		t.Error("no access token was issued")
	}
	if r.sessions.cachedCalls != 0 {
		t.Errorf("the rotation used the cached session read %d times", r.sessions.cachedCalls)
	}
	if r.sessions.freshCalls == 0 {
		t.Error("the rotation did not read the session fresh")
	}

	oldRow := r.rowByDigest(first.Value)
	if !oldRow.UsedAt.Valid {
		t.Error("the old row was not marked used")
	}
	newRow := r.rowByDigest(second.Value)
	if !newRow.ParentID.Valid || newRow.ParentID.String != oldRow.ID {
		t.Errorf("the new row's parent is %v, want %s", newRow.ParentID, oldRow.ID)
	}
	if !oldRow.ReplacedBy.Valid || oldRow.ReplacedBy.String != newRow.ID {
		t.Errorf("the old row's replaced_by is %v, want %s", oldRow.ReplacedBy, newRow.ID)
	}
	if rows := r.rowsForSession(id); len(rows) != 2 {
		t.Errorf("the session has %d rows, want 2", len(rows))
	}
}

func TestConcurrentPresentationOfOneTokenHasExactlyOneWinner(t *testing.T) {
	r := newRig(t)
	_, first := r.issueRefresh()
	ctx := context.Background()

	const racers = 4
	var wg sync.WaitGroup
	results := make([]error, racers)
	pairs := make([]bool, racers)
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, refresh, err := r.p.Refresh(ctx, first.Value)
			results[i] = err
			pairs[i] = refresh.Value != ""
		}()
	}
	close(start)
	wg.Wait()

	winners := 0
	for i, err := range results {
		switch {
		case err == nil && pairs[i]:
			winners++
		case errors.Is(err, authn.ErrRefreshReuse):
		case errors.Is(err, authn.ErrInvalidGrant):
		default:
			t.Errorf("racer %d failed with %v, want a win, ErrRefreshReuse or ErrInvalidGrant", i, err)
		}
	}
	if winners != 1 {
		t.Fatalf("%d of %d concurrent presentations won, want exactly one", winners, racers)
	}
	// The losers are treated as reuse (the default grace is strict), so the
	// family is revoked exactly once per loser and the event and audit are
	// written.
	if got := r.sessions.revoked; len(got) != racers-1 {
		t.Errorf("the family was revoked %d times, want %d", len(got), racers-1)
	}
	if got := r.bus.types(); len(got) != racers-1 {
		t.Errorf("published %v, want %d reuse events", got, racers-1)
	}
	if got := r.audit.actions(); len(got) != racers-1 {
		t.Errorf("wrote audit %v, want %d reuse rows", got, racers-1)
	}
}

func TestARetryInsideTheGraceGetsASibling(t *testing.T) {
	r := newRig(t)
	r.settings.set(refreshGracePath, time.Minute)
	_, first := r.issueRefresh()
	ctx := context.Background()

	if _, _, err := r.p.Refresh(ctx, first.Value); err != nil {
		t.Fatalf("the first rotation failed: %v", err)
	}
	_, retried, err := r.p.Refresh(ctx, first.Value)
	if err != nil {
		t.Fatalf("a retry inside the grace failed: %v", err)
	}
	if retried.Value == "" || retried.Value == first.Value {
		t.Errorf("the retry returned %q, want a fresh token", retried.Value)
	}
	if len(r.sessions.revoked) != 0 {
		t.Errorf("a retry inside the grace revoked the family: %v", r.sessions.revoked)
	}
	if rows := r.rowsForSession("sess-token-1"); len(rows) != 3 {
		t.Errorf("the session has %d rows, want 3 (the original and two siblings)", len(rows))
	}
}

func TestAPresentationPastTheGraceIsAReuse(t *testing.T) {
	r := newRig(t)
	_, first := r.issueRefresh()
	ctx := context.Background()

	if _, _, err := r.p.Refresh(ctx, first.Value); err != nil {
		t.Fatalf("the first rotation failed: %v", err)
	}
	// Move the row's used_at an hour back, past any grace, so the second
	// presentation is read as a reuse rather than a retry.
	row := r.rowByDigest(first.Value)
	if err := r.p.handle.Exec("UPDATE "+tableName+" SET used_at = ? WHERE id = ?",
		epoch.Add(-time.Hour), row.ID).Error; err != nil {
		t.Fatalf("rewriting used_at: %v", err)
	}

	_, _, err := r.p.Refresh(ctx, first.Value)
	if !errors.Is(err, authn.ErrRefreshReuse) {
		t.Fatalf("a presentation past the grace gave %v, want ErrRefreshReuse", err)
	}
	if got := r.sessions.revoked; len(got) != 1 || got[0] != "sess-token-1" {
		t.Errorf("the family was revoked as %v, want the one session", got)
	}
	if got := r.sessions.revokeReason["sess-token-1"]; got != authn.ReasonRefreshReuse {
		t.Errorf("the revocation reason is %q, want %q", got, authn.ReasonRefreshReuse)
	}
	wantEvent := r.bus.types()
	if len(wantEvent) != 1 || wantEvent[0] != authn.EventTokenReuseDetected {
		t.Errorf("published %v, want one reuse event", wantEvent)
	}
	wantAudit := r.audit.actions()
	if len(wantAudit) != 1 || wantAudit[0] != actionRefreshReuse {
		t.Errorf("wrote audit %v, want one %s row", wantAudit, actionRefreshReuse)
	}
}

func TestRefreshOfAnUnknownTokenIsInvalidGrantAndRevokesNothing(t *testing.T) {
	r := newRig(t)
	_, _, err := r.p.Refresh(context.Background(), "spd_rt_"+strings.Repeat("A", 43))
	if !errors.Is(err, authn.ErrInvalidGrant) {
		t.Fatalf("an unknown token gave %v, want ErrInvalidGrant", err)
	}
	if len(r.sessions.revoked) != 0 {
		t.Errorf("an unknown token revoked %v, want nothing", r.sessions.revoked)
	}
}

func TestRefreshAfterTheSessionTurnsInactiveIsInvalidGrant(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	id, first := r.issueRefresh()
	// A revoked session, a disabled account and a changed password all reach the
	// module the same way: StatusFresh reports it is no longer active.
	r.sessions.put(id, authn.SessionStatus{Active: false, Account: account, Kind: authn.SessionToken})
	if _, _, err := r.p.Refresh(ctx, first.Value); !errors.Is(err, authn.ErrInvalidGrant) {
		t.Fatalf("a refresh over an inactive session gave %v, want ErrInvalidGrant", err)
	}
}

func TestRefreshOfAnIdleExpiredRowIsInvalidGrant(t *testing.T) {
	r := newRig(t)
	_, first := r.issueRefresh()
	if err := r.p.handle.Exec("UPDATE "+tableName+" SET idle_expires_at = ? WHERE token_hash = ?",
		epoch.Add(-time.Minute), digestRefreshToken(first.Value)).Error; err != nil {
		t.Fatalf("rewriting the idle expiry: %v", err)
	}
	if _, _, err := r.p.Refresh(context.Background(), first.Value); !errors.Is(err, authn.ErrInvalidGrant) {
		t.Fatalf("a refresh of an idle-expired row gave %v, want ErrInvalidGrant", err)
	}
}

func TestARefreshTokenOutlivesTheSessionIdleTimeout(t *testing.T) {
	// A token session has no idle timeout of its own; the refresh row carries the
	// idle window. A rotation well past the browser idle timeout still succeeds
	// while the row is within its own idle window and the session is active.
	r := newRig(t)
	_, first := r.issueRefresh()
	r.clock.advance(2 * time.Hour)
	if _, _, err := r.p.Refresh(context.Background(), first.Value); err != nil {
		t.Fatalf("a refresh past the session idle timeout failed: %v", err)
	}
}

// TestTheRowOperationsBindTimesInUTC holds that every time written to the table
// is UTC, which is what makes the string comparisons on SQLite correct.
func TestTheRowOperationsBindTimesInUTC(t *testing.T) {
	r := newRig(t)
	r.clock.t = time.Date(2026, time.October, 12, 16, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	_, refresh := r.issueRefresh()
	row := r.rowByDigest(refresh.Value)
	if row.IssuedAt.Location() != time.UTC || row.IdleExpiresAt.Location() != time.UTC {
		t.Errorf("the row times are in %v and %v, want UTC", row.IssuedAt.Location(), row.IdleExpiresAt.Location())
	}
	if row.IssuedAt != time.Date(2026, time.October, 12, 8, 0, 0, 0, time.UTC) {
		t.Errorf("the issued time is %v, want 08:00Z", row.IssuedAt)
	}
}

// TestTheMigrationCreatesTheTable holds the schema the module owns: the columns
// it reads and writes, and the unique hash.
func TestTheMigrationCreatesTheTable(t *testing.T) {
	r := newRig(t)
	if err := r.p.probe(context.Background()); err != nil {
		t.Fatalf("the table is not there after its migration: %v", err)
	}
	var count int
	if err := r.p.handle.Raw("SELECT count(*) FROM " + tableName).Scan(&count).Error; err != nil {
		t.Fatalf("reading the table: %v", err)
	}
	if count != 0 {
		t.Errorf("a fresh table holds %d rows", count)
	}
}

// applyMigrationFile applies one dialect's migration to a handle, statement by
// statement.
func applyMigrationFile(t *testing.T, handle *gorm.DB, dialect string) {
	t.Helper()
	body, err := fs.ReadFile(migrationsFS, dialect+"/0001_authn_refresh_tokens.sql")
	if err != nil {
		t.Fatalf("reading the %s migration: %v", dialect, err)
	}
	for statement := range strings.SplitSeq(string(body), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("applying the %s migration: %v", dialect, err)
		}
	}
}

// TestBothDialectMigrationsRejectADuplicateTokenHash holds the uniqueness of
// token_hash in both migrations: a second row carrying the same digest is
// refused. The postgres migration is applied too, so a loss there is caught, not
// only in the SQLite one.
func TestBothDialectMigrationsRejectADuplicateTokenHash(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "m.db")+"?_pragma=busy_timeout(5000)", nil)
			applyMigrationFile(t, database.DB(), dialect)
			insert := func(id string) error {
				return database.DB().Exec("INSERT INTO "+tableName+
					" (id, session_id, token_hash, parent_id, issued_at, idle_expires_at, used_at, replaced_by) "+
					"VALUES (?, ?, ?, NULL, ?, ?, NULL, NULL)",
					id, "sess-1", "hash-1", epoch, epoch.Add(time.Hour)).Error
			}
			if err := insert("id-1"); err != nil {
				t.Fatalf("the first row: %v", err)
			}
			if err := insert("id-2"); err == nil {
				t.Error("a second row with the same token_hash was accepted, so token_hash is not unique")
			}
		})
	}
}

// TestARotationCopiesTheSessionsAuthenticationFacts holds that the access token
// a rotation issues carries the session's assurance and authentication time, not
// a widened level or a dropped time.
func TestARotationCopiesTheSessionsAuthenticationFacts(t *testing.T) {
	r := newRig(t)
	_, first := r.issueRefresh() // the fixture session: single factor, AuthTime epoch
	access, _, err := r.p.Refresh(context.Background(), first.Value)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	claims := r.issuedClaims(access.Value)
	if claims["acr"] != "1" {
		t.Errorf("the rotated token's acr is %v, want the session's 1", claims["acr"])
	}
	if at, ok := claims["auth_time"].(float64); !ok || int64(at) != epoch.Unix() {
		t.Errorf("the rotated token's auth_time is %v, want the session's %d", claims["auth_time"], epoch.Unix())
	}
}

// TestARotationCarriesAMultiFactorSessionAsMulti holds the other side: the acr
// is copied from the session, so a multi-factor session yields "2".
func TestARotationCarriesAMultiFactorSessionAsMulti(t *testing.T) {
	r := newRig(t)
	id := authn.SessionID("sess-token-1")
	r.sessions.put(id, authn.SessionStatus{
		Active: true, Account: account, Kind: authn.SessionToken,
		Methods:   []authn.Method{authn.MethodPassword, authn.MethodTOTP},
		Assurance: authn.AssuranceMulti, AuthTime: epoch,
	})
	first, err := r.p.NewRefresh(context.Background(), id)
	if err != nil {
		t.Fatalf("NewRefresh: %v", err)
	}
	access, _, err := r.p.Refresh(context.Background(), first.Value)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if claims := r.issuedClaims(access.Value); claims["acr"] != "2" {
		t.Errorf("the rotated token's acr is %v, want the session's 2", claims["acr"])
	}
}

// TestRefreshVerifiesTheSessionEvenInTheStatelessMode holds that check-session
// turns off only the verifying backend's session step: a rotation still reads the
// session fresh, so a session that is no longer live refuses the exchange.
func TestRefreshVerifiesTheSessionEvenInTheStatelessMode(t *testing.T) {
	r := newRig(t)
	id, first := r.issueRefresh()
	r.settings.setBool(checkSessionPath, false)
	r.sessions.put(id, authn.SessionStatus{Active: false, Account: account, Kind: authn.SessionToken})
	if _, _, err := r.p.Refresh(context.Background(), first.Value); !errors.Is(err, authn.ErrInvalidGrant) {
		t.Fatalf("a refresh over an inactive session in the stateless mode gave %v, want ErrInvalidGrant", err)
	}
	if r.sessions.freshCalls == 0 {
		t.Error("the rotation did not read the session fresh")
	}
}

// TestARefreshWhoseRowDisappearsMidCallIsInvalidGrant holds the reread branch: a
// row that vanishes between the read by digest and the reread after a lost
// consume answers invalid_grant, the same case the first read answers, not the
// store's private sentinel the endpoint would map to 503.
func TestARefreshWhoseRowDisappearsMidCallIsInvalidGrant(t *testing.T) {
	r := newRig(t)
	_, first := r.issueRefresh()
	// The session read deletes the row, as a concurrent revoke or sweep would
	// between the read by digest and the reread.
	r.sessions.freshHook = func() {
		if err := r.p.handle.Exec("DELETE FROM " + tableName).Error; err != nil {
			t.Errorf("deleting the row: %v", err)
		}
	}
	if _, _, err := r.p.Refresh(context.Background(), first.Value); !errors.Is(err, authn.ErrInvalidGrant) {
		t.Fatalf("a refresh whose row vanished gave %v, want ErrInvalidGrant", err)
	}
}

// TestTheRowStoresTimesAsUTCText holds the stored representation, not just the
// instant: the column text carries the UTC designator, which is what the SQLite
// string comparisons rely on. The clock is in a non-UTC zone, so a bind without
// the UTC conversion would store the offset instead.
func TestTheRowStoresTimesAsUTCText(t *testing.T) {
	r := newRig(t)
	r.clock.t = time.Date(2026, time.October, 12, 16, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	_, refresh := r.issueRefresh()
	var issued, idle string
	if err := r.p.handle.Raw("SELECT issued_at, idle_expires_at FROM "+tableName+" WHERE token_hash = ?",
		digestRefreshToken(refresh.Value)).Row().Scan(&issued, &idle); err != nil {
		t.Fatalf("reading the raw times: %v", err)
	}
	if issued != "2026-10-12T08:00:00Z" {
		t.Errorf("the stored issued time is %q, want 2026-10-12T08:00:00Z", issued)
	}
	if !strings.HasSuffix(idle, "Z") {
		t.Errorf("the stored idle time %q does not carry the UTC designator", idle)
	}
}

// TestTheRefreshIdleTTLSettingIsRead holds that a configured refresh-idle-ttl is
// the idle window a new row carries, not the declared default.
func TestTheRefreshIdleTTLSettingIsRead(t *testing.T) {
	r := newRig(t)
	r.settings.set(refreshIdleTTLPath, 2*time.Hour)
	_, refresh := r.issueRefresh()
	row := r.rowByDigest(refresh.Value)
	if !row.IdleExpiresAt.Equal(epoch.Add(2 * time.Hour)) {
		t.Errorf("the idle expiry is %v, want the configured 2h (%v)", row.IdleExpiresAt, epoch.Add(2*time.Hour))
	}
}

// TestTheReuseRevocationDoesNotDependOnTheRequestContext holds that the family
// revocation, the event and the audit of a detected reuse run on a context
// detached from the request: the reuse is detected on the retry path, where the
// client's connection may already be gone, and the family must still be ended.
func TestTheReuseRevocationDoesNotDependOnTheRequestContext(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.p.revokeFamily(ctx, "sess-token-1", account)
	if got := r.sessions.revoked; len(got) != 1 || got[0] != "sess-token-1" {
		t.Errorf("a reuse on a cancelled request revoked %v, want the family", got)
	}
	if got := r.bus.types(); len(got) != 1 {
		t.Errorf("a reuse on a cancelled request published %v, want the event", got)
	}
	if got := r.audit.actions(); len(got) != 1 {
		t.Errorf("a reuse on a cancelled request wrote audit %v, want the row", got)
	}
}

// TestRefreshRefusesAnImpersonationSession holds the same guard NewRefresh has:
// a session marked an impersonation never rotates a refresh token, however its
// row came to exist.
func TestRefreshRefusesAnImpersonationSession(t *testing.T) {
	r := newRig(t)
	id, first := r.issueRefresh()
	r.sessions.put(id, authn.SessionStatus{
		Active: true, Account: account, Kind: authn.SessionToken, ImpersonationID: "grant-1",
	})
	if _, _, err := r.p.Refresh(context.Background(), first.Value); !errors.Is(err, authn.ErrInvalidGrant) {
		t.Fatalf("a refresh of an impersonation session gave %v, want ErrInvalidGrant", err)
	}
}

// TestAGraceHitIsLoggedAtWarn holds the security signal the design fixes: a
// second presentation inside the grace window is written at Warn, naming the
// session.
func TestAGraceHitIsLoggedAtWarn(t *testing.T) {
	r := newRig(t)
	r.settings.set(refreshGracePath, time.Minute)
	_, first := r.issueRefresh()
	ctx := context.Background()
	if _, _, err := r.p.Refresh(ctx, first.Value); err != nil {
		t.Fatalf("the first rotation failed: %v", err)
	}
	if _, _, err := r.p.Refresh(ctx, first.Value); err != nil {
		t.Fatalf("a retry inside the grace failed: %v", err)
	}
	logs := r.logs.String()
	if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "grace") {
		t.Errorf("a grace hit was not logged at Warn:\n%s", logs)
	}
}

// TestTheRefreshGraceBoundaryIsInclusive holds the grace comparison's boundary:
// a retry exactly one grace period after the row was used is still a sibling,
// not a reuse.
func TestTheRefreshGraceBoundaryIsInclusive(t *testing.T) {
	r := newRig(t)
	grace := time.Minute
	r.settings.set(refreshGracePath, grace)
	_, first := r.issueRefresh()
	ctx := context.Background()
	if _, _, err := r.p.Refresh(ctx, first.Value); err != nil {
		t.Fatalf("the first rotation failed: %v", err)
	}
	r.clock.advance(grace) // now - used_at == grace exactly
	_, retried, err := r.p.Refresh(ctx, first.Value)
	if err != nil {
		t.Fatalf("a retry at exactly the grace boundary gave %v, want a sibling", err)
	}
	if retried.Value == "" || len(r.sessions.revoked) != 0 {
		t.Errorf("a retry at exactly the grace boundary was read as reuse: token %q revoked %v",
			retried.Value, r.sessions.revoked)
	}
}
