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
	"github.com/vislake/speed/pkg/authn/internal/verdict"
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

// TestARefusalThatArrivesAfterTheBudgetIsNotHonouredEither holds the other half
// of the same rule. A backend slower than the budget is one that cannot answer
// in the time it is given, and whatever it says afterwards — a pass or a
// refusal — ends the request as its outage. The refusal is not kept as a 401:
// what raises a conclusion to an outage is the budget having run out, which the
// gateway reads off its own call context, never what the backend put into the
// refusal.
func TestARefusalThatArrivesAfterTheBudgetIsNotHonouredEither(t *testing.T) {
	slow := enumerating("local", "alice")
	slow.verify = func(context.Context, string, authn.Credential) authn.Verdict {
		time.Sleep(40 * time.Millisecond)
		return authn.Denied(errors.New("wrong password"))
	}

	g := gatewayWith(t, settings{
		endpoints: []string{"public"},
		budget:    5 * time.Millisecond,
		inFlight:  4,
	}, slow)
	sealed(t, g, routerWith("public"))

	_, deny := g.identify(withBasic(request("GET", "/orders"), "alice", "secret"))
	if deny == nil {
		t.Fatal("a refusal that arrived after the budget admitted the request")
	}
	if deny.status != nethttp.StatusServiceUnavailable || deny.backend != "local" {
		t.Errorf("a refusal that arrived after the budget answered %d charged to %q, want %d "+
			"charged to the backend that did not answer in time", deny.status, deny.backend,
			nethttp.StatusServiceUnavailable)
	}
}

// TestADeadlineInsideARefusalDoesNotMakeItAnOutage keeps the case above honest:
// a refusal that arrives within the budget stays a refusal whatever it carries,
// a timeout value included, so a backend cannot turn a wrong password into a 503
// by choosing its cause.
func TestADeadlineInsideARefusalDoesNotMakeItAnOutage(t *testing.T) {
	prompt := enumerating("local", "alice")
	prompt.verify = func(context.Context, string, authn.Credential) authn.Verdict {
		return authn.Denied(context.DeadlineExceeded)
	}

	g := gatewayWith(t, settings{
		endpoints: []string{"public"},
		budget:    time.Second,
		inFlight:  4,
	}, prompt)
	sealed(t, g, routerWith("public"))

	_, deny := g.identify(withBasic(request("GET", "/orders"), "alice", "secret"))
	if deny == nil || deny.status != nethttp.StatusUnauthorized {
		t.Errorf("a refusal carrying a deadline within the budget concluded as %+v, want a 401", deny)
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
// exactly the gate's width.
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
}

// TestAnExemptRouteStillAnswersWhileTheInFlightGateIsFull holds what the gate
// leaves alone: everything that needs no backend goes on being served, the
// exempt routes above all, which is what lets a health check answer while the
// credential store is down.
func TestAnExemptRouteStillAnswersWhileTheInFlightGateIsFull(t *testing.T) {
	admitted := make(chan struct{})
	release := make(chan struct{})
	blocking := enumerating("local", "alice")
	blocking.verify = func(context.Context, string, authn.Credential) authn.Verdict {
		close(admitted)
		<-release
		return authn.Denied(nil)
	}

	var refused, health reply
	liveAssembly{
		backends: []authn.Verifier{blocking},
		routes: []liveRoute{
			{endpoint: "public", method: "GET", path: "/orders"},
			{endpoint: "public", method: "GET", path: "/health"},
		},
		exempt:   []liveRoute{{endpoint: "public", method: "GET", path: "/health"}},
		inFlight: 1,
	}.serve(t, func(l *live) {
		// The one slot is taken by a request that stays inside the backend
		// until the case lets it go.
		held := make(chan reply, 1)
		go func() { held <- l.send("public", "GET", "/orders", asAlice) }()
		select {
		case <-admitted:
		case <-time.After(waitBound):
			t.Error("no request reached the backend, so the gate was never full")
			close(release)
			<-held
			return
		}

		refused = l.send("public", "GET", "/orders", asAlice)
		health = l.send("public", "GET", "/health")

		close(release)
		<-held
	})

	requireStatus(t, "a request past the full in-flight gate", refused, nethttp.StatusServiceUnavailable)
	requireStatus(t, "an exempt route while the in-flight gate is full", health, nethttp.StatusOK)
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

// endings are the two ways a request's own context comes to an end while a
// backend is being asked, before this module's budget runs out: the caller goes
// away, or the deadline the request arrived with fires first. inside is closed
// once the backend is inside the call.
var endings = []struct {
	name string
	end  func(inside chan struct{}) (context.Context, func())
}{
	{
		name: "the caller went away",
		end: func(inside chan struct{}) (context.Context, func()) {
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
		end: func(chan struct{}) (context.Context, func()) {
			return context.WithTimeout(context.Background(), 20*time.Millisecond)
		},
	},
}

// TestAnAnswerAboutTheCredentialStandsAfterTheCallersContextEnded holds which
// late answers the request's own context may change, and which it may not.
//
// A context the caller ended is read against one answer alone: a backend's
// report that it could not answer, which is what a backend returning on its
// context has to say and which is therefore no fact about its health. A pass, a
// refusal and a backend that returned without answering are each something the
// backend said about the credential or about itself, and each concludes as it
// would have with the caller still waiting. The refusal matters most: a wrong
// password read as an outage leaves the 401 bucket every lockout and rate rule
// keys on, and a backend that answers nothing stops being counted as a defect.
//
// The budget is ten seconds and never runs out, so what the case varies is the
// caller's context alone; the backend answers only once that has ended.
func TestAnAnswerAboutTheCredentialStandsAfterTheCallersContextEnded(t *testing.T) {
	for _, answer := range []struct {
		name    string
		verdict func(subject string) authn.Verdict
		// status and bucket are what the request concludes with; zero
		// status means it is admitted.
		status int
		bucket verdict.Outcome
		level  slog.Level
	}{
		{
			name:    "a pass",
			verdict: authn.Authenticated,
		},
		{
			name:    "a refusal",
			verdict: func(string) authn.Verdict { return authn.Denied(errors.New("wrong password")) },
			status:  nethttp.StatusUnauthorized,
			bucket:  verdict.Denied,
			level:   slog.LevelInfo,
		},
		{
			name:    "no answer at all",
			verdict: func(string) authn.Verdict { return authn.Verdict{} },
			status:  nethttp.StatusUnauthorized,
			bucket:  verdict.Unspoken,
			level:   slog.LevelError,
		},
	} {
		for _, ending := range endings {
			t.Run(answer.name+" after "+ending.name, func(t *testing.T) {
				inside := make(chan struct{})
				late := enumerating("local", "alice")
				late.verify = func(ctx context.Context, subject string, _ authn.Credential) authn.Verdict {
					close(inside)
					<-ctx.Done()
					return answer.verdict(subject)
				}
				g := gatewayWith(t, settings{
					endpoints: []string{"public"},
					budget:    10 * time.Second,
					inFlight:  4,
				}, late)
				sealed(t, g, routerWith("public"))

				ctx, cancel := ending.end(inside)
				defer cancel()
				r := withBasic(request("GET", "/orders"), "alice", "secret")

				_, deny := g.identify(r.WithContext(ctx))
				if answer.status == 0 {
					if deny != nil {
						t.Errorf("a pass that arrived after the caller's own context ended "+
							"concluded as %d charged to %q; the budget was not exceeded, "+
							"so the answer stands", deny.status, deny.backend)
					}
					return
				}
				if deny == nil {
					t.Fatalf("%s that arrived after the caller's own context ended admitted "+
						"the request", answer.name)
				}
				if deny.status != answer.status || deny.bucket != answer.bucket {
					t.Errorf("%s that arrived after the caller's own context ended concluded "+
						"as %d in the %v bucket, want %d in the %v bucket: only a report of "+
						"not being able to answer is read against the caller's context",
						answer.name, deny.status, deny.bucket, answer.status, answer.bucket)
				}
				if deny.backend != "local" {
					t.Errorf("the conclusion is charged to %q, want the backend that gave it",
						deny.backend)
				}
				if deny.level != answer.level {
					t.Errorf("the conclusion is written at %v, want %v", deny.level, answer.level)
				}
			})
		}
	}
}

// TestAnOpenClaimsAnswerStandsAfterTheCallersContextEnded holds the same rule
// for the ownership question. An open claim that says a bare name is not its own
// has answered, and the request is refused as unclaimed — a 401 charged to the
// gateway — however late the caller's context ended; only a claim that could
// not answer is read against that context.
func TestAnOpenClaimsAnswerStandsAfterTheCallersContextEnded(t *testing.T) {
	for _, ending := range endings {
		t.Run(ending.name, func(t *testing.T) {
			inside := make(chan struct{})
			directory := openly("directory", func(ctx context.Context, _ string) (bool, error) {
				close(inside)
				<-ctx.Done()
				return false, nil
			})
			g := gatewayWith(t, settings{
				endpoints: []string{"public"},
				budget:    10 * time.Second,
				inFlight:  4,
			}, directory)
			sealed(t, g, routerWith("public"))

			ctx, cancel := ending.end(inside)
			defer cancel()
			r := withBasic(request("GET", "/orders"), "dave", "secret")

			_, deny := g.identify(r.WithContext(ctx))
			if deny == nil {
				t.Fatal("a bare name no open claim owns was admitted")
			}
			if deny.status != nethttp.StatusUnauthorized || deny.bucket != verdict.Denied {
				t.Errorf("an open claim's answer that arrived after the caller's own context "+
					"ended concluded as %d in the %v bucket, want %d in the %v bucket",
					deny.status, deny.bucket, nethttp.StatusUnauthorized, verdict.Denied)
			}
			if deny.backend != "" {
				t.Errorf("the conclusion is charged to %q, want the gateway, whose table found "+
					"no owner", deny.backend)
			}
		})
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
	for _, ending := range endings {
		t.Run(ending.name, func(t *testing.T) {
			inside := make(chan struct{})
			g := gatewayWith(t, settings{
				endpoints: []string{"public"},
				budget:    10 * time.Second,
				inFlight:  4,
			}, honouring(inside))
			sealed(t, g, routerWith("public"))

			ctx, cancel := ending.end(inside)
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
