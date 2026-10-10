package catalog

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/tenant"
)

// The tests in this file are about an instance a host supplies for this module,
// to stand where the roster its New builds would stand. Prepare and New do not
// run for it, and every callback from Migrate to Close is handed it. The
// callbacks recognise only the roster this module builds, so what these tests
// observe is what an instance of any other type is left to.

// hostCatalog is an instance of the host's own that delivers the capability and
// is not the roster this module builds. It has no behaviour of its own: its
// methods are the nil interface's.
type hostCatalog struct{ tenant.Catalog }

// wrappedRoster is a Catalog a host wrote around the roster this module builds:
// it delivers the capability and is not the type the callbacks act on. The
// product is embedded, so its unexported methods are promoted along with the
// capability's, and a callback that acted on whatever carries them would act on
// the roster inside.
type wrappedRoster struct{ *roster }

var _ tenant.Catalog = wrappedRoster{}

// quietRoster is rosterOverPeriod with the startup account sent nowhere, so that
// a case that loads one does not write to the process's standard output.
func quietRoster(t *testing.T, period time.Duration, rows ...string) *roster {
	t.Helper()
	r := rosterOverPeriod(t, period, rows...)
	r.logger = slog.New(slog.DiscardHandler)
	return r
}

// TestCallbacksActOnTheModulesOwnProduct is the control of the case below:
// handed the roster this module builds, Init loads its snapshot, Start starts
// its refresher and Stop ends it. Without it the observations made on a supplied
// instance could not move.
func TestCallbacksActOnTheModulesOwnProduct(t *testing.T) {
	m := Module()
	ctx := context.Background()
	r := quietRoster(t, time.Hour, insert("acme", "acme"))
	t.Cleanup(r.stop)

	if err := m.Init(ctx, core.New(), r); err != nil {
		t.Fatalf("Init over this module's own product: %v", err)
	}
	if r.snap.Load() == nil {
		t.Fatal("Init did not load the snapshot of this module's own product")
	}
	if err := m.Start(ctx, core.New(), r); err != nil {
		t.Fatalf("Start over this module's own product: %v", err)
	}
	if r.stopCh == nil {
		t.Fatal("Start did not start the refresher of this module's own product")
	}
	if err := m.Stop(ctx, core.New(), r); err != nil {
		t.Fatalf("Stop over this module's own product: %v", err)
	}
	select {
	case <-r.stopCh:
	default:
		t.Fatal("Stop did not end the refresher of this module's own product")
	}
}

// TestCallbacksLeaveASuppliedInstanceAlone pins that Init, Start and Stop do
// nothing for an instance the host supplied: they load no snapshot, start no
// refresher and end none, whatever the instance carries.
//
// The wrapper is the sharper of the two. It carries the roster's own methods, so
// a callback that took any value having them for its product would pass over the
// instance of a type of the host's own and still act here. Each observation is
// made on a roster nobody else has touched, so a change in it can only have come
// from the callback just called.
func TestCallbacksLeaveASuppliedInstanceAlone(t *testing.T) {
	m := Module()
	ctx := context.Background()

	t.Run("an instance of a type of the host's own, or none", func(t *testing.T) {
		callbacks := map[string]func(context.Context, *core.Registry, any) error{
			"Init": m.Init, "Start": m.Start, "Stop": m.Stop,
		}
		for name, callback := range callbacks {
			for _, instance := range []any{hostCatalog{}, nil} {
				if err := callback(ctx, core.New(), instance); err != nil {
					t.Errorf("%s over %#v gave %v, want it left alone", name, instance, err)
				}
			}
		}
	})

	t.Run("a wrapper around this module's own product", func(t *testing.T) {
		inner := quietRoster(t, time.Hour, insert("acme", "acme"))
		t.Cleanup(inner.stop)
		wrapped := wrappedRoster{inner}

		if err := m.Init(ctx, core.New(), wrapped); err != nil {
			t.Fatalf("Init over a wrapper: %v", err)
		}
		if inner.snap.Load() != nil {
			t.Error("Init loaded the snapshot of the roster inside the wrapper")
		}
		if err := m.Start(ctx, core.New(), wrapped); err != nil {
			t.Fatalf("Start over a wrapper: %v", err)
		}
		if inner.stopCh != nil {
			t.Error("Start started the refresher of the roster inside the wrapper")
		}

		// The owner runs the roster it built itself, and Stop has to leave what it
		// started running.
		inner.start()
		running := inner.stopCh
		if err := m.Stop(ctx, core.New(), wrapped); err != nil {
			t.Fatalf("Stop over a wrapper: %v", err)
		}
		select {
		case <-running:
			t.Error("Stop ended the refresher of the roster inside the wrapper")
		default:
		}
	})
}
