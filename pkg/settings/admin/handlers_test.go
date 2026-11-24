package admin

import (
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/settings"
)

// TestTheLayerChoosesTheDomainAndThePermission checks that the read and write
// routes ask their permission in the domain the layer chooses.
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
		{"read tenant", http.MethodGet, "/api/v1/system/settings?layer=tenant&tenant=acme", nil, authz.PermissionTenantSettingsRead, authz.Domain("acme")},
		{"write global", http.MethodPut, "/api/v1/system/settings/app/mode?layer=global", map[string]any{"value": "on"}, authz.PermissionSettingsWrite, authz.PlatformDomain},
		{"write tenant", http.MethodPut, "/api/v1/system/settings/app/mode?layer=tenant&tenant=acme", map[string]any{"value": "on"}, authz.PermissionTenantSettingsWrite, authz.Domain("acme")},
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

// TestAnUnreadableLayerSelectionIsA400BeforeTheDecision checks that a bad layer
// selection is a 400 written before the guard is asked.
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

// TestNoIdentityIsA401WithTheRealmChallengeAndNoType checks that an
// identity-less request gets 401 and asks neither the guard nor the store.
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

// TestTheGuardWritesItsOwnRefusal checks that the guard writes its 403 or 503 and the store is untouched.
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

// TestTheListShowsEveryDeclaredItemWithItsResolvedValue checks the listing fields for each
// declared item: the type, description, default and written texts, and the resolved value.
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
	if window.Description != "the length of one counting window" {
		t.Errorf("the description is %q, want the declared text", window.Description)
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
	if limit.Description != "the limit" {
		t.Errorf("the limit's description is %q, want the declared text", limit.Description)
	}
	if limit.Domain != "" {
		t.Errorf("the limit's domain is %q, want empty: it declares none", limit.Domain)
	}
}

// TestAnEmptyTextRowIsReportedAsAHeldRow checks that an empty-text row is reported as held through has_row.
func TestAnEmptyTextRowIsReportedAsAHeldRow(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("authn.sms.allowed-prefixes", "authn.sms", reflect.TypeFor[string](), "[]", settings.Item{
			Description: "the number prefixes SMS may be sent to",
		}),
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
	if v.Description != "the number prefixes SMS may be sent to" {
		t.Errorf("the description is %q, want the declared text", v.Description)
	}
}

// TestASensitiveItemStillReportsWhetherItsRowExists checks that a sensitive item masks its text but still reports its row existence.
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
				decl("authn.sms.secret", "authn.sms", reflect.TypeFor[string](), "", settings.Item{
					Description: "the SMS provider's secret",
					Sensitive:   true,
				}),
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
			if views[0].Description != "the SMS provider's secret" {
				t.Errorf("the description is %q, want the declared text: masking blots out the text only", views[0].Description)
			}
		})
	}
}

// TestANamespaceGroupIsShownAndAnEmptyOneIsA404 checks the namespace route and its bare 404.
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

// TestASensitiveItemsValueIsMasked checks that a sensitive value never reaches a client.
func TestASensitiveItemsValueIsMasked(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("authn.sms.secret", "authn.sms", reflect.TypeFor[string](), "", settings.Item{
			Description: "the SMS provider's secret",
			Sensitive:   true,
		}),
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
		if v.Description != "the SMS provider's secret" {
			t.Errorf("%s: the description is %q, want the declared text", target, v.Description)
		}
		if v.Written != maskedValue || v.Value != maskedValue {
			t.Errorf("%s: written/value are %q/%q, want the mask %q", target, v.Written, v.Value, maskedValue)
		}
		if v.Value == "hunter2" || v.Written == "hunter2" {
			t.Errorf("%s: the credential reached the response", target)
		}
	}
}

// TestAMaskedWriteBackIsRefusedBeforeTheAuditAndTheStore checks that writing the
// mask back is refused with 422 before the audit and store.
func TestAMaskedWriteBackIsRefusedBeforeTheAuditAndTheStore(t *testing.T) {
	r := newRig(t)
	r.store.declared = []settings.DeclaredItem{
		decl("authn.sms.secret", "authn.sms", reflect.TypeFor[string](), "", settings.Item{
			Description: "the SMS provider's secret",
			Sensitive:   true,
		}),
	}
	r.store.values["authn.sms.secret"] = "hunter2"
	r.store.rows = []settings.Entry{{Key: "authn.sms.secret", Tenant: "", Value: "hunter2"}}

	w := r.do(http.MethodPut, "/api/v1/system/settings/authn.sms/secret?layer=global",
		map[string]any{"value": maskedValue})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("the request ended with %d, want 422: %s", w.Code, w.Body.String())
	}
	if p := problemOf(t, w); p.Type != "urn:speed:settings:masked-value" {
		t.Errorf("the problem type is %q, want the masked-value problem", p.Type)
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Errorf("a masked write landed %v, want no audit row", got)
	}
	if got := r.store.ops(); len(got) != 0 {
		t.Errorf("a masked write reached the settings store: %v", got)
	}
}

// TestAWriteLands204AndPassesTheActionOn checks that a write lands 204, passes
// its arguments to the settings module and writes one mandatory audit row.
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

// TestAnImpersonatedWriteIsAttributedToTheActor checks that an impersonated write is attributed to the administrator.
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

// TestAWriteOfAnUndeclaredKeyIsA404 checks that it reaches neither the audit nor the settings module.
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

// TestARefusedValueIsA422WithAFailedAuditRow checks that a refused value is a 422 and closes with a failed audit row.
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

// TestAWriteRefusedByTheModuleMapsToItsAnswer checks that an unknown key is a 404 and other failures a 503.
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

// TestTheMandatoryAuditRowGatesTheAction checks that a row that does not land answers 503 and the store is never called.
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

// TestTheDeleteRoute checks that the delete drops a declared key, writes its
// audit row, and refuses an undeclared key or a module failure.
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

// TestAnUnreadableBodyIsA400 checks that an unreadable body is a 400.
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

// TestAListingFailureIsA503 checks that a failed row read or value resolve is a 503.
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

// TestThePlatformSurfaceWritesANumericItemWithADomain checks that a text body
// and a JSON number both reach the store as the decoder's value.
func TestThePlatformSurfaceWritesANumericItemWithADomain(t *testing.T) {
	positive := settings.DomainOf("the positive integers", func(limit int) bool { return limit > 0 })
	shapes := []struct {
		name string
		body func(int) any
		want func(int) any
	}{
		{"the page's text", func(n int) any { return strconv.Itoa(n) }, func(n int) any { return strconv.Itoa(n) }},
		{"a JSON number", func(n int) any { return n }, func(n int) any { return float64(n) }},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = []settings.DeclaredItem{
				decl("org.create.max-organizations", "org.create", reflect.TypeFor[int](), "5",
					settings.Item{Description: "the active organization cap", Domain: positive}),
				decl("org.create.max-per-hour", "org.create", reflect.TypeFor[int](), "3",
					settings.Item{Description: "the hourly creation cap", Domain: positive}),
			}

			written := map[string]int{
				"max-organizations": 10,
				"max-per-hour":      6,
			}
			for key, value := range written {
				w := r.do(http.MethodPut, "/api/v1/system/settings/org.create/"+key+"?layer=global",
					map[string]any{"value": shape.body(value)})
				if w.Code != http.StatusNoContent {
					t.Fatalf("writing %s = %v ended with %d: %s", key, shape.body(value), w.Code, w.Body.String())
				}
			}

			writes := 0
			for _, call := range r.store.recorded() {
				if call.op != "Set" {
					continue
				}
				writes++
				if call.layer != settings.Global() {
					t.Errorf("%s was written on %v, want the global layer", call.key, call.layer)
				}
				key := strings.TrimPrefix(call.key, "org.create.")
				value, known := written[key]
				if !known {
					t.Errorf("the surface wrote %q, want one of the declared keys", call.key)
					continue
				}
				want := shape.want(value)
				if !reflect.DeepEqual(call.value, want) {
					t.Errorf("%s was written as %T %v, want %T %v",
						call.key, call.value, call.value, want, want)
				}
			}
			if writes != 2 {
				t.Errorf("the surface wrote %d item(s), want two", writes)
			}
		})
	}
}
