package tenantadmin

import (
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/settings"
)

// TestThePathSettlesTheLayerAndThePermission checks that the path tenant fixes
// the layer and the decision is made in that tenant's domain.
func TestThePathSettlesTheLayerAndThePermission(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
		body   any
		perm   authz.Permission
	}{
		{"read all", http.MethodGet, base, nil, authz.PermissionTenantSettingsRead},
		{"write", http.MethodPut, base + "/tenant.admission/require-mfa", map[string]any{"value": true}, authz.PermissionTenantSettingsWrite},
		{"delete", http.MethodDelete, base + "/tenant.admission/require-sso", nil, authz.PermissionTenantSettingsWrite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = admissionDeclared()
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

func asksLayer(calls []storeCall, layer settings.Layer) bool {
	for _, c := range calls {
		if c.layer == layer {
			return true
		}
	}
	return false
}

// TestALayerOrTenantQueryParameterIsA400AfterTheDecision checks that either
// parameter is a 400 written after the permission decision.
func TestALayerOrTenantQueryParameterIsA400AfterTheDecision(t *testing.T) {
	cases := map[string]struct {
		method string
		target string
		body   any
	}{
		"layer global":       {http.MethodGet, base + "?layer=global", nil},
		"layer tenant":       {http.MethodGet, base + "?layer=tenant&tenant=acme", nil},
		"tenant only":        {http.MethodGet, base + "?tenant=other", nil},
		"write with layer":   {http.MethodPut, base + "/tenant.admission/require-mfa?layer=global", map[string]any{"value": true}},
		"delete with tenant": {http.MethodDelete, base + "/tenant.admission/require-sso?tenant=acme", nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = admissionDeclared()
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

// TestThePermissionDecisionPrecedesTheQueryRefusal checks that the guard refusal wins over the query refusal.
func TestThePermissionDecisionPrecedesTheQueryRefusal(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	w := r.do(http.MethodGet, base+"?layer=global", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("the request ended with %d, want the guard's 403", w.Code)
	}
}

// TestNoIdentityIsA401WithTheRealmChallengeAndNoType checks that an
// identity-less request gets 401 and asks neither the guard nor the store.
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

// TestTheGuardWritesItsOwnRefusal checks that the guard writes its 403 or 503 and the store is untouched.
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

// TestAPathTenantOutsideTheIdentifierDomainIsA400 checks that a bad tenant is a 400 before a layer is built.
func TestAPathTenantOutsideTheIdentifierDomainIsA400(t *testing.T) {
	r := newRig(t)
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

// TestAWriteLands204AndPassesTheActionOn checks that a write lands 204, passes its arguments on and writes one mandatory audit row.
func TestAWriteLands204AndPassesTheActionOn(t *testing.T) {
	r := newRig(t)
	r.store.declared = admissionDeclared()
	w := r.do(http.MethodPut, base+"/tenant.admission/require-mfa", map[string]any{"value": true},
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
	if set.key != mfaKey {
		t.Errorf("the write named key %q, want %s", set.key, mfaKey)
	}
	if set.value != true {
		t.Errorf("the write carried value %v, want true", set.value)
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
	if rows[0].Action != actionSet || rows[0].Object != mfaKey || rows[0].Outcome != outcomeSuccess {
		t.Errorf("the mandatory row is %+v", rows[0])
	}
	if rows[0].Detail[detailKey] != mfaKey || rows[0].Detail[detailTenant] != "acme" {
		t.Errorf("the mandatory row's detail is %v, want the key and tenant", rows[0].Detail)
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
		"set":    {http.MethodPut, base + "/tenant.admission/require-mfa", map[string]any{"value": true}, "Set", actionSet},
		"delete": {http.MethodDelete, base + "/tenant.admission/require-sso", nil, "Delete", actionDelete},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = admissionDeclared()
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

// TestAWriteOfAnUndeclaredKeyIsA404 checks that it reaches neither the seam, the audit nor the store.
func TestAWriteOfAnUndeclaredKeyIsA404(t *testing.T) {
	r := newRig(t)
	r.store.declared = admissionDeclared()
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

// TestARefusedValueIsA422WithAFailedAuditRow checks that a refused value is a 422 and closes with a failed audit row.
func TestARefusedValueIsA422WithAFailedAuditRow(t *testing.T) {
	cases := map[string]error{
		"type mismatch": settings.ErrTypeMismatch,
		"invalid value": settings.ErrInvalidValue,
	}
	for name, cause := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = admissionDeclared()
			r.store.setErr = cause
			w := r.do(http.MethodPut, base+"/tenant.admission/require-mfa", map[string]any{"value": true})
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
			r.store.declared = admissionDeclared()
			r.store.setErr = tc.cause
			w := r.do(http.MethodPut, base+"/tenant.admission/require-mfa", map[string]any{"value": true})
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

// TestTheMandatoryAuditRowGatesTheAction checks that a row that does not land answers 503 and the store is never changed.
func TestTheMandatoryAuditRowGatesTheAction(t *testing.T) {
	r := newRig(t)
	r.store.declared = admissionDeclared()
	r.audit.fail(errors.New("the audit table is unreachable"))
	w := r.do(http.MethodPut, base+"/tenant.admission/require-mfa", map[string]any{"value": true})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("the request ended with %d, want 503", w.Code)
	}
	if got := r.store.recorded(); !noStoreWrites(got) {
		t.Errorf("the action ran after its mandatory row did not land: %v", got)
	}
}

// TestTheDeleteRoute checks that the delete drops a declared key, writes its
// audit row, and refuses an undeclared key or a module failure.
func TestTheDeleteRoute(t *testing.T) {
	t.Run("landed", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = admissionDeclared()
		w := r.do(http.MethodDelete, base+"/tenant.admission/require-mfa", nil)
		if w.Code != http.StatusNoContent {
			t.Fatalf("the request ended with %d: %s", w.Code, w.Body.String())
		}
		if !r.store.holdsLayerKey("Delete", settings.ForTenant("acme"), mfaKey) {
			t.Errorf("the settings module was not asked to delete %s on the acme layer: %v", mfaKey, r.store.recorded())
		}
		rows := r.audit.entries()
		if len(rows) != 1 || rows[0].Action != actionDelete || rows[0].Object != mfaKey {
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
		r.store.declared = admissionDeclared()
		r.store.deleteErr = errors.New("the database is unreachable")
		w := r.do(http.MethodDelete, base+"/tenant.admission/require-mfa", nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("the request ended with %d, want 503", w.Code)
		}
	})
}

// TestAnUnreadableBodyIsA400 checks that an unreadable body is a 400.
func TestAnUnreadableBodyIsA400(t *testing.T) {
	r := newRig(t)
	r.store.declared = admissionDeclared()
	w := r.doRaw(http.MethodPut, base+"/tenant.admission/require-mfa", "{")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("the request ended with %d, want 400", w.Code)
	}
	if got := r.store.ops(); len(got) != 0 {
		t.Errorf("an unreadable body reached the settings surface: %v", got)
	}
}

// TestAWriteSettingsRefusalIsProtected checks that a seam refusal is a 403 protected and the action does not run.
func TestAWriteSettingsRefusalIsProtected(t *testing.T) {
	r := newRig(t)
	r.store.declared = admissionDeclared()
	r.writeSeam.refuse()
	w := r.do(http.MethodPut, base+"/tenant.admission/require-mfa", map[string]any{"value": true})
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
	if got := r.store.recorded(); !noStoreWrites(got) {
		t.Errorf("a refused write changed a row: %v", got)
	}
}

// TestAWriteSettingsFailureIs503 checks that a seam failure is a 503 on both write routes.
func TestAWriteSettingsFailureIs503(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = admissionDeclared()
			r.writeSeam.fail(errors.New("the tenant roster is unreachable"))
			var body any
			if method == http.MethodPut {
				body = map[string]any{"value": true}
			}
			w := r.do(method, base+"/tenant.admission/require-mfa", body)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("the request ended with %d, want 503: %s", w.Code, w.Body.String())
			}
			if got := r.audit.entries(); len(got) != 0 {
				t.Errorf("an unanswered seam landed %d audit row(s), want none", len(got))
			}
			if got := r.store.recorded(); !noStoreWrites(got) {
				t.Errorf("an unanswered seam changed a row: %v", got)
			}
		})
	}
}

// TestAWriteToAReadOnlyItemIs403 checks that both write routes answer 403 read-only before the seam, the audit and the store.
func TestAWriteToAReadOnlyItemIs403(t *testing.T) {
	r := newRig(t)
	item := settings.Item{
		Description:    "a policy the platform alone writes",
		TitleKey:       "app.policy.title",
		DescriptionKey: "app.policy.description",
		Control:        settings.ControlSwitch,
		TenantAccess:   settings.TenantRead,
	}
	r.store.declared = []settings.DeclaredItem{
		decl("app.policy", "app", reflect.TypeFor[bool](), "false", item),
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		var body any
		if method == http.MethodPut {
			body = map[string]any{"value": true}
		}
		w := r.do(method, base+"/app/policy", body)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s ended with %d, want 403: %s", method, w.Code, w.Body.String())
			continue
		}
		if p := problemOf(t, w); p.Type != "urn:speed:settings:read-only" {
			t.Errorf("%s problem type = %q, want urn:speed:settings:read-only", method, p.Type)
		}
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Errorf("a read-only item reached the audit: %v", got)
	}
	if got := r.writeSeam.askedTenants(); len(got) != 0 {
		t.Errorf("a read-only item asked the seam about %v", got)
	}
	if got := r.store.ops(); len(got) != 0 {
		t.Errorf("a read-only write reached the settings store: %v", got)
	}
}
