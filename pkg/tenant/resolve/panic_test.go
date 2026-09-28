package resolve

import (
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
)

// panickingExtractor is an extractor with a defect.
type panickingExtractor struct{}

func (panickingExtractor) Extract(*nethttp.Request) (string, bool) { panic("extractor defect") }

// servePanicking serves one request and reports a panic that left the layer.
func servePanicking(h harness, header string) (w *httptest.ResponseRecorder, escaped any) {
	defer func() { escaped = recover() }()
	return h.serve(header), nil
}

// A panic inside the resolution layer is this module's defect, answered with a
// 500 and attributed to it here; left alone it would reach the entry point,
// which does not tell one layer's panic from another's.
func TestAPanicInTheLayerIsAnInternalErrorOfTheLayer(t *testing.T) {
	h := newHarness(t, standardRoster(), panickingExtractor{})
	w, escaped := servePanicking(h, "acme")
	if escaped != nil {
		t.Fatalf("the layer's own panic left it: %v", escaped)
	}
	if w.Code != nethttp.StatusInternalServerError || *h.reached {
		t.Fatalf("the layer answered %d (handler reached: %v), want 500 without the handler",
			w.Code, *h.reached)
	}
	records := h.request.all()
	if len(records) != 1 || records[0].level != slog.LevelError ||
		records[0].attrs["panic"] != "extractor defect" || records[0].attrs["subject"] != "alice" {
		t.Fatalf("the request's logger holds %+v, want one error record of the panic", records)
	}
}

// The handler's own panic is not this layer's to catch: recovering it here
// would change what every route behind the layer does on a panic.
func TestAPanicInTheHandlerIsNotCaughtByTheLayer(t *testing.T) {
	r := newResolver(settings{header: "X-Tenant"}, standardRoster())
	r.selected = headerExtractor{name: "X-Tenant"}
	layer := r.middlewareFor().Wrap(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {
		panic("handler defect")
	}))
	req := httptest.NewRequest(nethttp.MethodGet, "/orders", nil)
	req.Header.Set("X-Tenant", "acme")
	escaped := func() (escaped any) {
		defer func() { escaped = recover() }()
		layer.ServeHTTP(httptest.NewRecorder(), req)
		return nil
	}()
	if escaped != "handler defect" {
		t.Fatalf("the handler's panic surfaced as %v, want it to leave the layer untouched", escaped)
	}
}
