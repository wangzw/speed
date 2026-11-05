package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/i18n"
)

// listRoles drives the role list route and returns its views as the JSON
// objects a client reads, so a case holds the wire contract and not the Go
// struct that happens to produce it.
func listRoles(t *testing.T, r *rig) []map[string]any {
	t.Helper()
	w := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet,
		"/api/v1/system/tenants/t1/roles", params("tenant", "t1"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing the roles answered %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Roles []map[string]any `json:"roles"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("the answer is not a role list: %v; body = %s", err, w.Body.String())
	}
	if body.Roles == nil {
		t.Fatalf("the answer carries no roles array: %s", w.Body.String())
	}
	return body.Roles
}

// TestTheRoleListViewDescribesEachRole holds the role view's description: the
// operator's free text on a role the management surface created, and the
// rendered description of a builtin role's key. The counts and the caller's
// management capability are published with it.
func TestTheRoleListViewDescribesEachRole(t *testing.T) {
	r := newRig(t)
	r.manager.roles = []authz.RoleRecord{
		{Tenant: "t1", Role: "editor", Builtin: false, Description: "Writes and publishes articles."},
		{Tenant: "t1", Role: "viewer", Builtin: true, DescriptionKey: "authz.role.viewer"},
	}
	r.manager.grants = []authz.GrantRecord{
		{Tenant: "t1", Role: "editor", Resource: "article", Action: "read"},
		{Tenant: "t1", Role: "editor", Resource: "article", Action: "write"},
		{Tenant: "t1", Role: "viewer", Resource: "article", Action: "read"},
	}
	r.localizeWith(stubLocalizer{
		defaultLocale: "en-US",
		index: map[i18n.Locale]map[i18n.Key]string{
			"en-US": {"authz.role.viewer": "Reads articles."},
		},
	}, "en-US")

	views := listRoles(t, r)
	if len(views) != 2 {
		t.Fatalf("the answer carries %d views, want two", len(views))
	}
	byRole := make(map[string]map[string]any, len(views))
	for _, view := range views {
		name, _ := view["role"].(string)
		byRole[name] = view
	}
	editor := byRole["editor"]
	if got := editor["description"]; got != "Writes and publishes articles." {
		t.Errorf("editor description = %v, want the operator's free text", got)
	}
	if got := editor["permission_count"]; got != float64(2) {
		t.Errorf("editor permission_count = %v, want 2", got)
	}
	if got, ok := editor["member_count"]; !ok || got != nil {
		t.Errorf("editor member_count = %v (present %v), want null with no identity reader", got, ok)
	}
	if got := editor["can_manage"]; got != false {
		t.Errorf("editor can_manage = %v, want false for a caller without role.manage", got)
	}
	if _, ok := editor["description_key"]; ok {
		t.Error("the role view still carries description_key; the field was removed")
	}

	viewer := byRole["viewer"]
	if got := viewer["description"]; got != "Reads articles." {
		t.Errorf("viewer description = %v, want the builtin key rendered in the request's language", got)
	}
}

// TestABuiltinDescriptionFallsBackToItsKeyWithoutALocalizer holds the localizer
// being optional: with none assembled the builtin role's description is the key
// itself, the i18n engine's own fallback, and never an empty string that would
// read as "no description".
func TestABuiltinDescriptionFallsBackToItsKeyWithoutALocalizer(t *testing.T) {
	r := newRig(t)
	r.manager.roles = []authz.RoleRecord{
		{Tenant: "t1", Role: "viewer", Builtin: true, DescriptionKey: "authz.role.viewer"},
	}
	views := listRoles(t, r)
	if got := views[0]["description"]; got != "authz.role.viewer" {
		t.Errorf("description = %v, want the key itself with no localizer", got)
	}
}

// TestPutRoleStoresTheFreeTextDescription holds the write: the body's
// description reaches the manager as the record's Description, and the stored
// key stays empty because the request no longer carries one.
func TestPutRoleStoresTheFreeTextDescription(t *testing.T) {
	r := newRig(t)
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/system/tenants/t1/roles/ops",
		params("tenant", "t1", "role", "ops"), map[string]any{"description": "Runs the platform."})
	if w.Code != http.StatusNoContent {
		t.Fatalf("writing a role answered %d, want 204; body = %s", w.Code, w.Body.String())
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "PutRole" {
		t.Fatalf("the manager calls = %+v, want one PutRole", got)
	}
	rec := got[0].roleRec
	if rec.Description != "Runs the platform." {
		t.Errorf("the record's Description = %q, want the body's free text", rec.Description)
	}
	if rec.DescriptionKey != "" {
		t.Errorf("the record's DescriptionKey = %q, want empty when a description is written", rec.DescriptionKey)
	}
}

// TestPutRoleIgnoresTheRemovedDescriptionKey holds the removal: the request no
// longer declares description_key, so a client that still sends it reaches
// nothing. The role is written with no key and no description, the removed
// member is not a refusal.
func TestPutRoleIgnoresTheRemovedDescriptionKey(t *testing.T) {
	r := newRig(t)
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/system/tenants/t1/roles/ops",
		params("tenant", "t1", "role", "ops"), map[string]any{"description_key": "authz.role.ops"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("a removed-member write answered %d, want 204; body = %s", w.Code, w.Body.String())
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "PutRole" {
		t.Fatalf("the manager calls = %+v, want one PutRole", got)
	}
	rec := got[0].roleRec
	if rec.DescriptionKey != "" || rec.Description != "" {
		t.Errorf("the record = %+v, want neither a key nor a description", rec)
	}
}

// TestAnExplicitEmptyDescriptionClearsTheText holds the presence rule: a body
// that carries description as an explicit empty string clears the free text,
// rather than being treated as absent.
func TestAnExplicitEmptyDescriptionClearsTheText(t *testing.T) {
	r := newRig(t)
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/system/tenants/t1/roles/ops",
		params("tenant", "t1", "role", "ops"), map[string]any{"description": ""})
	if w.Code != http.StatusNoContent {
		t.Fatalf("writing a role answered %d, want 204; body = %s", w.Code, w.Body.String())
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "PutRole" {
		t.Fatalf("the manager calls = %+v, want one PutRole", got)
	}
	if rec := got[0].roleRec; rec.Description != "" {
		t.Errorf("the record = %+v, want the explicit empty description", rec)
	}
}

// TestPutRoleRefusesAnOverlongDescription holds the bound: a description longer
// than authz.RoleDescriptionMax characters is a 400 before the audit and the
// manager, and one exactly at the bound is written.
func TestPutRoleRefusesAnOverlongDescription(t *testing.T) {
	t.Run("over the bound", func(t *testing.T) {
		r := newRig(t)
		w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/system/tenants/t1/roles/ops",
			params("tenant", "t1", "role", "ops"),
			map[string]any{"description": strings.Repeat("界", authz.RoleDescriptionMax+1)})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("an overlong description answered %d, want 400", w.Code)
		}
		if got := r.manager.recorded(); len(got) != 0 {
			t.Fatalf("the manager was called %v for an overlong description", got)
		}
		if got := r.audit.entries(); len(got) != 0 {
			t.Fatalf("an audit row was written %v for an overlong description", got)
		}
	})
	t.Run("at the bound", func(t *testing.T) {
		r := newRig(t)
		text := strings.Repeat("界", authz.RoleDescriptionMax)
		w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/system/tenants/t1/roles/ops",
			params("tenant", "t1", "role", "ops"), map[string]any{"description": text})
		if w.Code != http.StatusNoContent {
			t.Fatalf("a description at the bound answered %d, want 204", w.Code)
		}
		got := r.manager.recorded()
		if len(got) != 1 || got[0].roleRec.Description != text {
			t.Fatalf("the record = %+v, want the description at the bound written", got)
		}
	})
}

// TestTheRoleWritePrescreenKeepsTheDescription holds the prescreen: the
// description member is part of the declared body, so it survives the member
// filtering, and a non-string value is the module's own bad-request answer
// rather than reaching the entry point's untyped refusal.
func TestTheRoleWritePrescreenKeepsTheDescription(t *testing.T) {
	r := newRig(t)
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/system/tenants/t1/roles/ops",
		params("tenant", "t1", "role", "ops"), map[string]any{"description": 42})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a non-string description answered %d, want 400", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v for a non-string description", got)
	}
}
