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
)

const (
	meAccount  = authn.AccountID("acct-me")
	meSession  = authn.SessionID("0190a6f2-3b6c-7c2e-8a4d-5e6f7a8b9c0d")
	otherActor = authn.AccountID("acct-other")
	tenantA    = "tenant-a"
)

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

type recordingAudit struct {
	mu    sync.Mutex
	rows  []audit.Entry
	ids   []authn.Identity
	found []bool
	err   error
}

func (a *recordingAudit) Record(ctx context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, ok := authn.FromContext(ctx)
	a.rows = append(a.rows, e)
	a.ids = append(a.ids, id)
	a.found = append(a.found, ok)
	return a.err
}

func (a *recordingAudit) entries() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.rows...)
}

func (a *recordingAudit) writtenUnder() ([]authn.Identity, []bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]authn.Identity(nil), a.ids...), append([]bool(nil), a.found...)
}

func (a *recordingAudit) fails(err error) { a.mu.Lock(); a.err = err; a.mu.Unlock() }

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

type leaveOut string

const noAudit leaveOut = "audit"

type rig struct {
	t            *testing.T
	mounter      *mounttest.Mounter
	accounts     *fakeAccounts
	verification *fakeSignupVerification
	admin        *fakeAccountAdmin
	audit        *recordingAudit
	logs         *logBuffer
	p            *product
}

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
		audit:        &recordingAudit{},
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
		logger:             logger,
	}
	if !left[noAudit] {
		p.audit = r.audit
	}
	r.p = p
	p.mountRoutes()
	return r
}

func me() []authntest.Option {
	return []authntest.Option{
		authntest.WithAccount(meAccount),
		authntest.WithSession(meSession),
		authntest.WithAuthTime(time.Now()),
	}
}

func impersonating() []authntest.Option {
	return []authntest.Option{
		authntest.WithAccount(meAccount),
		authntest.WithActor(otherActor),
		authntest.WithSession(meSession),
		authntest.WithScope(tenantA),
		authntest.WithAuthTime(time.Now()),
	}
}

func (r *rig) do(method, path string, body any, opts ...authntest.Option) *httptest.ResponseRecorder {
	r.t.Helper()
	r.mounter.As(opts...)
	return r.send(method, path, body)
}

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

type fakeSignupVerification struct {
	mu    sync.Mutex
	state authn.SignupStatus
	err   error
	reads int
}

func (v *fakeSignupVerification) SignupStateOf(_ context.Context, _ authn.AccountID) (authn.SignupStatus, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.reads++
	return v.state, v.err
}

func (v *fakeSignupVerification) PendingAccountByContact(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("profile: the pending-contact read is not part of this surface")
}

func (v *fakeSignupVerification) readCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.reads
}
