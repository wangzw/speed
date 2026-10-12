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

// The fixed values a case drives the method with. They stand for a deployment's
// configuration.
const (
	testClientID     = "the-client-id"
	testClientSecret = "the-client-secret"
	testRedirectURI  = "https://app.example/auth/callback/github"
	testToken        = "github-access-token"
)

// githubMock stands in for GitHub's token endpoint and API endpoints. It runs
// over TLS on the loopback interface, so the method is driven through the
// restricted client of internal/egress pointed at a development address, and it
// records what it was asked so a case reads the shape of a request back.
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

// newGitHubMock starts the server with the answers a plain successful flow
// needs: an access token, an account and no addresses.
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

// route dispatches one request to the endpoint it names.
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

// token answers the exchange. It records the verifier and the code the method
// sent and returns the configured outcome: an error, a non-200, or an access
// token.
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

// readEmails answers GET /user/emails.
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

// setToken configures the exchange's outcome: a non-zero status, an OAuth error
// code, or both. An empty error with a zero status is the success path.
func (m *githubMock) setToken(status int, errCode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokenStatus, m.tokenError = status, errCode
}

// setUser configures the account and the address GET /user carries.
func (m *githubMock) setUser(u githubUser, email string, status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.user, m.userEmail, m.userStatus = u, email, status
}

// setUserRaw configures the raw body GET /user answers with, so a case can drive
// a 200 whose body does not decode.
func (m *githubMock) setUserRaw(body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userRaw = body
}

// setEmails configures GET /user/emails.
func (m *githubMock) setEmails(emails []githubEmail, status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.emails, m.emailsStatus = emails, status
}

// seen returns the recorded requests under one read.
func (m *githubMock) seen() (tokenCalls, userCalls, emailsCalls int, verifier, authHeader string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tokenCalls, m.userCalls, m.emailsCalls, m.verifier, m.authHeader
}

// seenHeaders returns the request headers the account read carried.
func (m *githubMock) seenHeaders() (version, accept, agent string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.versionHead, m.acceptHead, m.agentHead
}

// quote renders a string as a JSON string, for the bodies a case configures.
func quote(s string) string {
	return `"` + s + `"`
}

// resolvedFor is the configuration the method runs on in a case: the mock's
// addresses stand in for GitHub's.
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

// methodFor builds the method over the mock through the restricted client,
// which admits the loopback address as a development one.
func methodFor(m *githubMock) *method {
	return newMethod(resolvedFor(m), egress.New(egress.Options{AllowLoopback: true, RootCAs: m.pool}))
}

// methodWithoutLoopback builds the method over the mock through the production
// client, which refuses the loopback address.
func methodWithoutLoopback(m *githubMock) *method {
	return newMethod(resolvedFor(m), egress.New(egress.Options{RootCAs: m.pool}))
}
