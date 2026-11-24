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

type sighting struct {
	path     string
	fullPath string
	id       tenant.ID
	tenanted bool
}

type probe struct {
	write      func(ctx context.Context)
	extractors map[string]tenant.Extractor

	mu      sync.Mutex
	handled []tenant.ID
	seen    []sighting
}

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

func (p *probe) requireNothingInside(t *testing.T) {
	t.Helper()
	if got := p.sightings(); len(got) != 0 {
		t.Fatalf("a refused request reached the layer inside the resolution layer: %+v", got)
	}
	if got := p.handledIDs(); len(got) != 0 {
		t.Fatalf("a refused request reached the handler: %v", got)
	}
}

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

// TestARequestWithNoTenantNameIsNotFound checks that a request carrying no tenant name is answered with a bare 404.
func TestARequestWithNoTenantNameIsNotFound(t *testing.T) {
	p := &probe{}
	h := startHost(t, hostOptions{roster: standardRoster(), register: p.register})

	requireProblem(t, h.get(t, "/orders", ""), nethttp.StatusNotFound)
	p.requireNothingInside(t)
}

// TestANameOffTheRosterAndADisabledTenantAnswerAlike checks that an unknown name, a disabled tenant and a missing name get the same answer.
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

// TestADeletedTenantAnswersLikeAnUnknownOne checks that a deleted tenant is answered exactly as an unknown name.
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

// TestAnUnreadableRosterIsAnInternalError checks that a roster that cannot be read is a 500 whose cause stays out of the response.
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

// TestAnAdmittedRequestCarriesTheIdentifier checks that an admitted request carries the identifier, not the name, in its context and on its logger.
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

// TestAnIdentifierNamesNoTenant checks that a request announcing a tenant's identifier as the name is refused like an unknown name.
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

// TestCrossingFromAnAdmittedRequestTakesTheTenantOffTheLogger checks that crossing tenants from an admitted request's context removes the tenant from its logger and keeps what outer layers bound.
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

// TestARefusalIsLoggedOnTheRequestsLogger checks that a refusal is logged through the request's logger, carrying what the outer layers bound.
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

// TestRefusalsAreProblemDetailsResponses checks that refusals and failures are problem-details responses carrying only their status.
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

// TestADeliveredErrorResponderDecidesTheShape checks that a delivered ErrorResponder decides the shape of every answer and receives the status alone.
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

// TestTheLayerSitsInsideAuthenticationAndOutsideWhatPointsAtIt checks that the chain lists the authentication layer, then the resolution layer, then a layer that points at it.
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

// TestTheDescriptorOfTheLayerStandsForResolutionInsideAuthentication checks that the layer's descriptor provides the Resolution token, comes after the authentication gate and carries a handler.
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
