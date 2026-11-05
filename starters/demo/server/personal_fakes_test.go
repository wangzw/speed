package main

// The stand-in seams several demo test suites share: the roster, the
// memberships, the policy manager and its grants, the seeding surface, the
// audit recorder and the step log. The personal-tenant module itself lives in
// pkg/tenant/personal; these remain because the tenant and self-service
// creation suites drive the host's seams over them.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
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

// List returns the rows the roster holds, ordered by identifier, as the real
// catalog does.
func (r *fakeRoster) List(_ context.Context) ([]tenant.Tenant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]tenant.Tenant, 0, len(r.rows))
	for _, row := range r.rows {
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b tenant.Tenant) int {
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return out, nil
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
func (m *fakeMemberships) Members(_ context.Context, tenantID string, query authn.MemberQuery) ([]authn.Membership, error) {
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
	for start < len(all) && query.After != "" && string(all[start].Account) <= query.After {
		start++
	}
	all = all[start:]
	if limit := query.EffectiveLimit(); len(all) > limit {
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
	m.removeRow(id, tenantID)
	return nil
}

// RemoveMembershipForDeletion deletes one membership without asking a guard, as
// the account module's account-deletion path does.
func (m *fakeMemberships) RemoveMembershipForDeletion(ctx context.Context, id authn.AccountID, tenantID string) error {
	return m.Remove(ctx, id, tenantID)
}

func (m *fakeMemberships) removeRow(id authn.AccountID, tenantID string) {
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
	m.steps.add("put:" + membership.Account.String())
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

	markBuiltinErr     error
	markBuiltinFailure int
	markBuiltinCalls   int
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
	m.steps.add("role:" + string(record.Role))
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
	m.markBuiltinCalls++
	m.steps.add("mark:" + string(role))
	if m.markBuiltinFailure > 0 {
		m.markBuiltinFailure--
		if m.markBuiltinErr != nil {
			return m.markBuiltinErr
		}
		return fmt.Errorf("the builtin mark is unavailable")
	}
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
	m.steps.add("bind:" + binding.Account + ":" + string(binding.Role))
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
	steps  *stepLog

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
	s.steps.add("seed:" + string(role))
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
