package resolve

import (
	"context"
	"log/slog"
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/tenant"
)

// panickingExtractor is an extractor with a defect.
type panickingExtractor struct{}

func (panickingExtractor) Extract(*nethttp.Request) (string, bool) { panic("extractor defect") }

// The message the entry point's fallback writes for a panic that reached it.
const fallbackCaught = "a request panicked and the endpoint's recovery layer caught it"

// The message this layer writes for a panic of its own.
const layerPanicked = diagnosticPrefix + ModuleName + ": the resolution layer panicked while resolving this request"

// A panic inside the resolution layer is this module's defect, answered with a
// 500 and attributed to it here; left alone it would reach the entry point's
// fallback, which does not tell one layer's panic from another's. The extractor
// is registered through the module's own seam, the way a third party's is.
func TestAPanicInTheLayerIsAnInternalErrorOfTheLayer(t *testing.T) {
	p := &probe{extractors: map[string]tenant.Extractor{"panicking": panickingExtractor{}}}
	h := startHost(t, hostOptions{roster: standardRoster(), extractor: "panicking", register: p.register})

	got := h.get(t, "/orders", "acme")

	requireProblem(t, got, nethttp.StatusInternalServerError)
	if strings.Contains(got.body, "extractor defect") {
		t.Fatalf("the panic value reached the client: %q", got.body)
	}
	p.requireNothingInside(t)
	records := h.logs.withMessage(layerPanicked)
	if len(records) != 1 || records[0].level != slog.LevelError ||
		records[0].attrs["panic"] != "extractor defect" || records[0].attrs["subject"] != "alice" {
		t.Fatalf("the request's logger holds %+v, want one error record of the panic carrying the subject", records)
	}
	if caught := h.logs.withMessage(fallbackCaught); len(caught) != 0 {
		t.Fatalf("the layer's own panic reached the entry point's fallback: %+v", caught)
	}
}

// The same answer in the shape a delivered ErrorResponder decides, handed the
// status alone: the panic value is in the log and in no Problem.
func TestAPanicInTheLayerIsAnsweredInTheShapeAResponderDecides(t *testing.T) {
	responder := &capturingResponder{}
	p := &probe{extractors: map[string]tenant.Extractor{"panicking": panickingExtractor{}}}
	h := startHost(t, hostOptions{
		roster: standardRoster(), extractor: "panicking", responder: responder, register: p.register,
	})

	got := h.get(t, "/orders", "acme")

	if got.status != nethttp.StatusInternalServerError || got.header.Get("Content-Type") != customMediaType {
		t.Fatalf("the layer's panic was answered %d in %q, want 500 in the responder's shape",
			got.status, got.header.Get("Content-Type"))
	}
	handed := responder.handed()
	if len(handed) != 1 || handed[0].Status != nethttp.StatusInternalServerError {
		t.Fatalf("the responder was handed %+v, want one Problem carrying the status 500 alone", handed)
	}
}

// The handler's own panic is not this layer's to catch: recovering it here
// would change what every route behind the layer does on a panic. It reaches the
// entry point's fallback, whose record says so, and the layer records nothing of
// it.
func TestAPanicInTheHandlerIsNotCaughtByTheLayer(t *testing.T) {
	p := &probe{write: func(context.Context) { panic("handler defect") }}
	h := startHost(t, hostOptions{roster: standardRoster(), register: p.register})

	got := h.get(t, "/orders", "acme")

	requireProblem(t, got, nethttp.StatusInternalServerError)
	caught := h.logs.withMessage(fallbackCaught)
	if len(caught) != 1 || caught[0].attrs["panic"] != "handler defect" {
		t.Fatalf("the entry point's fallback recorded %+v, want one record of the handler's panic", caught)
	}
	if recovered := h.logs.withMessage(layerPanicked); len(recovered) != 0 {
		t.Fatalf("the layer recovered a panic that was the handler's: %+v", recovered)
	}
}
