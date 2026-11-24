package gate

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/embedded"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/observability"
)

type countingRegistration struct {
	embedded.Registration
	n atomic.Int64
}

func (r *countingRegistration) Unregister() error { r.n.Add(1); return nil }

// TestDrainEndsWhenTheContextDoes checks that drain ends when the context
// does.
func TestDrainEndsWhenTheContextDoes(t *testing.T) {
	g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
	g.inFlight.Add(1)
	defer g.inFlight.Done()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := g.drain(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("drain returned %v, want the context's own error", err)
	}
}

// TestCloseWaitsForCallsInFlightAndWithdrawsTheCounter checks that close
// waits for calls in flight and withdraws the counter.
func TestCloseWaitsForCallsInFlightAndWithdrawsTheCounter(t *testing.T) {
	g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: 20 * time.Millisecond, inFlight: 1},
		basicBackend("static", "static."))
	registration := &countingRegistration{}
	g.metrics = registration
	g.inFlight.Add(1)
	defer g.inFlight.Done()

	err := Module().Close(t.Context(), nil, g)
	if !errors.Is(err, authn.ErrDrainTimeout) {
		t.Errorf("Close returned %v, want it to have waited and given up (ErrDrainTimeout)", err)
	}
	if registration.n.Load() != 1 {
		t.Errorf("the counter was withdrawn %d time(s), want once", registration.n.Load())
	}
}

// TestCallbacksTolerateATypedNilProduct checks that callbacks tolerate a
// typed nil product.
func TestCallbacksTolerateATypedNilProduct(t *testing.T) {
	var none *gateway
	m := Module()
	ctx := t.Context()
	for name, call := range map[string]func() error{
		"Migrate": func() error { return m.Migrate(ctx, nil, none) },
		"Init":    func() error { return m.Init(ctx, nil, none) },
		"Start":   func() error { return m.Start(ctx, nil, none) },
		"Stop":    func() error { return m.Stop(ctx, nil, none) },
		"Close":   func() error { return m.Close(ctx, nil, none) },
	} {
		if err := call(); err != nil {
			t.Errorf("%s over a typed-nil product returned %v", name, err)
		}
	}
}

type unregisterFails struct{ embedded.Registration }

func (unregisterFails) Unregister() error { return errors.New("unregister refused") }

// TestCloseReportsACounterThatWillNotBeWithdrawn checks that close reports a
// counter that will not be withdrawn.
func TestCloseReportsACounterThatWillNotBeWithdrawn(t *testing.T) {
	g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
	g.metrics = unregisterFails{}
	if err := Module().Close(t.Context(), nil, g); err == nil || !strings.Contains(err.Error(), "unregister refused") {
		t.Errorf("Close returned %v, want the withdrawal's failure", err)
	}
}

// TestTheCounterIsWithdrawnWhenTheGatewayCloses checks that the counter is
// withdrawn when the gateway closes.
func TestTheCounterIsWithdrawnWhenTheGatewayCloses(t *testing.T) {
	collected := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(collected))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	err := assembly{
		body:     guarding("public"),
		backends: []authn.Verifier{basicBackend("static", "static.")},
		router:   routerWith("public"),
		supplied: []supply{{module: "stand-in.meter", token: (*observability.MeterProvider)(nil), instance: provider}},
	}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	// The drive has returned, so Stop and Close have run.
	var rm metricdata.ResourceMetrics
	if err := collected.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}
	if counts, _, _ := countsIn(rm); len(counts) != 0 {
		t.Errorf("a collection after Close still reports %v: the gateway's counter was never withdrawn", counts)
	}
}

// TestAGateThatRefusedForCapacityStillServesTheNextCall checks that a gate
// that refused for capacity still serves the next call.
func TestAGateThatRefusedForCapacityStillServesTheNextCall(t *testing.T) {
	g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: time.Second, inFlight: 1},
		passes(basicBackend("static", "static.")))
	sealed(t, g, routerWith("public"))

	g.slots <- struct{}{}
	w, _ := run(g, "public", withBasic(request("GET", "/x"), "a", "b"))
	if w.Code != nethttp.StatusServiceUnavailable {
		t.Fatalf("a full gate answered %d, want 503", w.Code)
	}
	<-g.slots

	done := make(chan int, 1)
	go func() {
		w, _ := run(g, "public", withBasic(request("GET", "/x"), "a", "b"))
		done <- w.Code
	}()
	select {
	case code := <-done:
		if code != nethttp.StatusOK {
			t.Errorf("the call after the slot was freed answered %d, want 200", code)
		}
	case <-time.After(coretest.Scale(5 * time.Second)):
		t.Fatal("the next call never returned: the refusal for capacity left the lock held")
	}
}

// checks that a budget overrun belongs to the backend even when the caller
// had more time.
func TestABudgetOverrunIsTheBackendsEvenWhenTheCallerHadMoreTime(t *testing.T) {
	for name, answer := range map[string]func(*fakeVerifier) authn.Verdict{
		"a pass":    func(f *fakeVerifier) authn.Verdict { return held(f, "alice") },
		"a refusal": func(*fakeVerifier) authn.Verdict { return authn.Denied(errors.New("no")) },
	} {
		t.Run(name, func(t *testing.T) {
			slow := basicBackend("static", "static.")
			slow.verify = func(ctx context.Context, _ authn.Credential) authn.Verdict {
				// It answers only once its budget has run out, and answers
				// as though it had not: what it says after the deadline is
				// not to be believed.
				<-ctx.Done()
				return answer(slow)
			}
			g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: 5 * time.Millisecond, inFlight: 4}, slow)
			sealed(t, g, routerWith("public"))

			ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
			defer cancel()
			out := concludeUnder(t, g, ctx, asAliceBasic)

			if out.status != nethttp.StatusServiceUnavailable || out.reached || out.rec == nil {
				t.Fatalf("an answer that came after the budget, from a caller with an hour to spare, "+
					"answered %d (reached %v), want a 503 that ends the request", out.status, out.reached)
			}
			if got := attrText(*out.rec, "attributed_to"); got != "static" {
				t.Errorf("the conclusion is charged to %q, want the backend that did not answer in time", got)
			}
		})
	}
}

// TestNoCallHoldsASlotOnceDrainHasReturned holds the atomicity enter
// promises: a call is counted in the in-flight wait group under the same lock
// Stop flips the flag under, so a call that got a slot before Stop is waited
// for by drain, and one that did not is refused.
func TestNoCallHoldsASlotOnceDrainHasReturned(t *testing.T) {
	const rounds, callers = 1500, 64
	for round := range rounds {
		g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
		g.settings.inFlight = callers
		g.slots = make(chan struct{}, callers)

		var wg sync.WaitGroup
		start := make(chan struct{})
		for range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for range 50 {
					c, err := g.enter(t.Context())
					if err != nil {
						return
					}
					runtime.Gosched()
					c.release()
				}
			}()
		}
		close(start)
		runtime.Gosched()
		g.stop()
		if err := g.drain(t.Context()); err != nil {
			t.Fatalf("round %d: drain: %v", round, err)
		}
		if held := len(g.slots); held != 0 {
			t.Fatalf("round %d: drain returned while %d call(s) still held a slot", round, held)
		}
		wg.Wait()
	}
}

// TestNoRegistrationIsLostToTheSeal holds the lock the registration surface
// is written under.
func TestNoRegistrationIsLostToTheSeal(t *testing.T) {
	for round := range 300 {
		g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
		var accepted atomic.Int64
		var wg sync.WaitGroup
		start := make(chan struct{})
		for w := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for i := range 50 {
					func() {
						defer func() { _ = recover() }() // a write after the seal panics: refused, not lost
						g.Exempt("public", "GET", fmt.Sprintf("/w%d/r%d", w, i))
						accepted.Add(1)
					}()
				}
			}()
		}
		close(start)
		runtime.Gosched()
		if err := g.seal(routerWith("public")); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		wg.Wait()
		if got := int64(len(g.routes["public"])); got != accepted.Load() {
			t.Fatalf("round %d: %d registration(s) returned and %d are in the frozen table", round, accepted.Load(), got)
		}
	}
}

// TestConcurrentRegistrationsAreAllKept holds that registrations from several goroutines are all kept.
func TestConcurrentRegistrationsAreAllKept(t *testing.T) {
	g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
	const n = 64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := "/r" + string(rune('a'+i%26)) + string(rune('a'+i/26))
			switch i % 3 {
			case 0:
				g.Exempt("public", "GET", path)
			case 1:
				g.Optional("public", "GET", path)
			default:
				g.Impersonable("public", "GET", path)
			}
		}()
	}
	wg.Wait()
	sealed(t, g, routerWith("public"))
	if got := len(g.routes["public"]); got != n {
		t.Errorf("%d routes were kept, want %d", got, n)
	}
}

// TestNoCallJoinsAfterStopHasReturnedAndDrainHasBegun holds that no call joins after Stop has returned.
func TestNoCallJoinsAfterStopHasReturnedAndDrainHasBegun(t *testing.T) {
	for round := 0; round < 20; round++ {
		var entered atomic.Int64
		b := schemeBackend("token", "bearer")
		b.verify = func(context.Context, authn.Credential) authn.Verdict {
			entered.Add(1)
			return held(b, "alice")
		}
		g := testGateway(t, []string{"public"}, b)
		sealed(t, g, routerWith("public"))
		engine := bareEngine(g, "public")

		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for j := 0; j < 200; j++ {
					engine.ServeHTTP(httptest.NewRecorder(), withBearer(request("GET", "/x"), "t"))
				}
			}()
		}
		close(start)
		for entered.Load() == 0 {
			runtime.Gosched()
		}
		g.stop()
		if err := g.drain(t.Context()); err != nil {
			t.Fatalf("drain: %v", err)
		}
		after := entered.Load()
		wg.Wait()
		if got := entered.Load(); got != after {
			t.Fatalf("round %d: %d call(s) joined after Stop returned and the drain finished", round, got-after)
		}
	}
}

// TestConclusionsAreCountedExactlyUnderConcurrency holds that conclusions are counted exactly under concurrency.
func TestConclusionsAreCountedExactlyUnderConcurrency(t *testing.T) {
	g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
	sealed(t, g, routerWith("public"))
	engine := bareEngine(g, "public")
	const workers, each = 64, 20
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				engine.ServeHTTP(httptest.NewRecorder(), request("GET", "/x"))
			}
		}()
	}
	wg.Wait()
	if got := g.tally()["denied"]; got != workers*each {
		t.Errorf("counted %d, want %d", got, workers*each)
	}
}

// TestPassesAreCountedExactlyUnderConcurrency holds that authenticated passes are counted exactly under concurrency.
func TestPassesAreCountedExactlyUnderConcurrency(t *testing.T) {
	g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: time.Second, inFlight: 4096},
		passes(basicBackend("static", "static.")))
	sealed(t, g, routerWith("public"))
	engine := bareEngine(g, "public")
	const workers, each = 64, 20
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				engine.ServeHTTP(httptest.NewRecorder(), withBasic(request("GET", "/x"), "a", "b"))
			}
		}()
	}
	wg.Wait()
	if got := g.tally()["authenticated"]; got != workers*each {
		t.Errorf("counted %d, want %d", got, workers*each)
	}
}

// TestOpeningAndSealingTheWindowRaceWithRegistrations holds that opening and
// sealing the registration window are safe against registrations made from
// other goroutines.
func TestOpeningAndSealingTheWindowRaceWithRegistrations(t *testing.T) {
	g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
	g.mu.Lock()
	g.phase = phaseShut
	g.mu.Unlock()
	var wg sync.WaitGroup
	var attempts atomic.Int64
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				func() {
					defer func() { _ = recover() }()
					attempts.Add(1)
					g.Exempt("public", "GET", "/x")
				}()
			}
		}()
	}
	g.open()
	for attempts.Load() == 0 {
		runtime.Gosched()
	}
	if err := g.seal(routerWith("public")); err != nil {
		t.Fatalf("seal: %v", err)
	}
	wg.Wait()
}
