package token

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
)

func (r *rig) mountToken() *mounttest.Mounter {
	r.t.Helper()
	m := mounttest.New(r.t)
	r.p.mounter = m
	r.p.mountTokenRoutes()
	return m
}

func form(m *mounttest.Mounter, path string, values url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return m.Do(req)
}

func raw(m *mounttest.Mounter, path, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	return m.Do(req)
}

func (r *rig) goodServiceSecret() authn.SessionID {
	r.t.Helper()
	session := r.serviceSession()
	r.passwords.verify = func(string, string) (authn.AccountID, string, time.Time, error) {
		return account, "secret-1", time.Time{}, nil
	}
	return session
}

func decodeToken(t *testing.T, rec *httptest.ResponseRecorder) AuthnTokenResponse {
	t.Helper()
	var body AuthnTokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the body is not a token response: %v\n%s", err, rec.Body.String())
	}
	return body
}

// decodeError reads a failure body as the members it carries.
func decodeError(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the body is not a JSON object: %v\n%s", err, rec.Body.String())
	}
	return body
}

// TestTheTokenEndpointAnswersAClientCredentialWithAnAccessTokenOnly answers the
// client_credentials grant with an access token and no refresh token.
func TestTheTokenEndpointAnswersAClientCredentialWithAnAccessTokenOnly(t *testing.T) {
	r := newRig(t)
	m := r.mountToken()
	session := r.goodServiceSecret()

	rec := form(m, m.BaseIn("")+tokenPath, url.Values{
		"grant_type":    {grantClientCredentials},
		"client_id":     {string(account)},
		"client_secret": {"spd_cs_whatever"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := decodeToken(t, rec)
	if body.AccessToken == "" || body.TokenType != "Bearer" {
		t.Errorf("the body is %+v, want an access token of type Bearer", body)
	}
	if body.ExpiresIn != int(defaults.AccessTTL.Seconds()) {
		t.Errorf("expires_in = %d, want %d", body.ExpiresIn, int(defaults.AccessTTL.Seconds()))
	}
	if body.RefreshToken != "" {
		t.Errorf("a client_credentials response carried a refresh token %q", body.RefreshToken)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Pragma"); got != "no-cache" {
		t.Errorf("Pragma = %q, want no-cache", got)
	}
	if claims := r.issuedClaims(body.AccessToken); claims["sid"] != string(session) {
		t.Errorf("the token names session %v, want the service session %s", claims["sid"], session)
	}
}

// TestTheTokenEndpointTakesTheClientCredentialFromTheBasicHeader accepts the
// client credential from the Basic header as well as from the form fields.
func TestTheTokenEndpointTakesTheClientCredentialFromTheBasicHeader(t *testing.T) {
	r := newRig(t)
	m := r.mountToken()
	r.goodServiceSecret()

	req := httptest.NewRequest(http.MethodPost, m.BaseIn("")+tokenPath,
		strings.NewReader(url.Values{"grant_type": {grantClientCredentials}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(string(account), "spd_cs_whatever")
	rec := m.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if decodeToken(t, rec).AccessToken == "" {
		t.Error("no access token was issued")
	}
}

// TestTheTokenEndpointAnswersARefreshWithANewPair answers the refresh_token grant
// with a new access token and a fresh refresh token.
func TestTheTokenEndpointAnswersARefreshWithANewPair(t *testing.T) {
	r := newRig(t)
	m := r.mountToken()
	_, first := r.issueRefresh()

	rec := form(m, m.BaseIn("")+tokenPath, url.Values{
		"grant_type":    {grantRefreshToken},
		"refresh_token": {first.Value},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := decodeToken(t, rec)
	if body.AccessToken == "" {
		t.Error("no access token was issued")
	}
	if body.RefreshToken == "" || body.RefreshToken == first.Value {
		t.Errorf("the response carried refresh token %q, want a fresh one", body.RefreshToken)
	}
}

// TestTheTokenEndpointFailuresAreTheOAuthShape answers each failure of the closed
// set with its status and error alone.
func TestTheTokenEndpointFailuresAreTheOAuthShape(t *testing.T) {
	const badSecret = "spd_cs_wrong"

	cases := []struct {
		name       string
		path       string
		build      func(r *rig, m *mounttest.Mounter) *httptest.ResponseRecorder
		wantStatus int
		wantError  string
		wantRetry  string
	}{
		{
			name: "a missing grant_type",
			build: func(r *rig, m *mounttest.Mounter) *httptest.ResponseRecorder {
				return form(m, m.BaseIn("")+tokenPath, url.Values{})
			},
			wantStatus: http.StatusBadRequest, wantError: errInvalidRequest,
		},
		{
			name: "a grant_type outside the set",
			build: func(r *rig, m *mounttest.Mounter) *httptest.ResponseRecorder {
				return form(m, m.BaseIn("")+tokenPath, url.Values{"grant_type": {"password"}})
			},
			wantStatus: http.StatusBadRequest, wantError: errUnsupportedGrantType,
		},
		{
			name: "a body past the limit",
			build: func(r *rig, m *mounttest.Mounter) *httptest.ResponseRecorder {
				return raw(m, m.BaseIn("")+tokenPath, "application/x-www-form-urlencoded",
					"grant_type=refresh_token&refresh_token="+strings.Repeat("a", formLimit))
			},
			wantStatus: http.StatusBadRequest, wantError: errInvalidRequest,
		},
		{
			name: "a body that is not a form",
			build: func(r *rig, m *mounttest.Mounter) *httptest.ResponseRecorder {
				return raw(m, m.BaseIn("")+tokenPath, "application/json", `{"grant_type":"client_credentials"}`)
			},
			wantStatus: http.StatusBadRequest, wantError: errInvalidRequest,
		},
		{
			name: "a client that presented nothing",
			build: func(r *rig, m *mounttest.Mounter) *httptest.ResponseRecorder {
				return form(m, m.BaseIn("")+tokenPath, url.Values{"grant_type": {grantClientCredentials}})
			},
			wantStatus: http.StatusUnauthorized, wantError: errInvalidClient,
		},
		{
			name: "a client credential that did not verify",
			build: func(r *rig, m *mounttest.Mounter) *httptest.ResponseRecorder {
				r.passwords.verify = func(string, string) (authn.AccountID, string, time.Time, error) {
					return "", "", time.Time{}, authn.ErrInvalidCredentials
				}
				return form(m, m.BaseIn("")+tokenPath, url.Values{
					"grant_type":    {grantClientCredentials},
					"client_id":     {string(account)},
					"client_secret": {badSecret},
				})
			},
			wantStatus: http.StatusUnauthorized, wantError: errInvalidClient,
		},
		{
			name: "a client in back-off",
			build: func(r *rig, m *mounttest.Mounter) *httptest.ResponseRecorder {
				r.throttle.retry = 30 * time.Second
				return form(m, m.BaseIn("")+tokenPath, url.Values{
					"grant_type":    {grantClientCredentials},
					"client_id":     {string(account)},
					"client_secret": {badSecret},
				})
			},
			wantStatus: http.StatusUnauthorized, wantError: errInvalidClient, wantRetry: "30",
		},
		{
			name: "a refresh token that is not in the store",
			build: func(r *rig, m *mounttest.Mounter) *httptest.ResponseRecorder {
				return form(m, m.BaseIn("")+tokenPath, url.Values{
					"grant_type":    {grantRefreshToken},
					"refresh_token": {"spd_rt_" + strings.Repeat("A", 43)},
				})
			},
			wantStatus: http.StatusBadRequest, wantError: errInvalidGrant,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			m := r.mountToken()
			rec := c.build(r, m)
			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, c.wantStatus, rec.Body.String())
			}
			body := decodeError(t, rec)
			if len(body) != 1 {
				t.Errorf("the body is %v, want error alone and no error_description", body)
			}
			if body["error"] != c.wantError {
				t.Errorf("error = %v, want %q", body["error"], c.wantError)
			}
			if c.wantError == errInvalidClient {
				if got := rec.Header().Get("WWW-Authenticate"); got != `Basic realm="authn-token"` {
					t.Errorf("WWW-Authenticate = %q, want the Basic challenge", got)
				}
			}
			if got := rec.Header().Get("Retry-After"); got != c.wantRetry {
				t.Errorf("Retry-After = %q, want %q", got, c.wantRetry)
			}
		})
	}
}

// TestTheTokenEndpointAnswersUnavailableWhenTheStoreIsDown answers 503 rather
// than invalid_grant when the store cannot answer.
func TestTheTokenEndpointAnswersUnavailableWhenTheStoreIsDown(t *testing.T) {
	r := newRig(t)
	m := r.mountToken()
	_, first := r.issueRefresh()

	sqlDB, err := r.p.handle.DB()
	if err != nil {
		t.Fatalf("taking the database handle: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("closing the database: %v", err)
	}

	rec := form(m, m.BaseIn("")+tokenPath, url.Values{
		"grant_type":    {grantRefreshToken},
		"refresh_token": {first.Value},
	})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); body["error"] != errTemporarilyUnavail {
		t.Errorf("error = %v, want %q", body["error"], errTemporarilyUnavail)
	}
}

// TestTheRevocationEndpointAnswers200ForATokenOutcome answers 200 with an empty
// body whatever token is presented.
func TestTheRevocationEndpointAnswers200ForATokenOutcome(t *testing.T) {
	t.Run("a live refresh token", func(t *testing.T) {
		r := newRig(t)
		m := r.mountToken()
		id, refresh := r.issueRefresh()
		rec := form(m, m.BaseIn("")+revokePath, url.Values{"token": {refresh.Value}})
		if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
			t.Fatalf("status = %d, body = %q, want 200 and an empty body", rec.Code, rec.Body.String())
		}
		if got := r.sessions.revoked; len(got) != 1 || got[0] != id {
			t.Errorf("the revocation reached %v, want the token's session %s", got, id)
		}
	})
	for _, c := range []struct {
		name  string
		build func(m *mounttest.Mounter) *httptest.ResponseRecorder
	}{
		{"no token", func(m *mounttest.Mounter) *httptest.ResponseRecorder {
			return form(m, m.BaseIn("")+revokePath, url.Values{})
		}},
		{"an unrecognisable token", func(m *mounttest.Mounter) *httptest.ResponseRecorder {
			return form(m, m.BaseIn("")+revokePath, url.Values{"token": {"not-a-token"}})
		}},
		{"a body past the limit", func(m *mounttest.Mounter) *httptest.ResponseRecorder {
			return raw(m, m.BaseIn("")+revokePath, "application/x-www-form-urlencoded",
				"token="+strings.Repeat("a", formLimit))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			m := r.mountToken()
			rec := c.build(m)
			if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
				t.Fatalf("status = %d, body = %q, want 200 and an empty body", rec.Code, rec.Body.String())
			}
		})
	}
	t.Run("a service token", func(t *testing.T) {
		r := newRig(t)
		m := r.mountToken()
		service := r.serviceSession()
		access := r.accessFor(service, string(account))
		rec := form(m, m.BaseIn("")+revokePath, url.Values{"token": {access}})
		if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
			t.Fatalf("status = %d, body = %q, want 200 and an empty body", rec.Code, rec.Body.String())
		}
		if len(r.sessions.revoked) != 0 {
			t.Errorf("a service token revoked %v, want nothing", r.sessions.revoked)
		}
	})
	t.Run("an impersonation token", func(t *testing.T) {
		r := newRig(t)
		m := r.mountToken()
		admin := authn.SessionID("sess-admin-1")
		r.sessions.put(admin, authn.SessionStatus{Active: true, Account: "acct-admin", Kind: authn.SessionToken})
		in := normalIssue(admin)
		in.Actor = "acct-admin"
		in.ImpersonationID = "grant-1"
		r.activeRecord("grant-1", epoch.Add(defaults.AccessTTL))
		access, err := r.p.Issue(context.Background(), in)
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		rec := form(m, m.BaseIn("")+revokePath, url.Values{"token": {access.Value}})
		if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
			t.Fatalf("status = %d, body = %q, want 200 and an empty body", rec.Code, rec.Body.String())
		}
		if len(r.imp.revoked) != 1 || r.imp.revoked[0] != "grant-1" {
			t.Errorf("the endpoint ended %v, want the impersonation record grant-1", r.imp.revoked)
		}
		if len(r.sessions.revoked) != 0 {
			t.Errorf("the endpoint revoked the administrator's session: %v", r.sessions.revoked)
		}
	})
}

// TestTheRevocationEndpointRefusesWhenTheStoreIsDown answers 503, never the 200 a
// presenter would read as "the token is gone".
func TestTheRevocationEndpointRefusesWhenTheStoreIsDown(t *testing.T) {
	r := newRig(t)
	m := r.mountToken()
	_, refresh := r.issueRefresh()
	r.closeStore()

	rec := form(m, m.BaseIn("")+revokePath, url.Values{"token": {refresh.Value}})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); body["error"] != errTemporarilyUnavail {
		t.Errorf("error = %v, want %q", body["error"], errTemporarilyUnavail)
	}
}

// TestTheTokenEndpointsAreCallbacksWithoutTheCrossOriginCheck registers both POST
// routes as callbacks that a cross-site request reaches.
func TestTheTokenEndpointsAreCallbacksWithoutTheCrossOriginCheck(t *testing.T) {
	r := newRig(t)
	m := r.mountToken()

	for _, path := range []string{tokenPath, revokePath} {
		reg, ok := m.Registered(http.MethodPost, m.BaseIn("")+path)
		if !ok || reg.Kind != mounttest.Callback {
			t.Errorf("POST %s is registered as %+v, want a Callback registration", path, reg)
		}
	}

	req := httptest.NewRequest(http.MethodPost, m.BaseIn("")+tokenPath,
		strings.NewReader(url.Values{}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Origin", "https://evil.example")
	rec := m.Do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a cross-site POST was answered %d, want the handler's 400 rather than a 401", rec.Code)
	}
	if decodeError(t, rec)["error"] != errInvalidRequest {
		t.Errorf("a cross-site POST did not reach the handler: %s", rec.Body.String())
	}
}

// TestTheEndpointsReadTheFormTheWayTheDesignFixesIt ignores unknown form
// parameters and keeps the first value of a repeated one.
func TestTheEndpointsReadTheFormTheWayTheDesignFixesIt(t *testing.T) {
	t.Run("an extra parameter is ignored", func(t *testing.T) {
		r := newRig(t)
		m := r.mountToken()
		r.goodServiceSecret()
		rec := form(m, m.BaseIn("")+tokenPath, url.Values{
			"grant_type":    {grantClientCredentials},
			"client_id":     {string(account)},
			"client_secret": {"spd_cs_whatever"},
			"scope":         {"tokens.read"},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a repeated grant_type keeps its first value", func(t *testing.T) {
		r := newRig(t)
		m := r.mountToken()
		r.goodServiceSecret()
		rec := form(m, m.BaseIn("")+tokenPath, url.Values{
			"grant_type":    {grantClientCredentials, "password"},
			"client_id":     {string(account)},
			"client_secret": {"spd_cs_whatever"},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a revocation hint is ignored", func(t *testing.T) {
		r := newRig(t)
		m := r.mountToken()
		id, refresh := r.issueRefresh()
		rec := form(m, m.BaseIn("")+revokePath, url.Values{
			"token":           {refresh.Value},
			"token_type_hint": {"refresh_token"},
		})
		if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
			t.Fatalf("status = %d, body = %q, want 200 and an empty body", rec.Code, rec.Body.String())
		}
		if got := r.sessions.revoked; len(got) != 1 || got[0] != id {
			t.Errorf("the revocation reached %v, want the token's session %s", got, id)
		}
	})
}

// TestTheGrantErrorMappingIsClosed maps every sentinel a grant can return to its
// status and error member.
func TestTheGrantErrorMappingIsClosed(t *testing.T) {
	wrapped := refreshUnavailable("op", authn.ErrUnavailable)
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantError  string
		wantRetry  time.Duration
	}{
		{"no signing key", authn.ErrNoSigningKey, http.StatusServiceUnavailable, errTemporarilyUnavail, 0},
		{"an unavailable store", authn.ErrUnavailable, http.StatusServiceUnavailable, errTemporarilyUnavail, 0},
		{"a wrapped unavailability", wrapped, http.StatusServiceUnavailable, errTemporarilyUnavail, 0},
		{"a bad credential", authn.ErrInvalidCredentials, http.StatusUnauthorized, errInvalidClient, 0},
		{"a client in back-off", &authn.ThrottledError{RetryAfter: 30 * time.Second}, http.StatusUnauthorized, errInvalidClient, 30 * time.Second},
		{"an invalid grant", authn.ErrInvalidGrant, http.StatusBadRequest, errInvalidGrant, 0},
		{"a detected reuse", authn.ErrRefreshReuse, http.StatusBadRequest, errInvalidGrant, 0},
		{"a revoked service session", authn.ErrSessionRevoked, http.StatusBadRequest, errInvalidGrant, 0},
		{"an unclassified failure", errors.New("boom"), http.StatusServiceUnavailable, errTemporarilyUnavail, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := classifyGrantError(c.err)
			if f.status != c.wantStatus || f.code != c.wantError || f.retry != c.wantRetry {
				t.Errorf("classify(%v) = %+v, want status %d, error %q, retry %s",
					c.err, f, c.wantStatus, c.wantError, c.wantRetry)
			}
		})
	}
}

// TestARefreshGrantWithoutATokenIsInvalidRequest answers a refresh_token grant
// with no refresh_token as invalid_request, not invalid_grant.
func TestARefreshGrantWithoutATokenIsInvalidRequest(t *testing.T) {
	r := newRig(t)
	m := r.mountToken()
	rec := form(m, m.BaseIn("")+tokenPath, url.Values{"grant_type": {grantRefreshToken}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); body["error"] != errInvalidRequest {
		t.Errorf("error = %v, want %s", body["error"], errInvalidRequest)
	}
}

// TestAClaimsContributorDefectIsLoggedAtError answers 503 and logs at Error when a
// contributor returns a reserved claim name.
func TestAClaimsContributorDefectIsLoggedAtError(t *testing.T) {
	r := newRig(t)
	r.p.claims = []authn.ClaimsContributor{contributor{claims: map[string]any{"sub": "x"}}}
	m := r.mountToken()
	r.goodServiceSecret()
	rec := form(m, m.BaseIn("")+tokenPath, url.Values{
		"grant_type":    {grantClientCredentials},
		"client_id":     {string(account)},
		"client_secret": {"spd_cs_whatever"},
	})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if logs := r.logs.String(); !strings.Contains(logs, "level=ERROR") {
		t.Errorf("a ClaimsContributor defect was not logged at Error:\n%s", logs)
	}
}
