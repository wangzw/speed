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

// The process's translation is resolved by the policy module, which these cases
// drive directly: they hold what this module's own faces make of a module's
// declared row — the route declaration check accepts it, the middleware decides
// on it, and the decider refuses what no row covers. What the resolution itself
// makes of a declared row is the translation package's business and is held
// there.

// declaredPermission is a permission of a module's own, beyond the ones this
// capability carries.
const declaredPermission = authz.Permission("crm.deal.read")

// declaredModule builds a stand-in module that declares the given permission
// rows of its own: the permission spec listing them and the translation spec
// beside it, the way a business module carries both.
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

// resolveDeclared installs the process's translation set over one stand-in
// module that declares the given rows. The built-in-only set is restored when
// the case ends, so one case's rows do not reach the next.
func resolveDeclared(t *testing.T, rows map[authz.Permission]authz.Translation) {
	t.Helper()
	reg := core.New()
	reg.Register(declaredModule("stand-in.business", rows))
	if err := translation.Resolve(reg); err != nil {
		t.Fatalf("resolving the translation: %v", err)
	}
	t.Cleanup(translation.Reset)
}

// A module's declared row reaches the declaration check: a route that names
// the module's permission passes it rather than failing the startup as a
// permission the translation cannot express.
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

// A permission the union has no row for is refused with the union in place: the
// declaration check refuses a route that names it, and the decider refuses an
// action it cannot express without an error and without asking the engine.
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

// The middleware builds its query from the union: a route that declares a
// module's own permission is decided on the (object, action) the module
// declared, with the account's key and the request's domain.
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
