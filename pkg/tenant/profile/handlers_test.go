package profile

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// writeBody is the write request as a case builds it. The names are the JSON
// names the API declares: snake_case, as every management write body uses.
type writeBody struct {
	DisplayName  string `json:"display_name"`
	Description  string `json:"description"`
	ContactEmail string `json:"contact_email"`
	Website      string `json:"website"`
}

// validWrite is a body every field of which is admitted.
func validWrite() writeBody {
	return writeBody{
		DisplayName:  "New Name",
		Description:  "A new description.",
		ContactEmail: "hello@acme.test",
		Website:      "https://new.acme.test",
	}
}

// TestTheReadAnswersTheProfileWithoutTheDisableSource holds the read's view:
// every profile field beside the read-only identity and state, and no disable
// source, which belongs to the platform management surface.
func TestTheReadAnswersTheProfileWithoutTheDisableSource(t *testing.T) {
	r := newRig(t)
	r.catalog.held = sampleTenant()

	w := r.do(http.MethodGet, profileTarget, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", w.Code, w.Body.String())
	}
	var body struct {
		Profile TenantProfileView `json:"profile"`
	}
	decode(t, w, &body)
	got := body.Profile
	want := profileViewOf(sampleTenant())
	if !reflect.DeepEqual(got, want) {
		t.Errorf("profile = %+v, want %+v", got, want)
	}
	if !got.CreatedAt.Equal(sampleCreatedAt) {
		t.Errorf("createdAt = %v, want %v", got.CreatedAt, sampleCreatedAt)
	}

	var raw map[string]json.RawMessage
	decode(t, w, &raw)
	var profile map[string]json.RawMessage
	if err := json.Unmarshal(raw["profile"], &profile); err != nil {
		t.Fatalf("decoding the profile object: %v", err)
	}
	if _, present := profile["disabledBy"]; present {
		t.Error("the tenant profile view carries disabledBy, which belongs to the platform management surface")
	}

	if asked := r.guard.askedFor(); !reflect.DeepEqual(asked, []authz.Permission{authz.PermissionTenantProfileRead}) {
		t.Errorf("permissions asked = %v, want [tenant.profile.read]", asked)
	}
	if domains := r.guard.domainsAsked(); !reflect.DeepEqual(domains, []authz.Domain{"acme"}) {
		t.Errorf("domains asked = %v, want [acme]", domains)
	}
	if reads := r.signup.readCount(); reads != 0 {
		t.Errorf("the read asked the signup-verification seam %d times, want none", reads)
	}
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("the read wrote %d audit rows, want none", len(rows))
	}
}

// TestAnUnknownTenantIsABareNotFound holds the read's answer when the roster
// does not carry the resolved tenant.
func TestAnUnknownTenantIsABareNotFound(t *testing.T) {
	r := newRig(t)
	r.catalog.found = false

	w := r.do(http.MethodGet, profileTarget, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %q", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Errorf("the 404 carries a body %q, want none", w.Body.String())
	}
}

// TestAReadRequiresAnIdentity holds the route reached with no identity.
func TestAReadRequiresAnIdentity(t *testing.T) {
	r := newRig(t)

	w := r.doAnonymous(http.MethodGet, profileTarget)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %q", w.Code, w.Body.String())
	}
	if asked := r.guard.askedFor(); len(asked) != 0 {
		t.Errorf("the guard was asked %v on an anonymous request, want nothing", asked)
	}
}

// TestTheReadNeedsTheReadPermission holds the guard's denial: nothing reaches
// the roster.
func TestTheReadNeedsTheReadPermission(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false

	w := r.do(http.MethodGet, profileTarget, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %q", w.Code, w.Body.String())
	}
	if calls := r.catalog.recorded(); len(calls) != 0 {
		t.Errorf("the roster was reached %v on a denied read, want nothing", calls)
	}
}

// TestTheWriteStoresTheProfileAndAuditsIt holds the write's action, the view it
// answers, and the order of its two steps: the mandatory audit row lands before
// the roster write.
func TestTheWriteStoresTheProfileAndAuditsIt(t *testing.T) {
	r := newRig(t)
	r.catalog.held = sampleTenant()

	body := writeBody{
		DisplayName:  "  Renamed Acme  ",
		Description:  "  A sharper description.  ",
		ContactEmail: "hello@acme.test",
		Website:      "https://new.acme.test",
	}
	w := r.do(http.MethodPatch, profileTarget, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", w.Code, w.Body.String())
	}

	var answer struct {
		Profile TenantProfileView `json:"profile"`
	}
	decode(t, w, &answer)
	if got, want := answer.Profile.DisplayName, "Renamed Acme"; got != want {
		t.Errorf("displayName = %q, want %q", got, want)
	}
	if got, want := answer.Profile.Description, "A sharper description."; got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
	if got, want := answer.Profile.ContactEmail, "hello@acme.test"; got != want {
		t.Errorf("contactEmail = %q, want %q", got, want)
	}
	if got, want := answer.Profile.Website, "https://new.acme.test"; got != want {
		t.Errorf("website = %q, want %q", got, want)
	}

	var wrote []catalogCall
	for _, call := range r.catalog.recorded() {
		if call.op == "SetProfile" {
			wrote = append(wrote, call)
		}
	}
	if len(wrote) != 1 {
		t.Fatalf("SetProfile was called %d times, want once", len(wrote))
	}
	wantProfile := tenant.Profile{
		DisplayName:  "Renamed Acme",
		Description:  "A sharper description.",
		ContactEmail: "hello@acme.test",
		Website:      "https://new.acme.test",
	}
	if wrote[0].id != "acme" {
		t.Errorf("SetProfile wrote %q, want acme", wrote[0].id)
	}
	if wrote[0].profile != wantProfile {
		t.Errorf("SetProfile wrote %+v, want %+v", wrote[0].profile, wantProfile)
	}

	rows := r.audit.entries()
	if len(rows) != 1 {
		t.Fatalf("the write wrote %d audit rows, want one", len(rows))
	}
	row := rows[0]
	if row.Action != actionTenantProfileUpdate {
		t.Errorf("audit action = %q, want %q", row.Action, actionTenantProfileUpdate)
	}
	if row.Object != "acme" {
		t.Errorf("audit object = %q, want acme", row.Object)
	}
	if row.Outcome != outcomeSuccess {
		t.Errorf("audit outcome = %q, want %q", row.Outcome, outcomeSuccess)
	}
	if row.Detail[detailSurface] != surfaceTenant {
		t.Errorf("audit surface = %q, want %q", row.Detail[detailSurface], surfaceTenant)
	}
	if row.Detail[detailTenant] != "acme" {
		t.Errorf("audit tenant = %q, want acme", row.Detail[detailTenant])
	}
	if row.Detail[detailActionID] == "" {
		t.Error("the audit row carries no action identifier")
	}

	if got, want := *r.order, []string{"audit", "SetProfile"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the write's order = %v, want %v: the mandatory audit row lands before the roster write", got, want)
	}

	if asked := r.guard.askedFor(); !reflect.DeepEqual(asked, []authz.Permission{authz.PermissionTenantProfileWrite}) {
		t.Errorf("permissions asked = %v, want [tenant.profile.write]", asked)
	}
	if domains := r.guard.domainsAsked(); !reflect.DeepEqual(domains, []authz.Domain{"acme"}) {
		t.Errorf("domains asked = %v, want [acme]", domains)
	}
	if reads := r.signup.readCount(); reads != 1 {
		t.Errorf("the write asked the signup-verification seam %d times, want once", reads)
	}
}

// TestTheWriteRequiresAProvedEmailAddress holds the registration gate: an
// account whose self-registration has not proved an email address is refused
// before the guard, the audit or the roster.
func TestTheWriteRequiresAProvedEmailAddress(t *testing.T) {
	r := newRig(t)
	r.signup.answer(authn.SignupPending, nil)

	w := r.do(http.MethodPatch, profileTarget, validWrite())
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %q", w.Code, w.Body.String())
	}
	if got := problemOf(t, w); got.Type != authn.ContactRequiredProblemType {
		t.Errorf("problem type = %q, want %q", got.Type, authn.ContactRequiredProblemType)
	}
	if asked := r.guard.askedFor(); len(asked) != 0 {
		t.Errorf("the guard was asked %v, want nothing: the email gate sits before the decision", asked)
	}
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("the refused write wrote %d audit rows, want none", len(rows))
	}
	if calls := r.catalog.recorded(); len(calls) != 0 {
		t.Errorf("the refused write reached the roster: %v", calls)
	}
}

// TestTheWriteRefusesAnEmailStateThatCannotBeRead holds the gate's failure mode:
// an answer that cannot be read is a 503, never a pass.
func TestTheWriteRefusesAnEmailStateThatCannotBeRead(t *testing.T) {
	r := newRig(t)
	r.signup.answer(authn.SignupNone, errors.New("the account store is down"))

	w := r.do(http.MethodPatch, profileTarget, validWrite())
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body %q", w.Code, w.Body.String())
	}
	if !strings.Contains(r.logs.String(), "the signup state could not be read") {
		t.Errorf("the 503 was not the registration gate's: logs %q", r.logs.String())
	}
	if asked := r.guard.askedFor(); len(asked) != 0 {
		t.Errorf("the guard was asked %v, want nothing", asked)
	}
	if calls := r.catalog.recorded(); len(calls) != 0 {
		t.Errorf("the failed gate reached the roster: %v", calls)
	}
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("the failed gate wrote %d audit rows, want none", len(rows))
	}
}

// TestTheWriteNeedsTheWritePermission holds the guard's denial: no audit row and
// no roster write.
func TestTheWriteNeedsTheWritePermission(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false

	w := r.do(http.MethodPatch, profileTarget, validWrite())
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %q", w.Code, w.Body.String())
	}
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("the denied write wrote %d audit rows, want none", len(rows))
	}
	for _, call := range r.catalog.recorded() {
		if call.op == "SetProfile" {
			t.Error("the denied write reached SetProfile")
		}
	}
}

// TestTheMandatoryAuditRowComesBeforeTheWrite holds that a mandatory row that
// cannot land stops the write: no roster call follows it.
func TestTheMandatoryAuditRowComesBeforeTheWrite(t *testing.T) {
	r := newRig(t)
	r.audit.fail(errors.New("the audit store is down"))

	w := r.do(http.MethodPatch, profileTarget, validWrite())
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body %q", w.Code, w.Body.String())
	}
	if !strings.Contains(r.logs.String(), "the mandatory audit row could not be written") {
		t.Errorf("the 503 was not the audit gate's: logs %q", r.logs.String())
	}
	for _, call := range r.catalog.recorded() {
		if call.op == "SetProfile" {
			t.Error("the write reached SetProfile although its mandatory audit row did not land")
		}
	}
	if len(*r.order) != 0 {
		t.Errorf("the write's order = %v, want nothing", *r.order)
	}
}

// TestAProfileOutsideTheDomainIsRefusedBeforeTheAudit holds the shape check: a
// profile the root package refuses is a 400 with this surface's problem type,
// and neither the roster nor the audit is reached.
func TestAProfileOutsideTheDomainIsRefusedBeforeTheAudit(t *testing.T) {
	cases := map[string]writeBody{
		"a website with a non-http scheme":  {Website: "ftp://acme.test"},
		"a contact address in display form": {ContactEmail: "Ops <ops@acme.test>"},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			w := r.do(http.MethodPatch, profileTarget, body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body %q", w.Code, w.Body.String())
			}
			if got := problemOf(t, w); got.Type != typePrefix+badRequestCode {
				t.Errorf("problem type = %q, want %q", got.Type, typePrefix+badRequestCode)
			}
			if calls := r.catalog.recorded(); len(calls) != 0 {
				t.Errorf("the refused profile reached the roster: %v", calls)
			}
			if rows := r.audit.entries(); len(rows) != 0 {
				t.Errorf("the refused profile wrote %d audit rows, want none", len(rows))
			}
		})
	}
}

// TestABodyThatIsNotOneJSONObjectIsABadRequest holds the prescreen: the entry
// point's own binding refusal is replaced by this surface's problem type.
func TestABodyThatIsNotOneJSONObjectIsABadRequest(t *testing.T) {
	r := newRig(t)

	w := r.doRaw(http.MethodPatch, profileTarget, `{"display_name": 3}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %q", w.Code, w.Body.String())
	}
	if got := problemOf(t, w); got.Type != typePrefix+badRequestCode {
		t.Errorf("problem type = %q, want %q", got.Type, typePrefix+badRequestCode)
	}
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("the unreadable body wrote %d audit rows, want none", len(rows))
	}
}

// TestAWriteMissingAFieldIsABadRequest holds the body's four required members:
// a write that omits one replaces no field.
func TestAWriteMissingAFieldIsABadRequest(t *testing.T) {
	r := newRig(t)

	w := r.doRaw(http.MethodPatch, profileTarget, `{"display_name": "Acme"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %q", w.Code, w.Body.String())
	}
	if got := problemOf(t, w); got.Type != typePrefix+badRequestCode {
		t.Errorf("problem type = %q, want %q", got.Type, typePrefix+badRequestCode)
	}
	if calls := r.catalog.recorded(); len(calls) != 0 {
		t.Errorf("the incomplete body reached the roster: %v", calls)
	}
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("the incomplete body wrote %d audit rows, want none", len(rows))
	}
}

// TestTheWriteMapsARosterFailure holds the closed mapping of the failures a
// roster write returns, and the linked failed row each one closes with.
func TestTheWriteMapsARosterFailure(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantType   string
	}{
		{"invalid profile", tenant.ErrInvalidProfile, http.StatusBadRequest, typePrefix + badRequestCode},
		{"unknown tenant", tenant.ErrInvalidTenant, http.StatusNotFound, ""},
		{"tombstone", tenant.ErrTenantDeleted, http.StatusConflict, typePrefix + conflictCode},
		{"unexpected", errors.New("the roster is down"), http.StatusServiceUnavailable, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.catalog.held = sampleTenant()
			r.catalog.profileErr = c.err

			w := r.do(http.MethodPatch, profileTarget, validWrite())
			if w.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d; body %q", w.Code, c.wantStatus, w.Body.String())
			}
			if c.wantType != "" {
				var body struct {
					Type string `json:"type"`
				}
				decode(t, w, &body)
				if body.Type != c.wantType {
					t.Errorf("problem type = %q, want %q", body.Type, c.wantType)
				}
			} else if c.wantStatus == http.StatusNotFound && w.Body.Len() != 0 {
				t.Errorf("the 404 carries a body %q, want none", w.Body.String())
			}

			rows := r.audit.entries()
			if len(rows) != 2 {
				t.Fatalf("the failed write wrote %d audit rows, want a success row and a failed row", len(rows))
			}
			if rows[0].Outcome != outcomeSuccess || rows[1].Outcome != outcomeFailed {
				t.Errorf("outcomes = %q, %q, want success then failed", rows[0].Outcome, rows[1].Outcome)
			}
			if rows[0].Detail[detailActionID] == "" || rows[0].Detail[detailActionID] != rows[1].Detail[detailActionID] {
				t.Errorf("the two rows do not share one action identifier: %q, %q",
					rows[0].Detail[detailActionID], rows[1].Detail[detailActionID])
			}
			if rows[1].Detail[detailSurface] != surfaceTenant {
				t.Errorf("the failed row's surface = %q, want %q", rows[1].Detail[detailSurface], surfaceTenant)
			}
		})
	}
}

// TestTheViewAlwaysCarriesTheCreationTime holds the mandatory column's
// rendering: every row has one, so the field is present rather than omitted.
func TestTheViewAlwaysCarriesTheCreationTime(t *testing.T) {
	r := newRig(t)
	r.catalog.held = sampleTenant()

	w := r.do(http.MethodGet, profileTarget, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", w.Code, w.Body.String())
	}
	var body struct {
		Profile map[string]json.RawMessage `json:"profile"`
	}
	decode(t, w, &body)
	if _, present := body.Profile["createdAt"]; !present {
		t.Error("the view omits createdAt, want the creation time every row carries")
	}
}
