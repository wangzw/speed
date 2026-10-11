package gate

import (
	"context"
	"errors"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/log"
)

// recorder is a slog handler that keeps every record written to it, so that a
// case can read the level and the attributes a conclusion was logged with.
//
// Attributes bound to a logger made from it, with With or through log.WithAttrs,
// are on the records that logger writes, as they are with any handler: a case
// about what a layer bound on the request's logger reads them there.
type recorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recorder) Enabled(context.Context, slog.Level) bool { return true }

func (h *recorder) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *recorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	return boundRecorder{parent: h, attrs: attrs}
}

func (h *recorder) WithGroup(string) slog.Handler { return h }

// boundRecorder is a recorder with attributes bound: it writes each record to
// the parent with them added.
type boundRecorder struct {
	parent *recorder
	attrs  []slog.Attr
}

func (boundRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (b boundRecorder) Handle(ctx context.Context, r slog.Record) error {
	bound := r.Clone()
	bound.AddAttrs(b.attrs...)
	return b.parent.Handle(ctx, bound)
}

func (b boundRecorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	return boundRecorder{parent: b.parent, attrs: append(append([]slog.Attr(nil), b.attrs...), attrs...)}
}

func (b boundRecorder) WithGroup(string) slog.Handler { return b }

// only returns the one record written, failing the case when there is not
// exactly one: every conclusion writes one line, and a second would be a second
// conclusion.
func (h *recorder) only(t *testing.T) slog.Record {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.records) != 1 {
		t.Fatalf("the request wrote %d record(s), want exactly one", len(h.records))
	}
	return h.records[0]
}

// attr returns the value of the attribute key on r, and whether it is there.
func attr(r slog.Record, key string) (slog.Value, bool) {
	var found slog.Value
	var ok bool
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found, ok = a.Value, true
			return false
		}
		return true
	})
	return found, ok
}

// recorded builds a request whose logger writes into h.
func recorded(h *recorder, method, target string) *nethttp.Request {
	r := httptest.NewRequest(method, target, nil)
	return r.WithContext(log.WithLogger(r.Context(), slog.New(h)))
}

// TestEachConclusionIsLoggedAtTheLevelItsCauseDeserves holds the level every
// refusal and outage is written at.
//
// The level is the one filter every log pipeline applies before anything else,
// so a conclusion written below the level an operator keeps is a conclusion that
// was never written. A wrong password is ordinary traffic. A backend that cannot
// answer is something to look at. A backend that returned without answering, or
// that panicked, is a defect in code somebody has to go and fix, and the zero
// verdict in particular is counted apart from a refusal precisely so that it can
// be found — a signal only visible at the level a production deployment
// discards is not a signal.
func TestEachConclusionIsLoggedAtTheLevelItsCauseDeserves(t *testing.T) {
	for _, c := range []struct {
		name   string
		verify func(context.Context, authn.Credential) authn.Verdict
		level  slog.Level
		stack  bool
	}{
		{
			name: "the credential does not hold",
			verify: func(context.Context, authn.Credential) authn.Verdict {
				return authn.Denied(errors.New("wrong password"))
			},
			level: slog.LevelInfo,
		},
		{
			name: "the backend cannot say right now",
			verify: func(context.Context, authn.Credential) authn.Verdict {
				return authn.Unavailable(errors.New("the store is unreachable"))
			},
			level: slog.LevelWarn,
		},
		{
			name: "the backend returned without answering",
			verify: func(context.Context, authn.Credential) authn.Verdict {
				return authn.Verdict{}
			},
			level: slog.LevelError,
		},
		{
			name: "the backend panicked",
			verify: func(context.Context, authn.Credential) authn.Verdict {
				panic("the backend's store is nil")
			},
			level: slog.LevelError,
			stack: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			backend := basicBackend("static", "static.")
			backend.verify = c.verify
			g := testGateway(t, []string{"public"}, backend)
			sealed(t, g, routerWith("public"))

			h := &recorder{}
			run(g, "public", withBasic(recorded(h, "GET", "/orders"), "alice", "secret"))

			record := h.only(t)
			if record.Level != c.level {
				t.Errorf("the conclusion %q was logged at %v, want %v", record.Message,
					record.Level, c.level)
			}
			if _, has := attr(record, "stack"); has != c.stack {
				t.Errorf("the record carries a stack: %v, want %v; a panic is found by where it "+
					"was raised, and that is gone once the recover returns", has, c.stack)
			}
		})
	}
}

// TestEveryConclusionNamesTheEndpointTheStatusTheBucketAndWhoItIsChargedTo holds
// the four attributes every conclusion carries, which are what an operator reads
// a 401 or a 503 from since the response body says nothing of why.
func TestEveryConclusionNamesTheEndpointTheStatusTheBucketAndWhoItIsChargedTo(t *testing.T) {
	deny := basicBackend("static", "static.")
	deny.verify = func(context.Context, authn.Credential) authn.Verdict {
		return authn.Denied(errors.New("wrong password"))
	}
	g := testGateway(t, []string{"public"}, deny)
	sealed(t, g, routerWith("public"))

	h := &recorder{}
	run(g, "public", withBasic(recorded(h, "GET", "/orders"), "alice", "secret"))
	record := h.only(t)

	for key, want := range map[string]string{
		"endpoint": "public", "outcome": "denied", "attributed_to": "static",
	} {
		if got := attrText(record, key); got != want {
			t.Errorf("the record carries %s=%q, want %q", key, got, want)
		}
	}
	if status, found := attr(record, "status"); !found || status.Int64() != nethttp.StatusUnauthorized {
		t.Errorf("the record carries status %v, want %d", status, nethttp.StatusUnauthorized)
	}
	if got := attrText(record, "cause"); got != "wrong password" {
		t.Errorf("the record carries the cause %q, want what the backend gave", got)
	}
}

// TestAGatewayRefusalIsLoggedAsOrdinaryTraffic holds the other end: a request
// that brought no usable credential is what a public endpoint sees all day, and
// writing it any louder would bury the lines that matter.
func TestAGatewayRefusalIsLoggedAsOrdinaryTraffic(t *testing.T) {
	g := testGateway(t, []string{"public"}, passes(basicBackend("static", "static.")))
	sealed(t, g, routerWith("public"))

	h := &recorder{}
	run(g, "public", recorded(h, "GET", "/orders"))

	if record := h.only(t); record.Level != slog.LevelInfo {
		t.Errorf("a request with no credential was logged at %v, want %v", record.Level,
			slog.LevelInfo)
	}
}

// TestAnAnonymousPassIsLoggedAsOrdinaryTraffic holds that a request that passed
// on an optional route with no credential writes one line, at Info, and counts
// the anonymous bucket named in it.
func TestAnAnonymousPassIsLoggedAsOrdinaryTraffic(t *testing.T) {
	g := testGateway(t, []string{"public"}, passes(basicBackend("static", "static.")))
	g.Optional("public", "GET", "/catalog")
	sealed(t, g, routerWith("public"))

	h := &recorder{}
	engine, _ := routedEngine(g, "public", [2]string{"GET", "/catalog"})
	engine.ServeHTTP(httptest.NewRecorder(), recorded(h, "GET", "/catalog"))

	record := h.only(t)
	if record.Level != slog.LevelInfo {
		t.Errorf("an anonymous pass was logged at %v, want %v", record.Level, slog.LevelInfo)
	}
	if got := attrText(record, "outcome"); got != "anonymous" {
		t.Errorf("the record carries the outcome %q, want anonymous", got)
	}
	if got := attrText(record, "endpoint"); got != "public" {
		t.Errorf("the record carries the endpoint %q, want public", got)
	}
}

// TestARequestOnAnExemptRouteWritesNoRecordAndCountsNothing holds the silence of
// a route that was opened on purpose: it is not a conclusion about a credential,
// so it is neither logged nor counted.
func TestARequestOnAnExemptRouteWritesNoRecordAndCountsNothing(t *testing.T) {
	g := testGateway(t, []string{"public"}, passes(basicBackend("static", "static.")))
	g.Exempt("public", "GET", "/status")
	sealed(t, g, routerWith("public"))

	h := &recorder{}
	engine, _ := routedEngine(g, "public", [2]string{"GET", "/status"})
	engine.ServeHTTP(httptest.NewRecorder(), recorded(h, "GET", "/status"))

	if n := len(h.recordsOf()); n != 0 {
		t.Errorf("an exempt route wrote %d record(s), want none", n)
	}
	for bucket, n := range g.tally() {
		if n != 0 {
			t.Errorf("an exempt route was counted in the %s bucket (%d)", bucket, n)
		}
	}
}

// TestAPassOutsideTheNamespaceIsLoggedAsTheDefectItIs holds the level of the
// namespace check. The refusal is charged to the gateway, which judged it, but
// what it reports is a backend saying something it had no authority to say — a
// defect in that backend or an overlap in the deployment — and either one is
// somebody's to fix.
func TestAPassOutsideTheNamespaceIsLoggedAsTheDefectItIs(t *testing.T) {
	minting := basicBackend("corp", "corp.")
	minting.verify = func(context.Context, authn.Credential) authn.Verdict {
		return authn.Authenticated(authn.Principal{
			Account:   "static.ops",
			Methods:   []authn.Method{authn.MethodStatic},
			Assurance: authn.AssuranceSingle,
			AuthTime:  time.Now(),
		})
	}
	g := testGateway(t, []string{"public"}, minting)
	sealed(t, g, routerWith("public"))

	h := &recorder{}
	run(g, "public", withBasic(recorded(h, "GET", "/orders"), "alice", "secret"))

	record := h.only(t)
	if record.Level != slog.LevelError {
		t.Errorf("a pass refused by the namespace check was logged at %v, want %v", record.Level, slog.LevelError)
	}
	if got := attrText(record, "reason"); !strings.Contains(got, "namespace") {
		t.Errorf("the record gives the reason %q, want it to name the namespace", got)
	}
}

// TestAnImpersonationOutOfScopeIsLoggedAtInfoWithItsCause holds the record of the
// one refusal that is the gateway's judgement of a well-formed pass: it is
// ordinary traffic, written at Info, charged to the backend whose pass it
// declines, and says impersonation-scope as the cause.
func TestAnImpersonationOutOfScopeIsLoggedAtInfoWithItsCause(t *testing.T) {
	imp := &fakeVerifier{name: "imp", accepts: authn.Accepts{Schemes: []string{"bearer"}, Impersonation: true}}
	imp.verify = func(context.Context, authn.Credential) authn.Verdict {
		p := principalOf(imp, "customer")
		p.Actor, p.Session, p.Scope = "admin", "sess", "tenant-a"
		return authn.Authenticated(p)
	}
	g := testGateway(t, []string{"public"}, imp)
	sealed(t, g, routerWith("public"))

	h := &recorder{}
	run(g, "public", withBearer(recorded(h, "GET", "/orders"), "t"))

	record := h.only(t)
	if record.Level != slog.LevelInfo {
		t.Errorf("logged at %v, want %v", record.Level, slog.LevelInfo)
	}
	if got := attrText(record, "cause"); got != "impersonation-scope" {
		t.Errorf("the record gives the cause %q, want impersonation-scope", got)
	}
	if got := attrText(record, "attributed_to"); got != "imp" {
		t.Errorf("charged to %q, want the backend whose pass is declined", got)
	}
	if got := attrText(record, "outcome"); got != "denied" {
		t.Errorf("counted as %q, want denied", got)
	}
}

// TestTheGatewaysOwnPanicCarriesItsStack holds the record of this module's own
// defect: it is already written at Error, and without the stack it names the
// endpoint and the value and nothing that finds the line.
func TestTheGatewaysOwnPanicCarriesItsStack(t *testing.T) {
	g := testGateway(t, []string{"public"}, passes(basicBackend("static", "static.")))
	sealed(t, g, routerWith("public"))
	handler, _ := layerOn(g, "public")
	g.table = nil

	h := &recorder{}
	handler.ServeHTTP(httptest.NewRecorder(),
		withBasic(recorded(h, "GET", "/orders"), "alice", "secret"))

	record := h.only(t)
	if record.Level != slog.LevelError {
		t.Errorf("the gateway's own panic was logged at %v, want %v", record.Level, slog.LevelError)
	}
	if _, has := attr(record, "stack"); !has {
		t.Error("the record of the gateway's own panic carries no stack")
	}
}
