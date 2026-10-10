package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// TestUndeliveredCapabilityNamesWhichWayNothingWasBuilt separates the two ways a
// module whose New did not run leaves the instance nil. One module has no New
// callback at all; the other has one and it returned no product. Both leave
// assertDelivered with a nil instance, and the fixing actions differ — one
// module has to gain an instance from the host, the other has to correct what
// its callback returns — so a message that reports a missing callback for both
// sends half its readers after a callback that is not absent. The module that
// has no New is the one only the host can give an instance, and the text says
// how.
func TestUndeliveredCapabilityNamesWhichWayNothingWasBuilt(t *testing.T) {
	for _, tc := range []struct {
		why    string
		module core.Module
		want   []string
		// absent is a fragment the message must not carry, because it names
		// the other way of arriving here.
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

// TestAModuleWithoutNewDeliversWhatTheHostSupplied pins the way out the text
// above names: a module that declares a capability and builds nothing is
// delivered by the instance the host handed it, and the drive that includes it
// goes through.
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

// TestASuppliedInstanceTakesThePlaceOfNewEvenWhereNewWouldDeliverNothing pins
// that the supplied instance replaces the module's New instead of competing
// with it: New does not run, so what it would have returned is of no account.
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

// TestMissingProviderPointsAtSupplyForADeclarerWithoutNew pins that the fix a
// capability's sole declarer offers decides the text, whatever the moment the
// lookup came at: a module without New never builds an instance, so asking it
// to be included, constructed earlier or declared in Requires would change
// nothing, and the text sends the reader to the host's Supply. Each case
// reaches the failure from another side — the planning, a stance, a
// construction ordered ahead of the declarer, and an action in a drive that
// builds nothing.
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

// TestMissingProviderForExclusiveProvidersNamesSupplyAsAFix pins the second
// fix a capability whose every provider claims it exclusively has. Neither
// provider is brought in on the requirer's behalf, so none is in the
// participating set until the host names one on the command tree or supplies
// an instance for one of them.
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
