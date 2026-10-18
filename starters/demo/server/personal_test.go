package main

// The personal-tenant cases: the ensure's idempotence and eligibility at the
// unit level, over stand-ins for the five seams, and the whole flow through the
// program for the object routes it unblocks.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// The account the unit cases ensure for. It is not a UUID so the derived
// identifier is easy to read in a failure message.
const unitAccount = authn.AccountID("7c1e0f6a")

// The permissions the unit cases' catalog declares. The object-store pair is
// the one the integration case holds the tenant area to; the untranslatable one
// stands for a declared permission with no (object, action) row.
var (
	unitObjectRead  authz.Permission = "objectstore.object.read"
	unitObjectWrite authz.Permission = "objectstore.object.write"
	unitUnmapped    authz.Permission = "unmapped.permission"
)

// fakeRoster is the tenant.Catalog the unit cases use. Only Get and Create are
// called by the ensure; every other method panics through the embedded nil
// interface if a case ever reaches one.
type fakeRoster struct {
	tenant.Catalog

	mu      sync.Mutex
	rows    map[tenant.ID]tenant.Tenant
	creates int
}

func newFakeRoster() *fakeRoster {
	return &fakeRoster{rows: map[tenant.ID]tenant.Tenant{}}
}

func (r *fakeRoster) Get(_ context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, found := r.rows[id]
	return row, found, nil
}

func (r *fakeRoster) Create(_ context.Context, t tenant.Tenant) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, held := r.rows[t.ID]; held {
		return fmt.Errorf("%w: identifier %q", tenant.ErrTenantExists, t.ID)
	}
	r.creates++
	r.rows[t.ID] = t
	return nil
}

// fakeMemberships is the authn.Memberships the unit cases use.
type fakeMemberships struct {
	authn.Memberships

	mu   sync.Mutex
	rows map[authn.AccountID][]authn.Membership
	puts int
}

func newFakeMemberships() *fakeMemberships {
	return &fakeMemberships{rows: map[authn.AccountID][]authn.Membership{}}
}

func (m *fakeMemberships) Tenants(_ context.Context, id authn.AccountID) ([]authn.Membership, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]authn.Membership(nil), m.rows[id]...), nil
}

func (m *fakeMemberships) Put(_ context.Context, membership authn.Membership) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.puts++
	held := m.rows[membership.Account]
	for i := range held {
		if held[i].Tenant == membership.Tenant {
			held[i] = membership
			m.rows[membership.Account] = held
			return nil
		}
	}
	m.rows[membership.Account] = append(held, membership)
	return nil
}

// fakeManager is the authz.Manager the unit cases use.
type fakeManager struct {
	authz.Manager

	mu       sync.Mutex
	roles    map[string][]authz.RoleRecord
	bindings map[string][]authz.Binding

	putRoleErr error
	putRole    int
	putBinding int
}

func newFakeManager() *fakeManager {
	return &fakeManager{
		roles:    map[string][]authz.RoleRecord{},
		bindings: map[string][]authz.Binding{},
	}
}

func (m *fakeManager) Roles(_ context.Context, tenantID string) ([]authz.RoleRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]authz.RoleRecord(nil), m.roles[tenantID]...), nil
}

func (m *fakeManager) PutRole(_ context.Context, record authz.RoleRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.putRoleErr != nil {
		return m.putRoleErr
	}
	m.putRole++
	held := m.roles[record.Tenant]
	for i := range held {
		if held[i].Role == record.Role {
			held[i] = record
			m.roles[record.Tenant] = held
			return nil
		}
	}
	m.roles[record.Tenant] = append(held, record)
	return nil
}

func (m *fakeManager) BindingsOf(_ context.Context, tenantID, account string) ([]authz.Binding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []authz.Binding
	for _, binding := range m.bindings[tenantID] {
		if binding.Account == account {
			out = append(out, binding)
		}
	}
	return out, nil
}

func (m *fakeManager) PutBinding(_ context.Context, binding authz.Binding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.putBinding++
	held := m.bindings[binding.Tenant]
	for i := range held {
		if held[i].Account == binding.Account && held[i].Role == binding.Role {
			return nil
		}
	}
	m.bindings[binding.Tenant] = append(held, binding)
	return nil
}

// fakeSeeder is the authz.Seeder the unit cases use.
type fakeSeeder struct {
	authz.Seeder

	mu   sync.Mutex
	sets map[string]map[authz.Role]map[authz.Grant]struct{}
	seed int
}

func newFakeSeeder() *fakeSeeder {
	return &fakeSeeder{sets: map[string]map[authz.Role]map[authz.Grant]struct{}{}}
}

func (s *fakeSeeder) SeedGrants(_ context.Context, tenantID string, role authz.Role, grants []authz.Grant) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seed++
	if s.sets[tenantID] == nil {
		s.sets[tenantID] = map[authz.Role]map[authz.Grant]struct{}{}
	}
	if s.sets[tenantID][role] == nil {
		s.sets[tenantID][role] = map[authz.Grant]struct{}{}
	}
	added := 0
	for _, grant := range grants {
		if _, held := s.sets[tenantID][role][grant]; held {
			continue
		}
		s.sets[tenantID][role][grant] = struct{}{}
		added++
	}
	return added, nil
}

// fakeCatalog is the authz.Catalog the unit cases use.
type fakeCatalog struct {
	authz.Catalog
}

func (fakeCatalog) Declared() []authz.Permission {
	return []authz.Permission{unitObjectRead, unitObjectWrite, unitUnmapped}
}

func (fakeCatalog) ObjectAction(p authz.Permission) (string, string, bool) {
	switch p {
	case unitObjectRead:
		return "objects", "read", true
	case unitObjectWrite:
		return "objects", "write", true
	default:
		return "", "", false
	}
}

// unitProduct assembles a personal product over the fakes.
func unitProduct() (*personalProduct, *fakeRoster, *fakeMemberships, *fakeManager, *fakeSeeder) {
	roster := newFakeRoster()
	memberships := newFakeMemberships()
	manager := newFakeManager()
	seeder := newFakeSeeder()
	return &personalProduct{
		config:      personalConfig{Enabled: true},
		roster:      roster,
		memberships: memberships,
		manager:     manager,
		seeder:      seeder,
		catalog:     fakeCatalog{},
		logger:      log.Default(),
	}, roster, memberships, manager, seeder
}

// TestEnsureIsIdempotent holds a second call to completing nothing and changing
// nothing: one tenant row, one role, one binding, and the same tenant back.
func TestEnsureIsIdempotent(t *testing.T) {
	product, roster, _, manager, seeder := unitProduct()
	ctx := context.Background()

	first, err := product.ensure(ctx, unitAccount)
	if err != nil {
		t.Fatalf("the first ensure: %v", err)
	}
	if !first.Created {
		t.Errorf("the first ensure reports created=false, want true")
	}
	wantID := tenant.ID(personalIDPrefix + string(unitAccount))
	if first.Tenant.ID != wantID || first.Tenant.Alias != string(wantID) {
		t.Errorf("the tenant is %q/%q, want both %q", first.Tenant.ID, first.Tenant.Alias, wantID)
	}

	second, err := product.ensure(ctx, unitAccount)
	if err != nil {
		t.Fatalf("the second ensure: %v", err)
	}
	if second.Created {
		t.Errorf("the second ensure reports created=true, want false")
	}
	if second.Tenant.ID != first.Tenant.ID {
		t.Errorf("the second ensure answered %q, want the first's %q", second.Tenant.ID, first.Tenant.ID)
	}
	if roster.creates != 1 || len(roster.rows) != 1 {
		t.Errorf("the roster holds %d row(s) after %d create(s), want one and one",
			len(roster.rows), roster.creates)
	}
	if manager.putRole != 1 {
		t.Errorf("the owner role was written %d time(s), want once", manager.putRole)
	}
	if manager.putBinding != 1 {
		t.Errorf("the binding was written %d time(s), want once", manager.putBinding)
	}
	if seeder.seed != 2 {
		t.Errorf("the grants were seeded %d time(s), want once per ensure", seeder.seed)
	}
	grants := seeder.sets[string(wantID)][personalOwnerRole]
	for _, want := range []authz.Grant{
		{Resource: "objects", Action: "read"},
		{Resource: "objects", Action: "write"},
	} {
		if _, held := grants[want]; !held {
			t.Errorf("the owner role does not hold %+v; it holds %v", want, grants)
		}
	}
}

// TestEnsureCompletesAMidwayFailureOnTheNextCall holds the ensure's promise that
// earlier steps stay and the next call completes the rest.
func TestEnsureCompletesAMidwayFailureOnTheNextCall(t *testing.T) {
	product, roster, memberships, manager, seeder := unitProduct()
	ctx := context.Background()
	manager.putRoleErr = fmt.Errorf("the policy store is unavailable")

	if _, err := product.ensure(ctx, unitAccount); err == nil {
		t.Fatalf("the ensure succeeded while the role write failed")
	}
	wantID := string(personalTenantID(unitAccount))
	if _, found := roster.rows[tenant.ID(wantID)]; !found {
		t.Errorf("the tenant row did not survive the failure")
	}
	if len(memberships.rows[unitAccount]) != 1 {
		t.Errorf("the membership did not survive the failure")
	}
	if _, held := seeder.sets[wantID][personalOwnerRole]; held {
		t.Errorf("the grants were seeded after the role write failed")
	}

	manager.putRoleErr = nil
	result, err := product.ensure(ctx, unitAccount)
	if err != nil {
		t.Fatalf("the completing ensure: %v", err)
	}
	if result.Created {
		t.Errorf("the completing ensure reports created=true, want false: the tenant row was already there")
	}
	if _, found := roster.rows[tenant.ID(wantID)]; !found {
		t.Errorf("the completing ensure lost the tenant row")
	}
	if len(manager.roles[wantID]) != 1 {
		t.Errorf("the owner role is not there after the completing call")
	}
	if len(seeder.sets[wantID][personalOwnerRole]) != 2 {
		t.Errorf("the owner role holds %d grant(s), want the two translated permissions",
			len(seeder.sets[wantID][personalOwnerRole]))
	}
	if len(manager.bindings[wantID]) != 1 {
		t.Errorf("the account is not bound after the completing call")
	}
}

// TestEnsureRefusesAnAccountInAnotherTenant holds the eligibility rule: an
// account with a membership outside its personal tenant gets no tenant.
func TestEnsureRefusesAnAccountInAnotherTenant(t *testing.T) {
	product, roster, memberships, _, _ := unitProduct()
	memberships.rows[unitAccount] = []authn.Membership{
		{Account: unitAccount, Tenant: "acme", Active: true, Source: "invite"},
	}
	_, err := product.ensure(context.Background(), unitAccount)
	if !errors.Is(err, errAlreadyMember) {
		t.Fatalf("the ensure on an account of another tenant gave %v, want errAlreadyMember", err)
	}
	if len(roster.rows) != 0 {
		t.Errorf("a tenant was created for an account that already belongs to another: %v", roster.rows)
	}
}

// TestEnsureCreatesOneTenantUnderConcurrentCalls runs the ensure from several
// goroutines at once: the deterministic identifier makes them collide, one
// creates the row and the rest read it back.
func TestEnsureCreatesOneTenantUnderConcurrentCalls(t *testing.T) {
	product, roster, _, _, _ := unitProduct()
	const callers = 8
	results := make([]personalResult, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = product.ensure(context.Background(), unitAccount)
		}(i)
	}
	wg.Wait()

	created := 0
	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("caller %d failed: %v", i, errs[i])
		}
		if results[i].Tenant.ID != results[0].Tenant.ID {
			t.Errorf("caller %d answered %q, caller 0 answered %q", i, results[i].Tenant.ID, results[0].Tenant.ID)
		}
		if results[i].Created {
			created++
		}
	}
	if created != 1 {
		t.Errorf("%d callers report created=true, want exactly one", created)
	}
	if len(roster.rows) != 1 || roster.creates != 1 {
		t.Errorf("the roster holds %d row(s) after %d create(s), want one and one", len(roster.rows), roster.creates)
	}
}

// TestTheDerivedIdentifierFitsTheRostersDomain holds the identifier derivation
// to the two domains it has to satisfy: the account identifier's, at its longest
// admissible value, and the roster's, which refuses blanks and control
// characters and sets no length bound. Nothing is truncated.
func TestTheDerivedIdentifierFitsTheRostersDomain(t *testing.T) {
	longest := authn.AccountID(strings.Repeat("a", 64))
	id := personalTenantID(longest)
	if string(id) != personalIDPrefix+string(longest) {
		t.Errorf("the derived identifier is %q, want the prefix plus the whole account identifier", id)
	}
	if len(id) != len(personalIDPrefix)+64 {
		t.Errorf("the derived identifier is %d bytes, want %d (no truncation)", len(id), len(personalIDPrefix)+64)
	}
	if !tenant.IsIdentifier(string(id)) {
		t.Errorf("the roster's domain refuses the derived identifier %q", id)
	}
	if strings.ContainsAny(strings.TrimPrefix(string(id), personalIDPrefix), "@+ ") {
		t.Errorf("the derived identifier carries personal data: %q", id)
	}
}

// ginContext builds a gin test context whose request carries ctx.
func ginContext(ctx context.Context) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = (&http.Request{}).WithContext(ctx)
	c.Request.Method = http.MethodPost
	c.Request.URL = &url.URL{Path: personalRoutePath}
	return c, recorder
}

// TestTheEnsureAnswersAnImpersonatingCaller403 holds the handler's own refusal;
// the gateway lets an impersonating identity through this route so the handler
// can say 403 rather than the gateway's 401.
func TestTheEnsureAnswersAnImpersonatingCaller403(t *testing.T) {
	product, _, _, _, _ := unitProduct()
	ctx := authntest.Context(t, context.Background(),
		authntest.WithAccount("target-account"),
		authntest.WithActor("operator-account"),
		authntest.WithSession("session-1"),
		authntest.WithScope("acme"),
	)
	c, recorder := ginContext(ctx)
	product.handleEnsure(c)
	if recorder.Code != http.StatusForbidden {
		t.Errorf("an impersonating ensure answered %d, want 403:\n%s", recorder.Code, recorder.Body.String())
	}
}

// TestTheEnsureAnswersAnotherMembership409 holds the handler's mapping of the
// eligibility refusal.
func TestTheEnsureAnswersAnotherMembership409(t *testing.T) {
	product, _, memberships, _, _ := unitProduct()
	memberships.rows[unitAccount] = []authn.Membership{
		{Account: unitAccount, Tenant: "acme", Active: true, Source: "invite"},
	}
	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
	c, recorder := ginContext(ctx)
	product.handleEnsure(c)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("an account of another tenant answered %d, want 409:\n%s", recorder.Code, recorder.Body.String())
	}
	var problem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("the 409 body is not JSON: %v\n%s", err, recorder.Body.String())
	}
	if problem.Type != personalProblemPrefix+alreadyMemberCode {
		t.Errorf("the 409 carries type %q, want %q", problem.Type, personalProblemPrefix+alreadyMemberCode)
	}
}

// TestTheEnsureAnswersTheTenantAndKind holds the success answer's shape.
func TestTheEnsureAnswersTheTenantAndKind(t *testing.T) {
	product, _, _, _, _ := unitProduct()
	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
	c, recorder := ginContext(ctx)
	product.handleEnsure(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("the ensure answered %d, want 200:\n%s", recorder.Code, recorder.Body.String())
	}
	var reply personalReply
	if err := json.Unmarshal(recorder.Body.Bytes(), &reply); err != nil {
		t.Fatalf("the answer is not JSON: %v\n%s", err, recorder.Body.String())
	}
	wantID := string(personalTenantID(unitAccount))
	if reply.Tenant.ID != wantID || reply.Tenant.Alias != wantID {
		t.Errorf("the answer names %q/%q, want both %q", reply.Tenant.ID, reply.Tenant.Alias, wantID)
	}
	if reply.Tenant.Kind != personalKind {
		t.Errorf("the answer's kind is %q, want %q", reply.Tenant.Kind, personalKind)
	}
	if !reply.Created {
		t.Errorf("the first ensure answered created=false, want true")
	}
}

// personalServe starts a seeded serve with the personal tenant switched as the
// caller asks, and answers the backend base URL and the bearer token of an
// account with no membership (the configuration's second seed account).
func personalServe(t *testing.T, enabled bool) (string, string) {
	t.Helper()
	ports := freePorts(t, listenerCount)
	dsn := filepath.Join(t.TempDir(), "demo.db")
	config := writeConfigWith(t, ports, dsn)
	if enabled {
		config = writeConfigReplacing(t, ports, dsn, "    enabled: false", "    enabled: true")
	}
	if res := runCommand(t, config, "sqlite-memory", seedCommand); res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	serve := start(t, config, "sqlite-memory", serveCommand)
	serve.waitReady(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	return base, signIn(t, base, hashIdentifier, seedPassword)
}

// personalCall invokes the ensure and answers the decoded reply.
type personalCallReply struct {
	Tenant struct {
		ID    string `json:"id"`
		Alias string `json:"alias"`
		Kind  string `json:"kind"`
	} `json:"tenant"`
	Created bool `json:"created"`
}

// TestThePersonalTenantUnblocksTheObjectRoutes is the fail-before case: an
// account with no membership reads the object list as a 404, the ensure creates
// its personal tenant, and upload, list and delete then succeed under that
// tenant. A second ensure after the fact, which is what a page reload makes,
// reads the same tenant back.
func TestThePersonalTenantUnblocksTheObjectRoutes(t *testing.T) {
	base, token := personalServe(t, true)
	client := &http.Client{Timeout: 10 * time.Second}

	// The fail-before: the account has no membership, so the admission layer
	// answers a tenant-scoped read with its bare 404.
	noMembership := map[string]string{
		"Authorization": "Bearer " + token,
		"X-Tenant":      "demo",
	}
	if res := send(t, client, http.MethodGet, base+"/api/v1/objects", nil, noMembership); res.status != http.StatusNotFound {
		t.Fatalf("the object list before the ensure answered %d, want the admission layer's 404:\n%s",
			res.status, res.body)
	}

	auth := map[string]string{"Authorization": "Bearer " + token}
	first := send(t, client, http.MethodPost, base+personalRoutePath, nil, auth)
	if first.status != http.StatusOK {
		t.Fatalf("the ensure answered %d, want 200:\n%s", first.status, first.body)
	}
	var reply personalCallReply
	if err := json.Unmarshal(first.body, &reply); err != nil {
		t.Fatalf("the ensure's answer is not JSON: %v\n%s", err, reply.Tenant.ID)
	}
	if !reply.Created {
		t.Errorf("the first ensure answered created=false, want true")
	}
	if reply.Tenant.Kind != personalKind {
		t.Errorf("the ensure's kind is %q, want %q", reply.Tenant.Kind, personalKind)
	}
	if !strings.HasPrefix(reply.Tenant.ID, personalIDPrefix) || reply.Tenant.ID != reply.Tenant.Alias {
		t.Errorf("the personal tenant is %q/%q, want one personal-prefixed string",
			reply.Tenant.ID, reply.Tenant.Alias)
	}

	// A second call -- the one a page reload makes -- reads the same tenant
	// back and creates nothing.
	again := send(t, client, http.MethodPost, base+personalRoutePath, nil, auth)
	if again.status != http.StatusOK {
		t.Fatalf("the second ensure answered %d, want 200:\n%s", again.status, again.body)
	}
	var second personalCallReply
	if err := json.Unmarshal(again.body, &second); err != nil {
		t.Fatalf("the second answer is not JSON: %v\n%s", err, again.body)
	}
	if second.Tenant.ID != reply.Tenant.ID || second.Created {
		t.Errorf("the second ensure answered %+v, want the same tenant %q and created=false",
			second, reply.Tenant.ID)
	}

	// The tenant's owner holds every declared permission, the object store's
	// pair included: the tenant-scoped object routes work.
	tenantHeaders := map[string]string{
		"Authorization": "Bearer " + token,
		"X-Tenant":      reply.Tenant.Alias,
	}
	if res := send(t, client, http.MethodGet, base+"/api/v1/objects", nil, tenantHeaders); res.status != http.StatusOK {
		t.Fatalf("the object list after the ensure answered %d, want 200:\n%s", res.status, res.body)
	}

	body := []byte("hello")
	declared := send(t, client, http.MethodPost, base+"/api/v1/objects", map[string]any{
		"name":         "personal.txt",
		"size":         len(body),
		"content_type": "text/plain",
	}, tenantHeaders)
	if declared.status != http.StatusCreated {
		t.Fatalf("declaring an object answered %d, want 201:\n%s", declared.status, declared.body)
	}
	var declaration struct {
		Object struct {
			ID string `json:"id"`
		} `json:"object"`
	}
	if err := json.Unmarshal(declared.body, &declaration); err != nil || declaration.Object.ID == "" {
		t.Fatalf("reading the declaration's object: %v\n%s", err, declared.body)
	}

	uploaded := sendBytes(t, client, http.MethodPut,
		base+"/api/v1/objects/"+declaration.Object.ID+"/content", body, map[string]string{
			"Authorization": "Bearer " + token,
			"X-Tenant":      reply.Tenant.Alias,
			"Content-Type":  "application/octet-stream",
		})
	if uploaded.status != http.StatusNoContent {
		t.Fatalf("uploading the object answered %d, want 204:\n%s", uploaded.status, uploaded.body)
	}
	completed := send(t, client, http.MethodPost,
		base+"/api/v1/objects/"+declaration.Object.ID+"/complete", nil, tenantHeaders)
	if completed.status != http.StatusOK {
		t.Fatalf("completing the object answered %d, want 200:\n%s", completed.status, completed.body)
	}
	list := send(t, client, http.MethodGet, base+"/api/v1/objects", nil, tenantHeaders)
	if list.status != http.StatusOK {
		t.Fatalf("listing the objects answered %d, want 200:\n%s", list.status, list.body)
	}
	var listed struct {
		Objects []struct {
			ID string `json:"id"`
		} `json:"objects"`
	}
	if err := json.Unmarshal(list.body, &listed); err != nil {
		t.Fatalf("the object list is not JSON: %v\n%s", err, list.body)
	}
	found := false
	for _, object := range listed.Objects {
		if object.ID == declaration.Object.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("the listed objects do not carry %q:\n%s", declaration.Object.ID, list.body)
	}
	if res := send(t, client, http.MethodDelete,
		base+"/api/v1/objects/"+declaration.Object.ID, nil, tenantHeaders); res.status != http.StatusNoContent {
		t.Fatalf("deleting the object answered %d, want 204:\n%s", res.status, res.body)
	}
}

// TestTheEnsureRefusesAnAccountOfAnotherTenant runs the eligibility rule through
// the program: the configuration's second account is given a membership in the
// deployment's tenant, and the ensure then refuses it rather than adding a
// second tenant.
//
// The seed wires the administrator's authorization bindings but no authn
// membership for either account, so the membership is written through the
// account administration route -- the same path an operator uses -- with the
// administrator's token.
func TestTheEnsureRefusesAnAccountOfAnotherTenant(t *testing.T) {
	base, memberToken := personalServe(t, true)
	client := &http.Client{Timeout: 10 * time.Second}
	operatorToken := signIn(t, base, seedIdentifier, seedPassword)

	profile := send(t, client, http.MethodGet, base+"/auth/me", nil,
		map[string]string{"Authorization": "Bearer " + memberToken})
	if profile.status != http.StatusOK {
		t.Fatalf("reading the member's profile answered %d:\n%s", profile.status, profile.body)
	}
	var who struct {
		AccountID string `json:"account_id"`
	}
	if err := json.Unmarshal(profile.body, &who); err != nil || who.AccountID == "" {
		t.Fatalf("reading the member's account identifier: %v\n%s", err, profile.body)
	}

	added := send(t, client, http.MethodPut,
		base+"/auth/admin/tenants/demo/members/"+who.AccountID,
		map[string]any{"active": true},
		map[string]string{"Authorization": "Bearer " + operatorToken})
	if added.status != http.StatusNoContent {
		t.Fatalf("writing the membership answered %d, want 204:\n%s", added.status, added.body)
	}

	res := send(t, client, http.MethodPost,
		base+personalRoutePath, nil, map[string]string{"Authorization": "Bearer " + memberToken})
	if res.status != http.StatusConflict {
		t.Fatalf("the member's ensure answered %d, want 409:\n%s", res.status, res.body)
	}
	if !strings.Contains(string(res.body), "already-member") {
		t.Errorf("the 409 body does not name already-member:\n%s", res.body)
	}
}

// TestThePersonalTenantRouteIsAbsentWithTheSwitchOff holds the switch: the
// prefix stays claimed, and the route is not mounted, so an authenticated call
// answers the entry point's 404.
func TestThePersonalTenantRouteIsAbsentWithTheSwitchOff(t *testing.T) {
	base, token := personalServe(t, false)
	res := send(t, &http.Client{Timeout: 10 * time.Second}, http.MethodPost,
		base+personalRoutePath, nil, map[string]string{"Authorization": "Bearer " + token})
	if res.status != http.StatusNotFound {
		t.Fatalf("the ensure with the switch off answered %d, want 404:\n%s", res.status, res.body)
	}
}

// sendBytes sends a request whose body is the raw bytes, not JSON.
func sendBytes(t *testing.T, client *http.Client, method, url string, body []byte,
	headers map[string]string,
) response {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building %s %s: %v", method, url, err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = res.Body.Close() }()
	answer, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading the body of %s %s: %v", method, url, err)
	}
	return response{status: res.StatusCode, body: answer}
}
