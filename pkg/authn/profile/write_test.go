package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/i18n"
)

func decodePatch(t *testing.T, rec *httptest.ResponseRecorder) AuthnProfilePatch {
	t.Helper()
	var v AuthnProfilePatch
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decoding the write view: %v (body %s)", err, rec.Body.String())
	}
	return v
}

// stale is an identity whose authentication is older than the first-factor
// window.
func stale() []authntest.Option {
	return []authntest.Option{
		authntest.WithAccount(meAccount),
		authntest.WithSession(meSession),
		authntest.WithAuthTime(time.Now().Add(-time.Hour)),
	}
}

// noSession is an authenticated account with no login session.
func noSession() []authntest.Option {
	return []authntest.Option{
		authntest.WithAccount(meAccount),
		authntest.WithAuthTime(time.Now()),
	}
}

// sendBody sends a request through the Mounter with a raw body and Content-Type,
// so a case can drive the capability's decode rule. An empty contentType leaves
// the header off the request.
func (r *rig) sendBody(method, path, contentType string, body io.Reader, opts ...authntest.Option) *httptest.ResponseRecorder {
	r.t.Helper()
	r.mounter.As(opts...)
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return r.mounter.Do(req)
}

// problemTypeOf reads the problem type of a refusal.
func problemTypeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var problem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("the answer is not a problem document: %v (body %s)", err, rec.Body.String())
	}
	return problem.Type
}

// TestPatchDecodesTheBodyByTheCapabilityRule holds the rule the route applied
// before it was declared as an operation: the media type is application/json
// with no parameter or with charset=utf-8 only, the body is non-empty and no
// larger than the capability's 64 KiB limit, it is one JSON value the
// declaration can hold, and every refusal carries the capability's own problem
// code rather than the entry point's default about:blank.
func TestPatchDecodesTheBodyByTheCapabilityRule(t *testing.T) {
	oversized := []byte(`{"display_name":"` + strings.Repeat("a", mount.MaxBodyBytes) + `"}`)
	cases := []struct {
		name        string
		contentType string
		body        io.Reader
		want        int
		code        string
	}{
		{"an empty body", "application/json", nil, http.StatusBadRequest, mount.CodeBadRequest},
		{"a body of white space", "application/json", strings.NewReader(" \n"), http.StatusBadRequest, mount.CodeBadRequest},
		{"malformed JSON", "application/json", strings.NewReader("{"), http.StatusBadRequest, mount.CodeBadRequest},
		{"a value that is not an object", "application/json", strings.NewReader(`"nope"`), http.StatusBadRequest, mount.CodeBadRequest},
		{"an unknown member", "application/json", strings.NewReader(`{"status":"disabled"}`), http.StatusBadRequest, mount.CodeBadRequest},
		{"a body over the capability limit", "application/json", bytes.NewReader(oversized), http.StatusBadRequest, mount.CodeBadRequest},
		{"the media type missing", "", strings.NewReader(`{}`), http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType},
		{"a media type that is not JSON", "text/plain", strings.NewReader(`{}`), http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType},
		{"a charset other than utf-8", "application/json; charset=utf-16", strings.NewReader(`{}`), http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType},
		{"a parameter beyond charset", `application/json; profile="x"`, strings.NewReader(`{}`), http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			rec := r.sendBody(http.MethodPatch, "/api/v1/auth/me", tc.contentType, tc.body, me()...)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
			if want := typePrefix + tc.code; problemTypeOf(t, rec) != want {
				t.Errorf("problem type = %q, want %q", problemTypeOf(t, rec), want)
			}
			if r.admin.count("set-display-name") != 0 || r.admin.count("set-attributes") != 0 {
				t.Error("a refused body reached a write")
			}
		})
	}
}

// TestPatchNullBodyChangesNothing holds the null body the old decoder read as
// the zero value: the route answers 200 and neither write nor audit row runs.
func TestPatchNullBodyChangesNothing(t *testing.T) {
	r := newRig(t)
	rec := r.sendBody(http.MethodPatch, "/api/v1/auth/me", "application/json", strings.NewReader("null"), me()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if r.admin.count("set-display-name") != 0 || r.admin.count("set-attributes") != 0 {
		t.Errorf("a null body called a write: display-name %d, attributes %d",
			r.admin.count("set-display-name"), r.admin.count("set-attributes"))
	}
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("a null body wrote an audit row: %+v", rows)
	}
	if got := decodePatch(t, rec); got.DisplayName != "Me" {
		t.Errorf("the answer's display_name = %q, want the current Me", got.DisplayName)
	}
}

// TestPatchAcceptsABodyAtTheCapabilityLimit holds the boundary: a body of
// exactly mount.MaxBodyBytes is within the limit and is processed.
func TestPatchAcceptsABodyAtTheCapabilityLimit(t *testing.T) {
	r := newRig(t)
	displayName := strings.Repeat("a", mount.MaxBodyBytes-len(`{"display_name":""}`))
	rec := r.sendBody(http.MethodPatch, "/api/v1/auth/me", "application/json",
		strings.NewReader(`{"display_name":"`+displayName+`"}`), me()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if r.admin.displayName != displayName {
		t.Errorf("the written display name is %d bytes, want the whole %d-byte one",
			len(r.admin.displayName), len(displayName))
	}
}

// TestPatchAcceptsAUTF8Charset holds the one media-type parameter the rule
// allows: the declared type with charset=utf-8 is bound and processed.
func TestPatchAcceptsAUTF8Charset(t *testing.T) {
	r := newRig(t)
	rec := r.sendBody(http.MethodPatch, "/api/v1/auth/me", "application/json; charset=utf-8",
		strings.NewReader(`{"display_name":"Renamed"}`), me()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if r.admin.displayName != "Renamed" {
		t.Errorf("the written display name = %q, want Renamed", r.admin.displayName)
	}
}

// TestPatchWritesTheDisplayName holds the display-name write: only SetDisplayName
// is called, and the answer carries the written value.
func TestPatchWritesTheDisplayName(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "张三"}, me()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := r.admin.count("set-display-name"); got != 1 {
		t.Errorf("SetDisplayName calls = %d, want 1", got)
	}
	if got := r.admin.count("set-attributes"); got != 0 {
		t.Errorf("SetAttributes calls = %d, want 0", got)
	}
	if r.admin.displayName != "张三" {
		t.Errorf("the written display name = %q, want 张三", r.admin.displayName)
	}
	if got := decodePatch(t, rec); got.DisplayName != "张三" {
		t.Errorf("the answer's display_name = %q, want 张三", got.DisplayName)
	}
	if rows := r.audit.entries(); len(rows) != 1 || rows[0].Action != actionProfileUpdate {
		t.Errorf("audit rows = %+v, want one %s row", rows, actionProfileUpdate)
	}
}

// TestPatchGivenAttributesReplacesTheWholeSet holds the replacement semantics: a
// given attribute set is written as it stands, and only SetAttributes is called.
func TestPatchGivenAttributesReplacesTheWholeSet(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"attributes": map[string]string{"team": "red"}}, me()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := r.admin.count("set-display-name"); got != 0 {
		t.Errorf("SetDisplayName calls = %d, want 0", got)
	}
	if got := r.admin.count("set-attributes"); got != 1 {
		t.Errorf("SetAttributes calls = %d, want 1", got)
	}
	attrs, ok := r.admin.lastAttributes()
	if !ok {
		t.Fatal("SetAttributes was not called")
	}
	if len(attrs) != 1 || attrs["team"] != "red" {
		t.Errorf("the written attributes = %v, want exactly {team: red}: the given set replaces the whole of the current one", attrs)
	}
	got := decodePatch(t, rec)
	if got.Attributes["team"] != "red" {
		t.Errorf("the answer's attributes = %v, want team=red", got.Attributes)
	}
}

// TestPatchMergesLocaleAndTimeZoneIntoTheAttributeSet holds the named uses of the
// attribute set: a given locale or time zone overwrites its key and leaves the
// rest of the set alone.
func TestPatchMergesLocaleAndTimeZoneIntoTheAttributeSet(t *testing.T) {
	r := newRig(t)
	body := map[string]any{"locale": "zh-CN", "time_zone": "Asia/Shanghai"}
	rec := r.do(http.MethodPatch, "/api/v1/auth/me", body, me()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	attrs, _ := r.admin.lastAttributes()
	if attrs["dept"] != "eng" {
		t.Errorf("the merge dropped the current attribute dept: %v", attrs)
	}
	if attrs[i18n.LocaleAttributeKey] != "zh-CN" || attrs[i18n.TimeZoneAttributeKey] != "Asia/Shanghai" {
		t.Errorf("the merge did not write the two preferences: %v", attrs)
	}
	got := decodePatch(t, rec)
	if got.Locale != "zh-CN" || got.TimeZone != "Asia/Shanghai" {
		t.Errorf("the answer's locale/time_zone = %q/%q, want zh-CN/Asia/Shanghai", got.Locale, got.TimeZone)
	}
}

// TestPatchEmptyLocaleClearsTheKey holds the clear semantics: a preference given
// as the empty string removes its key.
func TestPatchEmptyLocaleClearsTheKey(t *testing.T) {
	r := newRig(t)
	r.accounts.put(meAccount, authn.Account{
		Kind: authn.KindUser, Status: authn.StatusActive,
		Attributes: map[string]string{i18n.LocaleAttributeKey: "zh-CN", "dept": "eng"},
	})
	rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"locale": ""}, me()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	attrs, _ := r.admin.lastAttributes()
	if _, present := attrs[i18n.LocaleAttributeKey]; present {
		t.Errorf("the empty locale did not clear the key: %v", attrs)
	}
	if attrs["dept"] != "eng" {
		t.Errorf("clearing the locale dropped another attribute: %v", attrs)
	}
}

// TestPatchEmptyBodyChangesNothing holds the all-absent case: neither write is
// called and no audit row is written.
func TestPatchEmptyBodyChangesNothing(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{}, me()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if r.admin.count("set-display-name") != 0 || r.admin.count("set-attributes") != 0 {
		t.Errorf("an empty body called a write: display-name %d, attributes %d",
			r.admin.count("set-display-name"), r.admin.count("set-attributes"))
	}
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("an empty body wrote an audit row: %+v", rows)
	}
	if got := decodePatch(t, rec); got.DisplayName != "Me" {
		t.Errorf("the answer's display_name = %q, want the current Me", got.DisplayName)
	}
}

// TestPatchUnknownFieldIs400 holds the closed body: a field the body does not
// have is refused and no write is made.
func TestPatchUnknownFieldIs400(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"status": "disabled"}, me()...)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if r.admin.count("set-display-name") != 0 || r.admin.count("set-attributes") != 0 {
		t.Error("a refused body reached a write")
	}
}

// TestPatchInvalidLocaleAndTimeZoneAre400 holds the two value domains.
func TestPatchInvalidLocaleAndTimeZoneAre400(t *testing.T) {
	cases := map[string]map[string]any{
		"a locale that is not a tag": {"locale": "!!!"},
		"a zone that is not IANA":    {"time_zone": "CST"},
		"a fixed offset as a zone":   {"time_zone": "+08:00"},
		"the process's Local zone":   {"time_zone": "Local"},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			rec := r.do(http.MethodPatch, "/api/v1/auth/me", body, me()...)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if r.admin.count("set-attributes") != 0 {
				t.Error("a refused value reached a write")
			}
		})
	}
}

// TestPatchRefusalsCarryTheCapabilityProblemType holds the fault path: a value
// outside its domain is answered with the capability's own problem type, the
// prefix and code the entry point writes from the returned fault.
func TestPatchRefusalsCarryTheCapabilityProblemType(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"locale": "!!!"}, me()...)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var problem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("the answer is not a problem document: %v", err)
	}
	if want := typePrefix + mount.CodeBadRequest; problem.Type != want {
		t.Errorf("problem type = %q, want %q", problem.Type, want)
	}
}

// TestPatchRefusesBeforeTheBodyIsRead holds the order the route documents: the
// identity, the impersonation, the session and the freshness are decided before
// the body is bound. A body the declaration would refuse is therefore answered
// with the earlier refusal, not the binding's 400.
func TestPatchRefusesBeforeTheBodyIsRead(t *testing.T) {
	cases := []struct {
		name string
		opts []authntest.Option
		want int
	}{
		{"impersonating", impersonating(), http.StatusForbidden},
		{"no session", noSession(), http.StatusUnauthorized},
		{"stale", stale(), http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			// A member the declaration does not carry: binding it would be a 400.
			body := map[string]any{"unknown": "member"}
			rec := r.invoke(r.p.patchEntry(), http.MethodPatch, "/api/v1/auth/me", body, tc.opts...)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d before the body is read (body %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// TestPatchIsNotReachableWhileImpersonating holds the refusal at both layers: the
// gateway answers 401 because the route is not impersonable, and the handler
// answers 403 as the fallback for a registration made wrongly, without reading a
// body or calling a write.
func TestPatchIsNotReachableWhileImpersonating(t *testing.T) {
	r := newRig(t)
	if rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"}, impersonating()...); rec.Code != http.StatusUnauthorized {
		t.Errorf("through the gateway: status = %d, want 401", rec.Code)
	}
	rec := r.invoke(r.p.patchEntry(), http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"}, impersonating()...)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("the handler while impersonating: status = %d, want 403", rec.Code)
	}
	if r.admin.count("set-display-name") != 0 || r.admin.count("set-attributes") != 0 {
		t.Error("an impersonating write reached a capability")
	}
}

// TestPatchWithoutASessionIs401 holds the session requirement.
func TestPatchWithoutASessionIs401(t *testing.T) {
	r := newRig(t)
	if rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"}, noSession()...); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if r.admin.count("set-display-name") != 0 {
		t.Error("a session-less write reached a capability")
	}
}

// TestPatchWithoutAnIdentityIs401 holds the no-identity refusal at the handler.
func TestPatchWithoutAnIdentityIs401(t *testing.T) {
	r := newRig(t)
	rec := r.invokeAnonymous(r.p.patchEntry(), http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestPatchStaleAuthenticationAsksForAStepUp holds the freshness of a write: an
// authentication older than the first-factor window is answered with the step-up
// 401, before the body is read.
func TestPatchStaleAuthenticationAsksForAStepUp(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"}, stale()...)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("WWW-Authenticate"), mount.CodeInsufficientUserAuthentication) {
		t.Errorf("the challenge = %q, want it to ask for a stronger authentication", rec.Header().Get("WWW-Authenticate"))
	}
	if r.admin.count("set-display-name") != 0 {
		t.Error("a stale write reached a capability")
	}
}

// TestPatchWithAnActiveFactorRequiresAMultiFactor holds the account's own
// requirement: an account with an active factor is held to a fresh local
// multi-factor authentication, and a single-factor session cannot change it.
func TestPatchWithAnActiveFactorRequiresAMultiFactor(t *testing.T) {
	r := newRig(t)
	r.mfa.factors = []authn.FactorRecord{{Kind: authn.FactorTOTP, Status: authn.FactorActive}}

	rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"}, me()...)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a single-factor write with an active factor: status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("WWW-Authenticate"), mount.CodeInsufficientUserAuthentication) {
		t.Errorf("the challenge = %q, want the step-up", rec.Header().Get("WWW-Authenticate"))
	}

	if rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"}, multiFactor()...); rec.Code != http.StatusOK {
		t.Fatalf("a multi-factor write with an active factor: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestPatchWhenTheFactorsCannotBeReadIs503 holds the fail-closed direction: a
// requirement that cannot be decided is not read as one that asks for nothing.
func TestPatchWhenTheFactorsCannotBeReadIs503(t *testing.T) {
	r := newRig(t)
	r.mfa.err = errors.New("boom")
	if rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"}, me()...); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if r.admin.count("set-display-name") != 0 {
		t.Error("a write reached a capability with the requirement undecided")
	}
}

// TestPatchOfAGoneAccountIs401 holds the mapping of the identity's own account
// gone.
func TestPatchOfAGoneAccountIs401(t *testing.T) {
	r := newRig(t)
	r.accounts.getErr = authn.ErrAccountNotFound
	if rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"}, me()...); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestPatchInvalidArgumentIs400 holds the mapping of the account module's
// shape refusal, on either write.
func TestPatchInvalidArgumentIs400(t *testing.T) {
	cases := map[string]func(*rig){
		"the display name": func(r *rig) { r.admin.nameErr = authn.ErrInvalidArgument },
		"the attributes":   func(r *rig) { r.admin.attrErr = authn.ErrInvalidArgument },
	}
	for name, arm := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			arm(r)
			body := map[string]any{"display_name": "X", "attributes": map[string]string{"a": "b"}}
			if rec := r.do(http.MethodPatch, "/api/v1/auth/me", body, me()...); rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

// TestPatchOtherFailureIs503 holds the fail-closed default and its cause staying
// out of the answer.
func TestPatchOtherFailureIs503(t *testing.T) {
	r := newRig(t)
	r.admin.attrErr = errors.New("database is down")
	rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"attributes": map[string]string{"a": "b"}}, me()...)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "database is down") {
		t.Errorf("the cause reached the caller: %s", rec.Body.String())
	}
	if !strings.Contains(r.logs.String(), "database is down") {
		t.Error("the cause did not reach the log")
	}
}

// TestPatchWritesAreNotOneAtomicChange holds the ordering of the two writes: when
// the display name lands and the attribute set does not, the display name's
// change is in force and the answer is the failure's.
func TestPatchWritesAreNotOneAtomicChange(t *testing.T) {
	r := newRig(t)
	r.admin.attrErr = authn.ErrInvalidArgument
	body := map[string]any{"display_name": "New", "attributes": map[string]string{"a": "b"}}
	rec := r.do(http.MethodPatch, "/api/v1/auth/me", body, me()...)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if r.admin.count("set-display-name") != 1 || r.admin.count("set-attributes") != 1 {
		t.Errorf("writes = display-name %d, attributes %d, want one of each before the failure",
			r.admin.count("set-display-name"), r.admin.count("set-attributes"))
	}
	if r.admin.displayName != "New" {
		t.Errorf("the display name = %q, want the landed New", r.admin.displayName)
	}
}

// TestPatchAuditFailureDoesNotChangeTheAnswer holds the best-effort audit: a row
// that does not land is an Error line and the write still answers 200.
func TestPatchAuditFailureDoesNotChangeTheAnswer(t *testing.T) {
	r := newRig(t)
	r.audit.fails(errors.New("audit is down"))
	if rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"}, me()...); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(r.logs.String(), "audit row could not be written") {
		t.Error("the audit failure did not reach the log")
	}
}

// TestPatchWithoutAnAuditCapabilityStillAnswers holds the optional audit: an
// assembly with no audit capability writes no row and the write still lands.
func TestPatchWithoutAnAuditCapabilityStillAnswers(t *testing.T) {
	r := newRig(t, noAudit)
	if r.p.audit != nil {
		t.Fatal("the rig still holds an audit capability")
	}
	if rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"}, me()...); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestPatchAuditRowIsTheCapabilitysOwn holds the row's shape: the closed action,
// the account as the object, and no field value a client sent.
func TestPatchAuditRowIsTheCapabilitysOwn(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "secret-name"}, me()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	rows := r.audit.entries()
	if len(rows) != 1 {
		t.Fatalf("audit rows = %+v, want one", rows)
	}
	row := rows[0]
	if row.Action != actionProfileUpdate || row.Object != string(meAccount) || row.Outcome != outcomeSuccess {
		t.Errorf("the audit row = %+v, want action %s on %s", row, actionProfileUpdate, meAccount)
	}
	for _, v := range row.Detail {
		if strings.Contains(v, "secret-name") {
			t.Errorf("a client's value reached the audit detail: %v", row.Detail)
		}
	}
}
