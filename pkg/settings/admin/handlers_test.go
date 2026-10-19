package admin

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

// The read and write routes disagree on the permission they ask for and agree
// on the domain the layer chooses: a global layer is decided in the platform
// domain, a tenant's in that tenant's own domain. The two ride together, so a
// caller authorized in one tenant cannot reach the global layer.
func TestTheLayerChoosesTheDomainAndThePermission(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
		body   any
		want   authz.Permission
		domain authz.Domain
	}{
		{"read global", http.MethodGet, "/api/v1/system/settings?layer=global", nil, authz.PermissionSettingsRead, authz.PlatformDomain},
		{"read tenant", http.MethodGet, "/api/v1/system/settings?layer=tenant&tenant=acme", nil, authz.PermissionSettingsRead, authz.Domain("acme")},
		{"write global", http.MethodPut, "/api/v1/system/settings/app/mode?layer=global", map[string]any{"value": "on"}, authz.PermissionSettingsWrite, authz.PlatformDomain},
		{"write tenant", http.MethodPut, "/api/v1/system/settings/app/mode?layer=tenant&tenant=acme", map[string]any{"value": "on"}, authz.PermissionSettingsWrite, authz.Domain("acme")},
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
			if got := r.guard.askedFor(); len(got) != 1 || got[0] != tc.want {
				t.Errorf("the route asked for %v, want %q", got, tc.want)
			}
			if got := r.guard.domainsAsked(); len(got) != 1 || got[0] != tc.domain {
				t.Errorf("the decision was made in %v, want %q", got, tc.domain)
			}
		})
	}
}

// A layer selection that is absent, out of the closed set, or paired with a
// tenant the wrong way is a 400 written before the guard is asked: the request
// cannot be put to the policy until the layer names a domain.
func TestAnUnreadableLayerSelectionIsA400BeforeTheDecision(t *testing.T) {
	targets := map[string]string{
		"layer absent":            "/api/v1/system/settings",
		"layer out of the set":    "/api/v1/system/settings?layer=bogus",
		"tenant with global":      "/api/v1/system/settings?layer=global&tenant=acme",
		"tenant layer, no tenant": "/api/v1/system/settings?layer=tenant",
		"empty tenant":            "/api/v1/system/settings?layer=tenant&tenant=",
		"blank tenant":            "/api/v1/system/settings?layer=tenant&tenant=a%20b",
		"control tenant":          "/api/v1/system/settings?layer=tenant&tenant=a%0Ab",
		"tenant without layer":    "/api/v1/system/settings?tenant=acme",
	}
	for name, target := range targets {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			w := r.do(http.MethodGet, target, nil)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("the request ended with %d, want 400", w.Code)
			}
			if p := problemOf(t, w); p.Type != typePrefix+badRequestCode {
				t.Errorf("the problem type is %q, want %q", p.Type, typePrefix+badRequestCode)
			}
			if got := r.guard.askedFor(); len(got) != 0 {
				t.Errorf("the guard was asked %v before the layer was settled", got)
			}
			if got := r.store.ops(); len(got) != 0 {
				t.Errorf("the settings surface was reached (%v) before the layer was settled", got)
			}
		})
	}
}

// A request with no identity is answered by this module with a 401 carrying the
// realm challenge and no problem type, and asks neither the guard nor the
// settings surface.
func TestNoIdentityIsA401WithTheRealmChallengeAndNoType(t *testing.T) {
	r := newRig(t)
	w := r.doAnonymous(http.MethodGet, "/api/v1/system/settings?layer=global")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("the request ended with %d, want 401", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); got != `Bearer realm="public", Cookie realm="public"` {
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
		w := r.do(http.MethodGet, "/api/v1/system/settings?layer=global", nil)
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
		w := r.do(http.MethodGet, "/api/v1/system/settings?layer=global", nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("the request ended with %d, want 503", w.Code)
		}
	})
}

// The list route shows every declared item with the value it resolves to on the
// selected layer: the type is the declared type's text form, the default is the
// declared default's text, written is the selected layer's text and value is the
// resolved text.
func TestTheListShowsEveryDeclaredItemWithItsResolvedValue(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("ratelimit.window", "ratelimit", reflect.TypeFor[time.Duration](), "1m0s", settings.Item{
			Description: "the length of one counting window",
			Domain:      settings.DomainOf("the positive durations", func(d time.Duration) bool { return d > 0 }),
		}),
		decl("ratelimit.limit", "ratelimit", reflect.TypeFor[int](), "100", settings.Item{Description: "the limit"}),
	}
	r.store.values["ratelimit.window"] = 5 * time.Minute
	r.store.values["ratelimit.limit"] = 42
	r.store.rows = []settings.Entry{{Key: "ratelimit.limit", Tenant: "", Value: "42"}}

	w := r.do(http.MethodGet, "/api/v1/system/settings?layer=global", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the request ended with %d: %s", w.Code, w.Body.String())
	}
	var views []SettingsAdminSetting
	decode(t, w, &views)
	if len(views) != 2 {
		t.Fatalf("the listing holds %d items, want 2", len(views))
	}
	window := views[0]
	if window.Key != "ratelimit.window" || window.Namespace != "ratelimit" {
		t.Errorf("the first item is %+v", window)
	}
	if window.Type != "time.Duration" {
		t.Errorf("the type is %q, want time.Duration", window.Type)
	}
	if window.Domain != "the positive durations" {
		t.Errorf("the domain is %q, want the declared rule", window.Domain)
	}
	if window.Default != "1m0s" {
		t.Errorf("the default is %q, want 1m0s", window.Default)
	}
	if window.Written != "" {
		t.Errorf("written is %q, want empty: the layer holds no row", window.Written)
	}
	if window.HasRow {
		t.Error("has_row is true, want false: the layer holds no row")
	}
	if window.Value != "5m0s" {
		t.Errorf("value is %q, want 5m0s: the resolved duration in its text form", window.Value)
	}

	limit := views[1]
	if limit.Written != "42" || limit.Value != "42" {
		t.Errorf("the limit's written/value are %q/%q, want 42/42", limit.Written, limit.Value)
	}
	if !limit.HasRow {
		t.Error("has_row is false, want true: the layer holds the limit's row")
	}
	if limit.Default != "100" {
		t.Errorf("the limit's default is %q, want 100", limit.Default)
	}
	if limit.Domain != "" {
		t.Errorf("the limit's domain is %q, want empty: it declares none", limit.Domain)
	}
}

// A row the layer holds as the empty string is reported as a row: the text is
// empty either way, so has_row is what tells the client the answer holds an
// override. A domain that admits the empty text lets the write land.
func TestAnEmptyTextRowIsReportedAsAHeldRow(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("authn.sms.allowed-prefixes", "authn.sms", reflect.TypeFor[string](), "[]", settings.Item{}),
	}
	r.store.values["authn.sms.allowed-prefixes"] = ""
	r.store.rows = []settings.Entry{{Key: "authn.sms.allowed-prefixes", Tenant: "", Value: ""}}

	w := r.do(http.MethodGet, "/api/v1/system/settings?layer=global", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the request ended with %d: %s", w.Code, w.Body.String())
	}
	var views []SettingsAdminSetting
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
}

// A sensitive item masks its text but not its row's existence: a request for a
// sensitive item whose layer holds no row reports has_row false, and one whose
// layer holds a row reports true.
func TestASensitiveItemStillReportsWhetherItsRowExists(t *testing.T) {
	cases := []struct {
		name string
		rows []settings.Entry
		want bool
	}{
		{"no row", nil, false},
		{"a row", []settings.Entry{{Key: "authn.sms.secret", Tenant: "", Value: "hunter2"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = []settings.DeclaredItem{
				decl("authn.sms.secret", "authn.sms", reflect.TypeFor[string](), "", settings.Item{Sensitive: true}),
			}
			r.store.values["authn.sms.secret"] = "hunter2"
			r.store.rows = tc.rows

			w := r.do(http.MethodGet, "/api/v1/system/settings?layer=global", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("the request ended with %d: %s", w.Code, w.Body.String())
			}
			var views []SettingsAdminSetting
			decode(t, w, &views)
			if len(views) != 1 {
				t.Fatalf("the listing holds %d items, want 1", len(views))
			}
			if views[0].Written != maskedValue {
				t.Errorf("written is %q, want the mask", views[0].Written)
			}
			if views[0].HasRow != tc.want {
				t.Errorf("has_row is %t, want %t", views[0].HasRow, tc.want)
			}
		})
	}
}

// The namespace route shows one module's items and refuses a namespace no
// declared item belongs to with a bare 404.
func TestANamespaceGroupIsShownAndAnEmptyOneIsA404(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("ratelimit.window", "ratelimit", reflect.TypeFor[int](), "1", settings.Item{}),
		decl("authn.session.idle-timeout", "authn.session", reflect.TypeFor[int](), "30", settings.Item{}),
	}
	r.store.values["ratelimit.window"] = 5
	r.store.values["authn.session.idle-timeout"] = 60

	t.Run("one module's group", func(t *testing.T) {
		w := r.do(http.MethodGet, "/api/v1/system/settings/ratelimit?layer=global", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("the request ended with %d: %s", w.Code, w.Body.String())
		}
		var views []SettingsAdminSetting
		decode(t, w, &views)
		if len(views) != 1 || views[0].Key != "ratelimit.window" {
			t.Fatalf("the group holds %+v, want only ratelimit.window", views)
		}
	})
	t.Run("a namespace with a module name that carries a dot", func(t *testing.T) {
		// The namespace is the module name, which may contain a dot: the route
		// parameter matches it whole, and the key is the rest under it.
		w := r.do(http.MethodGet, "/api/v1/system/settings/authn.session?layer=global", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("the request ended with %d: %s", w.Code, w.Body.String())
		}
		var views []SettingsAdminSetting
		decode(t, w, &views)
		if len(views) != 1 || views[0].Key != "authn.session.idle-timeout" {
			t.Fatalf("the group holds %+v, want only the session item", views)
		}
	})
	t.Run("no declared item belongs to it", func(t *testing.T) {
		w := r.do(http.MethodGet, "/api/v1/system/settings/ghost?layer=global", nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("the request ended with %d, want 404", w.Code)
		}
		if w.Body.Len() != 0 {
			t.Errorf("the 404 carried the body %q, want none: it names no problem type", w.Body.String())
		}
	})
}

// A sensitive item's value never reaches a client: its default is empty and its
// written and value carry the fixed mask string, on every read route.
func TestASensitiveItemsValueIsMasked(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("authn.sms.secret", "authn.sms", reflect.TypeFor[string](), "", settings.Item{Sensitive: true}),
	}
	r.store.values["authn.sms.secret"] = "hunter2"
	r.store.rows = []settings.Entry{{Key: "authn.sms.secret", Tenant: "", Value: "hunter2"}}

	for _, target := range []string{
		"/api/v1/system/settings?layer=global",
		"/api/v1/system/settings/authn.sms?layer=global",
	} {
		w := r.do(http.MethodGet, target, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s ended with %d: %s", target, w.Code, w.Body.String())
		}
		var views []SettingsAdminSetting
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
		if v.Written != maskedValue || v.Value != maskedValue {
			t.Errorf("%s: written/value are %q/%q, want the mask %q", target, v.Written, v.Value, maskedValue)
		}
		if v.Value == "hunter2" || v.Written == "hunter2" {
			t.Errorf("%s: the credential reached the response", target)
		}
	}
}

// A write lands a 204 and passes the layer, the full key, the body's value and
// the caller's account on to the settings module, and it writes one mandatory
// audit row before the action.
func TestAWriteLands204AndPassesTheActionOn(t *testing.T) {
	r := newRig(t)
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

	rows := r.audit.entries()
	if len(rows) != 1 {
		t.Fatalf("the write wrote %d audit rows before the action, want one mandatory row", len(rows))
	}
	if rows[0].Action != actionSet || rows[0].Object != "app.mode" || rows[0].Outcome != outcomeSuccess {
		t.Errorf("the mandatory row is %+v", rows[0])
	}
	if rows[0].Detail[detailKey] != "app.mode" || rows[0].Detail[detailLayer] != layerTenant || rows[0].Detail[detailTenant] != "acme" {
		t.Errorf("the mandatory row's detail is %v, want the key, layer and tenant", rows[0].Detail)
	}
	if rows[0].Detail[detailActionID] == "" {
		t.Error("the mandatory row carries no action identifier")
	}
}

// A write by an impersonating administrator is attributed to the administrator,
// not to the account impersonated. The actor the settings module records is the
// caller's accountability key, Identity.Key(), which is the hands on the request
// and not the account the request is taken to be from; the same holds on the
// delete route.
func TestAnImpersonatedWriteIsAttributedToTheActor(t *testing.T) {
	impersonation := []authntest.Option{
		authntest.WithAccount(authn.AccountID("carol")),
		authntest.WithActor(authn.AccountID("admin-7")),
		authntest.WithSession(authn.SessionID("0198f0aa-0000-7000-8000-000000000001")),
		authntest.WithScope("acme"),
	}
	declared := []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}

	actorOf := func(calls []storeCall, op string) (string, bool) {
		for _, c := range calls {
			if c.op == op {
				return c.actor, true
			}
		}
		return "", false
	}

	t.Run("set", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = declared
		w := r.do(http.MethodPut, "/api/v1/system/settings/app/mode?layer=tenant&tenant=acme",
			map[string]any{"value": "manual"}, impersonation...)
		if w.Code != http.StatusNoContent {
			t.Fatalf("the request ended with %d: %s", w.Code, w.Body.String())
		}
		actor, ok := actorOf(r.store.recorded(), "Set")
		if !ok {
			t.Fatal("the settings module was never asked to write")
		}
		if actor != "admin-7" {
			t.Errorf("the write recorded actor %q, want the impersonating administrator admin-7", actor)
		}
		if actor == "carol" {
			t.Error("the write was attributed to the account impersonated")
		}
	})

	t.Run("delete", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = declared
		w := r.do(http.MethodDelete, "/api/v1/system/settings/app/mode?layer=tenant&tenant=acme", nil,
			impersonation...)
		if w.Code != http.StatusNoContent {
			t.Fatalf("the request ended with %d: %s", w.Code, w.Body.String())
		}
		actor, ok := actorOf(r.store.recorded(), "Delete")
		if !ok {
			t.Fatal("the settings module was never asked to delete")
		}
		if actor != "admin-7" {
			t.Errorf("the delete recorded actor %q, want the impersonating administrator admin-7", actor)
		}
		if actor == "carol" {
			t.Error("the delete was attributed to the account impersonated")
		}
	})
}

// A write whose key has no declaration is a 404 and reaches neither the audit
// nor the settings module.
func TestAWriteOfAnUndeclaredKeyIsA404(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}
	w := r.do(http.MethodPut, "/api/v1/system/settings/app/ghost?layer=global", map[string]any{"value": "on"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("the request ended with %d, want 404", w.Code)
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Errorf("an undeclared key reached the audit: %v", got)
	}
	if got := r.store.ops(); len(got) != 0 {
		t.Errorf("an undeclared key reached the settings surface: %v", got)
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
			w := r.do(http.MethodPut, "/api/v1/system/settings/app/mode?layer=global", map[string]any{"value": 1})
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
			w := r.do(http.MethodPut, "/api/v1/system/settings/app/mode?layer=global", map[string]any{"value": "on"})
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
	w := r.do(http.MethodPut, "/api/v1/system/settings/app/mode?layer=global", map[string]any{"value": "on"})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("the request ended with %d, want 503", w.Code)
	}
	if got := r.store.ops(); len(got) != 0 {
		t.Errorf("the action ran (%v) after its mandatory row did not land", got)
	}
}

// The delete route drops a declared key's row on the selected layer and writes
// its own mandatory audit row before the action. An undeclared key is a 404 and
// a failure the module returns is a 503.
func TestTheDeleteRoute(t *testing.T) {
	t.Run("landed", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = []settings.DeclaredItem{
			decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
		}
		w := r.do(http.MethodDelete, "/api/v1/system/settings/app/mode?layer=global", nil)
		if w.Code != http.StatusNoContent {
			t.Fatalf("the request ended with %d: %s", w.Code, w.Body.String())
		}
		if !r.store.holdsLayerKey("Delete", settings.Global(), "app.mode") {
			t.Errorf("the settings module was not asked to delete app.mode on the global layer: %v", r.store.recorded())
		}
		rows := r.audit.entries()
		if len(rows) != 1 || rows[0].Action != actionDelete || rows[0].Object != "app.mode" {
			t.Fatalf("the delete's audit rows are %+v, want one settings.delete row", rows)
		}
	})
	t.Run("undeclared key", func(t *testing.T) {
		r := newRig(t)
		w := r.do(http.MethodDelete, "/api/v1/system/settings/app/ghost?layer=global", nil)
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
		w := r.do(http.MethodDelete, "/api/v1/system/settings/app/mode?layer=global", nil)
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
	w := r.doRaw(http.MethodPut, "/api/v1/system/settings/app/mode?layer=global", "{")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("the request ended with %d, want 400", w.Code)
	}
	if got := r.store.ops(); len(got) != 0 {
		t.Errorf("an unreadable body reached the settings surface: %v", got)
	}
}

// A failure to read the layer's rows or to resolve a value is a 503: a listing
// that could not read a row must not answer as though the row were not there.
func TestAListingFailureIsA503(t *testing.T) {
	t.Run("the rows", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = []settings.DeclaredItem{
			decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
		}
		r.store.listErr = errors.New("the database is unreachable")
		w := r.do(http.MethodGet, "/api/v1/system/settings?layer=global", nil)
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
		w := r.do(http.MethodGet, "/api/v1/system/settings?layer=global", nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("the request ended with %d, want 503", w.Code)
		}
	})
}
