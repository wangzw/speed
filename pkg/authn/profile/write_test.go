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

func anHourOld() []authntest.Option {
	return []authntest.Option{
		authntest.WithAccount(meAccount),
		authntest.WithSession(meSession),
		authntest.WithAuthTime(time.Now().Add(-time.Hour)),
	}
}

func noSession() []authntest.Option {
	return []authntest.Option{
		authntest.WithAccount(meAccount),
		authntest.WithAuthTime(time.Now()),
	}
}

func (r *rig) sendBody(method, path, contentType string, body io.Reader, opts ...authntest.Option) *httptest.ResponseRecorder {
	r.t.Helper()
	r.mounter.As(opts...)
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return r.mounter.Do(req)
}

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

// TestPatchDecodesTheBodyByTheCapabilityRule checks the decode rule's media type, size and shape limits and problem codes.
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

// TestPatchNullBodyChangesNothing checks that a null body binds as zero, writes nothing and audits nothing.
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

// TestPatchAcceptsABodyAtTheCapabilityLimit checks that a body of exactly mount.MaxBodyBytes is processed.
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

// TestPatchAcceptsAUTF8Charset checks that charset=utf-8 is the one accepted media-type parameter.
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

// TestPatchWritesTheDisplayName checks that only SetDisplayName runs and the answer carries the written value.
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

// TestPatchGivenAttributesReplacesTheWholeSet checks that a given set replaces the current one and only SetAttributes runs.
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

// TestPatchMergesLocaleAndTimeZoneIntoTheAttributeSet checks that each preference overwrites its key and leaves the rest alone.
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

// TestPatchEmptyLocaleClearsTheKey checks that an empty locale removes its key and leaves the rest alone.
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

// TestPatchMergesColorModeIntoTheAttributeSet checks that the color mode overwrites its key and leaves the rest alone.
func TestPatchMergesColorModeIntoTheAttributeSet(t *testing.T) {
	r := newRig(t)
	body := map[string]any{"locale": "zh-CN", "time_zone": "Asia/Shanghai", "color_mode": "dark"}
	rec := r.do(http.MethodPatch, "/api/v1/auth/me", body, me()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	attrs, _ := r.admin.lastAttributes()
	if attrs["dept"] != "eng" {
		t.Errorf("the merge dropped the current attribute dept: %v", attrs)
	}
	if attrs[i18n.LocaleAttributeKey] != "zh-CN" || attrs[i18n.TimeZoneAttributeKey] != "Asia/Shanghai" ||
		attrs[ColorModeAttributeKey] != "dark" {
		t.Errorf("the merge did not write the three preferences: %v", attrs)
	}
	got := decodePatch(t, rec)
	if got.Locale != "zh-CN" || got.TimeZone != "Asia/Shanghai" || got.ColorMode != "dark" {
		t.Errorf("the answer's locale/time_zone/color_mode = %q/%q/%q, want zh-CN/Asia/Shanghai/dark",
			got.Locale, got.TimeZone, got.ColorMode)
	}
}

// TestPatchEmptyColorModeClearsTheKey checks that an empty color mode removes its key and leaves the rest alone.
func TestPatchEmptyColorModeClearsTheKey(t *testing.T) {
	r := newRig(t)
	r.accounts.put(meAccount, authn.Account{
		Kind: authn.KindUser, Status: authn.StatusActive,
		Attributes: map[string]string{ColorModeAttributeKey: "dark", "dept": "eng"},
	})
	rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"color_mode": ""}, me()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	attrs, _ := r.admin.lastAttributes()
	if _, present := attrs[ColorModeAttributeKey]; present {
		t.Errorf("the empty color mode did not clear the key: %v", attrs)
	}
	if attrs["dept"] != "eng" {
		t.Errorf("clearing the color mode dropped another attribute: %v", attrs)
	}
}

// TestPatchInvalidColorModeIs400 checks that a value outside light, dark and system is a 400 that reaches no write.
func TestPatchInvalidColorModeIs400(t *testing.T) {
	cases := map[string]string{
		"a mode outside the set":       "sepia",
		"a differently cased mode":     "Dark",
		"a mode the set does not name": "auto",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"color_mode": value}, me()...)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if got, want := problemTypeOf(t, rec), typePrefix+mount.CodeBadRequest; got != want {
				t.Errorf("problem type = %q, want %q", got, want)
			}
			if r.admin.count("set-attributes") != 0 {
				t.Error("a refused value reached a write")
			}
		})
	}
}

// TestPatchColorModeAloneWritesTheAttributeSetAndTheAuditRow checks that a color-mode-only body writes the set once and audits it.
func TestPatchColorModeAloneWritesTheAttributeSetAndTheAuditRow(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"color_mode": "system"}, me()...)
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
	if attrs[ColorModeAttributeKey] != "system" {
		t.Errorf("the written color mode = %q, want system", attrs[ColorModeAttributeKey])
	}
	if attrs["dept"] != "eng" {
		t.Errorf("the merge dropped the current attribute dept: %v", attrs)
	}
	rows := r.audit.entries()
	if len(rows) != 1 || rows[0].Action != actionProfileUpdate {
		t.Errorf("audit rows = %+v, want one %s row", rows, actionProfileUpdate)
	}
}

// TestPatchEmptyBodyChangesNothing checks that an empty body calls neither write and writes no audit row.
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

// TestPatchUnknownFieldIs400 checks that an unknown field is refused and no write is made.
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

// TestPatchInvalidLocaleAndTimeZoneAre400 checks the locale and time-zone value domains.
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

// TestPatchRefusalsCarryTheCapabilityProblemType checks that a value outside its domain answers with the capability's problem type.
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

// TestPatchRefusesBeforeTheBodyIsRead checks that the identity and session are decided before the body is bound.
func TestPatchRefusesBeforeTheBodyIsRead(t *testing.T) {
	cases := []struct {
		name string
		opts []authntest.Option
		want int
	}{
		{"no session", noSession(), http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)

			body := map[string]any{"unknown": "member"}
			rec := r.invoke(r.p.patchEntry(), http.MethodPatch, "/api/v1/auth/me", body, tc.opts...)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d before the body is read (body %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// TestPatchIsReachableWhileImpersonating checks that an impersonating identity writes the impersonated account's profile.
func TestPatchIsReachableWhileImpersonating(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"}, impersonating()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("through the gateway: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := r.admin.count("set-display-name"); got != 1 {
		t.Errorf("SetDisplayName calls = %d, want 1", got)
	}

	rows := r.audit.entries()
	if len(rows) != 1 {
		t.Fatalf("audit rows = %+v, want one: a write that now reaches the account is audited", rows)
	}
	if row := rows[0]; row.Action != actionProfileUpdate || row.Object != "account:"+string(meAccount) || row.Outcome != outcomeSuccess {
		t.Errorf("the audit row = %+v, want %s on the effective account %s", row, actionProfileUpdate, meAccount)
	}
	ids, found := r.audit.writtenUnder()
	if len(ids) != 1 || !found[0] {
		t.Fatalf("the audit row was written under %d identities (found %v), want one carrying the request's", len(ids), found)
	}
	id := ids[0]
	scope, scoped := id.Scope()
	if !id.Impersonating() || id.Key() != string(otherActor) || id.Account() != meAccount ||
		!scoped || scope != tenantA || id.Session() != meSession {
		t.Errorf("the audit's identity is key %q, account %q, scope %q (scoped %t), session %q (impersonating %t); "+
			"want the operator %q acting for %q in %q under %q, the facts the row's actor and its "+
			"on_behalf_of/impersonation_scope/session detail are written from",
			id.Key(), id.Account(), scope, scoped, id.Session(), id.Impersonating(),
			otherActor, meAccount, tenantA, meSession)
	}

	r = newRig(t)
	rec = r.invoke(r.p.patchEntry(), http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"}, impersonating()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("the handler while impersonating: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := r.admin.count("set-display-name"); got != 1 {
		t.Errorf("the handler alone: SetDisplayName calls = %d, want 1", got)
	}
}

// TestPatchWithoutASessionIs401 checks the login-session requirement.
func TestPatchWithoutASessionIs401(t *testing.T) {
	r := newRig(t)
	if rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"}, noSession()...); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if r.admin.count("set-display-name") != 0 {
		t.Error("a session-less write reached a capability")
	}
}

// TestPatchWithoutAnIdentityIs401 checks the no-identity refusal at the handler.
func TestPatchWithoutAnIdentityIs401(t *testing.T) {
	r := newRig(t)
	rec := r.invokeAnonymous(r.p.patchEntry(), http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestPatchWithAnOlderAuthenticationStillWrites checks that an hour-old authentication still writes.
func TestPatchWithAnOlderAuthenticationStillWrites(t *testing.T) {
	r := newRig(t)
	rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"}, anHourOld()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := r.admin.count("set-display-name"); got != 1 {
		t.Errorf("SetDisplayName calls = %d, want 1", got)
	}
	if r.admin.displayName != "X" {
		t.Errorf("the written display name = %q, want X", r.admin.displayName)
	}
}

// TestPatchOfAGoneAccountIs401 checks that the identity's own account gone is a 401.
func TestPatchOfAGoneAccountIs401(t *testing.T) {
	r := newRig(t)
	r.accounts.getErr = authn.ErrAccountNotFound
	if rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"}, me()...); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestPatchInvalidArgumentIs400 checks that the account module's shape refusal maps to 400 on either write.
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

// TestPatchOtherFailureIs503 checks that a failure no sentinel names is a 503 whose cause stays out of the answer.
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

// TestPatchWritesAreNotOneAtomicChange checks that a failed attribute write leaves the display name in force.
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

// TestPatchAuditFailureDoesNotChangeTheAnswer checks that a failed audit row logs an Error and still answers 200.
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

// TestPatchWithoutAnAuditCapabilityStillAnswers checks that an assembly with no audit capability still writes.
func TestPatchWithoutAnAuditCapabilityStillAnswers(t *testing.T) {
	r := newRig(t, noAudit)
	if r.p.audit != nil {
		t.Fatal("the rig still holds an audit capability")
	}
	if rec := r.do(http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "X"}, me()...); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestPatchAuditRowIsTheCapabilitysOwn checks that the row carries the closed action and no client value.
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
	if row.Action != actionProfileUpdate || row.Object != "account:"+string(meAccount) || row.Outcome != outcomeSuccess {
		t.Errorf("the audit row = %+v, want action %s on %s", row, actionProfileUpdate, meAccount)
	}
	for _, v := range row.Detail {
		if strings.Contains(v, "secret-name") {
			t.Errorf("a client's value reached the audit detail: %v", row.Detail)
		}
	}
}
