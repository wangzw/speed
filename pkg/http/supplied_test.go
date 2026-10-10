package http

import (
	"context"
	"fmt"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
)

// The tests in this file are about an instance a host supplies for this module
// itself, to stand where the router its New builds would stand. Prepare and New
// do not run for it, and every callback from Migrate to Close is handed it. The
// callbacks recognise only the router this module builds, so what these tests
// observe is what an instance of any other type is left to.

// foreignRouter is the smallest Router a host can supply: it delivers the
// capability and is not the router this module builds.
type foreignRouter struct{ label string }

func (f *foreignRouter) Endpoint(name string) (Endpoint, error) {
	return nil, fmt.Errorf("%w: %q (%s)", ErrUnknownEndpoint, name, f.label)
}

// Endpoints reports no names, which is what a router that hands out no endpoint
// declares.
func (f *foreignRouter) Endpoints() []string { return nil }

var _ Router = (*foreignRouter)(nil)

// wrappedRouter is a Router a host wrote around the router this module builds:
// it delivers the capability and is not the type the callbacks act on. The
// product is embedded, so its unexported methods are promoted along with
// Endpoint, and a callback that acted on whatever carries them would act on the
// router inside.
type wrappedRouter struct{ *router }

var _ Router = wrappedRouter{}

// TestASuppliedInstanceTakesThePlaceOfPrepareAndNew pins what a host gets by
// supplying an instance for this module, observed through a whole assembly.
//
// The configuration declares no endpoint, the one premise Prepare turns a
// startup down over. The first assembly supplies nothing and fails on it,
// naming the key to write, which shows the premise is enforced there. The second
// supplies an instance and starts: Prepare does not run for it. The instance is
// what the other modules take up as the Router, which is how New not having run
// shows: a router built from this configuration would be the answer otherwise.
func TestASuppliedInstanceTakesThePlaceOfPrepareAndNew(t *testing.T) {
	control := startAssembly(t, moduleConfig{}, Module())
	controlErr := control.stop(t)
	if controlErr == nil {
		t.Fatal("an assembly whose configuration declares no endpoint started up, so the run " +
			"below says nothing about Prepare being skipped for a supplied instance")
	}
	mustContain(t, controlErr.Error(), "http.endpoints", "the configuration key to write")

	supplied := &foreignRouter{label: "the router the host wrote"}
	taken := make(chan Router, 1)
	served := make(chan struct{}, 1)
	behind := &stageProbe{
		name:     "probe.requires-the-router",
		requires: requiresRouter(),
		onInit: func(reg *core.Registry) error {
			delivered, err := core.Resolve[Router](reg)
			if err != nil {
				return err
			}
			taken <- delivered
			return nil
		},
		onServe: func(*core.Registry) error {
			served <- struct{}{}
			return nil
		},
	}

	entry := Module()
	reg := core.New()
	supplyConfig(reg, moduleConfig{})
	reg.Register(entry)
	reg.Supply(supplied, entry.Name)
	reg.Register(behind.module())
	a := runAssembly(t, reg)
	a.waitForOrEnd(t, served, "the Serve callback of the module that requires the Router")

	select {
	case got := <-taken:
		if got != Router(supplied) {
			t.Errorf("the module behind the entry point took up %T as the Router, not the instance "+
				"the host supplied: this module's New ran, or the registry answered with another one", got)
		}
	default:
		t.Fatal("the module behind the entry point took up no Router in Init")
	}
	if err := a.stop(t); err != nil {
		t.Errorf("a run over a supplied instance that served and was then cancelled came back with %v", err)
	}
}

// TestCallbacksLeaveAWrappedProductAlone pins, for each callback that could
// act on it, that the router inside a wrapper is not touched when the wrapper is
// what the host supplied: the registration gate neither opens nor seals, and
// Serve binds nothing.
//
// The wrapper carries the unexported methods of the router inside it, so a
// callback that took any value with those methods for its product would pass
// the tolerance test over a foreign instance and still act here. Each
// observation is made on a router nobody else has touched, so a change in it can
// only have come from the callback just called.
func TestCallbacksLeaveAWrappedProductAlone(t *testing.T) {
	m := Module()
	ctx := context.Background()
	inner := testProduct(t, oneEndpoint("public"))
	wrapped := wrappedRouter{inner}
	// A callback that did bind the endpoint is released, so a failing run leaves
	// no listener behind.
	t.Cleanup(func() {
		_ = m.Stop(ctx, nil, inner)
		_ = m.Close(ctx, nil, inner)
	})

	if err := m.Migrate(ctx, nil, wrapped); err != nil {
		t.Fatalf("Migrate over a wrapped product: %v", err)
	}
	if inner.phase != phaseDeclared {
		t.Error("Migrate opened the registration gate of the router inside the wrapper")
	}
	if err := m.Start(ctx, nil, wrapped); err != nil {
		t.Fatalf("Start over a wrapped product: %v", err)
	}
	if inner.phase != phaseDeclared {
		t.Error("Start moved the registration gate of the router inside the wrapper")
	}
	if err := m.Serve(ctx, nil, wrapped); err != nil {
		t.Fatalf("Serve over a wrapped product: %v", err)
	}
	if addr := inner.endpoints["public"].socket.addr(); addr != nil {
		t.Errorf("Serve bound %v for the router inside the wrapper, which is not this module's "+
			"to serve", addr)
	}
}

// TestASuppliedInstanceThatIsNotTheProductIsLeftToItsOwner pins the other half:
// Stop and Close drain nothing and release nothing of an instance that is not the
// router this module builds, and the requests it took in are outside the drain
// the module waits for.
//
// The host is the owner here. It builds a router with this module's own code,
// runs that router through the callbacks itself and supplies a wrapper around it.
// A request is held in flight on the owner's endpoint while the assembly ends. A
// module whose Stop reached through the wrapper would close the endpoint and wait
// for that request, and with the short drain given here it would end the run in
// a drain timeout; one that left it alone ends the run clean, with the endpoint
// still accepting and the held request still answerable once it is let go.
func TestASuppliedInstanceThatIsNotTheProductIsLeftToItsOwner(t *testing.T) {
	gate := newGateHandler(t)
	ctx := context.Background()

	owned := testProduct(t, endpointsAt(100*time.Millisecond, "public"))
	owner := Module()
	if err := owner.Migrate(ctx, nil, owned); err != nil {
		t.Fatalf("the owner opening the gate of its router: %v", err)
	}
	endpointOf(t, owned, "public").Route("GET", "/held", gate.gin)
	if err := owner.Start(ctx, nil, owned); err != nil {
		t.Fatalf("the owner sealing the gate of its router: %v", err)
	}
	if err := owner.Serve(ctx, nil, owned); err != nil {
		t.Fatalf("the owner serving its router: %v", err)
	}
	t.Cleanup(func() {
		_ = owner.Stop(ctx, nil, owned)
		_ = owner.Close(ctx, nil, owned)
	})
	addr, err := boundAddr(owned, "public")
	if err != nil {
		t.Fatal(err)
	}

	served := make(chan struct{}, 1)
	behind := &stageProbe{
		name:     "probe.requires-the-router",
		requires: requiresRouter(),
		onServe: func(*core.Registry) error {
			served <- struct{}{}
			return nil
		},
	}
	entry := Module()
	reg := core.New()
	supplyConfig(reg, moduleConfig{})
	reg.Register(entry)
	reg.Supply(wrappedRouter{owned}, entry.Name)
	reg.Register(behind.module())
	a := runAssembly(t, reg)
	a.waitForOrEnd(t, served, "the Serve callback of the module that requires the Router")

	answered := make(chan int, 1)
	go func() {
		status, _ := getStatus(ctx, addr, "/held")
		answered <- status
	}()
	waitFor(t, gate.entered, "the request to reach the handler held on the owner's endpoint")

	if runErr := a.stop(t); runErr != nil {
		t.Fatalf("a run over a supplied wrapper, ended while a request was in flight on the "+
			"endpoint the wrapper's owner serves, came back with %v: Stop or Close drained "+
			"or closed what the module does not own", runErr)
	}
	if !owned.endpoints["public"].Accepting() {
		t.Error("the owner's endpoint stopped accepting when the run ended, so Stop or Close " +
			"released a router this module did not build")
	}

	gate.letGo()
	select {
	case status := <-answered:
		if status != nethttp.StatusNoContent {
			t.Errorf("the request held on the owner's endpoint was answered %d, not the %d "+
				"its handler writes", status, nethttp.StatusNoContent)
		}
	case <-time.After(waitDeadline):
		t.Fatal("the request held on the owner's endpoint was never answered")
	}
	status, err := getStatus(ctx, addr, "/held")
	if err != nil {
		t.Fatalf("the owner's endpoint refused a request after the run ended: %v", err)
	}
	if status != nethttp.StatusNoContent {
		t.Errorf("the owner's endpoint answered %d after the run ended, not the %d its handler "+
			"writes", status, nethttp.StatusNoContent)
	}
}
