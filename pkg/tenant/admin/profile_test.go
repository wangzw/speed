package admin

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// The profile route and the registration's profile fields: the platform
// surface carries the descriptive fields without letting them change anything
// the roster resolves requests by.

// profileWriteBody is the profile write's JSON body as a case sends it. The
// handler's declared input also carries the path's identifier, which JSON does
// not carry. The names are snake_case, the same shape the registration body
// uses.
type profileWriteBody struct {
	DisplayName  string `json:"display_name"`
	Description  string `json:"description"`
	ContactEmail string `json:"contact_email"`
	Website      string `json:"website"`
}

// fullProfile is every member present, which the profile route requires.
func fullProfile() profileWriteBody {
	return profileWriteBody{
		DisplayName:  "Acme",
		Description:  "The Acme corporation",
		ContactEmail: "ops@acme.test",
		Website:      "https://acme.test",
	}
}

// TestTheCreateCarriesTheProfile holds the registration's four optional fields
// on their way to the provisioner, trimmed the way the root package defines.
func TestTheCreateCarriesTheProfile(t *testing.T) {
	r := newRig(t)
	w := r.do(http.MethodPost, pathTenants, createBody{
		Name:         "acme",
		DisplayName:  "  Acme  ",
		Description:  "  The Acme corporation  ",
		ContactEmail: "  ops@acme.test ",
		Website:      " https://acme.test ",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", w.Code, w.Body.String())
	}
	calls := r.provisioner.recorded()
	if len(calls) != 1 {
		t.Fatalf("the provisioner was asked %+v, want one registration", calls)
	}
	want := tenant.Profile{
		DisplayName:  "Acme",
		Description:  "The Acme corporation",
		ContactEmail: "ops@acme.test",
		Website:      "https://acme.test",
	}
	if calls[0].tenant.Profile != want {
		t.Errorf("the profile passed on = %+v, want %+v", calls[0].tenant.Profile, want)
	}

	var got struct {
		Tenant tenantView `json:"tenant"`
	}
	decode(t, w, &got)
	if got.Tenant.DisplayName != want.DisplayName || got.Tenant.Description != want.Description ||
		got.Tenant.ContactEmail != want.ContactEmail || got.Tenant.Website != want.Website {
		t.Errorf("the answer carries %+v, want the profile %+v", got.Tenant, want)
	}
}

// A profile outside the root package's domain is refused before the audit and
// the provisioner: a registration that would store a website the caller was
// never shown does not run.
func TestTheCreateRefusesAnInvalidProfile(t *testing.T) {
	r := newRig(t)
	w := r.do(http.MethodPost, pathTenants, createBody{
		Name: "acme", Website: "javascript:alert(1)",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
	}
	if len(r.provisioner.recorded()) != 0 {
		t.Error("the provisioner was asked for a profile outside the domain")
	}
	if len(r.audit.entries()) != 0 {
		t.Error("the refused registration wrote an audit row")
	}
}

// TestTheProfileUpdateWritesOnlyTheProfile holds the write's seam: PATCH
// /tenants/:tenant/profile reads the tenant for the 404 and calls SetProfile,
// never Put or SetStatus, so it cannot undo a disable or a
// deletion another path committed.
func TestTheProfileUpdateWritesOnlyTheProfile(t *testing.T) {
	r := newRig(t)
	created := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive, CreatedAt: created}
	r.catalog.found = true

	w := r.do(http.MethodPatch, pathTenants+"/t_acme/profile", fullProfile())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}

	var ops []string
	for _, call := range r.catalog.recorded() {
		ops = append(ops, call.op)
	}
	if len(ops) != 2 || ops[0] != "Get" || ops[1] != "SetProfile" {
		t.Fatalf("roster calls = %v, want exactly [Get SetProfile]", ops)
	}
	last := r.catalog.recorded()[1]
	want := tenant.Profile{
		DisplayName:  "Acme",
		Description:  "The Acme corporation",
		ContactEmail: "ops@acme.test",
		Website:      "https://acme.test",
	}
	if last.profile != want {
		t.Errorf("SetProfile got %+v, want %+v", last.profile, want)
	}

	var got struct {
		Tenant tenantView `json:"tenant"`
	}
	decode(t, w, &got)
	if got.Tenant.DisplayName != want.DisplayName || got.Tenant.Description != want.Description ||
		got.Tenant.ContactEmail != want.ContactEmail || got.Tenant.Website != want.Website {
		t.Errorf("the answer carries %+v, want the written profile %+v", got.Tenant, want)
	}
	if !got.Tenant.CreatedAt.Equal(created) {
		t.Errorf("created_at = %v, want %v", got.Tenant.CreatedAt, created)
	}
}

// TestTheProfileUpdateWritesTheMandatoryAuditFirst holds the order: the
// permitted row lands before SetProfile, names the platform surface, and links
// to the failed row a refusal writes.
func TestTheProfileUpdateWritesTheMandatoryAuditFirst(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
	r.catalog.found = true
	rowsAtSetProfile := -1
	r.catalog.during = func() { rowsAtSetProfile = len(r.audit.entries()) }

	w := r.do(http.MethodPatch, pathTenants+"/t_acme/profile", fullProfile())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if rowsAtSetProfile != 1 {
		t.Errorf("%d audit row(s) had landed when SetProfile ran, want the mandatory one", rowsAtSetProfile)
	}
	rows := r.audit.entries()
	if len(rows) != 1 {
		t.Fatalf("audit rows = %+v, want one", rows)
	}
	if rows[0].Action != actionTenantProfileUpdate || rows[0].Object != "acme" ||
		rows[0].Outcome != outcomeSuccess {
		t.Errorf("the audit row = %+v, want a successful %q of acme", rows[0], actionTenantProfileUpdate)
	}
	if rows[0].Detail[detailSurface] != surfacePlatform || rows[0].Detail[detailTenant] != "acme" {
		t.Errorf("the audit detail = %v, want surface=platform and tenant=acme", rows[0].Detail)
	}
	if rows[0].Detail[detailActionID] == "" {
		t.Error("the audit row carries no action identifier")
	}
}

// A roster refusal closes the permitted row with a linked failed one carrying
// the same surface; a tombstone is the refusal this route maps to a 409.
func TestTheProfileUpdateOnATombstoneIsAConflict(t *testing.T) {
	r := newRig(t)
	r.catalog.found = true
	r.catalog.setProfileErr = tenant.ErrTenantDeleted

	w := r.do(http.MethodPatch, pathTenants+"/t_acme/profile", fullProfile())
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", w.Code, w.Body.String())
	}
	if p := problemOf(t, w); p.Type != typePrefix+conflictCode {
		t.Errorf("problem type = %q, want %q", p.Type, typePrefix+conflictCode)
	}
	rows := r.audit.entries()
	if len(rows) != 2 {
		t.Fatalf("audit rows = %+v, want the permitted and the failed one", rows)
	}
	if rows[0].Outcome != outcomeSuccess || rows[1].Outcome != outcomeFailed {
		t.Errorf("audit outcomes = %q, %q; want success, failed", rows[0].Outcome, rows[1].Outcome)
	}
	if rows[0].Detail[detailActionID] == "" || rows[0].Detail[detailActionID] != rows[1].Detail[detailActionID] {
		t.Errorf("the two rows are not linked: %q and %q",
			rows[0].Detail[detailActionID], rows[1].Detail[detailActionID])
	}
	if rows[1].Detail[detailSurface] != surfacePlatform {
		t.Errorf("the failed row's surface = %q, want platform", rows[1].Detail[detailSurface])
	}
}

// A mandatory audit row that does not land stops the write before SetProfile.
func TestTheProfileUpdateStopsWhenTheAuditDoesNotLand(t *testing.T) {
	r := newRig(t)
	r.catalog.found = true
	r.audit.fail(errors.New("the audit store is down"))

	w := r.do(http.MethodPatch, pathTenants+"/t_acme/profile", fullProfile())
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", w.Code, w.Body.String())
	}
	for _, call := range r.catalog.recorded() {
		if call.op == "SetProfile" {
			t.Error("the profile was written although its mandatory audit row did not land")
		}
	}
}

// The shape checks run before the roster read and the audit, and an unknown
// tenant is a bare 404 with no row written.
func TestTheProfileUpdateRefusesShapeAndMissingTenantsBeforeTheAudit(t *testing.T) {
	t.Run("invalid profile", func(t *testing.T) {
		r := newRig(t)
		r.catalog.found = true
		body := fullProfile()
		body.ContactEmail = "not an address"
		w := r.do(http.MethodPatch, pathTenants+"/t_acme/profile", body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
		}
		if len(r.catalog.recorded()) != 0 {
			t.Errorf("a shape refusal reached the roster: %+v", r.catalog.recorded())
		}
		if len(r.audit.entries()) != 0 {
			t.Error("a shape refusal wrote an audit row")
		}
	})
	t.Run("a member is absent", func(t *testing.T) {
		r := newRig(t)
		r.catalog.found = true
		w := r.do(http.MethodPatch, pathTenants+"/t_acme/profile", map[string]string{"display_name": "Acme"})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
		}
		if len(r.catalog.recorded()) != 0 {
			t.Errorf("a body without every member reached the roster: %+v", r.catalog.recorded())
		}
		if len(r.audit.entries()) != 0 {
			t.Error("a body without every member wrote an audit row")
		}
	})
	t.Run("unknown tenant", func(t *testing.T) {
		r := newRig(t)
		w := r.do(http.MethodPatch, pathTenants+"/t_ghost/profile", fullProfile())
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body %s)", w.Code, w.Body.String())
		}
		if w.Body.Len() != 0 {
			t.Errorf("a 404 carried a body %q, want a bare status", w.Body.String())
		}
		if len(r.audit.entries()) != 0 {
			t.Error("an unknown tenant wrote an audit row")
		}
	})
}

// The profile view carries the four descriptive fields and the mandatory
// creation time.
func TestTheTenantViewCarriesTheProfileAndTheCreationTime(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	r := newRig(t)
	r.catalog.held = tenant.Tenant{
		ID: "t_acme", Name: "acme", Status: tenant.StatusActive,
		Profile: tenant.Profile{
			DisplayName: "Acme", Description: "The Acme corporation",
			ContactEmail: "ops@acme.test", Website: "https://acme.test",
		},
		CreatedAt: created,
	}
	r.catalog.found = true

	w := r.do(http.MethodGet, pathTenants+"/t_acme", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var got struct {
		Tenant tenantView `json:"tenant"`
	}
	decode(t, w, &got)
	if got.Tenant.DisplayName != "Acme" || got.Tenant.Description != "The Acme corporation" ||
		got.Tenant.ContactEmail != "ops@acme.test" || got.Tenant.Website != "https://acme.test" {
		t.Errorf("the view = %+v, want the profile fields", got.Tenant)
	}
	if !got.Tenant.CreatedAt.Equal(created) {
		t.Errorf("created_at = %v, want %v", got.Tenant.CreatedAt, created)
	}
}

// The profile route is a management write: tenant.manage in the platform
// domain.
func TestTheProfileRouteAsksTenantManageInThePlatformDomain(t *testing.T) {
	r := newRig(t)
	r.catalog.found = true
	r.do(http.MethodPatch, pathTenants+"/t_acme/profile", fullProfile())

	asked := r.guard.askedFor()
	if len(asked) != 1 || asked[0] != PermissionTenantManage {
		t.Fatalf("the guard was asked %v, want exactly [%q]", asked, PermissionTenantManage)
	}
	domains := r.guard.domainsAsked()
	if len(domains) != 1 || domains[0] != authz.PlatformDomain {
		t.Fatalf("the decision was made in %v, want [%q]", domains, authz.PlatformDomain)
	}
}
