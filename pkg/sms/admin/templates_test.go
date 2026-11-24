package admin

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/sms"
)

// TestTheListAnswersOnePageWithItsSourceAndApproval checks the list answer and what reached the registry.
func TestTheListAnswersOnePageWithItsSourceAndApproval(t *testing.T) {
	r := newRig(t)
	r.registry.records = []sms.Record{
		{
			Name: "order-shipped", Carrier: "stub-carrier", Kind: sms.KindMarketing,
			Source: sms.SourceRuntime, Params: []string{"code"},
			Locales: []string{"en-US"}, Enabled: true, Version: 3,
			Bodies:     map[string]string{"en-US": "your order ${code} shipped"},
			Attributes: map[string]string{"IN_TEMPLATE_ID": "tpl-1"},
			Approvals:  map[string]sms.Approval{"en-US": {State: sms.ApprovalApproved}},
			CreatedAt:  time.Unix(100, 0), UpdatedAt: time.Unix(200, 0),
		},
		{
			Name: "login-code", Carrier: "stub-carrier", Kind: sms.KindNotification,
			Source: sms.SourceConfig, Params: []string{"code"},
			Locales: []string{"zh-CN"}, Enabled: true,
		},
	}

	w := r.do(http.MethodGet, pathTemplates, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var got struct {
		Templates  []templateView `json:"templates"`
		NextCursor string         `json:"next_cursor"`
	}
	decode(t, w, &got)
	if got.NextCursor != "" {
		t.Errorf("next_cursor = %q, want empty on the last page", got.NextCursor)
	}
	if len(got.Templates) != 2 || got.Templates[0].Name != "login-code" || got.Templates[1].Name != "order-shipped" {
		t.Fatalf("templates = %+v, want login-code then order-shipped", got.Templates)
	}
	if got.Templates[0].Source != string(sms.SourceConfig) || got.Templates[1].Source != string(sms.SourceRuntime) {
		t.Errorf("sources = %q, %q; want config, runtime", got.Templates[0].Source, got.Templates[1].Source)
	}
	if got.Templates[1].Approvals["en-US"].State != string(sms.ApprovalApproved) {
		t.Errorf("the English review = %q, want approved", got.Templates[1].Approvals["en-US"].State)
	}
	if got.Templates[1].Carrier != "stub-carrier" {
		t.Errorf("carrier = %q, want stub-carrier", got.Templates[1].Carrier)
	}
	if got.Templates[0].Kind != string(sms.KindNotification) || got.Templates[1].Kind != string(sms.KindMarketing) {
		t.Errorf("kinds = %q, %q; want notification, marketing", got.Templates[0].Kind, got.Templates[1].Kind)
	}
	if got.Templates[1].Bodies["en-US"] != "your order ${code} shipped" {
		t.Errorf("bodies = %+v, want the record's body", got.Templates[1].Bodies)
	}
	if got.Templates[1].Attributes["IN_TEMPLATE_ID"] != "tpl-1" {
		t.Errorf("attributes = %+v, want the record's registration identifier", got.Templates[1].Attributes)
	}
}

// TestTheRegisterRouteAnswersCreatedAndPassesTheSpecOn checks the created answer and the spec passed on.
func TestTheRegisterRouteAnswersCreatedAndPassesTheSpecOn(t *testing.T) {
	r := newRig(t)
	r.registry.register = sms.Record{Name: "login-code", Source: sms.SourceRuntime, Version: 1}

	w := r.do(http.MethodPost, pathTemplates, registerBody{
		Name: "login-code", Kind: "verification", Params: []string{"code"},
		Codes:      map[string]string{"zh-CN": "SMS_1"},
		Bodies:     map[string]string{"zh-CN": "your code is ${code}"},
		Attributes: map[string]string{"IN_TEMPLATE_ID": "tpl-1"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", w.Code, w.Body.String())
	}
	calls := r.registry.recorded()
	if len(calls) != 1 || calls[0].op != "Register" || calls[0].name != "login-code" {
		t.Fatalf("registry calls = %+v, want one Register of login-code", calls)
	}
	spec := calls[0].spec
	if spec.Kind != sms.KindVerification || len(spec.Params) != 1 || spec.Params[0] != "code" ||
		spec.Codes["zh-CN"] != "SMS_1" || spec.Bodies["zh-CN"] != "your code is ${code}" ||
		spec.Attributes["IN_TEMPLATE_ID"] != "tpl-1" {
		t.Errorf("spec = %+v, want every field the body named", spec)
	}
}

// TestARegistrationNeedsNoCodeOrParameter checks that a registration with only a body is passed on rather than refused on shape.
func TestARegistrationNeedsNoCodeOrParameter(t *testing.T) {
	r := newRig(t)
	r.registry.register = sms.Record{Name: "welcome", Source: sms.SourceRuntime, Version: 1}

	w := r.do(http.MethodPost, pathTemplates, registerBody{
		Name: "welcome", Bodies: map[string]string{"en-US": "hello"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", w.Code, w.Body.String())
	}
	calls := r.registry.recorded()
	if len(calls) != 1 || calls[0].op != "Register" {
		t.Fatalf("registry calls = %+v, want one Register", calls)
	}
	spec := calls[0].spec
	if len(spec.Codes) != 0 || len(spec.Params) != 0 || spec.Bodies["en-US"] != "hello" {
		t.Errorf("spec = %+v, want the body and no code or parameter", spec)
	}
	if spec.Kind != "" {
		t.Errorf("spec.Kind = %q, want empty so the registry applies its notification default", spec.Kind)
	}
}

// TestTheSubmissionRouteAnswersCreatedAndPassesTheSubmissionOn checks the created answer and the submission passed on.
func TestTheSubmissionRouteAnswersCreatedAndPassesTheSubmissionOn(t *testing.T) {
	r := newRig(t)
	r.registry.offer = sms.Record{Name: "login-code", Source: sms.SourceRuntime, Version: 1}

	w := r.do(http.MethodPost, pathSubmissions, offerBody{
		Name: "login-code", Content: "your code is ${code}", Kind: "verification",
		Locale: "zh-CN", Params: []string{"code"}, Attributes: map[string]string{"IN_TEMPLATE_ID": "tpl-1"},
		Remark: "login",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", w.Code, w.Body.String())
	}
	calls := r.registry.recorded()
	if len(calls) != 1 || calls[0].op != "Offer" {
		t.Fatalf("registry calls = %+v, want one Offer", calls)
	}
	got := calls[0].offer
	if got.Name != "login-code" || got.Content != "your code is ${code}" || got.Kind != sms.KindVerification ||
		got.Locale != "zh-CN" || len(got.Params) != 1 || got.Params[0] != "code" ||
		got.Attributes["IN_TEMPLATE_ID"] != "tpl-1" || got.Remark != "login" {
		t.Errorf("submission = %+v, want the fields the body named", got)
	}
}

// TestTheEnableRouteAnswersNoContentAndPassesTheTargetOn checks the 204 answer and the target state passed on.
func TestTheEnableRouteAnswersNoContentAndPassesTheTargetOn(t *testing.T) {
	r := newRig(t)
	w := r.do(http.MethodPost, "/api/v1/system/sms/templates/login-code/enabled", enabledBody{Enabled: ptr(true)})
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body.String())
	}
	calls := r.registry.recorded()
	if len(calls) != 1 || calls[0].op != "SetEnabled" || calls[0].name != "login-code" || !calls[0].enabled {
		t.Fatalf("registry calls = %+v, want one SetEnabled(login-code, true)", calls)
	}
}

// TestTheRemoveRouteAnswersNoContentAndPassesTheNameOn checks the 204 answer and the name passed on.
func TestTheRemoveRouteAnswersNoContentAndPassesTheNameOn(t *testing.T) {
	r := newRig(t)
	w := r.do(http.MethodDelete, "/api/v1/system/sms/templates/login-code", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body.String())
	}
	calls := r.registry.recorded()
	if len(calls) != 1 || calls[0].op != "Remove" || calls[0].name != "login-code" {
		t.Fatalf("registry calls = %+v, want one Remove of login-code", calls)
	}
}

// TestTheDecisionIsAskedPerMethodInThePlatformDomain checks that a read asks
// template.read and a write template.manage, in the platform domain.
func TestTheDecisionIsAskedPerMethodInThePlatformDomain(t *testing.T) {
	cases := map[string]struct {
		method string
		target string
		body   any
		want   authz.Permission
	}{
		"list":       {http.MethodGet, pathTemplates, nil, PermissionTemplateRead},
		"carrier":    {http.MethodGet, pathCarrier, nil, PermissionTemplateRead},
		"register":   {http.MethodPost, pathTemplates, registerBody{Name: "t", Params: []string{"p"}, Codes: map[string]string{"l": "c"}}, PermissionTemplateManage},
		"submission": {http.MethodPost, pathSubmissions, offerBody{Name: "t"}, PermissionTemplateManage},
		"enable":     {http.MethodPost, "/api/v1/system/sms/templates/t/enabled", enabledBody{Enabled: ptr(false)}, PermissionTemplateManage},
		"remove":     {http.MethodDelete, "/api/v1/system/sms/templates/t", nil, PermissionTemplateManage},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			g := newRig(t)
			g.do(tc.method, tc.target, tc.body)
			asked := g.guard.askedFor()
			if len(asked) != 1 || asked[0] != tc.want {
				t.Fatalf("the guard was asked %v, want exactly [%q]", asked, tc.want)
			}
			domains := g.guard.domainsAsked()
			if len(domains) != 1 || domains[0] != authz.PlatformDomain {
				t.Fatalf("the decision was made in %v, want [%q]", domains, authz.PlatformDomain)
			}
		})
	}
}

// TestARequestWithNoIdentityIsA401 checks that it answers 401 with the realm
// challenge and reaches neither the guard nor the registry.
func TestARequestWithNoIdentityIsA401(t *testing.T) {
	for name, target := range map[string]struct {
		method string
		target string
		body   any
	}{
		"list":       {http.MethodGet, pathTemplates, nil},
		"carrier":    {http.MethodGet, pathCarrier, nil},
		"register":   {http.MethodPost, pathTemplates, registerBody{Name: "t"}},
		"submission": {http.MethodPost, pathSubmissions, offerBody{Name: "t"}},
		"enable":     {http.MethodPost, "/api/v1/system/sms/templates/t/enabled", enabledBody{Enabled: ptr(true)}},
		"remove":     {http.MethodDelete, "/api/v1/system/sms/templates/t", nil},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			w := r.doAnonymous(target.method, target.target)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", w.Code)
			}
			if got := w.Header().Get("WWW-Authenticate"); got != `Bearer realm="public", Cookie realm="public"` {
				t.Errorf("WWW-Authenticate = %q, want the realm challenge", got)
			}
			if len(r.registry.recorded()) != 0 {
				t.Error("an anonymous request reached the registry")
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

// TestAGuardRefusalReachesNoRegistry checks that a guard refusal is answered by the guard and reaches no registry and no audit.
func TestAGuardRefusalReachesNoRegistry(t *testing.T) {
	t.Run("denial", func(t *testing.T) {
		r := newRig(t)
		r.guard.allow = false
		w := r.do(http.MethodPost, pathTemplates, registerBody{Name: "t", Params: []string{"p"}, Codes: map[string]string{"l": "c"}})
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", w.Code)
		}
		if len(r.registry.recorded()) != 0 || len(r.audit.entries()) != 0 {
			t.Error("a denied request reached the registry or wrote an audit row")
		}
	})
	t.Run("outage", func(t *testing.T) {
		r := newRig(t)
		r.guard.err = errors.New("engine down")
		w := r.do(http.MethodPost, pathTemplates, registerBody{Name: "t", Params: []string{"p"}, Codes: map[string]string{"l": "c"}})
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", w.Code)
		}
		if len(r.registry.recorded()) != 0 {
			t.Error("a request whose decision failed reached the registry")
		}
	})
}

// TestTheMandatoryAuditLandingIsRequired checks that a write whose mandatory row does not land answers 503 and reaches no registry.
func TestTheMandatoryAuditLandingIsRequired(t *testing.T) {
	writes := map[string]struct {
		method string
		target string
		body   any
	}{
		"register": {http.MethodPost, pathTemplates, registerBody{Name: "t", Params: []string{"p"}, Codes: map[string]string{"l": "c"}}},
		"offer":    {http.MethodPost, pathSubmissions, offerBody{Name: "t"}},
		"enable":   {http.MethodPost, "/api/v1/system/sms/templates/t/enabled", enabledBody{Enabled: ptr(true)}},
		"remove":   {http.MethodDelete, "/api/v1/system/sms/templates/t", nil},
	}
	for name, tc := range writes {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.audit.fail(errors.New("audit store down"))
			w := r.do(tc.method, tc.target, tc.body)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503 (body %s)", w.Code, w.Body.String())
			}
			if len(r.registry.recorded()) != 0 {
				t.Errorf("the action ran though its mandatory audit row did not land: %+v", r.registry.recorded())
			}
			if len(r.audit.entries()) != 0 {
				t.Errorf("a failed row was written beside a mandatory row that never landed: %+v", r.audit.entries())
			}
		})
	}
}

// TestAFailedWriteClosesWithALinkedRow checks that a refused write closes with a failed row linked by the action identifier.
func TestAFailedWriteClosesWithALinkedRow(t *testing.T) {
	r := newRig(t)
	r.registry.fail(sms.ErrTemplateExists)
	w := r.do(http.MethodPost, pathTemplates, registerBody{Name: "t", Params: []string{"p"}, Codes: map[string]string{"l": "c"}})
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
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
	if rows[0].Action != actionTemplateRegister || rows[1].Action != actionTemplateRegister {
		t.Errorf("actions = %q, %q; want %q", rows[0].Action, rows[1].Action, actionTemplateRegister)
	}
}

// TestTheFailureMappingIsClosed checks that each sentinel reaches its status and
// an unnamed failure is a 503 with the cause in the log.
func TestTheFailureMappingIsClosed(t *testing.T) {
	type writeCase struct {
		method string
		target string
		body   any
	}
	writes := map[string]writeCase{
		"register": {http.MethodPost, pathTemplates, registerBody{Name: "t", Params: []string{"p"}, Codes: map[string]string{"l": "c"}}},
		"offer":    {http.MethodPost, pathSubmissions, offerBody{Name: "t"}},
		"enable":   {http.MethodPost, "/api/v1/system/sms/templates/t/enabled", enabledBody{Enabled: ptr(true)}},
		"remove":   {http.MethodDelete, "/api/v1/system/sms/templates/t", nil},
	}

	cases := []struct {
		route  string
		err    error
		status int
		typ    string
	}{
		{"register", sms.ErrImmutable, http.StatusConflict, typePrefix + configTemplateCode},
		{"register", sms.ErrTemplateExists, http.StatusConflict, typePrefix + conflictCode},
		{"register", sms.ErrInvalidConfig, http.StatusBadRequest, typePrefix + badRequestCode},
		{"register", sms.ErrRejected, http.StatusBadRequest, typePrefix + badRequestCode},
		{"register", sms.ErrNoTemplate, http.StatusNotFound, ""},
		{"register", sms.ErrNotSendable, http.StatusServiceUnavailable, ""},
		{"register", errors.New("boom"), http.StatusServiceUnavailable, ""},
		{"offer", sms.ErrRejected, http.StatusBadRequest, typePrefix + badRequestCode},
		{"offer", errors.New("boom"), http.StatusServiceUnavailable, ""},
		{"enable", sms.ErrImmutable, http.StatusConflict, typePrefix + configTemplateCode},
		{"enable", sms.ErrNoTemplate, http.StatusNotFound, ""},
		{"enable", errors.New("boom"), http.StatusServiceUnavailable, ""},
		{"remove", sms.ErrImmutable, http.StatusConflict, typePrefix + configTemplateCode},
		{"remove", sms.ErrNoTemplate, http.StatusNotFound, ""},
		{"remove", errors.New("boom"), http.StatusServiceUnavailable, ""},
	}
	for _, tc := range cases {
		t.Run(tc.route+"/"+tc.err.Error(), func(t *testing.T) {
			r := newRig(t)
			r.registry.fail(tc.err)
			wc := writes[tc.route]
			w := r.do(wc.method, wc.target, wc.body)
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

	t.Run("list", func(t *testing.T) {
		r := newRig(t)
		r.registry.fail(errors.New("store down"))
		w := r.do(http.MethodGet, pathTemplates, nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", w.Code)
		}
		if !strings.Contains(r.logs.String(), "store down") {
			t.Error("the cause of the 503 did not reach the log")
		}
	})
}

// TestTheConfigSourcedRefusalNamesItsSource checks that the configured refusal names its source in the problem type.
func TestTheConfigSourcedRefusalNamesItsSource(t *testing.T) {
	r := newRig(t)
	r.registry.fail(sms.ErrImmutable)
	w := r.do(http.MethodDelete, "/api/v1/system/sms/templates/from-config", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	if got := problemOf(t, w); got.Type != typePrefix+configTemplateCode {
		t.Errorf("problem type = %q, want %q naming the configuration source", got.Type, typePrefix+configTemplateCode)
	}

	r2 := newRig(t)
	r2.registry.fail(sms.ErrTemplateExists)
	w2 := r2.do(http.MethodPost, pathTemplates, registerBody{Name: "t", Params: []string{"p"}, Codes: map[string]string{"l": "c"}})
	if got := problemOf(t, w2); got.Type != typePrefix+conflictCode {
		t.Errorf("problem type = %q, want the plain conflict type", got.Type)
	}
}

// TestAShapeRefusalReachesNeitherTheRegistryNorTheAudit checks that an empty
// name or enable body is a 400 reaching neither the registry nor the audit.
func TestAShapeRefusalReachesNeitherTheRegistryNorTheAudit(t *testing.T) {
	cases := map[string]struct {
		method string
		target string
		body   any
	}{
		"register with no name":               {http.MethodPost, pathTemplates, registerBody{Params: []string{"p"}, Codes: map[string]string{"l": "c"}}},
		"register with a body it cannot read": {http.MethodPost, pathTemplates, "not-an-object"},
		"submission with no name":             {http.MethodPost, pathSubmissions, offerBody{}},
		"enable with no target":               {http.MethodPost, "/api/v1/system/sms/templates/t/enabled", enabledBody{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			w := r.do(tc.method, tc.target, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
			}
			if len(r.registry.recorded()) != 0 {
				t.Error("a shape-refused request reached the registry")
			}
			if len(r.audit.entries()) != 0 {
				t.Error("a shape-refused request wrote an audit row")
			}
		})
	}

	t.Run("register with a body that is not JSON", func(t *testing.T) {
		r := newRig(t)
		w := r.doRaw(http.MethodPost, pathTemplates, "{not json")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
		}
		if len(r.registry.recorded()) != 0 || len(r.audit.entries()) != 0 {
			t.Error("a request whose body could not be read reached the registry or the audit")
		}
	})
}

// TestTheListPagesByTemplateName checks that the cursor is the last name on the page and the next page continues after it.
func TestTheListPagesByTemplateName(t *testing.T) {
	names := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	records := make([]sms.Record, len(names))
	for i, n := range names {
		records[i] = sms.Record{Name: n}
	}

	t.Run("the first page", func(t *testing.T) {
		r := newRig(t)
		r.registry.records = records
		w := r.do(http.MethodGet, pathTemplates+"?limit=2", nil)
		var got struct {
			Templates  []templateView `json:"templates"`
			NextCursor string         `json:"next_cursor"`
		}
		decode(t, w, &got)
		if len(got.Templates) != 2 || got.Templates[0].Name != "alpha" || got.Templates[1].Name != "bravo" {
			t.Fatalf("first page = %+v, want alpha, bravo", got.Templates)
		}
		if got.NextCursor != "bravo" {
			t.Errorf("next_cursor = %q, want bravo", got.NextCursor)
		}
	})

	t.Run("the page after a cursor", func(t *testing.T) {
		r := newRig(t)
		r.registry.records = records
		w := r.do(http.MethodGet, pathTemplates+"?after=bravo&limit=2", nil)
		var got struct {
			Templates  []templateView `json:"templates"`
			NextCursor string         `json:"next_cursor"`
		}
		decode(t, w, &got)
		if len(got.Templates) != 2 || got.Templates[0].Name != "charlie" || got.Templates[1].Name != "delta" {
			t.Fatalf("page = %+v, want charlie, delta", got.Templates)
		}
		if got.NextCursor != "delta" {
			t.Errorf("next_cursor = %q, want delta", got.NextCursor)
		}
	})

	t.Run("the last page names no cursor", func(t *testing.T) {
		r := newRig(t)
		r.registry.records = records
		w := r.do(http.MethodGet, pathTemplates+"?after=delta&limit=2", nil)
		var got struct {
			Templates  []templateView `json:"templates"`
			NextCursor string         `json:"next_cursor"`
		}
		decode(t, w, &got)
		if len(got.Templates) != 1 || got.Templates[0].Name != "echo" {
			t.Fatalf("page = %+v, want echo", got.Templates)
		}
		if got.NextCursor != "" {
			t.Errorf("next_cursor = %q, want empty", got.NextCursor)
		}
	})
}

// TestAnIllegalLimitIsABadRequest checks that an illegal limit is a 400 before the registry is read.
func TestAnIllegalLimitIsABadRequest(t *testing.T) {
	for _, limit := range []string{"abc", "-1", "1.5"} {
		t.Run(limit, func(t *testing.T) {
			r := newRig(t)
			w := r.do(http.MethodGet, pathTemplates+"?limit="+limit, nil)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if len(r.registry.recorded()) != 0 {
				t.Error("an illegal limit still read the registry")
			}
		})
	}
}

// TestTheViewCarriesNoVendorCode checks that the view carries the named fields and never the vendor code.
func TestTheViewCarriesNoVendorCode(t *testing.T) {
	r := newRig(t)
	r.registry.records = []sms.Record{{
		Name: "login-code", Carrier: "stub-carrier", Kind: sms.KindVerification,
		Source: sms.SourceRuntime, Params: []string{"code"}, Locales: []string{"zh-CN"},
		Bodies: map[string]string{"zh-CN": "code ${code}"}, Attributes: map[string]string{"IN_TEMPLATE_ID": "tpl-1"},
		Enabled: true, Version: 2, Approvals: map[string]sms.Approval{"zh-CN": {State: sms.ApprovalApproved}},
		CreatedAt: time.Unix(100, 0), UpdatedAt: time.Unix(200, 0),
	}}
	w := r.do(http.MethodGet, pathTemplates, nil)
	var raw struct {
		Templates []map[string]any `json:"templates"`
	}
	decode(t, w, &raw)
	if len(raw.Templates) != 1 {
		t.Fatalf("templates = %+v, want one", raw.Templates)
	}
	got := raw.Templates[0]
	for _, banned := range []string{"code", "Codes", "vendor_code"} {
		if _, ok := got[banned]; ok {
			t.Errorf("the view carries %q, which leaks the vendor's numbering", banned)
		}
	}
	for _, want := range []string{
		"name", "carrier", "kind", "source", "params", "locales", "bodies", "attributes",
		"approvals", "enabled", "version", "created_at", "updated_at",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("the view is missing %q", want)
		}
	}
}

// TestAMissingTemplateIsAnsweredWithNoBody checks that a missing template is a 404 carrying no problem document.
func TestAMissingTemplateIsAnsweredWithNoBody(t *testing.T) {
	r := newRig(t)
	r.registry.fail(sms.ErrNoTemplate)
	w := r.do(http.MethodDelete, "/api/v1/system/sms/templates/gone", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Errorf("body = %q, want none", w.Body.String())
	}
}

func ptr(v bool) *bool { return &v }
