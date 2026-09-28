package core_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/internal/regprobe"
)

// The two package paths a conflict panic has to name. The available set is
// decided by imports, so the other party may sit in a dependency rather than
// in the host's own code.
const (
	testSite    = "github.com/vislake/speed/pkg/core_test"
	regprobSite = "github.com/vislake/speed/pkg/core/internal/regprobe"
)

func TestSuppliedProductResolves(t *testing.T) {
	reg := core.New()
	reg.Supply((*Reader)(nil), reader{})

	got, err := core.Resolve[Reader](reg)
	if err != nil {
		t.Fatalf("taking up a supplied product returned %v", err)
	}
	if got.Value("key") != "value:key" {
		t.Fatalf("Resolve returned %#v, want the supplied reader", got)
	}
}

// TestResolveAllReturnsTheSuppliedProduct pins that the collecting lookup has
// the same matching domain as the single-valued one. The two kinds of source
// cannot hold one capability, so the supplied product is the whole result.
func TestResolveAllReturnsTheSuppliedProduct(t *testing.T) {
	reg := core.New()
	reg.Supply((*Reader)(nil), reader{})

	got := core.ResolveAll[Reader](reg)
	if len(got) != 1 {
		t.Fatalf("ResolveAll returned %d products, want the one that was supplied", len(got))
	}
	if got[0].Value("key") != "value:key" {
		t.Fatalf("ResolveAll returned %#v, want the supplied reader", got[0])
	}
}

// TestSuppliedProductIsNotAModule pins that supplying does not register
// anything. A product borrowed into the module tables would show up here, and
// from there in the enablement verdicts and in the rollback.
func TestSuppliedProductIsNotAModule(t *testing.T) {
	reg := core.New()
	reg.Supply((*Reader)(nil), reader{})

	if got := reg.Modules(); len(got) != 0 {
		t.Fatalf("Modules lists %v after a supply, want nothing: a supplied product has no "+
			"descriptor", got)
	}
	reg.Register(core.Module{Name: "only"})
	got := reg.Modules()
	if len(got) != 1 || got[0].Name != "only" {
		t.Fatalf("Modules lists %v, want the one registered module", got)
	}
	if _, ok := reg.Lookup("only"); !ok {
		t.Fatal("Lookup missed the registered module")
	}
}

// TestSuppliedProductNeedsNoRequiresDeclaration pins that taking a supplied
// product up is free of declaration, in every stage. It exists before the
// first construction, so there is nothing to order against and nothing to
// declare.
func TestSuppliedProductNeedsNoRequiresDeclaration(t *testing.T) {
	p := &probe{}
	reg := core.New()
	reg.Supply((*Reader)(nil), reader{})
	m := p.module("undeclaring")
	m.Prepare = func(_ context.Context, r *core.Registry) (core.Enablement, error) {
		cfg, err := core.Resolve[Reader](r)
		if err != nil {
			t.Errorf("Prepare could not take up the supplied product: %v", err)
			return core.Enablement{}, nil
		}
		p.record("prepare read %s", cfg.Value("a"))
		return core.Enablement{}, nil
	}
	m.New = func(_ context.Context, r *core.Registry) (any, error) {
		cfg, err := core.Resolve[Reader](r)
		if err != nil {
			return nil, err
		}
		p.record("new read %s", cfg.Value("b"))
		return &box{id: "undeclaring"}, nil
	}
	register(reg, m)

	if err := runUntilServed(t, reg, p, "serve undeclaring"); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	for _, want := range []string{"prepare read value:a", "new read value:b"} {
		if !p.has(want) {
			t.Errorf("the supplied product was not reachable: %q is missing from %v",
				want, p.snapshot())
		}
	}
}

// TestRequiringASuppliedCapabilityIsSatisfied pins that declaring the
// requirement anyway is legal. The design says the declaration is unnecessary,
// not forbidden, and failing a module over one extra line would keep it from
// starting for no gain.
func TestRequiringASuppliedCapabilityIsSatisfied(t *testing.T) {
	p := &probe{}
	reg := core.New()
	reg.Supply((*Reader)(nil), reader{})
	m := p.module("declaring")
	m.Requires = []core.Requirement{{Token: (*Reader)(nil)}}
	register(reg, m)

	if err := runUntilServed(t, reg, p, "serve declaring"); err != nil {
		t.Fatalf("Run returned %v, want a declared requirement on a supplied capability to "+
			"count as met", err)
	}
}

func TestSupplyingOneCapabilityTwicePanics(t *testing.T) {
	reg := core.New()
	regprobe.Supply(reg, (*Reader)(nil), reader{})

	msg := recoverMessage(t, func() {
		reg.Supply((*Reader)(nil), reader{})
	})
	for _, want := range []string{"Reader", testSite, regprobSite} {
		if !strings.Contains(msg, want) {
			t.Errorf("panic %q does not name %s", msg, want)
		}
	}
}

// TestSupplyThenRegisterPanicsNamingBoth and its mirror pin that the conflict
// is caught from whichever side arrives second. Checking one side only lets
// the supply-then-register order through, and that is the order a registry
// assembled by hand runs in.
func TestSupplyThenRegisterPanicsNamingBoth(t *testing.T) {
	reg := core.New()
	regprobe.Supply(reg, (*Reader)(nil), reader{})

	msg := recoverMessage(t, func() {
		reg.Register(core.Module{
			Name:     "second-reader",
			Provides: []core.Provision{{Token: (*Reader)(nil)}},
		})
	})
	for _, want := range []string{"second-reader", "Reader", testSite, regprobSite} {
		if !strings.Contains(msg, want) {
			t.Errorf("panic %q does not name %s", msg, want)
		}
	}
}

func TestRegisterThenSupplyPanicsNamingBoth(t *testing.T) {
	reg := core.New()
	regprobe.Register(reg, core.Module{
		Name:     "second-reader",
		Provides: []core.Provision{{Token: (*Reader)(nil)}},
	})

	msg := recoverMessage(t, func() {
		reg.Supply((*Reader)(nil), reader{})
	})
	for _, want := range []string{"second-reader", "Reader", testSite, regprobSite} {
		if !strings.Contains(msg, want) {
			t.Errorf("panic %q does not name %s", msg, want)
		}
	}
}

func TestSupplyRejectsAConcreteToken(t *testing.T) {
	reg := core.New()

	msg := recoverMessage(t, func() {
		reg.Supply((*box)(nil), &box{id: "x"})
	})
	if !strings.Contains(msg, "interface") {
		t.Fatalf("panic %q does not say a capability token points at an interface", msg)
	}
}

func TestSupplyRejectsANonNilToken(t *testing.T) {
	reg := core.New()
	var r Reader = reader{}

	msg := recoverMessage(t, func() {
		reg.Supply(&r, reader{})
	})
	if !strings.Contains(msg, "nil") {
		t.Fatalf("panic %q does not say a token is a typed nil pointer", msg)
	}
}

// TestSupplyRejectsAProductThatDoesNotImplementTheCapability pins the one
// check the token cannot make: a supplied product answers every lookup for
// its capability, so it has to satisfy it.
func TestSupplyRejectsAProductThatDoesNotImplementTheCapability(t *testing.T) {
	reg := core.New()

	msg := recoverMessage(t, func() {
		reg.Supply((*Reader)(nil), &box{id: "not a reader"})
	})
	for _, want := range []string{"Reader", "implement"} {
		if !strings.Contains(msg, want) {
			t.Errorf("panic %q does not say %q", msg, want)
		}
	}
}

// TestSupplyAfterTheDriveStartedPanics pins the boundary the mechanism rests
// on: the host hands products in before the drive. A module supplying one
// from inside a stage would reach around everything that decides what the
// registry holds.
func TestSupplyAfterTheDriveStartedPanics(t *testing.T) {
	reg := core.New()
	m := core.Module{
		Name: "sneaky",
		Prepare: func(_ context.Context, r *core.Registry) (core.Enablement, error) {
			r.Supply((*Reader)(nil), reader{})
			return core.Enablement{}, nil
		},
	}
	register(reg, m)

	msg := recoverMessage(t, func() {
		_ = reg.Drive(t.Context(), core.Drive{
			Depth:        core.StagePrepare,
			Participants: []string{"sneaky"},
		})
	})
	if !strings.Contains(msg, "after the drive") {
		t.Fatalf("panic %q does not say the supply came too late", msg)
	}
}

// TestRegisterAfterTheDriveStartedPanics pins the other way into the registry
// shut at the same moment. Descriptors are written before the drive and read
// only after it: a module registered from inside a stage, or by the action, or
// once the drive has returned, would change what Modules and Resources answer
// in the middle of a run that promised them fixed, and would report as a
// module this drive left out although it was never asked about.
func TestRegisterAfterTheDriveStartedPanics(t *testing.T) {
	late := core.Module{Name: "late", Resources: []any{"a resource that arrives too late"}}
	for name, drive := range map[string]func(reg *core.Registry) error{
		"from Prepare": func(reg *core.Registry) error {
			register(reg, core.Module{
				Name: "registrar",
				Prepare: func(_ context.Context, r *core.Registry) (core.Enablement, error) {
					r.Register(late)
					return core.Enablement{}, nil
				},
			})
			return reg.Drive(t.Context(), core.Drive{Depth: core.StagePrepare, Participants: []string{"registrar"}})
		},
		"from New": func(reg *core.Registry) error {
			register(reg, core.Module{
				Name: "registrar",
				New: func(_ context.Context, r *core.Registry) (any, error) {
					r.Register(late)
					return nil, nil
				},
			})
			return reg.Drive(t.Context(), core.Drive{Depth: core.StageNew, Participants: []string{"registrar"}})
		},
		"from the action": func(reg *core.Registry) error {
			return reg.Drive(t.Context(), core.Drive{
				Depth:        core.StagePrepare,
				Participants: []string{},
				Action: func(_ context.Context, r *core.Registry) error {
					r.Register(late)
					return nil
				},
			})
		},
		"after the drive returned": func(reg *core.Registry) error {
			if err := reg.Drive(t.Context(), core.Drive{Participants: []string{}}); err != nil {
				return err
			}
			reg.Register(late)
			return nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			reg := core.New()
			msg := recoverMessage(t, func() { _ = drive(reg) })
			if !strings.Contains(msg, "after the drive") || !strings.Contains(msg, `"late"`) {
				t.Fatalf("panic %q does not name the module and say it came too late", msg)
			}
			if _, registered := reg.Lookup("late"); registered {
				t.Fatal("the late module was recorded although its registration was refused")
			}
		})
	}
}

// TestSuppliedProductSurvivesTheDriveUntouched pins the release boundary from
// the product's own side: it is handed in whole and comes back whole, because
// the party that built it is the party that releases it.
func TestSuppliedProductSurvivesTheDriveUntouched(t *testing.T) {
	p := &probe{}
	reg := core.New()
	p.supplyReader(reg)
	register(reg, p.module("only"))

	if err := runUntilServed(t, reg, p, "serve only"); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	events := p.snapshot()
	for _, unwanted := range []string{"stop supplied", "close supplied"} {
		if slices.Contains(events, unwanted) {
			t.Fatalf("the drive released a product it did not build: %v", events)
		}
	}
	got, err := core.Resolve[Reader](reg)
	if err != nil {
		t.Fatalf("the supplied product is no longer reachable after the drive: %v", err)
	}
	if got.Value("key") != "value:key" {
		t.Fatalf("Resolve returned %#v after the drive, want the supplied reader", got)
	}
}
