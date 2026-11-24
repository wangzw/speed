package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

type recorder struct{ calls []string }

func (r *recorder) note(format string, args ...any) {
	r.calls = append(r.calls, fmt.Sprintf(format, args...))
}

func closer(rec *recorder, name string, stopErr, closeErr error) Module {
	return Module{
		Name: name,
		New: func(context.Context, *Registry) (any, error) {
			return &product{id: name}, nil
		},
		Stop: func(context.Context, *Registry, any) error {
			rec.note("stop %s", name)
			return stopErr
		},
		Close: func(context.Context, *Registry, any) error {
			rec.note("close %s", name)
			return closeErr
		},
	}
}

func constructAll(t *testing.T, reg *Registry, mods ...Module) {
	t.Helper()
	for _, m := range mods {
		reg.Register(m)
		if err := reg.construct(t.Context(), m); err != nil {
			t.Fatalf("constructing %q failed: %v", m.Name, err)
		}
	}
}

// TestRollbackIsReverseDependencyOrderStopsBeforeCloses checks that a rollback stops modules in
// reverse construction order, all before any Close runs.
func TestRollbackIsReverseDependencyOrderStopsBeforeCloses(t *testing.T) {
	rec := &recorder{}
	reg := New()
	constructAll(t, reg, closer(rec, "first", nil, nil), closer(rec, "second", nil, nil), closer(rec, "third", nil, nil))

	if err := reg.shutdown(t.Context(), nil); err != nil {
		t.Fatalf("a clean rollback returned %v", err)
	}
	want := []string{"stop third", "stop second", "stop first", "close third", "close second", "close first"}
	if !slices.Equal(rec.calls, want) {
		t.Fatalf("rollback ran %v, want %v", rec.calls, want)
	}
}

// TestEntryPointsStopFirstAndCloseIgnoresTheGrouping checks that Stop reaches entry points first in
// reverse order while Close runs in plain reverse order.
func TestEntryPointsStopFirstAndCloseIgnoresTheGrouping(t *testing.T) {
	rec := &recorder{}
	serving := func(name string) Module {
		m := closer(rec, name, nil, nil)
		m.Serve = func(context.Context, *Registry, any) error { return nil }
		return m
	}
	reg := New()
	constructAll(t, reg,
		closer(rec, "first", nil, nil),
		serving("entry"),
		closer(rec, "later", nil, nil),
		serving("second-entry"),
		closer(rec, "last", nil, nil),
	)

	if err := reg.shutdown(t.Context(), nil); err != nil {
		t.Fatalf("a clean shutdown returned %v", err)
	}
	want := []string{
		"stop second-entry", "stop entry", "stop last", "stop later", "stop first",
		"close last", "close second-entry", "close later", "close entry", "close first",
	}
	if !slices.Equal(rec.calls, want) {
		t.Fatalf("shutdown ran %v, want %v", rec.calls, want)
	}
}

// TestStopFailureDoesNotAbortStopPhase pins that a failed drain notice cannot hold back the rest
// of the stage or the Close that follows.
func TestStopFailureDoesNotAbortStopPhase(t *testing.T) {
	rec := &recorder{}
	reg := New()
	constructAll(t, reg,
		closer(rec, "first", nil, nil),
		closer(rec, "second", errors.New("drain refused"), nil),
		closer(rec, "third", nil, nil),
	)

	err := reg.shutdown(t.Context(), nil)
	if err == nil || !strings.Contains(err.Error(), "drain refused") {
		t.Fatalf("shutdown returned %v, want the stop failure reported", err)
	}
	want := []string{"stop third", "stop second", "stop first", "close third", "close second", "close first"}
	if !slices.Equal(rec.calls, want) {
		t.Fatalf("rollback ran %v, want %v", rec.calls, want)
	}
}

// TestPrimaryErrorSurvivesCleanupFailure pins that what terminated the startup is the first
// failure, not a secondary one on the way out.
func TestPrimaryErrorSurvivesCleanupFailure(t *testing.T) {
	rec := &recorder{}
	cleanupErr := errors.New("close refused")
	reg := New()
	constructAll(t, reg, closer(rec, "only", nil, cleanupErr))

	err := reg.shutdown(t.Context(), fmt.Errorf("%w: capability lost", ErrMissingProvider))
	if !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("shutdown returned %v, which no longer carries the primary error", err)
	}
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("shutdown returned %v, which dropped the cleanup failure", err)
	}
	if !strings.HasPrefix(err.Error(), ErrMissingProvider.Error()) {
		t.Fatalf("shutdown returned %q, want the primary error first", err)
	}
}

// TestCleanCancelReturnsNil checks that a cancelled context does not turn a clean shutdown into an
// error.
func TestCleanCancelReturnsNil(t *testing.T) {
	rec := &recorder{}
	reg := New()
	constructAll(t, reg, closer(rec, "only", nil, nil))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := reg.shutdown(ctx, nil); err != nil {
		t.Fatalf("a clean shutdown on a cancelled context returned %v, want nil", err)
	}
}

// TestShutdownKeepsContextValues pins the other half of detaching the cancellation: what the host
// attached to the context still reaches Stop and Close, so only the cancellation is dropped.
func TestShutdownKeepsContextValues(t *testing.T) {
	type key struct{}
	var seen []any
	reg := New()
	m := Module{
		Name: "only",
		New:  func(context.Context, *Registry) (any, error) { return &product{id: "only"}, nil },
		Stop: func(ctx context.Context, _ *Registry, _ any) error {
			seen = append(seen, ctx.Value(key{}))
			return ctx.Err()
		},
		Close: func(ctx context.Context, _ *Registry, _ any) error {
			seen = append(seen, ctx.Value(key{}))
			return ctx.Err()
		},
	}
	constructAll(t, reg, m)

	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), key{}, "host"))
	cancel()
	if err := reg.shutdown(ctx, nil); err != nil {
		t.Fatalf("a shutdown whose callbacks honour the context returned %v, want nil", err)
	}
	if !slices.Equal(seen, []any{"host", "host"}) {
		t.Fatalf("Stop and Close saw %v, want the value the host attached in both", seen)
	}
}

// TestRollbackCallsNeverInitializedInstances checks that Stop and Close reach a constructed module
// whose Init never ran, with its instance.
func TestRollbackCallsNeverInitializedInstances(t *testing.T) {
	rec := &recorder{}
	reg := New()
	never := Module{
		Name: "never-initialised",
		New: func(context.Context, *Registry) (any, error) {
			return &product{id: "never-initialised"}, nil
		},
		Init: func(context.Context, *Registry, any) error {
			rec.note("init never-initialised")
			return nil
		},
		Stop: func(_ context.Context, _ *Registry, instance any) error {
			if instance == nil {
				t.Error("Stop received a nil instance for a constructed module")
			}
			rec.note("stop never-initialised")
			return nil
		},
		Close: func(context.Context, *Registry, any) error {
			rec.note("close never-initialised")
			return nil
		},
	}
	constructAll(t, reg, never)

	if err := reg.shutdown(t.Context(), nil); err != nil {
		t.Fatalf("shutdown returned %v", err)
	}
	want := []string{"stop never-initialised", "close never-initialised"}
	if !slices.Equal(rec.calls, want) {
		t.Fatalf("rollback ran %v, want %v with no Init in sight", rec.calls, want)
	}
}

func suppliedCloser(t *testing.T, rec *recorder, reg *Registry, name string, adjust ...func(*Module)) Module {
	t.Helper()
	idOf := func(instance any) string {
		if p, ok := instance.(*product); ok {
			return p.id
		}
		return fmt.Sprintf("%v", instance)
	}
	m := Module{
		Name: name,
		Stop: func(_ context.Context, _ *Registry, instance any) error {
			rec.note("stop %s (%s)", name, idOf(instance))
			return nil
		},
		Close: func(_ context.Context, _ *Registry, instance any) error {
			rec.note("close %s (%s)", name, idOf(instance))
			return nil
		},
	}
	for _, f := range adjust {
		f(&m)
	}
	reg.Register(m)
	reg.Supply(&product{id: "supplied-" + name}, name)
	return m
}

// TestShutdownReleasesASuppliedInstanceTheDriveNeverReached checks that a supplied instance nobody
// constructed is still stopped and closed.
func TestShutdownReleasesASuppliedInstanceTheDriveNeverReached(t *testing.T) {
	rec := &recorder{}
	reg := New()
	suppliedCloser(t, rec, reg, "supplied")
	constructAll(t, reg, closer(rec, "built", nil, nil))

	if err := reg.shutdown(t.Context(), nil); err != nil {
		t.Fatalf("a clean shutdown returned %v", err)
	}
	want := []string{
		"stop supplied (supplied-supplied)", "stop built",
		"close supplied (supplied-supplied)", "close built",
	}
	if !slices.Equal(rec.calls, want) {
		t.Fatalf("shutdown ran %v, want %v", rec.calls, want)
	}
}

// TestShutdownReleasesSuppliedInstancesInDependencyOrder checks that a supplied requirer is
// released before the supplied module it requires.
func TestShutdownReleasesSuppliedInstancesInDependencyOrder(t *testing.T) {
	rec := &recorder{}
	reg := New()
	suppliedCloser(t, rec, reg, "zzz-provider", func(m *Module) {
		m.Provides = provides((*cacheCap)(nil))
	})
	suppliedCloser(t, rec, reg, "aaa-requirer", func(m *Module) {
		m.Requires = needs((*cacheCap)(nil))
	})

	if err := reg.shutdown(t.Context(), nil); err != nil {
		t.Fatalf("a clean shutdown returned %v", err)
	}
	want := []string{
		"stop aaa-requirer (supplied-aaa-requirer)", "stop zzz-provider (supplied-zzz-provider)",
		"close aaa-requirer (supplied-aaa-requirer)", "close zzz-provider (supplied-zzz-provider)",
	}
	if !slices.Equal(rec.calls, want) {
		t.Fatalf("shutdown ran %v, want the requirer released before the module it requires: %v",
			rec.calls, want)
	}
}

// TestShutdownReleasesSuppliedInstancesOnACycleAlike pins that a dependency cycle among instances
// nobody constructed does not keep them from being released.
func TestShutdownReleasesSuppliedInstancesOnACycleAlike(t *testing.T) {
	rec := &recorder{}
	reg := New()
	suppliedCloser(t, rec, reg, "x", func(m *Module) {
		m.Requires = needs((*storeCap)(nil))
		m.Provides = provides((*cacheCap)(nil))
	})
	suppliedCloser(t, rec, reg, "y", func(m *Module) {
		m.Requires = needs((*cacheCap)(nil))
		m.Provides = provides((*storeCap)(nil))
	})

	if err := reg.shutdown(t.Context(), nil); err != nil {
		t.Fatalf("a clean shutdown returned %v", err)
	}
	got := slices.Clone(rec.calls)
	slices.Sort(got)
	want := []string{
		"close x (supplied-x)", "close y (supplied-y)",
		"stop x (supplied-x)", "stop y (supplied-y)",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("shutdown ran %v, want each module on the cycle stopped and closed once: %v", rec.calls, want)
	}
}

// TestShutdownReleasesASuppliedInstanceOnceWhereConstructionPutIt checks that a supplied instance
// released in construction order is stopped once and then closed once.
func TestShutdownReleasesASuppliedInstanceOnceWhereConstructionPutIt(t *testing.T) {
	rec := &recorder{}
	reg := New()
	constructAll(t, reg, closer(rec, "first", nil, nil))
	supplied := suppliedCloser(t, rec, reg, "middle")
	if err := reg.construct(t.Context(), supplied); err != nil {
		t.Fatalf("constructing the module with a supplied instance failed: %v", err)
	}
	constructAll(t, reg, closer(rec, "last", nil, nil))

	if err := reg.shutdown(t.Context(), nil); err != nil {
		t.Fatalf("a clean shutdown returned %v", err)
	}
	want := []string{
		"stop last", "stop middle (supplied-middle)", "stop first",
		"close last", "close middle (supplied-middle)", "close first",
	}
	if !slices.Equal(rec.calls, want) {
		t.Fatalf("shutdown ran %v, want %v", rec.calls, want)
	}
}

// TestShutdownStopsASuppliedEntryPointWithTheEntryPoints checks that a supplied entry point is
// stopped with the entry points and then closed in reverse order.
func TestShutdownStopsASuppliedEntryPointWithTheEntryPoints(t *testing.T) {
	rec := &recorder{}
	reg := New()
	constructAll(t, reg, closer(rec, "first", nil, nil))
	entry := suppliedCloser(t, rec, reg, "entry", func(m *Module) {
		m.Serve = func(context.Context, *Registry, any) error { return nil }
	})
	if err := reg.construct(t.Context(), entry); err != nil {
		t.Fatalf("constructing the module with a supplied instance failed: %v", err)
	}
	constructAll(t, reg, closer(rec, "last", nil, nil))

	if err := reg.shutdown(t.Context(), nil); err != nil {
		t.Fatalf("a clean shutdown returned %v", err)
	}
	want := []string{
		"stop entry (supplied-entry)", "stop last", "stop first",
		"close last", "close entry (supplied-entry)", "close first",
	}
	if !slices.Equal(rec.calls, want) {
		t.Fatalf("shutdown ran %v, want %v", rec.calls, want)
	}
}

// TestConstructLeavesAModuleWithASuppliedInstanceAlone checks that constructing a module whose
// instance the host supplied runs no New and keeps the supplied instance in the lifecycle.
func TestConstructLeavesAModuleWithASuppliedInstanceAlone(t *testing.T) {
	built := false
	reg := New()
	m := Module{
		Name:     "supplied",
		Provides: provides((*cacheCap)(nil)),
		New: func(context.Context, *Registry) (any, error) {
			built = true
			return &product{id: "built"}, nil
		},
	}
	reg.Register(m)
	want := &product{id: "supplied"}
	reg.Supply(want)

	if err := reg.construct(t.Context(), m); err != nil {
		t.Fatalf("constructing failed: %v", err)
	}
	if built {
		t.Error("New ran for a module whose instance the host supplied")
	}
	if got := reg.instanceOf("supplied"); got != any(want) {
		t.Errorf("the module's instance is %#v after construction, want the supplied one %#v", got, want)
	}
	if !slices.Equal(reg.lifecycle, []string{"supplied"}) {
		t.Errorf("the lifecycle is %v, want the module in it", reg.lifecycle)
	}
}

// TestPrepareStatesEnabledForAModuleWithASuppliedInstance checks that Prepare runs only for modules
// nobody supplied, while a supplied module's stance is StateEnabled.
func TestPrepareStatesEnabledForAModuleWithASuppliedInstance(t *testing.T) {
	var ran []string
	prepared := func(name string) Module {
		return Module{
			Name: name,
			Prepare: func(context.Context, *Registry) (Enablement, error) {
				ran = append(ran, name)
				return Enablement{State: StateAuto}, nil
			},
		}
	}
	reg := New()
	supplied, ordinary := prepared("supplied"), prepared("ordinary")
	reg.Register(supplied)
	reg.Register(ordinary)
	reg.Supply(&product{id: "supplied"}, "supplied")

	stances, err := reg.prepare(t.Context(), []Module{ordinary, supplied})
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if !slices.Equal(ran, []string{"ordinary"}) {
		t.Errorf("Prepare ran for %v, want the module nobody supplied for alone", ran)
	}
	got := map[string]EnablementState{}
	for _, s := range stances {
		got[s.module.Name] = s.state.State
	}
	if got["supplied"] != StateEnabled {
		t.Errorf("the stance of the module with a supplied instance is %v, want StateEnabled", got["supplied"])
	}
	if got["ordinary"] != StateAuto {
		t.Errorf("the stance of the other module is %v, want the one its Prepare stated", got["ordinary"])
	}
}
