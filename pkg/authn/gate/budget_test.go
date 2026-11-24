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

type ended struct {
	status  int
	rec     *slog.Record
	reached bool
}

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

func asAliceBasic(r *nethttp.Request) *nethttp.Request { return withBasic(r, "alice", "secret") }

// TestAnAnswerThatArrivesAfterTheBudgetIsNotHonoured holds what the call
// budget is actually for.
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

// TestARefusalThatArrivesAfterTheBudgetIsNotHonouredEither holds the other
// half of the same rule.
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

// TestADeadlineInsideARefusalDoesNotMakeItAnOutage keeps a refusal carrying a
// deadline within the budget a 401, so a backend cannot turn it into a 503.
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

// TestTheInFlightGateRefusesRatherThanQueues holds the bound this module puts
// on its own side.
func TestTheInFlightGateRefusesRatherThanQueues(t *testing.T) {
	admitted := make(chan struct{})
	release := make(chan struct{})

	blocking := basicBackend("static", "static.")
	blocking.verify = func(context.Context, authn.Credential) authn.Verdict {
		close(admitted)
		<-release
		return authn.Denied(nil)
	}

	// The call budget is an hour, far past the bound this case allows: a gate that
	// queued behind the backend would need it, one that refuses at once none of it.
	// The refusal bound carries the caller's scale alone, so a fixed pause is caught.
	g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: time.Hour, inFlight: 1}, blocking)
	sealed(t, g, routerWith("public"))

	go run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))
	<-admitted
	defer close(release)

	// The one slot is taken; the second request must answer while the first still
	// holds it, so it answered at all only because it did not queue.
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

// TestAnExemptRouteStillAnswersWhileTheInFlightGateIsFull keeps exempt and
// optional routes served while the in-flight gate is full.
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
// refusal.
func TestASaturatedGateIsNotChargedToTheBackend(t *testing.T) {
	var asked atomic.Int64
	b := passes(basicBackend("static", "static."))
	b.asked = &asked
	g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: time.Second, inFlight: 1}, b)
	sealed(t, g, routerWith("public"))

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

// TestDrainingGivesUpOnCallsThatOutlastTheBudget holds the other end of
// Close.
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

	// The drain runs on the configured 20ms budget, not the package default of five
	// seconds, and the backend never answers. The elapsed bound carries GATE_TIME_SCALE
	// alone; the default (configDefaults.CallBudget) is the wrong implementation's time.
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

// endings are the two ways a request's own context ends while a backend is being
// asked, before the module's budget runs out: the caller goes away, or the
// deadline it brought fires first. inside is closed once the backend is in the call.
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

// TestABackendThatHonoursItsContextIsNotChargedForTheCallersDeadline holds
// the attribution of a context the request itself brought to an end.
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
// near side of the call.
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

// TestABackendThatRunsOutTheBudgetIsChargedEvenWhenItHonoursItsContext charges a
// backend that stops at the gateway's own deadline to the backend's outage.
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

// TestACallBudgetOverrunOnAnImpersonationIsStillAnOutage refuses a late
// impersonating pass before the principal is looked at.
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
