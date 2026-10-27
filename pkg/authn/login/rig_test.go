package login

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/authn/login/internal/resolve"
)

// fakeKV is an in-memory kv.KV that records failures a case can arm.
type fakeKV struct {
	mu      sync.Mutex
	m       map[string][]byte
	exp     map[string]time.Time
	failing bool
	// failIncrPrefix arms a failure of IncrByFloatWithTTL for the keys under one
	// prefix only, so a case can make one budget's counter fail while the other
	// succeeds.
	failIncrPrefix string
	// failGetPrefix arms a failure of Get for the keys under one prefix only,
	// so a case can make one layer's counter unreadable while the flow store
	// stays usable.
	failGetPrefix string
}

func newKV() *fakeKV {
	return &fakeKV{m: map[string][]byte{}, exp: map[string]time.Time{}}
}

var errKV = errors.New("kv is unavailable")

func (k *fakeKV) live(key string) bool {
	e, ok := k.exp[key]
	if !ok {
		return true
	}
	if time.Now().After(e) {
		delete(k.m, key)
		delete(k.exp, key)
		return false
	}
	return true
}

func (k *fakeKV) Get(_ context.Context, key string) ([]byte, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.failing {
		return nil, false, errKV
	}
	if k.failGetPrefix != "" && strings.HasPrefix(key, k.failGetPrefix) {
		return nil, false, errKV
	}
	if !k.live(key) {
		return nil, false, nil
	}
	v, ok := k.m[key]
	return append([]byte(nil), v...), ok, nil
}

func (k *fakeKV) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.failing {
		return errKV
	}
	k.m[key] = append([]byte(nil), value...)
	if ttl > 0 {
		k.exp[key] = time.Now().Add(ttl)
	} else {
		delete(k.exp, key)
	}
	return nil
}

func (k *fakeKV) Delete(_ context.Context, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.failing {
		return errKV
	}
	delete(k.m, key)
	delete(k.exp, key)
	return nil
}

func (k *fakeKV) CompareAndSwap(_ context.Context, key string, old, newVal []byte) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.failing {
		return false, errKV
	}
	cur, ok := k.m[key]
	if !ok {
		if len(old) != 0 {
			return false, nil
		}
	} else if string(cur) != string(old) {
		return false, nil
	}
	k.m[key] = append([]byte(nil), newVal...)
	return true, nil
}

func (k *fakeKV) SetIfAbsent(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.failing {
		return false, errKV
	}
	if k.live(key) {
		if _, ok := k.m[key]; ok {
			return false, nil
		}
	}
	k.m[key] = append([]byte(nil), value...)
	if ttl > 0 {
		k.exp[key] = time.Now().Add(ttl)
	}
	return true, nil
}

func (k *fakeKV) IncrByFloat(_ context.Context, key string, delta float64) (float64, error) {
	return k.IncrByFloatWithTTL(context.Background(), key, delta, 0)
}

// keys lists the live keys the store holds, so a case can assert what a route
// wrote and what it did not.
func (k *fakeKV) keys() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make([]string, 0, len(k.m))
	for key := range k.m {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

func (k *fakeKV) IncrByFloatWithTTL(_ context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.failing {
		return 0, errKV
	}
	if k.failIncrPrefix != "" && strings.HasPrefix(key, k.failIncrPrefix) {
		return 0, errKV
	}
	var cur float64
	if k.live(key) {
		if v, ok := k.m[key]; ok {
			fmt.Sscanf(string(v), "%g", &cur)
		}
	}
	cur += delta
	// The store keeps the shortest exact decimal encoding, as the kv contract
	// says, so a counter that reaches two digits reads back as itself. Trimming
	// trailing zeroes by hand would turn 10 into 1.
	k.m[key] = []byte(strconv.FormatFloat(cur, 'g', -1, 64))
	if _, ok := k.exp[key]; !ok && ttl > 0 {
		k.exp[key] = time.Now().Add(ttl)
	}
	return cur, nil
}

// fakeMethod is a login method whose answers a case scripts per call.
type fakeMethod struct {
	name  string
	desc  authn.MethodDescriptor
	calls int
	begin func(s authn.Start) (authn.Step, error)
	ans   func(f authn.FlowState, in authn.StepInput) (authn.Step, error)
}

func (m *fakeMethod) Name() string { return m.name }

// Describe reports the descriptor a case set, or a form named after the method.
func (m *fakeMethod) Describe() authn.MethodDescriptor {
	if m.desc.Name == "" {
		return authn.MethodDescriptor{Name: m.name, Kind: authn.MethodKindForm}
	}
	return m.desc
}

func (m *fakeMethod) Begin(_ context.Context, s authn.Start) (authn.Step, error) {
	m.calls++
	if m.begin == nil {
		return authn.Step{}, nil
	}
	return m.begin(s)
}

func (m *fakeMethod) Answer(_ context.Context, f authn.FlowState, in authn.StepInput) (authn.Step, error) {
	m.calls++
	if m.ans == nil {
		return authn.Step{}, nil
	}
	return m.ans(f, in)
}

// fakeMFA is the multi-factor module's behaviour a case scripts.
type fakeMFA struct {
	authn.MFA // the methods a case does not script panic if reached
	outcome   authn.MFAOutcome
	offered   []authn.FactorRecord
	assurance authn.MFAResult
	verify    func(factorID string) authn.Step
	reason    error
	factors   []authn.FactorRecord
	enroll    func(kind authn.FactorKind, in authn.StepInput) authn.Step
	enrolls   []authn.StepInput
	// enrollIDs and enrollFound record the identity each Enroll call ran under.
	// The multi-factor module writes a registration's terminal audit row from
	// this context, so a case reads here the identity that row is written under.
	enrollIDs   []authn.Identity
	enrollFound []bool
	// required records every Require call: the proof account and the account
	// whose policy was applied.
	required []requireCall
	kinds    []authn.FactorKind
	// challenges counts the times a factor's challenge was started, so a case
	// can see whether an SMS factor reached its send.
	challenges int
}

func (m *fakeMFA) Require(_ context.Context, proof, requirement authn.AccountID, first authn.Proof) (authn.MFAOutcome, []authn.FactorRecord, error) {
	m.required = append(m.required, requireCall{proof, requirement})
	return m.outcome, m.offered, m.reason
}

// requireCall is one call Require received: which account was proving and which
// account's policy was applied.
type requireCall struct {
	proof, requirement authn.AccountID
}

func (m *fakeMFA) Factors(context.Context, authn.AccountID) ([]authn.FactorRecord, error) {
	return m.factors, m.reason
}
func (m *fakeMFA) Assurance(authn.Proof, *authn.Pass) authn.MFAResult { return m.assurance }
func (m *fakeMFA) Challenge(context.Context, authn.FlowState, string) (authn.Step, error) {
	m.challenges++
	return authn.StepPrompt("code", nil, authn.Field{Name: "code", Kind: authn.FieldCode, Required: true}), nil
}

func (m *fakeMFA) Verify(_ context.Context, _ authn.FlowState, factorID string, _ authn.StepInput) (authn.Step, error) {
	if m.verify != nil {
		return m.verify(factorID), nil
	}
	return authn.StepPassed(authn.Pass{Methods: []authn.Method{authn.MethodTOTP}}), nil
}

func (m *fakeMFA) Enroll(ctx context.Context, _ authn.FlowState, kind authn.FactorKind, in authn.StepInput) (authn.Step, error) {
	id, ok := authn.FromContext(ctx)
	m.enrollIDs = append(m.enrollIDs, id)
	m.enrollFound = append(m.enrollFound, ok)
	m.enrolls = append(m.enrolls, in)
	if m.enroll != nil {
		return m.enroll(kind, in), nil
	}
	return authn.StepEnrolled(authn.FactorRecord{ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive}), nil
}

func (m *fakeMFA) Kinds() []authn.FactorKind {
	if m.kinds != nil {
		return m.kinds
	}
	return []authn.FactorKind{authn.FactorTOTP}
}

// fakeAccounts answers for a fixed set of accounts and the external bindings a
// case registers.
type fakeAccounts struct {
	authn.Accounts
	byID   map[authn.AccountID]authn.Account
	byExt  map[string]authn.AccountID
	ident  map[authn.AccountID][]authn.Identifier
	lookup map[string]authn.AccountID
}

func (a *fakeAccounts) Get(_ context.Context, id authn.AccountID) (authn.Account, error) {
	if acct, ok := a.byID[id]; ok {
		return acct, nil
	}
	return authn.Account{}, authn.ErrAccountNotFound
}

// ByExternal answers for the external bindings a case registers; with none, an
// identity is bound to no account.
func (a *fakeAccounts) ByExternal(_ context.Context, provider, subject string) (authn.AccountID, error) {
	if id, ok := a.byExt[provider+"\x00"+subject]; ok {
		return id, nil
	}
	return "", authn.ErrAccountNotFound
}

// Identifiers answers for the identifiers a case registers.
func (a *fakeAccounts) Identifiers(_ context.Context, id authn.AccountID) ([]authn.Identifier, error) {
	return a.ident[id], nil
}

// Lookup answers for the identifiers a case registers by kind and value.
func (a *fakeAccounts) Lookup(_ context.Context, kind authn.IdentifierKind, value string) (authn.AccountID, error) {
	if id, ok := a.lookup[string(kind)+"\x00"+value]; ok {
		return id, nil
	}
	return "", authn.ErrAccountNotFound
}

// fakeImpersonation answers for one record and judges it by the state a case
// scripts: the record's own end, whether its initiator and target are enabled,
// and whether the target is a member. Those are the four causes the real module
// lapses a record for without End, kept here so a completion case can reach
// each without the real module's stores.
type fakeImpersonation struct {
	authn.Impersonation
	now      func() time.Time
	accounts *fakeAccounts
	member   map[authn.AccountID]bool
	grant    authn.ImpersonationGrant
	err      error
	calls    int
}

func (f *fakeImpersonation) Active(_ context.Context, id string) (authn.ImpersonationGrant, bool, error) {
	f.calls++
	if f.err != nil {
		return authn.ImpersonationGrant{}, false, f.err
	}
	if id != f.grant.ID {
		return authn.ImpersonationGrant{}, false, nil
	}
	switch {
	case !f.now().Before(f.grant.EndsAt):
	case f.accounts.byID[f.grant.Actor].Status != authn.StatusActive:
	case f.accounts.byID[f.grant.Target].Status != authn.StatusActive:
	case !f.member[f.grant.Target]:
	default:
		return f.grant, true, nil
	}
	return f.grant, false, nil
}

// fakeSessions records the sessions made, reports the status a case configures
// for the completion review, and records the elevation.
type fakeSessions struct {
	authn.Sessions
	created   []authn.NewSession
	made      []authn.Issued
	revoked   []authn.SessionID
	status    authn.SessionStatus
	statusErr error
	elevated  []authn.Elevation
	// elevatedFor records the session each elevation was written to.
	elevatedFor []authn.SessionID
	revokedAll  int
	except      authn.SessionID
	// revokedAccount is the account whose other sessions were revoked last.
	revokedAccount authn.AccountID
	rec            *recorder
}

func (s *fakeSessions) Create(_ context.Context, in authn.NewSession) (authn.Issued, error) {
	s.rec.add("create")
	s.created = append(s.created, in)
	issued := authn.Issued{ID: "sid-1", Token: "tok-1", ExpiresAt: time.Now().Add(time.Hour)}
	s.made = append(s.made, issued)
	return issued, nil
}
func (s *fakeSessions) SetCookie(http.ResponseWriter, authn.Issued) {}
func (s *fakeSessions) Current(*http.Request) (authn.SessionID, bool, error) {
	return "", false, nil
}

func (s *fakeSessions) Revoke(_ context.Context, id authn.SessionID, _ string) error {
	s.revoked = append(s.revoked, id)
	return nil
}

func (s *fakeSessions) RevokeAccount(_ context.Context, account authn.AccountID, except authn.SessionID, _ string) ([]authn.SessionID, error) {
	s.rec.add("revoke:" + string(except))
	s.revokedAll++
	s.revokedAccount = account
	s.except = except
	return nil, nil
}

func (s *fakeSessions) StatusFresh(context.Context, authn.SessionID) (authn.SessionStatus, error) {
	if s.statusErr != nil {
		return authn.SessionStatus{}, s.statusErr
	}
	if !s.status.Active {
		return authn.SessionStatus{}, authn.ErrSessionNotFound
	}
	return s.status, nil
}

func (s *fakeSessions) Elevate(_ context.Context, id authn.SessionID, e authn.Elevation) (authn.Issued, error) {
	s.elevated = append(s.elevated, e)
	s.elevatedFor = append(s.elevatedFor, id)
	return authn.Issued{ID: id, Token: "tok-2", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

// fakeAdmin records the external links made and answers for no other write.
type fakeAdmin struct {
	authn.AccountAdmin
	links []authn.ExternalLink
	touch int
}

func (a *fakeAdmin) LinkExternal(_ context.Context, _ authn.AccountID, l authn.ExternalLink) error {
	a.links = append(a.links, l)
	return nil
}

func (a *fakeAdmin) Touch(context.Context, authn.AccountID, time.Time) error {
	a.touch++
	return nil
}

// auditing is an audit sink that records the entries it was handed and the
// identity each row was written under, so a case can see both what was written
// and which identity reached it. An armed err is returned to the caller, which
// is how a case makes a best-effort write fail.
//
// The sink completes the detail the way the audit module's documented contract
// does, so a case sees the row the journal would write: an impersonating
// identity fills on_behalf_of, impersonation_scope and session, and on every
// other row those keys are stripped. Without this the double would show only
// the caller's half of the row.
type auditing struct {
	audit.Audit
	mu      sync.Mutex
	ids     []authn.Identity // the identity of each row; the zero value when there was none
	found   []bool           // whether the context carried an identity
	entries []audit.Entry
	err     error
}

func (a *auditing) Record(ctx context.Context, e audit.Entry) error {
	id, ok := authn.FromContext(ctx)
	e.Detail = completedDetail(e.Detail, id, ok)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ids = append(a.ids, id)
	a.found = append(a.found, ok)
	a.entries = append(a.entries, e)
	return a.err
}

// completedDetail is the detail an audit row is written with: the caller's keys
// plus, for an impersonating identity, the account, scope and session the
// journal writes, and minus those three keys on every other row.
func completedDetail(detail map[string]string, id authn.Identity, ok bool) map[string]string {
	const onBehalfOf, scope, session = "on_behalf_of", "impersonation_scope", "session"
	if !ok || !id.Impersonating() {
		if _, a := detail[onBehalfOf]; !a {
			if _, b := detail[scope]; !b {
				if _, c := detail[session]; !c {
					return detail
				}
			}
		}
		out := map[string]string{}
		for k, v := range detail {
			if k != onBehalfOf && k != scope && k != session {
				out[k] = v
			}
		}
		return out
	}
	out := make(map[string]string, len(detail)+3)
	for k, v := range detail {
		out[k] = v
	}
	tenant, _ := id.Scope()
	out[onBehalfOf] = string(id.Account())
	out[scope] = tenant
	if s := string(id.Session()); s != "" {
		out[session] = s
	} else {
		delete(out, session)
	}
	return out
}

// identities is the actor key of each row's identity, the column the audit
// table records; "" when the row was written in an anonymous context.
func (a *auditing) identities() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.ids))
	for i, id := range a.ids {
		out[i] = id.Key()
	}
	return out
}

// writtenUnder returns the identity of each row and whether the context that
// wrote it carried one.
func (a *auditing) writtenUnder() ([]authn.Identity, []bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]authn.Identity(nil), a.ids...), append([]bool(nil), a.found...)
}

func (a *auditing) rows() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.entries...)
}

// stub types satisfy an interface with a nil method set, for the dependencies a
// case's path does not reach.
type (
	stubMemberships struct{ authn.Memberships }
	stubPasswords   struct{ authn.Passwords }
	stubThrottle    struct{ authn.Throttle }
)

// fakeSignups records the registrations made and answers the account or the error
// a case scripts.
type fakeSignups struct {
	authn.Signups
	registered []authn.RegisterAccount
	acct       authn.AccountID
	err        error
}

func (s *fakeSignups) Register(_ context.Context, in authn.RegisterAccount) (authn.Account, error) {
	s.registered = append(s.registered, in)
	if s.err != nil {
		return authn.Account{}, s.err
	}
	id := s.acct
	if id == "" {
		id = "acct-new"
	}
	return authn.Account{ID: id, Kind: authn.KindUser, Status: authn.StatusActive}, nil
}

// rig is a login product wired to fakes and mounted through the test mounter.
type rig struct {
	t            *testing.T
	m            *mounttest.Mounter
	kv           *fakeKV
	mfa          *fakeMFA
	sessions     *fakeSessions
	accounts     *fakeAccounts
	imp          *fakeImpersonation
	admin        *fakeAdmin
	signups      *fakeSignups
	verification *fakeVerification
	p            *product

	// watchCtx arms the request-context probe and leaked records what it saw. A
	// case sets watchCtx on a request whose route carries no identity, so that an
	// identity left on the request's own context can only have come from a
	// handler that wrote one back.
	watchCtx atomic.Bool
	leaked   atomic.Bool
}

// newRig builds a rig with one password method and a multi-factor module whose
// policy requires nothing.
func newRig(t *testing.T, m *fakeMethod) *rig {
	return newRigMethod(t, m)
}

// newRigMethod builds a rig whose single method is any authn.LoginMethod, for a
// case that needs a method shaped other than fakeMethod.
func newRigMethod(t *testing.T, m authn.LoginMethod) *rig {
	t.Helper()
	return newRigMethods(t, m)
}

// newRigMethods builds a rig with the given methods, mounted through the test
// mounter. It is what a case that needs more than one method builds on.
func newRigMethods(t *testing.T, methods ...authn.LoginMethod) *rig {
	t.Helper()
	kv := newKV()
	mfa := &fakeMFA{outcome: authn.MFANone, assurance: authn.MFAResult{Assurance: authn.AssuranceSingle}}
	accounts := &fakeAccounts{
		byID:   map[authn.AccountID]authn.Account{},
		byExt:  map[string]authn.AccountID{},
		ident:  map[authn.AccountID][]authn.Identifier{},
		lookup: map[string]authn.AccountID{},
	}
	sessions := &fakeSessions{}
	imp := &fakeImpersonation{now: time.Now, accounts: accounts, member: map[authn.AccountID]bool{}}
	admin := &fakeAdmin{}
	signups := &fakeSignups{}
	verification := &fakeVerification{}
	mounter := mounttest.New(t)

	p := &product{
		mounter:        mounter,
		kv:             kv,
		accounts:       accounts,
		admin:          admin,
		signups:        signups,
		verification:   verification,
		passwords:      stubPasswords{},
		memberships:    stubMemberships{},
		throttle:       stubThrottle{},
		sessions:       sessions,
		impersonation:  imp,
		mfa:            mfa,
		resetMail:      newResetMailer(resetMailWorkers),
		methods:        map[string]authn.LoginMethod{},
		settings:       newReader(nil, slog.New(slog.NewTextHandler(ioDiscard{}, nil))),
		logger:         slog.New(slog.NewTextHandler(ioDiscard{}, nil)),
		trustedOrigins: []string{"https://app.example.org"},
		allowInsecure:  true,
	}
	for _, m := range methods {
		p.methods[m.Name()] = m
	}
	p.methodNames = slices.Sorted(maps.Keys(p.methods))

	r, err := resolve.New(resolve.Config{Accounts: accounts, Admin: admin, Memberships: stubMemberships{}})
	if err != nil {
		t.Fatalf("building the resolver: %v", err)
	}
	p.resolver = r

	rig := &rig{
		t:            t,
		m:            mounter,
		kv:           kv,
		mfa:          mfa,
		sessions:     sessions,
		accounts:     accounts,
		imp:          imp,
		admin:        admin,
		signups:      signups,
		verification: verification,
		p:            p,
	}
	// The probe goes in before the routes are mounted, so it runs inside every
	// route's chain and sees the request's own context after the handler.
	mounter.Engine().Use(rig.watchRequestContext)
	p.mountRoutes()

	return rig
}

// watchRequestContext records whether a handler left an identity on the request's
// own context. The gateway stamps an identity or an access level and never both,
// so on a route a case armed with no identity an identity there was written back
// by a handler. It is a no-op until a case arms it.
func (r *rig) watchRequestContext(c *gin.Context) {
	c.Next()
	if !r.watchCtx.Load() {
		return
	}
	if _, ok := authn.FromContext(c.Request.Context()); ok {
		r.leaked.Store(true)
	}
}

// loginAsAuthenticated puts an identity in the request context, as the gateway
// does for a guarded route, and configures the session status the completion
// review reads.
func (r *rig) loginAsAuthenticated(account authn.AccountID, session authn.SessionID) {
	r.t.Helper()
	now := time.Now()
	r.m.As(
		authntest.WithAccount(account),
		authntest.WithSession(session),
		authntest.WithMethods(authn.MethodPassword),
		authntest.WithAssurance(authn.AssuranceSingle),
		authntest.WithAuthTime(now),
	)
	r.sessions.status = authn.SessionStatus{
		Active: true, Account: account, Kind: authn.SessionBrowser,
		Methods: []authn.Method{authn.MethodPassword}, Assurance: authn.AssuranceSingle, AuthTime: now,
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(b []byte) (int, error) { return len(b), nil }

// allow puts an active account in the fake store.
func (r *rig) allow(id authn.AccountID) {
	r.accounts.byID[id] = authn.Account{ID: id, Kind: authn.KindUser, Status: authn.StatusActive}
}

// send issues one request through the mounted routes.
func (r *rig) send(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	r.t.Helper()
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	var req *http.Request
	if reader != nil {
		req = httptest.NewRequest(method, path, reader)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	return r.m.Do(req)
}

// postForm issues a POST carrying an urlencoded form body and the given flow
// cookie, as a SAML ACS does.
func (r *rig) postForm(path, body, cookie string) *httptest.ResponseRecorder {
	r.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	return r.m.Do(req)
}

// flowCookie is the flow cookie a start reply set, as the "name=value" pair to
// send back.
func flowCookie(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if strings.Contains(c.Name, "authn-flow") {
			return c.Name + "=" + c.Value
		}
	}
	t.Fatalf("the reply set no flow cookie; headers %v", rec.Header())
	return ""
}

// recorder is a shared ordered log of the calls a case cares about the order of.
// Its methods are nil-safe, so a fake that was not given one records nothing.
type recorder struct {
	mu    sync.Mutex
	steps []string
}

func (r *recorder) add(s string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.steps = append(r.steps, s)
	r.mu.Unlock()
}

func (r *recorder) all() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.steps...)
}

// recordingPasswords records the Clear call so a case can see where it falls.
type recordingPasswords struct {
	authn.Passwords
	rec *recorder
}

func (p *recordingPasswords) Clear(context.Context, authn.AccountID) error {
	p.rec.add("clear")
	return nil
}

// fakeTokens issues token pairs a case reads back.
type fakeTokens struct {
	authn.Tokens
	issued []authn.IssueToken
}

func (t *fakeTokens) Issue(_ context.Context, in authn.IssueToken) (authn.AccessToken, error) {
	t.issued = append(t.issued, in)
	return authn.AccessToken{Value: "access-1", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (t *fakeTokens) NewRefresh(_ context.Context, _ authn.SessionID) (authn.RefreshToken, error) {
	return authn.RefreshToken{Value: "refresh-1"}, nil
}

// withAutoLink rebuilds the resolver with the automatic link of A4 turned on, so
// a social login whose verified address is on an existing account merges.
func (r *rig) withAutoLink() {
	r.t.Helper()
	res, err := resolve.New(resolve.Config{
		Accounts:    r.accounts,
		Admin:       r.admin,
		Memberships: stubMemberships{},
		AutoLink:    resolve.AutoLinkVerifiedEmail,
	})
	if err != nil {
		r.t.Fatalf("building the resolver: %v", err)
	}
	r.p.resolver = res
}

// fakeVerification reports the self-registration state a case scripts and
// counts how often it was read.
type fakeVerification struct {
	mu    sync.Mutex
	state authn.SignupStatus
	err   error
	reads int
}

// SignupStateOf answers the scripted state, or the scripted failure.
func (v *fakeVerification) SignupStateOf(_ context.Context, _ authn.AccountID) (authn.SignupStatus, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.reads++
	return v.state, v.err
}

// PendingAccountByContact is not part of a login route.
func (v *fakeVerification) PendingAccountByContact(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("login: the pending-contact read is not part of this surface")
}

// readCount is how many state reads the fake answered.
func (v *fakeVerification) readCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.reads
}
