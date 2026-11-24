package resolve

import (
	"log/slog"
	nethttp "net/http"
	"strings"
	"testing"
)

func requireNoErrorRecord(t *testing.T, h *host) {
	t.Helper()
	for _, rec := range h.logs.all() {
		if rec.level >= slog.LevelError {
			t.Fatalf("an error record was written: %+v", rec)
		}
	}
}

// TestARequestMatchingNoRouteIsResolvedWithoutOne checks that a request matching no route is resolved without one and the 404 that follows is the entry point's.
func TestARequestMatchingNoRouteIsResolvedWithoutOne(t *testing.T) {
	p := &probe{}
	h := startHost(t, hostOptions{roster: standardRoster(), register: p.register})

	got := h.get(t, "/nowhere", "acme")

	requireProblem(t, got, nethttp.StatusNotFound)
	want := []sighting{{path: "/nowhere", fullPath: "", id: "t_acme", tenanted: true}}
	if seen := p.sightings(); len(seen) != 1 || seen[0] != want[0] {
		t.Fatalf("the layer inside the resolution layer saw %+v, want %+v", seen, want)
	}
	if ids := p.handledIDs(); len(ids) != 0 {
		t.Fatalf("a request that matches no route reached the route's handler: %v", ids)
	}
	if refusals := h.logs.withMessage(diagnosticPrefix + ModuleName + ": request refused"); len(refusals) != 0 {
		t.Fatalf("the layer refused a request that carries an active tenant: %+v", refusals)
	}
	requireNoErrorRecord(t, h)
}

// TestARequestWhoseMethodIsNotAllowedIsResolvedWithoutARoute checks that a request whose method the path does not allow is resolved and then answered 405 by the entry point.
func TestARequestWhoseMethodIsNotAllowedIsResolvedWithoutARoute(t *testing.T) {
	p := &probe{}
	h := startHost(t, hostOptions{roster: standardRoster(), register: p.register})

	got := h.send(t, nethttp.MethodPost, "/orders", map[string]string{"X-Tenant": "acme"})

	requireProblem(t, got, nethttp.StatusMethodNotAllowed)
	if allow := got.header.Get("Allow"); !strings.Contains(allow, nethttp.MethodGet) {
		t.Fatalf("the 405 carries Allow %q, want it to list GET", allow)
	}
	want := sighting{path: "/orders", fullPath: "", id: "t_acme", tenanted: true}
	if seen := p.sightings(); len(seen) != 1 || seen[0] != want {
		t.Fatalf("the layer inside the resolution layer saw %+v, want %+v", seen, want)
	}
	requireNoErrorRecord(t, h)
}

// TestRequestsWithoutATenantAreRefusedHereWhateverTheyAskFor checks that a request with no tenant is refused with 404 whatever it asks for, so a disallowed method never gets the entry point's 405 with an Allow header.
func TestRequestsWithoutATenantAreRefusedHereWhateverTheyAskFor(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		header map[string]string
		reason string
	}{
		{"no route, no name", nethttp.MethodGet, "/nowhere", nil, "the request carries no tenant name"},
		{"no route, unknown name", nethttp.MethodGet, "/nowhere", map[string]string{"X-Tenant": "ghost"}, "the name is not on the roster"},
		{"no route, disabled tenant", nethttp.MethodGet, "/nowhere", map[string]string{"X-Tenant": "paused"}, "the tenant is not being served"},
		{"method not allowed, no name", nethttp.MethodPost, "/orders", nil, "the request carries no tenant name"},
		{"method not allowed, unknown name", nethttp.MethodPost, "/orders", map[string]string{"X-Tenant": "ghost"}, "the name is not on the roster"},
	}
	p := &probe{}
	h := startHost(t, hostOptions{roster: standardRoster(), register: p.register})

	refused := diagnosticPrefix + ModuleName + ": request refused"
	for i, c := range cases {
		got := h.send(t, c.method, c.path, c.header)

		requireProblem(t, got, nethttp.StatusNotFound)
		if allow := got.header.Get("Allow"); allow != "" {
			t.Errorf("%s: the refusal carries Allow %q, which says the path exists", c.name, allow)
		}
		records := h.logs.withMessage(refused)
		if len(records) != i+1 {
			t.Fatalf("%s: the layer has written %d refusals, want %d", c.name, len(records), i+1)
		}
		if reason := records[i].attrs["reason"]; reason != c.reason {
			t.Errorf("%s: the layer recorded reason %q, want %q", c.name, reason, c.reason)
		}
	}
	p.requireNothingInside(t)
	requireNoErrorRecord(t, h)
}

// TestTheAnswersForUnmatchedRequestsShareTheLayersShape checks that answers for unmatched requests take the shape a delivered ErrorResponder decides, like the layer's own refusals.
func TestTheAnswersForUnmatchedRequestsShareTheLayersShape(t *testing.T) {
	responder := &capturingResponder{}
	p := &probe{}
	h := startHost(t, hostOptions{roster: standardRoster(), responder: responder, register: p.register})

	entryPoint := h.get(t, "/nowhere", "acme")
	layer := h.get(t, "/nowhere", "")

	for name, got := range map[string]response{"the entry point's 404": entryPoint, "the layer's 404": layer} {
		if got.status != nethttp.StatusNotFound || got.header.Get("Content-Type") != customMediaType {
			t.Fatalf("%s is %d in %q, want 404 in the responder's shape %q",
				name, got.status, got.header.Get("Content-Type"), customMediaType)
		}
	}
	if !sameAnswer(entryPoint, layer) {
		t.Fatalf("the entry point's 404 and the layer's differ: %+v and %+v", entryPoint, layer)
	}
}
