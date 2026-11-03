package tenantadmin

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/vislake/speed/pkg/settings"
)

// TestTheTenantSettingsRoutesAreMountedAndAnswer holds the route set as a whole:
// every one of the four routes is mounted and answers as its operation declares,
// with the layer fixed to the tenant in the path.
func TestTheTenantSettingsRoutesAreMountedAndAnswer(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}

	if got := len(r.p.routes()); got != 4 {
		t.Fatalf("the module mounts %d routes, want 4", got)
	}

	if w := r.do(http.MethodGet, base, nil); w.Code != http.StatusOK {
		t.Fatalf("the list ended with %d, want 200: %s", w.Code, w.Body.String())
	}
	if w := r.do(http.MethodGet, base+"/app", nil); w.Code != http.StatusOK {
		t.Fatalf("the namespace read ended with %d, want 200: %s", w.Code, w.Body.String())
	}
	if w := r.do(http.MethodPut, base+"/app/mode", map[string]any{"value": "manual"}); w.Code != http.StatusNoContent {
		t.Fatalf("the write ended with %d, want 204: %s", w.Code, w.Body.String())
	}
	if w := r.do(http.MethodDelete, base+"/app/mode", nil); w.Code != http.StatusNoContent {
		t.Fatalf("the delete ended with %d, want 204: %s", w.Code, w.Body.String())
	}
}
