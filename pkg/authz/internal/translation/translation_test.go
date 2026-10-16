package translation

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
)

// A module that declares a permission of its own declares how it translates it.
// These cases resolve a process's translation over such a declaration and hold
// what the resolved set is: the module's row beside the built-in ones, the
// built-in rows unchanged, and the three shapes of a declared row that fail the
// resolution.

// declaredPermission is a permission of a module's own, beyond the ones the
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
	if err := Resolve(reg); err != nil {
		t.Fatalf("resolving the translation: %v", err)
	}
	t.Cleanup(Reset)
}

// resolveDeclared installs the process's translation set over one stand-in
// module that declares the given rows.
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

// The built-in set is exactly the rows this capability carries: every permission
// of the two vocabularies maps to its one (object, action) pair, and the set
// holds no row beyond them. A permission with no row is a decision the
// authorization layer cannot express, and a row with no permission is a mapping
// nothing can reach.
func TestTheBuiltinSetIsTheWholeClosedSet(t *testing.T) {
	want := map[authz.Permission][2]string{
		authz.PermissionTenantAccess:        {"tenant", "access"},
		authz.PermissionAccountRead:         {"account", "read"},
		authz.PermissionAccountCreate:       {"account", "create"},
		authz.PermissionAccountUpdate:       {"account", "update"},
		authz.PermissionAccountDisable:      {"account", "disable"},
		authz.PermissionAccountDelete:       {"account", "delete"},
		authz.PermissionMembershipManage:    {"membership", "manage"},
		authz.PermissionSessionRead:         {"session", "read"},
		authz.PermissionSessionRevoke:       {"session", "revoke"},
		authz.PermissionMFAReset:            {"factor", "reset"},
		authz.PermissionClientManage:        {"client", "manage"},
		authz.PermissionIDPManage:           {"idp", "manage"},
		authz.PermissionImpersonateStart:    {"impersonation", "start"},
		authz.PermissionImpersonationRead:   {"impersonation", "read"},
		authz.PermissionImpersonationRevoke: {"impersonation", "revoke"},
		authz.PermissionRoleRead:            {"role", "read"},
		authz.PermissionRoleManage:          {"role", "manage"},
		authz.PermissionAuditRead:           {"audit", "read"},
		authz.PermissionAuditExport:         {"audit", "export"},
		authz.PermissionSettingsRead:        {"settings", "read"},
		authz.PermissionSettingsWrite:       {"settings", "write"},
	}
	for p, want := range want {
		object, action, ok := Lookup(p)
		if !ok || object != want[0] || action != want[1] {
			t.Errorf("Lookup(%q) = (%q, %q, %t), want (%q, %q, true)",
				p, object, action, ok, want[0], want[1])
		}
	}
	for _, p := range Builtin() {
		if _, ok := want[p]; !ok {
			t.Errorf("the built-in set holds a row for %q, which no permission names", p)
		}
	}
	if len(Builtin()) != len(want) {
		t.Errorf("the built-in set holds %d rows, want %d", len(Builtin()), len(want))
	}
}

// The union does not change a built-in row: every row this capability carries
// still translates to the same (object, action) after a module's row is added.
func TestTheBuiltinRowsSurviveTheUnion(t *testing.T) {
	// The rows as they stand before any module's is added. Reading them first is
	// what makes the case an assertion: comparing the set after the resolution
	// with itself would pass whatever the resolution did to it.
	before := make(map[authz.Permission][2]string, len(Builtin()))
	for _, key := range Builtin() {
		object, action, _ := Lookup(key)
		before[key] = [2]string{object, action}
	}
	if len(before) == 0 {
		t.Fatal("the built-in set is empty, so the case cannot show a row surviving")
	}

	resolveDeclared(t, map[authz.Permission]authz.Translation{
		declaredPermission: {Object: "deal", Action: "read"},
	})

	for key, want := range before {
		object, action, ok := Lookup(key)
		if !ok || object != want[0] || action != want[1] {
			t.Errorf("Lookup(%q) = (%q, %q, %t) after the union, want the built-in row (%q, %q, true)",
				key, object, action, ok, want[0], want[1])
		}
	}
}

// A module may not disagree with a row already in the set, on either axis of the
// pair: a row that names a permission already translated as another pair fails
// the resolution, wrapped around the same sentinel the declaration check uses,
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
			err := Resolve(reg)
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
			object, action, ok := Lookup(authz.PermissionAccountRead)
			if !ok || object != "account" || action != "read" {
				t.Fatalf("Lookup(%q) = (%q, %q, %t), want the built-in row (account, read, true): a failed resolution must install nothing",
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
	for perm, want := range map[authz.Permission][2]string{
		first:  {"a", "read"},
		second: {"b", "write"},
	} {
		object, action, ok := Lookup(perm)
		if !ok || object != want[0] || action != want[1] {
			t.Errorf("Lookup(%q) = (%q, %q, %t), want (%q, %q, true)",
				perm, object, action, ok, want[0], want[1])
		}
	}
}

// Every row of a module reaches the process's translation, not just the first: a
// module that declares more than one permission of its own has each of them
// translatable, so a resolution that stopped after the first row would leave the
// rest untranslatable.
func TestEveryRowOfAModuleReachesTheTranslation(t *testing.T) {
	first := authz.Permission("crm.a.read")
	second := authz.Permission("crm.b.write")
	resolveModules(t, declaredModule("stand-in.business", map[authz.Permission]authz.Translation{
		first:  {Object: "a", Action: "read"},
		second: {Object: "b", Action: "write"},
	}))
	for perm, want := range map[authz.Permission][2]string{
		first:  {"a", "read"},
		second: {"b", "write"},
	} {
		object, action, ok := Lookup(perm)
		if !ok || object != want[0] || action != want[1] {
			t.Errorf("Lookup(%q) = (%q, %q, %t), want (%q, %q, true)",
				perm, object, action, ok, want[0], want[1])
		}
	}
}

// A row that agrees with one already in the set is kept once and is not a
// failure: a module may re-declare a built-in row, and two modules may declare
// one permission between them. A resolution that failed on any repeated key
// would refuse both shapes.
func TestAnAgreeingTranslationRowIsKept(t *testing.T) {
	t.Run("a module may re-declare a built-in row", func(t *testing.T) {
		reg := core.New()
		reg.Register(core.Module{
			Name: "stand-in.business",
			Resources: []any{authz.TranslationSpec{Rows: map[authz.Permission]authz.Translation{
				authz.PermissionAccountRead: {Object: "account", Action: "read"},
			}}},
		})
		if err := Resolve(reg); err != nil {
			t.Fatalf("resolving an agreeing row = %v, want nil", err)
		}
		t.Cleanup(Reset)
		object, action, ok := Lookup(authz.PermissionAccountRead)
		if !ok || object != "account" || action != "read" {
			t.Fatalf("Lookup(%q) = (%q, %q, %t), want the built-in row (account, read, true)",
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
		object, action, ok := Lookup(shared)
		if !ok || object != "deal" || action != "read" {
			t.Fatalf("Lookup(%q) = (%q, %q, %t), want (deal, read, true)", shared, object, action, ok)
		}
	})
}

// Two permissions may not translate to one pair. The engine matches the pair
// verbatim and never sees the permission key, so a grant written for one
// permission would authorize the other wherever their domains overlap; the pair
// is unique across the whole set, and a module row that reuses a pair already
// held by another permission — carried or a peer module's — fails the
// resolution.
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
		err := Resolve(reg)
		if !errors.Is(err, authz.ErrUnknownPermission) {
			t.Fatalf("resolving a module row sharing a carried row's pair = %v, want an error wrapping ErrUnknownPermission", err)
		}
		// The diagnostic names the pair and the permission already carrying it.
		for _, want := range []string{`"account"`, `"read"`, strconv.Quote(string(authz.PermissionAccountRead))} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the collision error %q does not name %s", err, want)
			}
		}
		if _, _, ok := Lookup(moduleRow); ok {
			t.Errorf("Lookup(%q) found a row a failed resolution must not install", moduleRow)
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
		err := Resolve(reg)
		if !errors.Is(err, authz.ErrUnknownPermission) {
			t.Fatalf("resolving two module rows sharing a pair = %v, want an error wrapping ErrUnknownPermission", err)
		}
		for _, p := range []authz.Permission{first, second} {
			if _, _, ok := Lookup(p); ok {
				t.Errorf("Lookup(%q) found a row a failed resolution must not install", p)
			}
		}
	})
}

// A declared row with an empty object or action fails the resolution: the engine
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
			err := Resolve(reg)
			if !errors.Is(err, authz.ErrUnknownPermission) {
				t.Fatalf("resolving a row with %s = %v, want an error wrapping ErrUnknownPermission", tc.name, err)
			}
			if _, _, ok := Lookup(declared); ok {
				t.Errorf("Lookup(%q) found a row a failed resolution must not install", declared)
			}
		})
	}
}
