package resolve

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/tenant"
)

// The tests in this file are about an instance a host supplies for this module,
// to stand where the resolver its New builds would stand. Prepare and New do not
// run for it, and every callback from Migrate to Close is handed it. The
// callbacks recognise only the resolver this module builds, so what these tests
// observe is what an instance of any other type is left to.

// hostResolution is an instance of the host's own that delivers the capability
// and is not the resolver this module builds. It has no behaviour of its own:
// its methods are the nil interface's.
type hostResolution struct{ tenant.Resolution }

// wrappedResolver is a Resolution a host wrote around the resolver this module
// builds: it delivers the capability and is not the type the callbacks act on.
// The product is embedded, so its unexported methods are promoted along with
// Register, and a callback that acted on whatever carries them would act on the
// resolver inside.
type wrappedResolver struct{ *resolver }

var _ tenant.Resolution = wrappedResolver{}

// quietResolver is a resolver selecting the header extractor whose startup line
// goes nowhere, so that a case that seals one does not write to the process's
// standard output.
func quietResolver() *resolver {
	r := newResolver(settings{extractor: HeaderExtractor, header: "X-Tenant"}, nil)
	r.logger = slog.New(slog.DiscardHandler)
	return r
}

// TestCallbacksActOnTheModulesOwnProduct is the control of the case below:
// handed the resolver this module builds, Migrate opens its registration window,
// Init goes on to look for the entry point it registers the layer on, and Start
// shuts the window and settles the extractor. Without it the observations made
// on a supplied instance could not move.
//
// The registry holds no entry point, so Init over the product ends in the
// sentinel that says so, which is how the case sees Init reach it.
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

// TestCallbacksLeaveASuppliedInstanceAlone pins that Migrate, Init and Start do
// nothing for an instance the host supplied: they open no window, register no
// layer and settle no extractor, whatever the instance carries.
//
// The wrapper is the sharper of the two. It carries the resolver's own methods,
// so a callback that took any value having them for its product would pass over
// the instance of a type of the host's own and still act here. Each observation
// is made on a resolver nobody else has touched, so a change in it can only have
// come from the callback just called.
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
		// A callback that acted would fail here for want of an entry point.
		if err := m.Init(ctx, core.New(), wrapped); err != nil {
			t.Fatalf("Init over a wrapper: %v", err)
		}
		if len(inner.extractors) != 0 {
			t.Error("Init registered the built-in extractors on the resolver inside the wrapper")
		}

		// The owner opens the window of the resolver it built and fills it, and
		// Start has to leave both as they are.
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
