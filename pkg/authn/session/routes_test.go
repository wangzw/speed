package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
)

// The five self-service routes are mounted through the mounter with the access
// level the design gives each: the session read is optional and impersonable,
// logout is guarded and impersonable, and listing and revoking sessions is
// guarded and not impersonable.
func TestTheSelfServiceRoutesAreMountedWithTheirAccessLevels(t *testing.T) {
	f := newFixture(t, dialects[0])
	want := []mounttest.Registration{
		{Kind: mounttest.Optional, Methods: []string{"GET"}, Path: "/auth/session", Impersonable: true},
		{Kind: mounttest.Guarded, Methods: []string{"POST"}, Path: "/auth/logout", Impersonable: true},
		{Kind: mounttest.Guarded, Methods: []string{"GET"}, Path: "/auth/sessions"},
		{Kind: mounttest.Guarded, Methods: []string{"DELETE"}, Path: "/auth/sessions"},
		{Kind: mounttest.Guarded, Methods: []string{"DELETE"}, Path: "/auth/sessions/:id"},
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
	rec := get(t, f, "/auth/session")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"authenticated":false`) {
		t.Errorf("body = %q, want authenticated false", body)
	}
}

// An identity that is impersonating reaches the session read and logout, which
// are impersonable, and is refused on the three routes that manage sessions.
func TestAnImpersonatingIdentityIsRefusedOnTheSessionManagementRoutes(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	f.accounts.add("acct-victim", nil)
	issued := f.create(newBrowser(alice))
	f.mounter.As(
		authntest.WithAccount("acct-victim"),
		authntest.WithActor(alice),
		authntest.WithSession(issued.ID),
		authntest.WithScope("acme"),
	)

	if rec := get(t, f, "/auth/session"); rec.Code != http.StatusOK {
		t.Errorf("the impersonable session read answered %d, want 200", rec.Code)
	}
	for _, c := range []struct {
		method, path string
	}{
		{http.MethodGet, "/auth/sessions"},
		{http.MethodDelete, "/auth/sessions"},
		{http.MethodDelete, "/auth/sessions/" + string(issued.ID)},
	} {
		if rec := send(t, f, c.method, c.path); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s answered %d, want 401 for an impersonating identity", c.method, c.path, rec.Code)
		}
	}
}

// The listing shows the caller's own live sessions and marks the current one.
func TestTheListingShowsTheCallersSessions(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	current := f.create(newBrowser(alice))
	other := f.create(newBrowser(alice))
	f.mounter.As(authntest.WithAccount(alice), authntest.WithSession(current.ID))

	rec := get(t, f, "/auth/sessions")
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

	rec := send(t, f, http.MethodPost, "/auth/logout")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if !f.row(issued.ID).RevokedAt.Valid {
		t.Error("the session was not revoked")
	}
	if got := cookiesOf(rec.Header()); len(got) != 1 || got[0].MaxAge >= 0 {
		t.Errorf("logout wrote %+v, want one clearing cookie", got)
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

// The handlers of the session-management routes refuse an impersonating identity
// themselves, so the refusal does not rest on the gateway's guard alone. Here
// each is called without the gateway in front, with the identity on the request.
func TestAnImpersonatingIdentityIsRefusedByTheHandlersThemselves(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	f.accounts.add("acct-victim", nil)
	issued := f.create(newBrowser(alice))
	id := authntest.Context(t, context.Background(),
		authntest.WithAccount("acct-victim"),
		authntest.WithActor(alice),
		authntest.WithSession(issued.ID),
		authntest.WithScope("acme"),
	)

	check := func(name string, w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: the handler alone answered %d, want 403", name, w.Code)
		}
		var p struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || p.Type != "urn:speed:authn:"+mount.CodeImpersonationRestricted {
			t.Errorf("%s: the handler alone answered %s, want impersonation-restricted (%v)", name, w.Body.String(), err)
		}
	}
	call := func(method, path string, params gin.Params, h gin.HandlerFunc) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(method, path, nil).WithContext(id)
		c.Params = params
		h(c)
		return w
	}

	check("GET /sessions", call(http.MethodGet, "/auth/sessions", nil, f.p.handleSessions))
	check("DELETE /sessions", call(http.MethodDelete, "/auth/sessions", nil, f.p.handleRevokeOthers))
	check("DELETE /sessions/:id", call(http.MethodDelete, "/auth/sessions/"+string(issued.ID),
		gin.Params{{Key: "id", Value: string(issued.ID)}}, f.p.handleRevokeOne))

	if f.row(issued.ID).RevokedAt.Valid {
		t.Error("an impersonating identity revoked a session through a handler")
	}
}

// Revoking another of the caller's sessions leaves the caller logged in: the
// cookie is cleared only when the revoked session is the one the request came
// on.
func TestRevokingAnotherOfTheCallersSessionsKeepsTheCookie(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	current := f.create(newBrowser(alice))
	other := f.create(newBrowser(alice))
	f.mounter.As(authntest.WithAccount(alice), authntest.WithSession(current.ID))

	rec := send(t, f, http.MethodDelete, "/auth/sessions/"+string(other.ID))
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

	rec := send(t, f, http.MethodDelete, "/auth/sessions/"+string(svc))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if f.row(svc).RevokedAt.Valid {
		t.Error("the service session was revoked through the route")
	}
}
