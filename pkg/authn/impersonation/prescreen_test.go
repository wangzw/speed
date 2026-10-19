package impersonation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// problemTypeOf reads the problem type of a refusal.
func problemTypeOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var problem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
		t.Fatalf("the answer %q is not a problem document: %v", w.Body.String(), err)
	}
	return problem.Type
}

// refusedBeforeStart holds that a request the route refused on its body reached
// nothing: the status and the capability's problem code, and no record, audit
// row or denied count.
func refusedBeforeStart(t *testing.T, r *rig, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", w.Code, status, w.Body.String())
	}
	if got, want := problemTypeOf(t, w), typePrefix+code; got != want {
		t.Errorf("problem type = %q, want %q", got, want)
	}
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("the refusal wrote %d audit rows, want none: %+v", len(rows), rows)
	}
	if got := r.counter(outcomeDenied); got != 0 {
		t.Errorf("the refusal counted %d denied attempts, want none", got)
	}
	if got := r.recordCount(); got != 0 {
		t.Errorf("the refusal wrote %d records, want none", got)
	}
}

// TestStartRefusesAnEmptyBodyBeforeItStarts holds the rule the route carried
// before it was declared as an operation: a request that declares no body is a
// 400 bad-request, answered before Start, so it writes neither a denied audit
// row nor a record and counts no denied attempt.
func TestStartRefusesAnEmptyBodyBeforeItStarts(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.mounter.As(adminOpts()...)
		r.p.mountRoutes()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/impersonation", nil)
		req.Header.Set("Content-Type", "application/json")
		if req.ContentLength != 0 {
			t.Fatalf("the request declares a length of %d, want 0", req.ContentLength)
		}
		w := r.mounter.Do(req)
		refusedBeforeStart(t, r, w, http.StatusBadRequest, mount.CodeBadRequest)
	})
}

// TestStartKeepsTheCapabilityMediaTypeRule holds the media type the route
// accepted before it was declared as an operation: application/json with no
// parameter or with charset=utf-8 as its only one. Every other type, including
// a parameter beyond that one, is a 415 unsupported-media-type.
func TestStartKeepsTheCapabilityMediaTypeRule(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.mounter.As(adminOpts()...)
		r.p.mountRoutes()
		for _, contentType := range []string{
			"",
			"text/plain",
			"application/json; charset=iso-8859-1",
			"application/json; charset=utf-16",
			"application/json; foo=bar",
		} {
			t.Run(contentType, func(t *testing.T) {
				req := startRequest(postBody())
				req.Header.Set("Content-Type", contentType)
				w := r.mounter.Do(req)
				refusedBeforeStart(t, r, w, http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType)
			})
		}
	})
}

// TestStartAcceptsTheOneAllowedMediaTypeParameter holds the parameter the rule
// allows: application/json with charset=utf-8 is bound and processed.
func TestStartAcceptsTheOneAllowedMediaTypeParameter(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.mounter.As(adminOpts()...)
		r.p.mountRoutes()
		req := startRequest(postBody())
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		w := r.mounter.Do(req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
		}
		if got := r.recordCount(); got != 1 {
			t.Errorf("the start wrote %d records, want 1", got)
		}
	})
}

// TestStartKeepsTheCapabilityBodyRule holds the body rule the route carried
// before it was declared as an operation: one JSON value the declaration can
// hold, no member the declaration does not carry, and nothing after the value.
// Every refusal carries the capability's bad-request code rather than the entry
// point's untyped about:blank.
func TestStartKeepsTheCapabilityBodyRule(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.mounter.As(adminOpts()...)
		r.p.mountRoutes()
		for _, tc := range []struct {
			name string
			body string
		}{
			{"malformed JSON", "{"},
			{"white space", " \n"},
			{"a value that is not an object", `"nope"`},
			{"an unknown member", `{"account":"acct-user","tenant":"tenant-a","reason":"r","status":"disabled"}`},
			{"a member of the wrong shape", `{"account":"acct-user","tenant":"tenant-a","reason":"r","ttl_seconds":"soon"}`},
			{"a second value", `{"account":"acct-user","tenant":"tenant-a","reason":"r"}{}`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				w := r.mounter.Do(startRequest(tc.body))
				refusedBeforeStart(t, r, w, http.StatusBadRequest, mount.CodeBadRequest)
			})
		}
	})
}

// TestStartRefusesABodyOverTheCapabilityLimit holds the capability's 64 KiB
// body limit: a valid JSON body over it is a 400 bad-request, the answer the
// route gave before it was declared as an operation.
func TestStartRefusesABodyOverTheCapabilityLimit(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.mounter.As(adminOpts()...)
		r.p.mountRoutes()
		oversized := `{"account":"acct-user","tenant":"tenant-a","reason":"` +
			strings.Repeat("a", mount.MaxBodyBytes) + `"}`
		w := r.mounter.Do(startRequest(oversized))
		refusedBeforeStart(t, r, w, http.StatusBadRequest, mount.CodeBadRequest)
	})
}

// TestStartBindsMemberNamesWithoutRegardToCase holds the member matching the
// route had before it was declared as an operation: encoding/json matches a
// member to its field without regard to case, so the start binds a body whose
// member names are capitalised.
func TestStartBindsMemberNamesWithoutRegardToCase(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.mounter.As(adminOpts()...)
		r.p.mountRoutes()
		w := r.mounter.Do(startRequest(
			`{"ACCOUNT":"acct-user","Tenant":"tenant-a","REASON":"a support call"}`))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
		}
		var body struct {
			Grant AuthnImpersonationGrant `json:"grant"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("the body %q is not the grant: %v", w.Body.String(), err)
		}
		if body.Grant.Target != string(targetID) || body.Grant.Tenant != tenantA {
			t.Errorf("the grant reads %+v, want the target and the tenant", body.Grant)
		}
	})
}
