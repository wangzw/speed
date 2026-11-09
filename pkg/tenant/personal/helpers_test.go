package personal

// The stand-ins the unit cases drive: the roster and its transaction-aware
// write, the membership read and cleanup, the transaction-aware membership
// write, the policy manager and its transaction-aware surface, the seeding
// surface, the permission catalog, the audit capability and the account read.
// Each records what it was handed so a case can assert on it, and each has
// failure fields a case sets.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/provisioning"
	"github.com/vislake/speed/pkg/authz"
	authzpolicy "github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	tenantcatalog "github.com/vislake/speed/pkg/tenant/catalog"
)

// unitAccount is the account the unit cases provision for. It is not a UUID so
// the derived identifier is easy to read in a failure message.
const unitAccount = authn.AccountID("7c1e0f6a")

// stepLog records the order writes reached the stand-ins.
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

// fakeRoster is the tenant.Catalog the unit cases use.
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
	return &fakeRoster{rows: map[tenant.ID]tenant.Tenant{}, lagging: map[tenant.ID]bool{}}
}

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

func (r *fakeRoster) List(_ context.Context) ([]tenant.Tenant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]tenant.Tenant, 0, len(r.rows))
	for _, row := range r.rows {
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b tenant.Tenant) int { return strings.Compare(string(a.ID), string(b.ID)) })
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

// fakeMemberships is the membership read and cleanup the unit cases use.
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
	slices.SortFunc(all, func(a, b authn.Membership) int { return strings.Compare(string(a.Account), string(b.Account)) })
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

// fakeTxCatalog records the transaction handle and the tenant rows a case
// writes through it.
type fakeTxCatalog struct {
	tenantcatalog.TxCatalog

	mu        sync.Mutex
	tx        *gorm.DB
	created   []tenant.Tenant
	deleted   []tenant.ID
	createErr error
	deleteErr error
	getErr    error
	after     []string
}

// GetTx answers the personal row the deletion's audit row names. The fake holds
// no table, so it answers the row the account's username derives unless a case
// says the read fails.
func (c *fakeTxCatalog) GetTx(_ context.Context, tx *gorm.DB, id tenant.ID) (tenant.Tenant, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tx = tx
	if c.getErr != nil {
		return tenant.Tenant{}, false, c.getErr
	}
	return tenant.Tenant{ID: id, Name: "alice", Status: tenant.StatusActive}, true, nil
}

func (c *fakeTxCatalog) CreateTx(_ context.Context, tx *gorm.DB, t tenant.Tenant) (func(context.Context) error, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tx = tx
	if c.createErr != nil {
		return nil, c.createErr
	}
	c.created = append(c.created, t)
	return afterClosure(&c.after, "create"), nil
}

func (c *fakeTxCatalog) DeleteTx(_ context.Context, tx *gorm.DB, id tenant.ID) (func(context.Context) error, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tx = tx
	if c.deleteErr != nil {
		return nil, c.deleteErr
	}
	c.deleted = append(c.deleted, id)
	return afterClosure(&c.after, "delete"), nil
}

func afterClosure(sink *[]string, name string) func(context.Context) error {
	return func(context.Context) error {
		*sink = append(*sink, name)
		return nil
	}
}

// fakeTxManager records the transaction handle and every policy write.
type fakeTxManager struct {
	authzpolicy.TxManager

	mu      sync.Mutex
	tx      *gorm.DB
	roles   []authz.RoleRecord
	grants  []authz.Grant
	binding []authz.Binding
	cleared []string
	removed []authz.Role
	after   []string

	putRoleErr    error
	seedErr       error
	putBindErr    error
	clearErr      error
	deleteRoleErr error
}

func (m *fakeTxManager) PutRoleTx(_ context.Context, tx *gorm.DB, r authz.RoleRecord) (func(context.Context) error, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tx = tx
	if m.putRoleErr != nil {
		return nil, m.putRoleErr
	}
	m.roles = append(m.roles, r)
	return afterClosure(&m.after, "role"), nil
}

func (m *fakeTxManager) DeleteRoleTx(_ context.Context, tx *gorm.DB, _ string, role authz.Role) (func(context.Context) error, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tx = tx
	if m.deleteRoleErr != nil {
		return nil, m.deleteRoleErr
	}
	m.removed = append(m.removed, role)
	return afterClosure(&m.after, "delete-role"), nil
}

func (m *fakeTxManager) SeedGrantsTx(_ context.Context, tx *gorm.DB, _ string, _ authz.Role,
	grants []authz.Grant,
) (int, func(context.Context) error, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tx = tx
	if m.seedErr != nil {
		return 0, nil, m.seedErr
	}
	// The production write answers a nil closure when it added no row, so a
	// grant set that is empty — the personal tenant's is — registers no
	// after-commit work. Mirroring that keeps the collector assertion honest.
	added := 0
	for _, grant := range grants {
		if !slices.Contains(m.grants, grant) {
			m.grants = append(m.grants, grant)
			added++
		}
	}
	if added == 0 {
		return 0, nil, nil
	}
	return added, afterClosure(&m.after, "grants"), nil
}

func (m *fakeTxManager) PutBindingTx(_ context.Context, tx *gorm.DB, b authz.Binding) (func(context.Context) error, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tx = tx
	if m.putBindErr != nil {
		return nil, m.putBindErr
	}
	m.binding = append(m.binding, b)
	return afterClosure(&m.after, "binding"), nil
}

func (m *fakeTxManager) ClearAccountBindingsTx(_ context.Context, tx *gorm.DB, _, account string) (func(context.Context) error, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tx = tx
	if m.clearErr != nil {
		return nil, m.clearErr
	}
	m.cleared = append(m.cleared, account)
	return afterClosure(&m.after, "clear-bindings"), nil
}

// fakeMembershipWriter records the transaction handle and the membership.
type fakeMembershipWriter struct {
	provisioning.MembershipWriter

	mu    sync.Mutex
	tx    *gorm.DB
	put   []authn.Membership
	after []string
	err   error
}

func (w *fakeMembershipWriter) PutMembershipTx(_ context.Context, tx *gorm.DB,
	m authn.Membership,
) (func(context.Context) error, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tx = tx
	if w.err != nil {
		return nil, w.err
	}
	w.put = append(w.put, m)
	return afterClosure(&w.after, "membership"), nil
}

// fakeGrants is the grant table the manager and the seeding surface share.
type fakeGrants struct {
	mu   sync.Mutex
	sets map[string]map[authz.Role]map[authz.Grant]struct{}
}

func newFakeGrants() *fakeGrants {
	return &fakeGrants{sets: map[string]map[authz.Role]map[authz.Grant]struct{}{}}
}

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

	putRoleErr        error
	putRole           int
	putBinding        int
	deleted           int
	steps             *stepLog
	bindingsErr       error
	deleteBindErr     error
	deleteBindingsErr error
	deletedBindings   int
	deleteRoleErr     error
	deletedRoles      int
	markBuiltinErr    error
}

func newFakeManager(grants *fakeGrants) *fakeManager {
	return &fakeManager{roles: map[string][]authz.RoleRecord{}, bindings: map[string][]authz.Binding{}, grants: grants}
}

func (m *fakeManager) Roles(_ context.Context, tenantID string) ([]authz.RoleRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]authz.RoleRecord(nil), m.roles[tenantID]...), nil
}

func (m *fakeManager) Grants(_ context.Context, tenantID string, role authz.Role) ([]authz.GrantRecord, error) {
	out := make([]authz.GrantRecord, 0)
	for _, grant := range m.grants.held(tenantID, role) {
		out = append(out, authz.GrantRecord{Tenant: tenantID, Role: role, Resource: grant.Resource, Action: grant.Action})
	}
	return out, nil
}

func (m *fakeManager) DeleteGrant(_ context.Context, grant authz.GrantRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted++
	m.grants.mu.Lock()
	defer m.grants.mu.Unlock()
	delete(m.grants.sets[grant.Tenant][grant.Role], authz.Grant{Resource: grant.Resource, Action: grant.Action})
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
				return fmt.Errorf("%w: the role %q of tenant %q is builtin", authz.ErrBuiltinRole, record.Role, record.Tenant)
			}
			held[i] = record
			m.roles[record.Tenant] = held
			return nil
		}
	}
	m.roles[record.Tenant] = append(held, record)
	return nil
}

func (m *fakeManager) MarkRoleBuiltin(_ context.Context, tenantID string, role authz.Role) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps.add("mark:" + string(role))
	if m.markBuiltinErr != nil {
		return m.markBuiltinErr
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

func (m *fakeManager) Bindings(_ context.Context, tenantID string) ([]authz.Binding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps.add("bindings")
	if m.bindingsErr != nil {
		return nil, m.bindingsErr
	}
	return append([]authz.Binding(nil), m.bindings[tenantID]...), nil
}

func (m *fakeManager) DeleteBinding(_ context.Context, tenantID, account string, role authz.Role) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deletedBindings++
	m.steps.add("unbind:" + account + ":" + string(role))
	if m.deleteBindErr != nil {
		return m.deleteBindErr
	}
	kept := m.bindings[tenantID][:0]
	for _, binding := range m.bindings[tenantID] {
		if binding.Account == account && binding.Role == role {
			continue
		}
		kept = append(kept, binding)
	}
	m.bindings[tenantID] = kept
	return nil
}

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
			return fmt.Errorf("%w: the role %q of tenant %q is builtin", authz.ErrBuiltinRole, role, tenantID)
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

// fakeSeeder is the authz.Seeder the unit cases use.
type fakeSeeder struct {
	authz.Seeder

	grants *fakeGrants
	steps  *stepLog

	mu   sync.Mutex
	seed int
}

func newFakeSeeder(grants *fakeGrants) *fakeSeeder { return &fakeSeeder{grants: grants} }

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

// fakeCatalog is the authz.Catalog the unit cases use.
type fakeCatalog struct {
	authz.Catalog

	governed []authz.Permission
}

func (fakeCatalog) Declared() []authz.Permission { return nil }

func (c fakeCatalog) TenantGoverned() []authz.Permission { return slices.Clone(c.governed) }

func (c fakeCatalog) Governed(permission authz.Permission) bool {
	return slices.Contains(c.governed, permission)
}

func (fakeCatalog) ObjectAction(authz.Permission) (string, string, bool) { return "", "", false }

// fakeAudit is the audit.Audit the unit cases use.
type fakeAudit struct {
	mu         sync.Mutex
	entries    []audit.Entry
	identities []bool
	err        error
	steps      *stepLog
}

func (a *fakeAudit) Record(ctx context.Context, e audit.Entry) error {
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

func (a *fakeAudit) rows() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.entries...)
}

// fakeAccounts is the account read the module makes for the event compensation
// and for the ensure's username.
type fakeAccounts struct {
	kind          authn.AccountKind
	status        authn.AccountStatus
	err           error
	identifiers   []authn.Identifier
	identifierErr error
	reads         int
}

func (a *fakeAccounts) Get(_ context.Context, id authn.AccountID) (authn.Account, error) {
	a.reads++
	if a.err != nil {
		return authn.Account{}, a.err
	}
	return authn.Account{ID: id, Kind: a.kind, Status: a.status}, nil
}

func (a *fakeAccounts) Identifiers(context.Context, authn.AccountID) ([]authn.Identifier, error) {
	if a.identifierErr != nil {
		return nil, a.identifierErr
	}
	return append([]authn.Identifier(nil), a.identifiers...), nil
}

// unitProduct assembles a product over the fakes.
func unitProduct() (*product, *fakeRoster, *fakeMemberships, *fakeManager, *fakeSeeder) {
	steps := &stepLog{}
	roster := newFakeRoster()
	roster.steps = steps
	memberships := newFakeMemberships()
	memberships.steps = steps
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	manager.steps = steps
	seeder := newFakeSeeder(grants)
	seeder.steps = steps
	auditor := &fakeAudit{steps: steps}
	return &product{
		roster:            roster,
		memberships:       memberships,
		membershipCleanup: memberships,
		manager:           manager,
		seeder:            seeder,
		catalog:           fakeCatalog{},
		audit:             auditor,
		logger:            log.Default(),
		seen:              newSeenEvents(seenEventsCapacity),
		// A user account always carries a username, and the unit product models
		// one so the ensure and the creation hook can name the tenant.
		accounts: &fakeAccounts{
			kind:        authn.KindUser,
			status:      authn.StatusActive,
			identifiers: []authn.Identifier{{Kind: authn.IdentifierUsername, Value: "unit-user"}},
		},
	}, roster, memberships, manager, seeder
}

// seededPersonal assembles a product and gives the account its personal tenant.
func seededPersonal(t *testing.T) (*product, *fakeRoster, *fakeMemberships, *fakeManager, *fakeAudit) {
	t.Helper()
	p, roster, memberships, manager, _ := unitProduct()
	if _, err := p.ensure(context.Background(), unitAccount); err != nil {
		t.Fatalf("seeding the personal tenant: %v", err)
	}
	return p, roster, memberships, manager, p.audit.(*fakeAudit)
}

// entriesOfAction answers the entries written for one action.
func entriesOfAction(entries []audit.Entry, action string) []audit.Entry {
	var out []audit.Entry
	for _, entry := range entries {
		if entry.Action == action {
			out = append(out, entry)
		}
	}
	return out
}
