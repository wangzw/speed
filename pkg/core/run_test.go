package core_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
)

// Reader stands in for the configuration reading face a host supplies.
type Reader interface{ Value(key string) string }

type reader struct{}

func (reader) Value(key string) string { return "value:" + key }

// probe records the callbacks the driver made, in order.
type probe struct {
	mu     sync.Mutex
	events []string
}

func (p *probe) record(format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, fmt.Sprintf(format, args...))
}

func (p *probe) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.events)
}

func (p *probe) has(event string) bool {
	return slices.Contains(p.snapshot(), event)
}

func (p *probe) waitFor(t *testing.T, event string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !p.has(event) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q; recorded %v", event, p.snapshot())
		}
		time.Sleep(time.Millisecond)
	}
}

func (p *probe) indexOf(t *testing.T, event string) int {
	t.Helper()
	i := slices.Index(p.snapshot(), event)
	if i < 0 {
		t.Fatalf("event %q never happened; recorded %v", event, p.snapshot())
	}
	return i
}

// module builds a module that records every stage it takes part in.
func (p *probe) module(name string) core.Module {
	return core.Module{
		Name: name,
		New: func(context.Context, *core.Registry) (any, error) {
			p.record("new %s", name)
			return &box{id: name}, nil
		},
		Migrate: func(context.Context, *core.Registry, any) error {
			p.record("migrate %s", name)
			return nil
		},
		Init: func(context.Context, *core.Registry, any) error {
			p.record("init %s", name)
			return nil
		},
		Start: func(context.Context, *core.Registry, any) error {
			p.record("start %s", name)
			return nil
		},
		Serve: func(context.Context, *core.Registry, any) error {
			p.record("serve %s", name)
			return nil
		},
		Stop: func(context.Context, *core.Registry, any) error {
			p.record("stop %s", name)
			return nil
		},
		Close: func(context.Context, *core.Registry, any) error {
			p.record("close %s", name)
			return nil
		},
	}
}

// count returns how many times the event was recorded.
func (p *probe) count(event string) int {
	n := 0
	for _, recorded := range p.snapshot() {
		if recorded == event {
			n++
		}
	}
	return n
}

// eventsOf returns what was recorded for one module, in order. Every event the
// recording modules write ends in the module's name.
func (p *probe) eventsOf(name string) []string {
	var events []string
	for _, recorded := range p.snapshot() {
		if strings.HasSuffix(recorded, " "+name) {
			events = append(events, recorded)
		}
	}
	return events
}

// readerFace names the module that carries the reading face in a registry
// assembled by hand.
const readerFace = "reader-face"

// probeReader is a configuration reader a host builds itself and hands in
// with Supply. It carries the shutdown shape a module's product is driven
// through, and the module that owns it calls the two from its Stop and Close,
// so a drive that reached the supplied instance leaves a record.
type probeReader struct{ p *probe }

func (r probeReader) Value(key string) string { return "value:" + key }

func (r probeReader) Stop(context.Context) error {
	r.p.record("stop supplied")
	return nil
}

func (r probeReader) Close() error {
	r.p.record("close supplied")
	return nil
}

// supplyReader hands the reader in the way a host does: built before the
// drive begins, to the module that declares the capability. That module has no
// New, so the reader is the only way it gets an instance, and its Stop and
// Close pass the instance on to the shutdown shape the reader carries.
func (p *probe) supplyReader(reg *core.Registry) {
	reg.Register(core.Module{
		Name:     readerFace,
		Provides: []core.Provision{{Token: (*Reader)(nil)}},
		Stop: func(ctx context.Context, _ *core.Registry, instance any) error {
			if r, ok := instance.(probeReader); ok {
				return r.Stop(ctx)
			}
			return nil
		},
		Close: func(_ context.Context, _ *core.Registry, instance any) error {
			if r, ok := instance.(probeReader); ok {
				return r.Close()
			}
			return nil
		},
	})
	reg.Supply(probeReader{p: p}, readerFace)
}

func register(reg *core.Registry, mods ...core.Module) {
	for _, m := range mods {
		reg.Register(m)
	}
}

// runUntilServed drives a registry to the point where every named event has
// happened, then cancels and returns what Run returned.
func runUntilServed(t *testing.T, reg *core.Registry, p *probe, events ...string) error {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- reg.Run(ctx) }()

	for _, event := range events {
		p.waitFor(t, event)
	}
	select {
	case err := <-done:
		t.Fatalf("Run returned %v before the context was cancelled", err)
	default:
	}

	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
		return nil
	}
}

// runToFailure drives a registry that is expected to fail during startup.
func runToFailure(t *testing.T, reg *core.Registry) error {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- reg.Run(ctx) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run succeeded where a startup failure was expected")
		}
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Run blocked where a startup failure was expected")
		return nil
	}
}

// driveUntilServed drives a registry that is expected to stay up, waits for
// every named event, then cancels and returns what Drive returned.
func driveUntilServed(t *testing.T, reg *core.Registry, p *probe, d core.Drive, events ...string) error {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- reg.Drive(ctx, d) }()

	for _, event := range events {
		p.waitFor(t, event)
	}
	select {
	case err := <-done:
		t.Fatalf("Drive returned %v before the context was cancelled", err)
	default:
	}

	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Drive did not return after the context was cancelled")
		return nil
	}
}

// driveToEnd drives a registry that is expected to come back on its own and
// fails the test if it does not.
//
// The judgement is a deadline rather than an awaited event, because what is
// being pinned is that nothing blocks: an implementation that does block has
// no event to offer, and waiting for one would hang the suite instead of
// failing it. The context is never cancelled here, so a drive that comes back
// did so because it ran out of work.
func driveToEnd(t *testing.T, reg *core.Registry, d core.Drive) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- reg.Drive(t.Context(), d) }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the drive blocked where it had nothing left to do")
		return nil
	}
}

// captureStderr collects what core writes to os.Stderr while fn runs.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating a pipe failed: %v", err)
	}
	original := os.Stderr
	os.Stderr = w
	collected := make(chan string, 1)
	go func() {
		text, _ := io.ReadAll(r)
		collected <- string(text)
	}()

	fn()

	os.Stderr = original
	w.Close()
	text := <-collected
	r.Close()
	return text
}

func TestPhaseOrderNewMigrateInitStartServe(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, p.module("only"))

	if err := runUntilServed(t, reg, p, "serve only"); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	want := []string{"new only", "migrate only", "init only", "start only", "serve only", "stop only", "close only"}
	if got := p.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("the driver ran %v, want %v", got, want)
	}
}

func TestMigrateRunsAfterEveryNewAndBeforeAnyInit(t *testing.T) {
	p := &probe{}
	reg := core.New()
	provider := p.module("aaa-provider")
	provider.Provides = []core.Provision{{Token: (*Cache)(nil)}}
	consumer := p.module("bbb-consumer")
	consumer.Requires = []core.Requirement{{Token: (*Cache)(nil)}}
	register(reg, provider, consumer)

	if err := runUntilServed(t, reg, p, "serve bbb-consumer"); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	lastNew := p.indexOf(t, "new bbb-consumer")
	firstMigrate := p.indexOf(t, "migrate aaa-provider")
	lastMigrate := p.indexOf(t, "migrate bbb-consumer")
	firstInit := p.indexOf(t, "init aaa-provider")
	if firstMigrate < lastNew {
		t.Errorf("Migrate started before every New finished: %v", p.snapshot())
	}
	if firstInit < lastMigrate {
		t.Errorf("Init started before every Migrate finished: %v", p.snapshot())
	}
}

func TestMigrateFollowsDependencyOrder(t *testing.T) {
	p := &probe{}
	reg := core.New()
	provider := p.module("zzz-provider")
	provider.Provides = []core.Provision{{Token: (*Cache)(nil)}}
	consumer := p.module("aaa-consumer")
	consumer.Requires = []core.Requirement{{Token: (*Cache)(nil)}}
	register(reg, consumer, provider)

	if err := runUntilServed(t, reg, p, "serve aaa-consumer"); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if p.indexOf(t, "migrate zzz-provider") > p.indexOf(t, "migrate aaa-consumer") {
		t.Fatalf("Migrate ignored the dependency order: %v", p.snapshot())
	}
}

// TestServeRunsAfterEveryStart pins what dependency order cannot express: an
// entry point needs "after everyone", not "after my dependencies".
func TestServeRunsAfterEveryStart(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, p.module("one"), p.module("two"), p.module("three"))

	if err := runUntilServed(t, reg, p, "serve one", "serve three", "serve two"); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	events := p.snapshot()
	lastStart := 0
	firstServe := len(events)
	for i, event := range events {
		if strings.HasPrefix(event, "start ") && i > lastStart {
			lastStart = i
		}
		if strings.HasPrefix(event, "serve ") && i < firstServe {
			firstServe = i
		}
	}
	if firstServe < lastStart {
		t.Fatalf("a Serve ran before the last Start: %v", events)
	}
}

// TestRunBlocksOnlyAfterEveryServeReturned pins that Serve must not block: a
// Serve that never returned would starve every entry point after it.
func TestRunBlocksOnlyAfterEveryServeReturned(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, p.module("one"), p.module("two"), p.module("three"))

	if err := runUntilServed(t, reg, p, "serve one", "serve two", "serve three"); err != nil {
		t.Fatalf("Run returned %v", err)
	}
}

func TestCancelReturnsNil(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, p.module("only"))

	if err := runUntilServed(t, reg, p, "serve only"); err != nil {
		t.Fatalf("cancellation returned %v, want nil: it is the normal way to stop", err)
	}
}

// TestDrainingStopSeesALiveContext pins that a module whose Stop drains
// against its context is not cut short by the cancellation that started the
// shutdown. Cancellation is the normal way to stop, so a Stop shaped like
// http.Server.Shutdown(ctx) has to get a context it can still drain on, and
// Run has to come back nil.
func TestDrainingStopSeesALiveContext(t *testing.T) {
	p := &probe{}
	reg := core.New()
	m := p.module("only")
	m.Stop = func(ctx context.Context, _ *core.Registry, _ any) error {
		p.record("stop only")
		return ctx.Err()
	}
	m.Close = func(ctx context.Context, _ *core.Registry, _ any) error {
		p.record("close only")
		return ctx.Err()
	}
	register(reg, m)

	if err := runUntilServed(t, reg, p, "serve only"); err != nil {
		t.Fatalf("cancellation returned %v, want nil: a drain that honours its context "+
			"must not be handed one that is already done", err)
	}
}

func TestCloseFailureReturned(t *testing.T) {
	p := &probe{}
	reg := core.New()
	failure := errors.New("release refused")
	m := p.module("only")
	m.Close = func(context.Context, *core.Registry, any) error {
		p.record("close only")
		return failure
	}
	register(reg, m)

	err := runUntilServed(t, reg, p, "serve only")
	if !errors.Is(err, failure) {
		t.Fatalf("Run returned %v, want the cleanup failure", err)
	}
}

// TestRollbackCoversAllConstructedInstances pins that rollback reaches the
// instances constructed earlier that never got as far as the failing stage.
func TestRollbackCoversAllConstructedInstances(t *testing.T) {
	p := &probe{}
	reg := core.New()
	m1 := p.module("m1")
	m1.Provides = []core.Provision{{Token: (*Cache)(nil)}}
	m2 := p.module("m2")
	m2.Requires = []core.Requirement{{Token: (*Cache)(nil)}}
	m2.Provides = []core.Provision{{Token: (*Store)(nil)}}
	m3 := p.module("m3")
	m3.Requires = []core.Requirement{{Token: (*Store)(nil)}}
	failure := errors.New("wiring refused")
	m3.Init = func(context.Context, *core.Registry, any) error {
		p.record("init m3")
		return failure
	}
	register(reg, m1, m2, m3)

	err := runToFailure(t, reg)
	if !errors.Is(err, failure) {
		t.Fatalf("Run returned %v, want the Init failure", err)
	}
	events := p.snapshot()
	for _, want := range []string{"stop m1", "close m1", "stop m2", "close m2", "stop m3", "close m3"} {
		if !slices.Contains(events, want) {
			t.Errorf("rollback skipped %q; recorded %v", want, events)
		}
	}
	if slices.Contains(events, "init m1") && !slices.Contains(events, "start m1") {
		// m1 was initialised but never started, and still gets both
		// callbacks: that is the contract Stop and Close carry.
		t.Log("m1 rolled back from an initialised but unstarted state")
	}
}

// TestDependencyFailureConstructsNothing pins that resolution is the
// preliminary to construction rather than a step inside it: an unmet
// requirement stops the drive before anything is constructed, so the only
// instance the rollback has to reach is the one the host supplied.
func TestDependencyFailureConstructsNothing(t *testing.T) {
	p := &probe{}
	reg := core.New()
	p.supplyReader(reg)
	consumer := p.module("consumer")
	consumer.Requires = []core.Requirement{{Token: (*Cache)(nil)}}
	register(reg, p.module("bystander"), consumer)

	err := runToFailure(t, reg)
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("Run returned %v, want ErrMissingProvider", err)
	}
	for _, event := range p.snapshot() {
		if strings.HasPrefix(event, "new ") {
			t.Fatalf("a module was constructed after dependency resolution failed: %v", p.snapshot())
		}
	}
	for _, want := range []string{"stop supplied", "close supplied"} {
		if got := p.count(want); got != 1 {
			t.Errorf("%q was recorded %d times, want the supplied instance released once; recorded %v",
				want, got, p.snapshot())
		}
	}
}

// TestMissingProviderNamesTheRequiringModuleAndItsSite pins that the failure
// carries who asked and where that module was registered, through Run rather
// than through the planner alone. The available set is decided by imports, so
// the module left unsatisfied may be one a dependency pulled in, and a host
// that never wrote it down can act on neither a bare capability name nor a
// module name with no package behind it.
func TestMissingProviderNamesTheRequiringModuleAndItsSite(t *testing.T) {
	p := &probe{}
	reg := core.New()
	consumer := p.module("consumer")
	consumer.Requires = []core.Requirement{{Token: (*Cache)(nil)}}
	register(reg, consumer)

	err := runToFailure(t, reg)
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("Run returned %v, want ErrMissingProvider", err)
	}
	if !strings.Contains(err.Error(), `"consumer"`) {
		t.Errorf("error %q does not name the module whose requirement went unmet", err)
	}
	if !strings.Contains(err.Error(), "registered from ") {
		t.Errorf("error %q does not say where that module was registered, so a host that "+
			"did not write it down has nothing to trace back to what pulled it in", err)
	}
}

// TestPrepareErrorAbortsBeforeConstruction pins that a stance that cannot be
// stated terminates the drive where it stands. No product exists yet, so the
// other participants are left unbuilt rather than built and torn down.
func TestPrepareErrorAbortsBeforeConstruction(t *testing.T) {
	p := &probe{}
	reg := core.New()
	failure := errors.New("cannot read my configuration")
	m := p.module("stubborn")
	m.Prepare = func(context.Context, *core.Registry) (core.Enablement, error) {
		return core.Enablement{}, failure
	}
	register(reg, m, p.module("bystander"))

	err := runToFailure(t, reg)
	if !errors.Is(err, failure) {
		t.Fatalf("Run returned %v, want the Prepare failure", err)
	}
	if p.has("new bystander") {
		t.Fatalf("a module was constructed after a Prepare failure; recorded %v", p.snapshot())
	}
}

func TestUndeliveredCapability(t *testing.T) {
	p := &probe{}
	reg := core.New()
	m := p.module("liar")
	m.Provides = []core.Provision{{Token: (*Reader)(nil)}}
	register(reg, m)

	err := runToFailure(t, reg)
	if !errors.Is(err, core.ErrUndeliveredCapability) {
		t.Fatalf("Run returned %v, want ErrUndeliveredCapability", err)
	}
	for _, want := range []string{"liar", "Reader"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if !p.has("close liar") {
		t.Fatalf("the constructed instance was not rolled back; recorded %v", p.snapshot())
	}
}

// TestASuppliedInstanceIsAvailableInPrepare pins the bootstrap: the host hands
// the configuration reader in before the drive, so every module reads its own
// configuration while stating whether it is enabled.
func TestASuppliedInstanceIsAvailableInPrepare(t *testing.T) {
	p := &probe{}
	reg := core.New()
	p.supplyReader(reg)
	m := p.module("dependent")
	m.Prepare = func(_ context.Context, r *core.Registry) (core.Enablement, error) {
		cfg, err := core.Resolve[Reader](r)
		if err != nil {
			p.record("prepare dependent failed: %v", err)
			return core.Enablement{}, nil
		}
		p.record("prepare dependent read %s", cfg.Value("key"))
		return core.Enablement{}, nil
	}
	register(reg, m)

	if err := runUntilServed(t, reg, p, "serve dependent"); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if !p.has("prepare dependent read value:key") {
		t.Fatalf("the supplied reader was not available in Prepare; recorded %v", p.snapshot())
	}
}

// TestTheDriveStopsAndClosesASuppliedInstance pins that a supplied instance
// leaves through the same shutdown as one the drive constructed: its module's
// Stop runs once, after every Serve, and its Close runs once, after every
// Stop.
func TestTheDriveStopsAndClosesASuppliedInstance(t *testing.T) {
	p := &probe{}
	reg := core.New()
	p.supplyReader(reg)
	register(reg, p.module("aaa"), p.module("zzz"))

	if err := runUntilServed(t, reg, p, "serve aaa", "serve zzz"); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	for _, want := range []string{"stop supplied", "close supplied"} {
		if got := p.count(want); got != 1 {
			t.Fatalf("%q was recorded %d times, want once; recorded %v", want, got, p.snapshot())
		}
	}
	stop := p.indexOf(t, "stop supplied")
	for _, serve := range []string{"serve aaa", "serve zzz"} {
		if p.indexOf(t, serve) > stop {
			t.Errorf("the supplied instance was stopped before %q: %v", serve, p.snapshot())
		}
	}
	closed := p.indexOf(t, "close supplied")
	for _, stopped := range []string{"stop aaa", "stop zzz", "stop supplied"} {
		if p.indexOf(t, stopped) > closed {
			t.Errorf("the supplied instance was closed before %q: %v", stopped, p.snapshot())
		}
	}
}

// TestRegistryWithNothingSuppliedStillRuns pins that supplying is not a
// precondition of assembly: a registry that holds a small group of modules and
// nothing else drives as usual.
func TestRegistryWithNothingSuppliedStillRuns(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, p.module("alone"))

	if err := runUntilServed(t, reg, p, "serve alone"); err != nil {
		t.Fatalf("Run returned %v, want a registry with nothing supplied to run", err)
	}
}

// TestRunDrivesASuppliedModuleThroughEveryStageButPrepareAndNew pins the
// sequence a module takes when the host built its instance: the same stages as
// any other, minus the two that produce the instance. The module is not named
// anywhere, and Run takes in every registered module.
func TestRunDrivesASuppliedModuleThroughEveryStageButPrepareAndNew(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, preparing(p, "only"))
	reg.Supply(&box{id: "host"}, "only")

	if err := runUntilServed(t, reg, p, "serve only"); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	want := []string{"migrate only", "init only", "start only", "serve only", "stop only", "close only"}
	if got := p.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("the driver ran %v, want %v", got, want)
	}
}

// TestRunFailsWhenTheHostSuppliedNothingToAModuleThatHasNoNew pins what Run
// leaves to its caller. It takes in every registered module and loads nothing,
// so a module that declares a capability and builds no instance of it — the
// shape of the configuration module — fails the startup naming the one way to
// give it an instance, until the caller supplies one. Which sentinel comes
// first depends on whether anything took the capability up before the module's
// turn to be constructed.
func TestRunFailsWhenTheHostSuppliedNothingToAModuleThatHasNoNew(t *testing.T) {
	reg := core.New()
	register(reg, placeholder(readerFace, (*Reader)(nil)))

	err := runToFailure(t, reg)
	if !errors.Is(err, core.ErrMissingProvider) && !errors.Is(err, core.ErrUndeliveredCapability) {
		t.Fatalf("Run returned %v, want ErrMissingProvider or ErrUndeliveredCapability", err)
	}
	for _, want := range []string{readerFace, "Reader", "Supply"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestNothingSuppliedYieldsMissingProviderOnReader pins that a missed supply
// surfaces as a named failure rather than as a quietly absent piece of
// functionality.
func TestNothingSuppliedYieldsMissingProviderOnReader(t *testing.T) {
	p := &probe{}
	reg := core.New()
	m := p.module("needs-config")
	m.Prepare = func(_ context.Context, r *core.Registry) (core.Enablement, error) {
		_, err := core.Resolve[Reader](r)
		if !errors.Is(err, core.ErrMissingProvider) {
			t.Errorf("taking up the reader returned %v, want ErrMissingProvider", err)
		}
		p.record("prepare saw: %v", err)
		return core.Enablement{}, nil
	}
	register(reg, m)

	if err := runUntilServed(t, reg, p, "serve needs-config"); err != nil {
		t.Fatalf("Run returned %v", err)
	}
}

// TestModuleWithoutNewGetsNilInstance pins the contract for a module that only
// declares resources and was handed no instance: every later stage receives
// nil.
func TestModuleWithoutNewGetsNilInstance(t *testing.T) {
	p := &probe{}
	reg := core.New()
	m := core.Module{
		Name:      "resources-only",
		Resources: []any{"a declaration core never interprets"},
		Init: func(_ context.Context, _ *core.Registry, instance any) error {
			if instance != nil {
				t.Errorf("Init received %v, want nil for a module without New", instance)
			}
			p.record("init resources-only")
			return nil
		},
		Start: func(_ context.Context, _ *core.Registry, instance any) error {
			if instance != nil {
				t.Errorf("Start received %v, want nil for a module without New", instance)
			}
			p.record("start resources-only")
			return nil
		},
		Serve: func(context.Context, *core.Registry, any) error {
			p.record("serve resources-only")
			return nil
		},
	}
	register(reg, m)

	if err := runUntilServed(t, reg, p, "serve resources-only"); err != nil {
		t.Fatalf("Run returned %v", err)
	}
}

// TestPrepareSeesNoModuleProductButSeesSuppliedOnes pins the two halves of
// what a stance can reach: no instance a module constructs exists yet, while a
// supplied instance was there before the drive began.
func TestPrepareSeesNoModuleProductButSeesSuppliedOnes(t *testing.T) {
	p := &probe{}
	reg := core.New()
	p.supplyReader(reg)
	provider := p.module("provider")
	provider.Provides = []core.Provision{{Token: (*Cache)(nil)}}
	consumer := p.module("consumer")
	consumer.Prepare = func(_ context.Context, r *core.Registry) (core.Enablement, error) {
		if _, err := core.Resolve[Cache](r); !errors.Is(err, core.ErrMissingProvider) {
			t.Errorf("taking up a module's capability in Prepare returned %v, want ErrMissingProvider", err)
		}
		cfg, err := core.Resolve[Reader](r)
		if err != nil {
			t.Errorf("taking up the supplied reader in Prepare returned %v", err)
			return core.Enablement{}, nil
		}
		p.record("prepare consumer read %s", cfg.Value("key"))
		return core.Enablement{}, nil
	}
	register(reg, provider, consumer)

	if err := runUntilServed(t, reg, p, "serve consumer"); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if !p.has("prepare consumer read value:key") {
		t.Fatalf("the consumer's Prepare did not run; recorded %v", p.snapshot())
	}
}

func TestSecondRunPanics(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, p.module("only"))

	if err := runUntilServed(t, reg, p, "serve only"); err != nil {
		t.Fatalf("the first Run returned %v", err)
	}
	msg := recoverMessage(t, func() {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_ = reg.Run(ctx)
	})
	if !strings.Contains(msg, "more than once") {
		t.Fatalf("panic %q does not explain that Run is single-use", msg)
	}
}

// TestRunEmitsDiagnosticsAfterResolution pins the one observable surface this
// framework has: a module resolution stood down says so on stderr.
//
// Standing down is the only way an included module misses the enabled set. The
// pair below is that case: an exclusive default stating StateAuto, and an
// exclusive implementation beside it stating StateEnabled, which propagation
// prefers.
func TestRunEmitsDiagnosticsAfterResolution(t *testing.T) {
	p := &probe{}
	reg := core.New()
	def := p.module("switched-off")
	def.Provides = []core.Provision{{Token: (*Cache)(nil), Exclusive: true}}
	ground := p.module("running")
	ground.Provides = []core.Provision{{Token: (*Cache)(nil), Exclusive: true}}
	ground.Prepare = func(context.Context, *core.Registry) (core.Enablement, error) {
		return core.Enablement{State: core.StateEnabled}, nil
	}
	register(reg, ground, def)

	var runErr error
	text := captureStderr(t, func() {
		runErr = runUntilServed(t, reg, p, "serve running")
	})
	if runErr != nil {
		t.Fatalf("Run returned %v", runErr)
	}
	for _, want := range []string{"core: ", "switched-off", "explicitly enabled"} {
		if !strings.Contains(text, want) {
			t.Errorf("stderr %q does not contain %q", text, want)
		}
	}
	if strings.Contains(text, `module "running"`) {
		t.Errorf("stderr %q lists a module that is enabled", text)
	}
	if p.has("new switched-off") {
		t.Errorf("a module resolution stood down was constructed: %v", p.snapshot())
	}
	if got, ok := reg.Enablement("switched-off"); !ok || got.State != core.StateDisabled {
		t.Errorf("Enablement reported %+v/%v for a module resolution stood down", got, ok)
	}
}

// TestProvidesWithoutNewFailsUndelivered pins the consequence of the two rules
// meeting: a module with no New has nil for its instance in every later stage,
// so one that declares Provides delivers nothing and must fail unless the host
// supplied an instance for it. A module carrying resources alone cannot
// declare a capability on the side, because what is delivered is the instance
// and it builds none. The text names the one way out the module has.
func TestProvidesWithoutNewFailsUndelivered(t *testing.T) {
	reg := core.New()
	register(reg, core.Module{
		Name:      "resources-only",
		Resources: []any{"a declaration core never interprets"},
		Provides:  []core.Provision{{Token: (*Reader)(nil)}},
	})

	err := runToFailure(t, reg)
	if !errors.Is(err, core.ErrUndeliveredCapability) {
		t.Fatalf("Run returned %v, want ErrUndeliveredCapability", err)
	}
	for _, want := range []string{"resources-only", "Reader", "New", "Supply"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
