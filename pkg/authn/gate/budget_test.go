package gate

import (
	"context"
	"errors"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/log"
)

// ended is what a request concluded with: the status it was answered with, the
// one record a refusal writes (a pass writes none), and whether the layer behind
// the gateway was reached.
type ended struct {
	status  int
	rec     *slog.Record
	reached bool
}

// concludeUnder runs one request on the public endpoint, with the context the
// case gives it and a recorder behind its logger, and reports how it ended.
func concludeUnder(t *testing.T, g *gateway, ctx context.Context, edit func(*nethttp.Request) *nethttp.Request) ended {
	t.Helper()
	h := &recorder{}
	r := edit(httptest.NewRequest("GET", "/orders", nil))
	r = r.WithContext(log.WithLogger(ctx, slog.New(h)))

	engine, seen := layerOn(g, "public")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, r)

	out := ended{status: w.Code, reached: seen.reached}
	if recs := h.recordsOf(); len(recs) > 0 {
		out.rec = &recs[0]
	}
	return out
}

// asAliceBasic puts alice's Basic credential on a request.
func asAliceBasic(r *nethttp.Request) *nethttp.Request { return withBasic(r, "alice", "secret") }

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
	slow := basicBackend("static", "static.")
	slow.verify = func(context.Context, authn.Credential) authn.Verdict {
		// Deliberately ignores the deadline it was handed, which is what a
		// backend that does not honour its context looks like.
		time.Sleep(40 * time.Millisecond)
		return held(slow, "alice")
	}

	g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: 5 * time.Millisecond, inFlight: 4}, slow)
	sealed(t, g, routerWith("public"))

	w, seen := run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))

	if w.Code != nethttp.StatusServiceUnavailable {
		t.Errorf("a pass that arrived after the budget answered %d, want %d", w.Code, nethttp.StatusServiceUnavailable)
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
// of the same rule. A backend slower than the budget is one that cannot answer in
// the time it is given, and whatever it says afterwards — a pass or a refusal —
// ends the request as its outage. The refusal is not kept as a 401: what raises a
// conclusion to an outage is the budget having run out, which the gateway reads
// off its own call context, never what the backend put into the refusal.
func TestARefusalThatArrivesAfterTheBudgetIsNotHonouredEither(t *testing.T) {
	slow := basicBackend("static", "static.")
	slow.verify = func(context.Context, authn.Credential) authn.Verdict {
		time.Sleep(40 * time.Millisecond)
		return authn.Denied(errors.New("wrong password"))
	}

	g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: 5 * time.Millisecond, inFlight: 4}, slow)
	sealed(t, g, routerWith("public"))

	out := concludeUnder(t, g, context.Background(), asAliceBasic)
	if out.status != nethttp.StatusServiceUnavailable || out.rec == nil {
		t.Fatalf("a refusal that arrived after the budget answered %d, want %d", out.status, nethttp.StatusServiceUnavailable)
	}
	if got := attrText(*out.rec, "attributed_to"); got != "static" {
		t.Errorf("the conclusion is charged to %q, want the backend that did not answer in time", got)
	}
}

// TestADeadlineInsideARefusalDoesNotMakeItAnOutage keeps the case above honest: a
// refusal that arrives within the budget stays a refusal whatever it carries, a
// timeout value included, so a backend cannot turn a wrong password into a 503 by
// choosing its cause.
func TestADeadlineInsideARefusalDoesNotMakeItAnOutage(t *testing.T) {
	prompt := basicBackend("static", "static.")
	prompt.verify = func(context.Context, authn.Credential) authn.Verdict {
		return authn.Denied(context.DeadlineExceeded)
	}

	g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: time.Second, inFlight: 4}, prompt)
	sealed(t, g, routerWith("public"))

	if out := concludeUnder(t, g, context.Background(), asAliceBasic); out.status != nethttp.StatusUnauthorized {
		t.Errorf("a refusal carrying a deadline within the budget concluded as %d, want a 401", out.status)
	}
}

// TestAnAnswerWithinTheBudgetIsHonoured keeps the two cases above honest: they
// have to fail for the lateness, not for the shape of a slow backend.
func TestAnAnswerWithinTheBudgetIsHonoured(t *testing.T) {
	prompt := basicBackend("static", "static.")
	prompt.verify = func(context.Context, authn.Credential) authn.Verdict {
		time.Sleep(2 * time.Millisecond)
		return held(prompt, "alice")
	}

	g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: 2 * time.Second, inFlight: 4}, prompt)
	sealed(t, g, routerWith("public"))

	if w, _ := run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret")); w.Code != nethttp.StatusOK {
		t.Errorf("a pass that arrived within the budget answered %d, want %d", w.Code, nethttp.StatusOK)
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

	blocking := basicBackend("static", "static.")
	blocking.verify = func(context.Context, authn.Credential) authn.Verdict {
		close(admitted)
		<-release
		return authn.Denied(nil)
	}

	// The call budget is an hour, far past the bound this case allows: a gate
	// that queued behind the backend and then gave up when the budget ran out
	// would need up to that hour, while a gate that refuses at once needs none
	// of it. That hour is the wrong implementation's time, stated to
	// DeclaredAgainst rather than left to a comment. The bound on the refusal
	// carries the caller's scale alone and not the race factor, so an
	// implementation that pauses a fixed few seconds before refusing is seen
	// here as well.
	g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: time.Hour, inFlight: 1}, blocking)
	sealed(t, g, routerWith("public"))

	go run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))
	<-admitted
	defer close(release)

	// The one slot is taken. A second request does not queue behind it: the
	// refusal has to come back while the first request still holds the slot.
	// That is the structural difference. A gate that queued would wait for the
	// first request, which the case holds inside the backend until it ends, so
	// the second one answers at all only because it did not queue.
	refused := make(chan struct{})
	var w *httptest.ResponseRecorder
	var seen *arrival
	started := time.Now()
	go func() {
		w, seen = run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))
		close(refused)
	}()
	select {
	case <-refused:
	case <-time.After(coretest.Scale(30 * time.Second)):
		t.Fatal("a request past the full in-flight gate did not answer while the one slot was " +
			"held: it queued behind the backend that stopped answering")
	}
	if waited, limit := time.Since(started), coretest.DeclaredAgainst(t, 0, time.Second, time.Hour); waited > limit {
		t.Errorf("the request waited %s before being refused, past the %s an immediate refusal gets: "+
			"a gate that pauses before refusing still holds the request while the backend has stopped answering",
			waited, limit)
	}

	if w.Code != nethttp.StatusServiceUnavailable {
		t.Errorf("a request past the in-flight gate answered %d, want %d", w.Code, nethttp.StatusServiceUnavailable)
	}
	if seen.reached {
		t.Error("the layer behind the gateway was reached by a request no backend answered")
	}
	if got := w.Header().Get("Retry-After"); got != "" {
		t.Errorf("the refusal carries Retry-After %q: a full gate promises nothing about when to come back", got)
	}
}

// TestAnExemptRouteStillAnswersWhileTheInFlightGateIsFull holds what the gate
// leaves alone: everything that needs no backend goes on being served, the exempt
// routes above all, which is what lets a health check answer while the credential
// store is down.
func TestAnExemptRouteStillAnswersWhileTheInFlightGateIsFull(t *testing.T) {
	admitted := make(chan struct{})
	release := make(chan struct{})
	blocking := basicBackend("static", "static.")
	blocking.verify = func(context.Context, authn.Credential) authn.Verdict {
		close(admitted)
		<-release
		return authn.Denied(nil)
	}

	var refused, health, catalog reply
	liveAssembly{
		backends: []authn.Verifier{blocking},
		routes: []liveRoute{
			{endpoint: "public", method: "GET", path: "/orders"},
			{endpoint: "public", method: "GET", path: "/health"},
			{endpoint: "public", method: "GET", path: "/catalog"},
		},
		exempt:   []liveRoute{{endpoint: "public", method: "GET", path: "/health"}},
		optional: []liveRoute{{endpoint: "public", method: "GET", path: "/catalog"}},
		inFlight: 1,
	}.serve(t, func(l *live) {
		// The one slot is taken by a request that stays inside the backend until
		// the case lets it go.
		held := make(chan reply, 1)
		go func() { held <- l.send("public", "GET", "/orders", asAlice) }()
		select {
		case <-admitted:
		case <-time.After(waitBound()):
			t.Error("no request reached the backend, so the gate was never full")
			close(release)
			<-held
			return
		}

		refused = l.send("public", "GET", "/orders", asAlice)
		health = l.send("public", "GET", "/health")
		catalog = l.send("public", "GET", "/catalog")

		close(release)
		<-held
	})

	requireStatus(t, "a request past the full in-flight gate", refused, nethttp.StatusServiceUnavailable)
	requireStatus(t, "an exempt route while the in-flight gate is full", health, nethttp.StatusOK)
	requireStatus(t, "an optional route with no credential while the in-flight gate is full", catalog, nethttp.StatusOK)
}

// TestASaturatedGateIsNotChargedToTheBackend holds the attribution of that
// refusal. The backend was never asked, and charging it would send an operator to
// read the logs of a credential store that is working perfectly.
func TestASaturatedGateIsNotChargedToTheBackend(t *testing.T) {
	var asked atomic.Int64
	b := passes(basicBackend("static", "static."))
	b.asked = &asked
	g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: time.Second, inFlight: 1}, b)
	sealed(t, g, routerWith("public"))

	// Take the only slot and keep it for the length of the case.
	g.slots <- struct{}{}

	out := concludeUnder(t, g, context.Background(), asAliceBasic)
	if out.status != nethttp.StatusServiceUnavailable || out.rec == nil {
		t.Fatalf("a request past a full in-flight gate answered %d, want %d", out.status, nethttp.StatusServiceUnavailable)
	}
	if got := attrText(*out.rec, "attributed_to"); got != ModuleName {
		t.Errorf("the outage is charged to %q, want the gateway: the gate is this module's own "+
			"limit and the backend was never asked", got)
	}
	if asked.Load() != 0 {
		t.Errorf("the backend was asked %d time(s) past a full gate", asked.Load())
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

	stuck := basicBackend("static", "static.")
	stuck.verify = func(context.Context, authn.Credential) authn.Verdict {
		close(admitted)
		<-release
		return authn.Denied(nil)
	}

	const budget = 20 * time.Millisecond
	// drainHeadroom is how far past the configured budget a drain may return:
	// ten times the budget, which is room for a loaded machine and still far
	// short of the package default the wrong implementation runs on.
	const drainHeadroom = 10 * budget
	g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: budget, inFlight: 2}, stuck)
	sealed(t, g, routerWith("public"))

	go run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))
	<-admitted
	g.stop()

	// The drain runs on the configured 20ms budget, not the package default of
	// five seconds; the backend never answers. Waiting in a goroutine separates
	// a bounded drain (which returns at once) from an unbounded one (which
	// waits for the backend the case holds until it ends). The elapsed bound
	// below is what tells the configured budget from the default: it carries
	// GATE_TIME_SCALE alone, and the default (configDefaults.CallBudget) is the
	// wrong implementation's time, stated to DeclaredAgainst.
	drained := make(chan error, 1)
	started := time.Now()
	go func() { drained <- g.drain(context.Background()) }()
	select {
	case err := <-drained:
		if !errors.Is(err, authn.ErrDrainTimeout) {
			t.Errorf("draining returned %v while a call was still inside a backend, want a failure "+
				"wrapping %v: a shutdown that reports nothing looks like a clean one", err, authn.ErrDrainTimeout)
		}
		took := time.Since(started)
		if took < budget {
			t.Errorf("draining returned after %s, before the configured budget of %s: it did not "+
				"wait for the call inside the backend", took, budget)
		}
		if limit := coretest.DeclaredAgainst(t, budget, drainHeadroom, configDefaults.CallBudget); took > limit {
			t.Errorf("draining returned after %s, past the limit of %s the configured budget of %s "+
				"allows: it ran on the default call budget of %v rather than the configured one",
				took, limit, budget, configDefaults.CallBudget)
		}
	case <-time.After(coretest.Scale(30 * time.Second)):
		t.Fatal("draining did not return within its budget while a call was still inside a " +
			"backend: the whole shutdown waited for a backend that had stopped answering")
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
// A context the caller ended is read against one answer alone: a backend's report
// that it could not answer, which is what a backend returning on its context has
// to say and which is therefore no fact about its health. A pass, a refusal and a
// backend that returned without answering are each something the backend said
// about the credential or about itself, and each concludes as it would have with
// the caller still waiting. The refusal matters most: a wrong password read as an
// outage leaves the 401 bucket every lockout and rate rule keys on, and a backend
// that answers nothing stops being counted as a defect.
//
// The budget is ten seconds and never runs out, so what the case varies is the
// caller's context alone; the backend answers only once that has ended.
func TestAnAnswerAboutTheCredentialStandsAfterTheCallersContextEnded(t *testing.T) {
	for _, answer := range []struct {
		name    string
		verdict func(*fakeVerifier) authn.Verdict
		// status and bucket are what the request concludes with.
		status int
		bucket string
		level  slog.Level
	}{
		{"a pass", func(f *fakeVerifier) authn.Verdict { return held(f, "alice") }, nethttp.StatusOK, "authenticated", 0},
		{
			"a refusal", func(*fakeVerifier) authn.Verdict { return authn.Denied(errors.New("wrong password")) },
			nethttp.StatusUnauthorized, "denied", slog.LevelInfo,
		},
		{
			"no answer at all", func(*fakeVerifier) authn.Verdict { return authn.Verdict{} },
			nethttp.StatusUnauthorized, "unspoken", slog.LevelError,
		},
	} {
		for _, ending := range endings {
			t.Run(answer.name+" after "+ending.name, func(t *testing.T) {
				inside := make(chan struct{})
				late := basicBackend("static", "static.")
				late.verify = func(ctx context.Context, _ authn.Credential) authn.Verdict {
					close(inside)
					<-ctx.Done()
					return answer.verdict(late)
				}
				g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: 10 * time.Second, inFlight: 4}, late)
				sealed(t, g, routerWith("public"))

				ctx, cancel := ending.end(inside)
				defer cancel()

				out := concludeUnder(t, g, ctx, asAliceBasic)
				if out.status != answer.status {
					t.Fatalf("%s that arrived after the caller's own context ended concluded as %d, want %d: "+
						"only a report of not being able to answer is read against the caller's context",
						answer.name, out.status, answer.status)
				}
				if got := g.tally()[answer.bucket]; got != 1 {
					t.Errorf("the conclusion was counted %d time(s) in the %s bucket, want 1: %v", got, answer.bucket, g.tally())
				}
				if answer.status == nethttp.StatusOK {
					return
				}
				if out.rec == nil {
					t.Fatal("the refusal wrote no record")
				}
				if got := attrText(*out.rec, "attributed_to"); got != "static" {
					t.Errorf("the conclusion is charged to %q, want the backend that gave it", got)
				}
				if out.rec.Level != answer.level {
					t.Errorf("the conclusion is written at %v, want %v", out.rec.Level, answer.level)
				}
			})
		}
	}
}

// honouring builds a backend that does what the extension surface asks of every
// backend: it waits on its context, and once that ends it reports that it could
// not answer. inside is closed when the call has begun.
func honouring(inside chan struct{}) *fakeVerifier {
	backend := basicBackend("static", "static.")
	backend.verify = func(ctx context.Context, _ authn.Credential) authn.Verdict {
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
			g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: 10 * time.Second, inFlight: 4}, honouring(inside))
			sealed(t, g, routerWith("public"))

			ctx, cancel := ending.end(inside)
			defer cancel()

			out := concludeUnder(t, g, ctx, asAliceBasic)
			if out.status != nethttp.StatusServiceUnavailable || out.rec == nil {
				t.Fatalf("the conclusion answers %d, want %d", out.status, nethttp.StatusServiceUnavailable)
			}
			if got := attrText(*out.rec, "attributed_to"); got != ModuleName {
				t.Errorf("the conclusion is charged to %q, want the gateway: the backend did what it "+
					"is required to do when its context ends", got)
			}
			if out.rec.Level != slog.LevelInfo {
				t.Errorf("the conclusion is written at %v, want %v: a request its own caller ended is "+
					"ordinary traffic, not an outage anybody has to look at", out.rec.Level, slog.LevelInfo)
			}
		})
	}
}

// TestARequestWhoseContextHasEndedReachesNoBackend holds the same rule on the
// near side of the call. Nobody is waiting for the answer, and a backend asked
// anyway spends a verification on it — a password derivation, a directory round
// trip — and a slot of the in-flight gate while it does.
func TestARequestWhoseContextHasEndedReachesNoBackend(t *testing.T) {
	var asked atomic.Int64
	backend := passes(basicBackend("static", "static."))
	backend.asked = &asked
	g := testGateway(t, []string{"public"}, backend)
	sealed(t, g, routerWith("public"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := concludeUnder(t, g, ctx, asAliceBasic)
	if out.status != nethttp.StatusServiceUnavailable || out.rec == nil {
		t.Fatalf("the conclusion answers %d, want %d", out.status, nethttp.StatusServiceUnavailable)
	}
	if got := attrText(*out.rec, "attributed_to"); got != ModuleName {
		t.Errorf("the conclusion is charged to %q, want the gateway", got)
	}
	if asked.Load() != 0 {
		t.Errorf("a backend was asked %d time(s) about a request nobody was waiting for, want none", asked.Load())
	}
}

// TestABackendThatRunsOutTheBudgetIsChargedEvenWhenItHonoursItsContext keeps the
// cases above honest: a backend that stops at the gateway's own deadline did not
// answer within the time it was given, and that is the backend's outage whatever
// it reports once the deadline has fired.
func TestABackendThatRunsOutTheBudgetIsChargedEvenWhenItHonoursItsContext(t *testing.T) {
	g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: 5 * time.Millisecond, inFlight: 4},
		honouring(make(chan struct{})))
	sealed(t, g, routerWith("public"))

	out := concludeUnder(t, g, context.Background(), asAliceBasic)
	if out.status != nethttp.StatusServiceUnavailable || out.rec == nil {
		t.Fatalf("a backend that ran out the budget answered %d, want %d", out.status, nethttp.StatusServiceUnavailable)
	}
	if got := attrText(*out.rec, "attributed_to"); got != "static" {
		t.Errorf("the conclusion is charged to %q, want the backend that did not answer within the budget", got)
	}
}

// TestACallBudgetOverrunOnAnImpersonationIsStillAnOutage holds that a pass that
// arrives late is not read for what it says, impersonating or not: the budget
// invalidates the answer before the principal is looked at, so a backend that is
// late cannot get an identity with an actor admitted either.
func TestACallBudgetOverrunOnAnImpersonationIsStillAnOutage(t *testing.T) {
	imp := &fakeVerifier{name: "imp", accepts: authn.Accepts{Schemes: []string{"bearer"}, Impersonation: true}}
	imp.verify = func(context.Context, authn.Credential) authn.Verdict {
		time.Sleep(40 * time.Millisecond)
		p := principalOf(imp, "customer")
		p.Actor, p.Session, p.Scope = "admin", "sess", "tenant-a"
		return authn.Authenticated(p)
	}
	g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: 5 * time.Millisecond, inFlight: 4}, imp)
	g.Impersonable("public", "GET", "/x")
	sealed(t, g, routerWith("public"))

	w, seen := run(g, "public", withBearer(request("GET", "/x"), "t"))
	if w.Code != nethttp.StatusServiceUnavailable || seen.reached {
		t.Errorf("answered %d (reached %v), want a 503 that ends the request", w.Code, seen.reached)
	}
}
