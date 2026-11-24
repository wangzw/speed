package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
)

// TestTheSelfServiceRoutesAreMountedWithTheirAccessLevels holds each self-service
// route's access level and impersonability.
func TestTheSelfServiceRoutesAreMountedWithTheirAccessLevels(t *testing.T) {
	f := newFixture(t, dialects[0])
	want := []mounttest.Registration{
		{Kind: mounttest.Optional, Methods: []string{"GET"}, Path: "/api/v1/auth/session", Impersonable: true},
		{Kind: mounttest.Guarded, Methods: []string{"POST"}, Path: "/api/v1/auth/logout", Impersonable: true},
		{Kind: mounttest.Guarded, Methods: []string{"GET"}, Path: "/api/v1/auth/sessions", Impersonable: true},
		{Kind: mounttest.Guarded, Methods: []string{"DELETE"}, Path: "/api/v1/auth/sessions", Impersonable: true},
		{Kind: mounttest.Guarded, Methods: []string{"DELETE"}, Path: "/api/v1/auth/sessions/:id", Impersonable: true},
	}
	got := f.mounter.Registrations()
	if len(got) != len(want) {
		t.Fatalf("mounted %d routes, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Kind != want[i].Kind || got[i].Path != want[i].Path || got[i].Impersonable != want[i].Impersonable ||
			len(got[i].Methods) != 1 || got[i].Methods[0] != want[i].Methods[0] {
			t.Errorf("route %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A request that carries no credential still gets an answer from the session
// read, not a 401: a front end can ask on startup.
func TestAnAnonymousSessionReadSaysNobodyIsLoggedIn(t *testing.T) {
	f := newFixture(t, dialects[0])
	rec := get(t, f, "/api/v1/auth/session")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"authenticated":false`) {
		t.Errorf("body = %q, want authenticated false", body)
	}
}

// An identity that is impersonating reaches every session route.
func TestAnImpersonatingIdentityManagesTheTargetsSessions(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	f.accounts.add("acct-victim", nil)
	operators := f.create(newBrowser(alice))
	victimCurrent := f.create(newBrowser("acct-victim"))
	victimOther := f.create(newBrowser("acct-victim"))
	f.mounter.As(
		authntest.WithAccount("acct-victim"),
		authntest.WithActor(alice),
		authntest.WithSession(operators.ID),
		authntest.WithScope("acme"),
	)

	rec := get(t, f, "/api/v1/auth/sessions")
	if rec.Code != http.StatusOK {
		t.Fatalf("the listing answered %d, want 200: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, string(victimCurrent.ID)) || !strings.Contains(body, string(victimOther.ID)) {
		t.Errorf("the listing does not name the impersonated account's sessions:\n%s", body)
	}
	if strings.Contains(body, string(operators.ID)) {
		t.Errorf("the listing names the operator's own session:\n%s", body)
	}

	if rec := send(t, f, http.MethodDelete, "/api/v1/auth/sessions/"+string(victimOther.ID)); rec.Code != http.StatusNoContent {
		t.Fatalf("revoking the target's session answered %d, want 204", rec.Code)
	}
	if !f.row(victimOther.ID).RevokedAt.Valid {
		t.Error("the impersonated account's session was not revoked")
	}
	if rec := send(t, f, http.MethodDelete, "/api/v1/auth/sessions/"+string(operators.ID)); rec.Code != http.StatusForbidden {
		t.Errorf("revoking the operator's own session answered %d, want 403", rec.Code)
	}
	if f.row(operators.ID).RevokedAt.Valid {
		t.Error("the operator's own session was revoked")
	}

	if rec := send(t, f, http.MethodDelete, "/api/v1/auth/sessions"); rec.Code != http.StatusOK {
		t.Fatalf("revoking the others answered %d, want 200", rec.Code)
	}
	if !f.row(victimCurrent.ID).RevokedAt.Valid {
		t.Error("the impersonated account's remaining session was not revoked")
	}
	if f.row(operators.ID).RevokedAt.Valid {
		t.Error("the operator's own session was revoked by the target's revoke-the-others")
	}
}

// The listing shows the caller's own live sessions and marks the current one.
func TestTheListingShowsTheCallersSessions(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	current := f.create(newBrowser(alice))
	other := f.create(newBrowser(alice))
	f.mounter.As(authntest.WithAccount(alice), authntest.WithSession(current.ID))

	rec := get(t, f, "/api/v1/auth/sessions")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, string(current.ID)) || !strings.Contains(body, string(other.ID)) {
		t.Errorf("the listing does not name both sessions:\n%s", body)
	}
	if !strings.Contains(body, `"current":true`) {
		t.Errorf("the listing does not mark the current session:\n%s", body)
	}
}

// Logout revokes the session the request carries and clears the cookie.
func TestLogoutRevokesTheCallersSession(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	issued := f.create(newBrowser(alice))
	f.mounter.As(authntest.WithAccount(alice), authntest.WithSession(issued.ID))

	rec := send(t, f, http.MethodPost, "/api/v1/auth/logout")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if !f.row(issued.ID).RevokedAt.Valid {
		t.Error("the session was not revoked")
	}
	if got := cookiesOf(rec.Header()); len(got) != 1 || got[0].MaxAge >= 0 {
		t.Errorf("logout wrote %+v, want one clearing cookie", got)
	}
	if got := rec.Header().Values("Cache-Control"); !slices.Contains(got, "no-store") ||
		!slices.Contains(got, `no-cache="Set-Cookie"`) {
		t.Errorf("logout answered Cache-Control %v, want no-store and %q", got, `no-cache="Set-Cookie"`)
	}
}

// Every self-service revocation route writes the seam-level audit row of the
// action it performed.
func TestTheSelfServiceRevocationRoutesWriteTheirAuditRows(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		path    func(current, first, second authn.SessionID) string
		action  string
		objects func(current, first, second authn.SessionID) []string
	}{
		{
			name:    "logout",
			method:  http.MethodPost,
			path:    func(authn.SessionID, authn.SessionID, authn.SessionID) string { return "/api/v1/auth/logout" },
			action:  "authn.logout",
			objects: func(current, _, _ authn.SessionID) []string { return []string{string(current)} },
		},
		{
			name:   "revoking the other sessions",
			method: http.MethodDelete,
			path:   func(authn.SessionID, authn.SessionID, authn.SessionID) string { return "/api/v1/auth/sessions" },
			action: "authn.session.revoke",
			objects: func(_, first, second authn.SessionID) []string {
				return []string{string(first), string(second)}
			},
		},
		{
			name:    "revoking one session by identifier",
			method:  http.MethodDelete,
			path:    func(_, first, _ authn.SessionID) string { return "/api/v1/auth/sessions/" + string(first) },
			action:  "authn.session.revoke",
			objects: func(_, first, _ authn.SessionID) []string { return []string{string(first)} },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, dialects[0])
			f.accounts.add(alice, nil)
			current := f.create(newBrowser(alice))
			first := f.create(newBrowser(alice))
			second := f.create(newBrowser(alice))
			f.mounter.As(authntest.WithAccount(alice), authntest.WithSession(current.ID))
			sink := f.withAudit()

			rec := send(t, f, tc.method, tc.path(current.ID, first.ID, second.ID))
			if rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 204 or 200", rec.Code)
			}
			var got []string
			for _, row := range sink.entries() {
				if row.Action != tc.action || row.Outcome != "success" {
					t.Errorf("the audit row = %+v, want action %q and outcome success", row, tc.action)
					continue
				}
				got = append(got, row.Object)
			}
			want := tc.objects(current.ID, first.ID, second.ID)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("the audit names the sessions %v, want %v", got, want)
			}
		})
	}
}

// A bulk revocation with nothing to name revokes nothing and writes no row.
func TestRevokingTheOtherSessionsWithNoneToNameRevokesNothing(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	current := f.create(newBrowser(alice))
	f.mounter.As(authntest.WithAccount(alice), authntest.WithSession(current.ID))
	sink := f.withAudit()

	rec := send(t, f, http.MethodDelete, "/api/v1/auth/sessions")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"revoked":0}` {
		t.Errorf("the answer is %s, want no session revoked", got)
	}
	if rows := sink.entries(); len(rows) != 0 {
		t.Errorf("the audit holds %v, want no row: no session was revoked", rows)
	}
	if f.row(current.ID).RevokedAt.Valid {
		t.Error("the session the route keeps was revoked")
	}
}

func get(t *testing.T, f *fixture, path string) *httptest.ResponseRecorder {
	t.Helper()
	return send(t, f, http.MethodGet, path)
}

func send(t *testing.T, f *fixture, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := newRequest("", "").r
	r.Method = method
	r.URL.Path = path
	return f.mounter.Do(r)
}

// The handlers of the session-management routes act for the effective account
// themselves, so the behaviour does not rest on the gateway's guard alone.
func TestAnImpersonatingIdentityHandlersActOnTheTarget(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	f.accounts.add("acct-victim", nil)
	operators := f.create(newBrowser(alice))
	victim := f.create(newBrowser("acct-victim"))
	id := authntest.Context(t, context.Background(),
		authntest.WithAccount("acct-victim"),
		authntest.WithActor(alice),
		authntest.WithSession(operators.ID),
		authntest.WithScope("acme"),
	)

	call := func(method, path string, params gin.Params, h gin.HandlerFunc) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(method, path, nil).WithContext(id)
		c.Params = params
		h(c)
		return w
	}

	listed := call(http.MethodGet, "/api/v1/auth/sessions", nil, listOperation.Handle(f.p.handleSessions))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), string(victim.ID)) {
		t.Errorf("the handler alone listed %d %s, want the impersonated account's session", listed.Code, listed.Body.String())
	}
	one := call(http.MethodDelete, "/api/v1/auth/sessions/"+string(victim.ID),
		gin.Params{{Key: "id", Value: string(victim.ID)}}, revokeOneOperation.Handle(f.p.handleRevokeOne))
	if one.Code != http.StatusNoContent {
		t.Errorf("the handler alone revoked the target's session with %d, want 204", one.Code)
	}
	if !f.row(victim.ID).RevokedAt.Valid {
		t.Error("the handler alone did not revoke the impersonated account's session")
	}
	others := call(http.MethodDelete, "/api/v1/auth/sessions", nil, revokeOthersOperation.Handle(f.p.handleRevokeOthers))
	if others.Code != http.StatusOK {
		t.Errorf("the handler alone revoked the others with %d, want 200", others.Code)
	}
	if f.row(operators.ID).RevokedAt.Valid {
		t.Error("the handler alone revoked the operator's own session")
	}
}

// TestTheHandlersOwn401CarriesTheCapabilityChallenge holds that a bare 401 carries
// the capability's challenge and a problem document.
func TestTheHandlersOwn401CarriesTheCapabilityChallenge(t *testing.T) {
	f := newFixture(t, dialects[0])
	cases := []struct {
		name    string
		method  string
		path    string
		params  gin.Params
		handler gin.HandlerFunc
	}{
		{
			"GET /sessions", http.MethodGet, "/api/v1/auth/sessions", nil,
			listOperation.Handle(f.p.handleSessions),
		},
		{
			"DELETE /sessions", http.MethodDelete, "/api/v1/auth/sessions", nil,
			revokeOthersOperation.Handle(f.p.handleRevokeOthers),
		},
		{
			"DELETE /sessions/:id", http.MethodDelete, "/api/v1/auth/sessions/unknown",
			gin.Params{{Key: "id", Value: "unknown"}},
			revokeOneOperation.Handle(f.p.handleRevokeOne),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(tc.method, tc.path, nil)
			c.Request.Header.Set("Sec-Fetch-Site", "same-origin")
			c.Params = tc.params
			mount.Wrap(f.mounter.CSRF(), f.mounter.Endpoint(), tc.handler)(c)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", w.Code)
			}
			if got, want := w.Header().Get("WWW-Authenticate"), `Bearer realm="main", Cookie realm="main"`; got != want {
				t.Errorf("WWW-Authenticate = %q, want %q", got, want)
			}
			var p struct {
				Type   string `json:"type"`
				Status int    `json:"status"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
				t.Fatalf("the answer is not a problem document: %v", err)
			}
			if p.Type != "about:blank" || p.Status != http.StatusUnauthorized {
				t.Errorf("the problem is %+v, want the default 401 document", p)
			}
		})
	}
}

// TestRevokingAnotherOfTheCallersSessionsKeepsTheCookie holds that revoking a
// different session leaves the caller's cookie in place.
func TestRevokingAnotherOfTheCallersSessionsKeepsTheCookie(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	current := f.create(newBrowser(alice))
	other := f.create(newBrowser(alice))
	f.mounter.As(authntest.WithAccount(alice), authntest.WithSession(current.ID))

	rec := send(t, f, http.MethodDelete, "/api/v1/auth/sessions/"+string(other.ID))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if !f.row(other.ID).RevokedAt.Valid {
		t.Error("the other session was not revoked")
	}
	if f.row(current.ID).RevokedAt.Valid {
		t.Error("the current session was revoked")
	}
	if got := cookiesOf(rec.Header()); len(got) != 0 {
		t.Errorf("revoking another session wrote %+v, want no cookie: the caller is still logged in", got)
	}
	if got := rec.Header().Values("Cache-Control"); slices.Contains(got, `no-cache="Set-Cookie"`) {
		t.Errorf("revoking another session answered Cache-Control %v, want no Set-Cookie directive: no cookie was written", got)
	}
}

// TestRevokingTheCurrentSessionClearsTheCookieWithTheCacheDirective holds that
// revoking the request's own session clears the cookie.
func TestRevokingTheCurrentSessionClearsTheCookieWithTheCacheDirective(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	current := f.create(newBrowser(alice))
	f.mounter.As(authntest.WithAccount(alice), authntest.WithSession(current.ID))

	rec := send(t, f, http.MethodDelete, "/api/v1/auth/sessions/"+string(current.ID))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if !f.row(current.ID).RevokedAt.Valid {
		t.Error("the current session was not revoked")
	}
	if got := cookiesOf(rec.Header()); len(got) != 1 || got[0].MaxAge >= 0 {
		t.Errorf("revoking the current session wrote %+v, want one clearing cookie", got)
	}
	if got := rec.Header().Values("Cache-Control"); !slices.Contains(got, "no-store") ||
		!slices.Contains(got, `no-cache="Set-Cookie"`) {
		t.Errorf("revoking the current session answered Cache-Control %v, want no-store and %q", got, `no-cache="Set-Cookie"`)
	}
}

// A service session is not one the self-service route revokes by identifier: the
// set of sessions a caller manages of their own is the browser and token kinds.
func TestAServiceSessionCannotBeRevokedByIDThroughTheRoute(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	current := f.create(newBrowser(alice))
	svc, err := f.p.ForCredential(context.Background(), alice, "cred-svc", nil)
	noErr(t, err)
	f.mounter.As(authntest.WithAccount(alice), authntest.WithSession(current.ID))

	rec := send(t, f, http.MethodDelete, "/api/v1/auth/sessions/"+string(svc))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if f.row(svc).RevokedAt.Valid {
		t.Error("the service session was revoked through the route")
	}
}
