package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// TestUndeliveredCapabilityNamesWhichWayNothingWasBuilt separates the two ways a module whose New
// did not run leaves the instance nil.
func TestUndeliveredCapabilityNamesWhichWayNothingWasBuilt(t *testing.T) {
	for _, tc := range []struct {
		why    string
		module core.Module
		want   []string
		absent string
	}{
		{
			why: "a New callback that returned no product",
			module: core.Module{
				Name:     "hollow",
				Provides: []core.Provision{{Token: (*Reader)(nil)}},
				New:      func(context.Context, *core.Registry) (any, error) { return nil, nil },
			},
			want:   []string{"hollow", "Reader", "returned no product"},
			absent: "has no New callback",
		},
		{
			why: "a module with no New callback",
			module: core.Module{
				Name:     "resources-only",
				Provides: []core.Provision{{Token: (*Reader)(nil)}},
			},
			want:   []string{"resources-only", "Reader", "has no New callback", "Supply"},
			absent: "returned no product",
		},
	} {
		reg := core.New()
		register(reg, tc.module)

		err := runToFailure(t, reg)
		if !errors.Is(err, core.ErrUndeliveredCapability) {
			t.Fatalf("%s: Run returned %v, want ErrUndeliveredCapability", tc.why, err)
		}
		for _, want := range tc.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q does not mention %q", tc.why, err, want)
			}
		}
		if strings.Contains(err.Error(), tc.absent) {
			t.Errorf("%s: error %q describes the other way of arriving here with %q",
				tc.why, err, tc.absent)
		}
	}
}

// TestAModuleWithoutNewDeliversWhatTheHostSupplied checks that Resolve returns the instance the
// host supplied for a module that declares a capability without a New callback.
func TestAModuleWithoutNewDeliversWhatTheHostSupplied(t *testing.T) {
	reg := core.New()
	register(reg, placeholder("face", (*Reader)(nil)))
	reg.Supply(reader{})

	var got Reader
	err := driveToEnd(t, reg, core.Drive{
		Depth: core.StageServe,
		Action: func(_ context.Context, r *core.Registry) error {
			var err error
			got, err = core.Resolve[Reader](r)
			return err
		},
	})
	if err != nil {
		t.Fatalf("Drive returned %v, want the supplied instance to count as delivered", err)
	}
	if got == nil || got.Value("key") != "value:key" {
		t.Fatalf("Resolve returned %#v, want the supplied reader", got)
	}
}

// TestASuppliedInstanceTakesThePlaceOfNewEvenWhereNewWouldDeliverNothing checks that a supplied
// instance satisfies the delivery check without the module's New running.
func TestASuppliedInstanceTakesThePlaceOfNewEvenWhereNewWouldDeliverNothing(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, core.Module{
		Name:     "hollow",
		Provides: []core.Provision{{Token: (*Reader)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			p.record("new hollow")
			return nil, nil
		},
	})
	reg.Supply(reader{}, "hollow")

	err := driveToEnd(t, reg, core.Drive{Depth: core.StageServe, Action: noAction})
	if err != nil {
		t.Fatalf("Drive returned %v, want the supplied instance to count as delivered", err)
	}
	if p.has("new hollow") {
		t.Fatal("New ran for a module the host supplied an instance to")
	}
}

// TestMissingProviderPointsAtSupplyForADeclarerWithoutNew checks that a missing provider of a
// module with no New names the capability and Supply from any lookup point.
func TestMissingProviderPointsAtSupplyForADeclarerWithoutNew(t *testing.T) {
	cases := []struct {
		name  string
		setup func(reg *core.Registry)
		drive core.Drive
	}{
		{
			name: "the dependency resolution of a module that requires it",
			setup: func(reg *core.Registry) {
				consumer := core.Module{
					Name:     "consumer",
					Requires: []core.Requirement{{Token: (*Reader)(nil)}},
				}
				register(reg, consumer, placeholder("face", (*Reader)(nil)))
			},
			drive: core.Drive{Depth: core.StageNew, Participants: []string{"consumer"}},
		},
		{
			name: "a Prepare that takes it up",
			setup: func(reg *core.Registry) {
				consumer := core.Module{
					Name: "consumer",
					Prepare: func(_ context.Context, r *core.Registry) (core.Enablement, error) {
						_, err := core.Resolve[Reader](r)
						return core.Enablement{}, err
					},
				}
				register(reg, consumer, placeholder("face", (*Reader)(nil)))
			},
			drive: core.Drive{Depth: core.StageNew, Participants: []string{"consumer", "face"}},
		},
		{
			name: "a New that runs ahead of it and takes it up",
			setup: func(reg *core.Registry) {
				early := core.Module{
					Name:     "early",
					Provides: []core.Provision{{Token: (*Cache)(nil)}},
					New: func(_ context.Context, r *core.Registry) (any, error) {
						if _, err := core.Resolve[Reader](r); err != nil {
							return nil, err
						}
						return &box{id: "early"}, nil
					},
				}
				face := placeholder("face", (*Reader)(nil))
				face.Requires = []core.Requirement{{Token: (*Cache)(nil)}}
				register(reg, early, face)
			},
			drive: core.Drive{Depth: core.StageNew, Participants: []string{"early", "face"}},
		},
		{
			name: "an action in a drive that stops at the stances",
			setup: func(reg *core.Registry) {
				register(reg, placeholder("face", (*Reader)(nil)))
			},
			drive: core.Drive{
				Depth:        core.StagePrepare,
				Participants: []string{"face"},
				Action: func(_ context.Context, r *core.Registry) error {
					_, err := core.Resolve[Reader](r)
					return err
				},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg := core.New()
			c.setup(reg)

			err := driveToEnd(t, reg, c.drive)

			if !errors.Is(err, core.ErrMissingProvider) {
				t.Fatalf("Drive returned %v, want ErrMissingProvider", err)
			}
			for _, want := range []string{"face", "Reader", "Supply"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// TestMissingProviderForExclusiveProvidersNamesSupplyAsAFix pins the second fix a capability whose
// every provider claims it exclusively has.
func TestMissingProviderForExclusiveProvidersNamesSupplyAsAFix(t *testing.T) {
	p := &probe{}
	reg := core.New()
	app := p.module("app")
	app.Requires = []core.Requirement{{Token: (*Cache)(nil)}}
	memory := p.module("memory")
	memory.Provides = []core.Provision{{Token: (*Cache)(nil), Exclusive: true}}
	redis := p.module("redis")
	redis.Provides = []core.Provision{{Token: (*Cache)(nil), Exclusive: true}}
	register(reg, app, memory, redis)

	err := driveToEnd(t, reg, core.Drive{Depth: core.StageNew, Participants: []string{"app"}})

	if !errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("Drive returned %v, want ErrMissingProvider", err)
	}
	for _, want := range []string{"Cache", "Supply"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
