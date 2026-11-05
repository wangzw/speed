package selfcreate

// The settings cases drive the real settings module over the declaration this
// module states. They hold that the two numeric limits are ordinary declared
// items a platform administrator writes on the global layer, and that the
// declaration leaves them off the tenant surface.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/internal/routecontract"
	"github.com/vislake/speed/pkg/settings"
)

// busStub is the event capability the settings module takes up. It records
// nothing and delivers nothing: the cases judge the store, not the events.
type busStub struct{ event.Bus }

func (busStub) Publish(context.Context, event.Event) error { return nil }
func (busStub) Subscribe(string, string, event.Handler)    {}

// declarerModuleName is the stand-in module that states this module's settings
// schema. The declaration's Module field is the declaring module's registry
// name, which is why the assertions below name it.
const declarerModuleName = "test.selfcreate.declaration"

// declaration is a module that states this module's settings schema and nothing
// else, so the real settings module builds its key table over the declaration
// the module ships.
func declaration() core.Module {
	return core.Module{
		Name:      declarerModuleName,
		Resources: []any{settingsSchema()},
	}
}

// withStore drives the real settings module over a real SQLite engine and this
// module's declaration, and runs check inside the drive: the engine's lifecycle
// closes the database when the drive returns. The engine applies the settings
// module's own migration set, so the store runs over the table an assembly
// gives it.
func withStore(t *testing.T, check func(settings.Store)) {
	t.Helper()
	engine := sqlite.Module()
	declarer := declaration()
	modules := []core.Module{
		engine,
		routecontract.StandIn("stand-in.bus", busStub{}, (*event.Bus)(nil)),
		declarer,
		settings.Module(),
	}
	document := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}}}`, filepath.Join(t.TempDir(), "settings.db"))
	reader := configtest.Reader(t, document, modules...)

	reg := core.New()
	reg.Register(config.Module())
	names := []string{config.ModuleName}
	for _, module := range modules {
		reg.Register(module)
		names = append(names, module.Name)
	}
	reg.Supply(reader, config.ModuleName)

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action: func(_ context.Context, reg *core.Registry) error {
			store, err := core.Resolve[settings.Store](reg)
			if err != nil {
				return err
			}
			check(store)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the settings module: %v", err)
	}
}

// The declared defaults are the product's two bounds, and the declaration
// carries no tenant access.
func TestTheRealStoreDeclaresTheTwoLimitsAsPlatformItems(t *testing.T) {
	withStore(t, func(store settings.Store) {
		declared := store.Declared()
		want := map[string]string{
			maxOrganizationsPath: "5",
			maxPerHourPath:       "3",
		}
		for key, value := range want {
			item, found := declaredItem(declared, key)
			if !found {
				t.Errorf("the declaration does not list %q", key)
				continue
			}
			if item.Default != value {
				t.Errorf("%q default = %q, want %q", key, item.Default, value)
			}
			if item.Module != declarerModuleName {
				t.Errorf("%q is declared by %q, want %q", key, item.Module, declarerModuleName)
			}
			if item.TenantAccess != settings.TenantNone {
				t.Errorf("%q carries tenant access %q, want none", key, item.TenantAccess)
			}
		}
	})
}

// A platform administrator edits both numeric items on the global layer, the
// domain accepts a positive integer, and a value outside it is refused before
// anything is stored.
func TestThePlatformLayerWritesBothNumericLimits(t *testing.T) {
	withStore(t, func(store settings.Store) {
		ctx := context.Background()

		// The platform surface hands the store the number a JSON body decoded
		// to: a float64. The store coerces it to the declared int, which is what
		// makes the item editable from the settings page.
		for key, value := range map[string]float64{
			maxOrganizationsPath: 10,
			maxPerHourPath:       6,
		} {
			if err := store.Set(ctx, settings.Write{
				Layer: settings.Global(), Key: key, Value: value, Actor: "operator",
			}); err != nil {
				t.Fatalf("writing %s = %v: %v", key, value, err)
			}
			var read int
			if err := store.Get(settings.Global(), key, &read); err != nil {
				t.Fatalf("reading %s back: %v", key, err)
			}
			if read != int(value) {
				t.Errorf("%s read back as %d, want %d", key, read, int(value))
			}
		}

		for _, key := range []string{maxOrganizationsPath, maxPerHourPath} {
			err := store.Set(ctx, settings.Write{
				Layer: settings.Global(), Key: key, Value: float64(0), Actor: "operator",
			})
			if !errors.Is(err, settings.ErrInvalidValue) {
				t.Errorf("%s = 0 gave %v, want ErrInvalidValue", key, err)
			}
		}
	})
}

// declaredItem finds one item in a declaration listing.
func declaredItem(items []settings.DeclaredItem, key string) (settings.DeclaredItem, bool) {
	for _, item := range items {
		if item.Key == key {
			return item, true
		}
	}
	return settings.DeclaredItem{}, false
}
