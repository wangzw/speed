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

func supply(reg *core.Registry, name string, token core.Token, instance any) {
	reg.Register(core.Module{Name: name, Provides: []core.Provision{{Token: token}}})
	reg.Supply(instance, name)
}

func createTables(t *testing.T, handle *gorm.DB) {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE authz_roles (
			tenant_id TEXT NOT NULL CHECK (tenant_id <> ''), role TEXT NOT NULL,
			builtin BOOLEAN NOT NULL, description_key TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL,
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

const routerBody = `{"authz":{"guard":{"endpoints":["public"]}}}`

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
	supply(reg, "stand-in.router", (*speedhttp.Router)(nil), routerStub{})

	names := []string{policy.ModuleName, guard.ModuleName, stand.name, "stand-in.router"}
	if roster != nil {
		supply(reg, "stand-in.roster", (*tenant.Catalog)(nil), roster)
		names = append(names, "stand-in.roster")
	}
	return reg, database, names
}

type routerStub struct{}

func (routerStub) Endpoint(string) (speedhttp.Endpoint, error) { return endpointStub{}, nil }
func (routerStub) Endpoints() []string                         { return []string{"public"} }

type endpointStub struct{}

func (endpointStub) Route(string, string, ...gin.HandlerFunc) {}
func (endpointStub) Use(speedhttp.Middleware)                 {}
func (endpointStub) Accepting() bool                          { return true }

func identity(t *testing.T) authn.Identity {
	t.Helper()
	ctx := authntest.Context(t, t.Context(), authntest.WithAccount("acc1"))
	id, ok := authn.FromContext(ctx)
	if !ok {
		t.Fatalf("the fixture left no identity on the context")
	}
	return id
}

type rosterStub struct{ ids map[tenant.ID]bool }

func (r rosterStub) Get(_ context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
	return tenant.Tenant{ID: id}, r.ids[id], nil
}

func (rosterStub) ByName(context.Context, string) (tenant.Tenant, bool, error) {
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
func (rosterStub) Delete(context.Context, tenant.ID) error           { return nil }
func (rosterStub) SetOwner(context.Context, tenant.ID, string) error { return nil }
func (rosterStub) SetOwnerIfEmpty(context.Context, tenant.ID, string) (bool, error) {
	return false, nil
}

func (rosterStub) SetProfile(context.Context, tenant.ID, tenant.Profile) error {
	return nil
}
func (rosterStub) List(context.Context) ([]tenant.Tenant, error)             { return nil, nil }
func (rosterStub) Enter(context.Context, tenant.ID) (context.Context, error) { return nil, nil }
func (rosterStub) EnterRegistered(context.Context, tenant.ID) (context.Context, error) {
	return nil, nil
}

// TestTheRegistryWiresPolicyAndGuard checks that the registry wires policy and guard.
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

// TestTheStartupRefusesARosterWithoutThePlatformDomain checks that the startup refuses a roster without the platform domain.
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

// TestTheStartupAcceptsARosterCarryingThePlatformDomain checks that the startup accepts a roster carrying the platform domain.
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

// TestTheCatalogTranslatesWithoutTheGuardModule checks that the catalog translates without the guard module.
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
