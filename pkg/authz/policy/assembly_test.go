package policy_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
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
	speedhttp "github.com/vislake/speed/pkg/http"
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

// routerBody satisfies the guard module's required endpoint list, so a drive
// that only needs the module to wire reaches Init.
const routerBody = `{"authz":{"guard":{"endpoints":["public"]}}}`

// assembly builds a registry holding the policy and guard modules and the
// stand-ins they need.
func assembly(t *testing.T, roster tenant.Catalog) (*core.Registry, db.Database, []string) {
	t.Helper()
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "authz.db"), nil)
	createTables(t, database.DB())

	policyModule := policy.Module()
	guardModule := guard.Module()
	reader := authztest.ConfigReaderAll(t, routerBody, policyModule, guardModule)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(policyModule)
	reg.Register(guardModule)
	stand := dbStandIn{name: "stand-in.database", base: database}
	reg.Register(stand.module())
	// The guard module takes the entry point up at Init; the stand-in declares
	// the endpoint the configuration guards, so the module wires without a
	// listener.
	supply(reg, "stand-in.router", (*speedhttp.Router)(nil), routerStub{})

	names := []string{policy.ModuleName, guard.ModuleName, stand.name, "stand-in.router"}
	if roster != nil {
		supply(reg, "stand-in.roster", (*tenant.Catalog)(nil), roster)
		names = append(names, "stand-in.roster")
	}
	return reg, database, names
}

// routerStub stands for an entry point module: one endpoint whose registrations
// are dropped, so wiring succeeds without a listener.
type routerStub struct{}

func (routerStub) Endpoint(string) (speedhttp.Endpoint, error) { return endpointStub{}, nil }
func (routerStub) Endpoints() []string                         { return []string{"public"} }

// endpointStub accepts registrations and does nothing with them.
type endpointStub struct{}

func (endpointStub) Route(string, string, ...gin.HandlerFunc) {}
func (endpointStub) Use(speedhttp.Middleware)                 {}
func (endpointStub) Accepting() bool                          { return true }

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

func (rosterStub) SetStatusByAccount(context.Context, tenant.ID, tenant.Status) error {
	return nil
}
func (rosterStub) Delete(context.Context, tenant.ID) error                   { return nil }
func (rosterStub) SetAlias(context.Context, tenant.ID, string) error         { return nil }
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

// A roster with no tenant carrying the platform domain stops the startup.
func TestTheStartupRefusesARosterWithoutThePlatformDomain(t *testing.T) {
	reg, _, names := assembly(t, rosterStub{ids: map[tenant.ID]bool{"acme": true}})
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action:       func(context.Context, *core.Registry) error { return nil },
	})
	if !errors.Is(err, authz.ErrPlatformDomainMissing) {
		t.Fatalf("the drive ended with %v, want ErrPlatformDomainMissing", err)
	}
}

// A roster that carries the platform domain passes the startup check.
func TestTheStartupAcceptsARosterCarryingThePlatformDomain(t *testing.T) {
	reg, _, names := assembly(t, rosterStub{ids: map[tenant.ID]bool{
		"acme": true, tenant.ID(authz.PlatformDomain): true,
	}})
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action:       func(context.Context, *core.Registry) error { return nil },
	})
	if err != nil {
		t.Fatalf("the drive ended with %v, want the startup to pass", err)
	}
}

// A command that writes policy data in a permission's name reaches the
// translation without the enforcement module and without an HTTP entry point:
// the catalog this module delivers answers the (object, action) pair a
// permission translates to, for a permission the capability carries and for one
// a module declared of its own.
//
// The drive names no guard module, and that is the whole of what the case holds.
// The enforcement module is where a decision reads the translation, and it
// refuses to wire without an entry point — so while the translation was resolved
// there, a bootstrap that seeds roles had to stand an entry point up to write a
// grant. Its absence here is the fix.
func TestTheCatalogTranslatesWithoutTheGuardModule(t *testing.T) {
	declared := authz.Permission("crm.deal.read")
	const declaredKey = "crm.permission.deal-read"
	translator := core.Module{
		Name: "stand-in.translator",
		Resources: []any{
			authz.PermissionSpec{
				Permissions:     []authz.Permission{declared},
				DescriptionKeys: map[authz.Permission]string{declared: declaredKey},
			},
			authz.TranslationSpec{Rows: map[authz.Permission]authz.Translation{
				declared: {Object: "deal", Action: "read"},
			}},
		},
	}

	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "authz.db"), nil)
	createTables(t, database.DB())

	policyModule := policy.Module()
	reader := authztest.ConfigReaderAll(t, `{}`, policyModule, translator)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(policyModule)
	reg.Register(translator)
	stand := dbStandIn{name: "stand-in.database", base: database}
	reg.Register(stand.module())

	names := []string{policyModule.Name, translator.Name, stand.name}
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action: func(_ context.Context, reg *core.Registry) error {
			catalog, err := core.Resolve[authz.Catalog](reg)
			if err != nil {
				return err
			}
			cases := []struct {
				permission authz.Permission
				object     string
				action     string
			}{
				{authz.PermissionAccountRead, "account", "read"},
				{declared, "deal", "read"},
			}
			for _, tc := range cases {
				object, action, ok := catalog.ObjectAction(tc.permission)
				if !ok || object != tc.object || action != tc.action {
					t.Errorf("the catalog translates %q to (%q, %q, %t) with no guard module and no entry point, "+
						"want (%q, %q, true)", tc.permission, object, action, ok, tc.object, tc.action)
				}
			}
			// The key a declaring module gave a permission reaches a caller
			// through the interface, not only through the concrete catalog: the
			// management surface renders a description from it.
			if key, ok := catalog.DescriptionKey(declared); !ok || key != declaredKey {
				t.Errorf("DescriptionKey(%q) = (%q, %t) through the catalog interface, want (%q, true)",
					declared, key, ok, declaredKey)
			}
			if key, ok := catalog.DescriptionKey("crm.undeclared"); ok || key != "" {
				t.Errorf("DescriptionKey of an undeclared permission = (%q, %t), want (\"\", false)", key, ok)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}
