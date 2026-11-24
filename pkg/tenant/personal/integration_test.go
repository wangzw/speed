package personal_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

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
	"github.com/vislake/speed/pkg/tenant/personal"
)

type recordingAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (a *recordingAudit) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

func (a *recordingAudit) rows() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.entries...)
}

func entriesOfAction(entries []audit.Entry, action string) []audit.Entry {
	var out []audit.Entry
	for _, entry := range entries {
		if entry.Action == action {
			out = append(out, entry)
		}
	}
	return out
}

type personalWriteGuard struct{}

func (personalWriteGuard) Denied(_ context.Context, tenantID string, _ authz.WriteOp) (bool, error) {
	return personal.IsPersonalTenantID(tenantID) || tenantID == string(authz.PlatformDomain), nil
}

type assembly struct {
	admin       authn.AccountAdmin
	accounts    authn.Accounts
	memberships authn.Memberships
	catalog     tenant.Catalog
	audit       *recordingAudit
	handle      *gorm.DB
}

// withAssembly drives the real modules to Init over a SQLite database of the
// case's own; body runs inside the drive because it closes the database afterwards.
func withAssembly(t *testing.T, body func(t *testing.T, a *assembly)) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "personal.db")
	configuration := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}}}`, dsn)
	auditor := &recordingAudit{}

	modules := []core.Module{
		sqlite.Module(),
		tenantcatalog.Module(),
		authzpolicy.Module(),
		account.Module(),
		personal.Module(),
		standIn("stand-in.audit", auditor, (*audit.Audit)(nil)),
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
	recorder, err := core.Resolve[audit.Audit](reg)
	if err != nil {
		t.Fatalf("taking up the audit capability: %v", err)
	}
	auditor, ok := recorder.(*recordingAudit)
	if !ok {
		t.Fatalf("the audit capability is %T, want the recording stand-in", recorder)
	}
	return &assembly{
		admin:       admin,
		accounts:    accounts,
		memberships: memberships,
		catalog:     roster,
		audit:       auditor,
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

func (a *assembly) count(t *testing.T, table, where string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := a.handle.Table(table).Where(where, args...).Count(&n).Error; err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return n
}

// An account and its personal tenant come into being in one transaction.
func TestAnAccountAndItsPersonalTenantStandTogether(t *testing.T) {
	for _, via := range []string{"admin", "jit"} {
		t.Run(via, func(t *testing.T) {
			withAssembly(t, func(t *testing.T, a *assembly) {
				created, err := a.admin.Create(t.Context(), authn.CreateAccount{Username: "alice", Via: via})
				if err != nil {
					t.Fatalf("creating an account: %v", err)
				}
				personalID := personal.TenantID(created.ID)
				held, found, err := a.catalog.Get(t.Context(), personalID)
				if err != nil || !found {
					t.Fatalf("reading the personal tenant: found=%t err=%v", found, err)
				}
				if held.Name != "alice" {
					t.Errorf("the personal tenant's name is %q, want the username", held.Name)
				}
				if held.OwnerAccount != created.ID.String() {
					t.Errorf("the personal tenant's owner is %q, want %q", held.OwnerAccount, created.ID)
				}
				if n := a.count(t, "authn_memberships", "account_id = ? AND tenant_id = ?", created.ID.String(), string(personalID)); n != 1 {
					t.Errorf("the account holds %d personal membership(s), want one", n)
				}
				if n := a.count(t, "authz_bindings", "tenant_id = ? AND account_id = ?", string(personalID), created.ID.String()); n != 1 {
					t.Errorf("the account holds %d personal binding(s), want one", n)
				}
				identifiers, err := a.accounts.Identifiers(t.Context(), created.ID)
				if err != nil {
					t.Fatalf("reading the identifiers: %v", err)
				}
				var usernameID string
				for _, identifier := range identifiers {
					if identifier.Kind == authn.IdentifierUsername {
						usernameID = identifier.ID
					}
				}
				if usernameID == "" {
					t.Fatalf("the account holds no username identifier: %+v", identifiers)
				}
				if err := a.admin.RemoveIdentifier(t.Context(), created.ID, usernameID); !errors.Is(err, authn.ErrUsernameImmutable) {
					t.Errorf("removing the username gave %v, want ErrUsernameImmutable", err)
				}
			})
		})
	}
}

// A creation whose provisioning fails after the tenant row was written leaves
// neither the account nor the tenant row.
func TestAFailedProvisioningLeavesNoAccount(t *testing.T) {
	for _, via := range []string{"admin", "jit"} {
		t.Run(via, func(t *testing.T) {
			withAssembly(t, func(t *testing.T, a *assembly) {
				if err := a.handle.Exec("DROP TABLE authn_memberships").Error; err != nil {
					t.Fatalf("dropping the memberships table: %v", err)
				}
				if _, err := a.admin.Create(t.Context(), authn.CreateAccount{Username: "alice", Via: via}); err == nil {
					t.Fatal("creating an account whose provisioning failed succeeded")
				}
				if n := a.count(t, "authn_accounts", ""); n != 0 {
					t.Errorf("the failed provisioning left %d account row(s), want none", n)
				}
				if n := a.count(t, "tenants", ""); n != 0 {
					t.Errorf("the failed provisioning left %d tenant row(s), want none", n)
				}
			})
		})
	}
}

// A username already held as a business tenant's name rolls the whole creation
// back with ErrAccountExists.
func TestAnNameConflictLeavesNoAccount(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		if err := a.catalog.Create(t.Context(), tenant.Tenant{
			ID: "business", Name: "bob", Status: tenant.StatusActive,
		}); err != nil {
			t.Fatalf("registering the business tenant: %v", err)
		}
		_, err := a.admin.Create(t.Context(), authn.CreateAccount{Username: "bob", Via: "admin"})
		if err == nil {
			t.Fatal("creating an account under a held name succeeded")
		}
		if !errors.Is(err, authn.ErrAccountExists) {
			t.Errorf("the name conflict gave %v, want the account module's ErrAccountExists", err)
		}
		if errors.Is(err, authn.ErrUnavailable) {
			t.Errorf("the refusal = %v, want it not to read as an unavailable dependency", err)
		}
		if n := a.count(t, "authn_accounts", ""); n != 0 {
			t.Errorf("the refused creation left %d account row(s), want none", n)
		}
		if n := a.count(t, "tenants", "name = ?", "bob"); n != 1 {
			t.Errorf("the refused creation changed the tenant rows carrying bob: %d", n)
		}
	})
}

// A deletion whose tombstone write fails rolls back the account's own erase:
// the account is in service and its identifiers are still there.
func TestAFailedTombstoneLeavesTheAccountInService(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		created, err := a.admin.Create(t.Context(), authn.CreateAccount{Username: "carol", Via: "admin"})
		if err != nil {
			t.Fatalf("creating an account: %v", err)
		}
		if err := a.handle.Exec("DROP TABLE tenants").Error; err != nil {
			t.Fatalf("dropping the tenants table: %v", err)
		}
		if err := a.admin.Delete(t.Context(), created.ID, "admin"); err == nil {
			t.Fatal("deleting an account whose tombstone write failed succeeded")
		}
		if n := a.count(t, "authn_accounts", "id = ? AND status = ?", created.ID.String(), 1); n != 1 {
			t.Errorf("the refused deletion left %d live account row(s), want one", n)
		}
		if n := a.count(t, "authn_identifiers", "account_id = ?", created.ID.String()); n == 0 {
			t.Errorf("the refused deletion erased the account's identifiers")
		}
	})
}

// A service account owns no personal tenant.
func TestAServiceAccountOwnsNoPersonalTenant(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		created, err := a.admin.Create(t.Context(), authn.CreateAccount{Kind: authn.KindService, Via: "admin"})
		if err != nil {
			t.Fatalf("creating a service account: %v", err)
		}
		if n := a.count(t, "tenants", "id = ?", string(personal.TenantID(created.ID))); n != 0 {
			t.Errorf("a service account was given a personal tenant")
		}
	})
}

// A personal tenant keeps its name after the account is deleted.
func TestTheTombstoneKeepsTheName(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		created, err := a.admin.Create(t.Context(), authn.CreateAccount{Username: "dave", Via: "admin"})
		if err != nil {
			t.Fatalf("creating an account: %v", err)
		}
		if err := a.admin.Delete(t.Context(), created.ID, "admin"); err != nil {
			t.Fatalf("deleting the account: %v", err)
		}
		personalID := personal.TenantID(created.ID)
		var name string
		if err := a.handle.Table("tenants").Select("name").Where("id = ?", string(personalID)).Scan(&name).Error; err != nil {
			t.Fatalf("reading the tombstone's name: %v", err)
		}
		if name != "dave" {
			t.Errorf("the tombstone's name is %q, want the username", name)
		}
	})
}

// A committed account creation leaves its creation audit row.
func TestCreatingAnAccountWritesTheCreationAuditRow(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		_, err := a.admin.Create(t.Context(), authn.CreateAccount{Username: "erin", Via: "admin"})
		if err != nil {
			t.Fatalf("creating an account: %v", err)
		}
		rows := entriesOfAction(a.audit.rows(), "org.personal.tenant.create")
		if len(rows) != 1 || rows[0].Object != "erin" || rows[0].Outcome != "success" {
			t.Errorf("the creation wrote the audit rows %+v, want one successful row on the name %q", rows, "erin")
		}
	})
}

// The deletion hook removes the other members the transaction seam cannot reach.
func TestDeletingAnAccountRemovesTheOtherMembersOfItsPersonalTenant(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		alice, err := a.admin.Create(t.Context(), authn.CreateAccount{Username: "alice", Via: "admin"})
		if err != nil {
			t.Fatalf("creating alice: %v", err)
		}
		bob, err := a.admin.Create(t.Context(), authn.CreateAccount{Username: "bob", Via: "admin"})
		if err != nil {
			t.Fatalf("creating bob: %v", err)
		}
		personalID := string(personal.TenantID(alice.ID))
		if err := a.memberships.Put(t.Context(), authn.Membership{
			Account: bob.ID, Tenant: personalID, Active: true, Source: "admin",
		}); err != nil {
			t.Fatalf("adding bob to alice's personal tenant: %v", err)
		}
		if n := a.count(t, "authn_memberships", "account_id = ? AND tenant_id = ?", bob.ID.String(), personalID); n != 1 {
			t.Fatalf("bob holds %d membership(s) in alice's personal tenant before the deletion, want one", n)
		}
		if err := a.admin.Delete(t.Context(), alice.ID, "admin"); err != nil {
			t.Fatalf("deleting alice: %v", err)
		}
		if n := a.count(t, "authn_memberships", "account_id = ? AND tenant_id = ?", bob.ID.String(), personalID); n != 0 {
			t.Errorf("the deletion left %d membership(s) of bob in alice's personal tenant, want none", n)
		}
	})
}
