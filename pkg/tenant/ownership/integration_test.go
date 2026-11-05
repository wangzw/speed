package ownership_test

// The transactional cases: the real account, roster, policy and personal
// modules and this module, over one SQLite database. They hold the property the
// cross-module writes exist for — the roster row, the policy rows and the
// memberships stand or fall together — and they are deliberately end to end
// rather than over fakes: a transaction boundary is the one thing a fake
// transaction cannot answer.

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
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/tenant"
	tenantcatalog "github.com/vislake/speed/pkg/tenant/catalog"
	"github.com/vislake/speed/pkg/tenant/ownership"
	"github.com/vislake/speed/pkg/tenant/personal"
)

// recordingAudit accepts every row.
type recordingAudit struct{}

func (*recordingAudit) Record(context.Context, audit.Entry) error { return nil }

// personalWriteGuard is the host's statement that a personal tenant is not a
// business organization. The owner counter reads it to keep a personal tenant
// from counting as an organization an account cannot leave.
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
	writer      tenant.OrganizationWriter
	handle      *gorm.DB
}

func withAssembly(t *testing.T, body func(t *testing.T, a *assembly)) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "ownership.db")
	configuration := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}}}`, dsn)

	modules := []core.Module{
		sqlite.Module(),
		tenantcatalog.Module(),
		authzpolicy.Module(),
		account.Module(),
		personal.Module(),
		ownership.Module(),
		standIn("stand-in.audit", &recordingAudit{}, (*audit.Audit)(nil)),
		standIn("stand-in.write-guard", personalWriteGuard{}, (*authz.WriteGuard)(nil)),
	}
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
			body(t, resolve(t, reg))
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the assembly to Init: %v", err)
	}
}

func resolve(t *testing.T, reg *core.Registry) *assembly {
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
	return &assembly{
		admin:       admin,
		accounts:    accounts,
		memberships: memberships,
		catalog:     roster,
		manager:     manager,
		writer:      writer,
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

// user opens a user account and answers it. Its personal tenant is created by
// the account transaction.
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

// organization creates a business tenant with owner at its head: the roster
// row, its tenant-admin role, the owner's membership and the owner's binding.
func (a *assembly) organization(t *testing.T, id, alias string, owner authn.Account) {
	t.Helper()
	if err := a.catalog.Create(t.Context(), tenant.Tenant{
		ID: tenant.ID(id), Alias: alias, Status: tenant.StatusActive, OwnerAccount: owner.ID.String(),
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

// join makes an account an active member of the tenant.
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

// A transfer gives the target tenant-admin, writes the owner, and removes the
// previous owner's binding and membership, all in one transaction.
func TestTransferOwnerHandsTheOrganizationOn(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)

		if err := a.writer.TransferOwner(t.Context(), "acme", target.ID.String()); err != nil {
			t.Fatalf("transferring the owner: %v", err)
		}
		if got := a.held(t, "acme").OwnerAccount; got != target.ID.String() {
			t.Errorf("the owner is %q, want %q", got, target.ID)
		}
		if bindings := a.bindingsOf(t, "acme", target); len(bindings) != 1 ||
			bindings[0].Role != authz.Role(authz.TenantAdminRole) {
			t.Errorf("the new owner holds %+v, want tenant-admin", bindings)
		}
		if bindings := a.bindingsOf(t, "acme", owner); len(bindings) != 0 {
			t.Errorf("the previous owner still holds %+v, want nothing", bindings)
		}
		if _, found, err := a.memberships.Membership(t.Context(), "acme", owner.ID); err != nil || found {
			t.Errorf("the previous owner is still a member: found=%t err=%v", found, err)
		}
	})
}

// A target that is not an active member is refused and nothing changes.
func TestTransferOwnerRefusesANonMember(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)

		err := a.writer.TransferOwner(t.Context(), "acme", target.ID.String())
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

// An owner-initiated deletion carries the owner it observed: when a transfer
// has changed the owner, the deletion is refused and the organization stays.
func TestDeleteOrganizationRefusesAnOwnerThatChanged(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)
		if err := a.writer.TransferOwner(t.Context(), "acme", target.ID.String()); err != nil {
			t.Fatalf("transferring the owner: %v", err)
		}

		err := a.writer.DeleteOrganization(t.Context(), "acme", owner.ID.String())
		if !errors.Is(err, tenant.ErrOwnerChanged) {
			t.Fatalf("the deletion gave %v, want ErrOwnerChanged", err)
		}
		if got := a.held(t, "acme"); got.Status != tenant.StatusActive {
			t.Errorf("the tenant is %q, want active", got.Status)
		}
	})
}

// A deletion writes the tombstone and removes the core relations; the
// identifier and alias are not reusable afterwards.
func TestDeleteOrganizationWritesTheTombstoneAndCleansTheCoreRelations(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		a.organization(t, "acme", "acme-org", owner)

		if err := a.writer.DeleteOrganization(t.Context(), "acme", ""); err != nil {
			t.Fatalf("deleting the organization: %v", err)
		}
		got := a.held(t, "acme")
		if got.Status != tenant.StatusDeleted || got.DeletedAt == nil {
			t.Errorf("the tenant is %+v, want a tombstone with a deletion instant", got)
		}
		if got.Alias != "acme-org" || got.ID != "acme" {
			t.Errorf("the tombstone lost its identifier or alias: %+v", got)
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
			ID: "acme", Alias: "another", Status: tenant.StatusActive,
		}); !errors.Is(err, tenant.ErrTenantExists) {
			t.Errorf("registering the deleted identifier gave %v, want ErrTenantExists", err)
		}
		if err := a.catalog.Create(t.Context(), tenant.Tenant{
			ID: "other", Alias: "acme-org", Status: tenant.StatusActive,
		}); !errors.Is(err, tenant.ErrAliasConflict) {
			t.Errorf("registering the deleted alias gave %v, want ErrAliasConflict", err)
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
		if got := a.held(t, "acme"); got.Status != tenant.StatusActive || got.DeletedAt != nil {
			t.Errorf("the rollback left %+v, want the active tenant", got)
		}
		if n := a.count(t, "authz_bindings", "tenant_id = ?", "acme"); n != 1 {
			t.Errorf("the rollback left %d binding(s), want the one binding", n)
		}
	})
}

// A failure after the binding and the owner were written rolls the transfer
// back: the owner stays, the target gains no binding, and the previous owner
// keeps its binding and membership.
func TestTransferOwnerRollsBackAFailedStep(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)
		if err := a.handle.Exec(`CREATE TRIGGER refuse_membership_delete
			BEFORE DELETE ON authn_memberships
			BEGIN SELECT RAISE(ABORT, 'induced failure'); END`).Error; err != nil {
			t.Fatalf("creating the trigger that refuses the last step: %v", err)
		}

		if err := a.writer.TransferOwner(t.Context(), "acme", target.ID.String()); err == nil {
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

		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs[0] = a.writer.TransferOwner(t.Context(), "acme", target.ID.String())
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

		if err := a.writer.TransferOwner(t.Context(), "acme", target.ID.String()); err != nil {
			t.Fatalf("transferring the owner: %v", err)
		}
		if err := a.admin.Delete(t.Context(), owner.ID, authn.ReasonAdmin); err != nil {
			t.Fatalf("deleting the previous owner: %v", err)
		}
	})
}
