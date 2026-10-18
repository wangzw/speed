package wechat

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn/internal/egress"
)

const (
	testAppID     = "wx-app-id-001"
	testAppSecret = "app-secret-2f7a91c4"
)

// recorded is what the mock server saw of the last request.
type recorded struct {
	method string
	query  url.Values
}

// tokenServer serves one token answer over TLS and reports the request it saw.
// The client reaches it through the restricted egress client, which admits the
// loopback address only when told to and trusts the server's own certificate.
func tokenServer(t *testing.T, status int, body string) (*httptest.Server, *x509.CertPool, *recorded) {
	t.Helper()
	seen := &recorded{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.method = r.Method
		seen.query = r.URL.Query()
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return srv, pool, seen
}

// clientFor points a client at a mock server through the restricted client.
func clientFor(srv *httptest.Server, pool *x509.CertPool) *Client {
	return New(Options{
		AppID:         testAppID,
		AppSecret:     testAppSecret,
		TokenEndpoint: srv.URL,
		HTTP:          egress.New(egress.Options{AllowLoopback: true, RootCAs: pool}),
	})
}

func TestExchangeSendsTheProvidersRequest(t *testing.T) {
	srv, pool, seen := tokenServer(t, http.StatusOK, `{"access_token":"t","expires_in":7200,"openid":"OPEN-1","unionid":"UNION-1"}`)
	c := clientFor(srv, pool)

	id, err := c.Exchange(context.Background(), "the-code")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if id.OpenID != "OPEN-1" || id.UnionID != "UNION-1" {
		t.Errorf("identity = %+v, want OPEN-1 / UNION-1", id)
	}
	if seen.method != http.MethodGet {
		t.Errorf("method = %q, want GET", seen.method)
	}
	want := map[string]string{
		"appid":      testAppID,
		"secret":     testAppSecret,
		"code":       "the-code",
		"grant_type": "authorization_code",
	}
	for key, value := range want {
		if got := seen.query.Get(key); got != value {
			t.Errorf("query %s = %q, want %q", key, got, value)
		}
	}
}

func TestANonZeroErrcodeIsARefusalToldApart(t *testing.T) {
	cases := []struct {
		name  string
		code  int
		class error
	}{
		{"an invalid code", 40029, ErrCodeRejected},
		{"a code already used", 40163, ErrCodeRejected},
		{"an invalid appid", 40013, ErrCredentialRejected},
		{"an invalid appsecret", 40125, ErrCredentialRejected},
		{"an invalid credential", 40001, ErrCredentialRejected},
		{"a missing appid", 41002, ErrCredentialRejected},
		{"a missing appsecret", 41004, ErrCredentialRejected},
		{"another refusal", 45011, ErrProviderRefused},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, pool, _ := tokenServer(t, http.StatusOK, `{"errcode":`+strconv.Itoa(tc.code)+`,"errmsg":"whatever"}`)
			c := clientFor(srv, pool)

			_, err := c.Exchange(context.Background(), "the-code")
			if err == nil {
				t.Fatal("Exchange succeeded, want a refusal")
			}
			var refusal *RefusalError
			if !errors.As(err, &refusal) {
				t.Fatalf("error = %v, want a *RefusalError", err)
			}
			if refusal.Code != tc.code {
				t.Errorf("errcode = %d, want %d", refusal.Code, tc.code)
			}
			if !errors.Is(err, tc.class) {
				t.Errorf("error = %v, want it to be %v", err, tc.class)
			}
		})
	}
}

func TestAnUnusableAnswerIsAnOutage(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"a status that is not 200", http.StatusBadGateway, `{"openid":"x"}`},
		{"a body that is not JSON", http.StatusOK, `<html>oops</html>`},
		{"a body with no open id", http.StatusOK, `{"access_token":"t"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, pool, _ := tokenServer(t, tc.status, tc.body)
			c := clientFor(srv, pool)
			_, err := c.Exchange(context.Background(), "the-code")
			if !errors.Is(err, ErrProviderFailed) {
				t.Fatalf("error = %v, want ErrProviderFailed", err)
			}
			var refusal *RefusalError
			if errors.As(err, &refusal) {
				t.Errorf("error = %v, want an outage and not a refusal", err)
			}
		})
	}
}

// roundTripFunc is a transport a case scripts, to drive a transport failure of a
// chosen shape without a network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestATransportFailureNeverCarriesTheURL holds that the *url.Error net/http
// returns — whose text is the whole request URL, secret included — is unwrapped
// and never handed back. The cause is kept, so the log still says why.
func TestATransportFailureNeverCarriesTheURL(t *testing.T) {
	c := New(Options{
		AppID:         testAppID,
		AppSecret:     testAppSecret,
		TokenEndpoint: "https://api.example/connect",
		HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, &url.Error{Op: req.Method, URL: req.URL.String(), Err: errors.New("connection refused")}
		})},
	})

	_, err := c.Exchange(context.Background(), "the-code")
	if !errors.Is(err, ErrProviderFailed) {
		t.Fatalf("error = %v, want ErrProviderFailed", err)
	}
	text := err.Error()
	for _, leak := range []string{testAppSecret, "secret=", "api.example", "the-code"} {
		if strings.Contains(text, leak) {
			t.Errorf("the error text %q carries %q", text, leak)
		}
	}
	if !strings.Contains(text, "connection refused") {
		t.Errorf("the error text %q lost the cause", text)
	}
}

// TestNoErrorPathCarriesTheSecret holds the invariant the ADR states: for every
// way an exchange can fail, the secret and the URL carrying it stay inside the
// client.
func TestNoErrorPathCarriesTheSecret(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T) *Client
	}{
		{"an errcode", func(t *testing.T) *Client {
			srv, pool, _ := tokenServer(t, http.StatusOK, `{"errcode":40125,"errmsg":"invalid appsecret"}`)
			return clientFor(srv, pool)
		}},
		{"a non-200 status", func(t *testing.T) *Client {
			srv, pool, _ := tokenServer(t, http.StatusInternalServerError, `{}`)
			return clientFor(srv, pool)
		}},
		{"a malformed body", func(t *testing.T) *Client {
			srv, pool, _ := tokenServer(t, http.StatusOK, `not json`)
			return clientFor(srv, pool)
		}},
		{"a transport failure", func(t *testing.T) *Client {
			return New(Options{
				AppID:         testAppID,
				AppSecret:     testAppSecret,
				TokenEndpoint: "https://api.example/connect",
				HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					return nil, &url.Error{Op: req.Method, URL: req.URL.String(), Err: errors.New("refused")}
				})},
			})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.build(t).Exchange(context.Background(), "the-code")
			if err == nil {
				t.Fatal("Exchange succeeded, want a failure")
			}
			if text := err.Error(); strings.Contains(text, testAppSecret) || strings.Contains(text, "secret=") {
				t.Errorf("the error text %q carries the secret", text)
			}
		})
	}
}

func TestAuthorizeURLNamesTheApplicationAndEndsWithTheFragment(t *testing.T) {
	c := New(Options{AppID: testAppID, AppSecret: testAppSecret})
	got := c.AuthorizeURL(AuthorizeQRConnect, "snsapi_login", "https://app.example/api/v1/auth/callback/wechat.web", "the-state")

	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("the authorize URL does not parse: %v", err)
	}
	if u.Host != "open.weixin.qq.com" || u.Path != "/connect/qrconnect" {
		t.Errorf("address = %q, want the website-application endpoint", got)
	}
	if u.Fragment != "wechat_redirect" {
		t.Errorf("fragment = %q, want wechat_redirect", u.Fragment)
	}
	q := u.Query()
	want := map[string]string{
		"appid":         testAppID,
		"scope":         "snsapi_login",
		"response_type": "code",
		"state":         "the-state",
		"redirect_uri":  "https://app.example/api/v1/auth/callback/wechat.web",
	}
	for key, value := range want {
		if have := q.Get(key); have != value {
			t.Errorf("query %s = %q, want %q", key, have, value)
		}
	}
	if strings.Contains(got, testAppSecret) {
		t.Errorf("the authorize address carries the secret: %q", got)
	}
}

// TestTheDefaultOutboundClientIsTheRestrictedOne holds that a client built with
// no transport of its own sends through internal/egress: it refuses a plain-http
// destination and a non-public one.
func TestTheDefaultOutboundClientIsTheRestrictedOne(t *testing.T) {
	t.Run("a plain-http destination is refused", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		defer srv.Close()
		c := New(Options{AppID: testAppID, AppSecret: testAppSecret, TokenEndpoint: srv.URL})
		_, err := c.Exchange(context.Background(), "the-code")
		if !errors.Is(err, egress.ErrSchemeNotHTTPS) {
			t.Fatalf("error = %v, want egress.ErrSchemeNotHTTPS", err)
		}
	})
	t.Run("a loopback destination is refused when not admitted", func(t *testing.T) {
		srv, _, _ := tokenServer(t, http.StatusOK, `{"openid":"x"}`)
		c := New(Options{AppID: testAppID, AppSecret: testAppSecret, TokenEndpoint: srv.URL})
		_, err := c.Exchange(context.Background(), "the-code")
		if !errors.Is(err, egress.ErrDestinationDenied) {
			t.Fatalf("error = %v, want egress.ErrDestinationDenied", err)
		}
	})
}

// TestTheTokenResponseSizeIsCapped holds the client's own response cap, which is
// what bounds the answer under a client a test injects: a body exactly at the cap
// is read, and one byte over is refused as an outage. It runs over a plain client
// so the cap the client applies itself is the one observed.
func TestTheTokenResponseSizeIsCapped(t *testing.T) {
	build := func(t *testing.T, body string) *Client {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, body)
		}))
		t.Cleanup(srv.Close)
		return New(Options{AppID: testAppID, AppSecret: testAppSecret, TokenEndpoint: srv.URL, HTTP: srv.Client()})
	}

	const prefix = `{"openid":"x","pad":"`
	const suffix = `"}`
	atLimit := prefix + strings.Repeat("a", maxResponseBytes-len(prefix)-len(suffix)) + suffix
	if len(atLimit) != maxResponseBytes {
		t.Fatalf("the boundary body is %d bytes, want %d", len(atLimit), maxResponseBytes)
	}
	if _, err := build(t, atLimit).Exchange(context.Background(), "the-code"); err != nil {
		t.Fatalf("a body exactly at the cap: %v", err)
	}
	if _, err := build(t, atLimit+" ").Exchange(context.Background(), "the-code"); !errors.Is(err, ErrProviderFailed) {
		t.Fatalf("a body one byte over the cap gave %v, want ErrProviderFailed", err)
	}
}

// TestTheDefaultClientHonoursAllowLoopbackAndRootCAs holds the two affordances
// the default restricted client exposes: a deployment or a dev run that admits
// the loopback address and trusts its own roots can reach the server through the
// client built with no transport of its own.
func TestTheDefaultClientHonoursAllowLoopbackAndRootCAs(t *testing.T) {
	srv, pool, seen := tokenServer(t, http.StatusOK, `{"openid":"OPEN-1"}`)
	c := New(Options{
		AppID:         testAppID,
		AppSecret:     testAppSecret,
		TokenEndpoint: srv.URL,
		AllowLoopback: true,
		RootCAs:       pool,
	})

	id, err := c.Exchange(context.Background(), "the-code")
	if err != nil {
		t.Fatalf("Exchange through the default client: %v", err)
	}
	if id.OpenID != "OPEN-1" {
		t.Errorf("openid = %q, want OPEN-1", id.OpenID)
	}
	if seen.query.Get("secret") != testAppSecret {
		t.Error("the exchange did not reach the server")
	}
}
