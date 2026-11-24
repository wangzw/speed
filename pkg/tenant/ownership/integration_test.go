package ownership_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authz"
	authzpolicy "github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/i18n"
	i18naccount "github.com/vislake/speed/pkg/i18n/account"
	i18ncatalog "github.com/vislake/speed/pkg/i18n/catalog"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/jobs"
	speedlog "github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
	tenantcatalog "github.com/vislake/speed/pkg/tenant/catalog"
	"github.com/vislake/speed/pkg/tenant/ownership"
	"github.com/vislake/speed/pkg/tenant/personal"
)

type recordingAudit struct{}

func (*recordingAudit) Record(context.Context, audit.Entry) error { return nil }

type personalWriteGuard struct{}

func (personalWriteGuard) Denied(_ context.Context, tenantID string, _ authz.WriteOp) (bool, error) {
	return strings.HasPrefix(tenantID, "personal-"), nil
}

type assembly struct {
	admin       authn.AccountAdmin
	accounts    authn.Accounts
	memberships authn.Memberships
	catalog     tenant.Catalog
	manager     authz.Manager
	seeder      authz.Seeder
	counter     authz.TenantAdminCounter
	writer      tenant.OrganizationWriter
	invitations *recordingInvitationRevoker
	target      tenant.OwnershipTarget
	dispatcher  *recordingDispatcher
	logs        *logRecorder
	handle      *gorm.DB
}

type recordingInvitationRevoker struct {
	mu      sync.Mutex
	tenants []string
}

func (r *recordingInvitationRevoker) RevokeTenantInvitations(_ context.Context, tenant string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tenants = append(r.tenants, tenant)
	return nil
}

func (r *recordingInvitationRevoker) revoked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.tenants...)
}

type recordingDispatcher struct {
	mu         sync.Mutex
	dispatched []notification.Dispatch
}

func (d *recordingDispatcher) Dispatch(_ context.Context, dispatch notification.Dispatch) (jobs.JobID, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dispatched = append(d.dispatched, dispatch)
	return jobs.JobID("job"), nil
}

func (d *recordingDispatcher) all() []notification.Dispatch {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]notification.Dispatch(nil), d.dispatched...)
}

func (d *recordingDispatcher) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dispatched = nil
}

const testDefaultLocale = "zh-CN"

func withAssembly(t *testing.T, body func(t *testing.T, a *assembly)) {
	t.Helper()
	withI18nProfile(t, i18naccount.Module(), body)
}

func withI18nProfile(t *testing.T, profile core.Module, body func(t *testing.T, a *assembly)) {
	t.Helper()
	withI18nProfiles(t, []core.Module{profile}, body)
}

func withI18nProfiles(t *testing.T, profiles []core.Module, body func(t *testing.T, a *assembly)) {
	t.Helper()
	modules := append([]core.Module{i18ncatalog.Module()}, profiles...)
	withI18n(t, modules, testDefaultLocale, body)
}

func withNoI18nEngine(t *testing.T, body func(t *testing.T, a *assembly)) {
	t.Helper()
	withI18n(t, []core.Module{i18naccount.Module()}, "", body)
}

func withI18n(t *testing.T, i18nModules []core.Module, defaultLocale string, body func(t *testing.T, a *assembly)) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "ownership.db")
	configuration := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}}}`, dsn)
	if defaultLocale != "" {
		configuration = fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}},"i18n":{"default-locale":%q}}`,
			dsn, defaultLocale)
	}
	dispatcher := &recordingDispatcher{}
	logs := newLogRecorder()

	modules := []core.Module{
		sqlite.Module(),
		tenantcatalog.Module(),
		authzpolicy.Module(),
		account.Module(),
		personal.Module(),
		ownership.Module(),
		standIn("stand-in.audit", &recordingAudit{}, (*audit.Audit)(nil)),
		standIn("stand-in.write-guard", personalWriteGuard{}, (*authz.WriteGuard)(nil)),
		standIn("stand-in.invitations", &recordingInvitationRevoker{}, (*authn.InvitationRevoker)(nil)),
		standIn("stand-in.tenant-lock", authz.NewInProcessTenantLock(), (*authz.TenantLock)(nil)),
		standIn("stand-in.dispatcher", dispatcher, (*notification.Dispatcher)(nil)),
		standIn("stand-in.logger", recordingLogger{handler: logs}, (*speedlog.Logger)(nil)),
	}
	modules = append(modules, i18nModules...)
	reader := configtest.Reader(t, configuration, modules...)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	names := []string{config.ModuleName}
	for _, module := range modules {
		reg.Register(module)
		names = append(names, module.Name)
	}
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: names,
		Action: func(_ context.Context, reg *core.Registry) error {
			body(t, resolve(t, reg, dispatcher, logs))
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the assembly to Init: %v", err)
	}
}

func resolve(t *testing.T, reg *core.Registry, dispatcher *recordingDispatcher, logs *logRecorder) *assembly {
	t.Helper()
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		t.Fatalf("taking up the database: %v", err)
	}
	admin, err := core.Resolve[authn.AccountAdmin](reg)
	if err != nil {
		t.Fatalf("taking up the account write surface: %v", err)
	}
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		t.Fatalf("taking up the account read surface: %v", err)
	}
	memberships, err := core.Resolve[authn.Memberships](reg)
	if err != nil {
		t.Fatalf("taking up the membership surface: %v", err)
	}
	roster, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		t.Fatalf("taking up the roster: %v", err)
	}
	manager, err := core.Resolve[authz.Manager](reg)
	if err != nil {
		t.Fatalf("taking up the policy manager: %v", err)
	}
	writer, err := core.Resolve[tenant.OrganizationWriter](reg)
	if err != nil {
		t.Fatalf("taking up the organization writer: %v", err)
	}
	target, err := core.Resolve[tenant.OwnershipTarget](reg)
	if err != nil {
		t.Fatalf("taking up the ownership target: %v", err)
	}
	counter, err := core.Resolve[authz.TenantAdminCounter](reg)
	if err != nil {
		t.Fatalf("taking up the account-administration counter: %v", err)
	}
	seeder, err := core.Resolve[authz.Seeder](reg)
	if err != nil {
		t.Fatalf("taking up the policy seeder: %v", err)
	}
	revoker, err := core.Resolve[authn.InvitationRevoker](reg)
	if err != nil {
		t.Fatalf("taking up the invitation revoker: %v", err)
	}
	recorded, ok := revoker.(*recordingInvitationRevoker)
	if !ok {
		t.Fatalf("the invitation revoker is %T, want the recording stand-in", revoker)
	}
	return &assembly{
		admin:       admin,
		accounts:    accounts,
		memberships: memberships,
		catalog:     roster,
		manager:     manager,
		seeder:      seeder,
		counter:     counter,
		writer:      writer,
		invitations: recorded,
		target:      target,
		dispatcher:  dispatcher,
		logs:        logs,
		handle:      database.DB(),
	}
}

func standIn(name string, instance any, token core.Token) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: token, Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

func profileStandIn(name string, profile i18n.Profile) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: (*i18n.Profile)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return profile, nil },
	}
}

func (a *assembly) transfer(t *testing.T, id, target string) tenant.TransferRequest {
	t.Helper()
	request, err := a.writer.RequestTransfer(t.Context(), tenant.ID(id), target, "")
	if err != nil {
		t.Fatalf("requesting the transfer of %q to %q: %v", id, target, err)
	}
	if err := a.target.ConfirmTransfer(t.Context(), request.ID, target); err != nil {
		t.Fatalf("confirming the transfer of %q to %q: %v", id, target, err)
	}
	return request
}

func (a *assembly) user(t *testing.T, username string) authn.Account {
	t.Helper()
	created, err := a.admin.Create(t.Context(), authn.CreateAccount{
		Kind: authn.KindUser, DisplayName: username, Username: username, Via: authn.ReasonAdmin,
	})
	if err != nil {
		t.Fatalf("creating the account %q: %v", username, err)
	}
	return created
}

func (a *assembly) organization(t *testing.T, id, name string, owner authn.Account) {
	t.Helper()
	if err := a.catalog.Create(t.Context(), tenant.Tenant{
		ID: tenant.ID(id), Name: name, Status: tenant.StatusActive, OwnerAccount: owner.ID.String(),
	}); err != nil {
		t.Fatalf("registering the organization %q: %v", id, err)
	}
	if err := a.manager.PutRole(t.Context(), authz.RoleRecord{
		Tenant: id, Role: authz.TenantAdminRole, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("writing the organization's tenant-admin role: %v", err)
	}
	a.join(t, id, owner)
	if err := a.manager.PutBinding(t.Context(), authz.Binding{
		Tenant: id, Account: owner.ID.String(), Role: authz.TenantAdminRole, GrantedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("binding the owner to tenant-admin: %v", err)
	}
}

func (a *assembly) join(t *testing.T, id string, who authn.Account) {
	t.Helper()
	if err := a.memberships.Put(t.Context(), authn.Membership{
		Account: who.ID, Tenant: id, Active: true, Source: authn.ReasonAdmin,
	}); err != nil {
		t.Fatalf("making %q a member of %q: %v", who.ID, id, err)
	}
}

func (a *assembly) held(t *testing.T, id string) tenant.Tenant {
	t.Helper()
	got, found, err := a.catalog.Get(t.Context(), tenant.ID(id))
	if err != nil || !found {
		t.Fatalf("reading tenant %q: found=%t err=%v", id, found, err)
	}
	return got
}

func (a *assembly) bindingsOf(t *testing.T, id string, who authn.Account) []authz.Binding {
	t.Helper()
	got, err := a.manager.BindingsOf(t.Context(), id, who.ID.String())
	if err != nil {
		t.Fatalf("reading %q's bindings in %q: %v", who.ID, id, err)
	}
	return got
}

func (a *assembly) count(t *testing.T, table, where string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := a.handle.Table(table).Where(where, args...).Count(&n).Error; err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return n
}

func (a *assembly) rosterRow(t *testing.T, id string) (tenant.Status, *time.Time) {
	t.Helper()
	var got struct {
		Status    int
		DeletedAt *time.Time
	}
	if err := a.handle.Table("tenants").Select("status", "deleted_at").
		Where("id = ?", id).Take(&got).Error; err != nil {
		t.Fatalf("reading tenant %q from the table: %v", id, err)
	}
	return tenant.Status(got.Status), got.DeletedAt
}

// TestTransferOwnerHandsTheOrganizationOn checks the new owner's binding and
// that the previous owner stays an active member holding its binding.
func TestTransferOwnerHandsTheOrganizationOn(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)

		a.transfer(t, "acme", target.ID.String())
		if got := a.held(t, "acme").OwnerAccount; got != target.ID.String() {
			t.Errorf("the owner is %q, want %q", got, target.ID)
		}
		if bindings := a.bindingsOf(t, "acme", target); len(bindings) != 1 ||
			bindings[0].Role != authz.Role(authz.TenantAdminRole) {
			t.Errorf("the new owner holds %+v, want tenant-admin", bindings)
		}
		if bindings := a.bindingsOf(t, "acme", owner); len(bindings) != 1 ||
			bindings[0].Role != authz.Role(authz.TenantAdminRole) {
			t.Errorf("the previous owner holds %+v after the transfer, want tenant-admin", bindings)
		}
		m, found, err := a.memberships.Membership(t.Context(), "acme", owner.ID)
		if err != nil || !found || !m.Active {
			t.Errorf("the previous owner's membership is %+v (found=%t err=%v), want the active one",
				m, found, err)
		}
	})
}

// A target that is not an active member is refused and nothing changes.
func TestTransferOwnerRefusesANonMember(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)

		_, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), "")
		if !errors.Is(err, tenant.ErrOwnerNotMember) {
			t.Fatalf("the transfer gave %v, want ErrOwnerNotMember", err)
		}
		if got := a.held(t, "acme").OwnerAccount; got != owner.ID.String() {
			t.Errorf("the owner changed to %q, want %q", got, owner.ID)
		}
		if bindings := a.bindingsOf(t, "acme", target); len(bindings) != 0 {
			t.Errorf("the refused transfer bound the target: %+v", bindings)
		}
	})
}

// A tenant no row carries gives ErrInvalidTenant and a tombstone ErrTenantDeleted,
// because the roster is read before the membership.
func TestTransferOwnerReadsTheRosterBeforeTheMembership(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)

		if _, err := a.writer.RequestTransfer(t.Context(), "ghost", target.ID.String(), ""); !errors.Is(err, tenant.ErrInvalidTenant) {
			t.Errorf("transferring a tenant no row carries gave %v, want ErrInvalidTenant", err)
		}
		if err := a.writer.DeleteOrganization(t.Context(), "acme", ""); err != nil {
			t.Fatalf("deleting the organization: %v", err)
		}
		if _, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), ""); !errors.Is(err, tenant.ErrTenantDeleted) {
			t.Errorf("transferring a tombstone gave %v, want ErrTenantDeleted", err)
		}
	})
}

// An owner-initiated deletion carries the owner it observed: when a transfer
// has changed the owner, the deletion is refused and the organization stays.
func TestDeleteOrganizationRefusesAnOwnerThatChanged(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)
		a.transfer(t, "acme", target.ID.String())

		err := a.writer.DeleteOrganization(t.Context(), "acme", owner.ID.String())
		if !errors.Is(err, tenant.ErrOwnerChanged) {
			t.Fatalf("the deletion gave %v, want ErrOwnerChanged", err)
		}
		if got := a.held(t, "acme"); got.Status != tenant.StatusActive {
			t.Errorf("the tenant is %q, want active", got.Status)
		}
	})
}

// TestTransferOwnerRefusesAnOwnerThatChanged checks that a stale owner's call is
// refused and the current owner stays.
func TestTransferOwnerRefusesAnOwnerThatChanged(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		third := a.user(t, "carol")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)
		a.join(t, "acme", third)
		request, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), owner.ID.String())
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		if checkErr := a.target.ConfirmTransfer(t.Context(), request.ID, target.ID.String()); checkErr != nil {
			t.Fatalf("confirming the transfer: %v", checkErr)
		}

		_, err = a.writer.RequestTransfer(t.Context(), "acme", third.ID.String(), owner.ID.String())
		if !errors.Is(err, tenant.ErrOwnerChanged) {
			t.Fatalf("the stale transfer gave %v, want ErrOwnerChanged", err)
		}
		if got := a.held(t, "acme").OwnerAccount; got != target.ID.String() {
			t.Errorf("the owner changed to %q, want %q", got, target.ID)
		}
		if bindings := a.bindingsOf(t, "acme", third); len(bindings) != 0 {
			t.Errorf("the stale transfer bound the target: %+v", bindings)
		}
	})
}

// TestTransferOwnerRefusesADeletedAccount checks that a deleted account is
// ErrOwnerInactive before the transaction opens.
func TestTransferOwnerRefusesADeletedAccount(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)
		if err := a.admin.Delete(t.Context(), target.ID, authn.ReasonAdmin); err != nil {
			t.Fatalf("deleting the target: %v", err)
		}

		_, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), "")
		if !errors.Is(err, tenant.ErrOwnerInactive) {
			t.Fatalf("the transfer gave %v, want ErrOwnerInactive", err)
		}
		if got := a.held(t, "acme").OwnerAccount; got != owner.ID.String() {
			t.Errorf("the owner changed to %q, want %q", got, owner.ID)
		}
		if bindings := a.bindingsOf(t, "acme", target); len(bindings) != 0 {
			t.Errorf("the refused transfer bound the target: %+v", bindings)
		}
	})
}

// A deletion writes the tombstone and removes the core relations; the
// identifier and name are not reusable afterwards.
func TestDeleteOrganizationWritesTheTombstoneAndCleansTheCoreRelations(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		a.organization(t, "acme", "acme-org", owner)
		if _, err := a.seeder.SeedGrants(t.Context(), "acme", authz.TenantAdminRole,
			[]authz.Grant{{Resource: "deal", Action: "read"}}); err != nil {
			t.Fatalf("seeding a grant: %v", err)
		}
		if n := a.count(t, "authz_grants", "tenant_id = ?", "acme"); n != 1 {
			t.Fatalf("the tenant holds %d grant(s) before the deletion, want one", n)
		}

		if err := a.writer.DeleteOrganization(t.Context(), "acme", ""); err != nil {
			t.Fatalf("deleting the organization: %v", err)
		}
		got := a.held(t, "acme")
		if got.Status != tenant.StatusDeleted || got.DeletedAt == nil {
			t.Errorf("the tenant is %+v, want a tombstone with a deletion instant", got)
		}
		if got.Name != "acme-org" || got.ID != "acme" {
			t.Errorf("the tombstone lost its identifier or name: %+v", got)
		}
		if n := a.count(t, "authz_bindings", "tenant_id = ?", "acme"); n != 0 {
			t.Errorf("the deleted tenant holds %d binding(s), want none", n)
		}
		if n := a.count(t, "authz_roles", "tenant_id = ?", "acme"); n != 0 {
			t.Errorf("the deleted tenant holds %d role(s), want none", n)
		}
		if n := a.count(t, "authz_grants", "tenant_id = ?", "acme"); n != 0 {
			t.Errorf("the deleted tenant holds %d grant(s), want none", n)
		}
		if n := a.count(t, "authn_memberships", "tenant_id = ?", "acme"); n != 0 {
			t.Errorf("the deleted tenant holds %d membership(s), want none", n)
		}

		if err := a.catalog.Create(t.Context(), tenant.Tenant{
			ID: "acme", Name: "another", Status: tenant.StatusActive,
		}); !errors.Is(err, tenant.ErrTenantExists) {
			t.Errorf("registering the deleted identifier gave %v, want ErrTenantExists", err)
		}
		if err := a.catalog.Create(t.Context(), tenant.Tenant{
			ID: "other", Name: "acme-org", Status: tenant.StatusActive,
		}); !errors.Is(err, tenant.ErrNameConflict) {
			t.Errorf("registering the deleted name gave %v, want ErrNameConflict", err)
		}
		if got := a.invitations.revoked(); len(got) != 1 || got[0] != "acme" {
			t.Errorf("the invitation revoker was asked for %v, want [acme]", got)
		}
	})
}

// A failure after the tombstone and the policy rows were written rolls the
// whole deletion back: the tenant stays active and its policy stays.
func TestDeleteOrganizationRollsBackAFailedStep(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		a.organization(t, "acme", "acme-org", owner)
		if err := a.handle.Exec("DROP TABLE authn_memberships").Error; err != nil {
			t.Fatalf("dropping the memberships table: %v", err)
		}

		if err := a.writer.DeleteOrganization(t.Context(), "acme", ""); err == nil {
			t.Fatal("the deletion succeeded though its last step failed")
		}
		if status, deletedAt := a.rosterRow(t, "acme"); status != tenant.StatusActive || deletedAt != nil {
			t.Errorf("the rollback left status %q and deleted_at %v, want the active row", status, deletedAt)
		}
		if n := a.count(t, "authz_bindings", "tenant_id = ?", "acme"); n != 1 {
			t.Errorf("the rollback left %d binding(s), want the one binding", n)
		}
		if got := a.invitations.revoked(); len(got) != 0 {
			t.Errorf("a rolled-back deletion asked for the invitations of %v, want none", got)
		}
	})
}

// A failure after the binding is written rolls the transfer back, leaving the
// owner, its binding and its membership in place.
func TestTransferOwnerRollsBackAFailedStep(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)
		if err := a.handle.Exec(`CREATE TRIGGER refuse_owner_write
			BEFORE UPDATE ON tenants
			BEGIN SELECT RAISE(ABORT, 'induced failure'); END`).Error; err != nil {
			t.Fatalf("creating the trigger that refuses the last step: %v", err)
		}

		request, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}
		if checkErr := a.target.ConfirmTransfer(t.Context(), request.ID, target.ID.String()); checkErr == nil {
			t.Fatal("the transfer succeeded though its last step failed")
		}
		if got := a.held(t, "acme").OwnerAccount; got != owner.ID.String() {
			t.Errorf("the rollback left owner %q, want %q", got, owner.ID)
		}
		if got := a.bindingsOf(t, "acme", target); len(got) != 0 {
			t.Errorf("the target holds bindings %+v, want none", got)
		}
		if got := a.bindingsOf(t, "acme", owner); len(got) != 1 {
			t.Errorf("the previous owner holds bindings %+v, want the one binding", got)
		}
		m, found, err := a.memberships.Membership(t.Context(), "acme", owner.ID)
		if err != nil || !found || !m.Active {
			t.Errorf("the previous owner's membership is %+v (%v), want the active one", m, err)
		}
	})
}

// A transfer and an owner-initiated deletion of the same organization race
// under the tenant lock, and exactly one of them takes effect.
func TestATransferAndADeletionOfOneOrganizationLeaveOneWinner(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)

		request, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), "")
		if err != nil {
			t.Fatalf("requesting the transfer: %v", err)
		}

		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs[0] = a.target.ConfirmTransfer(t.Context(), request.ID, target.ID.String())
		}()
		go func() {
			defer wg.Done()
			errs[1] = a.writer.DeleteOrganization(t.Context(), "acme", owner.ID.String())
		}()
		wg.Wait()

		won := 0
		for _, err := range errs {
			if err == nil {
				won++
			}
		}
		if won != 1 {
			t.Fatalf("the race left %d winners (transfer %v, deletion %v), want one", won, errs[0], errs[1])
		}
	})
}

// After the ownership is handed on, the previous owner is no longer the
// organization's owner and can be deleted.
func TestThePreviousOwnerCanBeDeletedAfterTheTransfer(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)

		a.transfer(t, "acme", target.ID.String())
		if err := a.admin.Delete(t.Context(), owner.ID, authn.ReasonAdmin); err != nil {
			t.Fatalf("deleting the previous owner: %v", err)
		}
	})
}

// A target whose account is suspended is refused: an active membership is not
// enough, the account that receives the organization has to be active itself.
func TestTransferOwnerRefusesAnInactiveAccount(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)
		if err := a.admin.SetStatus(t.Context(), target.ID, authn.StatusDisabled, authn.ReasonAdmin); err != nil {
			t.Fatalf("suspending the target: %v", err)
		}

		_, err := a.writer.RequestTransfer(t.Context(), "acme", target.ID.String(), "")
		if !errors.Is(err, tenant.ErrOwnerInactive) {
			t.Fatalf("the transfer gave %v, want ErrOwnerInactive", err)
		}
		if got := a.held(t, "acme").OwnerAccount; got != owner.ID.String() {
			t.Errorf("the owner changed to %q, want %q", got, owner.ID)
		}
		if bindings := a.bindingsOf(t, "acme", target); len(bindings) != 0 {
			t.Errorf("the refused transfer bound the target: %+v", bindings)
		}
	})
}

// TestTheAccountAdministrationLockCoversAMemberOnlyAccount checks that an active
// member of an active business tenant is covered by the lock a transfer takes.
func TestTheAccountAdministrationLockCoversAMemberOnlyAccount(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		member := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", member)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		release, err := a.counter.LockAccountAdministration(ctx, member.ID.String())
		if err != nil {
			t.Fatalf("taking the account-administration lock: %v", err)
		}
		closed := false
		defer func() {
			if !closed {
				release()
			}
		}()

		type answer struct {
			request tenant.TransferRequest
			err     error
		}
		done := make(chan answer, 1)
		go func() {
			request, requestErr := a.writer.RequestTransfer(ctx, "acme", member.ID.String(), "")
			done <- answer{request: request, err: requestErr}
		}()
		select {
		case got := <-done:
			t.Fatalf("the request ran while the member-only account's tenant lock was held: %v", got.err)
		case <-time.After(100 * time.Millisecond):
		}

		release()
		closed = true
		var request tenant.TransferRequest
		select {
		case got := <-done:
			if got.err != nil {
				t.Fatalf("the request after the release: %v", got.err)
			}
			request = got.request
		case <-time.After(10 * time.Second):
			t.Fatal("the request did not finish after the lock was released")
		}
		if err := a.target.ConfirmTransfer(t.Context(), request.ID, member.ID.String()); err != nil {
			t.Fatalf("confirming the transfer: %v", err)
		}
		if got := a.held(t, "acme").OwnerAccount; got != member.ID.String() {
			t.Errorf("the owner is %q, want %q", got, member.ID)
		}
	})
}
