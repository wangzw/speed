package catalog

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/tenant"
)

type hostCatalog struct{ tenant.Catalog }

type wrappedRoster struct{ *roster }

var _ tenant.Catalog = wrappedRoster{}

func quietRoster(t *testing.T, period time.Duration, rows ...string) *roster {
	t.Helper()
	r := rosterOverPeriod(t, period, rows...)
	r.logger = slog.New(slog.DiscardHandler)
	return r
}

// TestCallbacksActOnTheModulesOwnProduct checks that, handed the roster this
// module builds, Init loads its snapshot, Start starts its refresher and Stop
// ends it.
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

// TestCallbacksLeaveASuppliedInstanceAlone checks that Init, Start and Stop
// load no snapshot and start or end no refresher for an instance the host
// supplied, including a wrapper that promotes the roster's unexported methods.
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
