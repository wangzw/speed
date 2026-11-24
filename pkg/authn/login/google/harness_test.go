package google

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
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/egress"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

const (
	testClientID     = "google-client"
	testClientSecret = "google-secret"
)

type idp struct {
	t      *testing.T
	server *httptest.Server
	pool   *x509.CertPool
	key    *ecdsa.PrivateKey
	kid    string

	mu    sync.Mutex
	codes map[string]authRequest

	discoveryCalls int
	tokenCalls     int

	override        map[string]any
	dropNonce       bool
	signRaw         func(claims map[string]any) (string, error)
	tokenStatus     int
	omitIDToken     bool
	discoveryStatus int
	authorizeStatus int
	jwksURI         string
}

type authRequest struct {
	challenge string
	nonce     string
}

func newIDP(t *testing.T) *idp {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	d := &idp{t: t, key: key, kid: "test-key", codes: map[string]authRequest{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", d.discovery)
	mux.HandleFunc("/authorize", d.authorize)
	mux.HandleFunc("/token", d.token)
	mux.HandleFunc("/jwks", d.jwks)
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	d.server = srv
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	d.pool = pool
	return d
}

func (d *idp) issuer() string { return d.server.URL }

func (d *idp) discovery(w http.ResponseWriter, _ *http.Request) {
	d.mu.Lock()
	d.discoveryCalls++
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

func (d *idp) authorize(w http.ResponseWriter, r *http.Request) {
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
	loc := back + "?" + url.Values{"code": {code}, "state": {q.Get("state")}, "iss": {d.server.URL}}.Encode()
	http.Redirect(w, r, loc, http.StatusFound)
}

func (d *idp) token(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	d.tokenCalls++
	d.mu.Unlock()
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

func (d *idp) jwks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(d.t, w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
		{Key: d.key.Public(), KeyID: d.kid, Algorithm: "ES256", Use: "sig"},
	}})
}

func (d *idp) claims(nonce string) map[string]any {
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

func (d *idp) sign(claims map[string]any) (string, error) {
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

func (d *idp) browser() *http.Client {
	return &http.Client{
		Transport: d.server.Client().Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (d *idp) discoveries() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.discoveryCalls
}

func (d *idp) tokens() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tokenCalls
}

type rig struct {
	t   *testing.T
	m   *product
	idp *idp
}

func newRig(t *testing.T, cfg resolved) *rig {
	t.Helper()
	d := newIDP(t)
	cfg.clientID = testClientID
	if cfg.clientSecret == "" {
		cfg.clientSecret = testClientSecret
	}
	if cfg.redirectURL == "" {
		cfg.redirectURL = d.server.URL + "/api/v1/auth/callback/google"
	}
	client := egress.New(egress.Options{AllowLoopback: true, RootCAs: d.pool})
	m := &product{
		config:   cfg,
		client:   client,
		provider: &providerCache{issuer: d.issuer(), client: client},
	}
	return &rig{t: t, m: m, idp: d}
}

func (r *rig) begin(purpose string) (private []byte, authURL string) {
	r.t.Helper()
	got, err := r.m.Begin(context.Background(), authn.Start{Purpose: purpose})
	if err != nil {
		r.t.Fatalf("Begin: %v", err)
	}
	if step.KindOf(got) != step.KindRedirect {
		r.t.Fatalf("Begin answered %s, want a redirect", step.KindOf(got))
	}
	return step.PrivateOf(got), step.RedirectOf(got)
}

func (r *rig) callback(authURL string) url.Values {
	r.t.Helper()
	resp, err := r.idp.browser().Get(authURL)
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

func (r *rig) start(purpose string) (private []byte, query url.Values) {
	r.t.Helper()
	private, authURL := r.begin(purpose)
	return private, r.callback(authURL)
}

func (r *rig) answer(private []byte, query url.Values, purpose string) authn.Step {
	r.t.Helper()
	got, err := r.m.Answer(context.Background(), authn.FlowState{
		Purpose: purpose,
		Step:    stepAuthorize,
		Private: private,
	}, authn.StepInput{Action: authn.InputCallback, Query: query})
	if err != nil {
		r.t.Fatalf("Answer: %v", err)
	}
	return got
}

func (r *rig) run(purpose string) authn.Step {
	r.t.Helper()
	private, query := r.start(purpose)
	return r.answer(private, query, purpose)
}

func loginStart() authn.Start { return authn.Start{Purpose: authn.PurposeLogin} }

func tamperedVerifier(t *testing.T, private []byte) []byte {
	t.Helper()
	st, ok := readState(private)
	if !ok {
		t.Fatal("the flow's private state is not the method's")
	}
	st.Verifier = "a-different-verifier-0123456789abcdefghij"
	return writeState(st.State, st.Verifier, st.Nonce)
}

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
