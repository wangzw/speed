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

// The stubs implement the seams the module takes up, so a stub and an interface
// cannot drift apart without failing the build.
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

// stubTenantAdminGuard is the tenant administration guard's stand-in: the
// path-tenant reconciliation is the real guard's work and a case drives the
// handler with the tenant already in the context, so this guard passes through.
type stubTenantAdminGuard struct{}

func (stubTenantAdminGuard) GuardTenantAdmin(next gin.HandlerFunc) gin.HandlerFunc { return next }

// catalogCall is one call the stub roster recorded: the method's name and the
// arguments that name a row, so a case can hold what the handler passed on and
// that an action the module refused never reached the roster.
type catalogCall struct {
	op      string
	id      tenant.ID
	owner   string
	tenant  tenant.Tenant
	status  tenant.Status
	profile tenant.Profile
}

// stubCatalog is a stand-in tenant.Catalog. It records every call, answers reads
// from what a case set, and fails with the error a case set. A per-method error
// overrides the general one, so a case can let a read succeed while the write it
// guards against fails.
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

// store records the row a successful provisioner call wrote, so the create
// route's read-back finds it as it would find a row the real roster holds.
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

// errOr returns a method's own error when it was set, and the general one
// otherwise.
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

// NameOf implements tenant.NameResolver over the rows a case set, so a view
// or an audit row names the name the fixture carries.
func (s *stubCatalog) NameOf(_ context.Context, id tenant.ID) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A case drives a handler directly, so the path segment it passes is taken
	// as the tenant in either spelling; the name is the answer either way.
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

// stubGuard is a stand-in guard.Guard. It records the permission every decision
// asked about, and the domain it decided in, and answers from one setting:
// allow, deny, or fail. The write-out matches the real guard's — a 403 for a
// denial and a 503 for an outage — so a handler that returns on a shortfall
// writes the same answer.
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

// recordingAudit keeps the rows it was asked to write and can be told to fail,
// which is how a case holds that a write whose mandatory row did not land
// reached no roster.
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

// mountedRoute is one (method, path) a registration recorded.
type mountedRoute struct{ method, path string }

// stubEndpoint records the routes registered on it.
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

// stubRouter is a stand-in speedhttp.Router over a fixed set of endpoints. A
// name it does not hold is an error, the way the real entry point answers an
// undeclared endpoint.
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

// loggerStub delivers the logging capability over a recorder, so the module's
// startup diagnostic lands in a case's buffer rather than on the test's output.
type loggerStub struct{ logger *slog.Logger }

func (l loggerStub) Named(string) *slog.Logger { return l.logger }
func (loggerStub) Redaction() log.Redaction    { return noRules{} }

// noRules takes redaction registrations and masks nothing: a case reads the
// records themselves, not their masking.
type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

// logBuffer collects what the module logs, so a case can hold the module's own
// records apart from the test's output.
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

// decode reads a JSON response body into target.
func decode(t *testing.T, w *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), target); err != nil {
		t.Fatalf("decoding the response %q: %v", w.Body.String(), err)
	}
}

// problem is the default problem-details shape this module's refusals carry.
type problem struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
}

// problemOf reads the problem a recorder's body holds.
func problemOf(t *testing.T, w *httptest.ResponseRecorder) problem {
	t.Helper()
	var p problem
	decode(t, w, &p)
	return p
}

// standIn is a module that delivers instance for the tokens named, the way
// another module delivers its capability exclusively.
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

// rig is the module with every dependency a case drives, mounted on a real gin
// engine so a case reaches it over a real HTTP surface: the real routing, the
// real path parameters and the real JSON binding, with the roster and the other
// capabilities stood in for.
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
	// signup answers the acting account's self-registration state. Its zero
	// value reports SignupNone, which is what an account created outside
	// self-registration reads as.
	signup *fakeSignupVerification
}

// newRig builds the module over the stubs and mounts its route set on a real
// engine, with one endpoint declared on the router.
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

// organizationCall is one call the organization writer recorded.
type organizationCall struct {
	op            string
	id            tenant.ID
	owner         string
	request       string
	expectedOwner string
}

// stubTarget is the target's account-level side. It records every action and
// answers the reads a case scripts.
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

// failAction makes the next confirmation or decline fail while the reads still
// answer what a case scripted.
func (t *stubTarget) failAction(err error) { t.mu.Lock(); t.err = err; t.mu.Unlock() }

// answer scripts the reads: the one request, the list and any read failure.
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

	// The pending read's answer, scripted by a case.
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

// mount registers the module's whole route set on a fresh gin engine, the way
// the real entry point mounts them, and binds the realm each handler reads. A
// conflict among the routes panics here, which is how a case holds that the
// route set is mountable.
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

// do sends one request through the engine with the identity the options build
// and, when a body is given, a JSON body.
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

// tenantPath resolves a mounted route's :tenant parameter to a concrete
// identifier, the way a request line carries it.
func tenantPath(path, id string) string {
	return strings.Replace(path, ":tenant", id, 1)
}

// doInTenant sends one request through the engine with the resolved tenant the
// tenant-surface routes read, the identity the options build and, when a body is
// given, a JSON body.
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

// doRaw sends a body as written, so a case can put a body the handler cannot
// read in front of it.
func (r *rig) doRaw(method, target, raw string) *httptest.ResponseRecorder {
	r.t.Helper()
	return r.send(method, target, strings.NewReader(raw), true, authntest.Context(r.t, context.Background()))
}

// doAnonymous sends one request with no identity on it, an optional-access
// anonymous request, the way one reaches a management route the middleware let
// through.
func (r *rig) doAnonymous(method, target string) *httptest.ResponseRecorder {
	r.t.Helper()
	ctx := authntest.Anonymous(r.t, context.Background(), authn.AccessOptional)
	return r.send(method, target, nil, false, ctx)
}

// send builds the request, installs the context and drives the engine.
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

// fakeSignupVerification answers the acting account's self-registration state
// with what a case scripts, and counts the reads, so a case can hold that a
// granted reader was not asked. The zero value reports SignupNone with no
// failure, which is what an account created outside self-registration reads as.
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

// PendingAccountByContact is not part of this surface.
func (v *fakeSignupVerification) PendingAccountByContact(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("tenant.admin: the pending-contact read is not part of this surface")
}

// answer scripts the state and the failure the next reads report.
func (v *fakeSignupVerification) answer(state authn.SignupState, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.state, v.err = state, err
}

// readCount is how many state reads the fake answered.
func (v *fakeSignupVerification) readCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.reads
}

// lastAccount is the account the last state read named, which a case holds to
// the request's actor rather than its impersonation target.
func (v *fakeSignupVerification) lastAccount() authn.AccountID {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.lastID
}
