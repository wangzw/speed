package resolve

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/tenant"
)

type hostResolution struct{ tenant.Resolution }

// wrappedResolver embeds the resolver, so its unexported methods are promoted: a
// callback that acted on any value carrying them would act on the resolver inside.
type wrappedResolver struct{ *resolver }

var _ tenant.Resolution = wrappedResolver{}

func quietResolver() *resolver {
	r := newResolver(settings{extractor: HeaderExtractor, header: "X-Tenant"}, nil)
	r.logger = slog.New(slog.DiscardHandler)
	return r
}

// TestCallbacksActOnTheModulesOwnProduct checks that for the resolver this module builds, Migrate opens the registration window, Init looks for the entry point and Start closes the window and settles the extractor.
func TestCallbacksActOnTheModulesOwnProduct(t *testing.T) {
	m := Module()
	ctx := context.Background()
	r := quietResolver()

	if err := m.Migrate(ctx, nil, r); err != nil {
		t.Fatalf("Migrate over this module's own product: %v", err)
	}
	if !r.openWindow {
		t.Fatal("Migrate did not open the registration window of this module's own product")
	}
	if err := m.Init(ctx, core.New(), r); !errors.Is(err, tenant.ErrNoRouter) {
		t.Fatalf("Init over this module's own product, on a registry with no entry point, gave %v, "+
			"want ErrNoRouter", err)
	}
	if err := m.Start(ctx, nil, r); err != nil {
		t.Fatalf("Start over this module's own product: %v", err)
	}
	if r.openWindow {
		t.Error("Start did not shut the registration window of this module's own product")
	}
	if r.selected == nil {
		t.Error("Start did not settle the extractor of this module's own product")
	}
}

// TestCallbacksLeaveASuppliedInstanceAlone checks that Migrate, Init and Start do nothing for an instance the host supplied, even one that wraps the resolver.
func TestCallbacksLeaveASuppliedInstanceAlone(t *testing.T) {
	m := Module()
	ctx := context.Background()

	t.Run("an instance of a type of the host's own, or none", func(t *testing.T) {
		callbacks := map[string]func(context.Context, *core.Registry, any) error{
			"Migrate": m.Migrate, "Init": m.Init, "Start": m.Start,
		}
		for name, callback := range callbacks {
			for _, instance := range []any{hostResolution{}, nil} {
				if err := callback(ctx, core.New(), instance); err != nil {
					t.Errorf("%s over %#v gave %v, want it left alone", name, instance, err)
				}
			}
		}
	})

	t.Run("a wrapper around this module's own product", func(t *testing.T) {
		inner := quietResolver()
		wrapped := wrappedResolver{inner}

		if err := m.Migrate(ctx, nil, wrapped); err != nil {
			t.Fatalf("Migrate over a wrapper: %v", err)
		}
		if inner.openWindow {
			t.Error("Migrate opened the registration window of the resolver inside the wrapper")
		}
		if err := m.Init(ctx, core.New(), wrapped); err != nil {
			t.Fatalf("Init over a wrapper: %v", err)
		}
		if len(inner.extractors) != 0 {
			t.Error("Init registered the built-in extractors on the resolver inside the wrapper")
		}

		inner.open()
		inner.registerBuiltins()
		if err := m.Start(ctx, nil, wrapped); err != nil {
			t.Fatalf("Start over a wrapper: %v", err)
		}
		if !inner.openWindow {
			t.Error("Start shut the registration window of the resolver inside the wrapper")
		}
		if inner.selected != nil {
			t.Error("Start settled the extractor of the resolver inside the wrapper")
		}
	})
}
