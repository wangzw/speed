package admin

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// The first cases drive each route over the real HTTP surface the rig mounts,
// with the identity a caller carries, and hold both the answer and what reached
// the roster.

func TestTheListAnswersEveryTenantInRosterOrder(t *testing.T) {
	r := newRig(t)
	r.catalog.list = []tenant.Tenant{
		{ID: "t_acme", Name: "acme", Status: tenant.StatusActive},
		{ID: "t_globex", Name: "globex", Status: tenant.StatusDisabled},
	}

	w := r.do(http.MethodGet, pathTenants, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var got struct {
		Tenants []tenantView `json:"tenants"`
	}
	decode(t, w, &got)
	if len(got.Tenants) != 2 ||
		got.Tenants[0].Name != "acme" || got.Tenants[1].Name != "globex" {
		t.Fatalf("tenants = %+v, want acme then globex, in the roster's order", got.Tenants)
	}
	if got.Tenants[0].Status != "active" || got.Tenants[1].Status != "disabled" {
		t.Errorf("statuses = %q, %q; want active, disabled", got.Tenants[0].Status, got.Tenants[1].Status)
	}
}

func TestTheGetAnswersOneTenant(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
	r.catalog.found = true

	w := r.do(http.MethodGet, pathTenants+"/t_acme", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var got struct {
		Tenant tenantView `json:"tenant"`
	}
	decode(t, w, &got)
	if got.Tenant.Name != "acme" || got.Tenant.Status != "active" || got.Tenant.Kind != "business" {
		t.Errorf("tenant = %+v, want acme active and business", got.Tenant)
	}
	calls := r.catalog.recorded()
	if len(calls) != 1 || calls[0].op != "Get" || calls[0].id != "t_acme" {
		t.Fatalf("roster calls = %+v, want one Get of t_acme", calls)
	}
}

// A tenant the roster does not hold is a bare 404: no problem type, so the
// answer says something is not there and not which refusal it was.
func TestTheGetOfAnUnknownTenantIs404(t *testing.T) {
	r := newRig(t)
	w := r.do(http.MethodGet, pathTenants+"/t_ghost", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Errorf("a 404 carried a body %q, want a bare status", w.Body.String())
	}
}

// Names are immutable after creation and the name-change route is gone: a
// PATCH on a tenant path reaches no handler, so the request is a bare 404 and
// nothing reaches the roster or the audit.
func TestTheNameChangeRouteIsGone(t *testing.T) {
	r := newRig(t)
	w := r.do(http.MethodPatch, pathTenants+"/t_acme", map[string]string{"name": "acme-labs"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", w.Code, w.Body.String())
	}
	if calls := r.catalog.recorded(); len(calls) != 0 {
		t.Errorf("roster calls = %+v, want none: no route reads the roster", calls)
	}
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("audit rows = %+v, want none", rows)
	}
}

func TestTheCreateAnswersCreatedAndPassesTheRowOn(t *testing.T) {
	r := newRig(t)
	w := r.do(http.MethodPost, pathTenants, createBody{Name: "acme"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", w.Code, w.Body.String())
	}
	var got struct {
		Tenant tenantView `json:"tenant"`
	}
	decode(t, w, &got)
	if got.Tenant.Name != "acme" || got.Tenant.Status != "active" {
		t.Errorf("tenant = %+v, want the registration, active", got.Tenant)
	}
	// The answer carries the creation instant the roster recorded, not the zero
	// value of the value this call assembled.
	if !got.Tenant.CreatedAt.Equal(provisionedAt) {
		t.Errorf("createdAt = %v, want the roster's %v", got.Tenant.CreatedAt, provisionedAt)
	}
	calls := r.provisioner.recorded()
	if len(calls) != 1 {
		t.Fatalf("the provisioner was asked %+v, want one registration", calls)
	}
	want := tenant.Tenant{
		Name: "acme", Status: tenant.StatusActive,
		OwnerAccount: authntest.DefaultAccount.String(),
	}
	if calls[0].tenant.Name != want.Name || calls[0].tenant.Status != want.Status ||
		calls[0].tenant.OwnerAccount != want.OwnerAccount {
		t.Errorf("the row passed on = %+v, want name, state and owner from %+v", calls[0].tenant, want)
	}
	if !strings.HasPrefix(string(calls[0].tenant.ID), "org-") {
		t.Errorf("the generated identifier is %q, want the org- prefix", calls[0].tenant.ID)
	}
}

func TestTheStatusChangePassesTheTargetOn(t *testing.T) {
	r := newRig(t)
	w := r.do(http.MethodPost, pathTenants+"/t_acme/status", statusBody{Status: "disabled"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body.String())
	}
	calls := r.catalog.recorded()
	if len(calls) != 1 || calls[0].op != "SetStatus" ||
		calls[0].id != "t_acme" || calls[0].status != tenant.StatusDisabled {
		t.Fatalf("roster calls = %+v, want one SetStatus(t_acme, disabled)", calls)
	}
}

// The decision is asked per method — a read needs tenant.read and a write needs
// tenant.manage — and it is asked in the platform domain, so the request's own
// tenant never steers it.
func TestTheDecisionIsAskedPerMethodInThePlatformDomain(t *testing.T) {
	cases := map[string]struct {
		method string
		target string
		body   any
		want   authz.Permission
	}{
		"list":             {http.MethodGet, pathTenants, nil, PermissionTenantRead},
		"get":              {http.MethodGet, pathTenants + "/t", nil, PermissionTenantRead},
		"create":           {http.MethodPost, pathTenants, createBody{Name: "abc"}, PermissionTenantManage},
		"request transfer": {http.MethodPost, pathTenants + "/t/owner/requests", map[string]any{"account": "acc-2"}, PermissionTenantManage},
		"pending transfer": {http.MethodGet, pathTenants + "/t/owner/requests/pending", nil, PermissionTenantRead},
		"delete":           {http.MethodDelete, pathTenants + "/t", nil, PermissionTenantManage},
		"status":           {http.MethodPost, pathTenants + "/t/status", statusBody{Status: "active"}, PermissionTenantManage},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			g := newRig(t)
			g.do(tc.method, tc.target, tc.body)
			asked := g.guard.askedFor()
			if len(asked) == 0 {
				t.Fatalf("the guard was not asked, want [%q]", tc.want)
			}
			for _, perm := range asked {
				if perm != tc.want {
					t.Fatalf("the guard was asked %v, want only %q", asked, tc.want)
				}
			}
			domains := g.guard.domainsAsked()
			if len(domains) == 0 {
				t.Fatalf("the decision named no domain, want %q", authz.PlatformDomain)
			}
			for _, domain := range domains {
				if domain != authz.PlatformDomain {
					t.Fatalf("the decision was made in %v, want only %q", domains, authz.PlatformDomain)
				}
			}
		})
	}
}

// A request with no identity is a 401 with the realm challenge, the same answer
// the admission and authorization layers write, and it reaches neither the guard
// nor the roster nor the audit.
func TestARequestWithNoIdentityIsA401(t *testing.T) {
	for name, tc := range map[string]struct {
		method string
		target string
		body   any
	}{
		"list":   {http.MethodGet, pathTenants, nil},
		"get":    {http.MethodGet, pathTenants + "/t", nil},
		"create": {http.MethodPost, pathTenants, createBody{Name: "abc"}},
		"status": {http.MethodPost, pathTenants + "/t/status", statusBody{Status: "active"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			w := r.doAnonymous(tc.method, tc.target)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", w.Code)
			}
			if got := w.Header().Get("WWW-Authenticate"); got != `Bearer realm="public", Cookie realm="public"` {
				t.Errorf("WWW-Authenticate = %q, want the realm challenge", got)
			}
			if len(r.catalog.recorded()) != 0 {
				t.Error("an anonymous request reached the roster")
			}
			if len(r.audit.entries()) != 0 {
				t.Error("an anonymous request wrote an audit row")
			}
			if len(r.guard.askedFor()) != 0 {
				t.Error("an anonymous request asked the guard")
			}
		})
	}
}

// A guard refusal is answered by the guard — a 403 for a denial and a 503 for an
// outage — and reaches no roster and no audit.
func TestAGuardRefusalReachesNoRoster(t *testing.T) {
	t.Run("denial", func(t *testing.T) {
		r := newRig(t)
		r.guard.allow = false
		w := r.do(http.MethodPost, pathTenants, createBody{Name: "abc"})
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", w.Code)
		}
		if len(r.catalog.recorded()) != 0 || len(r.audit.entries()) != 0 {
			t.Error("a denied request reached the roster or wrote an audit row")
		}
	})
	t.Run("outage", func(t *testing.T) {
		r := newRig(t)
		r.guard.err = errors.New("engine down")
		w := r.do(http.MethodPost, pathTenants, createBody{Name: "abc"})
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", w.Code)
		}
		if len(r.catalog.recorded()) != 0 {
			t.Error("a request whose decision failed reached the roster")
		}
	})
}

// A write whose mandatory audit row does not land is a 503 and the action is not
// carried out: the roster is not reached and no failed row is written beside a
// row that was never written.
func TestTheMandatoryAuditLandingIsRequired(t *testing.T) {
	writes := map[string]struct {
		method string
		target string
		body   any
	}{
		"create": {http.MethodPost, pathTenants, createBody{Name: "abc"}},
		"status": {http.MethodPost, pathTenants + "/t/status", statusBody{Status: "disabled"}},
	}
	for name, tc := range writes {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.catalog.found = true
			r.audit.fail(errors.New("audit store down"))
			w := r.do(tc.method, tc.target, tc.body)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503 (body %s)", w.Code, w.Body.String())
			}
			for _, call := range r.catalog.recorded() {
				if call.op != "Get" {
					t.Fatalf("the action ran though its mandatory audit row did not land: %+v", r.catalog.recorded())
				}
			}
			if calls := r.provisioner.recorded(); len(calls) != 0 {
				t.Fatalf("the registration ran though its mandatory audit row did not land: %+v", calls)
			}
			if len(r.audit.entries()) != 0 {
				t.Errorf("a failed row was written beside a mandatory row that never landed: %+v", r.audit.entries())
			}
		})
	}
}

// A write the roster refuses is closed with a failed row linked to the mandatory
// one by the action identifier, and the answer is the mapped one.
func TestAFailedWriteClosesWithALinkedRow(t *testing.T) {
	r := newRig(t)
	r.provisioner.fail(tenant.ErrTenantExists)
	w := r.do(http.MethodPost, pathTenants, createBody{Name: "acme"})
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", w.Code, w.Body.String())
	}
	rows := r.audit.entries()
	if len(rows) != 2 {
		t.Fatalf("audit rows = %+v, want a mandatory row and a failed row", rows)
	}
	if rows[0].Outcome != outcomeSuccess || rows[1].Outcome != outcomeFailed {
		t.Fatalf("outcomes = %q, %q; want success then failed", rows[0].Outcome, rows[1].Outcome)
	}
	first, second := rows[0].Detail[detailActionID], rows[1].Detail[detailActionID]
	if first == "" || first != second {
		t.Errorf("action ids = %q, %q; want one non-empty value shared by both rows", first, second)
	}
	if rows[0].Action != actionTenantCreate || rows[1].Action != actionTenantCreate {
		t.Errorf("actions = %q, %q; want %q", rows[0].Action, rows[1].Action, actionTenantCreate)
	}
	if rows[0].Object != "acme" {
		t.Errorf("object = %q, want the tenant name acme", rows[0].Object)
	}
}

// Each write route carries its own audit action, one per route, and a read
// writes no audit row at all.
func TestEachWriteCarriesItsOwnAuditAction(t *testing.T) {
	cases := map[string]struct {
		method string
		target string
		body   any
		want   string
	}{
		"create": {http.MethodPost, pathTenants, createBody{Name: "abc"}, actionTenantCreate},
		"status": {http.MethodPost, pathTenants + "/t/status", statusBody{Status: "disabled"}, actionTenantStatus},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.catalog.found = true
			r.do(tc.method, tc.target, tc.body)
			rows := r.audit.entries()
			if len(rows) != 1 || rows[0].Action != tc.want {
				t.Fatalf("audit rows = %+v, want one row with action %q", rows, tc.want)
			}
		})
	}

	for name, target := range map[string]string{
		"list": pathTenants,
		"get":  pathTenants + "/t",
	} {
		t.Run(name+" writes none", func(t *testing.T) {
			r := newRig(t)
			r.do(http.MethodGet, target, nil)
			if len(r.audit.entries()) != 0 {
				t.Errorf("a read wrote an audit row: %+v", r.audit.entries())
			}
		})
	}
}

// The refusal mapping is closed and fail-closed: each sentinel the roster
// returns reaches its status, and a failure no sentinel names is a 503 whose
// cause stays in the log.
func TestTheFailureMappingIsClosed(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		for name, tc := range map[string]struct {
			err    error
			status int
			typ    string
		}{
			"an identifier the table holds": {tenant.ErrTenantExists, http.StatusConflict, typePrefix + conflictCode},
			"a name another holds":          {tenant.ErrNameConflict, http.StatusConflict, typePrefix + conflictCode},
			"an unknown failure":            {errors.New("boom"), http.StatusServiceUnavailable, ""},
		} {
			t.Run(name, func(t *testing.T) {
				r := newRig(t)
				r.provisioner.fail(tc.err)
				w := r.do(http.MethodPost, pathTenants, createBody{Name: "abc"})
				if w.Code != tc.status {
					t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.status, w.Body.String())
				}
				if tc.typ != "" {
					if got := problemOf(t, w); got.Type != tc.typ {
						t.Errorf("problem type = %q, want %q", got.Type, tc.typ)
					}
				}
			})
		}
	})

	t.Run("status", func(t *testing.T) {
		for name, tc := range map[string]struct {
			err    error
			status int
			typ    string
		}{
			"an unknown tenant":  {tenant.ErrInvalidTenant, http.StatusNotFound, ""},
			"a tombstone":        {tenant.ErrTenantDeleted, http.StatusConflict, typePrefix + conflictCode},
			"an unknown failure": {errors.New("boom"), http.StatusServiceUnavailable, ""},
		} {
			t.Run(name, func(t *testing.T) {
				r := newRig(t)
				r.catalog.setStatusErr = tc.err
				w := r.do(http.MethodPost, pathTenants+"/t/status", statusBody{Status: "disabled"})
				if w.Code != tc.status {
					t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.status, w.Body.String())
				}
				if tc.typ != "" {
					if got := problemOf(t, w); got.Type != tc.typ {
						t.Errorf("problem type = %q, want %q", got.Type, tc.typ)
					}
				}
			})
		}
	})

	t.Run("list", func(t *testing.T) {
		r := newRig(t)
		r.catalog.fail(errors.New("store down"))
		w := r.do(http.MethodGet, pathTenants, nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", w.Code)
		}
		if !strings.Contains(r.logs.String(), "store down") {
			t.Error("the cause of the 503 did not reach the log")
		}
	})
}

// A shape refusal happens after the decision and before the action: a blank
// identifier or name, a body that cannot be read, and a status outside the
// closed set are 400s, and neither the roster nor the audit is reached.
func TestAShapeRefusalReachesNeitherTheRosterNorTheAudit(t *testing.T) {
	cases := map[string]struct {
		method string
		target string
		body   any
	}{
		"create carrying an identifier":            {http.MethodPost, pathTenants, map[string]any{"name": "acme", "id": "t_acme"}},
		"create with a blank name":                 {http.MethodPost, pathTenants, createBody{Name: "a b"}},
		"create with a short name":                 {http.MethodPost, pathTenants, createBody{Name: "ab"}},
		"create with an underscored name":          {http.MethodPost, pathTenants, createBody{Name: "a_b"}},
		"create with a body it cannot read":        {http.MethodPost, pathTenants, "not-an-object"},
		"delete with a blank identifier":           {http.MethodDelete, pathTenants + "/t%20acme", nil},
		"transfer request with an invalid account": {http.MethodPost, pathTenants + "/t_acme/owner/requests", map[string]any{"account": "not an account"}},
		"status outside the closed set":            {http.MethodPost, pathTenants + "/t_acme/status", statusBody{Status: "pending"}},
		"status the closed set keeps for deletion": {
			http.MethodPost, pathTenants + "/t_acme/status", statusBody{Status: "deleted"},
		},
		"status with a blank identifier": {http.MethodPost, pathTenants + "/t%20acme/status", statusBody{Status: "active"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.catalog.found = true
			w := r.do(tc.method, tc.target, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
			}
			if got := problemOf(t, w); got.Type != typePrefix+badRequestCode {
				t.Errorf("problem type = %q, want %q (body %s)", got.Type, typePrefix+badRequestCode, w.Body.String())
			}
			for _, call := range r.catalog.recorded() {
				if call.op != "Get" {
					t.Errorf("a shape-refused request reached the roster: %+v", call)
				}
			}
			if calls := r.provisioner.recorded(); len(calls) != 0 {
				t.Errorf("a shape-refused registration reached the provisioner: %+v", calls)
			}
			if len(r.audit.entries()) != 0 {
				t.Error("a shape-refused request wrote an audit row")
			}
		})
	}

	t.Run("create with a body that is not JSON", func(t *testing.T) {
		r := newRig(t)
		w := r.doRaw(http.MethodPost, pathTenants, "{not json")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
		}
		if len(r.catalog.recorded()) != 0 || len(r.audit.entries()) != 0 {
			t.Error("a request whose body could not be read reached the roster or the audit")
		}
	})
}

// The view carries the fields the design names, per state, spelled the way
// tenant.Status and tenant.DisabledBy spell them: the name, the kind, the owner
// account and the state's name on every row, and disabledBy only on a disabled
// one. It carries no identifier. A field added to a record later does not reach a
// client by being there, and a source never appears on a row whose state is not
// disabled.
func TestTheViewCarriesTheNamedFieldsPerState(t *testing.T) {
	createdAt := time.Date(2026, 7, 8, 9, 10, 11, 0, time.UTC)
	cases := map[string]struct {
		row        tenant.Tenant
		wantStatus string
		wantKeys   []string
		disabledBy string
	}{
		"active": {
			row: tenant.Tenant{
				ID: "t_acme", Name: "acme", Status: tenant.StatusActive,
				OwnerAccount: "acct-owner", CreatedAt: createdAt,
			},
			wantStatus: "active",
			wantKeys: []string{
				"name", "kind", "displayName", "description", "contactEmail", "website",
				"ownerAccount", "status", "createdAt",
			},
		},
		"disabled by an operator": {
			row: tenant.Tenant{
				ID: "t_acme", Name: "acme", Status: tenant.StatusDisabled,
				OwnerAccount: "acct-owner", DisabledBy: tenant.DisabledByOperator,
			},
			wantStatus: "disabled",
			wantKeys: []string{
				"name", "kind", "displayName", "description", "contactEmail", "website",
				"ownerAccount", "status", "createdAt", "disabledBy",
			},
			disabledBy: "operator",
		},
		"disabled by an account": {
			row: tenant.Tenant{
				ID: "t_acme", Name: "acme", Status: tenant.StatusDisabled,
				OwnerAccount: "acct-owner", DisabledBy: tenant.DisabledByAccount,
			},
			wantStatus: "disabled",
			wantKeys: []string{
				"name", "kind", "displayName", "description", "contactEmail", "website",
				"ownerAccount", "status", "createdAt", "disabledBy",
			},
			disabledBy: "account",
		},
		"deleted": {
			row:        tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusDeleted, OwnerAccount: "acct-owner"},
			wantStatus: "deleted",
			wantKeys:   []string{"name", "kind", "displayName", "description", "contactEmail", "website", "ownerAccount", "status", "createdAt"},
		},
		"a tenant with no owner": {
			row:        tenant.Tenant{ID: "t_system", Name: "system", Status: tenant.StatusActive},
			wantStatus: "active",
			wantKeys:   []string{"name", "kind", "displayName", "description", "contactEmail", "website", "ownerAccount", "status", "createdAt"},
		},
		"a status this build does not know": {
			row: tenant.Tenant{
				ID: "t_acme", Name: "acme", Status: tenant.Status(7),
				OwnerAccount: "acct-owner", DisabledBy: tenant.DisabledByOperator,
			},
			wantStatus: "disabled",
			wantKeys: []string{
				"name", "kind", "displayName", "description", "contactEmail", "website",
				"ownerAccount", "status", "createdAt", "disabledBy",
			},
			disabledBy: "operator",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.catalog.list = []tenant.Tenant{tc.row}
			w := r.do(http.MethodGet, pathTenants, nil)
			var raw struct {
				Tenants []map[string]any `json:"tenants"`
			}
			decode(t, w, &raw)
			if len(raw.Tenants) != 1 {
				t.Fatalf("tenants = %+v, want one", raw.Tenants)
			}
			got := raw.Tenants[0]
			if len(got) != len(tc.wantKeys) {
				t.Errorf("the view carries %d fields (%v), want exactly %v", len(got), got, tc.wantKeys)
			}
			for _, want := range tc.wantKeys {
				if _, ok := got[want]; !ok {
					t.Errorf("the view is missing %q", want)
				}
			}
			if owner, _ := got["ownerAccount"].(string); owner != tc.row.OwnerAccount {
				t.Errorf("ownerAccount = %q, want %q", owner, tc.row.OwnerAccount)
			}
			if status, _ := got["status"].(string); status != tc.wantStatus {
				t.Errorf("status = %q, want %q", status, tc.wantStatus)
			}
			if kind, _ := got["kind"].(string); kind != "business" {
				t.Errorf("kind = %q, want business with no kind resolver assembled", kind)
			}
			if tc.disabledBy == "" {
				if _, ok := got["disabledBy"]; ok {
					t.Errorf("disabledBy is on a row whose state is %q: %v", tc.wantStatus, got)
				}
			} else if source, _ := got["disabledBy"].(string); source != tc.disabledBy {
				t.Errorf("disabledBy = %q, want %q", source, tc.disabledBy)
			}
		})
	}
}

// TestThePlatformDetailCarriesTheOwner holds the one place the platform surface
// names the organization's owner: the single-tenant detail. The value is the
// roster row's owner column, not a value this module derives.
func TestThePlatformDetailCarriesTheOwner(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{
		ID: "t_acme", Name: "acme", Status: tenant.StatusActive, OwnerAccount: "acct-owner",
	}
	r.catalog.found = true

	w := r.do(http.MethodGet, pathTenants+"/t_acme", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var got struct {
		Tenant map[string]any `json:"tenant"`
	}
	decode(t, w, &got)
	if owner, _ := got.Tenant["ownerAccount"].(string); owner != "acct-owner" {
		t.Errorf("ownerAccount = %q, want acct-owner", owner)
	}
}
