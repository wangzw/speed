package humanverify

import (
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/humanverify/turnstile"
	"github.com/vislake/speed/pkg/authn/internal/egress"
)

// secretSentinel is the value a case configures as the Turnstile secret and
// then looks for in every failure.
const secretSentinel = "turnstile-secret-sentinel-9f8e7d6c"

// TestAConfigurationErrorNeverRepeatsTheTurnstileSecret holds the discipline that
// a module holding a secret configuration value keeps it out of the text of
// every failure on the way to a runnable configuration. The value is registered
// as a literal redaction rule for logs; this is the other half: the failure a
// startup prints must not carry it, whichever item the failure actually names.
func TestAConfigurationErrorNeverRepeatsTheTurnstileSecret(t *testing.T) {
	cases := map[string]func(map[string]any){
		"a secret with no site key":      func(s map[string]any) { delete(s, siteKeyItem) },
		"a secret with no hostname":      func(s map[string]any) { delete(s, hostnameItem) },
		"a secret with an http endpoint": func(s map[string]any) { s[verifyURLItem] = "http://example.com/siteverify" },
		"a secret with a bad timeout":    func(s map[string]any) { s[timeoutItem] = "31s" },
	}
	for name, breakSection := range cases {
		t.Run(name, func(t *testing.T) {
			section := turnstileSection()
			section[secretKeyItem] = secretSentinel
			breakSection(section)
			_, err := readBody(t, body(t, section))
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("resolve = %v, want one wrapping ErrInvalidConfig", err)
			}
			if strings.Contains(err.Error(), secretSentinel) {
				t.Errorf("the failure %q repeats the Turnstile secret", err)
			}
		})
	}
}

// TestAVerificationFailureNeverRepeatsTheTurnstileSecret holds the same
// discipline over the runtime path: a siteverify failure answers an outage whose
// text carries neither the configured secret nor the token the caller presented.
func TestAVerificationFailureNeverRepeatsTheTurnstileSecret(t *testing.T) {
	const token = "turnstile-token-sentinel-1a2b3c"
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"success":false}`)
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	provider, err := turnstile.New(turnstile.Options{
		SiteKey:   "1x00000000000000000000AA",
		Secret:    secretSentinel,
		VerifyURL: srv.URL,
		Hostname:  "login.example.com",
		Action:    "login",
		HTTP:      egress.New(egress.Options{AllowLoopback: true, RootCAs: pool}),
		Store:     newStubKV(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = provider.Verify(t.Context(), authn.HumanProof{Token: token}, authn.HumanScopeOther)
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("a 500 resolved with %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), secretSentinel) {
		t.Errorf("the failure %q repeats the Turnstile secret", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("the failure %q repeats the token", err)
	}
}
