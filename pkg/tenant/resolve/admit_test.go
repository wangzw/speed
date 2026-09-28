package resolve

import (
	"context"
	"errors"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// The cases in this file drive the resolution layer the way the entry point
// does — through the middleware it registers — against a roster whose answers
// the case chooses, and read what it answered and what it logged.

// fakeRoster answers ByAlias from a fixed table, or with a fixed error. The
// resolution layer calls nothing else on the roster.
type fakeRoster struct {
	byAlias map[string]tenant.Tenant
	err     error
}

func (f fakeRoster) ByAlias(_ context.Context, alias string) (tenant.Tenant, bool, error) {
	if f.err != nil {
		return tenant.Tenant{}, false, f.err
	}
	t, ok := f.byAlias[alias]
	return t, ok, nil
}

func (fakeRoster) Get(context.Context, tenant.ID) (tenant.Tenant, bool, error) {
	return tenant.Tenant{}, false, errors.New("not used by the resolution layer")
}

func (fakeRoster) Put(context.Context, tenant.Tenant) error {
	return errors.New("not used by the resolution layer")
}

func (fakeRoster) SetStatus(context.Context, tenant.ID, tenant.Status) error {
	return errors.New("not used by the resolution layer")
}

func (fakeRoster) List(context.Context) ([]tenant.Tenant, error) {
	return nil, errors.New("not used by the resolution layer")
}

func (fakeRoster) Enter(ctx context.Context, _ tenant.ID) (context.Context, error) {
	return ctx, errors.New("not used by the resolution layer")
}

// record is one log record as a case reads it: the level, the message, and
// every attribute, bound or passed, rendered as text.
type record struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

// recorder is a slog handler that keeps what it is handed.
type recorder struct {
	mu      *sync.Mutex
	records *[]record
	bound   []slog.Attr
}

func newRecorder() *recorder {
	return &recorder{mu: &sync.Mutex{}, records: &[]record{}}
}

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	attrs := make(map[string]string, len(r.bound)+rec.NumAttrs())
	for _, a := range r.bound {
		attrs[a.Key] = a.Value.String()
	}
	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.records = append(*r.records, record{level: rec.Level, msg: rec.Message, attrs: attrs})
	return nil
}

func (r *recorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	bound := append(append([]slog.Attr(nil), r.bound...), attrs...)
	return &recorder{mu: r.mu, records: r.records, bound: bound}
}

func (r *recorder) WithGroup(string) slog.Handler { return r }

func (r *recorder) all() []record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]record(nil), *r.records...)
}

// harness is one resolution layer, the logger the module holds, and the
// logger the request arrives with.
type harness struct {
	layer   nethttp.Handler
	module  *recorder
	request *recorder
	reached *bool
	seen    *tenant.ID
}

// newHarness builds the layer over this roster, with the header extractor in
// force, in front of a handler that notes it was reached and writes one record
// through the request's logger.
func newHarness(t *testing.T, roster tenant.Catalog, extractor tenant.Extractor) harness {
	t.Helper()
	r := newResolver(settings{header: "X-Tenant"}, roster)
	r.selected = extractor
	module := newRecorder()
	r.logger = slog.New(module)

	reached := false
	var seen tenant.ID
	next := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, req *nethttp.Request) {
		reached = true
		seen, _ = tenant.FromContext(req.Context())
		log.FromContext(req.Context()).Info("handled")
		w.WriteHeader(nethttp.StatusNoContent)
	})
	return harness{
		layer:   r.middlewareFor().Wrap(next),
		module:  module,
		request: newRecorder(),
		reached: &reached,
		seen:    &seen,
	}
}

// serve sends one request carrying this tenant header, on a context whose
// logger already carries the subject the authentication layer bound.
func (h harness) serve(header string) *httptest.ResponseRecorder {
	ctx := log.WithLogger(context.Background(), slog.New(h.request))
	ctx = log.WithAttrs(ctx, slog.String("subject", "alice"))
	req := httptest.NewRequest(nethttp.MethodGet, "/orders", nil).WithContext(ctx)
	if header != "" {
		req.Header.Set("X-Tenant", header)
	}
	w := httptest.NewRecorder()
	h.layer.ServeHTTP(w, req)
	return w
}

func standardRoster() fakeRoster {
	return fakeRoster{byAlias: map[string]tenant.Tenant{
		"acme":   {ID: "t_acme", Alias: "acme", Status: tenant.StatusActive},
		"paused": {ID: "t_paused", Alias: "paused", Status: tenant.StatusDisabled},
	}}
}

// requireRefused asserts a 404 with no body that did not reach the handler.
func requireRefused(t *testing.T, h harness, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != nethttp.StatusNotFound {
		t.Fatalf("the layer answered %d, want 404", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("the layer wrote a body %q, want none", w.Body.String())
	}
	if *h.reached {
		t.Fatal("a refused request reached the handler")
	}
}

func TestARequestWithNoTenantNameIsNotFound(t *testing.T) {
	h := newHarness(t, standardRoster(), headerExtractor{name: "X-Tenant"})
	requireRefused(t, h, h.serve(""))
}

func TestANameOffTheRosterAndADisabledTenantAnswerAlike(t *testing.T) {
	unknown := newHarness(t, standardRoster(), headerExtractor{name: "X-Tenant"})
	wu := unknown.serve("ghost")
	requireRefused(t, unknown, wu)

	disabled := newHarness(t, standardRoster(), headerExtractor{name: "X-Tenant"})
	wd := disabled.serve("paused")
	requireRefused(t, disabled, wd)

	if len(wu.Header()) != len(wd.Header()) {
		t.Fatalf("an unknown and a disabled tenant were answered with different headers: %v and %v",
			wu.Header(), wd.Header())
	}
}

func TestAnUnreadableRosterIsAnInternalError(t *testing.T) {
	h := newHarness(t, fakeRoster{err: tenant.ErrRosterMissing}, headerExtractor{name: "X-Tenant"})
	w := h.serve("acme")
	if w.Code != nethttp.StatusInternalServerError {
		t.Fatalf("the layer answered %d over an unreadable roster, want 500", w.Code)
	}
	if *h.reached {
		t.Fatal("a request the roster could not answer reached the handler")
	}
}

// An admitted request carries the identifier, not the alias, both in the
// context and on the logger the handler writes through, beside what the
// layers outside it bound.
func TestAnAdmittedRequestCarriesTheIdentifier(t *testing.T) {
	h := newHarness(t, standardRoster(), headerExtractor{name: "X-Tenant"})
	w := h.serve("acme")
	if w.Code != nethttp.StatusNoContent || !*h.reached {
		t.Fatalf("an active tenant was answered %d (handler reached: %v), want the handler's 204",
			w.Code, *h.reached)
	}
	if *h.seen != "t_acme" {
		t.Fatalf("the handler read tenant %q, want the identifier t_acme", *h.seen)
	}
	records := h.request.all()
	if len(records) != 1 || records[0].msg != "handled" {
		t.Fatalf("the request's logger holds %+v, want the handler's one record", records)
	}
	if got := records[0].attrs[tenantField]; got != "t_acme" {
		t.Fatalf("the handler's record carries %s=%q, want t_acme", tenantField, got)
	}
	if got := records[0].attrs["subject"]; got != "alice" {
		t.Fatalf("the handler's record lost the subject bound outside, got %q", got)
	}
}

// A refusal is a record about one request, so it is written through the
// request's logger: it carries what the layers outside bound — the subject the
// authentication layer settled, the trace the tracing layer opened — which is
// what ties an authenticated caller probing tenant names to the probes.
func TestARefusalIsLoggedOnTheRequestsLogger(t *testing.T) {
	cases := []struct {
		name   string
		roster fakeRoster
		header string
		level  slog.Level
		reason string
	}{
		{"no name", standardRoster(), "", slog.LevelInfo, "the request carries no tenant name"},
		{"off the roster", standardRoster(), "ghost", slog.LevelInfo, "the name is not on the roster"},
		{"disabled", standardRoster(), "paused", slog.LevelInfo, "the tenant is not being served"},
		{"unreadable roster", fakeRoster{err: tenant.ErrRosterMissing}, "acme", slog.LevelError, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.roster, headerExtractor{name: "X-Tenant"})
			h.serve(c.header)
			if records := h.module.all(); len(records) != 0 {
				t.Fatalf("the refusal went to the module's logger: %+v", records)
			}
			records := h.request.all()
			if len(records) != 1 {
				t.Fatalf("the request's logger holds %+v, want one record of the refusal", records)
			}
			got := records[0]
			if got.level != c.level || got.attrs["subject"] != "alice" {
				t.Fatalf("the refusal was recorded as %+v, want level %v carrying subject alice",
					got, c.level)
			}
			if c.reason != "" && got.attrs["reason"] != c.reason {
				t.Fatalf("the refusal gives reason %q, want %q", got.attrs["reason"], c.reason)
			}
		})
	}
}
