package authz

import (
	"reflect"
	"slices"
	"testing"
)

// TestModelTextIsThePublishedModel checks that the model text matches the design
// character for character.
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

// TestTheSharedTypesHoldTheirFields checks that every shared type keeps its
// exact field set and field types.
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
			[]string{
				"Permissions []authz.Permission",
				"TenantGoverned []authz.Permission",
				"DescriptionKeys map[authz.Permission]string",
				"Metadata map[authz.Permission]authz.PermissionMeta",
			},
		},
		{
			"PermissionMeta",
			structFields(t, PermissionMeta{}),
			[]string{"Group string", "Dangerous bool"},
		},
		{
			"Binding",
			structFields(t, Binding{}),
			[]string{"Tenant string", "Account string", "Role authz.Role", "GrantedAt time.Time"},
		},
		{
			"RoleRecord",
			structFields(t, RoleRecord{}),
			[]string{"Tenant string", "Role authz.Role", "Builtin bool", "DescriptionKey string", "Description string", "CreatedAt time.Time"},
		},
		{
			"AccountIdentity",
			structFields(t, AccountIdentity{}),
			[]string{"DisplayName string", "Username string", "Active bool"},
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

// TestTheCapabilityInterfacesHoldTheirMethodSets checks that every capability
// interface keeps its exact method set and signatures.
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
				"Governed func(authz.Permission) bool",
				"Has func(authz.Permission) bool",
				"Metadata func(authz.Permission) (authz.PermissionMeta, bool)",
				"ObjectAction func(authz.Permission) (string, string, bool)",
				"PermissionOf func(string, string) (authz.Permission, bool)",
				"TenantGoverned func() []authz.Permission",
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
			"AccountIdentityReader",
			interfaceMethods(t, (*AccountIdentityReader)(nil)),
			[]string{
				"Identities func(context.Context, string, []string) (map[string]authz.AccountIdentity, error)",
			},
		},
		{
			"WriteGuard",
			interfaceMethods(t, (*WriteGuard)(nil)),
			[]string{"Denied func(context.Context, string, authz.WriteOp) (bool, error)"},
		},
		{
			"WriteSettings",
			interfaceMethods(t, (*WriteSettings)(nil)),
			[]string{"Denied func(context.Context, string) (bool, error)"},
		},
		{
			"Manager",
			interfaceMethods(t, (*Manager)(nil)),
			[]string{
				"Bindings func(context.Context, string) ([]authz.Binding, error)",
				"BindingsOf func(context.Context, string, string) ([]authz.Binding, error)",
				"BindingsOfRole func(context.Context, string, authz.Role, string, int) ([]authz.Binding, error)",
				"ClearAccountBindings func(context.Context, string, string) error",
				"DeleteBinding func(context.Context, string, string, authz.Role) error",
				"DeleteGrant func(context.Context, authz.GrantRecord) error",
				"DeleteRole func(context.Context, string, authz.Role) error",
				"GrantCounts func(context.Context, string) (map[authz.Role]int, error)",
				"Grants func(context.Context, string, authz.Role) ([]authz.GrantRecord, error)",
				"MarkRoleBuiltin func(context.Context, string, authz.Role) error",
				"PutBinding func(context.Context, authz.Binding) error",
				"PutGrant func(context.Context, authz.GrantRecord) error",
				"PutRole func(context.Context, authz.RoleRecord) error",
				"RoleSubsetHeld func(context.Context, string, authz.Role, map[authz.Permission]bool) (bool, error)",
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
			"Reconciler",
			interfaceMethods(t, (*Reconciler)(nil)),
			[]string{
				"ReconcileGrants func(context.Context, string, authz.Role, []authz.Grant, []authz.Grant) (int, int, error)",
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
