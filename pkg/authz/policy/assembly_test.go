package policy_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/authztest"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/tenant"
)

// The cases here drive the real descriptors through a real registry: the
// configuration module with a reader the real loader produced, the policy
// module, the guard module, and stand-ins for the capabilities they take. What
// they hold is what a handle built by hand cannot: which callback runs which
// step, and that the two modules resolve each other's capabilities.

// dbStandIn delivers the database capability over a handle the database module
// opened, so that the policy module reaches what an assembly hands it.
type dbStandIn struct {
	name string
	base db.Database
}

func (d dbStandIn) module() core.Module {
	return core.Module{
		Name:     d.name,
		Provides: []core.Provision{{Token: (*db.Database)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return d.base, nil },
	}
}

// supply registers a stand-in module and supplies its instance.
func supply(reg *core.Registry, name string, token core.Token, instance any) {
	reg.Register(core.Module{Name: name, Provides: []core.Provision{{Token: token}}})
	reg.Supply(instance, name)
}

// createTables creates the three policy tables on a handle the database module
// delivered.
func createTables(t *testing.T, handle *gorm.DB) {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE authz_roles (
			tenant_id TEXT NOT NULL CHECK (tenant_id <> ''), role TEXT NOT NULL,
			builtin BOOLEAN NOT NULL, description_key TEXT NOT NULL, created_at DATETIME NOT NULL,
			PRIMARY KEY (tenant_id, role))`,
		`CREATE TABLE authz_grants (
			tenant_id TEXT NOT NULL CHECK (tenant_id <> ''), role TEXT NOT NULL,
			resource TEXT NOT NULL, action TEXT NOT NULL, created_at DATETIME NOT NULL,
			PRIMARY KEY (tenant_id, role, resource, action))`,
		`CREATE TABLE authz_bindings (
			tenant_id TEXT NOT NULL CHECK (tenant_id <> ''), account_id TEXT NOT NULL,
			role TEXT NOT NULL, granted_at DATETIME NOT NULL,
			PRIMARY KEY (tenant_id, account_id, role))`,
	} {
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("creating a policy table: %v", err)
		}
	}
}

// assembly builds a registry holding the policy and guard modules and the
// stand-ins they need.
func assembly(t *testing.T, roster tenant.Catalog) (*core.Registry, db.Database, []string) {
	t.Helper()
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "authz.db"), nil)
	createTables(t, database.DB())

	policyModule := policy.Module()
	guardModule := guard.Module()
	reader := authztest.ConfigReaderAll(t, `{}`, policyModule, guardModule)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(policyModule)
	reg.Register(guardModule)
	stand := dbStandIn{name: "stand-in.database", base: database}
	reg.Register(stand.module())

	names := []string{policy.ModuleName, guard.ModuleName, stand.name}
	if roster != nil {
		supply(reg, "stand-in.roster", (*tenant.Catalog)(nil), roster)
		names = append(names, "stand-in.roster")
	}
	return reg, database, names
}

// identity builds the identity the authentication fixture would have left on a
// request, for a decision's actor.
func identity(t *testing.T) authn.Identity {
	t.Helper()
	ctx := authntest.Context(t, t.Context(), authntest.WithAccount("acc1"))
	id, ok := authn.FromContext(ctx)
	if !ok {
		t.Fatalf("the fixture left no identity on the context")
	}
	return id
}

// rosterStub carries a fixed set of identifiers.
type rosterStub struct{ ids map[tenant.ID]bool }

func (r rosterStub) Get(_ context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
	return tenant.Tenant{ID: id}, r.ids[id], nil
}

func (rosterStub) ByAlias(context.Context, string) (tenant.Tenant, bool, error) {
	return tenant.Tenant{}, false, nil
}
func (rosterStub) Put(context.Context, tenant.Tenant) error    { return nil }
func (rosterStub) Create(context.Context, tenant.Tenant) error { return nil }
func (rosterStub) SetStatus(context.Context, tenant.ID, tenant.Status) error {
	return nil
}
func (rosterStub) List(context.Context) ([]tenant.Tenant, error)             { return nil, nil }
func (rosterStub) Enter(context.Context, tenant.ID) (context.Context, error) { return nil, nil }
func (rosterStub) EnterRegistered(context.Context, tenant.ID) (context.Context, error) {
	return nil, nil
}

// A role, its grant and a binding written through the manager decide the guard
// module's decider, driven through the real registry: the two modules resolve
// each other's capabilities and the seam answers from the policy the manager
// wrote.
func TestTheRegistryWiresPolicyAndGuard(t *testing.T) {
	reg, _, names := assembly(t, nil)

	driveErr := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action: func(ctx context.Context, reg *core.Registry) error {
			manager, err := core.Resolve[authz.Manager](reg)
			if err != nil {
				return err
			}
			if err = manager.PutRole(ctx, authz.RoleRecord{Tenant: "acme", Role: "roleA", DescriptionKey: "k"}); err != nil {
				return err
			}
			if err = manager.PutGrant(ctx, authz.GrantRecord{Tenant: "acme", Role: "roleA", Resource: "account", Action: "read"}); err != nil {
				return err
			}
			if err = manager.PutBinding(ctx, authz.Binding{Tenant: "acme", Account: "acc1", Role: "roleA"}); err != nil {
				return err
			}
			decider, err := core.Resolve[authn.Decider](reg)
			if err != nil {
				return err
			}
			id := identity(t)
			allow, err := decider.Decide(ctx, authn.Decision{
				Action: authn.ActionAccountRead, Actor: id, Tenant: "acme", Target: "acc1",
			})
			if err != nil {
				return err
			}
			if !allow {
				t.Errorf("the decider refused a granted action")
			}
			// A different tenant's query is refused: the policy is scoped.
			denied, err := decider.Decide(ctx, authn.Decision{
				Action: authn.ActionAccountRead, Actor: id, Tenant: "globex", Target: "acc1",
			})
			if err != nil {
				return err
			}
			if denied {
				t.Errorf("the decider allowed the action in a tenant with no binding")
			}
			return nil
		},
	})
	if driveErr != nil {
		t.Fatalf("the assembly did not drive: %v", driveErr)
	}
}

// A roster holding a tenant named for the platform domain stops the startup.
func TestTheStartupRefusesThePlatformDomainTaken(t *testing.T) {
	reg, _, names := assembly(t, rosterStub{ids: map[tenant.ID]bool{tenant.ID(authz.PlatformDomain): true}})
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action:       func(context.Context, *core.Registry) error { return nil },
	})
	if !errors.Is(err, authz.ErrPlatformDomainTaken) {
		t.Fatalf("the drive ended with %v, want ErrPlatformDomainTaken", err)
	}
}
