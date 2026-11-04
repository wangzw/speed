package turnstile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/egress"
	"github.com/vislake/speed/pkg/kv"
	speedlog "github.com/vislake/speed/pkg/log"
)

const (
	testSiteKey  = "1x00000000000000000000AA"
	testSecret   = "turnstile-secret-2f7a91c4"
	testHostname = "login.example.com"
	testAction   = "login-sms"
	testToken    = "a-turnstile-token"
	testRemoteIP = "203.0.113.7"
)

// answer is one response the mock siteverify serves.
type answer struct {
	status int
	body   string
}

// recorded is what the server saw of one call.
type recorded struct {
	method string
	form   url.Values
}

// siteverify serves the answers a case names, one per call in order; the last
// answer repeats once the list is exhausted. It serves over TLS because the
// provider reaches it through the restricted egress client, which admits
// loopback only when told to and trusts the server's own certificate.
func siteverify(t *testing.T, answers ...answer) (*httptest.Server, *x509.CertPool, func() []recorded) {
	t.Helper()
	if len(answers) == 0 {
		t.Fatal("a case has to name at least one answer")
	}
	var mu sync.Mutex
	var calls []recorded
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		index := len(calls)
		seen := recorded{method: r.Method, form: r.URL.Query()}
		if err := r.ParseForm(); err == nil {
			seen.form = r.PostForm
		}
		calls = append(calls, seen)
		mu.Unlock()
		if index >= len(answers) {
			index = len(answers) - 1
		}
		w.WriteHeader(answers[index].status)
		_, _ = io.WriteString(w, answers[index].body)
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return srv, pool, func() []recorded {
		mu.Lock()
		defer mu.Unlock()
		return append([]recorded(nil), calls...)
	}
}

// providerFor builds a provider pointed at the mock server through the
// restricted client, with the case's own changes applied and a fresh claim
// store.
func providerFor(t *testing.T, srv *httptest.Server, pool *x509.CertPool, change func(*Options)) *Provider {
	t.Helper()
	return providerWithStore(t, srv, pool, newClaimStore(), change)
}

// providerWithStore is providerFor over a store the case keeps, so a case can
// inspect the claim or arm a fault.
func providerWithStore(t *testing.T, srv *httptest.Server, pool *x509.CertPool,
	store kv.KV, change func(*Options),
) *Provider {
	t.Helper()
	o := Options{
		SiteKey:   testSiteKey,
		Secret:    testSecret,
		VerifyURL: srv.URL,
		Hostname:  testHostname,
		Action:    testAction,
		Timeout:   2 * time.Second,
		HTTP:      egress.New(egress.Options{AllowLoopback: true, RootCAs: pool}),
		Store:     store,
	}
	if change != nil {
		change(&o)
	}
	p, err := New(o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

// claimStore is an in-memory kv.KV for the claim cases: it records each claim's
// value and expiry and can be armed to fail the set-if-absent.
type claimStore struct {
	mu     sync.Mutex
	values map[string][]byte
	ttls   map[string]time.Duration
	setErr error
}

func newClaimStore() *claimStore {
	return &claimStore{values: map[string][]byte{}, ttls: map[string]time.Duration{}}
}

var _ kv.KV = (*claimStore)(nil)

func (s *claimStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, present := s.values[key]
	return append([]byte(nil), value...), present, nil
}

func (s *claimStore) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = append([]byte(nil), value...)
	s.ttls[key] = ttl
	return nil
}

func (s *claimStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	delete(s.ttls, key)
	return nil
}

func (s *claimStore) IncrByFloat(context.Context, string, float64) (float64, error) {
	return 0, errors.New("not used")
}

func (s *claimStore) IncrByFloatWithTTL(context.Context, string, float64, time.Duration) (float64, error) {
	return 0, errors.New("not used")
}

func (s *claimStore) CompareAndSwap(context.Context, string, []byte, []byte) (bool, error) {
	return false, errors.New("not used")
}

func (s *claimStore) SetIfAbsent(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setErr != nil {
		return false, s.setErr
	}
	if _, present := s.values[key]; present {
		return false, nil
	}
	s.values[key] = append([]byte(nil), value...)
	s.ttls[key] = ttl
	return true, nil
}

// keys lists the claims the store holds.
func (s *claimStore) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.values))
	for key := range s.values {
		out = append(out, key)
	}
	return out
}

// accepted is a siteverify answer accepting a proof from the expected hostname
// and action.
const accepted = `{"success":true,"hostname":"` + testHostname + `","action":"` + testAction + `","challenge_ts":"2026-10-25T10:00:00Z"}`

// A proof the provider accepts passes, and the call carries the secret, the
// token and the source address in its body.
func TestAValidProofPasses(t *testing.T) {
	srv, pool, calls := siteverify(t, answer{http.StatusOK, accepted})
	p := providerFor(t, srv, pool, nil)

	err := p.Verify(t.Context(), authn.HumanProof{Token: testToken, RemoteIP: testRemoteIP}, authn.HumanScopeOther)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	seen := calls()
	if len(seen) != 1 {
		t.Fatalf("the provider made %d calls, want one", len(seen))
	}
	if seen[0].method != http.MethodPost {
		t.Errorf("method = %q, want POST", seen[0].method)
	}
	want := map[string]string{"secret": testSecret, "response": testToken, "remoteip": testRemoteIP}
	for key, value := range want {
		if got := seen[0].form.Get(key); got != value {
			t.Errorf("form %q = %q, want %q", key, got, value)
		}
	}
}

// A false success is an invalid proof, which the surface answers with 403 and a
// human-verification code. The provider's error codes reach the log at Warn and
// never the error the client sees.
func TestAFalseSuccessIsRefusedAsAMissingProof(t *testing.T) {
	const code = "invalid-input-response"
	srv, pool, _ := siteverify(t, answer{http.StatusOK, `{"success":false,"error-codes":["` + code + `"]}`})
	p := providerFor(t, srv, pool, nil)

	var logged bytes.Buffer
	ctx := speedlog.WithLogger(t.Context(),
		slog.New(slog.NewJSONHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})))

	err := p.Verify(ctx, authn.HumanProof{Token: testToken}, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Fatalf("a false success resolved with %v, want ErrHumanVerificationRequired", err)
	}
	if errors.Is(err, authn.ErrUnavailable) {
		t.Errorf("a false success reads as an outage: %v", err)
	}
	if strings.Contains(err.Error(), code) {
		t.Errorf("the client-facing error carries the provider's error code: %v", err)
	}
	if !strings.Contains(logged.String(), code) {
		t.Errorf("the provider's error code did not reach the log at all: %q", logged.String())
	}
	if !strings.Contains(logged.String(), "WARN") {
		t.Errorf("the provider's error code was not logged at Warn: %q", logged.String())
	}
}

// A refusal the provider states about the proof itself -- missing, invalid,
// expired or already spent -- is an invalid proof, whatever code of that class
// it names.
func TestProofRefusalsAreHumanVerificationRequired(t *testing.T) {
	for _, code := range []string{"missing-input-response", "invalid-input-response", "timeout-or-duplicate"} {
		t.Run(code, func(t *testing.T) {
			srv, pool, _ := siteverify(t, answer{http.StatusOK, `{"success":false,"error-codes":["` + code + `"]}`})
			p := providerFor(t, srv, pool, nil)

			err := p.Verify(t.Context(), authn.HumanProof{Token: testToken}, authn.HumanScopeOther)
			if !errors.Is(err, authn.ErrHumanVerificationRequired) {
				t.Fatalf("%s resolved with %v, want ErrHumanVerificationRequired", code, err)
			}
			if errors.Is(err, authn.ErrUnavailable) {
				t.Errorf("%s reads as an outage: %v", code, err)
			}
		})
	}
}

// A refusal the provider states about its own side -- an internal error, a
// misconfigured secret or widget, or a code this package does not know -- is an
// outage: the proof was not judged, so nothing is sent. It must not read as the
// caller's proof being invalid, and the error the client sees carries neither
// the token nor the secret.
func TestProviderFaultsAreUnavailable(t *testing.T) {
	codes := []string{
		"internal-error",
		"missing-input-secret",
		"invalid-input-secret",
		"invalid-widget-id",
		"a-code-this-package-does-not-know",
	}
	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			srv, pool, _ := siteverify(t, answer{http.StatusOK, `{"success":false,"error-codes":["` + code + `"]}`})
			p := providerFor(t, srv, pool, nil)
			var logged bytes.Buffer
			ctx := speedlog.WithLogger(t.Context(),
				slog.New(slog.NewJSONHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})))

			err := p.Verify(ctx, authn.HumanProof{Token: testToken, RemoteIP: testRemoteIP}, authn.HumanScopeOther)
			if !errors.Is(err, authn.ErrUnavailable) {
				t.Fatalf("%s resolved with %v, want ErrUnavailable", code, err)
			}
			if errors.Is(err, authn.ErrHumanVerificationRequired) {
				t.Errorf("%s reads as an invalid proof: %v", code, err)
			}
			if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), testSecret) {
				t.Errorf("the client-facing error carries the token or secret: %v", err)
			}
			if !strings.Contains(logged.String(), code) {
				t.Errorf("the provider's error code did not reach the log: %q", logged.String())
			}
		})
	}
}

// An answer that accepted the proof for another hostname is not trusted: the
// provider fails closed with the outage answer.
func TestAHostnameMismatchIsUnavailable(t *testing.T) {
	srv, pool, _ := siteverify(t, answer{
		http.StatusOK,
		`{"success":true,"hostname":"evil.example.com","action":"` + testAction + `"}`,
	})
	p := providerFor(t, srv, pool, nil)

	err := p.Verify(t.Context(), authn.HumanProof{Token: testToken}, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("a hostname mismatch resolved with %v, want ErrUnavailable", err)
	}
	if errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Errorf("a hostname mismatch reads as an invalid proof: %v", err)
	}
}

// An answer that accepted the proof for another action is not trusted either.
func TestAnActionMismatchIsUnavailable(t *testing.T) {
	srv, pool, _ := siteverify(t, answer{
		http.StatusOK,
		`{"success":true,"hostname":"` + testHostname + `","action":"another-action"}`,
	})
	p := providerFor(t, srv, pool, nil)

	err := p.Verify(t.Context(), authn.HumanProof{Token: testToken}, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("an action mismatch resolved with %v, want ErrUnavailable", err)
	}
	if errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Errorf("an action mismatch reads as an invalid proof: %v", err)
	}
}

// The action is required: a provider built without one cannot compare the
// answer's action, so New refuses it rather than accepting a token obtained
// under any action.
func TestAnEmptyActionIsRefused(t *testing.T) {
	srv, pool, _ := siteverify(t, answer{http.StatusOK, accepted})

	_, err := New(Options{
		SiteKey:   testSiteKey,
		Secret:    testSecret,
		VerifyURL: srv.URL,
		Hostname:  testHostname,
		Action:    "",
		Timeout:   2 * time.Second,
		HTTP:      egress.New(egress.Options{AllowLoopback: true, RootCAs: pool}),
	})
	if err == nil {
		t.Fatal("New with no action resolved; a provider without one cannot check the answer's action")
	}
	if !strings.Contains(err.Error(), "action") {
		t.Errorf("the failure %q does not name the missing action", err)
	}
}

// A non-2xx answer is an outage: the provider could not be reached for a
// verdict, so the request is refused without sending.
func TestANonSuccessStatusIsUnavailable(t *testing.T) {
	srv, pool, _ := siteverify(t, answer{http.StatusInternalServerError, `{"success":true}`})
	p := providerFor(t, srv, pool, nil)

	err := p.Verify(t.Context(), authn.HumanProof{Token: testToken}, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("a 500 resolved with %v, want ErrUnavailable", err)
	}
	if errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Errorf("a 500 reads as an invalid proof: %v", err)
	}
}

// A body that is not the provider's response is an outage, not a refusal: the
// answer cannot be judged at all.
func TestABrokenBodyIsUnavailable(t *testing.T) {
	srv, pool, _ := siteverify(t, answer{http.StatusOK, `{"success":`})
	p := providerFor(t, srv, pool, nil)

	err := p.Verify(t.Context(), authn.HumanProof{Token: testToken}, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("a broken body resolved with %v, want ErrUnavailable", err)
	}
}

// A token is single-use. The provider treats the duplicate code as an invalid
// proof, so a token accepted once is refused when it is presented again.
func TestAReusedTokenIsRefused(t *testing.T) {
	srv, pool, _ := siteverify(t,
		answer{http.StatusOK, accepted},
		answer{http.StatusOK, `{"success":false,"error-codes":["timeout-or-duplicate"]}`},
	)
	p := providerFor(t, srv, pool, nil)

	if err := p.Verify(t.Context(), authn.HumanProof{Token: testToken}, authn.HumanScopeOther); err != nil {
		t.Fatalf("first Verify: %v", err)
	}
	err := p.Verify(t.Context(), authn.HumanProof{Token: testToken}, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Fatalf("a reused token resolved with %v, want ErrHumanVerificationRequired", err)
	}
	if errors.Is(err, authn.ErrUnavailable) {
		t.Errorf("a reused token reads as an outage: %v", err)
	}
}

// An empty proof is a caller that presented nothing and is refused before any
// call is made.
func TestAnEmptyProofIsRefused(t *testing.T) {
	srv, pool, calls := siteverify(t, answer{http.StatusOK, accepted})
	p := providerFor(t, srv, pool, nil)

	err := p.Verify(t.Context(), authn.HumanProof{}, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Fatalf("an empty proof resolved with %v, want ErrHumanVerificationRequired", err)
	}
	if len(calls()) != 0 {
		t.Errorf("an empty proof still reached the provider")
	}
}

// An over-long proof is a malformed request (400), refused before any call.
func TestAnOverlongProofIsHumanProofTooLong(t *testing.T) {
	srv, pool, calls := siteverify(t, answer{http.StatusOK, accepted})
	p := providerFor(t, srv, pool, nil)

	err := p.Verify(t.Context(), authn.HumanProof{Token: strings.Repeat("x", maxTokenLen+1)}, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrHumanProofTooLong) {
		t.Fatalf("an over-long proof resolved with %v, want ErrHumanProofTooLong", err)
	}
	if len(calls()) != 0 {
		t.Errorf("an over-long proof still reached the provider")
	}
}

// The provider reports the seam's answers about the deployment.
func TestTheProviderReportsItsNameSiteKeyAndRequirement(t *testing.T) {
	srv, pool, _ := siteverify(t, answer{http.StatusOK, accepted})
	p := providerFor(t, srv, pool, nil)

	if p.Provider(authn.HumanScopeOther) != ProviderName {
		t.Errorf("Provider = %q, want %q", p.Provider(authn.HumanScopeOther), ProviderName)
	}
	if !p.Required(authn.HumanScopeOther) {
		t.Error("Required = false, want true")
	}
	if p.SiteKey(authn.HumanScopeOther) != testSiteKey {
		t.Errorf("SiteKey = %q, want %q", p.SiteKey(authn.HumanScopeOther), testSiteKey)
	}
}

// A provider that cannot be reached is an outage, not a refusal.
func TestAnUnreachableProviderIsUnavailable(t *testing.T) {
	srv, pool, _ := siteverify(t, answer{http.StatusOK, accepted})
	srv.Close()
	p := providerFor(t, srv, pool, nil)

	err := p.Verify(context.Background(), authn.HumanProof{Token: testToken}, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("an unreachable provider resolved with %v, want ErrUnavailable", err)
	}
}

// A token is refused locally on its second presentation even when siteverify
// answers success both times. This is what the local claim buys: single use
// does not depend on the upstream refusing the duplicate. The second call is
// made against the upstream -- its answer is success -- and the refusal is the
// local claim's.
func TestAReplayedTokenIsRefusedEvenWhenTheUpstreamAcceptsIt(t *testing.T) {
	srv, pool, calls := siteverify(t, answer{http.StatusOK, accepted})
	p := providerFor(t, srv, pool, nil)

	if err := p.Verify(t.Context(), authn.HumanProof{Token: testToken}, authn.HumanScopeOther); err != nil {
		t.Fatalf("first Verify: %v", err)
	}
	err := p.Verify(t.Context(), authn.HumanProof{Token: testToken}, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Fatalf("a replayed token resolved with %v, want ErrHumanVerificationRequired", err)
	}
	if errors.Is(err, authn.ErrUnavailable) {
		t.Errorf("a replayed token reads as an outage: %v", err)
	}
	if got := len(calls()); got != 2 {
		t.Errorf("siteverify judged %d presentations, want 2: the replay has to reach the local guard", got)
	}
}

// Two concurrent presentations of one token are resolved by the store's
// set-if-absent: exactly one is accepted, whatever the upstream answers.
func TestConcurrentClaimsAdmitExactlyOne(t *testing.T) {
	srv, pool, _ := siteverify(t, answer{http.StatusOK, accepted})
	p := providerFor(t, srv, pool, nil)

	const racers = 8
	errs := make([]error, racers)
	var wait sync.WaitGroup
	ctx := t.Context()
	for i := range errs {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			errs[i] = p.Verify(ctx, authn.HumanProof{Token: testToken}, authn.HumanScopeOther)
		}(i)
	}
	wait.Wait()

	accepted := 0
	for i, err := range errs {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, authn.ErrHumanVerificationRequired):
		default:
			t.Errorf("racer %d resolved with %v, want acceptance or ErrHumanVerificationRequired", i, err)
		}
	}
	if accepted != 1 {
		t.Errorf("%d racers accepted the same token, want exactly 1", accepted)
	}
}

// The store is a requirement: a provider built without one has only the
// upstream's duplicate refusal, not this deployment's single-use guarantee.
func TestAnEmptyStoreIsRefused(t *testing.T) {
	srv, pool, _ := siteverify(t, answer{http.StatusOK, accepted})
	_, err := New(Options{
		SiteKey:   testSiteKey,
		Secret:    testSecret,
		VerifyURL: srv.URL,
		Hostname:  testHostname,
		Action:    testAction,
		Timeout:   2 * time.Second,
		HTTP:      egress.New(egress.Options{AllowLoopback: true, RootCAs: pool}),
	})
	if err == nil {
		t.Fatal("New with no store resolved; a provider without one cannot record a spent token")
	}
	if !strings.Contains(err.Error(), "store") {
		t.Errorf("the failure %q does not name the missing store", err)
	}
}

// A store that cannot record the claim is an outage: the proof was judged but
// not recorded, and accepting it would leave it replayable.
func TestAClaimFaultIsUnavailable(t *testing.T) {
	srv, pool, _ := siteverify(t, answer{http.StatusOK, accepted})
	store := newClaimStore()
	store.setErr = errors.New("the store is unavailable")
	p := providerWithStore(t, srv, pool, store, nil)

	err := p.Verify(t.Context(), authn.HumanProof{Token: testToken}, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("a claim fault resolved with %v, want ErrUnavailable", err)
	}
	if errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Errorf("a claim fault reads as an invalid proof: %v", err)
	}
}

// The claim is the token's digest under the spent prefix, with the whole token
// nowhere in the store, and it carries the lifetime that covers the token's
// validity.
func TestTheClaimStoresTheTokenDigestAndItsLifetime(t *testing.T) {
	srv, pool, _ := siteverify(t, answer{http.StatusOK, accepted})
	store := newClaimStore()
	p := providerWithStore(t, srv, pool, store, nil)

	if err := p.Verify(t.Context(), authn.HumanProof{Token: testToken}, authn.HumanScopeOther); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	sum := sha256.Sum256([]byte(testToken))
	want := spentKey(sum[:])
	keys := store.keys()
	if len(keys) != 1 || keys[0] != want {
		t.Fatalf("the store holds %v, want the one claim under %q", keys, want)
	}
	for _, key := range keys {
		if strings.Contains(key, testToken) {
			t.Errorf("the store key %q carries the token itself", key)
		}
	}
	store.mu.Lock()
	ttl := store.ttls[want]
	value := string(store.values[want])
	store.mu.Unlock()
	if value != "spent" {
		t.Errorf("the claim holds %q, want the spent marker", value)
	}
	if ttl != ClaimTTL {
		t.Errorf("the claim expires in %v, want %v", ttl, ClaimTTL)
	}
}
