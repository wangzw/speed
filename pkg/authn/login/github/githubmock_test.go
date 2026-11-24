package github

import (
	"crypto/x509"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/vislake/speed/pkg/authn/internal/egress"
)

const (
	testClientID     = "the-client-id"
	testClientSecret = "the-client-secret"
	testRedirectURI  = "https://app.example/api/v1/auth/callback/github"
	testToken        = "github-access-token"
)

type githubMock struct {
	server *httptest.Server
	pool   *x509.CertPool

	mu           sync.Mutex
	tokenStatus  int    // 0 means 200
	tokenError   string // the OAuth error a refused exchange is answered with
	userStatus   int    // 0 means 200
	emailsStatus int    // 0 means 200
	user         githubUser
	userEmail    string // the address /user carries, which must never be used
	userRaw      string // when non-empty, the raw body /user answers with
	emails       []githubEmail

	verifier    string
	authHeader  string
	versionHead string
	acceptHead  string
	agentHead   string
	tokenCalls  int
	userCalls   int
	emailsCalls int
}

func newGitHubMock(t *testing.T) *githubMock {
	t.Helper()
	m := &githubMock{
		user: githubUser{ID: 4242, Login: "ada", Name: "Ada Lovelace"},
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(m.route))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	m.server = srv
	m.pool = x509.NewCertPool()
	m.pool.AddCert(srv.Certificate())
	return m
}

func (m *githubMock) route(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/login/oauth/access_token":
		m.token(w, r)
	case "/user":
		m.readUser(w, r)
	case "/user/emails":
		m.readEmails(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (m *githubMock) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	m.mu.Lock()
	m.tokenCalls++
	m.verifier = r.PostForm.Get("code_verifier")
	status, errCode := m.tokenStatus, m.tokenError
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if status != 0 || errCode != "" {
		if status == 0 {
			status = http.StatusBadRequest
		}
		if errCode == "" {
			errCode = "invalid_grant"
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":`+quote(errCode)+`}`)
		return
	}
	_, _ = io.WriteString(w, `{"access_token":`+quote(testToken)+`,"token_type":"bearer",`+
		`"scope":"read:user user:email"}`)
}

// readUser answers GET /user. The address the account carries is always present
// in the body and is never read by the method.
func (m *githubMock) readUser(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.userCalls++
	m.authHeader = r.Header.Get("Authorization")
	m.versionHead = r.Header.Get("X-GitHub-Api-Version")
	m.acceptHead = r.Header.Get("Accept")
	m.agentHead = r.Header.Get("User-Agent")
	status, user, email, raw := m.userStatus, m.user, m.userEmail, m.userRaw
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if status != 0 {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"message":"nope"}`)
		return
	}
	if raw != "" {
		_, _ = io.WriteString(w, raw)
		return
	}
	body, _ := json.Marshal(struct {
		githubUser
		Email string `json:"email"`
	}{user, email})
	_, _ = w.Write(body)
}

func (m *githubMock) readEmails(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.emailsCalls++
	status, emails := m.emailsStatus, m.emails
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if status != 0 {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"message":"nope"}`)
		return
	}
	body, _ := json.Marshal(emails)
	_, _ = w.Write(body)
}

func (m *githubMock) setToken(status int, errCode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokenStatus, m.tokenError = status, errCode
}

func (m *githubMock) setUser(u githubUser, email string, status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.user, m.userEmail, m.userStatus = u, email, status
}

func (m *githubMock) setUserRaw(body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userRaw = body
}

func (m *githubMock) setEmails(emails []githubEmail, status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.emails, m.emailsStatus = emails, status
}

func (m *githubMock) seen() (tokenCalls, userCalls, emailsCalls int, verifier, authHeader string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tokenCalls, m.userCalls, m.emailsCalls, m.verifier, m.authHeader
}

func (m *githubMock) seenHeaders() (version, accept, agent string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.versionHead, m.acceptHead, m.agentHead
}

func quote(s string) string {
	return `"` + s + `"`
}

func resolvedFor(m *githubMock) resolved {
	return resolved{
		clientID:     testClientID,
		clientSecret: testClientSecret,
		redirectURI:  testRedirectURI,
		endpoint: oauth2.Endpoint{
			AuthURL:  m.server.URL + "/login/oauth/authorize",
			TokenURL: m.server.URL + "/login/oauth/access_token",
		},
		apiBase: m.server.URL,
	}
}

func methodFor(m *githubMock) *method {
	return newMethod(resolvedFor(m), egress.New(egress.Options{AllowLoopback: true, RootCAs: m.pool}))
}

func methodWithoutLoopback(m *githubMock) *method {
	return newMethod(resolvedFor(m), egress.New(egress.Options{RootCAs: m.pool}))
}
