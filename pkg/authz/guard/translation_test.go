package guard

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/authztest"
	"github.com/vislake/speed/pkg/core"
)

// A module that declares a permission of its own declares how it translates it.
// These cases resolve a process's translation over such a declaration and hold
// that the resolved set carries the module's row beside the built-in ones, that
// the built-in rows are unchanged, that a permission no row covers is still
// refused, and that the middleware decides a route that names the module's
// permission on the pair the module declared.

// declaredPermission is a permission of a module's own, beyond the ones this
// capability carries.
const declaredPermission = authz.Permission("crm.deal.read")

// declaredModule builds a stand-in module that declares the given permission
// rows of its own: the permission spec listing them and the translation spec
// beside it, the way a business module carries both.
func declaredModule(name string, rows map[authz.Permission]authz.Translation) core.Module {
	return core.Module{
		Name: name,
		Resources: []any{
			authz.PermissionSpec{Permissions: sortedKeys(rows)},
			authz.TranslationSpec{Rows: rows},
		},
	}
}

// resolveModules installs the process's translation set over the given stand-in
// modules. The built-in-only set is restored when the case ends, so one case's
// rows do not reach the next.
func resolveModules(t *testing.T, modules ...core.Module) {
	t.Helper()
	reg := core.New()
	for _, m := range modules {
		reg.Register(m)
	}
	if err := resolveTranslation(reg); err != nil {
		t.Fatalf("resolving the translation: %v", err)
	}
	t.Cleanup(func() { processTranslation.Store(nil) })
}

// resolveDeclared installs the process's translation set over one stand-in
// module that declares the given rows — a catalog declaration and the
// translation beside it, the way a business module carries both.
func resolveDeclared(t *testing.T, rows map[authz.Permission]authz.Translation) {
	t.Helper()
	resolveModules(t, declaredModule("stand-in.business", rows))
}

// sortedKeys gives the rows' permissions in order, so the fixture's permission
// spec does not depend on map iteration order.
func sortedKeys(rows map[authz.Permission]authz.Translation) []authz.Permission {
	keys := make([]authz.Permission, 0, len(rows))
	for p := range rows {
		keys = append(keys, p)
	}
	slices.Sort(keys)
	return keys
}

// A module's declared row reaches the process's translation: ObjectAction finds
// the module's permission, and a route that names it passes the declaration
// check rather than failing the startup as a permission the translation cannot
// express. The case first holds the same route refused over the built-in rows
// alone, so the module's row is what makes the difference.
func TestAModuleDeclaredPermissionIsAccepted(t *testing.T) {
	if _, _, ok := ObjectAction(declaredPermission); ok {
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

	object, action, ok := ObjectAction(declaredPermission)
	if !ok || object != "deal" || action != "read" {
		t.Fatalf("ObjectAction(%q) = (%q, %q, %t), want (deal, read, true)", declaredPermission, object, action, ok)
	}

	gate := newGate(authztest.NewCatalog(declaredPermission))
	gate.Require(http.MethodGet, "/deals", declaredPermission)
	if err := gate.verify(); err != nil {
		t.Fatalf("verify over the resolved translation = %v, want nil for the module's declared row", err)
	}
}

// The union does not change a built-in row: every row this capability carries
// still translates to the same (object, action) after a module's row is added.
func TestTheBuiltinRowsSurviveTheUnion(t *testing.T) {
	resolveDeclared(t, map[authz.Permission]authz.Translation{
		declaredPermission: {Object: "deal", Action: "read"},
	})
	for key, want := range translation {
		object, action, ok := ObjectAction(authz.Permission(key))
		if !ok || object != want.object || action != want.action {
			t.Errorf("ObjectAction(%q) = (%q, %q, %t), want (%q, %q, true)",
				key, object, action, ok, want.object, want.action)
		}
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
	if _, _, ok := ObjectAction(untranslatable); ok {
		t.Fatalf("ObjectAction(%q) found a row the union should not hold", untranslatable)
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

// A module may not disagree with a row already in the set, on either axis of the
// pair: a row that names a permission already translated as another pair is a
// startup failure, wrapped around the same sentinel the declaration check uses,
// and the failed resolution installs nothing. The two cases disagree in the
// action alone and in the object alone, so a check that compared one axis and
// not the other is refused.
func TestATranslationConflictFailsResolution(t *testing.T) {
	cases := []struct {
		name     string
		declared authz.Translation
	}{
		{"a disagreement in the action only", authz.Translation{Object: "account", Action: "delete"}},
		{"a disagreement in the object only", authz.Translation{Object: "elsewhere", Action: "read"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := core.New()
			reg.Register(core.Module{
				Name: "stand-in.business",
				Resources: []any{authz.TranslationSpec{Rows: map[authz.Permission]authz.Translation{
					authz.PermissionAccountRead: tc.declared,
				}}},
			})
			err := resolveTranslation(reg)
			if !errors.Is(err, authz.ErrUnknownPermission) {
				t.Fatalf("resolving a disagreeing row = %v, want an error wrapping ErrUnknownPermission", err)
			}
			// The diagnostic names both pairs — the declared one and the one in
			// force — so it says what the module asked for and what it is up
			// against.
			for _, want := range []string{
				strconv.Quote(tc.declared.Object), strconv.Quote(tc.declared.Action),
				strconv.Quote("account"), strconv.Quote("read"),
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the conflict error %q does not name %s", err, want)
				}
			}
			object, action, ok := ObjectAction(authz.PermissionAccountRead)
			if !ok || object != "account" || action != "read" {
				t.Fatalf("ObjectAction(%q) = (%q, %q, %t), want the built-in row (account, read, true): a failed resolution must install nothing",
					authz.PermissionAccountRead, object, action, ok)
			}
		})
	}
}

// Every registered module's rows reach the process's translation, not just the
// first module's: a process that carries two business modules decides on both of
// their declared permissions, so a resolution that stopped at the first module
// would leave the second module's routes untranslatable.
func TestEachModulesRowReachesTheTranslation(t *testing.T) {
	first := authz.Permission("crm.a.read")
	second := authz.Permission("crm.b.write")
	resolveModules(t,
		declaredModule("stand-in.first", map[authz.Permission]authz.Translation{
			first: {Object: "a", Action: "read"},
		}),
		declaredModule("stand-in.second", map[authz.Permission]authz.Translation{
			second: {Object: "b", Action: "write"},
		}),
	)
	for perm, want := range map[authz.Permission]pair{
		first:  {object: "a", action: "read"},
		second: {object: "b", action: "write"},
	} {
		object, action, ok := ObjectAction(perm)
		if !ok || object != want.object || action != want.action {
			t.Errorf("ObjectAction(%q) = (%q, %q, %t), want (%q, %q, true)",
				perm, object, action, ok, want.object, want.action)
		}
	}
}

// Every row of a module reaches the process's translation, not just the first: a
// module that declares more than one permission of its own has each of them
// translatable, so a resolution that stopped after the first row would leave the
// rest untranslatable and their routes refused at startup or denied on every
// request.
func TestEveryRowOfAModuleReachesTheTranslation(t *testing.T) {
	first := authz.Permission("crm.a.read")
	second := authz.Permission("crm.b.write")
	resolveModules(t, declaredModule("stand-in.business", map[authz.Permission]authz.Translation{
		first:  {Object: "a", Action: "read"},
		second: {Object: "b", Action: "write"},
	}))
	for perm, want := range map[authz.Permission]pair{
		first:  {object: "a", action: "read"},
		second: {object: "b", action: "write"},
	} {
		object, action, ok := ObjectAction(perm)
		if !ok || object != want.object || action != want.action {
			t.Errorf("ObjectAction(%q) = (%q, %q, %t), want (%q, %q, true)",
				perm, object, action, ok, want.object, want.action)
		}
	}
}

// A row that agrees with one already in the set is kept once and is not a
// failure: a module may re-declare a built-in row, and two modules may declare
// one permission between them. A resolution that failed on any repeated key
// would refuse both shapes at startup.
func TestAnAgreeingTranslationRowIsKept(t *testing.T) {
	t.Run("a module may re-declare a built-in row", func(t *testing.T) {
		reg := core.New()
		reg.Register(core.Module{
			Name: "stand-in.business",
			Resources: []any{authz.TranslationSpec{Rows: map[authz.Permission]authz.Translation{
				authz.PermissionAccountRead: {Object: "account", Action: "read"},
			}}},
		})
		if err := resolveTranslation(reg); err != nil {
			t.Fatalf("resolving an agreeing row = %v, want nil", err)
		}
		t.Cleanup(func() { processTranslation.Store(nil) })
		object, action, ok := ObjectAction(authz.PermissionAccountRead)
		if !ok || object != "account" || action != "read" {
			t.Fatalf("ObjectAction(%q) = (%q, %q, %t), want the built-in row (account, read, true)",
				authz.PermissionAccountRead, object, action, ok)
		}
	})

	t.Run("two modules may share one declared row", func(t *testing.T) {
		shared := authz.Permission("crm.deal.read")
		rows := map[authz.Permission]authz.Translation{shared: {Object: "deal", Action: "read"}}
		resolveModules(t,
			declaredModule("stand-in.first", rows),
			declaredModule("stand-in.second", rows),
		)
		object, action, ok := ObjectAction(shared)
		if !ok || object != "deal" || action != "read" {
			t.Fatalf("ObjectAction(%q) = (%q, %q, %t), want (deal, read, true)", shared, object, action, ok)
		}
	})
}

// Two permissions may not translate to one pair. The engine matches the pair
// verbatim and never sees the permission key, so a grant written for one
// permission would authorize the other wherever their domains overlap; the pair
// is unique across the whole set, and a module row that reuses a pair already
// held by another permission — carried or a peer module's — fails the startup.
func TestASharedPairFailsResolution(t *testing.T) {
	t.Run("a module row reusing a carried row's pair", func(t *testing.T) {
		moduleRow := authz.Permission("crm.account.read")
		reg := core.New()
		reg.Register(core.Module{
			Name: "stand-in.business",
			Resources: []any{authz.TranslationSpec{Rows: map[authz.Permission]authz.Translation{
				moduleRow: {Object: "account", Action: "read"},
			}}},
		})
		err := resolveTranslation(reg)
		if !errors.Is(err, authz.ErrUnknownPermission) {
			t.Fatalf("resolving a module row sharing a carried row's pair = %v, want an error wrapping ErrUnknownPermission", err)
		}
		// The diagnostic names the pair and the permission already carrying it.
		for _, want := range []string{`"account"`, `"read"`, strconv.Quote(string(authz.PermissionAccountRead))} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the collision error %q does not name %s", err, want)
			}
		}
		if _, _, ok := ObjectAction(moduleRow); ok {
			t.Errorf("ObjectAction(%q) found a row a failed resolution must not install", moduleRow)
		}
	})

	t.Run("two modules sharing one pair", func(t *testing.T) {
		first := authz.Permission("crm.a.read")
		second := authz.Permission("crm.b.read")
		reg := core.New()
		reg.Register(declaredModule("stand-in.first", map[authz.Permission]authz.Translation{
			first: {Object: "shared", Action: "read"},
		}))
		reg.Register(declaredModule("stand-in.second", map[authz.Permission]authz.Translation{
			second: {Object: "shared", Action: "read"},
		}))
		err := resolveTranslation(reg)
		if !errors.Is(err, authz.ErrUnknownPermission) {
			t.Fatalf("resolving two module rows sharing a pair = %v, want an error wrapping ErrUnknownPermission", err)
		}
		for _, p := range []authz.Permission{first, second} {
			if _, _, ok := ObjectAction(p); ok {
				t.Errorf("ObjectAction(%q) found a row a failed resolution must not install", p)
			}
		}
	})
}

// A declared row with an empty object or action fails the startup: the engine
// matches a permission on its pair alone, so a permission whose pair is empty is
// one no grant can distinguishably carry, and a route that names it would pass
// the declaration check and then be refused on every request.
func TestAnEmptyTranslationRowFailsResolution(t *testing.T) {
	cases := []struct {
		name     string
		declared authz.Translation
	}{
		{"an empty object", authz.Translation{Object: "", Action: "read"}},
		{"an empty action", authz.Translation{Object: "deal", Action: ""}},
		{"both empty", authz.Translation{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			declared := authz.Permission("crm.deal.read")
			reg := core.New()
			reg.Register(core.Module{
				Name: "stand-in.business",
				Resources: []any{authz.TranslationSpec{Rows: map[authz.Permission]authz.Translation{
					declared: tc.declared,
				}}},
			})
			err := resolveTranslation(reg)
			if !errors.Is(err, authz.ErrUnknownPermission) {
				t.Fatalf("resolving a row with %s = %v, want an error wrapping ErrUnknownPermission", tc.name, err)
			}
			if _, _, ok := ObjectAction(declared); ok {
				t.Errorf("ObjectAction(%q) found a row a failed resolution must not install", declared)
			}
		})
	}
}
