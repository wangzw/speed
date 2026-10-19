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

// The first cases drive each route over the real HTTP surface the rig mounts,
// with the identity a caller carries, and hold both the answer and what reached
// the registry.

func TestTheListAnswersOnePageWithItsSourceAndApproval(t *testing.T) {
	r := newRig(t)
	r.registry.records = []sms.Record{
		{
			Name: "order-shipped", Source: sms.SourceRuntime, Params: []string{"code"},
			Locales: []string{"en-US"}, Enabled: true, Version: 3,
			Approvals: map[string]sms.Approval{"en-US": {State: sms.ApprovalApproved}},
			CreatedAt: time.Unix(100, 0), UpdatedAt: time.Unix(200, 0),
		},
		{
			Name: "login-code", Source: sms.SourceConfig, Params: []string{"code"},
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
	// The page is ordered by template name, whatever order the registry answered
	// in: "login-code" before "order-shipped".
	if len(got.Templates) != 2 || got.Templates[0].Name != "login-code" || got.Templates[1].Name != "order-shipped" {
		t.Fatalf("templates = %+v, want login-code then order-shipped", got.Templates)
	}
	if got.Templates[0].Source != string(sms.SourceConfig) || got.Templates[1].Source != string(sms.SourceRuntime) {
		t.Errorf("sources = %q, %q; want config, runtime", got.Templates[0].Source, got.Templates[1].Source)
	}
	if got.Templates[1].Approvals["en-US"].State != string(sms.ApprovalApproved) {
		t.Errorf("the English review = %q, want approved", got.Templates[1].Approvals["en-US"].State)
	}
}

func TestTheRegisterRouteAnswersCreatedAndPassesTheSpecOn(t *testing.T) {
	r := newRig(t)
	r.registry.register = sms.Record{Name: "login-code", Source: sms.SourceRuntime, Version: 1}

	w := r.do(http.MethodPost, pathTemplates, registerBody{
		Name: "login-code", Params: []string{"code"}, Codes: map[string]string{"zh-CN": "SMS_1"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", w.Code, w.Body.String())
	}
	calls := r.registry.recorded()
	if len(calls) != 1 || calls[0].op != "Register" || calls[0].name != "login-code" {
		t.Fatalf("registry calls = %+v, want one Register of login-code", calls)
	}
	if len(calls[0].spec.Params) != 1 || calls[0].spec.Params[0] != "code" || calls[0].spec.Codes["zh-CN"] != "SMS_1" {
		t.Errorf("spec = %+v, want the params and codes the body named", calls[0].spec)
	}
}

func TestTheSubmissionRouteAnswersCreatedAndPassesTheSubmissionOn(t *testing.T) {
	r := newRig(t)
	r.registry.offer = sms.Record{Name: "login-code", Source: sms.SourceRuntime, Version: 1}

	w := r.do(http.MethodPost, pathSubmissions, offerBody{
		Name: "login-code", Content: "your code is ${code}", Kind: "verification",
		Locale: "zh-CN", Params: []string{"code"}, Remark: "login",
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
		got.Locale != "zh-CN" || len(got.Params) != 1 || got.Params[0] != "code" || got.Remark != "login" {
		t.Errorf("submission = %+v, want the fields the body named", got)
	}
}

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

// The decision is asked per method — a read needs template.read and a write
// needs template.manage — and it is asked in the platform domain, so the
// request's own tenant never steers it.
func TestTheDecisionIsAskedPerMethodInThePlatformDomain(t *testing.T) {
	cases := map[string]struct {
		method string
		target string
		body   any
		want   authz.Permission
	}{
		"list":       {http.MethodGet, pathTemplates, nil, PermissionTemplateRead},
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

// A request with no identity is a 401 with the realm challenge, the same answer
// the admission and authorization layers write, and it reaches neither the guard
// nor the registry nor the audit.
func TestARequestWithNoIdentityIsA401(t *testing.T) {
	for name, target := range map[string]struct {
		method string
		target string
		body   any
	}{
		"list":       {http.MethodGet, pathTemplates, nil},
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

// A guard refusal is answered by the guard — a 403 for a denial and a 503 for an
// outage — and reaches no registry and no audit.
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

// A write whose mandatory audit row does not land is a 503 and the action is not
// carried out: the registry is not reached and no failed row is written beside a
// row that was never written.
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

// A write the registry refuses is closed with a failed row linked to the
// mandatory one by the action identifier, and the answer is the mapped one.
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

// The refusal mapping is closed and fail-closed: each sentinel the registry
// returns reaches its status, and a failure no sentinel names is a 503 whose
// cause stays in the log.
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

// The configuration-sourced refusal names its source in the problem type, so an
// operator reads the fix off the answer, while a name collision wears the plain
// conflict type.
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

// A registration and an enable refuse their shape before the action: an empty
// name, a name naming no row, and an enable body that names no target are 400s,
// and neither the registry nor the audit is reached.
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

// The list pages by template name: the cursor is the last name on the page and
// the next page continues after it, in the order the names sort.
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

// An illegal limit is a 400 before the registry is read.
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

// The view carries the fields the design names and never the vendor code, so a
// field added to a record later does not reach a client by being there.
func TestTheViewCarriesNoVendorCode(t *testing.T) {
	r := newRig(t)
	r.registry.records = []sms.Record{{
		Name: "login-code", Source: sms.SourceRuntime, Params: []string{"code"}, Locales: []string{"zh-CN"},
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
	for _, want := range []string{"name", "source", "params", "locales", "approvals", "enabled", "version", "created_at", "updated_at"} {
		if _, ok := got[want]; !ok {
			t.Errorf("the view is missing %q", want)
		}
	}
}

// A missing template is answered with the status alone: a 404 that carries no
// problem document, which is the shape the fragment declares and the surface has
// always written. The answer is an empty reply, so a declared body would turn it
// into an internal error.
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

// ptr returns a pointer to v, for building a body whose target is a pointer so a
// case can tell an absent field from a false one.
func ptr(v bool) *bool { return &v }
