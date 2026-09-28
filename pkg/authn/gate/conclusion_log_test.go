package gate

import (
	"context"
	"errors"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/log"
)

// recorder is a slog handler that keeps every record written to it, so that a
// case can read the level and the attributes a conclusion was logged with.
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

func (h *recorder) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recorder) WithGroup(string) slog.Handler      { return h }

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
		verify func(context.Context, string, authn.Credential) authn.Verdict
		level  slog.Level
		stack  bool
	}{
		{
			name: "the credential does not hold",
			verify: func(context.Context, string, authn.Credential) authn.Verdict {
				return authn.Denied(errors.New("wrong password"))
			},
			level: slog.LevelInfo,
		},
		{
			name: "the backend cannot say right now",
			verify: func(context.Context, string, authn.Credential) authn.Verdict {
				return authn.Unavailable(errors.New("the store is unreachable"))
			},
			level: slog.LevelWarn,
		},
		{
			name: "the backend returned without answering",
			verify: func(context.Context, string, authn.Credential) authn.Verdict {
				return authn.Verdict{}
			},
			level: slog.LevelError,
		},
		{
			name: "the backend panicked",
			verify: func(context.Context, string, authn.Credential) authn.Verdict {
				panic("the backend's store is nil")
			},
			level: slog.LevelError,
			stack: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			backend := enumerating("local", "alice")
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

// TestAGatewayRefusalIsLoggedAsOrdinaryTraffic holds the other end: a request
// that brought no usable credential is what a public endpoint sees all day, and
// writing it any louder would bury the lines that matter.
func TestAGatewayRefusalIsLoggedAsOrdinaryTraffic(t *testing.T) {
	g := testGateway(t, []string{"public"}, passes(enumerating("local", "alice")))
	sealed(t, g, routerWith("public"))

	h := &recorder{}
	run(g, "public", recorded(h, "GET", "/orders"))

	if record := h.only(t); record.Level != slog.LevelInfo {
		t.Errorf("a request with no credential was logged at %v, want %v", record.Level,
			slog.LevelInfo)
	}
}

// TestAPassOutsideTheClaimIsLoggedAsTheDefectItIs holds the level of exit
// binding. The refusal is charged to the gateway, which judged it, but what it
// reports is a backend saying something it had no authority to say — a defect
// in that backend or an overlap in the deployment — and either one is somebody's
// to fix.
func TestAPassOutsideTheClaimIsLoggedAsTheDefectItIs(t *testing.T) {
	minting := enumerating("local", "alice")
	minting.verify = func(context.Context, string, authn.Credential) authn.Verdict {
		return authn.Authenticated("mallory")
	}
	g := testGateway(t, []string{"public"}, minting)
	sealed(t, g, routerWith("public"))

	h := &recorder{}
	run(g, "public", withBasic(recorded(h, "GET", "/orders"), "alice", "secret"))

	if record := h.only(t); record.Level != slog.LevelError {
		t.Errorf("a pass refused by exit binding was logged at %v, want %v", record.Level,
			slog.LevelError)
	}
}

// TestAnOpenClaimThatPanicsIsLoggedAsTheDefectItIs holds the same level for the
// other call a request makes into a backend. The ownership question runs on a
// goroutine of its own, and the stack has to be taken there, where the panic
// was raised.
func TestAnOpenClaimThatPanicsIsLoggedAsTheDefectItIs(t *testing.T) {
	g := testGateway(t, []string{"public"}, openly("directory",
		func(context.Context, string) (bool, error) { panic("the directory client is nil") }))
	sealed(t, g, routerWith("public"))

	h := &recorder{}
	run(g, "public", withBasic(recorded(h, "GET", "/orders"), "dave", "secret"))

	record := h.only(t)
	if record.Level != slog.LevelError {
		t.Errorf("an open claim that panicked was logged at %v, want %v", record.Level,
			slog.LevelError)
	}
	if _, has := attr(record, "stack"); !has {
		t.Error("the record of an open claim's panic carries no stack")
	}
}

// TestTheGatewaysOwnPanicCarriesItsStack holds the record of this module's own
// defect: it is already written at Error, and without the stack it names the
// endpoint and the value and nothing that finds the line.
func TestTheGatewaysOwnPanicCarriesItsStack(t *testing.T) {
	g := testGateway(t, []string{"public"}, passes(enumerating("local", "alice")))
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
