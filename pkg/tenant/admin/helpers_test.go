package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

var (
	_ tenant.Catalog                  = (*stubCatalog)(nil)
	_ tenant.WriteGuard               = (*stubWriteGuard)(nil)
	_ tenant.Provisioner              = (*stubProvisioner)(nil)
	_ tenant.OrganizationWriter       = (*stubOrganization)(nil)
	_ tenant.OwnershipTarget          = (*stubTarget)(nil)
	_ guard.Guard                     = (*stubGuard)(nil)
	_ audit.Audit                     = (*recordingAudit)(nil)
	_ speedhttp.Router                = (*stubRouter)(nil)
	_ speedhttp.Endpoint              = (*stubEndpoint)(nil)
	_ speedhttp.TenantAdminRouteGuard = (*stubTenantAdminGuard)(nil)
	_ log.Logger                      = loggerStub{}
)

type stubTenantAdminGuard struct{}

func (stubTenantAdminGuard) GuardTenantAdmin(next gin.HandlerFunc) gin.HandlerFunc { return next }

type catalogCall struct {
	op      string
	id      tenant.ID
	owner   string
	tenant  tenant.Tenant
	status  tenant.Status
	profile tenant.Profile
}

type stubCatalog struct {
	mu    sync.Mutex
	calls []catalogCall
	held  tenant.Tenant
	found bool
	list  []tenant.Tenant
	err   error

	getErr        error
	setStatusErr  error
	setProfileErr error
	// during runs at the start of SetProfile, before the call is recorded, so a
	// case can hold that the mandatory audit row landed first.
	during func()
	// getHook runs before every Get answer, so a case can make a write land
	// between two reads (a transfer committing between the guard's read and
	// the handler's).
	getHook func()
}

func (s *stubCatalog) store(t tenant.Tenant) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held = t
	s.found = true
}

func (s *stubCatalog) record(call catalogCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
}

func (s *stubCatalog) recorded() []catalogCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]catalogCall(nil), s.calls...)
}

func (s *stubCatalog) fail(err error) { s.mu.Lock(); s.err = err; s.mu.Unlock() }

func (s *stubCatalog) errNow() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *stubCatalog) errOr(specific error) error {
	if specific != nil {
		return specific
	}
	return s.errNow()
}

func (s *stubCatalog) Get(_ context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
	s.record(catalogCall{op: "Get", id: id})
	if err := s.errOr(s.getErr); err != nil {
		return tenant.Tenant{}, false, err
	}
	if s.getHook != nil {
		s.getHook()
	}
	return s.held, s.found, nil
}

func (s *stubCatalog) ByName(_ context.Context, name string) (tenant.Tenant, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.found && s.held.Name == name {
		return s.held, true, nil
	}
	for _, t := range s.list {
		if t.Name == name {
			return t, true, nil
		}
	}
	return tenant.Tenant{}, false, nil
}

func (s *stubCatalog) NameOf(_ context.Context, id tenant.ID) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.found && (s.held.ID == id || tenant.ID(s.held.Name) == id) {
		return s.held.Name, nil
	}
	for _, t := range s.list {
		if t.ID == id || tenant.ID(t.Name) == id {
			return t.Name, nil
		}
	}
	return "", nil
}

func (s *stubCatalog) Put(_ context.Context, t tenant.Tenant) error {
	s.record(catalogCall{op: "Put", id: t.ID, tenant: t})
	return errors.New("the management surface must not write through Put")
}

func (s *stubCatalog) Create(_ context.Context, t tenant.Tenant) error {
	s.record(catalogCall{op: "Create", id: t.ID, tenant: t})
	return errors.New("the management surface must not register through Catalog.Create; it goes through the Provisioner")
}

func (s *stubCatalog) SetStatus(_ context.Context, id tenant.ID, st tenant.Status) error {
	s.record(catalogCall{op: "SetStatus", id: id, status: st})
	return s.errOr(s.setStatusErr)
}

func (s *stubCatalog) SetStatusByAccount(_ context.Context, id tenant.ID, st tenant.Status) error {
	s.record(catalogCall{op: "SetStatusByAccount", id: id, status: st})
	return errors.New("the management surface must not change status by account")
}

func (s *stubCatalog) Delete(_ context.Context, id tenant.ID) error {
	s.record(catalogCall{op: "Delete", id: id})
	return errors.New("the management surface must not delete a tenant")
}

func (s *stubCatalog) SetOwner(_ context.Context, id tenant.ID, owner string) error {
	s.record(catalogCall{op: "SetOwner", id: id, owner: owner})
	return errors.New("the management surface must not transfer a tenant's owner")
}

func (s *stubCatalog) SetOwnerIfEmpty(_ context.Context, id tenant.ID, owner string) (bool, error) {
	s.record(catalogCall{op: "SetOwnerIfEmpty", id: id, owner: owner})
	return false, errors.New("the management surface must not repair a tenant's owner")
}

func (s *stubCatalog) SetProfile(_ context.Context, id tenant.ID, profile tenant.Profile) error {
	if s.during != nil {
		s.during()
	}
	s.record(catalogCall{op: "SetProfile", id: id, profile: profile})
	return s.errOr(s.setProfileErr)
}

func (s *stubCatalog) List(context.Context) ([]tenant.Tenant, error) {
	s.record(catalogCall{op: "List"})
	if err := s.errNow(); err != nil {
		return nil, err
	}
	return append([]tenant.Tenant(nil), s.list...), nil
}

func (s *stubCatalog) Enter(ctx context.Context, _ tenant.ID) (context.Context, error) {
	return ctx, nil
}

func (s *stubCatalog) EnterRegistered(ctx context.Context, _ tenant.ID) (context.Context, error) {
	return ctx, nil
}

type stubGuard struct {
	mu      sync.Mutex
	allow   bool
	err     error
	asked   []authz.Permission
	domains []authz.Domain
}

func (g *stubGuard) note(p authz.Permission, domain authz.Domain) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.asked = append(g.asked, p)
	g.domains = append(g.domains, domain)
}

func (g *stubGuard) answer() (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.allow, g.err
}

func (g *stubGuard) Allowed(_ *gin.Context, perm authz.Permission) (bool, error) {
	g.note(perm, "")
	return g.answer()
}

func (g *stubGuard) refuse(c *gin.Context) bool {
	allow, err := g.answer()
	switch {
	case err != nil:
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusServiceUnavailable})
		return false
	case !allow:
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusForbidden})
		return false
	}
	return true
}

func (g *stubGuard) RequirePermission(c *gin.Context, perm authz.Permission) bool {
	g.note(perm, "")
	return g.refuse(c)
}

func (g *stubGuard) RequirePermissionIn(c *gin.Context, perm authz.Permission, domain authz.Domain) bool {
	g.note(perm, domain)
	return g.refuse(c)
}

func (g *stubGuard) askedFor() []authz.Permission {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]authz.Permission(nil), g.asked...)
}

func (g *stubGuard) domainsAsked() []authz.Domain {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]authz.Domain(nil), g.domains...)
}

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

func (a *recordingAudit) fail(err error) { a.mu.Lock(); a.err = err; a.mu.Unlock() }

type mountedRoute struct{ method, path string }

type stubEndpoint struct {
	mu     sync.Mutex
	routes []mountedRoute
}

func (e *stubEndpoint) Route(method, path string, _ ...gin.HandlerFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.routes = append(e.routes, mountedRoute{method, path})
}

func (e *stubEndpoint) Use(speedhttp.Middleware) {}

func (e *stubEndpoint) Accepting() bool { return true }

func (e *stubEndpoint) registered() []mountedRoute {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]mountedRoute(nil), e.routes...)
}

type stubRouter struct {
	mu        sync.Mutex
	endpoints map[string]*stubEndpoint
	order     []string
}

func newStubRouter(names ...string) *stubRouter {
	r := &stubRouter{endpoints: make(map[string]*stubEndpoint, len(names))}
	for _, name := range names {
		r.endpoints[name] = &stubEndpoint{}
		r.order = append(r.order, name)
	}
	return r
}

func (r *stubRouter) Endpoint(name string) (speedhttp.Endpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	endpoint, ok := r.endpoints[name]
	if !ok {
		return nil, fmt.Errorf("the entry point declares no endpoint %q", name)
	}
	return endpoint, nil
}

func (r *stubRouter) Endpoints() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

func (r *stubRouter) endpoint(name string) *stubEndpoint {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.endpoints[name]
}

type loggerStub struct{ logger *slog.Logger }

func (l loggerStub) Named(string) *slog.Logger { return l.logger }
func (loggerStub) Redaction() log.Redaction    { return noRules{} }

type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

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

func decode(t *testing.T, w *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), target); err != nil {
		t.Fatalf("decoding the response %q: %v", w.Body.String(), err)
	}
}

type problem struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
}

func problemOf(t *testing.T, w *httptest.ResponseRecorder) problem {
	t.Helper()
	var p problem
	decode(t, w, &p)
	return p
}

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

type rig struct {
	t            *testing.T
	p            *product
	catalog      *stubCatalog
	writeGuard   *stubWriteGuard
	provisioner  *stubProvisioner
	organization *stubOrganization
	target       *stubTarget
	guard        *stubGuard
	routeGuard   stubTenantAdminGuard
	audit        *recordingAudit
	router       *stubRouter
	logs         *logBuffer
	engine       *gin.Engine
	signup       *fakeSignupVerification
}

func newRig(t *testing.T) *rig {
	t.Helper()
	logs := &logBuffer{}
	r := &rig{
		t:            t,
		catalog:      &stubCatalog{},
		writeGuard:   &stubWriteGuard{},
		organization: &stubOrganization{},
		target:       &stubTarget{},
		guard:        &stubGuard{allow: true},
		audit:        &recordingAudit{},
		router:       newStubRouter("public"),
		logs:         logs,
		signup:       &fakeSignupVerification{},
	}
	r.provisioner = &stubProvisioner{roster: r.catalog}
	r.p = &product{
		catalog:      r.catalog,
		writeGuard:   r.writeGuard,
		provisioner:  r.provisioner,
		organization: r.organization,
		ownerTarget:  r.target,
		guard:        r.guard,
		routeGuard:   r.routeGuard,
		names:        r.catalog,
		audit:        r.audit,
		verification: r.signup,
		logger:       logs.logger(),
		cfg: resolved{
			endpoints: []string{"public"}, tenantEndpoints: []string{"public"},
			selfEndpoints: []string{"public"},
		},
	}
	r.engine = r.mount()
	return r
}

type organizationCall struct {
	op            string
	id            tenant.ID
	owner         string
	request       string
	expectedOwner string
}

type stubTarget struct {
	mu     sync.Mutex
	err    error
	calls  []organizationCall
	during func()

	request tenant.TransferRequest
	found   bool
	list    []tenant.TransferRequest
	readErr error
}

func (t *stubTarget) ConfirmTransfer(_ context.Context, requestID, target string) error {
	return t.record(organizationCall{op: "ConfirmTransfer", owner: target, request: requestID})
}

func (t *stubTarget) DeclineTransfer(_ context.Context, requestID, target string) error {
	return t.record(organizationCall{op: "DeclineTransfer", owner: target, request: requestID})
}

func (t *stubTarget) PendingTransfersFor(_ context.Context, target string) ([]tenant.TransferRequest, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.readErr != nil {
		return nil, t.readErr
	}
	out := make([]tenant.TransferRequest, len(t.list))
	copy(out, t.list)
	return out, nil
}

func (t *stubTarget) TransferFor(_ context.Context, requestID, target string) (tenant.TransferRequest, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.readErr != nil {
		return tenant.TransferRequest{}, t.readErr
	}
	if !t.found {
		return tenant.TransferRequest{}, fmt.Errorf("%w: request %q", tenant.ErrTransferNotFound, requestID)
	}
	request := t.request
	if request.To != "" && request.To != target {
		return tenant.TransferRequest{}, fmt.Errorf("%w: request %q", tenant.ErrTransferNotTarget, requestID)
	}
	return request, nil
}

func (t *stubTarget) record(c organizationCall) error {
	if t.during != nil {
		t.during()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, c)
	return t.err
}

func (t *stubTarget) recorded() []organizationCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]organizationCall(nil), t.calls...)
}

func (t *stubTarget) fail(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.err = err
	t.readErr = err
}

func (t *stubTarget) failAction(err error) { t.mu.Lock(); t.err = err; t.mu.Unlock() }

func (t *stubTarget) answer(request tenant.TransferRequest, found bool, list []tenant.TransferRequest, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.request, t.found, t.list, t.readErr = request, found, list, err
}

// stubOrganization records the owner transfers and organization deletions a
// case sends. during runs before the call is recorded, so a case can hold what
// had already happened (the audit row).
type stubOrganization struct {
	mu     sync.Mutex
	err    error
	calls  []organizationCall
	during func()

	pending      tenant.TransferRequest
	pendingFound bool
	pendingErr   error
}

func (o *stubOrganization) RequestTransfer(_ context.Context, id tenant.ID, target, expectedOwner string) (tenant.TransferRequest, error) {
	err := o.record(organizationCall{op: "RequestTransfer", id: id, owner: target, expectedOwner: expectedOwner})
	if err != nil {
		return tenant.TransferRequest{}, err
	}
	return tenant.TransferRequest{
		ID: "11111111-1111-1111-1111-111111111111", Tenant: id, From: expectedOwner, To: target,
		Status: tenant.TransferPending, RequestedAt: time.Unix(1, 0).UTC(), ExpiresAt: time.Unix(2, 0).UTC(),
	}, nil
}

func (o *stubOrganization) CancelTransfer(_ context.Context, id tenant.ID, requestID, expectedOwner string) error {
	return o.record(organizationCall{op: "CancelTransfer", id: id, request: requestID, expectedOwner: expectedOwner})
}

func (o *stubOrganization) PendingTransfer(_ context.Context, id tenant.ID) (tenant.TransferRequest, bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.pendingErr != nil {
		return tenant.TransferRequest{}, false, o.pendingErr
	}
	if !o.pendingFound {
		return tenant.TransferRequest{}, false, nil
	}
	request := o.pending
	request.Tenant = id
	return request, true, nil
}

func (o *stubOrganization) DeleteOrganization(_ context.Context, id tenant.ID, expectedOwner string) error {
	return o.record(organizationCall{op: "DeleteOrganization", id: id, expectedOwner: expectedOwner})
}

func (o *stubOrganization) record(c organizationCall) error {
	if o.during != nil {
		o.during()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, c)
	return o.err
}

func (o *stubOrganization) recorded() []organizationCall {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]organizationCall(nil), o.calls...)
}

func (o *stubOrganization) fail(err error) { o.mu.Lock(); o.err = err; o.mu.Unlock() }

func (r *rig) mount() *gin.Engine {
	r.t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	for _, rt := range r.p.routes() {
		engine.Handle(rt.method, rt.path, withRealm("public", rt.handler))
	}
	for _, rt := range r.p.tenantRoutes() {
		engine.Handle(rt.method, rt.path, withRealm("public", rt.handler))
	}
	for _, rt := range r.p.selfRoutes() {
		engine.Handle(rt.method, rt.path, withRealm("public", rt.handler))
	}
	return engine
}

func (r *rig) do(method, target string, body any, opts ...authntest.Option) *httptest.ResponseRecorder {
	r.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			r.t.Fatalf("encoding the body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	return r.send(method, target, reader, body != nil, authntest.Context(r.t, context.Background(), opts...))
}

func tenantPath(path, id string) string {
	return strings.Replace(path, ":tenant", id, 1)
}

func (r *rig) doInTenant(method, target, tenantID string, body any, opts ...authntest.Option) *httptest.ResponseRecorder {
	r.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			r.t.Fatalf("encoding the body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	ctx := tenantctx.WithID(authntest.Context(r.t, context.Background(), opts...), tenantID)
	return r.send(method, target, reader, body != nil, ctx)
}

func (r *rig) doRaw(method, target, raw string) *httptest.ResponseRecorder {
	r.t.Helper()
	return r.send(method, target, strings.NewReader(raw), true, authntest.Context(r.t, context.Background()))
}

func (r *rig) doAnonymous(method, target string) *httptest.ResponseRecorder {
	r.t.Helper()
	ctx := authntest.Anonymous(r.t, context.Background(), authn.AccessOptional)
	return r.send(method, target, nil, false, ctx)
}

func (r *rig) send(method, target string, reader io.Reader, jsonBody bool, ctx context.Context) *httptest.ResponseRecorder {
	r.t.Helper()
	req := httptest.NewRequest(method, target, reader)
	if jsonBody {
		req.Header.Set("Content-Type", "application/json")
	}
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	r.engine.ServeHTTP(w, req)
	return w
}

type fakeSignupVerification struct {
	mu     sync.Mutex
	state  authn.SignupState
	err    error
	reads  int
	lastID authn.AccountID
}

func (v *fakeSignupVerification) SignupStateOf(_ context.Context, id authn.AccountID) (authn.SignupStatus, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.reads++
	v.lastID = id
	return authn.SignupStatus{State: v.state}, v.err
}

func (v *fakeSignupVerification) PendingAccountByContact(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("tenant.admin: the pending-contact read is not part of this surface")
}

func (v *fakeSignupVerification) answer(state authn.SignupState, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.state, v.err = state, err
}

func (v *fakeSignupVerification) readCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.reads
}

func (v *fakeSignupVerification) lastAccount() authn.AccountID {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.lastID
}
