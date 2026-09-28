package gate

import (
	"context"
	"errors"
	"log/slog"
	nethttp "net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestAnAnswerThatArrivesAfterTheBudgetIsNotHonoured holds what the call budget
// is actually for.
//
// It does not cut the wait short. The call is synchronous, and abandoning it
// would either go on holding the in-flight slot, which changes nothing, or
// release it and let goroutines parked inside a broken backend grow without
// bound. What the budget does is invalidate the answer: a backend that takes
// longer than the budget and then says the credential holds does not get its
// caller admitted, and the request ends as the outage it was.
//
// Without this, a backend slow enough to be considered down could still
// authenticate a caller, arbitrarily late, on a request the caller gave up on.
func TestAnAnswerThatArrivesAfterTheBudgetIsNotHonoured(t *testing.T) {
	slow := enumerating("local", "alice")
	slow.verify = func(_ context.Context, subject string, _ authn.Credential) authn.Verdict {
		// Deliberately ignores the deadline it was handed, which is
		// what a backend that does not honour its context looks like.
		time.Sleep(40 * time.Millisecond)
		return authn.Authenticated(subject)
	}

	g := gatewayWith(t, settings{
		endpoints: []string{"public"},
		budget:    5 * time.Millisecond,
		inFlight:  4,
	}, slow)
	sealed(t, g, routerWith("public"))

	w, seen := run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))

	if w.Code != nethttp.StatusServiceUnavailable {
		t.Errorf("a pass that arrived after the budget answered %d, want %d",
			w.Code, nethttp.StatusServiceUnavailable)
	}
	if seen.reached {
		t.Error("the layer behind the gateway was reached by a request whose backend answered " +
			"only after the budget had run out")
	}
	if got := g.tally()["unavailable"]; got != 1 {
		t.Errorf("the conclusion was counted %d time(s) in the unavailable bucket, want 1", got)
	}
}

// TestAnOwnershipAnswerThatArrivesAfterTheBudgetIsNotHonoured holds the same
// rule for the other call a request makes into a backend: an open claim that
// answers late must not be able to pick up a subject either.
func TestAnOwnershipAnswerThatArrivesAfterTheBudgetIsNotHonoured(t *testing.T) {
	slow := openly("directory", func(context.Context, string) (bool, error) {
		time.Sleep(40 * time.Millisecond)
		return true, nil
	})
	slow.verify = func(_ context.Context, subject string, _ authn.Credential) authn.Verdict {
		return authn.Authenticated(subject)
	}

	g := gatewayWith(t, settings{
		endpoints: []string{"public"},
		budget:    5 * time.Millisecond,
		inFlight:  4,
	}, slow)
	sealed(t, g, routerWith("public"))

	w, _ := run(g, "public", withBasic(request("GET", "/orders"), "dave", "secret"))

	if w.Code != nethttp.StatusServiceUnavailable {
		t.Errorf("an ownership answer that arrived after the budget answered %d, want %d",
			w.Code, nethttp.StatusServiceUnavailable)
	}
}

// TestAnAnswerWithinTheBudgetIsHonoured keeps the two cases above honest: they
// have to fail for the lateness, not for the shape of a slow backend.
func TestAnAnswerWithinTheBudgetIsHonoured(t *testing.T) {
	prompt := enumerating("local", "alice")
	prompt.verify = func(_ context.Context, subject string, _ authn.Credential) authn.Verdict {
		time.Sleep(2 * time.Millisecond)
		return authn.Authenticated(subject)
	}

	g := gatewayWith(t, settings{
		endpoints: []string{"public"},
		budget:    2 * time.Second,
		inFlight:  4,
	}, prompt)
	sealed(t, g, routerWith("public"))

	if w, _ := run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret")); w.Code != nethttp.StatusOK {
		t.Errorf("a pass that arrived within the budget answered %d, want %d",
			w.Code, nethttp.StatusOK)
	}
}

// TestTheInFlightGateRefusesRatherThanQueues holds the bound this module puts on
// its own side.
//
// A backend that has stopped answering must not be able to take every goroutine
// the server has. Past the gate a request ends as an outage straight away rather
// than waiting, so the number of goroutines parked inside a broken backend is
// exactly the gate's width, and everything that does not need a backend goes on
// being served — the exempt routes above all, which is what lets a health check
// answer while the credential store is down.
func TestTheInFlightGateRefusesRatherThanQueues(t *testing.T) {
	admitted := make(chan struct{})
	release := make(chan struct{})

	blocking := enumerating("local", "alice")
	blocking.verify = func(context.Context, string, authn.Credential) authn.Verdict {
		close(admitted)
		<-release
		return authn.Denied(nil)
	}

	g := gatewayWith(t, settings{
		endpoints: []string{"public"},
		budget:    2 * time.Second,
		inFlight:  1,
	}, blocking)
	g.Exempt("public", "GET /health")
	sealed(t, g, routerWith("public"))

	go run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))
	<-admitted
	defer close(release)

	// The one slot is taken. A second request does not queue behind it.
	started := time.Now()
	w, seen := run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))
	waited := time.Since(started)

	if w.Code != nethttp.StatusServiceUnavailable {
		t.Errorf("a request past the in-flight gate answered %d, want %d",
			w.Code, nethttp.StatusServiceUnavailable)
	}
	if seen.reached {
		t.Error("the layer behind the gateway was reached by a request no backend answered")
	}
	if waited > time.Second {
		t.Errorf("the request waited %s before being refused; past the gate it has to end "+
			"straight away rather than queue behind a backend that stopped answering", waited)
	}

	if health, _ := run(g, "public", request("GET", "/health")); health.Code != nethttp.StatusOK {
		t.Errorf("an exempt route answered %d while the in-flight gate was full, want %d: an "+
			"exempt route reaches no backend, so a saturated gate cannot touch it",
			health.Code, nethttp.StatusOK)
	}
}

// TestASaturatedGateIsNotChargedToTheBackend holds the attribution of that
// refusal. The backend was never asked, and charging it would send an operator
// to read the logs of a credential store that is working perfectly.
func TestASaturatedGateIsNotChargedToTheBackend(t *testing.T) {
	g := gatewayWith(t, settings{
		endpoints: []string{"public"},
		budget:    time.Second,
		inFlight:  1,
	}, passes(enumerating("local", "alice")))
	sealed(t, g, routerWith("public"))

	// Take the only slot and keep it for the length of the case.
	g.slots <- struct{}{}

	_, deny := g.identify(withBasic(request("GET", "/orders"), "alice", "secret"))
	if deny == nil {
		t.Fatal("a request past a full in-flight gate was admitted")
	}
	if deny.status != nethttp.StatusServiceUnavailable {
		t.Errorf("the conclusion answers %d, want %d", deny.status,
			nethttp.StatusServiceUnavailable)
	}
	if deny.backend != "" {
		t.Errorf("the outage is charged to %q, want the gateway: the gate is this module's own "+
			"limit and the backend was never asked", deny.backend)
	}
}

// TestDrainingGivesUpOnCallsThatOutlastTheBudget holds the other end of Close.
//
// A backend that has stopped answering must not hold the whole shutdown open.
// Close waits for what is inside a backend, bounded by the longest any one call
// should still take, and then reports rather than waiting on.
func TestDrainingGivesUpOnCallsThatOutlastTheBudget(t *testing.T) {
	admitted := make(chan struct{})
	release := make(chan struct{})
	defer close(release)

	stuck := enumerating("local", "alice")
	stuck.verify = func(context.Context, string, authn.Credential) authn.Verdict {
		close(admitted)
		<-release
		return authn.Denied(nil)
	}

	g := gatewayWith(t, settings{
		endpoints: []string{"public"},
		budget:    20 * time.Millisecond,
		inFlight:  2,
	}, stuck)
	sealed(t, g, routerWith("public"))

	go run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))
	<-admitted
	g.stop()

	started := time.Now()
	err := g.drain(context.Background())
	waited := time.Since(started)

	if !errors.Is(err, authn.ErrDrainTimeout) {
		t.Errorf("draining returned %v while a call was still inside a backend, want a failure "+
			"wrapping %v: a shutdown that reports nothing looks like a clean one", err,
			authn.ErrDrainTimeout)
	}
	if waited > time.Second {
		t.Errorf("draining waited %s for a backend that had stopped answering, and the whole "+
			"shutdown waited with it", waited)
	}
}

// TestACallerWithLessTimeThanTheBudgetIsNotABackendFailure holds the
// attribution of a deadline the request arrived with.
//
// A deadline already on the request context is the caller's, and it is the one
// that fires when it is the earlier of the two. Reading it as the backend
// overrunning its budget would name a backend that answered perfectly well, and
// send an operator to read the logs of a credential store that is fine.
func TestACallerWithLessTimeThanTheBudgetIsNotABackendFailure(t *testing.T) {
	unhurried := enumerating("local", "alice")
	unhurried.verify = func(_ context.Context, subject string, _ authn.Credential) authn.Verdict {
		time.Sleep(30 * time.Millisecond)
		return authn.Authenticated(subject)
	}

	g := gatewayWith(t, settings{
		endpoints: []string{"public"},
		budget:    10 * time.Second,
		inFlight:  4,
	}, unhurried)
	sealed(t, g, routerWith("public"))

	// The request arrives with far less time left than the budget allows.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	r := withBasic(request("GET", "/orders"), "alice", "secret")

	// The backend ignores the deadline and answers after the caller's has
	// fired. The budget was never exceeded, so the answer is the backend's
	// own and it stands: a caller's deadline is not the gateway's to turn
	// into a verdict about anybody.
	if _, deny := g.identify(r.WithContext(ctx)); deny != nil {
		t.Errorf("a pass that arrived within the budget but after the caller's own deadline "+
			"ended as %d charged to %q; the budget was not exceeded, so the answer stands",
			deny.status, deny.backend)
	}
}

// honouring builds a backend that does what the extension surface asks of every
// backend: it waits on its context, and once that ends it reports that it could
// not answer. inside is closed when the call has begun.
func honouring(inside chan struct{}) *fakeVerifier {
	backend := enumerating("local", "alice")
	backend.verify = func(ctx context.Context, _ string, _ authn.Credential) authn.Verdict {
		close(inside)
		<-ctx.Done()
		return authn.Unavailable(ctx.Err())
	}
	return backend
}

// TestABackendThatHonoursItsContextIsNotChargedForTheCallersDeadline holds the
// attribution of a context the request itself brought to an end.
//
// A backend is required to return once its context ends, and the one that does
// reports that it could not answer — it has nothing else to report. When what
// ended the context is the caller's own deadline or the caller going away,
// charging that report to the backend names a credential store that is healthy
// and inflates the outage count an operator alerts on with requests nobody was
// waiting for. It is charged to the gateway, as the other outages in which no
// backend failed are.
func TestABackendThatHonoursItsContextIsNotChargedForTheCallersDeadline(t *testing.T) {
	for _, c := range []struct {
		name string
		// end brings the request's own context to an end once the
		// backend is inside the call.
		end func(t *testing.T, inside chan struct{}) (context.Context, func())
	}{
		{
			name: "the caller went away",
			end: func(_ *testing.T, inside chan struct{}) (context.Context, func()) {
				ctx, cancel := context.WithCancel(context.Background())
				go func() {
					<-inside
					cancel()
				}()
				return ctx, cancel
			},
		},
		{
			name: "the caller's deadline fired first",
			end: func(_ *testing.T, _ chan struct{}) (context.Context, func()) {
				return context.WithTimeout(context.Background(), 20*time.Millisecond)
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			inside := make(chan struct{})
			g := gatewayWith(t, settings{
				endpoints: []string{"public"},
				budget:    10 * time.Second,
				inFlight:  4,
			}, honouring(inside))
			sealed(t, g, routerWith("public"))

			ctx, cancel := c.end(t, inside)
			defer cancel()
			r := withBasic(request("GET", "/orders"), "alice", "secret")

			_, deny := g.identify(r.WithContext(ctx))
			if deny == nil {
				t.Fatal("a request whose own context ended while its backend waited was admitted")
			}
			if deny.status != nethttp.StatusServiceUnavailable {
				t.Errorf("the conclusion answers %d, want %d", deny.status,
					nethttp.StatusServiceUnavailable)
			}
			if deny.backend != "" {
				t.Errorf("the conclusion is charged to %q, want the gateway: the backend did "+
					"what it is required to do when its context ends", deny.backend)
			}
			if deny.level != slog.LevelInfo {
				t.Errorf("the conclusion is written at %v, want %v: a request its own caller "+
					"ended is ordinary traffic, not an outage anybody has to look at",
					deny.level, slog.LevelInfo)
			}
		})
	}
}

// TestAnOpenClaimThatHonoursItsContextIsNotChargedForTheCallersDeadline holds the
// same attribution for the ownership question, which is the other call a
// request makes into a backend and the one every bare name goes through.
func TestAnOpenClaimThatHonoursItsContextIsNotChargedForTheCallersDeadline(t *testing.T) {
	inside := make(chan struct{})
	directory := openly("directory", func(ctx context.Context, _ string) (bool, error) {
		close(inside)
		<-ctx.Done()
		return false, ctx.Err()
	})
	g := gatewayWith(t, settings{
		endpoints: []string{"public"},
		budget:    10 * time.Second,
		inFlight:  4,
	}, directory)
	sealed(t, g, routerWith("public"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-inside
		cancel()
	}()
	r := withBasic(request("GET", "/orders"), "dave", "secret")

	_, deny := g.identify(r.WithContext(ctx))
	if deny == nil {
		t.Fatal("a request whose own context ended while an open claim waited was admitted")
	}
	if deny.backend != "" {
		t.Errorf("the conclusion is charged to %q, want the gateway: the open claim did what it "+
			"is required to do when its context ends", deny.backend)
	}
}

// TestARequestWhoseContextHasEndedReachesNoBackend holds the same rule on the
// near side of the call. Nobody is waiting for the answer, and a backend asked
// anyway spends a verification on it — a password derivation, a directory round
// trip — and a slot of the in-flight gate while it does.
func TestARequestWhoseContextHasEndedReachesNoBackend(t *testing.T) {
	var asked atomic.Int64
	backend := passes(enumerating("local", "alice"))
	backend.asked = &asked
	g := testGateway(t, []string{"public"}, backend)
	sealed(t, g, routerWith("public"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := withBasic(request("GET", "/orders"), "alice", "secret")

	_, deny := g.identify(r.WithContext(ctx))
	if deny == nil {
		t.Fatal("a request whose own context had already ended was admitted")
	}
	if deny.status != nethttp.StatusServiceUnavailable || deny.backend != "" {
		t.Errorf("the conclusion answers %d charged to %q, want %d charged to the gateway",
			deny.status, deny.backend, nethttp.StatusServiceUnavailable)
	}
	if asked.Load() != 0 {
		t.Errorf("a backend was asked %d time(s) about a request nobody was waiting for, want none",
			asked.Load())
	}
}

// TestABackendThatRunsOutTheBudgetIsChargedEvenWhenItHonoursItsContext keeps the
// cases above honest: a backend that stops at the gateway's own deadline did not
// answer within the time it was given, and that is the backend's outage whatever
// it reports once the deadline has fired.
func TestABackendThatRunsOutTheBudgetIsChargedEvenWhenItHonoursItsContext(t *testing.T) {
	g := gatewayWith(t, settings{
		endpoints: []string{"public"},
		budget:    5 * time.Millisecond,
		inFlight:  4,
	}, honouring(make(chan struct{})))
	sealed(t, g, routerWith("public"))

	_, deny := g.identify(withBasic(request("GET", "/orders"), "alice", "secret"))
	if deny == nil {
		t.Fatal("a backend that ran out the budget admitted the request")
	}
	if deny.backend != "local" {
		t.Errorf("the conclusion is charged to %q, want the backend that did not answer within "+
			"the budget", deny.backend)
	}
}

// TestOneBareSubjectCostsOneSlotPerOpenClaim records a capacity interaction that
// is easy to miss when the gate's width is chosen.
//
// A bare name is put to every open claim at once, and each of those calls takes
// its own slot. A gateway with as many open claims as slots is therefore
// saturated by a single bare-name request, and the next one is refused while
// every backend involved is healthy. The gate has to be sized against the number
// of open claims, not against the number of requests.
func TestOneBareSubjectCostsOneSlotPerOpenClaim(t *testing.T) {
	inside := make(chan struct{}, 2)
	release := make(chan struct{})
	claims := func(context.Context, string) (bool, error) {
		inside <- struct{}{}
		<-release
		return false, nil
	}

	g := gatewayWith(t, settings{
		endpoints: []string{"public"},
		budget:    2 * time.Second,
		inFlight:  2,
	}, openly("first", claims), openly("second", claims))
	sealed(t, g, routerWith("public"))

	go run(g, "public", withBasic(request("GET", "/orders"), "dave", "secret"))
	<-inside
	<-inside
	defer close(release)

	// Both slots are held by one request's two ownership questions.
	w, _ := run(g, "public", withBasic(request("GET", "/orders"), "erin", "secret"))
	if w.Code != nethttp.StatusServiceUnavailable {
		t.Errorf("a second bare-name request answered %d, want %d: one request took a slot "+
			"per open claim, so the gate's width bounds open claims per request rather "+
			"than requests", w.Code, nethttp.StatusServiceUnavailable)
	}
}
