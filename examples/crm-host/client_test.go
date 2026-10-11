package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"testing"
	"time"
)

// credential is what a request authenticates with.
type credential interface {
	apply(r *nethttp.Request)
}

// noCredential sends no Authorization header at all.
type noCredential struct{}

func (noCredential) apply(*nethttp.Request) {}

// basicCredential sends a Basic credential.
type basicCredential struct {
	user     string
	password string
}

func (b basicCredential) apply(r *nethttp.Request) { r.SetBasicAuth(b.user, b.password) }

// bearerCredential sends a bearer token.
type bearerCredential string

func (b bearerCredential) apply(r *nethttp.Request) {
	r.Header.Set("Authorization", "Bearer "+string(b))
}

// basic is a CRM user's Basic credential, by email. The gateway hands Basic to
// the operators' table, so the only route a CRM user's Basic credential reaches
// is the login, which reads the header itself.
func basic(u userRef) credential { return basicCredential{user: u.Email, password: u.Password} }

// sessionCredential is a CRM user signed in: the first request it authenticates
// logs the user in on the replica it is sent to and sends the bearer token the
// login issued, and every later request to that replica sends the same one. A
// CRM user's requests are authenticated by a token, since a Basic credential
// reaches no backend of the users'.
type sessionCredential struct{ user userRef }

// apply is never reached: the replica swaps this credential for the bearer token
// before it builds the request, because the swap needs the replica's address.
func (sessionCredential) apply(*nethttp.Request) {
	panic("crm-host: a session credential is resolved by the replica it is sent to")
}

// signedIn is a CRM user's credential: the bearer token of a login the replica
// makes for the user when the request is sent.
func signedIn(u userRef) credential { return sessionCredential{user: u} }

// operator is the platform operator's Basic credential.
func operator() credential { return basicCredential{user: operatorSubject, password: operatorPassword} }

// bearer is a session token.
func bearer(token string) credential { return bearerCredential(token) }

// request is one request a case makes: where it goes, which tenant it names,
// what it authenticates with and what it carries.
type request struct {
	method   string
	endpoint string
	path     string
	tenant   string // an alias; empty sends no tenant header
	cred     credential
	body     any // encoded as JSON; nil sends no body
}

// response is what came back.
type response struct {
	status int
	header nethttp.Header
	body   []byte
}

// decode unmarshals the body, failing the case when it cannot.
func (r response) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decoding the %d response %q: %v", r.status, r.body, err)
	}
}

// client is the one the cases share. The timeout bounds a request a host never
// answers, so the case fails with a message rather than hanging.
var client = &nethttp.Client{Timeout: 30 * time.Second}

// send makes one request to a replica, failing the case when it cannot be
// made.
func (h *crmHost) send(t *testing.T, req request) response {
	t.Helper()
	resp, err := h.do(req)
	if err != nil {
		t.Fatalf("%s %s on %s: %v", req.method, req.path, req.endpoint, err)
	}
	return resp
}

// do makes one request to a replica and reports a request that could not be
// made rather than failing the case, so it can run off the case's goroutine:
// a case sending requests at once collects the errors and fails on its own.
func (h *crmHost) do(req request) (response, error) {
	var body io.Reader
	if req.body != nil {
		encoded, err := json.Marshal(req.body)
		if err != nil {
			return response{}, fmt.Errorf("encoding the request body: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	r, err := nethttp.NewRequest(req.method, h.url(req.endpoint)+req.path, body)
	if err != nil {
		return response{}, fmt.Errorf("building the request: %w", err)
	}
	if req.body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if req.tenant != "" {
		r.Header.Set(tenantHeader, req.tenant)
	}
	if req.cred != nil {
		if authErr := h.authorize(req.cred, r); authErr != nil {
			return response{}, authErr
		}
	}
	resp, err := client.Do(r)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	read, err := io.ReadAll(resp.Body)
	if err != nil {
		return response{}, fmt.Errorf("reading the response: %w", err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: read}, nil
}

// authorize puts a credential on a request, signing a signed-in user in first
// when the replica holds no token for the user yet.
func (h *crmHost) authorize(cred credential, r *nethttp.Request) error {
	resolved, err := h.resolve(cred)
	if err != nil {
		return err
	}
	resolved.apply(r)
	return nil
}

// resolve turns a signed-in user's credential into the bearer token the
// replica issued for the user, logging the user in the first time and keeping
// the token for the rest of the replica's life.
func (h *crmHost) resolve(cred credential) (credential, error) {
	session, ok := cred.(sessionCredential)
	if !ok {
		return cred, nil
	}
	h.tokensMu.Lock()
	defer h.tokensMu.Unlock()
	if token, held := h.tokens[session.user.Email]; held {
		return bearer(token), nil
	}
	resp, err := h.do(request{
		method: nethttp.MethodPost, endpoint: accountEndpoint, path: "/login", cred: basic(session.user),
	})
	if err != nil {
		return nil, fmt.Errorf("signing %s in: %w", session.user.Email, err)
	}
	var got issued
	if resp.status != nethttp.StatusCreated || json.Unmarshal(resp.body, &got) != nil || got.Token == "" {
		return nil, fmt.Errorf("signing %s in answered %d with %q, want a token", session.user.Email, resp.status, resp.body)
	}
	if h.tokens == nil {
		h.tokens = map[string]string{}
	}
	h.tokens[session.user.Email] = got.Token
	return bearer(got.Token), nil
}

// get makes one GET request.
func (h *crmHost) get(t *testing.T, endpoint, path, tenantAlias string, cred credential) response {
	t.Helper()
	return h.send(t, request{method: nethttp.MethodGet, endpoint: endpoint, path: path, tenant: tenantAlias, cred: cred})
}

// post makes one POST request.
func (h *crmHost) post(t *testing.T, endpoint, path, tenantAlias string, cred credential, body any) response {
	t.Helper()
	return h.send(t, request{
		method: nethttp.MethodPost, endpoint: endpoint, path: path, tenant: tenantAlias, cred: cred, body: body,
	})
}

// expect fails the case unless a response carries the status wanted.
func expect(t *testing.T, what string, got response, status int) {
	t.Helper()
	if got.status != status {
		t.Fatalf("%s answered %d, want %d; body %q", what, got.status, status, got.body)
	}
}

// problemMediaType is the media type of the error responses the HTTP module
// writes, its own and the ones its layers write through it: the refusal of a
// request that matches no route, of one whose method the matched path does not
// take, and of one the gateway, the resolution or the limiter turns away. The
// error body of a handler of this host is the host's own, JSON with an error
// member.
const problemMediaType = "application/problem+json"

// requireProblem fails the case unless an error response the HTTP module or one
// of its layers wrote carries the problem details media type.
func requireProblem(t *testing.T, what string, got response) {
	t.Helper()
	if mediaType := got.header.Get("Content-Type"); mediaType != problemMediaType {
		t.Errorf("%s was answered with the media type %q, want %s; body %q", what, mediaType, problemMediaType, got.body)
	}
}

// login signs a user in on the account endpoint and returns the bearer token.
func (h *crmHost) login(t *testing.T, u userRef) string {
	t.Helper()
	resp := h.post(t, accountEndpoint, "/login", "", basic(u), nil)
	expect(t, "a login with the user's Basic credential", resp, nethttp.StatusCreated)
	var got issued
	resp.decode(t, &got)
	if got.Token == "" || got.UserID != u.ID {
		t.Fatalf("the login answered %+v, want a token for %s", got, u.ID)
	}
	return got.Token
}

// adminCreateTenant registers an active tenant through the admin endpoint, as
// the platform operator. It is the admin-endpoint twin of crmCase.createTenant.
//
// The request bodies below are written with the field names a client writes,
// rather than encoded from the host's own request types: a field the host
// renamed would otherwise be renamed here as well, and the case would go on
// passing against an API no client can call.
func (h *crmHost) adminCreateTenant(t *testing.T, ref tenantRef) {
	t.Helper()
	resp := h.post(t, adminEndpoint, "/tenants", "", operator(), map[string]string{"id": ref.ID, "alias": ref.Alias})
	expect(t, "the operator registering "+ref.ID, resp, nethttp.StatusCreated)
	var got tenantView
	resp.decode(t, &got)
	if got.ID != ref.ID || got.Alias != ref.Alias || got.Status != "active" {
		t.Fatalf("the admin endpoint registered %+v, want %+v active", got, ref)
	}
}

// adminCreateUser creates a CRM user through the admin endpoint, as the
// platform operator, and returns it with its identifier filled in.
func (h *crmHost) adminCreateUser(t *testing.T, u userRef) userRef {
	t.Helper()
	name, _, _ := strings.Cut(u.Email, "@")
	resp := h.post(t, adminEndpoint, "/users", "", operator(),
		map[string]string{"email": u.Email, "display_name": name, "password": u.Password})
	expect(t, "the operator creating "+u.Email, resp, nethttp.StatusCreated)
	var got userView
	resp.decode(t, &got)
	if got.ID == "" || got.Email != u.Email {
		t.Fatalf("the admin endpoint created %+v for %s", got, u.Email)
	}
	u.ID = got.ID
	return u
}

// adminGrant gives a user a role in a tenant through the admin endpoint, as the
// platform operator.
func (h *crmHost) adminGrant(t *testing.T, ref tenantRef, u userRef, role string) {
	t.Helper()
	resp := h.post(t, adminEndpoint, "/tenants/"+ref.ID+"/members", "", operator(),
		map[string]string{"email": u.Email, "role": role})
	expect(t, "the operator granting "+u.Email+" a role in "+ref.ID, resp, nethttp.StatusOK)
	var got grantView
	resp.decode(t, &got)
	if got.TenantID != ref.ID || got.UserID != u.ID || got.Role != role {
		t.Fatalf("the admin endpoint granted %+v, want %s in %s as %s", got, u.ID, ref.ID, role)
	}
}

// members lists a tenant's members as a caller.
func (h *crmHost) members(t *testing.T, tenantAlias string, cred credential) memberList {
	t.Helper()
	resp := h.get(t, apiEndpoint, "/members", tenantAlias, cred)
	expect(t, "GET /members naming "+tenantAlias, resp, nethttp.StatusOK)
	var got memberList
	resp.decode(t, &got)
	return got
}

// me asks the account endpoint who the caller is.
func (h *crmHost) me(t *testing.T, cred credential) meView {
	t.Helper()
	resp := h.get(t, accountEndpoint, "/me", "", cred)
	expect(t, "GET /me", resp, nethttp.StatusOK)
	var got meView
	resp.decode(t, &got)
	return got
}
