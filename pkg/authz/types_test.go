package authz

import (
	"reflect"
	"slices"
	"testing"
)

// TestModelTextIsThePublishedModel pins the model text to the one the design
// states, character for character. The model is fixed in code and a host cannot
// change it, so a matcher written wrong here silently decides the wrong way —
// this holds every line, the effect and the matcher included.
func TestModelTextIsThePublishedModel(t *testing.T) {
	const want = `[request_definition]
r = sub, dom, obj, act
[policy_definition]
p = sub, dom, obj, act
[role_definition]
g = _, _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub, r.dom) && r.dom == p.dom && r.obj == p.obj && r.act == p.act
`
	if ModelText != want {
		t.Errorf("ModelText is\n%s\nwant\n%s", ModelText, want)
	}
}

// structFields renders the fields of a struct value as "Name Type" in
// declaration order.
func structFields(t *testing.T, v any) []string {
	t.Helper()
	rt := reflect.TypeOf(v)
	if rt.Kind() != reflect.Struct {
		t.Fatalf("%v is not a struct", rt)
	}
	out := make([]string, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		out = append(out, f.Name+" "+f.Type.String())
	}
	return out
}

// interfaceMethods renders the methods of an interface type as "Name Signature",
// in the order reflection reports them, which is by name.
func interfaceMethods(t *testing.T, v any) []string {
	t.Helper()
	rt := reflect.TypeOf(v).Elem()
	if rt.Kind() != reflect.Interface {
		t.Fatalf("%v is not an interface", rt)
	}
	out := make([]string, 0, rt.NumMethod())
	for i := 0; i < rt.NumMethod(); i++ {
		m := rt.Method(i)
		out = append(out, m.Name+" "+m.Type.String())
	}
	return out
}

// TestTheSharedTypesHoldTheirFields pins the field set and the field types of
// every type a consumer or an implementer of this package names. These shapes
// are a published surface: a field added, dropped, renamed or retyped is a
// breaking change for whoever builds or reads one, so each is held to its exact
// set here.
func TestTheSharedTypesHoldTheirFields(t *testing.T) {
	cases := []struct {
		name string
		got  []string
		want []string
	}{
		{
			"Query",
			structFields(t, Query{}),
			[]string{"Subject string", "Domain authz.Domain", "Object string", "Action string"},
		},
		{
			"Requirement",
			structFields(t, Requirement{}),
			[]string{"Method string", "Path string", "Permission authz.Permission"},
		},
		{
			"PermissionSpec",
			structFields(t, PermissionSpec{}),
			[]string{"Permissions []authz.Permission", "DescriptionKeys map[authz.Permission]string"},
		},
		{
			"Binding",
			structFields(t, Binding{}),
			[]string{"Tenant string", "Account string", "Role authz.Role", "GrantedAt time.Time"},
		},
		{
			"RoleRecord",
			structFields(t, RoleRecord{}),
			[]string{"Tenant string", "Role authz.Role", "Builtin bool", "DescriptionKey string", "CreatedAt time.Time"},
		},
		{
			"GrantRecord",
			structFields(t, GrantRecord{}),
			[]string{"Tenant string", "Role authz.Role", "Resource string", "Action string", "CreatedAt time.Time"},
		},
		{
			"Grant",
			structFields(t, Grant{}),
			[]string{"Resource string", "Action string"},
		},
	}
	for _, c := range cases {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("%s has fields %q, want %q", c.name, c.got, c.want)
		}
	}
}

// TestTheCapabilityInterfacesHoldTheirMethodSets pins the method set and each
// signature of the interfaces a module implements and a consumer takes up. The
// signature and the method set are the stable part of an interface: a method
// added, dropped or re-signed breaks every implementer and every consumer, so
// each is held here.
func TestTheCapabilityInterfacesHoldTheirMethodSets(t *testing.T) {
	cases := []struct {
		name string
		got  []string
		want []string
	}{
		{
			"Enforcer",
			interfaceMethods(t, (*Enforcer)(nil)),
			[]string{"Allowed func(context.Context, authz.Query) (bool, error)"},
		},
		{
			"Catalog",
			interfaceMethods(t, (*Catalog)(nil)),
			[]string{
				"Declared func() []authz.Permission",
				"DescriptionKey func(authz.Permission) (string, bool)",
				"Has func(authz.Permission) bool",
				"ObjectAction func(authz.Permission) (string, string, bool)",
			},
		},
		{
			"Gate",
			interfaceMethods(t, (*Gate)(nil)),
			[]string{
				"Covered func(string, string) bool",
				"Open func(string, string)",
				"Require func(string, string, authz.Permission)",
			},
		},
		{
			"MembershipChecker",
			interfaceMethods(t, (*MembershipChecker)(nil)),
			[]string{"IsMember func(context.Context, string, string) (bool, error)"},
		},
		{
			"Manager",
			interfaceMethods(t, (*Manager)(nil)),
			[]string{
				"Bindings func(context.Context, string) ([]authz.Binding, error)",
				"BindingsOf func(context.Context, string, string) ([]authz.Binding, error)",
				"ClearAccountBindings func(context.Context, string, string) error",
				"DeleteBinding func(context.Context, string, string, authz.Role) error",
				"DeleteGrant func(context.Context, authz.GrantRecord) error",
				"DeleteRole func(context.Context, string, authz.Role) error",
				"Grants func(context.Context, string, authz.Role) ([]authz.GrantRecord, error)",
				"MarkRoleBuiltin func(context.Context, string, authz.Role) error",
				"PutBinding func(context.Context, authz.Binding) error",
				"PutGrant func(context.Context, authz.GrantRecord) error",
				"PutRole func(context.Context, authz.RoleRecord) error",
				"Roles func(context.Context, string) ([]authz.RoleRecord, error)",
			},
		},
		{
			"Seeder",
			interfaceMethods(t, (*Seeder)(nil)),
			[]string{
				"SeedGrants func(context.Context, string, authz.Role, []authz.Grant) (int, error)",
			},
		},
		{
			"SubjectPermissions",
			interfaceMethods(t, (*SubjectPermissions)(nil)),
			[]string{
				"Held func(context.Context, authz.Domain, string) ([]authz.Permission, error)",
			},
		},
	}
	for _, c := range cases {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("%s has methods %q, want %q", c.name, c.got, c.want)
		}
	}
}
