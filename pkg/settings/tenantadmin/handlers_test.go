package tenantadmin

import (
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/settings"
)

// The layer is fixed by the tenant in the path and the decision is made in that
// tenant's domain on the tenant settings codes: a read asks settings.tenant.read
// and a write settings.tenant.write, both in authz.Domain(tenant). There is no
// query parameter that could point either at another layer or another tenant.
func TestThePathSettlesTheLayerAndThePermission(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
		body   any
		perm   authz.Permission
	}{
		{"read all", http.MethodGet, base, nil, authz.PermissionTenantSettingsRead},
		{"read one namespace", http.MethodGet, base + "/app", nil, authz.PermissionTenantSettingsRead},
		{"write", http.MethodPut, base + "/app/mode", map[string]any{"value": "manual"}, authz.PermissionTenantSettingsWrite},
		{"delete", http.MethodDelete, base + "/app/mode", nil, authz.PermissionTenantSettingsWrite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = []settings.DeclaredItem{
				decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
			}
			w := r.do(tc.method, tc.target, tc.body)
			if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
				t.Fatalf("the request ended with %d, want the allowed action to run: %s", w.Code, w.Body.String())
			}
			if got := r.guard.askedFor(); len(got) != 1 || got[0] != tc.perm {
				t.Errorf("the route asked for %v, want %q", got, tc.perm)
			}
			if got := r.guard.domainsAsked(); len(got) != 1 || got[0] != authz.Domain("acme") {
				t.Errorf("the decision was made in %v, want the path tenant's domain acme", got)
			}
			if !asksLayer(r.store.recorded(), settings.ForTenant("acme")) {
				t.Errorf("the settings surface was not reached on the path tenant's layer: %v", r.store.recorded())
			}
		})
	}
}

// asksLayer reports whether any settings call named the layer.
func asksLayer(calls []storeCall, layer settings.Layer) bool {
	for _, c := range calls {
		if c.layer == layer {
			return true
		}
	}
	return false
}

// A layer or tenant query parameter belongs to the platform surface and is
// refused here with 400: the layer is fixed by the path, and either parameter is
// a client trying to point the action at another layer or another tenant. The
// refusal comes after the permission decision, as the design fixes the order, so
// a caller without the permission is answered by the guard instead.
func TestALayerOrTenantQueryParameterIsA400AfterTheDecision(t *testing.T) {
	cases := map[string]struct {
		method string
		target string
		body   any
	}{
		"layer global":        {http.MethodGet, base + "?layer=global", nil},
		"layer tenant":        {http.MethodGet, base + "?layer=tenant&tenant=acme", nil},
		"tenant only":         {http.MethodGet, base + "?tenant=other", nil},
		"namespace and layer": {http.MethodGet, base + "/app?layer=tenant", nil},
		"write with layer":    {http.MethodPut, base + "/app/mode?layer=global", map[string]any{"value": "manual"}},
		"delete with tenant":  {http.MethodDelete, base + "/app/mode?tenant=acme", nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = []settings.DeclaredItem{
				decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
			}
			w := r.do(tc.method, tc.target, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("the request ended with %d, want 400: %s", w.Code, w.Body.String())
			}
			if p := problemOf(t, w); p.Type != typePrefix+badRequestCode {
				t.Errorf("the problem type is %q, want %q", p.Type, typePrefix+badRequestCode)
			}
			if got := r.guard.askedFor(); len(got) != 1 {
				t.Errorf("the guard was asked %v, want the decision before the shape refusal", got)
			}
			if got := r.store.ops(); len(got) != 0 {
				t.Errorf("the settings surface was reached (%v) for a refused request", got)
			}
		})
	}
}

// The permission decision precedes the query-parameter refusal: a caller the
// guard refuses is answered by the guard's 403 even when the request also carries
// a layer parameter.
func TestThePermissionDecisionPrecedesTheQueryRefusal(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	w := r.do(http.MethodGet, base+"?layer=global", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("the request ended with %d, want the guard's 403", w.Code)
	}
}

// A request with no identity is answered by this module with a 401 carrying the
// realm challenge and no problem type, and asks neither the guard nor the
// settings surface.
func TestNoIdentityIsA401WithTheRealmChallengeAndNoType(t *testing.T) {
	r := newRig(t)
	w := r.doAnonymous(http.MethodGet, base)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("the request ended with %d, want 401", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); got != `Bearer realm="api", Cookie realm="api"` {
		t.Errorf("the challenge is %q, want the realm challenge", got)
	}
	if p := problemOf(t, w); p.Type != "about:blank" {
		t.Errorf("the problem type is %q, want the default about:blank with no type of this capability", p.Type)
	}
	if got := r.guard.askedFor(); len(got) != 0 {
		t.Errorf("the guard was asked %v for an anonymous request", got)
	}
}

// The guard writes its own refusal: a denial is a 403 and an outage a 503, and
// the handler returns without touching the settings surface.
func TestTheGuardWritesItsOwnRefusal(t *testing.T) {
	t.Run("denied", func(t *testing.T) {
		r := newRig(t)
		r.guard.allow = false
		w := r.do(http.MethodGet, base, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("the request ended with %d, want 403", w.Code)
		}
		if got := r.store.ops(); len(got) != 0 {
			t.Errorf("the settings surface was reached (%v) after a denial", got)
		}
	})
	t.Run("outage", func(t *testing.T) {
		r := newRig(t)
		r.guard.err = errors.New("the policy engine is unreachable")
		w := r.do(http.MethodGet, base, nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("the request ended with %d, want 503", w.Code)
		}
	})
}

// A tenant outside the identifier's value domain is a 400 naming bad-request,
// written before a layer is built or the settings module is reached.
func TestAPathTenantOutsideTheIdentifierDomainIsA400(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}
	w := r.do(http.MethodGet, "/api/v1/tenants/acme%20x/settings", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("the request ended with %d, want 400: %s", w.Code, w.Body.String())
	}
	if p := problemOf(t, w); p.Type != typePrefix+badRequestCode {
		t.Errorf("the problem type is %q, want %q", p.Type, typePrefix+badRequestCode)
	}
	if got := r.store.ops(); len(got) != 0 {
		t.Errorf("the settings surface was reached (%v) for an identifier outside its domain", got)
	}
}

// The list route shows every declared item with the value it resolves to on the
// path tenant's layer, and with the source marker saying which layer that value
// comes from: the tenant row when there is one, else the global row, else the
// declared default. The global layer is read only to answer the source.
func TestTheListShowsTheValueAndItsSource(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("app.default", "app", reflect.TypeFor[string](), "auto", settings.Item{Description: "an item left at its default"}),
		decl("app.global", "app", reflect.TypeFor[int](), "100", settings.Item{Description: "an item set globally"}),
		decl("app.tenant", "app", reflect.TypeFor[time.Duration](), "1m0s", settings.Item{
			Description: "an item this tenant overrode",
			Domain:      settings.DomainOf("the positive durations", func(d time.Duration) bool { return d > 0 }),
		}),
	}
	r.store.values["app.tenant"] = 5 * time.Minute
	r.store.values["app.global"] = 42
	r.store.values["app.default"] = "auto"
	r.store.rows = []settings.Entry{
		{Key: "app.tenant", Tenant: "acme", Value: "5m0s"},
		{Key: "app.global", Tenant: "", Value: "42"},
	}

	w := r.do(http.MethodGet, base, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the request ended with %d: %s", w.Code, w.Body.String())
	}
	var views []SettingsTenantAdminSetting
	decode(t, w, &views)
	if len(views) != 3 {
		t.Fatalf("the listing holds %d items, want 3", len(views))
	}

	def := views[0]
	if def.Key != "app.default" || def.Source != sourceDefault {
		t.Errorf("the defaulted item is %+v, want source default", def)
	}
	if def.Written != "" || def.HasRow {
		t.Errorf("the defaulted item's tenant layer holds %q/%t, want no row", def.Written, def.HasRow)
	}

	global := views[1]
	if global.Key != "app.global" || global.Source != sourceGlobal {
		t.Errorf("the globally set item is %+v, want source global", global)
	}
	if global.Written != "" || global.HasRow {
		t.Errorf("the globally set item's tenant layer holds %q/%t, want no row", global.Written, global.HasRow)
	}
	if global.Value != "42" {
		t.Errorf("the globally set item's value is %q, want the global row's text", global.Value)
	}

	tenant := views[2]
	if tenant.Key != "app.tenant" || tenant.Source != sourceTenant {
		t.Errorf("the overridden item is %+v, want source tenant", tenant)
	}
	if tenant.Written != "5m0s" || !tenant.HasRow || tenant.Value != "5m0s" {
		t.Errorf("the overridden item's written/has_row/value are %q/%t/%q", tenant.Written, tenant.HasRow, tenant.Value)
	}
	if tenant.Default != "1m0s" || tenant.Type != "time.Duration" || tenant.Domain != "the positive durations" {
		t.Errorf("the declaration fields are %+v", tenant)
	}
}

// An empty-text row on the tenant layer is reported as a held row with source
// tenant: the text is empty either way, so has_row is what tells the client the
// answer holds an override.
func TestAnEmptyTextRowIsReportedAsAHeldRow(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("app.empty", "app", reflect.TypeFor[string](), "[]", settings.Item{}),
	}
	r.store.values["app.empty"] = ""
	r.store.rows = []settings.Entry{{Key: "app.empty", Tenant: "acme", Value: ""}}

	w := r.do(http.MethodGet, base, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the request ended with %d: %s", w.Code, w.Body.String())
	}
	var views []SettingsTenantAdminSetting
	decode(t, w, &views)
	if len(views) != 1 {
		t.Fatalf("the listing holds %d items, want 1", len(views))
	}
	v := views[0]
	if v.Written != "" {
		t.Errorf("written is %q, want empty: the row's text is the empty string", v.Written)
	}
	if !v.HasRow {
		t.Error("has_row is false, want true: the layer holds an empty-text row")
	}
	if v.Source != sourceTenant {
		t.Errorf("the source is %q, want tenant", v.Source)
	}
}

// A sensitive item's value never reaches a client: its default is empty and its
// written and value carry the fixed mask string, on every read route; its source
// and its row's existence are still reported, and its description is the
// declaration's text unchanged.
func TestASensitiveItemsValueIsMasked(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("app.secret", "app", reflect.TypeFor[string](), "", settings.Item{
			Description: "an application secret",
			Sensitive:   true,
		}),
	}
	r.store.values["app.secret"] = "hunter2"
	r.store.rows = []settings.Entry{{Key: "app.secret", Tenant: "acme", Value: "hunter2"}}

	for _, target := range []string{base, base + "/app"} {
		w := r.do(http.MethodGet, target, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s ended with %d: %s", target, w.Code, w.Body.String())
		}
		var views []SettingsTenantAdminSetting
		decode(t, w, &views)
		if len(views) != 1 {
			t.Fatalf("%s holds %d items, want 1", target, len(views))
		}
		v := views[0]
		if !v.Sensitive {
			t.Errorf("%s: the item is not marked sensitive", target)
		}
		if v.Default != "" {
			t.Errorf("%s: the default is %q, want empty", target, v.Default)
		}
		if v.Description != "an application secret" {
			t.Errorf("%s: the description is %q, want the declared text", target, v.Description)
		}
		if v.Written != maskedValue || v.Value != maskedValue {
			t.Errorf("%s: written/value are %q/%q, want the mask %q", target, v.Written, v.Value, maskedValue)
		}
		if v.Source != sourceTenant || !v.HasRow {
			t.Errorf("%s: source/has_row are %q/%t, want tenant/true", target, v.Source, v.HasRow)
		}
		if v.Value == "hunter2" || v.Written == "hunter2" {
			t.Errorf("%s: the credential reached the response", target)
		}
	}
}

// The namespace route shows one module's items and refuses a namespace no
// declared item belongs to with a bare 404.
func TestANamespaceGroupIsShownAndAnEmptyOneIsA404(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[int](), "1", settings.Item{}),
		decl("authn.session.idle-timeout", "authn.session", reflect.TypeFor[int](), "30", settings.Item{}),
	}
	r.store.values["app.mode"] = 5
	r.store.values["authn.session.idle-timeout"] = 60

	t.Run("one module's group", func(t *testing.T) {
		w := r.do(http.MethodGet, base+"/app", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("the request ended with %d: %s", w.Code, w.Body.String())
		}
		var views []SettingsTenantAdminSetting
		decode(t, w, &views)
		if len(views) != 1 || views[0].Key != "app.mode" {
			t.Fatalf("the group holds %+v, want only app.mode", views)
		}
	})
	t.Run("no declared item belongs to it", func(t *testing.T) {
		w := r.do(http.MethodGet, base+"/ghost", nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("the request ended with %d, want 404", w.Code)
		}
		if w.Body.Len() != 0 {
			t.Errorf("the 404 carried the body %q, want none: it names no problem type", w.Body.String())
		}
	})
	t.Run("the group is platform-only", func(t *testing.T) {
		w := r.do(http.MethodGet, base+"/authn.session", nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("the request ended with %d, want 404", w.Code)
		}
		if w.Body.Len() != 0 {
			t.Errorf("the 404 carried the body %q, want none", w.Body.String())
		}
	})
}

// The platform-only namespaces are invisible on this surface: the list omits
// them, and a single-key read or write answers the same bare 404 an undeclared
// key gets. The rest of the listing is untouched.
func TestPlatformOnlyNamespacesAreInvisible(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
		decl("ratelimit.window", "ratelimit", reflect.TypeFor[time.Duration](), "1m0s", settings.Item{}),
		decl("tenant.admission.open", "tenant.admission", reflect.TypeFor[bool](), "true", settings.Item{}),
		decl("authn.session.idle-timeout", "authn.session", reflect.TypeFor[int](), "30", settings.Item{}),
		decl("org.create.max-organizations", "org.create", reflect.TypeFor[int](), "5", settings.Item{}),
		decl("org.create.max-per-hour", "org.create", reflect.TypeFor[int](), "3", settings.Item{}),
	}
	r.store.values["app.mode"] = "manual"
	r.store.values["ratelimit.window"] = time.Minute
	r.store.values["tenant.admission.open"] = true
	r.store.values["authn.session.idle-timeout"] = 60
	r.store.values["org.create.max-organizations"] = 5
	r.store.values["org.create.max-per-hour"] = 3

	w := r.do(http.MethodGet, base, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the list ended with %d: %s", w.Code, w.Body.String())
	}
	var views []SettingsTenantAdminSetting
	decode(t, w, &views)
	if len(views) != 1 || views[0].Key != "app.mode" {
		t.Fatalf("the listing holds %+v, want only app.mode", views)
	}

	for _, target := range []string{
		base + "/ratelimit",
		base + "/tenant.admission",
		base + "/authn.session",
		base + "/org.create",
	} {
		if w := r.do(http.MethodGet, target, nil); w.Code != http.StatusNotFound {
			t.Errorf("%s ended with %d, want 404", target, w.Code)
		}
	}
}

// A write to a platform-only namespace is a 404 on both write routes, and the
// action does not run: the host's seam is not asked, no audit row lands and the
// settings module is not called.
func TestAWriteToAPlatformOnlyNamespaceIsA404(t *testing.T) {
	for _, tc := range []struct {
		name      string
		namespace string
		key       string
		declared  settings.DeclaredItem
	}{
		{
			name:      "authn",
			namespace: "authn.session",
			key:       "idle-timeout",
			declared:  decl("authn.session.idle-timeout", "authn.session", reflect.TypeFor[int](), "30", settings.Item{}),
		},
		{
			name:      "org.create",
			namespace: "org.create",
			key:       "max-organizations",
			declared:  decl("org.create.max-organizations", "org.create", reflect.TypeFor[int](), "5", settings.Item{}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = []settings.DeclaredItem{tc.declared}
			for name, write := range map[string]struct {
				method string
				body   any
			}{
				"set":    {http.MethodPut, map[string]any{"value": 60}},
				"delete": {http.MethodDelete, nil},
			} {
				t.Run(name, func(t *testing.T) {
					w := r.do(write.method, base+"/"+tc.namespace+"/"+tc.key, write.body)
					if w.Code != http.StatusNotFound {
						t.Fatalf("the request ended with %d, want 404: %s", w.Code, w.Body.String())
					}
					if w.Body.Len() != 0 {
						t.Errorf("the 404 carried the body %q, want none", w.Body.String())
					}
					if got := r.writeSeam.askedTenants(); len(got) != 0 {
						t.Errorf("the write seam was asked about %v, want no seam call", got)
					}
					if got := r.audit.entries(); len(got) != 0 {
						t.Errorf("the audit recorded %v, want none", got)
					}
					if got := r.store.ops(); len(got) != 0 {
						t.Errorf("the settings surface was reached (%v)", got)
					}
				})
			}
		})
	}
}

// A write lands a 204 and passes the path tenant's layer, the full key, the
// body's value and the caller's accountability key on to the settings module,
// and it writes one mandatory audit row before the action.
func TestAWriteLands204AndPassesTheActionOn(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}
	w := r.do(http.MethodPut, base+"/app/mode", map[string]any{"value": "manual"},
		authntest.WithAccount(authn.AccountID("carol")))
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
	if set.layer != settings.ForTenant("acme") {
		t.Errorf("the write named layer %v, want the acme tenant layer", set.layer)
	}
	if set.key != "app.mode" {
		t.Errorf("the write named key %q, want app.mode", set.key)
	}
	if set.value != "manual" {
		t.Errorf("the write carried value %v, want manual", set.value)
	}
	if set.actor != "carol" {
		t.Errorf("the write recorded actor %q, want the caller's account carol", set.actor)
	}
	if got := r.writeSeam.askedTenants(); len(got) != 1 || got[0] != "acme" {
		t.Errorf("the write seam was asked about %v, want [acme]", got)
	}

	rows := r.audit.entries()
	if len(rows) != 1 {
		t.Fatalf("the write wrote %d audit rows before the action, want one mandatory row", len(rows))
	}
	if rows[0].Action != actionSet || rows[0].Object != "app.mode" || rows[0].Outcome != outcomeSuccess {
		t.Errorf("the mandatory row is %+v", rows[0])
	}
	if rows[0].Detail[detailKey] != "app.mode" || rows[0].Detail[detailTenant] != "acme" {
		t.Errorf("the mandatory row's detail is %v, want the key and tenant", rows[0].Detail)
	}
	if rows[0].Detail[detailActionID] == "" {
		t.Error("the mandatory row carries no action identifier")
	}
}

// A write by an impersonating administrator is attributed to the administrator,
// not to the account impersonated: the actor the settings module records is the
// caller's accountability key, Identity.Key(), which is the hands on the request.
// The same holds on the delete route.
func TestAnImpersonatedWriteIsAttributedToTheActor(t *testing.T) {
	impersonation := []authntest.Option{
		authntest.WithAccount(authn.AccountID("carol")),
		authntest.WithActor(authn.AccountID("admin-7")),
		authntest.WithSession(authn.SessionID("0198f0aa-0000-7000-8000-000000000001")),
		authntest.WithScope("acme"),
	}

	actorOf := func(calls []storeCall, op string) (string, bool) {
		for _, c := range calls {
			if c.op == op {
				return c.actor, true
			}
		}
		return "", false
	}

	for name, tc := range map[string]struct {
		method string
		target string
		body   any
		op     string
		action string
	}{
		"set":    {http.MethodPut, base + "/app/mode", map[string]any{"value": "manual"}, "Set", actionSet},
		"delete": {http.MethodDelete, base + "/app/mode", nil, "Delete", actionDelete},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = []settings.DeclaredItem{
				decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
			}
			w := r.do(tc.method, tc.target, tc.body, impersonation...)
			if w.Code != http.StatusNoContent {
				t.Fatalf("the request ended with %d: %s", w.Code, w.Body.String())
			}
			actor, ok := actorOf(r.store.recorded(), tc.op)
			if !ok {
				t.Fatalf("the settings module was never asked to %s", tc.op)
			}
			if actor != "admin-7" {
				t.Errorf("the write recorded actor %q, want the impersonating administrator admin-7", actor)
			}
			if rows := r.audit.entries(); len(rows) != 1 || rows[0].Action != tc.action {
				t.Errorf("the audit rows are %+v, want one %s row", rows, tc.action)
			}
		})
	}
}

// A write whose key has no declaration is a 404 and reaches neither the write
// seam, the audit nor the settings module.
func TestAWriteOfAnUndeclaredKeyIsA404(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}
	w := r.do(http.MethodPut, base+"/app/ghost", map[string]any{"value": "on"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("the request ended with %d, want 404", w.Code)
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Errorf("an undeclared key reached the audit: %v", got)
	}
	if got := r.store.ops(); len(got) != 0 {
		t.Errorf("an undeclared key reached the settings surface: %v", got)
	}
	if got := r.writeSeam.askedTenants(); len(got) != 0 {
		t.Errorf("an undeclared key reached the write seam: %v", got)
	}
}

// A value the settings module refuses is a 422 naming invalid-value, and the
// write closes with a failed audit row linked to the mandatory one by its action
// identifier.
func TestARefusedValueIsA422WithAFailedAuditRow(t *testing.T) {
	cases := map[string]error{
		"type mismatch": settings.ErrTypeMismatch,
		"invalid value": settings.ErrInvalidValue,
	}
	for name, cause := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = []settings.DeclaredItem{
				decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
			}
			r.store.setErr = cause
			w := r.do(http.MethodPut, base+"/app/mode", map[string]any{"value": 1})
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("the request ended with %d, want 422", w.Code)
			}
			if p := problemOf(t, w); p.Type != typePrefix+invalidValueCode {
				t.Errorf("the problem type is %q, want %q", p.Type, typePrefix+invalidValueCode)
			}
			rows := r.audit.entries()
			if len(rows) != 2 {
				t.Fatalf("the write wrote %d audit rows, want a mandatory and a failed one", len(rows))
			}
			if rows[1].Outcome != outcomeFailed {
				t.Errorf("the closing row's outcome is %q, want %q", rows[1].Outcome, outcomeFailed)
			}
			if rows[0].Detail[detailActionID] != rows[1].Detail[detailActionID] {
				t.Errorf("the rows are linked by %q and %q, want one identifier",
					rows[0].Detail[detailActionID], rows[1].Detail[detailActionID])
			}
		})
	}
}

// A key the settings module does not know is a 404, the module's other failures
// a 503 with no problem type.
func TestAWriteRefusedByTheModuleMapsToItsAnswer(t *testing.T) {
	cases := map[string]struct {
		cause  error
		status int
	}{
		"undeclared key": {settings.ErrUnknownKey, http.StatusNotFound},
		"other failure":  {errors.New("the database is unreachable"), http.StatusServiceUnavailable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = []settings.DeclaredItem{
				decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
			}
			r.store.setErr = tc.cause
			w := r.do(http.MethodPut, base+"/app/mode", map[string]any{"value": "on"})
			if w.Code != tc.status {
				t.Fatalf("the request ended with %d, want %d", w.Code, tc.status)
			}
			if tc.status == http.StatusNotFound {
				if w.Body.Len() != 0 {
					t.Errorf("the 404 carried the body %q, want none", w.Body.String())
				}
				return
			}
			if p := problemOf(t, w); p.Type != "about:blank" {
				t.Errorf("the problem type is %q, want none of this capability", p.Type)
			}
		})
	}
}

// The mandatory audit row gates the action: when it does not land the write is a
// 503 and the settings module is never called.
func TestTheMandatoryAuditRowGatesTheAction(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}
	r.audit.fail(errors.New("the audit table is unreachable"))
	w := r.do(http.MethodPut, base+"/app/mode", map[string]any{"value": "on"})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("the request ended with %d, want 503", w.Code)
	}
	if got := r.store.ops(); len(got) != 0 {
		t.Errorf("the action ran (%v) after its mandatory row did not land", got)
	}
}

// The delete route drops a declared key's row on the path tenant's layer and
// writes its own mandatory audit row before the action. An undeclared key is a
// 404 and a failure the module returns is a 503.
func TestTheDeleteRoute(t *testing.T) {
	t.Run("landed", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = []settings.DeclaredItem{
			decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
		}
		w := r.do(http.MethodDelete, base+"/app/mode", nil)
		if w.Code != http.StatusNoContent {
			t.Fatalf("the request ended with %d: %s", w.Code, w.Body.String())
		}
		if !r.store.holdsLayerKey("Delete", settings.ForTenant("acme"), "app.mode") {
			t.Errorf("the settings module was not asked to delete app.mode on the acme layer: %v", r.store.recorded())
		}
		rows := r.audit.entries()
		if len(rows) != 1 || rows[0].Action != actionDelete || rows[0].Object != "app.mode" {
			t.Fatalf("the delete's audit rows are %+v, want one settings.delete row", rows)
		}
		if rows[0].Detail[detailTenant] != "acme" {
			t.Errorf("the delete's audit detail is %v, want the tenant", rows[0].Detail)
		}
	})
	t.Run("undeclared key", func(t *testing.T) {
		r := newRig(t)
		w := r.do(http.MethodDelete, base+"/app/ghost", nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("the request ended with %d, want 404", w.Code)
		}
		if got := r.store.ops(); len(got) != 0 {
			t.Errorf("an undeclared key reached the settings surface: %v", got)
		}
	})
	t.Run("module failure", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = []settings.DeclaredItem{
			decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
		}
		r.store.deleteErr = errors.New("the database is unreachable")
		w := r.do(http.MethodDelete, base+"/app/mode", nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("the request ended with %d, want 503", w.Code)
		}
	})
}

// A body the handler cannot read is a 400 before the settings module is called.
func TestAnUnreadableBodyIsA400(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}
	w := r.doRaw(http.MethodPut, base+"/app/mode", "{")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("the request ended with %d, want 400", w.Code)
	}
	if got := r.store.ops(); len(got) != 0 {
		t.Errorf("an unreadable body reached the settings surface: %v", got)
	}
}

// A failure to read the tenant layer's rows, the global layer's rows or resolve
// a value is a 503: a listing that could not read a row must not answer as
// though the row were not there.
func TestAListingFailureIsA503(t *testing.T) {
	t.Run("the tenant rows", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = []settings.DeclaredItem{
			decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
		}
		r.store.listErr = errors.New("the database is unreachable")
		w := r.do(http.MethodGet, base, nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("the request ended with %d, want 503", w.Code)
		}
	})
	t.Run("the global rows", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = []settings.DeclaredItem{
			decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
		}
		r.store.globalListErr = errors.New("the database is unreachable")
		w := r.do(http.MethodGet, base, nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("the request ended with %d, want 503", w.Code)
		}
	})
	t.Run("a resolved value", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = []settings.DeclaredItem{
			decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
		}
		r.store.getErr = settings.ErrNotLoaded
		w := r.do(http.MethodGet, base, nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("the request ended with %d, want 503", w.Code)
		}
	})
}

// The host's settings write seam refuses a write: the answer is 403 protected,
// the action does not run, no audit row lands and the settings module is not
// called. The seam is asked once, with the tenant in the path.
func TestAWriteSettingsRefusalIsProtected(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}
	r.writeSeam.refuse()
	w := r.do(http.MethodPut, base+"/app/mode", map[string]any{"value": "manual"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("the request ended with %d, want 403: %s", w.Code, w.Body.String())
	}
	if p := problemOf(t, w); p.Type != typePrefix+protectedCode {
		t.Errorf("the problem type is %q, want %q", p.Type, typePrefix+protectedCode)
	}
	if got := r.writeSeam.askedTenants(); len(got) != 1 || got[0] != "acme" {
		t.Errorf("the seam was asked about %v, want [acme]", got)
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Errorf("a refused write landed %d audit row(s), want none", len(got))
	}
	if got := r.store.ops(); len(got) != 0 {
		t.Errorf("a refused write reached the settings surface: %v", got)
	}
}

// The seam's own failure is a 503, never read as permission to proceed, and the
// action does not run.
func TestAWriteSettingsFailureIs503(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}
	r.writeSeam.fail(errors.New("the tenant roster is unreachable"))
	w := r.do(http.MethodPut, base+"/app/mode", map[string]any{"value": "manual"})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("the request ended with %d, want 503: %s", w.Code, w.Body.String())
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Errorf("an unanswered seam landed %d audit row(s), want none", len(got))
	}
	if got := r.store.ops(); len(got) != 0 {
		t.Errorf("an unanswered seam reached the settings surface: %v", got)
	}
}
