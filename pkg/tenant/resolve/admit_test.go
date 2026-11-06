package resolve

import (
	"context"
	"log/slog"
	nethttp "net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

// The cases in this file drive the resolution layer the way a request reaches
// it: over a socket, through the entry point's chain, against a roster whose
// answers the case chooses. They read what the layer answered and what it
// logged.

// sighting is what the layer just inside the resolution layer saw of one
// request.
type sighting struct {
	path     string
	fullPath string
	id       tenant.ID
	tenanted bool
}

// probe is what a case registers on the guarded endpoint, and what it noted
// about the requests that reached it. Three things are registered: a layer
// outside the resolution layer that binds the subject the way the
// authentication layer does, a layer just inside it, and one route.
type probe struct {
	// write is the record the route's handler writes through its request's
	// logger. Nil writes one record, "handled".
	write func(ctx context.Context)
	// extractors are registered through the resolution module's own seam, in
	// the registrant's Init.
	extractors map[string]tenant.Extractor

	mu      sync.Mutex
	handled []tenant.ID
	seen    []sighting
}

// register is the registrant's Init: what a real module does with the entry
// point.
func (p *probe) register(reg *core.Registry, ep speedhttp.Endpoint) error {
	if len(p.extractors) > 0 {
		resolution, err := core.Resolve[tenant.Resolution](reg)
		if err != nil {
			return err
		}
		for name, e := range p.extractors {
			resolution.Register(name, e)
		}
	}
	ep.Use(subjectLayer())
	ep.Use(p.witness())
	ep.Route(nethttp.MethodGet, "/orders", p.orders)
	return nil
}

// subjectLayer stands for the authentication layer: it points at no one, claims
// the capability the resolution layer points at, and binds the subject onto the
// request's logger. It is in the identifying stage and declares no constraint,
// so the resolution layer sits inside it only because of the After it declares.
func subjectLayer() speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:     "subject",
		Provides: []core.Token{(*authn.Gate)(nil)},
		Stage:    speedhttp.StageIdentify,
		Handler: func(c *gin.Context) {
			c.Request = c.Request.WithContext(log.WithAttrs(c.Request.Context(), slog.String("subject", "alice")))
			c.Next()
		},
	}
}

// witness is a layer that notes what a request looks like on the inside of the
// resolution layer. It points at the capability the resolution layer claims and
// is in the same stage, so it sits inside the resolution layer only because of
// the After it declares.
func (p *probe) witness() speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:  "witness",
		Stage: speedhttp.StageIdentify,
		After: []core.Token{(*tenant.Resolution)(nil)},
		Handler: func(c *gin.Context) {
			id, ok := tenant.FromContext(c.Request.Context())
			p.mu.Lock()
			p.seen = append(p.seen, sighting{
				path: c.Request.URL.Path, fullPath: c.FullPath(), id: id, tenanted: ok,
			})
			p.mu.Unlock()
			c.Next()
		},
	}
}

// orders is the route: it notes the tenant it read, writes its record and
// answers 204.
func (p *probe) orders(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := tenant.FromContext(ctx)
	p.mu.Lock()
	p.handled = append(p.handled, id)
	p.mu.Unlock()
	if p.write != nil {
		p.write(ctx)
	} else {
		log.FromContext(ctx).Info("handled")
	}
	c.Status(nethttp.StatusNoContent)
}

func (p *probe) handledIDs() []tenant.ID {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]tenant.ID(nil), p.handled...)
}

func (p *probe) sightings() []sighting {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]sighting(nil), p.seen...)
}

// requireNothingInside asserts no request got past the resolution layer: neither
// the layer just inside it nor the route saw one.
func (p *probe) requireNothingInside(t *testing.T) {
	t.Helper()
	if got := p.sightings(); len(got) != 0 {
		t.Fatalf("a refused request reached the layer inside the resolution layer: %+v", got)
	}
	if got := p.handledIDs(); len(got) != 0 {
		t.Fatalf("a refused request reached the handler: %v", got)
	}
}

// capturingResponder is an ErrorResponder that keeps the Problems it is asked to
// write, and writes a shape of its own: a media type and a body the default
// shape never produces.
type capturingResponder struct {
	mu       sync.Mutex
	problems []speedhttp.Problem
}

const customMediaType = "application/x.test-error+json"

func (r *capturingResponder) RespondError(w nethttp.ResponseWriter, _ *nethttp.Request, p speedhttp.Problem) {
	r.mu.Lock()
	r.problems = append(r.problems, p)
	r.mu.Unlock()
	w.Header().Set("Content-Type", customMediaType)
	w.WriteHeader(p.Status)
	_, _ = w.Write([]byte(`{"code":` + strconv.Itoa(p.Status) + `}`))
}

func (r *capturingResponder) handed() []speedhttp.Problem {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]speedhttp.Problem(nil), r.problems...)
}

// sameAnswer reports whether two responses are indistinguishable to a client:
// the status, the body and every header the server does not stamp with the time.
func sameAnswer(a, b response) bool {
	if a.status != b.status || a.body != b.body || len(a.header) != len(b.header) {
		return false
	}
	for k, av := range a.header {
		if k == "Date" {
			continue
		}
		bv := b.header[k]
		if len(av) != len(bv) {
			return false
		}
		for i := range av {
			if av[i] != bv[i] {
				return false
			}
		}
	}
	return true
}

func TestARequestWithNoTenantNameIsNotFound(t *testing.T) {
	p := &probe{}
	h := startHost(t, hostOptions{roster: standardRoster(), register: p.register})

	requireProblem(t, h.get(t, "/orders", ""), nethttp.StatusNotFound)
	p.requireNothingInside(t)
}

func TestANameOffTheRosterAndADisabledTenantAnswerAlike(t *testing.T) {
	p := &probe{}
	h := startHost(t, hostOptions{roster: standardRoster(), register: p.register})

	unknown := h.get(t, "/orders", "ghost")
	disabled := h.get(t, "/orders", "paused")
	nameless := h.get(t, "/orders", "")
	for _, got := range []response{unknown, disabled, nameless} {
		requireProblem(t, got, nethttp.StatusNotFound)
	}
	if !sameAnswer(unknown, disabled) {
		t.Fatalf("an unknown and a disabled tenant were answered differently: %+v and %+v", unknown, disabled)
	}
	if !sameAnswer(unknown, nameless) {
		t.Fatalf("a request with no tenant name was answered differently from an unknown tenant: %+v and %+v",
			nameless, unknown)
	}
	p.requireNothingInside(t)
}

// A deleted tenant is answered exactly as an unknown one. The request face must
// not say whether a name ever existed, and deletion is one more state that
// would leak it if it were answered differently: a caller could tell a tenant
// that was deleted from a name that was never registered and time an attack on
// the alias.
func TestADeletedTenantAnswersLikeAnUnknownOne(t *testing.T) {
	p := &probe{}
	h := startHost(t, hostOptions{roster: standardRoster(), register: p.register})

	unknown := h.get(t, "/orders", "ghost")
	deleted := h.get(t, "/orders", "deleted")
	requireProblem(t, deleted, nethttp.StatusNotFound)
	if !sameAnswer(unknown, deleted) {
		t.Fatalf("a deleted tenant was answered differently from an unknown one: %+v and %+v",
			deleted, unknown)
	}
	p.requireNothingInside(t)
}

func TestAnUnreadableRosterIsAnInternalError(t *testing.T) {
	p := &probe{}
	h := startHost(t, hostOptions{roster: fakeRoster{err: tenant.ErrRosterMissing}, register: p.register})

	got := h.get(t, "/orders", "acme")
	requireProblem(t, got, nethttp.StatusInternalServerError)
	if strings.Contains(strings.ToLower(got.body), "roster") {
		t.Fatalf("the cause of a 500 reached the client: %q", got.body)
	}
	p.requireNothingInside(t)
}

// An admitted request carries the identifier, not the alias, both in the
// context and on the logger the handler writes through, beside what the layers
// outside it bound.
func TestAnAdmittedRequestCarriesTheIdentifier(t *testing.T) {
	p := &probe{}
	h := startHost(t, hostOptions{roster: standardRoster(), register: p.register})

	got := h.get(t, "/orders", "acme")
	if got.status != nethttp.StatusNoContent {
		t.Fatalf("an active tenant was answered %d, want the handler's 204", got.status)
	}
	if ids := p.handledIDs(); len(ids) != 1 || ids[0] != "t_acme" {
		t.Fatalf("the handler read tenants %v, want the identifier t_acme once", ids)
	}
	if seen := p.sightings(); len(seen) != 1 || seen[0].id != "t_acme" || !seen[0].tenanted {
		t.Fatalf("the layer inside the resolution layer saw %+v, want the identifier t_acme", seen)
	}
	records := h.logs.withMessage("handled")
	if len(records) != 1 {
		t.Fatalf("the handler's record was written %d times, want once: %+v", len(records), records)
	}
	if got := records[0].attrs[tenantctx.LogField]; got != "t_acme" {
		t.Fatalf("the handler's record carries %s=%q, want t_acme", tenantctx.LogField, got)
	}
	if got := records[0].attrs["subject"]; got != "alice" {
		t.Fatalf("the handler's record lost the subject bound outside, got %q", got)
	}
	if got := records[0].attrs["logger"]; got != speedhttp.ModuleName {
		t.Fatalf("the handler's record went through the logger %q, want the entry point's request logger", got)
	}
}

// A request that announces the tenant's identifier names no tenant: the
// identifier is internal, and only the alias is a name the request surface
// carries.
func TestAnIdentifierNamesNoTenant(t *testing.T) {
	p := &probe{}
	h := startHost(t, hostOptions{roster: standardRoster(), register: p.register})

	got := h.get(t, "/orders", "t_acme")
	if got.status != nethttp.StatusNotFound {
		t.Fatalf("a tenant's identifier was answered %d, want the resolution layer's 404", got.status)
	}
	if ids := p.handledIDs(); len(ids) != 0 {
		t.Fatalf("the handler read tenants %v, want none", ids)
	}
}

// A handler that crosses tenants from its request's context gives up the
// tenant on the logger too: a record about work done across tenants does not
// name the tenant the request belonged to, because that work does not belong
// to it. What the layers outside bound stays.
func TestCrossingFromAnAdmittedRequestTakesTheTenantOffTheLogger(t *testing.T) {
	p := &probe{write: func(ctx context.Context) {
		log.FromContext(tenant.CrossTenant(ctx)).Info("crossed")
	}}
	h := startHost(t, hostOptions{roster: standardRoster(), register: p.register})

	if got := h.get(t, "/orders", "acme"); got.status != nethttp.StatusNoContent {
		t.Fatalf("an active tenant was answered %d, want the handler's 204", got.status)
	}
	records := h.logs.withMessage("crossed")
	if len(records) != 1 {
		t.Fatalf("the crossing record was written %d times, want once: %+v", len(records), records)
	}
	if got, ok := records[0].attrs[tenantctx.LogField]; ok {
		t.Fatalf("a record written across tenants carries %s=%q, want no tenant", tenantctx.LogField, got)
	}
	if got := records[0].attrs["subject"]; got != "alice" {
		t.Fatalf("the crossing record lost the subject bound outside, got %q", got)
	}
}

// A refusal is a record about one request, so it is written through the
// request's logger: it carries what the layers outside bound — the subject the
// authentication layer settled, the trace the tracing layer opened — which is
// what ties an authenticated caller probing tenant names to the probes. The
// module's own logger carries none of that.
func TestARefusalIsLoggedOnTheRequestsLogger(t *testing.T) {
	cases := []struct {
		name    string
		roster  fakeRoster
		header  string
		message string
		level   slog.Level
		reason  string
	}{
		{"no name", standardRoster(), "", "request refused", slog.LevelInfo, "the request carries no tenant name"},
		{"off the roster", standardRoster(), "ghost", "request refused", slog.LevelInfo, "the name is not on the roster"},
		{"disabled", standardRoster(), "paused", "request refused", slog.LevelInfo, "the tenant is not being served"},
		{"unreadable roster", fakeRoster{err: tenant.ErrRosterMissing}, "acme", "the roster could not be read", slog.LevelError, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &probe{}
			h := startHost(t, hostOptions{roster: c.roster, register: p.register})
			h.get(t, "/orders", c.header)

			records := h.logs.withMessage(diagnosticPrefix + ModuleName + ": " + c.message)
			if len(records) != 1 {
				t.Fatalf("the layer wrote %d records of the refusal, want one: %+v", len(records), records)
			}
			got := records[0]
			if got.level != c.level || got.attrs["subject"] != "alice" {
				t.Fatalf("the refusal was recorded as %+v, want level %v carrying subject alice", got, c.level)
			}
			if got.attrs["logger"] != speedhttp.ModuleName {
				t.Fatalf("the refusal went through the logger %q, want the request's, which is the entry point's",
					got.attrs["logger"])
			}
			if c.reason != "" && got.attrs["reason"] != c.reason {
				t.Fatalf("the refusal gives reason %q, want %q", got.attrs["reason"], c.reason)
			}
			if c.header != "" && c.reason != "" && got.attrs["name"] != c.header {
				t.Fatalf("the refusal names %q, want the name tried, %q", got.attrs["name"], c.header)
			}
		})
	}
}

// A refusal and a failure are written through the entry point's error response
// and carry nothing but their status: the reason is the log's, and a response
// that carried it would tell a name that never existed from one that was
// switched off.
func TestRefusalsAreProblemDetailsResponses(t *testing.T) {
	cases := []struct {
		name   string
		roster fakeRoster
		header string
		status int
	}{
		{"no name", standardRoster(), "", nethttp.StatusNotFound},
		{"off the roster", standardRoster(), "ghost", nethttp.StatusNotFound},
		{"disabled", standardRoster(), "paused", nethttp.StatusNotFound},
		{"unreadable roster", fakeRoster{err: tenant.ErrRosterMissing}, "acme", nethttp.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := startHost(t, hostOptions{roster: c.roster, register: (&probe{}).register})
			requireProblem(t, h.get(t, "/orders", c.header), c.status)
		})
	}
}

// When a module delivers the ErrorResponder capability the shape is its own, for
// every answer this layer gives, and what the layer hands it is the status and
// nothing else.
func TestADeliveredErrorResponderDecidesTheShape(t *testing.T) {
	cases := []struct {
		name   string
		roster fakeRoster
		header string
		status int
	}{
		{"no name", standardRoster(), "", nethttp.StatusNotFound},
		{"off the roster", standardRoster(), "ghost", nethttp.StatusNotFound},
		{"disabled", standardRoster(), "paused", nethttp.StatusNotFound},
		{"unreadable roster", fakeRoster{err: tenant.ErrRosterMissing}, "acme", nethttp.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			responder := &capturingResponder{}
			h := startHost(t, hostOptions{roster: c.roster, responder: responder, register: (&probe{}).register})

			got := h.get(t, "/orders", c.header)
			if got.status != c.status {
				t.Fatalf("the response is %d, want %d", got.status, c.status)
			}
			if ct := got.header.Get("Content-Type"); ct != customMediaType {
				t.Fatalf("the response's media type is %q, want the responder's %q", ct, customMediaType)
			}
			if want := `{"code":` + strconv.Itoa(c.status) + `}`; got.body != want {
				t.Fatalf("the response body is %q, want the responder's %q", got.body, want)
			}
			handed := responder.handed()
			if len(handed) != 1 || handed[0].Status != c.status {
				t.Fatalf("the responder was handed %+v, want one Problem carrying the status %d alone",
					handed, c.status)
			}
		})
	}
}

// The layer's place on the chain comes from what it declares within its stage:
// it stands for the resolution capability and sits inside the layer that stands
// for authentication, which is the After it declares. The witness declares
// itself inside the resolution layer, so the chain listing reads subject, then
// the resolution layer, then the witness.
func TestTheLayerSitsInsideAuthenticationAndOutsideWhatPointsAtIt(t *testing.T) {
	h := startHost(t, hostOptions{roster: standardRoster(), register: (&probe{}).register})

	listed := h.logs.withMessage("an HTTP endpoint's chain is assembled")
	if len(listed) != 1 {
		t.Fatalf("the entry point listed %d chains, want one: %+v", len(listed), listed)
	}
	chain := listed[0].attrs["chain"]
	subject := strings.Index(chain, "subject")
	resolution := strings.Index(chain, ModuleName)
	witness := strings.Index(chain, "witness")
	if subject < 0 || resolution < 0 || witness < 0 || subject >= resolution || resolution >= witness {
		t.Fatalf("the chain is %q, want subject, then %s, then witness", chain, ModuleName)
	}
}

func TestTheDescriptorOfTheLayerStandsForResolutionInsideAuthentication(t *testing.T) {
	mw := newResolver(settings{header: "X-Tenant"}, nil).middlewareFor()

	if mw.Handler == nil {
		t.Fatal("the layer has no handler")
	}
	if len(mw.Provides) != 1 || mw.Provides[0] != core.Token((*tenant.Resolution)(nil)) {
		t.Fatalf("Provides = %v, want the Resolution token", mw.Provides)
	}
	if len(mw.After) != 1 || mw.After[0] != core.Token((*authn.Gate)(nil)) {
		t.Fatalf("After = %v, want the authentication gate's token", mw.After)
	}
	if mw.Name != ModuleName {
		t.Fatalf("Name = %q, want %q", mw.Name, ModuleName)
	}
}
