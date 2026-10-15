package oidc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/egress"
	"github.com/vislake/speed/pkg/authn/internal/step"
	"github.com/vislake/speed/pkg/observability"
)

// The client credentials every case configures a provider with.
const (
	testClientID     = "oidc-client"
	testClientSecret = "oidc-secret"
)

// mockIDP is a mock OpenID Connect provider served over TLS on loopback: a
// discovery document, an authorization endpoint that insists on PKCE, a token
// endpoint that checks the verifier and issues a signed identity token, and a
// key set. A case scripts the token it issues by setting the fields below before
// it calls Answer.
type mockIDP struct {
	t      *testing.T
	server *httptest.Server
	key    *ecdsa.PrivateKey
	kid    string

	mu    sync.Mutex
	codes map[string]authRequest
	// discoveryCount is how many discovery documents the mock has served, so a
	// case can tell a cache hit from a cache miss.
	discoveryCount int

	// override replaces or, with a nil value, removes a claim of the identity
	// token. dropNonce removes the nonce claim. signRaw replaces the signing
	// entirely, which the algorithm-confusion cases use. tokenStatus, when
	// non-zero, makes the token endpoint answer that status. omitIDToken leaves
	// the identity token out of the token response. discoveryStatus, when
	// non-zero, makes the discovery document unreachable. authorizeStatus, when
	// non-zero, makes the authorization endpoint answer that status. jwksURI,
	// when set, replaces the key set's address in the discovery document.
	override        map[string]any
	dropNonce       bool
	signRaw         func(claims map[string]any) (string, error)
	tokenStatus     int
	omitIDToken     bool
	omitIss         bool
	discoveryStatus int
	authorizeStatus int
	jwksURI         string
	// userinfoCount is how many userinfo requests the mock has served, so a case
	// can hold that the method never calls it.
	userinfoCount int
}

// authRequest is what the authorization endpoint records per code so the token
// endpoint can check the verifier and echo the nonce.
type authRequest struct {
	challenge string
	nonce     string
}

func newMockIDP(t *testing.T) *mockIDP {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	d := &mockIDP{t: t, key: key, kid: "test-key", codes: map[string]authRequest{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", d.discovery)
	mux.HandleFunc("/authorize", d.authorize)
	mux.HandleFunc("/token", d.token)
	mux.HandleFunc("/jwks", d.jwks)
	mux.HandleFunc("/userinfo", d.userinfo)
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	d.server = srv
	return d
}

func (d *mockIDP) issuer() string { return d.server.URL }

// cert returns the server's certificate, for a root pool.
func (d *mockIDP) cert() *x509.Certificate { return d.server.Certificate() }

func (d *mockIDP) discoveries() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.discoveryCount
}

func (d *mockIDP) discovery(w http.ResponseWriter, _ *http.Request) {
	d.mu.Lock()
	d.discoveryCount++
	d.mu.Unlock()
	if d.discoveryStatus != 0 {
		w.WriteHeader(d.discoveryStatus)
		return
	}
	jwks := d.jwksURI
	if jwks == "" {
		jwks = d.server.URL + "/jwks"
	}
	writeJSON(d.t, w, map[string]any{
		"issuer":                                d.server.URL,
		"authorization_endpoint":                d.server.URL + "/authorize",
		"token_endpoint":                        d.server.URL + "/token",
		"jwks_uri":                              jwks,
		"id_token_signing_alg_values_supported": []string{"ES256", "RS256"},
	})
}

// authorize insists on a PKCE challenge with the S256 method, so a client that
// does not send one is refused.
func (d *mockIDP) authorize(w http.ResponseWriter, r *http.Request) {
	if d.authorizeStatus != 0 {
		w.WriteHeader(d.authorizeStatus)
		return
	}
	q := r.URL.Query()
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		http.Error(w, "a code_challenge with S256 is required", http.StatusBadRequest)
		return
	}
	code := fmt.Sprintf("code-%d", time.Now().UnixNano())
	d.mu.Lock()
	d.codes[code] = authRequest{challenge: q.Get("code_challenge"), nonce: q.Get("nonce")}
	d.mu.Unlock()

	back := q.Get("redirect_uri")
	params := url.Values{"code": {code}, "state": {q.Get("state")}}
	if !d.omitIss {
		params.Set("iss", d.server.URL)
	}
	loc := back + "?" + params.Encode()
	http.Redirect(w, r, loc, http.StatusFound)
}

// userinfo counts a request to the userinfo endpoint. The method never calls
// it, so a case asserts the count stays zero.
func (d *mockIDP) userinfo(w http.ResponseWriter, _ *http.Request) {
	d.mu.Lock()
	d.userinfoCount++
	d.mu.Unlock()
	writeJSON(d.t, w, map[string]any{"sub": "subject-1"})
}

func (d *mockIDP) userinfoRequests() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.userinfoCount
}

func (d *mockIDP) token(w http.ResponseWriter, r *http.Request) {
	if d.tokenStatus != 0 {
		w.WriteHeader(d.tokenStatus)
		writeJSON(d.t, w, map[string]any{"error": "invalid_grant"})
		return
	}
	_ = r.ParseForm()
	code := r.FormValue("code")
	d.mu.Lock()
	rec, ok := d.codes[code]
	d.mu.Unlock()
	if !ok || s256(r.FormValue("code_verifier")) != rec.challenge {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(d.t, w, map[string]any{"error": "invalid_grant"})
		return
	}
	body := map[string]any{"access_token": "access-token", "token_type": "Bearer"}
	if !d.omitIDToken {
		raw, err := d.sign(d.claims(rec.nonce))
		if err != nil {
			d.t.Errorf("signing the identity token: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		body["id_token"] = raw
	}
	writeJSON(d.t, w, body)
}

func (d *mockIDP) jwks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(d.t, w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
		{Key: d.key.Public(), KeyID: d.kid, Algorithm: "ES256", Use: "sig"},
	}})
}

// claims is the identity token's claim set: a valid provider identity that a
// case then adjusts through override and dropNonce.
func (d *mockIDP) claims(nonce string) map[string]any {
	now := time.Now()
	c := map[string]any{
		"iss":            d.server.URL,
		"sub":            "subject-1",
		"aud":            []string{testClientID},
		"exp":            now.Add(time.Hour).Unix(),
		"iat":            now.Unix(),
		"email":          "user@example.com",
		"email_verified": true,
		"name":           "Test User",
	}
	if !d.dropNonce {
		c["nonce"] = nonce
	}
	for k, v := range d.override {
		if v == nil {
			delete(c, k)
			continue
		}
		c[k] = v
	}
	return c
}

func (d *mockIDP) sign(claims map[string]any) (string, error) {
	if d.signRaw != nil {
		return d.signRaw(claims)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.ES256, Key: jose.JSONWebKey{Key: d.key, KeyID: d.kid, Algorithm: "ES256"}},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		return "", err
	}
	obj, err := signer.Sign(payload)
	if err != nil {
		return "", err
	}
	return obj.CompactSerialize()
}

// browser is a client that trusts the mock's certificate and does not follow
// redirects, so a case can read the authorization endpoint's Location header.
func (d *mockIDP) browser() *http.Client {
	return &http.Client{
		Transport: d.server.Client().Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// fakeProviders is a stand-in for the enterprise provider register: it hands out
// the rows and secrets a case put in, and a fixed failure when one is set. Only
// Get and Secret are used by this module.
type fakeProviders struct {
	mu        sync.Mutex
	rows      map[string]authn.SSOProvider
	secrets   map[string]string
	secretErr error
}

func newFakeProviders() *fakeProviders {
	return &fakeProviders{rows: map[string]authn.SSOProvider{}, secrets: map[string]string{}}
}

func (f *fakeProviders) put(p authn.SSOProvider, secret string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[p.ID] = p
	f.secrets[p.ID] = secret
}

func (f *fakeProviders) Get(_ context.Context, id string) (authn.SSOProvider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[id]
	if !ok {
		return authn.SSOProvider{}, fmt.Errorf("%w: no such provider", authn.ErrAccountNotFound)
	}
	return row, nil
}

func (f *fakeProviders) Secret(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.secretErr != nil {
		return "", f.secretErr
	}
	return f.secrets[id], nil
}

func (f *fakeProviders) List(context.Context, string) ([]authn.SSOProvider, error) { return nil, nil }
func (f *fakeProviders) Tenant(context.Context, string) (string, error)            { return "", nil }
func (f *fakeProviders) Discover(context.Context, string) ([]authn.SSOProvider, error) {
	return nil, nil
}

func (f *fakeProviders) Put(context.Context, authn.SSOProvider, *string) (authn.SSOProvider, error) {
	return authn.SSOProvider{}, nil
}
func (f *fakeProviders) Delete(context.Context, string) error { return nil }
func (f *fakeProviders) ClaimDomain(context.Context, string, string) (string, error) {
	return "", nil
}
func (f *fakeProviders) VerifyDomain(context.Context, string, string) error { return nil }
func (f *fakeProviders) RemoveDomain(context.Context, string, string) error { return nil }
func (f *fakeProviders) ReverifyDomains(context.Context, string) error      { return nil }

// recordingAudit records the entries a case hands it.
type recordingAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (a *recordingAudit) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

func (a *recordingAudit) all() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.entries...)
}

// rig is the method under test wired to one or more mock providers through the
// same restricted egress client the product builds, with loopback allowed so the
// mocks are reachable.
type rig struct {
	t         *testing.T
	p         *product
	providers *fakeProviders
	metrics   *sdkmetric.ManualReader
	audit     *recordingAudit
}

// newRig wires a product to the given mocks. Every mock's certificate is trusted
// and the registry hands out the rows a case puts in with putProvider.
func newRig(t *testing.T, mocks ...*mockIDP) *rig {
	t.Helper()
	pool := x509.NewCertPool()
	for _, m := range mocks {
		pool.AddCert(m.cert())
	}
	client := egress.New(egress.Options{AllowLoopback: true, RootCAs: pool})
	// The product wraps its client's transport from outside so an outbound call
	// carries the span of the context it was made for; the rig builds the client
	// the same way, so a case can hold the trace across the hop.
	client.Transport = observability.Transport(client.Transport)
	providers := newFakeProviders()
	reader := sdkmetric.NewManualReader()
	settings := newReader(nil, slog.Default())
	p := &product{
		redirect: redirectBase{publicURL: "https://app.example.com", basePath: "/auth"},
		client:   client,
		cache:    newProviderCache(client, settings),
		sso:      providers,
		settings: settings,
		logger:   slog.Default(),
	}
	m, err := newMeters(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	if err != nil {
		t.Fatalf("building the meters: %v", err)
	}
	p.meters = m
	rec := &recordingAudit{}
	p.audit = rec
	return &rig{t: t, p: p, providers: providers, metrics: reader, audit: rec}
}

// putProvider registers an OIDC provider row pointing at a mock, with the given
// claims mapping. The mapping defaults to email and display name when none is
// given.
func (r *rig) putProvider(id, tenant string, m *mockIDP, claims map[string]string) authn.SSOProvider {
	r.t.Helper()
	if claims == nil {
		claims = map[string]string{"email": "email", "name": "display_name"}
	}
	raw, err := json.Marshal(map[string]any{
		"issuer":    m.issuer(),
		"client_id": testClientID,
		"claims":    claims,
	})
	if err != nil {
		r.t.Fatalf("marshalling a provider config: %v", err)
	}
	row := authn.SSOProvider{
		ID:           id,
		Tenant:       tenant,
		Protocol:     authn.SSOProtocolOIDC,
		Name:         "Test IdP",
		Enabled:      true,
		Provisioning: authn.ProvisioningJIT,
		Config:       raw,
		Version:      1,
	}
	r.providers.put(row, testClientSecret)
	return row
}

// begin runs Begin for a login on the named provider and returns the flow's
// private bytes and the authorization URL.
func (r *rig) begin(id string) (private []byte, authURL string) {
	r.t.Helper()
	got, err := r.p.Begin(context.Background(), authn.Start{
		Purpose: authn.PurposeLogin,
		Fields:  map[string]string{fieldProvider: id},
	})
	if err != nil {
		r.t.Fatalf("Begin: %v", err)
	}
	if step.KindOf(got) != step.KindRedirect {
		r.t.Fatalf("Begin answered %s, want a redirect", step.KindOf(got))
	}
	return step.PrivateOf(got), step.RedirectOf(got)
}

// callback drives the mock authorization endpoint with the authorization URL and
// returns the query the provider would send back.
func (r *rig) callback(m *mockIDP, authURL string) url.Values {
	r.t.Helper()
	resp, err := m.browser().Get(authURL)
	if err != nil {
		r.t.Fatalf("the authorization endpoint: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		r.t.Fatalf("the authorization endpoint answered %d, want a redirect", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		r.t.Fatalf("the redirect location: %v", err)
	}
	return loc.Query()
}

// start runs a whole Begin and authorization round and returns the flow's
// private bytes and the callback query.
func (r *rig) start(id string, m *mockIDP) (private []byte, query url.Values) {
	r.t.Helper()
	private, authURL := r.begin(id)
	return private, r.callback(m, authURL)
}

// answer drives the method's Answer at the redirect step.
func (r *rig) answer(private []byte, query url.Values) authn.Step {
	r.t.Helper()
	got, err := r.p.Answer(context.Background(), authn.FlowState{
		Purpose: authn.PurposeLogin,
		Step:    stepAuthorize,
		Private: private,
	}, authn.StepInput{Action: authn.InputCallback, Query: query})
	if err != nil {
		r.t.Fatalf("Answer: %v", err)
	}
	return got
}

// run is start followed by answer with nothing adjusted between them.
func (r *rig) run(id string, m *mockIDP) authn.Step {
	r.t.Helper()
	private, query := r.start(id, m)
	return r.answer(private, query)
}

// assertionCount is the value of the authn.sso.assertions counter for the
// outcome and protocol given.
func (r *rig) assertionCount(protocol, outcome string) int64 {
	r.t.Helper()
	return counterValue(r.t, r.metrics, assertionsName, protocolAttribute, protocol, outcomeAttribute, outcome)
}

// s256 is the PKCE S256 challenge of a verifier.
func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("writing a JSON response: %v", err)
	}
}
