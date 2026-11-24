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
	"github.com/vislake/speed/pkg/authn/internal/slug"
	"github.com/vislake/speed/pkg/authn/login/internal/resolve"
)

type fakeKV struct {
	mu             sync.Mutex
	m              map[string][]byte
	exp            map[string]time.Time
	failing        bool
	failIncrPrefix string
	failGetPrefix  string
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
	k.m[key] = []byte(strconv.FormatFloat(cur, 'g', -1, 64))
	if _, ok := k.exp[key]; !ok && ttl > 0 {
		k.exp[key] = time.Now().Add(ttl)
	}
	return cur, nil
}

type fakeMethod struct {
	name  string
	desc  authn.MethodDescriptor
	calls int
	begin func(s authn.Start) (authn.Step, error)
	ans   func(f authn.FlowState, in authn.StepInput) (authn.Step, error)
}

func (m *fakeMethod) Name() string { return m.name }

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

type fakeMFA struct {
	authn.MFA
	outcome         authn.MFAOutcome
	offered         []authn.FactorRecord
	assurance       authn.MFAResult
	verify          func(factorID string) authn.Step
	challenge       func(factorID string) authn.Step
	reason          error
	factors         []authn.FactorRecord
	enroll          func(kind authn.FactorKind, in authn.StepInput) authn.Step
	enrolls         []authn.StepInput
	enrollStates    []authn.FlowState
	enrollIDs       []authn.Identity
	enrollFound     []bool
	required        []requireCall
	kinds           []authn.FactorKind
	challenges      int
	challengeStates []authn.FlowState
	require         func() (authn.MFAOutcome, []authn.FactorRecord, error)
	assure          func(first authn.Proof, second *authn.Pass) authn.MFAResult
}

func (m *fakeMFA) Require(_ context.Context, proof, requirement authn.AccountID, first authn.Proof) (authn.MFAOutcome, []authn.FactorRecord, error) {
	m.required = append(m.required, requireCall{proof, requirement})
	if m.require != nil {
		return m.require()
	}
	return m.outcome, m.offered, m.reason
}

type requireCall struct {
	proof, requirement authn.AccountID
}

func (m *fakeMFA) Factors(context.Context, authn.AccountID) ([]authn.FactorRecord, error) {
	return m.factors, m.reason
}

func (m *fakeMFA) Assurance(first authn.Proof, second *authn.Pass) authn.MFAResult {
	if m.assure != nil {
		return m.assure(first, second)
	}
	return m.assurance
}

func (m *fakeMFA) Challenge(_ context.Context, f authn.FlowState, factorID string) (authn.Step, error) {
	m.challenges++
	m.challengeStates = append(m.challengeStates, f)
	if m.challenge != nil {
		return m.challenge(factorID), nil
	}
	return authn.StepPrompt("code", nil, authn.Field{Name: "code", Kind: authn.FieldCode, Required: true}), nil
}

func (m *fakeMFA) Verify(_ context.Context, _ authn.FlowState, factorID string, _ authn.StepInput) (authn.Step, error) {
	if m.verify != nil {
		return m.verify(factorID), nil
	}
	return authn.StepPassed(authn.Pass{Methods: []authn.Method{authn.MethodTOTP}}), nil
}

func (m *fakeMFA) Enroll(ctx context.Context, f authn.FlowState, kind authn.FactorKind, in authn.StepInput) (authn.Step, error) {
	id, ok := authn.FromContext(ctx)
	m.enrollIDs = append(m.enrollIDs, id)
	m.enrollFound = append(m.enrollFound, ok)
	m.enrolls = append(m.enrolls, in)
	m.enrollStates = append(m.enrollStates, f)
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

type fakeAccounts struct {
	authn.Accounts
	byID      map[authn.AccountID]authn.Account
	byExt     map[string]authn.AccountID
	ident     map[authn.AccountID][]authn.Identifier
	lookup    map[string]authn.AccountID
	lookupErr map[string]error
}

func (a *fakeAccounts) Get(_ context.Context, id authn.AccountID) (authn.Account, error) {
	if acct, ok := a.byID[id]; ok {
		return acct, nil
	}
	return authn.Account{}, authn.ErrAccountNotFound
}

func (a *fakeAccounts) ByExternal(_ context.Context, provider, subject string) (authn.AccountID, error) {
	if id, ok := a.byExt[provider+"\x00"+subject]; ok {
		return id, nil
	}
	return "", authn.ErrAccountNotFound
}

func (a *fakeAccounts) Identifiers(_ context.Context, id authn.AccountID) ([]authn.Identifier, error) {
	return a.ident[id], nil
}

func (a *fakeAccounts) Lookup(_ context.Context, kind authn.IdentifierKind, value string) (authn.AccountID, error) {
	key := string(kind) + "\x00" + value
	if err, armed := a.lookupErr[key]; armed {
		return "", err
	}
	if id, ok := a.lookup[key]; ok {
		return id, nil
	}
	return "", authn.ErrAccountNotFound
}

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

type fakeSessions struct {
	authn.Sessions
	created        []authn.NewSession
	made           []authn.Issued
	revoked        []authn.SessionID
	status         authn.SessionStatus
	statusErr      error
	expires        time.Time
	elevated       []authn.Elevation
	elevatedFor    []authn.SessionID
	revokedAll     int
	except         authn.SessionID
	revokedAccount authn.AccountID
	cookie         string
}

func (s *fakeSessions) Create(_ context.Context, in authn.NewSession) (authn.Issued, error) {
	s.created = append(s.created, in)
	expires := s.expires
	if expires.IsZero() {
		expires = time.Now().Add(time.Hour)
	}
	issued := authn.Issued{ID: "sid-1", Token: "tok-1", ExpiresAt: expires}
	s.made = append(s.made, issued)
	return issued, nil
}

func (s *fakeSessions) SetCookie(w http.ResponseWriter, issued authn.Issued) {
	if s.cookie == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: s.cookie, Value: issued.Token, Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *fakeSessions) Current(req *http.Request) (authn.SessionID, bool, error) {
	if s.cookie == "" {
		return "", false, nil
	}
	c, err := req.Cookie(s.cookie)
	if err != nil || c.Value == "" {
		return "", false, nil
	}
	return authn.SessionID(c.Value), true, nil
}

func (s *fakeSessions) Revoke(_ context.Context, id authn.SessionID, _ string) error {
	s.revoked = append(s.revoked, id)
	return nil
}

func (s *fakeSessions) RevokeAccount(_ context.Context, account authn.AccountID, except authn.SessionID, _ string) ([]authn.SessionID, error) {
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

type fakeAdmin struct {
	authn.AccountAdmin
	links     []authn.ExternalLink
	created   []authn.CreateAccount
	touch     int
	createErr error
}

func (a *fakeAdmin) Create(_ context.Context, in authn.CreateAccount) (authn.Account, error) {
	a.created = append(a.created, in.Clone())
	if a.createErr != nil {
		return authn.Account{}, a.createErr
	}
	return authn.Account{ID: "acct-created", Kind: authn.KindUser, Status: authn.StatusActive}, nil
}

func (a *fakeAdmin) LinkExternal(_ context.Context, _ authn.AccountID, l authn.ExternalLink) error {
	a.links = append(a.links, l)
	return nil
}

func (a *fakeAdmin) Touch(context.Context, authn.AccountID, time.Time) error {
	a.touch++
	return nil
}

type auditing struct {
	audit.Audit
	mu      sync.Mutex
	ids     []authn.Identity
	found   []bool
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

func (a *auditing) identities() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.ids))
	for i, id := range a.ids {
		out[i] = id.Key()
	}
	return out
}

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

type (
	stubMemberships struct{ authn.Memberships }
	stubPasswords   struct{ authn.Passwords }
	stubThrottle    struct{ authn.Throttle }
)

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

	watchCtx atomic.Bool
	leaked   atomic.Bool
}

func newRig(t *testing.T, m *fakeMethod) *rig {
	return newRigMethod(t, m)
}

func newRigMethod(t *testing.T, m authn.LoginMethod) *rig {
	t.Helper()
	return newRigMethods(t, m)
}

func newRigMethods(t *testing.T, methods ...authn.LoginMethod) *rig {
	t.Helper()
	return newRigAt(t, mounttest.DefaultPublicURL, methods...)
}

func newRigAt(t *testing.T, publicURL string, methods ...authn.LoginMethod) *rig {
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
	mounter := mounttest.New(t, mounttest.WithPublicURL(publicURL))

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
	}
	for _, m := range methods {
		p.methods[m.Name()] = m
	}
	p.methodNames = slices.Sorted(maps.Keys(p.methods))
	p.nameCookies(mounter.PublicURL())

	r, err := resolve.New(resolve.Config{
		Accounts: accounts, Admin: admin, Memberships: stubMemberships{},
		NormalizeUsername: slug.Normalize,
	})
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
	mounter.Engine().Use(rig.watchRequestContext)
	p.mountRoutes()

	return rig
}

func (r *rig) watchRequestContext(c *gin.Context) {
	c.Next()
	if !r.watchCtx.Load() {
		return
	}
	if _, ok := authn.FromContext(c.Request.Context()); ok {
		r.leaked.Store(true)
	}
}

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

func (r *rig) allow(id authn.AccountID) {
	r.accounts.byID[id] = authn.Account{ID: id, Kind: authn.KindUser, Status: authn.StatusActive}
}

func (r *rig) allowWithPassword(id authn.AccountID) {
	r.accounts.byID[id] = authn.Account{
		ID: id, Kind: authn.KindUser, Status: authn.StatusActive, PasswordSet: true,
	}
}

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

func (r *rig) postForm(path, body, cookie string) *httptest.ResponseRecorder {
	r.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	return r.m.Do(req)
}

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

func (r *rig) withOpenSignup() {
	r.t.Helper()
	res, err := resolve.New(resolve.Config{
		Accounts:          r.accounts,
		Admin:             r.admin,
		Memberships:       stubMemberships{},
		NormalizeUsername: slug.Normalize,
		Signup:            resolve.SignupOpen,
	})
	if err != nil {
		r.t.Fatalf("building the resolver: %v", err)
	}
	r.p.resolver = res
}

type fakeVerification struct {
	mu    sync.Mutex
	state authn.SignupStatus
	err   error
	reads int
}

func (v *fakeVerification) SignupStateOf(_ context.Context, _ authn.AccountID) (authn.SignupStatus, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.reads++
	return v.state, v.err
}

func (v *fakeVerification) PendingAccountByContact(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("login: the pending-contact read is not part of this surface")
}

func (v *fakeVerification) readCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.reads
}
