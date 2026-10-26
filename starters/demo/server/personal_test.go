package main

// The personal-tenant cases: the ensure's idempotence and eligibility at the
// unit level, over stand-ins for the five seams, and the whole flow through the
// program for the tenant-scoped routes it unblocks.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	"github.com/vislake/speed/pkg/audit"
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

	steps          *stepLog
	statusCalls    int
	statusWrites   int
	setStatusErr   error
	deletes        int
	deleteAttempts int
	deleteErr      error
}

func newFakeRoster() *fakeRoster {
	return &fakeRoster{
		rows:    map[tenant.ID]tenant.Tenant{},
		lagging: map[tenant.ID]bool{},
	}
}

// SetStatusByAccount is the roster's account-driven transition: a disable is
// written only from active and an enable only from a disable the account made,
// so an operator's disable is left standing. A tombstone is ErrTenantDeleted.
func (r *fakeRoster) SetStatusByAccount(_ context.Context, id tenant.ID, s tenant.Status) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statusCalls++
	r.steps.add("status:" + s.String())
	if r.setStatusErr != nil {
		return r.setStatusErr
	}
	row, found := r.rows[id]
	if !found {
		return fmt.Errorf("%w: identifier %q", tenant.ErrInvalidTenant, id)
	}
	switch row.Status {
	case tenant.StatusDeleted:
		return fmt.Errorf("%w: identifier %q", tenant.ErrTenantDeleted, id)
	case tenant.StatusDisabled:
		if s == tenant.StatusActive && row.DisabledBy == tenant.DisabledByAccount {
			row.Status = tenant.StatusActive
			row.DisabledBy = tenant.DisabledByOperator
			r.rows[id] = row
			r.statusWrites++
		}
	case tenant.StatusActive:
		if s == tenant.StatusDisabled {
			row.Status = tenant.StatusDisabled
			row.DisabledBy = tenant.DisabledByAccount
			r.rows[id] = row
			r.statusWrites++
		}
	}
	return nil
}

// Delete writes the tombstone. An identifier no row carries is ErrInvalidTenant,
// a tombstone is no work, and the alias and the disable source are left as they
// are.
func (r *fakeRoster) Delete(_ context.Context, id tenant.ID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleteAttempts++
	r.steps.add("delete")
	if r.deleteErr != nil {
		return r.deleteErr
	}
	row, found := r.rows[id]
	if !found {
		return fmt.Errorf("%w: identifier %q", tenant.ErrInvalidTenant, id)
	}
	if row.Status == tenant.StatusDeleted {
		return nil
	}
	row.Status = tenant.StatusDeleted
	r.rows[id] = row
	r.deletes++
	return nil
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
	r.steps.add("create")
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

	steps      *stepLog
	pages      int
	removes    int
	membersErr error
	removeErr  error
}

func newFakeMemberships() *fakeMemberships {
	return &fakeMemberships{rows: map[authn.AccountID][]authn.Membership{}}
}

// Members lists one page of a tenant's memberships in account order, the way
// the account module does: After is the last account identifier of the page
// before.
func (m *fakeMemberships) Members(_ context.Context, tenantID string, page authn.Page) ([]authn.Membership, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pages++
	m.steps.add("members")
	if m.membersErr != nil {
		return nil, m.membersErr
	}
	var all []authn.Membership
	for _, held := range m.rows {
		for _, membership := range held {
			if membership.Tenant == tenantID {
				all = append(all, membership)
			}
		}
	}
	slices.SortFunc(all, func(a, b authn.Membership) int {
		return strings.Compare(string(a.Account), string(b.Account))
	})
	start := 0
	for start < len(all) && page.After != "" && string(all[start].Account) <= page.After {
		start++
	}
	all = all[start:]
	if limit := page.EffectiveLimit(); len(all) > limit {
		all = all[:limit]
	}
	return append([]authn.Membership(nil), all...), nil
}

// Remove deletes one membership.
func (m *fakeMemberships) Remove(_ context.Context, id authn.AccountID, tenantID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removes++
	m.steps.add("remove:" + id.String())
	if m.removeErr != nil {
		return m.removeErr
	}
	held := m.rows[id]
	kept := held[:0]
	for _, membership := range held {
		if membership.Tenant != tenantID {
			kept = append(kept, membership)
		}
	}
	if len(kept) == 0 {
		delete(m.rows, id)
	} else {
		m.rows[id] = kept
	}
	return nil
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

// IsMember reads one membership's active state, the way the account module
// does: an absent row and a suspended one both answer false.
func (m *fakeMemberships) IsMember(_ context.Context, id authn.AccountID, tenantID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, held := range m.rows[id] {
		if held.Tenant == tenantID {
			return held.Active, nil
		}
	}
	return false, nil
}

// Membership reads one membership row.
func (m *fakeMemberships) Membership(_ context.Context, tenantID string, id authn.AccountID) (authn.Membership, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, held := range m.rows[id] {
		if held.Tenant == tenantID {
			return held, true, nil
		}
	}
	return authn.Membership{}, false, nil
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

	steps             *stepLog
	bindingsErr       error
	deleteBindErr     error
	deleteBindingsErr error
	deletedBindings   int
	deleteRoleErr     error
	deletedRoles      int
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
			if held[i].Builtin {
				return fmt.Errorf("%w: the role %q of tenant %q is builtin",
					authz.ErrBuiltinRole, record.Role, record.Tenant)
			}
			held[i] = record
			m.roles[record.Tenant] = held
			return nil
		}
	}
	m.roles[record.Tenant] = append(held, record)
	return nil
}

// MarkRoleBuiltin sets the builtin mark on an existing role; an absent role is
// ErrUnknownRole, and an already-builtin role is no work.
func (m *fakeManager) MarkRoleBuiltin(_ context.Context, tenantID string, role authz.Role) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps.add("mark:" + string(role))
	held := m.roles[tenantID]
	for i := range held {
		if held[i].Role == role {
			held[i].Builtin = true
			m.roles[tenantID] = held
			return nil
		}
	}
	return fmt.Errorf("%w: the role %q of tenant %q is not there", authz.ErrUnknownRole, role, tenantID)
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

// Bindings lists every binding of a tenant.
func (m *fakeManager) Bindings(_ context.Context, tenantID string) ([]authz.Binding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps.add("bindings")
	if m.bindingsErr != nil {
		return nil, m.bindingsErr
	}
	return append([]authz.Binding(nil), m.bindings[tenantID]...), nil
}

// DeleteBinding removes one binding, and removing one that is not there is no
// work. The last tenant-administrator binding of a domain is refused, as the
// policy manager refuses it.
func (m *fakeManager) DeleteBinding(_ context.Context, tenantID, account string, role authz.Role) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deletedBindings++
	m.steps.add("unbind:" + account + ":" + string(role))
	if m.deleteBindErr != nil {
		return m.deleteBindErr
	}
	held := m.bindings[tenantID]
	if role == authz.TenantAdminRole {
		admins, target := 0, false
		for _, binding := range held {
			if binding.Role != authz.TenantAdminRole {
				continue
			}
			admins++
			if binding.Account == account {
				target = true
			}
		}
		if target && admins == 1 {
			return fmt.Errorf("%w: account %q is the last %q of tenant %q",
				authz.ErrLastTenantAdmin, account, authz.TenantAdminRole, tenantID)
		}
	}
	kept := held[:0]
	for _, binding := range held {
		if binding.Account == account && binding.Role == role {
			continue
		}
		kept = append(kept, binding)
	}
	m.bindings[tenantID] = kept
	return nil
}

// ClearAccountBindings removes one account's bindings within a tenant, the
// host's account-deletion path that the last-tenant-administrator rule does not
// constrain.
func (m *fakeManager) ClearAccountBindings(_ context.Context, tenantID, account string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps.add("unbind:" + account)
	if m.deleteBindingsErr != nil {
		return m.deleteBindingsErr
	}
	kept := m.bindings[tenantID][:0]
	for _, binding := range m.bindings[tenantID] {
		if binding.Account == account {
			m.deletedBindings++
			continue
		}
		kept = append(kept, binding)
	}
	m.bindings[tenantID] = kept
	return nil
}

// DeleteRole removes a role with its grants and bindings. A builtin role is
// refused with authz.ErrBuiltinRole; a role that is not there is no work.
func (m *fakeManager) DeleteRole(_ context.Context, tenantID string, role authz.Role) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deletedRoles++
	m.steps.add("role:" + string(role))
	if m.deleteRoleErr != nil {
		return m.deleteRoleErr
	}
	held := m.roles[tenantID]
	for _, record := range held {
		if record.Role == role && record.Builtin {
			return fmt.Errorf("%w: the role %q of tenant %q is builtin",
				authz.ErrBuiltinRole, role, tenantID)
		}
	}
	kept := held[:0]
	for _, record := range held {
		if record.Role != role {
			kept = append(kept, record)
		}
	}
	m.roles[tenantID] = kept
	m.grants.mu.Lock()
	delete(m.grants.sets[tenantID], role)
	m.grants.mu.Unlock()
	bindings := m.bindings[tenantID]
	keptBindings := bindings[:0]
	for _, binding := range bindings {
		if binding.Role != role {
			keptBindings = append(keptBindings, binding)
		}
	}
	m.bindings[tenantID] = keptBindings
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

// fakeCatalog is the authz.Catalog the unit cases use. The tenant-admin allowlist is
// empty, so no permission is ever translated into a grant; the two methods
// exist because the seam requires them, and they answer that nothing is
// declared and nothing maps.
type fakeCatalog struct {
	authz.Catalog
}

func (fakeCatalog) Declared() []authz.Permission {
	return nil
}

func (fakeCatalog) ObjectAction(authz.Permission) (string, string, bool) {
	return "", "", false
}

// stepLog records the order writes reached the stand-ins, so a case can pin a
// fixed sequence across seams that are separate objects.
type stepLog struct {
	mu    sync.Mutex
	steps []string
}

func (l *stepLog) add(step string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.steps = append(l.steps, step)
}

func (l *stepLog) snapshot() []string {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.steps...)
}

func (l *stepLog) reset() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.steps = nil
}

// recordedAudit is the audit.Audit the unit cases use. It keeps every accepted
// entry and whether the context carried an identity, and fails on demand.
type recordedAudit struct {
	mu         sync.Mutex
	entries    []audit.Entry
	identities []bool
	err        error
	steps      *stepLog
}

func newRecordedAudit() *recordedAudit { return &recordedAudit{} }

func (a *recordedAudit) Record(ctx context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	_, identified := authn.FromContext(ctx)
	a.entries = append(a.entries, e)
	a.identities = append(a.identities, identified)
	a.steps.add("audit:" + e.Action + ":" + e.Outcome)
	return nil
}

// rows answers the entries accepted so far.
func (a *recordedAudit) rows() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.entries...)
}

// unitProduct assembles a personal product over the fakes with the switch on.
// The verification read reports no self-registration, which is the state the
// eligibility cases that are not about verification run in.
func unitProduct() (*personalProduct, *fakeRoster, *fakeMemberships, *fakeManager, *fakeSeeder) {
	return unitProductWith(personalConfig{Enabled: true})
}

// unitProductWith assembles the product over the fakes with a configuration of
// the case's choosing. The lifecycle hooks must not follow the route's switch,
// so the cases that hold that build the product with the switch off.
func unitProductWith(cfg personalConfig) (*personalProduct, *fakeRoster, *fakeMemberships, *fakeManager, *fakeSeeder) {
	steps := &stepLog{}
	roster := newFakeRoster()
	roster.steps = steps
	memberships := newFakeMemberships()
	memberships.steps = steps
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	manager.steps = steps
	seeder := newFakeSeeder(grants)
	auditor := newRecordedAudit()
	auditor.steps = steps
	return &personalProduct{
		config:      cfg,
		roster:      roster,
		memberships: memberships,
		manager:     manager,
		seeder:      seeder,
		catalog:     fakeCatalog{},
		audit:       auditor,
		logger:      log.Default(),
		seen:        newPersonalSeenEvents(personalSeenEventsCapacity),
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
		t.Errorf("the tenant-admin role was written %d time(s), want once", manager.putRole)
	}
	if manager.putBinding != 1 {
		t.Errorf("the binding was written %d time(s), want once", manager.putBinding)
	}
	if seeder.seed != 2 {
		t.Errorf("the grants were seeded %d time(s), want once per ensure", seeder.seed)
	}
	if grants := seeder.grants.held(string(wantID), personalTenantAdminRole); len(grants) != 0 {
		t.Errorf("the tenant-admin role holds %v, want the empty allowlist", grants)
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
	if held := seeder.grants.held(wantID, personalTenantAdminRole); len(held) != 0 {
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
		t.Errorf("the tenant-admin role is not there after the completing call")
	}
	if held := seeder.grants.held(wantID, personalTenantAdminRole); len(held) != 0 {
		t.Errorf("the tenant-admin role holds %d grant(s), want the empty allowlist: %v", len(held), held)
	}
	if len(manager.bindings[wantID]) != 1 {
		t.Errorf("the account is not bound after the completing call")
	}
}

// TestEnsureCreatesAPersonalTenantBesideABusinessMembership holds the backfill
// rule: an account that belongs to a business tenant still owns its personal
// tenant, and the membership it already holds is left as it stands.
func TestEnsureCreatesAPersonalTenantBesideABusinessMembership(t *testing.T) {
	product, roster, memberships, _, _ := unitProduct()
	memberships.rows[unitAccount] = []authn.Membership{
		{Account: unitAccount, Tenant: "acme", Active: true, Source: "invite"},
	}
	result, err := product.ensure(context.Background(), unitAccount)
	if err != nil {
		t.Fatalf("the ensure on an account of a business tenant: %v", err)
	}
	if !result.Created {
		t.Error("the ensure did not create the personal tenant beside the business membership")
	}
	if len(roster.rows) != 1 {
		t.Errorf("the roster holds %d row(s), want the personal tenant only: %v", len(roster.rows), roster.rows)
	}
	if held := memberships.rows[unitAccount]; len(held) != 2 {
		t.Errorf("the account holds %d membership(s), want the business one and the personal one: %+v",
			len(held), held)
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
// backfill rule for an account that already has its personal tenant and later
// joined another one: the personal tenant is read back and nothing is written.
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

// TestEnsureConvergesTheTenantAdminsGrantsToTheAllowlist holds the reconciliation an
// older tenant gets: every grant the role holds is outside the host's allowlist,
// which is empty, so the next ensure revokes all of them.
func TestEnsureConvergesTheTenantAdminsGrantsToTheAllowlist(t *testing.T) {
	product, _, _, manager, seeder := unitProduct()
	ctx := context.Background()
	if _, err := product.ensure(ctx, unitAccount); err != nil {
		t.Fatalf("the first ensure: %v", err)
	}
	id := string(personalTenantID(unitAccount))
	outer := []authz.Grant{
		{Resource: "account", Action: "read"},
		{Resource: "object", Action: "read"},
	}
	seeder.grants.mu.Lock()
	for _, extra := range outer {
		seeder.grants.sets[id][personalTenantAdminRole][extra] = struct{}{}
	}
	seeder.grants.mu.Unlock()

	if _, err := product.ensure(ctx, unitAccount); err != nil {
		t.Fatalf("the second ensure: %v", err)
	}
	if manager.deleted != len(outer) {
		t.Errorf("the ensure revoked %d grant(s), want the %d outside the allowlist",
			manager.deleted, len(outer))
	}
	if held := seeder.grants.held(id, personalTenantAdminRole); len(held) != 0 {
		t.Errorf("the tenant-admin role holds %v, want the empty allowlist", held)
	}
}

// TestThePersonalTenantAdminRoleIsNotBuiltin holds the one builtin difference
// from a business tenant: the host adds the personal tenant's grants with the
// seeding surface and revokes the ones outside the allowlist with the manager,
// and a builtin role refuses the revocation.
func TestThePersonalTenantAdminRoleIsNotBuiltin(t *testing.T) {
	product, _, _, manager, _ := unitProduct()
	ctx := context.Background()
	tenantID := string(personalTenantID(unitAccount))

	if _, err := product.ensure(ctx, unitAccount); err != nil {
		t.Fatalf("the ensure: %v", err)
	}
	roles, err := manager.Roles(ctx, tenantID)
	if err != nil {
		t.Fatalf("reading the personal tenant's roles: %v", err)
	}
	found := false
	for _, role := range roles {
		if role.Role != personalTenantAdminRole {
			continue
		}
		found = true
		if role.Builtin {
			t.Error("the personal tenant's tenant-admin role is builtin, so the manager " +
				"cannot revoke a grant outside the allowlist")
		}
	}
	if !found {
		t.Errorf("the personal tenant holds no tenant-admin role: %+v", roles)
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
// given whole rather than under this surface's workspace prefix.
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

// TestTheEnsureAnswersADisabledTenant409 holds the handler's mapping of the
// withdrawal refusal. The frontend tells the two 409s apart by the type
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

// TestThePersonalTenantUnblocksTheTenantScopedRoutes is the fail-before case:
// an account with no membership in the tenant a request names reads that
// tenant-scoped route as a 404; the account's own personal tenant, built by the
// seed's account-creation hook, then answers the same read. A second ensure,
// which is what a page reload makes, reads the same tenant back and creates
// nothing. The object store's client-facing routes are gone, so an account with
// a tenant still reaches no object API.
func TestThePersonalTenantUnblocksTheTenantScopedRoutes(t *testing.T) {
	base, token := personalServe(t, true)
	client := &http.Client{Timeout: 10 * time.Second}

	// The fail-before: the account has no membership, so the admission layer
	// answers a tenant-scoped read with its bare 404.
	noMembership := map[string]string{
		"Authorization": "Bearer " + token,
		"X-Tenant":      "demo",
	}
	if res := send(t, client, http.MethodGet, base+"/api/v1/notifications/messages", nil, noMembership); res.status != http.StatusNotFound {
		t.Fatalf("the inbox before the ensure answered %d, want the admission layer's 404:\n%s",
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
	// The seed already built the personal tenant while it wrote the account, so
	// the ensure reads it back rather than creating a second one.
	if reply.Created {
		t.Error("the seed already built the personal tenant, but the ensure reported created=true")
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

	// The membership the ensure wrote is what the admission layer reads: the
	// same tenant-scoped read now answers for the personal tenant.
	tenantHeaders := map[string]string{
		"Authorization": "Bearer " + token,
		"X-Tenant":      reply.Tenant.Alias,
	}
	if res := send(t, client, http.MethodGet, base+"/api/v1/notifications/messages", nil, tenantHeaders); res.status != http.StatusOK {
		t.Fatalf("the inbox after the ensure answered %d, want 200:\n%s", res.status, res.body)
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

	// The object store answers no client-facing route in either scope: its
	// bytes are reached through a URL the business backend mints.
	for _, gone := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/objects"},
		{http.MethodGet, "/api/v1/objects"},
	} {
		if res := send(t, client, gone.method, base+gone.path, nil, tenantHeaders); res.status != http.StatusNotFound {
			t.Errorf("%s %s under the personal tenant answered %d, want the absent route's 404:\n%s",
				gone.method, gone.path, res.status, res.body)
		}
	}
}

// TestTheEnsureCreatesForAnAccountOfAnotherTenant runs the backfill rule through
// the program: the configuration's second account is given a membership in the
// deployment's tenant, and the ensure still creates its personal tenant rather
// than refusing it.
//
// The seed wires the administrator's authorization bindings but no authn
// membership for either account, so the membership is written through the
// account administration route -- the same path an operator uses -- with the
// administrator's token.
func TestTheEnsureCreatesForAnAccountOfAnotherTenant(t *testing.T) {
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
	if res.status != http.StatusOK {
		t.Fatalf("the member's backfill answered %d, want 200:\n%s", res.status, res.body)
	}
	var reply personalCallReply
	if err := json.Unmarshal(res.body, &reply); err != nil {
		t.Fatalf("the backfill's answer is not JSON: %v\n%s", err, res.body)
	}
	if !reply.Created {
		t.Error("the backfill beside a business membership answered created=false, want true")
	}
}

// TestTheTenantAdminHoldsTheAllowlistAndCannotManageTheTenant runs the authorization
// consequence of the personal tenant through the program: the tenant-admin role holds
// exactly the host's allowlist, and the account holding it is refused the
// tenant's membership and invitation management, which every self-registered
// account would otherwise wield in its own tenant.
func TestTheTenantAdminHoldsTheAllowlistAndCannotManageTheTenant(t *testing.T) {
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
	// are the host's allowlist and nothing else — and the allowlist is empty, so
	// the tenant-admin holds no permission at all.
	grants := send(t, client, http.MethodGet,
		base+"/api/v1/system/tenants/"+personalID+"/roles/"+string(personalTenantAdminRole)+"/grants",
		nil, operatorAuth)
	if grants.status != http.StatusOK {
		t.Fatalf("reading the tenant-admin role's grants answered %d:\n%s", grants.status, grants.body)
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
	if len(listed.Grants) != 0 {
		t.Errorf("the tenant-admin role holds %+v, want the empty allowlist", listed.Grants)
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
		t.Errorf("the personal tenant's tenant-admin wrote a membership and answered %d, want 403:\n%s",
			added.status, added.body)
	}

	invited := send(t, client, http.MethodPost,
		base+"/api/v1/tenants/"+personalID+"/invitations",
		map[string]any{"email": "someone@example.test"}, memberAuth)
	if invited.status != http.StatusForbidden {
		t.Errorf("the personal tenant's tenant-admin issued an invitation and answered %d, want 403:\n%s",
			invited.status, invited.body)
	}

	// The single holder cannot leave: removing its own membership is the same
	// membership management the allowlist withholds, so the route refuses it.
	// The last tenant-admin binding and the self-removal refusal stand behind
	// that, and the account is the tenant's only member.
	left := send(t, client, http.MethodDelete,
		base+"/api/v1/tenants/"+personalID+"/members/"+who.AccountID, nil, memberAuth)
	if left.status != http.StatusForbidden {
		t.Errorf("the personal tenant's holder removed its own membership and answered %d, want 403:\n%s",
			left.status, left.body)
	}

	// The host's write guard protects the system tenant and every personal
	// tenant from an operator's disable. The operator reaches the route — it
	// holds tenant.manage in the platform domain — and the refusal is the guard's
	// protected type, not a permission denial.
	for _, id := range []string{string(authz.PlatformDomain), personalID} {
		refused := send(t, client, http.MethodPost,
			base+"/api/v1/system/tenants/"+id+"/status",
			map[string]any{"status": "disabled"}, operatorAuth)
		if refused.status != http.StatusForbidden ||
			!strings.Contains(string(refused.body), "urn:speed:tenant:protected") {
			t.Errorf("disabling the protected tenant %q answered %d, want 403 urn:speed:tenant:protected:\n%s",
				id, refused.status, refused.body)
		}
	}
}

// TestTheSeedBuildsAPersonalTenantForEveryPresetAccount holds the seed's
// inclusion of the personal module: the account creation hook runs while the
// seed writes the preset accounts, so no seeded account is left without its
// space until a browser happens to call the backfill.
func TestTheSeedBuildsAPersonalTenantForEveryPresetAccount(t *testing.T) {
	base, memberToken := personalServe(t, true)
	client := &http.Client{Timeout: 10 * time.Second}

	for name, token := range map[string]string{
		"operator": signIn(t, base, seedIdentifier, seedPassword),
		"ops":      memberToken,
	} {
		self := send(t, client, http.MethodGet, base+"/api/v1/auth/me/tenants", nil,
			map[string]string{"Authorization": "Bearer " + token})
		if self.status != http.StatusOK {
			t.Fatalf("reading %s's tenants answered %d:\n%s", name, self.status, self.body)
		}
		var answer struct {
			Tenants []struct {
				ID string `json:"id"`
			} `json:"tenants"`
		}
		if err := json.Unmarshal(self.body, &answer); err != nil {
			t.Fatalf("reading %s's tenants: %v\n%s", name, err, self.body)
		}
		found := false
		for _, held := range answer.Tenants {
			if strings.HasPrefix(held.ID, personalIDPrefix) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s has no personal tenant before any backfill: %s", name, self.body)
		}
	}
}

// TestTheSeedBuildsNoPersonalTenantWithTheSwitchOff holds the other half of the
// seed's inclusion: the creation hook honours the switch, so a deployment that
// does not serve the feature gets no personal tenant rows.
func TestTheSeedBuildsNoPersonalTenantWithTheSwitchOff(t *testing.T) {
	base, memberToken := personalServe(t, false)
	read := send(t, &http.Client{Timeout: 10 * time.Second}, http.MethodGet,
		base+"/api/v1/auth/me/tenants", nil,
		map[string]string{"Authorization": "Bearer " + memberToken})
	if read.status != http.StatusOK {
		t.Fatalf("reading the account's tenants answered %d:\n%s", read.status, read.body)
	}
	var answer struct {
		Tenants []struct {
			ID string `json:"id"`
		} `json:"tenants"`
	}
	if err := json.Unmarshal(read.body, &answer); err != nil {
		t.Fatalf("the answer is not JSON: %v\n%s", err, read.body)
	}
	for _, held := range answer.Tenants {
		if strings.HasPrefix(held.ID, personalIDPrefix) {
			t.Errorf("the switch is off and the seed built the personal tenant %q", held.ID)
		}
	}
}

// TestThePolicyWriteGuardRefusesAPersonalTenantsPolicyWrite holds the host's
// write guard in the program: the platform administrator may manage an
// ordinary tenant's bindings, and the same write against a personal tenant is
// refused with the guard's forbidden answer rather than reaching the manager.
func TestThePolicyWriteGuardRefusesAPersonalTenantsPolicyWrite(t *testing.T) {
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

	operatorToken := signIn(t, base, seedIdentifier, seedPassword)
	operatorAuth := map[string]string{"Authorization": "Bearer " + operatorToken}
	profile := send(t, client, http.MethodGet, base+"/api/v1/auth/me", nil, operatorAuth)
	var who struct {
		AccountID string `json:"account_id"`
	}
	if err := json.Unmarshal(profile.body, &who); err != nil || who.AccountID == "" {
		t.Fatalf("reading the administrator's account identifier: %v\n%s", err, profile.body)
	}

	refused := send(t, client, http.MethodPut,
		base+"/api/v1/system/tenants/"+reply.Tenant.ID+"/bindings/"+who.AccountID+"/"+string(personalTenantAdminRole),
		nil, operatorAuth)
	if refused.status != http.StatusForbidden ||
		!strings.Contains(string(refused.body), "urn:speed:authz:forbidden") {
		t.Errorf("the administrator wrote a binding into the personal tenant and answered %d, "+
			"want 403 urn:speed:authz:forbidden:\n%s", refused.status, refused.body)
	}

	// The control: the same write in a business tenant reaches the manager and is
	// idempotent over the binding the seed already wrote.
	allowed := send(t, client, http.MethodPut,
		base+"/api/v1/system/tenants/"+demoTenant+"/bindings/"+who.AccountID+"/"+string(personalTenantAdminRole),
		nil, operatorAuth)
	if allowed.status != http.StatusNoContent {
		t.Errorf("the administrator's binding write in %s answered %d, want 204:\n%s",
			demoTenant, allowed.status, allowed.body)
	}
}

// TestThePersonalTenantRefusesAMembershipAddition holds the addition half of the
// single-member rule: the platform administrator has no membership.manage in the
// personal tenant's domain, and the tenant's own tenant-admin holds an
// allowlist without it, so neither can add a member.
func TestThePersonalTenantRefusesAMembershipAddition(t *testing.T) {
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

	operatorToken := signIn(t, base, seedIdentifier, seedPassword)
	operatorAuth := map[string]string{"Authorization": "Bearer " + operatorToken}
	profile := send(t, client, http.MethodGet, base+"/api/v1/auth/me", nil, operatorAuth)
	var who struct {
		AccountID string `json:"account_id"`
	}
	if err := json.Unmarshal(profile.body, &who); err != nil || who.AccountID == "" {
		t.Fatalf("reading the administrator's account identifier: %v\n%s", err, profile.body)
	}

	added := send(t, client, http.MethodPut,
		base+"/api/v1/tenants/"+reply.Tenant.ID+"/members/"+who.AccountID,
		map[string]any{"active": true}, operatorAuth)
	if added.status != http.StatusForbidden {
		t.Errorf("the platform administrator added a member to the personal tenant and answered %d, "+
			"want 403:\n%s", added.status, added.body)
	}
}

// TestThePersonalTenantAdminRoleIsNotBuiltinThroughTheAPI holds the builtin
// distinction the host relies on: a personal tenant's tenant-admin is not
// builtin, because the host revokes grants outside the allowlist through the
// manager, while a business tenant's is.
func TestThePersonalTenantAdminRoleIsNotBuiltinThroughTheAPI(t *testing.T) {
	base, memberToken := personalServe(t, true)
	client := &http.Client{Timeout: 10 * time.Second}

	ensured := send(t, client, http.MethodPost, base+personalRoutePath, nil,
		map[string]string{"Authorization": "Bearer " + memberToken})
	if ensured.status != http.StatusOK {
		t.Fatalf("the ensure answered %d, want 200:\n%s", ensured.status, ensured.body)
	}
	var reply personalCallReply
	if err := json.Unmarshal(ensured.body, &reply); err != nil {
		t.Fatalf("the ensure's answer is not JSON: %v\n%s", err, ensured.body)
	}

	operatorAuth := map[string]string{
		"Authorization": "Bearer " + signIn(t, base, seedIdentifier, seedPassword),
	}
	builtinOf := func(tenantID string) bool {
		got := send(t, client, http.MethodGet,
			base+"/api/v1/system/tenants/"+tenantID+"/roles", nil, operatorAuth)
		if got.status != http.StatusOK {
			t.Fatalf("reading the roles of %s answered %d:\n%s", tenantID, got.status, got.body)
		}
		var listed struct {
			Roles []struct {
				Role    string `json:"role"`
				Builtin bool   `json:"builtin"`
			} `json:"roles"`
		}
		if err := json.Unmarshal(got.body, &listed); err != nil {
			t.Fatalf("reading the roles of %s: %v\n%s", tenantID, err, got.body)
		}
		for _, role := range listed.Roles {
			if role.Role == string(personalTenantAdminRole) {
				return role.Builtin
			}
		}
		t.Fatalf("the tenant %s holds no tenant-admin role: %s", tenantID, got.body)
		return false
	}
	if builtinOf(reply.Tenant.ID) {
		t.Error("the personal tenant's tenant-admin role is builtin")
	}
	if !builtinOf(demoTenant) {
		t.Error("the business tenant's tenant-admin role is not builtin")
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

// TestTheEnsureCreatesForAnUnprovedRegistration holds the eligibility rule: an
// unproved email address is not a reason to withhold the workspace. The account
// gets its personal tenant, and the verification gate limits what it may do
// inside it, not whether it exists.
func TestTheEnsureCreatesForAnUnprovedRegistration(t *testing.T) {
	product, roster, memberships, manager, _ := unitProduct()
	result, err := product.ensure(context.Background(), unitAccount)
	if err != nil {
		t.Fatalf("the ensure: %v", err)
	}
	if !result.Created {
		t.Errorf("the ensure reports created=false, want true")
	}
	if roster.creates != 1 || memberships.puts != 1 || manager.putRole != 1 || manager.putBinding != 1 {
		t.Errorf("the ensure wrote %d tenant(s), %d membership(s), %d role(s), %d binding(s), want one of each",
			roster.creates, memberships.puts, manager.putRole, manager.putBinding)
	}
}

// TestTheBackfillCreatesBesideAnotherMembership holds the rule through the
// handler: a membership elsewhere is not a refusal, and the account gets its
// personal tenant.
func TestTheBackfillCreatesBesideAnotherMembership(t *testing.T) {
	product, roster, memberships, _, _ := unitProduct()
	memberships.rows[unitAccount] = []authn.Membership{
		{Account: unitAccount, Tenant: "acme", Active: true, Source: "invite"},
	}
	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
	c, recorder := ginContext(ctx)
	serveEnsure(product, c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("the backfill answered %d, want 200:\n%s", recorder.Code, recorder.Body.String())
	}
	if roster.creates != 1 || memberships.puts != 1 {
		t.Errorf("the backfill wrote %d tenant(s), %d membership(s), want one of each",
			roster.creates, memberships.puts)
	}
}

// TestTheEnsureReadsBackAnExistingPersonalTenant holds the reload scene: the
// account already holds its personal tenant, and the call answers it without
// writing anything.
func TestTheEnsureReadsBackAnExistingPersonalTenant(t *testing.T) {
	product, roster, memberships, manager, seeder := unitProduct()
	id := personalTenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusActive}
	memberships.rows[unitAccount] = []authn.Membership{
		{Account: unitAccount, Tenant: string(id), Active: true, Source: "personal"},
	}
	result, err := product.ensure(context.Background(), unitAccount)
	if err != nil {
		t.Fatalf("the ensure: %v", err)
	}
	if result.Created || result.Tenant.ID != id {
		t.Errorf("the ensure answered created=%t tenant=%q, want created=false tenant=%q",
			result.Created, result.Tenant.ID, id)
	}
	// The role, its grants and the binding are converged on every call by
	// design; a read-back writes no tenant row and no membership row.
	if roster.creates != 0 || memberships.puts != 0 {
		t.Errorf("the read-back ensure wrote %d tenant(s), %d membership(s), want none",
			roster.creates, memberships.puts)
	}
	_, _, _ = manager, seeder, id
}

// TestThePersonalTenantAdminAllowlistExcludesManagement holds the one
// tenant-kind difference the host declares: a personal tenant's tenant-admin
// keeps the business-resource face and loses membership and identity-provider
// management, which would let a single-person space add accounts or manage SSO.
func TestThePersonalTenantAdminAllowlistExcludesManagement(t *testing.T) {
	business := []authz.Permission{
		"crm.deal.read",
		authz.PermissionMembershipManage,
		authz.PermissionIDPManage,
	}
	got := personalAllowlist(business)
	want := []authz.Permission{"crm.deal.read"}
	if !slices.Equal(got, want) {
		t.Errorf("the allowlist = %v, want %v", got, want)
	}
	for _, excluded := range []authz.Permission{authz.PermissionMembershipManage, authz.PermissionIDPManage} {
		if !slices.Contains(personalExcludedPermissions, excluded) {
			t.Errorf("the exclusion set does not hold %q", excluded)
		}
		if slices.Contains(personalTenantAdminPermissions(), excluded) {
			t.Errorf("the host allowlist holds the excluded permission %q", excluded)
		}
	}
}

// TestEnsureMigratesTheLegacyOwnerRole holds the upgrade path: a personal
// tenant built under the old model has an `owner` role and its binding; the next
// backfill converges it to the tenant-admin role and removes the owner role with
// its grants and bindings.
func TestEnsureMigratesTheLegacyOwnerRole(t *testing.T) {
	product, roster, memberships, manager, seeder := unitProduct()
	ctx := context.Background()
	id := personalTenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusActive}
	memberships.rows[unitAccount] = []authn.Membership{
		{Account: unitAccount, Tenant: string(id), Active: true, Source: "personal"},
	}
	legacy := authz.Role("owner")
	if err := manager.PutRole(ctx, authz.RoleRecord{Tenant: string(id), Role: legacy}); err != nil {
		t.Fatalf("writing the legacy role: %v", err)
	}
	if err := manager.PutBinding(ctx, authz.Binding{Tenant: string(id), Account: unitAccount.String(), Role: legacy}); err != nil {
		t.Fatalf("writing the legacy binding: %v", err)
	}
	if _, err := seeder.SeedGrants(ctx, string(id), legacy, []authz.Grant{{Resource: "legacy", Action: "read"}}); err != nil {
		t.Fatalf("writing the legacy grant: %v", err)
	}

	if _, err := product.ensure(ctx, unitAccount); err != nil {
		t.Fatalf("the ensure over the legacy tenant: %v", err)
	}
	roles, _ := manager.Roles(ctx, string(id))
	for _, role := range roles {
		if role.Role == legacy {
			t.Errorf("the legacy owner role survived the backfill: %+v", roles)
		}
	}
	foundAdmin := false
	for _, role := range roles {
		if role.Role == personalTenantAdminRole {
			foundAdmin = true
		}
	}
	if !foundAdmin {
		t.Errorf("the backfill did not write the tenant-admin role: %+v", roles)
	}
	if held := seeder.grants.held(string(id), legacy); len(held) != 0 {
		t.Errorf("the legacy role's grants survived: %v", held)
	}
}
