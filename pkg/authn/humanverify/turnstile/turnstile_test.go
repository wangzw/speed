package turnstile

import (
	"bytes"
	"context"
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
// restricted client, with the case's own changes applied.
func providerFor(t *testing.T, srv *httptest.Server, pool *x509.CertPool, change func(*Options)) *Provider {
	t.Helper()
	o := Options{
		SiteKey:   testSiteKey,
		Secret:    testSecret,
		VerifyURL: srv.URL,
		Hostname:  testHostname,
		Action:    testAction,
		Timeout:   2 * time.Second,
		HTTP:      egress.New(egress.Options{AllowLoopback: true, RootCAs: pool}),
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

// accepted is a siteverify answer accepting a proof from the expected hostname
// and action.
const accepted = `{"success":true,"hostname":"` + testHostname + `","action":"` + testAction + `","challenge_ts":"2026-10-25T10:00:00Z"}`

// A proof the provider accepts passes, and the call carries the secret, the
// token and the source address in its body.
func TestAValidProofPasses(t *testing.T) {
	srv, pool, calls := siteverify(t, answer{http.StatusOK, accepted})
	p := providerFor(t, srv, pool, nil)

	err := p.Verify(t.Context(), authn.HumanProof{Token: testToken, RemoteIP: testRemoteIP})
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

	err := p.Verify(ctx, authn.HumanProof{Token: testToken})
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

// An answer that accepted the proof for another hostname is not trusted: the
// provider fails closed with the outage answer.
func TestAHostnameMismatchIsUnavailable(t *testing.T) {
	srv, pool, _ := siteverify(t, answer{
		http.StatusOK,
		`{"success":true,"hostname":"evil.example.com","action":"` + testAction + `"}`,
	})
	p := providerFor(t, srv, pool, nil)

	err := p.Verify(t.Context(), authn.HumanProof{Token: testToken})
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

	err := p.Verify(t.Context(), authn.HumanProof{Token: testToken})
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("an action mismatch resolved with %v, want ErrUnavailable", err)
	}
	if errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Errorf("an action mismatch reads as an invalid proof: %v", err)
	}
}

// The action is optional: a deployment that names none accepts the answer's
// action, whatever the widget was rendered with.
func TestAnUnnamedActionIsNotChecked(t *testing.T) {
	srv, pool, _ := siteverify(t, answer{
		http.StatusOK,
		`{"success":true,"hostname":"` + testHostname + `","action":"anything"}`,
	})
	p := providerFor(t, srv, pool, func(o *Options) { o.Action = "" })

	if err := p.Verify(t.Context(), authn.HumanProof{Token: testToken}); err != nil {
		t.Fatalf("Verify with no expected action: %v", err)
	}
}

// A non-2xx answer is an outage: the provider could not be reached for a
// verdict, so the request is refused without sending.
func TestANonSuccessStatusIsUnavailable(t *testing.T) {
	srv, pool, _ := siteverify(t, answer{http.StatusInternalServerError, `{"success":true}`})
	p := providerFor(t, srv, pool, nil)

	err := p.Verify(t.Context(), authn.HumanProof{Token: testToken})
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

	err := p.Verify(t.Context(), authn.HumanProof{Token: testToken})
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

	if err := p.Verify(t.Context(), authn.HumanProof{Token: testToken}); err != nil {
		t.Fatalf("first Verify: %v", err)
	}
	err := p.Verify(t.Context(), authn.HumanProof{Token: testToken})
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

	err := p.Verify(t.Context(), authn.HumanProof{})
	if !errors.Is(err, authn.ErrHumanVerificationRequired) {
		t.Fatalf("an empty proof resolved with %v, want ErrHumanVerificationRequired", err)
	}
	if len(calls()) != 0 {
		t.Errorf("an empty proof still reached the provider")
	}
}

// An over-long proof is a malformed request (400), refused before any call.
func TestAnOverlongProofIsInvalidArgument(t *testing.T) {
	srv, pool, calls := siteverify(t, answer{http.StatusOK, accepted})
	p := providerFor(t, srv, pool, nil)

	err := p.Verify(t.Context(), authn.HumanProof{Token: strings.Repeat("x", maxTokenLen+1)})
	if !errors.Is(err, authn.ErrInvalidArgument) {
		t.Fatalf("an over-long proof resolved with %v, want ErrInvalidArgument", err)
	}
	if len(calls()) != 0 {
		t.Errorf("an over-long proof still reached the provider")
	}
}

// The provider reports the seam's answers about the deployment.
func TestTheProviderReportsItsNameSiteKeyAndRequirement(t *testing.T) {
	srv, pool, _ := siteverify(t, answer{http.StatusOK, accepted})
	p := providerFor(t, srv, pool, nil)

	if p.Provider() != ProviderName {
		t.Errorf("Provider = %q, want %q", p.Provider(), ProviderName)
	}
	if !p.Required() {
		t.Error("Required = false, want true")
	}
	if p.SiteKey() != testSiteKey {
		t.Errorf("SiteKey = %q, want %q", p.SiteKey(), testSiteKey)
	}
}

// A provider that cannot be reached is an outage, not a refusal.
func TestAnUnreachableProviderIsUnavailable(t *testing.T) {
	srv, pool, _ := siteverify(t, answer{http.StatusOK, accepted})
	srv.Close()
	p := providerFor(t, srv, pool, nil)

	err := p.Verify(context.Background(), authn.HumanProof{Token: testToken})
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("an unreachable provider resolved with %v, want ErrUnavailable", err)
	}
}
