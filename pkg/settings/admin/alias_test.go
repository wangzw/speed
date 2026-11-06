package admin

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/settings"
)

// directoryStub answers the identifier each mapped alias names.
type directoryStub map[string]string

func (d directoryStub) TenantIDOf(_ context.Context, alias string) (string, bool, error) {
	id, ok := d[alias]
	return id, ok, nil
}

// TestTheTenantLayerIsKeyedByIdentifierAndNamedByAlias holds the two names
// apart: the query string names the tenant by alias, the layer and the policy
// domain are keyed by the identifier it resolves to, and the audit detail
// carries the alias a reader can recognise.
func TestTheTenantLayerIsKeyedByIdentifierAndNamedByAlias(t *testing.T) {
	r := newRig(t)
	r.p.directory = directoryStub{"acme": "t-acme"}
	r.store.declared = []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}
	w := r.do(http.MethodPut, "/api/v1/system/settings/app/mode?layer=tenant&tenant=acme",
		map[string]any{"value": "manual"}, authntest.WithAccount(authn.AccountID("carol")))
	if w.Code != http.StatusNoContent {
		t.Fatalf("the request ended with %d: %s", w.Code, w.Body.String())
	}
	var set *storeCall
	for _, c := range r.store.recorded() {
		if c.op == "Set" {
			set = &c
		}
	}
	if set == nil {
		t.Fatal("the settings module was never asked to write")
	}
	if set.layer != settings.ForTenant("t-acme") {
		t.Errorf("the write named layer %v, want the layer of the identifier t-acme", set.layer)
	}
	if got := r.guard.domainsAsked(); len(got) != 1 || got[0] != authz.Domain("t-acme") {
		t.Errorf("the decision was asked in %v, want the identifier's domain", got)
	}
	rows := r.audit.entries()
	if len(rows) != 1 {
		t.Fatalf("the write wrote %d audit rows, want one mandatory row", len(rows))
	}
	if rows[0].Detail[detailTenant] != "acme" {
		t.Errorf("the mandatory row names the tenant %q, want the alias acme", rows[0].Detail[detailTenant])
	}
}

// TestATenantAliasNoRosterCarriesIsA400 holds the unknown-name answer: the
// directory knows no such tenant, so there is no layer to act on and nothing is
// read or written.
func TestATenantAliasNoRosterCarriesIsA400(t *testing.T) {
	r := newRig(t)
	r.p.directory = directoryStub{"acme": "t-acme"}
	w := r.do(http.MethodGet, "/api/v1/system/settings?layer=tenant&tenant=ghost", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("the request ended with %d, want 400: %s", w.Code, w.Body.String())
	}
	if got := r.guard.askedFor(); len(got) != 0 {
		t.Errorf("the guard was asked %v, want nothing", got)
	}
}

// TestATenantLayerWithNoRosterIsRefused holds the assembly with no tenant
// directory: the query carries an alias and there is nothing to resolve it
// with, so the surface refuses rather than reading the value as an identifier.
// The global layer is untouched by the absence.
func TestATenantLayerWithNoRosterIsRefused(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}
	r.p.directory = nil
	w := r.do(http.MethodGet, "/api/v1/system/settings?layer=tenant&tenant=acme", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("the request ended with %d, want 400: %s", w.Code, w.Body.String())
	}
	if got := r.guard.askedFor(); len(got) != 0 {
		t.Errorf("the guard was asked %v, want nothing", got)
	}
	if got := r.store.ops(); len(got) != 0 {
		t.Errorf("the settings surface was reached %v, want nothing", got)
	}
}
