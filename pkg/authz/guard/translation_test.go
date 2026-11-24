package guard

import (
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/authztest"
	"github.com/vislake/speed/pkg/authz/internal/translation"
	"github.com/vislake/speed/pkg/core"
)

const declaredPermission = authz.Permission("crm.deal.read")

func declaredModule(name string, rows map[authz.Permission]authz.Translation) core.Module {
	keys := make([]authz.Permission, 0, len(rows))
	for p := range rows {
		keys = append(keys, p)
	}
	slices.Sort(keys)
	return core.Module{
		Name: name,
		Resources: []any{
			authz.PermissionSpec{Permissions: keys},
			authz.TranslationSpec{Rows: rows},
		},
	}
}

func resolveDeclared(t *testing.T, rows map[authz.Permission]authz.Translation) {
	t.Helper()
	reg := core.New()
	reg.Register(declaredModule("stand-in.business", rows))
	if err := translation.Resolve(reg); err != nil {
		t.Fatalf("resolving the translation: %v", err)
	}
	t.Cleanup(translation.Reset)
}

// TestAModuleDeclaredPermissionIsAccepted checks that a module declared permission is accepted.
func TestAModuleDeclaredPermissionIsAccepted(t *testing.T) {
	if _, _, ok := translation.Lookup(declaredPermission); ok {
		t.Fatal("the built-in translation already holds the module's permission, so the case cannot show the row being added")
	}
	unresolved := newGate(authztest.NewCatalog(declaredPermission))
	unresolved.Require(http.MethodGet, "/deals", declaredPermission)
	if err := unresolved.verify(); !errors.Is(err, authz.ErrUnknownPermission) {
		t.Fatalf("verify over the built-in translation = %v, want ErrUnknownPermission: the row was not the difference", err)
	}

	resolveDeclared(t, map[authz.Permission]authz.Translation{
		declaredPermission: {Object: "deal", Action: "read"},
	})

	object, action, ok := translation.Lookup(declaredPermission)
	if !ok || object != "deal" || action != "read" {
		t.Fatalf("Lookup(%q) = (%q, %q, %t), want (deal, read, true)", declaredPermission, object, action, ok)
	}

	gate := newGate(authztest.NewCatalog(declaredPermission))
	gate.Require(http.MethodGet, "/deals", declaredPermission)
	if err := gate.verify(); err != nil {
		t.Fatalf("verify over the resolved translation = %v, want nil for the module's declared row", err)
	}
}

// TestAPermissionOutsideTheUnionIsRefused checks that a permission outside the union is refused.
func TestAPermissionOutsideTheUnionIsRefused(t *testing.T) {
	resolveDeclared(t, map[authz.Permission]authz.Translation{
		declaredPermission: {Object: "deal", Action: "read"},
	})

	untranslatable := authz.Permission("crm.unknown.read")
	if _, _, ok := translation.Lookup(untranslatable); ok {
		t.Fatalf("Lookup(%q) found a row the union should not hold", untranslatable)
	}
	gate := newGate(authztest.NewCatalog(untranslatable))
	gate.Require(http.MethodGet, "/x", untranslatable)
	if err := gate.verify(); !errors.Is(err, authz.ErrUnknownPermission) {
		t.Fatalf("verify = %v, want ErrUnknownPermission for a permission outside the union", err)
	}

	enforcer := &stubEnforcer{}
	enforcer.answer = func(authz.Query) (bool, error) { return true, nil }
	p := &product{enforcer: enforcer}
	allow, err := p.Decide(t.Context(), authn.Decision{
		Action: authn.Action(untranslatable),
		Actor:  identity(t, "alice"),
		Tenant: "acme",
	})
	if err != nil || allow {
		t.Fatalf("Decide = (%t, %v), want (false, nil) for an action outside the union", allow, err)
	}
	if n := len(enforcer.queries()); n != 0 {
		t.Fatalf("the engine was asked %d times, want none for an action outside the union", n)
	}
}

// TestTheMiddlewareTranslatesAModuleDeclaredPermission checks that the middleware translates a module declared permission.
func TestTheMiddlewareTranslatesAModuleDeclaredPermission(t *testing.T) {
	resolveDeclared(t, map[authz.Permission]authz.Translation{
		declaredPermission: {Object: "deal", Action: "read"},
	})

	r := newRig(t)
	r.enforcer.answer = func(authz.Query) (bool, error) { return true, nil }
	rp := r.orders(caller(t), func(g *gate) {
		g.Require(http.MethodGet, "/orders", declaredPermission)
	})
	if !rp.reached() {
		t.Fatalf("the request did not reach the handler: %+v", rp)
	}
	want := authz.Query{Subject: "alice", Domain: authz.PlatformDomain, Object: "deal", Action: "read"}
	if got := r.enforcer.queries(); len(got) != 1 || got[0] != want {
		t.Fatalf("the engine was asked %v, want exactly %v from the module's declared row", got, want)
	}
}
