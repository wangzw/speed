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
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
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

// fakeRoster is the tenant.Catalog the unit cases use. Get and Create are the
// rows' store, and lagging hides a row Get would otherwise find, standing for a
// snapshot that predates it. EnterRegistered clears that; every other method
// panics through the embedded nil interface if a case ever reaches one.
type fakeRoster struct {
	tenant.Catalog

	mu        sync.Mutex
	rows      map[tenant.ID]tenant.Tenant
	lagging   map[tenant.ID]bool
	createErr error
	creates   int
	entered   int
}

func newFakeRoster() *fakeRoster {
	return &fakeRoster{
		rows:    map[tenant.ID]tenant.Tenant{},
		lagging: map[tenant.ID]bool{},
	}
}

func (r *fakeRoster) Get(_ context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, found := r.rows[id]
	if found && r.lagging[id] {
		return tenant.Tenant{}, false, nil
	}
	return row, found, nil
}

func (r *fakeRoster) Create(_ context.Context, t tenant.Tenant) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.createErr != nil {
		return r.createErr
	}
	if _, held := r.rows[t.ID]; held {
		return fmt.Errorf("%w: identifier %q", tenant.ErrTenantExists, t.ID)
	}
	r.creates++
	r.rows[t.ID] = t
	return nil
}

func (r *fakeRoster) EnterRegistered(ctx context.Context, id tenant.ID) (context.Context, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entered++
	if _, found := r.rows[id]; !found {
		return ctx, tenant.ErrInvalidTenant
	}
	delete(r.lagging, id)
	return ctx, nil
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

// fakeGrants is the grant table the manager and the seeding surface share, as
// the two share one in the process: the ensure reads what it seeded.
type fakeGrants struct {
	mu   sync.Mutex
	sets map[string]map[authz.Role]map[authz.Grant]struct{}
}

func newFakeGrants() *fakeGrants {
	return &fakeGrants{sets: map[string]map[authz.Role]map[authz.Grant]struct{}{}}
}

// held returns the grants a role holds, for a case to assert on.
func (g *fakeGrants) held(tenantID string, role authz.Role) []authz.Grant {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]authz.Grant, 0, len(g.sets[tenantID][role]))
	for grant := range g.sets[tenantID][role] {
		out = append(out, grant)
	}
	return out
}

// fakeManager is the authz.Manager the unit cases use.
type fakeManager struct {
	authz.Manager

	mu       sync.Mutex
	roles    map[string][]authz.RoleRecord
	bindings map[string][]authz.Binding
	grants   *fakeGrants

	putRoleErr error
	putRole    int
	putBinding int
	deleted    int
}

func newFakeManager(grants *fakeGrants) *fakeManager {
	return &fakeManager{
		roles:    map[string][]authz.RoleRecord{},
		bindings: map[string][]authz.Binding{},
		grants:   grants,
	}
}

func (m *fakeManager) Roles(_ context.Context, tenantID string) ([]authz.RoleRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]authz.RoleRecord(nil), m.roles[tenantID]...), nil
}

func (m *fakeManager) Grants(_ context.Context, tenantID string, role authz.Role) ([]authz.GrantRecord, error) {
	out := make([]authz.GrantRecord, 0)
	for _, grant := range m.grants.held(tenantID, role) {
		out = append(out, authz.GrantRecord{
			Tenant: tenantID, Role: role, Resource: grant.Resource, Action: grant.Action,
		})
	}
	return out, nil
}

func (m *fakeManager) DeleteGrant(_ context.Context, grant authz.GrantRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted++
	m.grants.mu.Lock()
	defer m.grants.mu.Unlock()
	delete(m.grants.sets[grant.Tenant][grant.Role], authz.Grant{
		Resource: grant.Resource, Action: grant.Action,
	})
	return nil
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

// fakeSeeder is the authz.Seeder the unit cases use. It writes into the same
// grant table the manager reads.
type fakeSeeder struct {
	authz.Seeder

	grants *fakeGrants

	mu   sync.Mutex
	seed int
}

func newFakeSeeder(grants *fakeGrants) *fakeSeeder {
	return &fakeSeeder{grants: grants}
}

func (s *fakeSeeder) SeedGrants(_ context.Context, tenantID string, role authz.Role, grants []authz.Grant) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seed++
	s.grants.mu.Lock()
	defer s.grants.mu.Unlock()
	if s.grants.sets[tenantID] == nil {
		s.grants.sets[tenantID] = map[authz.Role]map[authz.Grant]struct{}{}
	}
	if s.grants.sets[tenantID][role] == nil {
		s.grants.sets[tenantID][role] = map[authz.Grant]struct{}{}
	}
	added := 0
	for _, grant := range grants {
		if _, held := s.grants.sets[tenantID][role][grant]; held {
			continue
		}
		s.grants.sets[tenantID][role][grant] = struct{}{}
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
		return "object", "read", true
	case unitObjectWrite:
		return "object", "write", true
	default:
		return "", "", false
	}
}

// fakeSignupVerification answers the acting account's self-registration state
// with what a case scripts, and counts the reads. The zero value reports
// SignupNone, which is what an account created outside self-registration reads
// as.
type fakeSignupVerification struct {
	mu    sync.Mutex
	state authn.SignupState
	err   error
	reads int
}

func (v *fakeSignupVerification) SignupStateOf(context.Context, authn.AccountID) (authn.SignupStatus, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.reads++
	return authn.SignupStatus{State: v.state}, v.err
}

// PendingAccountByContact is not part of this surface.
func (v *fakeSignupVerification) PendingAccountByContact(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("workspace.personal: the pending-contact read is not part of this surface")
}

// answer scripts the state and the failure the next reads report.
func (v *fakeSignupVerification) answer(state authn.SignupState, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.state, v.err = state, err
}

// unitProduct assembles a personal product over the fakes. The verification
// read reports no self-registration, which is the state the eligibility cases
// that are not about verification run in.
func unitProduct() (*personalProduct, *fakeRoster, *fakeMemberships, *fakeManager, *fakeSeeder) {
	roster := newFakeRoster()
	memberships := newFakeMemberships()
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	seeder := newFakeSeeder(grants)
	return &personalProduct{
		config:       personalConfig{Enabled: true},
		roster:       roster,
		memberships:  memberships,
		manager:      manager,
		seeder:       seeder,
		catalog:      fakeCatalog{},
		verification: &fakeSignupVerification{},
		logger:       log.Default(),
	}, roster, memberships, manager, seeder
}

// TestEnsureIsIdempotent holds a second call to completing nothing and changing
// nothing: one tenant row, one role, one binding, and the same tenant back.
func TestEnsureIsIdempotent(t *testing.T) {
	product, roster, memberships, manager, seeder := unitProduct()
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
	// The membership this call writes is the account's whole tie to the tenant:
	// active, in the derived tenant, and carrying the source value the account
	// module publishes to its consumers. The literal is pinned rather than the
	// constant the code writes, so a change to either the row or the constant
	// fails here.
	held := memberships.rows[unitAccount]
	if len(held) != 1 {
		t.Fatalf("the account holds %d membership(s), want one", len(held))
	}
	if held[0].Tenant != string(wantID) || !held[0].Active || held[0].Source != "personal" {
		t.Errorf("the membership is %+v, want tenant %q, active, source %q",
			held[0], wantID, "personal")
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
	grants := seeder.grants.held(string(wantID), personalOwnerRole)
	for _, want := range []authz.Grant{
		{Resource: "object", Action: "read"},
		{Resource: "object", Action: "write"},
	} {
		if !slices.Contains(grants, want) {
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
	if held := seeder.grants.held(wantID, personalOwnerRole); len(held) != 0 {
		t.Errorf("the grants were seeded after the role write failed: %v", held)
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
	if held := seeder.grants.held(wantID, personalOwnerRole); len(held) != 2 {
		t.Errorf("the owner role holds %d grant(s), want the two allowlisted permissions: %v",
			len(held), held)
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

// TestEnsureCatchesTheRosterSnapshotUp holds the read-back path after a create
// the roster's table already held: the first Get reads a snapshot that predates
// the row, EnterRegistered rereads the table, and the second Get finds it. The
// call succeeds with created=false instead of answering 503.
func TestEnsureCatchesTheRosterSnapshotUp(t *testing.T) {
	product, roster, _, _, _ := unitProduct()
	ctx := context.Background()
	id := personalTenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusActive}
	roster.lagging[id] = true

	result, err := product.ensure(ctx, unitAccount)
	if err != nil {
		t.Fatalf("the ensure over a lagging snapshot failed: %v", err)
	}
	if result.Created {
		t.Errorf("the ensure reports created=true, want false: the table already held the row")
	}
	if result.Tenant.ID != id {
		t.Errorf("the ensure answered %q, want %q", result.Tenant.ID, id)
	}
	if roster.entered != 1 {
		t.Errorf("the roster was caught up %d time(s), want once", roster.entered)
	}
}

// TestEnsureKeepsAMembershipTheOperatorWrote holds the ensure's promise not to
// overwrite an operator's decision: a membership row that exists is left as it
// stands, whether it is suspended or active, and its source and SSO exemption
// are not rewritten.
func TestEnsureKeepsAMembershipTheOperatorWrote(t *testing.T) {
	cases := []struct {
		name       string
		membership authn.Membership
	}{
		{
			name: "suspended by an administrator",
			membership: authn.Membership{
				Account: unitAccount, Active: false, Source: "admin", SSOExempt: true,
			},
		},
		{
			name: "active with another source",
			membership: authn.Membership{
				Account: unitAccount, Active: true, Source: "invite", SSOExempt: true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			product, roster, memberships, manager, seeder := unitProduct()
			id := personalTenantID(unitAccount)
			roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusActive}
			want := tc.membership
			want.Tenant = string(id)
			memberships.rows[unitAccount] = []authn.Membership{want}

			result, err := product.ensure(context.Background(), unitAccount)
			if err != nil {
				t.Fatalf("the ensure over an existing membership failed: %v", err)
			}
			if result.Created {
				t.Errorf("the ensure reports created=true, want false")
			}
			if memberships.puts != 0 {
				t.Errorf("the ensure wrote the membership %d time(s), want none", memberships.puts)
			}
			held := memberships.rows[unitAccount]
			if len(held) != 1 || held[0] != want {
				t.Errorf("the membership is %+v, want the operator's row %+v", held, want)
			}
			// The rest of the tenant is still completed.
			if manager.putRole != 1 || manager.putBinding != 1 || seeder.seed != 1 {
				t.Errorf("the ensure completed the rest %d role(s), %d binding(s), %d seeding(s), want one of each",
					manager.putRole, manager.putBinding, seeder.seed)
			}
		})
	}
}

// TestEnsureWritesNothingForADisabledTenant holds the withdrawal path: an
// operator disables the personal tenant, and the ensure answers
// tenant-unavailable while writing nothing at all.
func TestEnsureWritesNothingForADisabledTenant(t *testing.T) {
	product, roster, memberships, manager, seeder := unitProduct()
	id := personalTenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusDisabled}

	_, err := product.ensure(context.Background(), unitAccount)
	if !errors.Is(err, errTenantUnavailable) {
		t.Fatalf("the ensure over a disabled tenant gave %v, want errTenantUnavailable", err)
	}
	if memberships.puts != 0 || manager.putRole != 0 || manager.putBinding != 0 || seeder.seed != 0 {
		t.Errorf("the ensure wrote %d membership(s), %d role(s), %d binding(s), %d seeding(s), want none",
			memberships.puts, manager.putRole, manager.putBinding, seeder.seed)
	}
	if roster.rows[id].Status != tenant.StatusDisabled {
		t.Errorf("the ensure changed the tenant's status to %v", roster.rows[id].Status)
	}
}

// TestEnsureWritesNothingForADisabledTenantWithAMembership holds the same
// withdrawal on the typical scene: the account already holds its personal
// membership row, so a page reload reads the tenant back instead of taking the
// create branch. The refusal and the empty write set are the same.
func TestEnsureWritesNothingForADisabledTenantWithAMembership(t *testing.T) {
	product, roster, memberships, manager, seeder := unitProduct()
	id := personalTenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusDisabled}
	memberships.rows[unitAccount] = []authn.Membership{
		{Account: unitAccount, Tenant: string(id), Active: true, Source: "admin"},
	}

	_, err := product.ensure(context.Background(), unitAccount)
	if !errors.Is(err, errTenantUnavailable) {
		t.Fatalf("the ensure over a disabled tenant with a membership gave %v, want errTenantUnavailable", err)
	}
	if memberships.puts != 0 || manager.putRole != 0 || manager.putBinding != 0 || seeder.seed != 0 {
		t.Errorf("the ensure wrote %d membership(s), %d role(s), %d binding(s), %d seeding(s), want none",
			memberships.puts, manager.putRole, manager.putBinding, seeder.seed)
	}
	if roster.creates != 0 {
		t.Errorf("the ensure registered a tenant over the disabled row")
	}
	if roster.rows[id].Status != tenant.StatusDisabled {
		t.Errorf("the ensure changed the tenant's status to %v", roster.rows[id].Status)
	}
}

// TestEnsureReadsBackThePersonalTenantBesideAnotherMembership holds the
// eligibility rule for an account that already has its personal tenant and
// later joined another one: the personal tenant is read back, and the call does
// not answer already-member.
func TestEnsureReadsBackThePersonalTenantBesideAnotherMembership(t *testing.T) {
	product, _, memberships, _, _ := unitProduct()
	ctx := context.Background()
	first, err := product.ensure(ctx, unitAccount)
	if err != nil {
		t.Fatalf("the first ensure: %v", err)
	}
	memberships.rows[unitAccount] = append(memberships.rows[unitAccount],
		authn.Membership{Account: unitAccount, Tenant: "beta", Active: true, Source: "invite"})

	second, err := product.ensure(ctx, unitAccount)
	if err != nil {
		t.Fatalf("the ensure after joining another tenant failed: %v", err)
	}
	if second.Created || second.Tenant.ID != first.Tenant.ID {
		t.Errorf("the second ensure answered %+v, want the same tenant %q and created=false",
			second, first.Tenant.ID)
	}
}

// TestEnsureConvergesTheOwnersGrantsToTheAllowlist holds the reconciliation an
// older tenant gets: a grant outside the host's allowlist is revoked on the
// next ensure, and the allowlist's own grants stay.
func TestEnsureConvergesTheOwnersGrantsToTheAllowlist(t *testing.T) {
	product, _, _, manager, seeder := unitProduct()
	ctx := context.Background()
	if _, err := product.ensure(ctx, unitAccount); err != nil {
		t.Fatalf("the first ensure: %v", err)
	}
	id := string(personalTenantID(unitAccount))
	extra := authz.Grant{Resource: "account", Action: "read"}
	seeder.grants.mu.Lock()
	seeder.grants.sets[id][personalOwnerRole][extra] = struct{}{}
	seeder.grants.mu.Unlock()

	if _, err := product.ensure(ctx, unitAccount); err != nil {
		t.Fatalf("the second ensure: %v", err)
	}
	if manager.deleted != 1 {
		t.Errorf("the ensure revoked %d grant(s), want the one outside the allowlist", manager.deleted)
	}
	held := seeder.grants.held(id, personalOwnerRole)
	if len(held) != 2 {
		t.Errorf("the owner role holds %v, want the allowlist's two grants", held)
	}
	if slices.Contains(held, extra) {
		t.Errorf("the grant outside the allowlist survived: %v", held)
	}
}

// TestTheEnsureRefusesAServiceAccount holds the account-kind refusal: a
// client-secret identity is a non-interactive API client and gets no workspace,
// and nothing is written for it.
func TestTheEnsureRefusesAServiceAccount(t *testing.T) {
	product, roster, memberships, manager, seeder := unitProduct()
	ctx := authntest.Context(t, context.Background(),
		authntest.WithAccount("svc-automation"),
		authntest.WithMethods(authn.MethodClientSecret),
	)
	c, recorder := ginContext(ctx)
	serveEnsure(product, c)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("a service account's ensure answered %d, want 403:\n%s", recorder.Code, recorder.Body.String())
	}
	if roster.creates != 0 || memberships.puts != 0 || manager.putRole != 0 ||
		manager.putBinding != 0 || seeder.seed != 0 {
		t.Errorf("the refused ensure wrote %d tenant(s), %d membership(s), %d role(s), %d binding(s), %d seeding(s)",
			roster.creates, memberships.puts, manager.putRole, manager.putBinding, seeder.seed)
	}
}

// TestTheEnsureAnswersAnIdentifierConflict409 holds the mapping of the two
// states a retry cannot change: an alias another tenant holds, and a derived
// identifier outside the roster's domain.
func TestTheEnsureAnswersAnIdentifierConflict409(t *testing.T) {
	t.Run("the alias is held by another tenant", func(t *testing.T) {
		product, roster, _, _, _ := unitProduct()
		roster.createErr = fmt.Errorf("%w: another tenant holds it", tenant.ErrAliasConflict)
		ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
		c, recorder := ginContext(ctx)
		serveEnsure(product, c)
		assertPersonalType(t, recorder, http.StatusConflict, identifierUnavailableCode)
	})
	t.Run("the derived identifier is outside the roster's domain", func(t *testing.T) {
		product, _, _, _, _ := unitProduct()
		_, err := product.ensure(context.Background(), authn.AccountID("outside the domain"))
		if !errors.Is(err, errIdentifierUnavailable) {
			t.Fatalf("the ensure gave %v, want errIdentifierUnavailable", err)
		}
	})
}

// TestTheEnsureAnswers401AndForbiddenCallers holds the two refusals the handler
// makes before the ensure runs, and the refusal of a static-table subject.
func TestTheEnsureAnswers401AndForbiddenCallers(t *testing.T) {
	product, roster, memberships, manager, seeder := unitProduct()

	c, recorder := ginContext(context.Background())
	serveEnsure(product, c)
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("an unauthenticated ensure answered %d, want 401", recorder.Code)
	}

	static := authntest.Context(t, context.Background(),
		authntest.WithAccount("static.ops"),
		authntest.WithBackend("static"),
	)
	c, recorder = ginContext(static)
	serveEnsure(product, c)
	if recorder.Code != http.StatusForbidden {
		t.Errorf("a static subject's ensure answered %d, want 403", recorder.Code)
	}
	if roster.creates != 0 || memberships.puts != 0 || manager.putRole != 0 ||
		manager.putBinding != 0 || seeder.seed != 0 {
		t.Errorf("a refused ensure wrote something")
	}
}

// TestTheEnsureAnswersADependencyFailure503WithoutTheCause holds the failure
// mapping: a dependency error is a 503 whose body does not repeat the cause.
func TestTheEnsureAnswersADependencyFailure503WithoutTheCause(t *testing.T) {
	product, roster, _, _, _ := unitProduct()
	roster.createErr = errors.New("the tenant table is unavailable")
	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
	c, recorder := ginContext(ctx)
	serveEnsure(product, c)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("a dependency failure answered %d, want 503:\n%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "the tenant table is unavailable") {
		t.Errorf("the 503 body repeats the cause:\n%s", recorder.Body.String())
	}
}

// assertPersonalType holds one 409's problem type.
func assertPersonalType(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("the ensure answered %d, want %d:\n%s", recorder.Code, status, recorder.Body.String())
	}
	var problem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("the answer is not JSON: %v\n%s", err, recorder.Body.String())
	}
	if want := personalProblemPrefix + code; problem.Type != want {
		t.Errorf("the answer carries type %q, want %q", problem.Type, want)
	}
}

// assertProblemType holds one answer's status and its problem type, the type
// given whole rather than under this surface's workspace prefix: the
// verification-required refusal carries the authentication capability's code.
func assertProblemType(t *testing.T, recorder *httptest.ResponseRecorder, status int, want string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("the ensure answered %d, want %d:\n%s", recorder.Code, status, recorder.Body.String())
	}
	var problem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("the answer is not JSON: %v\n%s", err, recorder.Body.String())
	}
	if problem.Type != want {
		t.Errorf("the answer carries type %q, want %q", problem.Type, want)
	}
}

// The stand-ins the wiring case hands the product. The registry resolves an
// optional capability by what a module provides, so each one is a module of its
// own supplying its instance.
type stubEndpoint struct{ routes []string }

func (e *stubEndpoint) Route(method, path string, _ ...gin.HandlerFunc) {
	e.routes = append(e.routes, method+" "+path)
}

func (e *stubEndpoint) Use(speedhttp.Middleware) {}

func (e *stubEndpoint) Accepting() bool { return true }

type stubRouter struct{ endpoint *stubEndpoint }

func (r *stubRouter) Endpoint(name string) (speedhttp.Endpoint, error) {
	if name != personalEndpoint {
		return nil, fmt.Errorf("%w: %q", speedhttp.ErrUnknownEndpoint, name)
	}
	return r.endpoint, nil
}

func (r *stubRouter) Endpoints() []string { return []string{personalEndpoint} }

type stubGate struct{ impersonable []string }

func (g *stubGate) Exempt(string, string, string)   {}
func (g *stubGate) Optional(string, string, string) {}
func (g *stubGate) Impersonable(endpoint, method, path string) {
	g.impersonable = append(g.impersonable, endpoint+" "+method+" "+path)
}

type stubBoard struct{ posted []string }

func (b *stubBoard) Post(module, name string) { b.posted = append(b.posted, module+" "+name) }

// TestTheWireRegistersTheRouteTheGateAndTheFeature holds the three writes the
// module makes when it is switched on: the route, the gateway's let-through for
// an impersonating caller, and the conditional feature the frontend reads to
// trigger the ensure.
func TestTheWireRegistersTheRouteTheGateAndTheFeature(t *testing.T) {
	reg := core.New()
	reg.Register(core.Module{
		Name:     "test.router",
		Provides: []core.Provision{{Token: (*speedhttp.Router)(nil)}},
	})
	reg.Register(core.Module{
		Name:     "test.gate",
		Provides: []core.Provision{{Token: (*authn.Gate)(nil)}},
	})
	reg.Register(core.Module{
		Name:     "test.board",
		Provides: []core.Provision{{Token: (*feature.Board)(nil)}},
	})
	endpoint := &stubEndpoint{}
	router := &stubRouter{endpoint: endpoint}
	gate := &stubGate{}
	board := &stubBoard{}
	reg.Supply(router, "test.router")
	reg.Supply(gate, "test.gate")
	reg.Supply(board, "test.board")

	product := &personalProduct{
		config: personalConfig{Enabled: true},
		logger: log.Default(),
	}
	if err := product.wire(reg); err != nil {
		t.Fatalf("the wiring failed: %v", err)
	}
	if want := []string{http.MethodPost + " " + personalRoutePath}; !slices.Equal(endpoint.routes, want) {
		t.Errorf("the endpoint carries %v, want %v", endpoint.routes, want)
	}
	if want := []string{personalEndpoint + " " + http.MethodPost + " " + personalRoutePath}; !slices.Equal(gate.impersonable, want) {
		t.Errorf("the gateway was told %v, want %v", gate.impersonable, want)
	}
	if want := []string{personalModuleName + " " + personalFeature}; !slices.Equal(board.posted, want) {
		t.Errorf("the board was told %v, want %v", board.posted, want)
	}
}

// TestTheWireRegistersNothingWithTheSwitchOff holds the switch: nothing is
// mounted and no feature is posted.
func TestTheWireRegistersNothingWithTheSwitchOff(t *testing.T) {
	reg := core.New()
	reg.Register(core.Module{
		Name:     "test.router",
		Provides: []core.Provision{{Token: (*speedhttp.Router)(nil)}},
	})
	reg.Register(core.Module{
		Name:     "test.gate",
		Provides: []core.Provision{{Token: (*authn.Gate)(nil)}},
	})
	reg.Register(core.Module{
		Name:     "test.board",
		Provides: []core.Provision{{Token: (*feature.Board)(nil)}},
	})
	endpoint := &stubEndpoint{}
	gate := &stubGate{}
	board := &stubBoard{}
	reg.Supply(&stubRouter{endpoint: endpoint}, "test.router")
	reg.Supply(gate, "test.gate")
	reg.Supply(board, "test.board")

	product := &personalProduct{config: personalConfig{Enabled: false}, logger: log.Default()}
	if err := product.wire(reg); err != nil {
		t.Fatalf("the wiring with the switch off failed: %v", err)
	}
	if len(endpoint.routes) != 0 || len(gate.impersonable) != 0 || len(board.posted) != 0 {
		t.Errorf("the switched-off module mounted %v, registered %v, posted %v",
			endpoint.routes, gate.impersonable, board.posted)
	}
}

// serveEnsure runs the operation's bound handler against c, the way the entry
// point does: the handler receives the request's context and the entry point
// writes the declared answer.
func serveEnsure(product *personalProduct, c *gin.Context) {
	personalEnsureOperation.Handle(product.handleEnsure)(c)
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
	serveEnsure(product, c)
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
	serveEnsure(product, c)
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

// TestTheEnsureAnswersADisabledTenant409 holds the handler's mapping of the
// withdrawal refusal. The frontend tells the three 409s apart by the type
// field, so the code it keys on is written out here rather than taken from the
// constant the handler also uses: a changed constant is a changed contract.
func TestTheEnsureAnswersADisabledTenant409(t *testing.T) {
	product, roster, memberships, manager, seeder := unitProduct()
	id := personalTenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusDisabled}
	memberships.rows[unitAccount] = []authn.Membership{
		{Account: unitAccount, Tenant: string(id), Active: true, Source: "admin"},
	}

	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
	c, recorder := ginContext(ctx)
	serveEnsure(product, c)
	assertPersonalType(t, recorder, http.StatusConflict, "tenant-unavailable")
	if memberships.puts != 0 || manager.putRole != 0 || manager.putBinding != 0 || seeder.seed != 0 {
		t.Errorf("the refused ensure wrote %d membership(s), %d role(s), %d binding(s), %d seeding(s), want none",
			memberships.puts, manager.putRole, manager.putBinding, seeder.seed)
	}
}

// TestTheEnsureAnswersTheTenantAndKind holds the success answer's shape.
func TestTheEnsureAnswersTheTenantAndKind(t *testing.T) {
	product, _, _, _, _ := unitProduct()
	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
	c, recorder := ginContext(ctx)
	serveEnsure(product, c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("the ensure answered %d, want 200:\n%s", recorder.Code, recorder.Body.String())
	}
	var reply WorkspacePersonalReply
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

	// The tenant's owner holds the host's allowlist, the object store's pair
	// included: the tenant-scoped object routes work.
	tenantHeaders := map[string]string{
		"Authorization": "Bearer " + token,
		"X-Tenant":      reply.Tenant.Alias,
	}
	if res := send(t, client, http.MethodGet, base+"/api/v1/objects", nil, tenantHeaders); res.status != http.StatusOK {
		t.Fatalf("the object list after the ensure answered %d, want 200:\n%s", res.status, res.body)
	}

	// The conditional feature is posted while the route is mounted: it is what
	// the frontend reads before it triggers the ensure.
	features := send(t, client, http.MethodGet, base+"/api/v1/features", nil, auth)
	if features.status != http.StatusOK {
		t.Fatalf("reading the feature document answered %d:\n%s", features.status, features.body)
	}
	var document struct {
		Features []string `json:"features"`
	}
	if err := json.Unmarshal(features.body, &document); err != nil {
		t.Fatalf("the feature document is not JSON: %v\n%s", err, features.body)
	}
	if !slices.Contains(document.Features, personalFeature) {
		t.Errorf("the feature document with the switch on does not carry %q: %s",
			personalFeature, features.body)
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

	profile := send(t, client, http.MethodGet, base+"/api/v1/auth/me", nil,
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
		base+"/api/v1/tenants/demo/members/"+who.AccountID,
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

// TestTheOwnerHoldsTheAllowlistAndCannotManageTheTenant runs the authorization
// consequence of the personal tenant through the program: the owner role holds
// exactly the host's allowlist, and the account holding it is refused the
// tenant's membership and invitation management, which every self-registered
// account would otherwise wield in its own tenant.
func TestTheOwnerHoldsTheAllowlistAndCannotManageTheTenant(t *testing.T) {
	base, memberToken := personalServe(t, true)
	client := &http.Client{Timeout: 10 * time.Second}
	memberAuth := map[string]string{"Authorization": "Bearer " + memberToken}

	ensured := send(t, client, http.MethodPost, base+personalRoutePath, nil, memberAuth)
	if ensured.status != http.StatusOK {
		t.Fatalf("the ensure answered %d, want 200:\n%s", ensured.status, ensured.body)
	}
	var reply personalCallReply
	if err := json.Unmarshal(ensured.body, &reply); err != nil {
		t.Fatalf("the ensure's answer is not JSON: %v\n%s", err, ensured.body)
	}
	personalID := reply.Tenant.ID

	operatorToken := signIn(t, base, seedIdentifier, seedPassword)
	operatorAuth := map[string]string{"Authorization": "Bearer " + operatorToken}

	// The role's grants, read by the administrator through the policy surface,
	// are the allowlist's two (object, action) pairs and nothing else.
	grants := send(t, client, http.MethodGet,
		base+"/api/v1/system/tenants/"+personalID+"/roles/"+string(personalOwnerRole)+"/grants",
		nil, operatorAuth)
	if grants.status != http.StatusOK {
		t.Fatalf("reading the owner role's grants answered %d:\n%s", grants.status, grants.body)
	}
	var listed struct {
		Grants []struct {
			Resource string `json:"resource"`
			Action   string `json:"action"`
		} `json:"grants"`
	}
	if err := json.Unmarshal(grants.body, &listed); err != nil {
		t.Fatalf("the grant list is not JSON: %v\n%s", err, grants.body)
	}
	held := make(map[string]bool, len(listed.Grants))
	for _, grant := range listed.Grants {
		held[grant.Resource+"/"+grant.Action] = true
	}
	want := map[string]bool{"object/read": true, "object/write": true}
	if len(held) != len(want) {
		t.Errorf("the owner role holds %v, want exactly the allowlist %v", held, want)
	}
	for pair := range want {
		if !held[pair] {
			t.Errorf("the owner role does not hold the allowlisted %q; it holds %v", pair, held)
		}
	}

	// The administrator's account identifier, to attempt a membership write
	// that a tenant administrator would be allowed.
	profile := send(t, client, http.MethodGet, base+"/api/v1/auth/me", nil, operatorAuth)
	if profile.status != http.StatusOK {
		t.Fatalf("reading the administrator's profile answered %d:\n%s", profile.status, profile.body)
	}
	var who struct {
		AccountID string `json:"account_id"`
	}
	if err := json.Unmarshal(profile.body, &who); err != nil || who.AccountID == "" {
		t.Fatalf("reading the administrator's account identifier: %v\n%s", err, profile.body)
	}

	added := send(t, client, http.MethodPut,
		base+"/api/v1/tenants/"+personalID+"/members/"+who.AccountID,
		map[string]any{"active": true}, memberAuth)
	if added.status != http.StatusForbidden {
		t.Errorf("the personal tenant's owner wrote a membership and answered %d, want 403:\n%s",
			added.status, added.body)
	}

	invited := send(t, client, http.MethodPost,
		base+"/api/v1/tenants/"+personalID+"/invitations",
		map[string]any{"email": "someone@example.test"}, memberAuth)
	if invited.status != http.StatusForbidden {
		t.Errorf("the personal tenant's owner issued an invitation and answered %d, want 403:\n%s",
			invited.status, invited.body)
	}
}

// TestThePersonalTenantRouteIsAbsentWithTheSwitchOff holds the switch: the
// prefix stays claimed, and the route is not mounted, so an authenticated call
// answers the entry point's 404.
func TestThePersonalTenantRouteIsAbsentWithTheSwitchOff(t *testing.T) {
	base, token := personalServe(t, false)
	client := &http.Client{Timeout: 10 * time.Second}
	res := send(t, client, http.MethodPost,
		base+personalRoutePath, nil, map[string]string{"Authorization": "Bearer " + token})
	if res.status != http.StatusNotFound {
		t.Fatalf("the ensure with the switch off answered %d, want 404:\n%s", res.status, res.body)
	}

	// The conditional feature is not posted while the route is not mounted.
	features := send(t, client, http.MethodGet, base+"/api/v1/features", nil,
		map[string]string{"Authorization": "Bearer " + token})
	if features.status != http.StatusOK {
		t.Fatalf("reading the feature document answered %d:\n%s", features.status, features.body)
	}
	var document struct {
		Features []string `json:"features"`
	}
	if err := json.Unmarshal(features.body, &document); err != nil {
		t.Fatalf("the feature document is not JSON: %v\n%s", err, features.body)
	}
	if slices.Contains(document.Features, personalFeature) {
		t.Errorf("the feature document with the switch off carries %q: %s",
			personalFeature, features.body)
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

// signupOf narrows the product's verification read to the fake the unit cases
// deliver, so a case can script the acting account's state.
func signupOf(t *testing.T, product *personalProduct) *fakeSignupVerification {
	t.Helper()
	v, ok := product.verification.(*fakeSignupVerification)
	if !ok {
		t.Fatalf("the verification read is %T, not the unit fake", product.verification)
	}
	return v
}

// TestTheEnsureRefusesAnUnverifiedRegistration409 holds the registration gate:
// an account whose self-registration has not proved an email address is answered
// 409 verification-required and nothing is written — no tenant, no membership,
// no role, no binding, no seeding. The verified case below creates the same
// tenant, which is what makes this refusal the gate's.
func TestTheEnsureRefusesAnUnverifiedRegistration409(t *testing.T) {
	product, roster, memberships, manager, seeder := unitProduct()
	signupOf(t, product).answer(authn.SignupPending, nil)
	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
	c, recorder := ginContext(ctx)
	serveEnsure(product, c)
	assertProblemType(t, recorder, http.StatusConflict, personalProblemPrefix + verificationRequiredCode)
	if roster.creates != 0 || memberships.puts != 0 || manager.putRole != 0 ||
		manager.putBinding != 0 || seeder.seed != 0 {
		t.Errorf("the refused ensure wrote %d tenant(s), %d membership(s), %d role(s), %d binding(s), %d seeding(s)",
			roster.creates, memberships.puts, manager.putRole, manager.putBinding, seeder.seed)
	}
}

// TestTheEnsureCreatesTheTenantOnceVerificationIsProved holds the recovery: the
// same account that was refused while pending creates its personal tenant once
// its email address has been proved.
func TestTheEnsureCreatesTheTenantOnceVerificationIsProved(t *testing.T) {
	product, roster, _, _, _ := unitProduct()
	signup := signupOf(t, product)
	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))

	signup.answer(authn.SignupPending, nil)
	c, recorder := ginContext(ctx)
	serveEnsure(product, c)
	assertProblemType(t, recorder, http.StatusConflict, personalProblemPrefix + verificationRequiredCode)
	if roster.creates != 0 {
		t.Fatalf("the pending ensure registered %d tenant(s), want none", roster.creates)
	}

	signup.answer(authn.SignupVerified, nil)
	c, recorder = ginContext(ctx)
	serveEnsure(product, c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("the verified ensure answered %d, want 200:\n%s", recorder.Code, recorder.Body.String())
	}
	if roster.creates != 1 {
		t.Errorf("the verified ensure registered %d tenant(s), want 1", roster.creates)
	}
}

// TestAnUnreadableSignupStateIs503 holds the failure direction: a state that
// cannot be read is a 503 and nothing is written, never read as verified.
func TestAnUnreadableSignupStateIs503(t *testing.T) {
	product, roster, memberships, manager, seeder := unitProduct()
	signupOf(t, product).answer(authn.SignupNone, errors.New("the database is down"))
	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
	c, recorder := ginContext(ctx)
	serveEnsure(product, c)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("the ensure answered %d, want 503:\n%s", recorder.Code, recorder.Body.String())
	}
	if roster.creates != 0 || memberships.puts != 0 || manager.putRole != 0 ||
		manager.putBinding != 0 || seeder.seed != 0 {
		t.Errorf("the ensure wrote %d tenant(s), %d membership(s), %d role(s), %d binding(s), %d seeding(s)",
			roster.creates, memberships.puts, manager.putRole, manager.putBinding, seeder.seed)
	}
}

// TestTheEnsureAnswersAnAccountWithNoRow503 holds the sentinel's chosen
// direction for this surface: authn.ErrAccountNotFound is the 503 the handler
// writes, not a state to read past. Unlike the capabilities' management faces,
// which treat "no account row" as "no self-registration" and let their pipeline
// answer, this ensure is an authenticated account's own provisioning, so a
// missing row is an anomaly and no tenant is written for it.
func TestTheEnsureAnswersAnAccountWithNoRow503(t *testing.T) {
	product, roster, memberships, manager, seeder := unitProduct()
	signupOf(t, product).answer(authn.SignupNone, authn.ErrAccountNotFound)
	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
	c, recorder := ginContext(ctx)
	serveEnsure(product, c)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("the ensure for an account with no row answered %d, want 503:\n%s",
			recorder.Code, recorder.Body.String())
	}
	if roster.creates != 0 || memberships.puts != 0 || manager.putRole != 0 ||
		manager.putBinding != 0 || seeder.seed != 0 {
		t.Errorf("the refused ensure wrote %d tenant(s), %d membership(s), %d role(s), %d binding(s), %d seeding(s)",
			roster.creates, memberships.puts, manager.putRole, manager.putBinding, seeder.seed)
	}
}

// TestTheVerificationGatePrecedesTheEligibilityRefusal holds the order: a
// pending caller that also belongs to another tenant is answered the
// verification refusal, not already-member. Without the gate first the answer
// would carry the workspace's already-member type, so the type distinguishes the
// two.
func TestTheVerificationGatePrecedesTheEligibilityRefusal(t *testing.T) {
	product, roster, memberships, _, _ := unitProduct()
	memberships.rows[unitAccount] = []authn.Membership{
		{Account: unitAccount, Tenant: "acme", Active: true, Source: "invite"},
	}
	signupOf(t, product).answer(authn.SignupPending, nil)
	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
	c, recorder := ginContext(ctx)
	serveEnsure(product, c)
	assertProblemType(t, recorder, http.StatusConflict, personalProblemPrefix + verificationRequiredCode)
	if roster.creates != 0 || memberships.puts != 0 {
		t.Errorf("the refused ensure wrote %d tenant(s), %d membership(s), want none",
			roster.creates, memberships.puts)
	}
}

// TestTheVerificationGatePrecedesThePersonalTenantReadBack holds the order on
// the reload scene: a pending account that already holds its personal tenant is
// answered the verification refusal rather than reading the tenant back. The
// gate runs before the membership read, so no write is attempted either.
func TestTheVerificationGatePrecedesThePersonalTenantReadBack(t *testing.T) {
	product, roster, memberships, manager, seeder := unitProduct()
	id := personalTenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusActive}
	memberships.rows[unitAccount] = []authn.Membership{
		{Account: unitAccount, Tenant: string(id), Active: true, Source: "personal"},
	}
	signupOf(t, product).answer(authn.SignupPending, nil)
	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
	c, recorder := ginContext(ctx)
	serveEnsure(product, c)
	assertProblemType(t, recorder, http.StatusConflict, personalProblemPrefix + verificationRequiredCode)
	if roster.creates != 0 || memberships.puts != 0 || manager.putRole != 0 ||
		manager.putBinding != 0 || seeder.seed != 0 {
		t.Errorf("the refused ensure wrote %d tenant(s), %d membership(s), %d role(s), %d binding(s), %d seeding(s)",
			roster.creates, memberships.puts, manager.putRole, manager.putBinding, seeder.seed)
	}
}
