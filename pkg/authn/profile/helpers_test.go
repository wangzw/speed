package profile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/settings"
)

// The identifiers a case starts from.
const (
	meAccount  = authn.AccountID("acct-me")
	meSession  = authn.SessionID("0190a6f2-3b6c-7c2e-8a4d-5e6f7a8b9c0d")
	otherActor = authn.AccountID("acct-other")
	tenantA    = "tenant-a"
)

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

func (b *logBuffer) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
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
	a.rows = append(a.rows, e)
	return a.err
}

func (a *recordingAudit) entries() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.rows...)
}

func (a *recordingAudit) fails(err error) { a.mu.Lock(); a.err = err; a.mu.Unlock() }

// fakeAccounts is the account read surface.
type fakeAccounts struct {
	authn.Accounts
	mu          sync.Mutex
	accounts    map[authn.AccountID]authn.Account
	identifiers map[authn.AccountID][]authn.Identifier
	links       map[authn.AccountID][]authn.ExternalBinding
	getErr      error
	idErr       error
	extErr      error
}

func newAccounts() *fakeAccounts {
	return &fakeAccounts{
		accounts:    map[authn.AccountID]authn.Account{},
		identifiers: map[authn.AccountID][]authn.Identifier{},
		links:       map[authn.AccountID][]authn.ExternalBinding{},
	}
}

func (a *fakeAccounts) put(id authn.AccountID, acct authn.Account) {
	a.mu.Lock()
	defer a.mu.Unlock()
	acct.ID = id
	a.accounts[id] = acct
}

func (a *fakeAccounts) Get(_ context.Context, id authn.AccountID) (authn.Account, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.getErr != nil {
		return authn.Account{}, a.getErr
	}
	acct, ok := a.accounts[id]
	if !ok {
		return authn.Account{}, authn.ErrAccountNotFound
	}
	return acct, nil
}

func (a *fakeAccounts) Identifiers(_ context.Context, id authn.AccountID) ([]authn.Identifier, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.idErr != nil {
		return nil, a.idErr
	}
	return append([]authn.Identifier(nil), a.identifiers[id]...), nil
}

func (a *fakeAccounts) Externals(_ context.Context, id authn.AccountID) ([]authn.ExternalBinding, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.extErr != nil {
		return nil, a.extErr
	}
	return append([]authn.ExternalBinding(nil), a.links[id]...), nil
}

// fakeAccountAdmin is the account write surface, counting each call and keeping
// the last set written.
type fakeAccountAdmin struct {
	authn.AccountAdmin
	mu          sync.Mutex
	calls       map[string]int
	nameErr     error
	attrErr     error
	displayName string
	attributes  map[string]string
}

func newAccountAdmin() *fakeAccountAdmin { return &fakeAccountAdmin{calls: map[string]int{}} }

func (a *fakeAccountAdmin) count(op string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls[op]
}

func (a *fakeAccountAdmin) lastAttributes() (map[string]string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.attributes == nil {
		return nil, false
	}
	return a.attributes, true
}

func (a *fakeAccountAdmin) SetDisplayName(_ context.Context, _ authn.AccountID, name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls["set-display-name"]++
	if a.nameErr != nil {
		return a.nameErr
	}
	a.displayName = name
	return nil
}

func (a *fakeAccountAdmin) SetAttributes(_ context.Context, _ authn.AccountID, attrs map[string]string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls["set-attributes"]++
	if a.attrErr != nil {
		return a.attrErr
	}
	a.attributes = attrs
	return nil
}

// fakeMFA is the multi-factor composition; only the factor listing is reached.
type fakeMFA struct {
	authn.MFA
	mu      sync.Mutex
	factors []authn.FactorRecord
	err     error
}

func (m *fakeMFA) Factors(_ context.Context, _ authn.AccountID) ([]authn.FactorRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]authn.FactorRecord(nil), m.factors...), m.err
}

// fakeStore reads the one setting this module consumes.
type fakeStore struct {
	settings.Store
	window time.Duration
	err    error
}

func (s *fakeStore) Get(_ settings.Layer, _ string, target any) error {
	if s.err != nil {
		return s.err
	}
	p, ok := target.(*time.Duration)
	if !ok {
		return settings.ErrTypeMismatch
	}
	*p = s.window
	return nil
}

// leaveOut names a capability a rig builds without.
type leaveOut string

const (
	noAudit leaveOut = "audit"
	noStore leaveOut = "store"
	noMFA   leaveOut = "mfa"
)

// rig is the module over a mounttest Mounter with every capability present.
type rig struct {
	t            *testing.T
	mounter      *mounttest.Mounter
	accounts     *fakeAccounts
	verification *fakeSignupVerification
	admin        *fakeAccountAdmin
	mfa          *fakeMFA
	audit        *recordingAudit
	store        *fakeStore
	logs         *logBuffer
	p            *product
}

// newRig builds the module over a mounttest Mounter with every capability
// present. Each capability the omit list leaves out is not there.
func newRig(t *testing.T, omit ...leaveOut) *rig {
	t.Helper()
	left := map[leaveOut]bool{}
	for _, o := range omit {
		left[o] = true
	}
	r := &rig{
		t:            t,
		mounter:      mounttest.New(t),
		accounts:     newAccounts(),
		verification: &fakeSignupVerification{},
		admin:        newAccountAdmin(),
		mfa:          &fakeMFA{},
		audit:        &recordingAudit{},
		store:        &fakeStore{window: defaultRecentWindow},
		logs:         &logBuffer{},
	}
	r.accounts.put(meAccount, authn.Account{
		Kind:        authn.KindUser,
		Status:      authn.StatusActive,
		DisplayName: "Me",
		Attributes:  map[string]string{"dept": "eng"},
	})

	logger := r.logs.logger()
	p := &product{
		mounter:            r.mounter,
		accounts:           r.accounts,
		signupVerification: r.verification,
		accountAdm:         r.admin,
		mfa:                r.mfa,
		logger:             logger,
		settings:           newReader(r.store, logger),
	}
	if !left[noAudit] {
		p.audit = r.audit
	}
	if left[noStore] {
		p.settings = newReader(nil, logger)
	}
	r.p = p
	p.mountRoutes()
	return r
}

// me is the identity of the account itself, freshly authenticated through a
// first factor and carrying a login session.
func me() []authntest.Option {
	return []authntest.Option{
		authntest.WithAccount(meAccount),
		authntest.WithSession(meSession),
		authntest.WithAuthTime(time.Now()),
	}
}

// multiFactor is the account's holder with a fresh, local multi-factor
// authentication.
func multiFactor() []authntest.Option {
	return []authntest.Option{
		authntest.WithAccount(meAccount),
		authntest.WithSession(meSession),
		authntest.WithMethods(authn.MethodPassword, authn.MethodTOTP),
		authntest.WithAssurance(authn.AssuranceMulti),
		authntest.WithAuthTime(time.Now()),
	}
}

// impersonating is an actor whose hands are on a request taken to be from the
// account, as a verifying backend would stamp it.
func impersonating() []authntest.Option {
	return []authntest.Option{
		authntest.WithAccount(meAccount),
		authntest.WithActor(otherActor),
		authntest.WithSession(meSession),
		authntest.WithScope(tenantA),
		authntest.WithAuthTime(time.Now()),
	}
}

// do sends a request through the Mounter with the identity the options build and
// a JSON body when one is given.
func (r *rig) do(method, path string, body any, opts ...authntest.Option) *httptest.ResponseRecorder {
	r.t.Helper()
	r.mounter.As(opts...)
	return r.send(method, path, body)
}

// doAnonymous sends a request with no identity.
func (r *rig) doAnonymous(method, path string, body any) *httptest.ResponseRecorder {
	r.t.Helper()
	r.mounter.AsAnonymous()
	return r.send(method, path, body)
}

func (r *rig) send(method, path string, body any) *httptest.ResponseRecorder {
	r.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			r.t.Fatalf("encoding the body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return r.mounter.Do(req)
}

// invoke calls a handler directly, with the identity the options build,
// bypassing the gateway. It is how a case drives the handler's own refusals,
// which the Mounter's gateway would answer first.
func (r *rig) invoke(handler gin.HandlerFunc, method, target string, body any, opts ...authntest.Option) *httptest.ResponseRecorder {
	r.t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, target, bodyReader(r.t, body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authntest.Context(r.t, req.Context(), opts...))
	c.Request = req
	handler(c)
	return w
}

// invokeAnonymous calls a handler directly with a context carrying no identity.
func (r *rig) invokeAnonymous(handler gin.HandlerFunc, method, target string, body any) *httptest.ResponseRecorder {
	r.t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, target, bodyReader(r.t, body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	handler(c)
	return w
}

func bodyReader(t *testing.T, body any) io.Reader {
	t.Helper()
	if body == nil {
		return nil
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encoding the body: %v", err)
	}
	return bytes.NewReader(encoded)
}

// fakeSignupVerification reports the self-registration state a case scripts and
// counts how often it was read.
type fakeSignupVerification struct {
	mu    sync.Mutex
	state authn.SignupStatus
	err   error
	reads int
}

// SignupStateOf answers the scripted state, or the scripted failure.
func (v *fakeSignupVerification) SignupStateOf(_ context.Context, _ authn.AccountID) (authn.SignupStatus, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.reads++
	return v.state, v.err
}

// PendingAccountByContact is not part of the profile surface.
func (v *fakeSignupVerification) PendingAccountByContact(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("profile: the pending-contact read is not part of this surface")
}

// readCount is how many state reads the fake answered.
func (v *fakeSignupVerification) readCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.reads
}
