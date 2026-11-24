package selfcreate

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

type busStub struct{ event.Bus }

func (busStub) Publish(context.Context, event.Event) error { return nil }
func (busStub) Subscribe(string, string, event.Handler)    {}

const declarerModuleName = "test.selfcreate.declaration"

func declaration() core.Module {
	return core.Module{
		Name:      declarerModuleName,
		Resources: []any{settingsSchema()},
	}
}

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

// TestTheRealStoreDeclaresTheTwoLimitsAsPlatformItems checks that the declared defaults are
// the two product bounds and no item carries tenant access.
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

// TestThePlatformLayerWritesBothNumericLimits checks that both limits are writable on the
// global layer with a positive integer and that other values are refused unstored.
func TestThePlatformLayerWritesBothNumericLimits(t *testing.T) {
	withStore(t, func(store settings.Store) {
		ctx := context.Background()

		// The platform surface passes the number a JSON body decodes to, a float64, and the store
		// coerces it to the declared int.
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

func declaredItem(items []settings.DeclaredItem, key string) (settings.DeclaredItem, bool) {
	for _, item := range items {
		if item.Key == key {
			return item, true
		}
	}
	return settings.DeclaredItem{}, false
}
