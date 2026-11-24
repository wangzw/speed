package wechatweb

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/egress"
	"github.com/vislake/speed/pkg/authn/internal/step"
	wechat "github.com/vislake/speed/pkg/authn/login/internal/wechat"
)

const (
	testAppID     = "wx-web-app-001"
	testAppSecret = "web-app-secret-4b9c"
	testRedirect  = "https://app.example.com/api/v1/auth/callback/wechat.web"
)

func tokenServer(t *testing.T, status int, body string) (*httptest.Server, *wechat.Client) {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	client := wechat.New(wechat.Options{
		AppID:         testAppID,
		AppSecret:     testAppSecret,
		TokenEndpoint: srv.URL,
		HTTP:          egress.New(egress.Options{AllowLoopback: true, RootCAs: pool}),
	})
	return srv, client
}

func begin(t *testing.T, m *method) (private []byte, state string, to string) {
	t.Helper()
	got, err := m.Begin(context.Background(), authn.Start{Purpose: authn.PurposeLogin})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if step.KindOf(got) != step.KindRedirect {
		t.Fatalf("Begin answered %s, want a redirect", describe(got))
	}
	to = step.RedirectOf(got)
	u, err := url.Parse(to)
	if err != nil {
		t.Fatalf("the authorize address does not parse: %v", err)
	}
	return step.PrivateOf(got), u.Query().Get("state"), to
}

func answer(m *method, private []byte, query url.Values) authn.Step {
	got, _ := m.Answer(context.Background(),
		authn.FlowState{Purpose: authn.PurposeLogin, Private: private},
		authn.StepInput{Action: authn.InputCallback, Query: query})
	return got
}

// TestBeginRedirectsToTheWebsiteApplicationEndpoint checks the scope, app id, redirect URI and 64-character state.
func TestBeginRedirectsToTheWebsiteApplicationEndpoint(t *testing.T) {
	_, client := tokenServer(t, http.StatusOK, `{"openid":"x"}`)
	m := newMethod(client, testRedirect)

	_, state, to := begin(t, m)

	u, err := url.Parse(to)
	if err != nil {
		t.Fatalf("the authorize address does not parse: %v", err)
	}
	if u.Host != "open.weixin.qq.com" || u.Path != "/connect/qrconnect" {
		t.Errorf("address = %q, want the website-application endpoint", to)
	}
	q := u.Query()
	if got := q.Get("scope"); got != "snsapi_login" {
		t.Errorf("scope = %q, want snsapi_login", got)
	}
	if got := q.Get("appid"); got != testAppID {
		t.Errorf("appid = %q, want %q", got, testAppID)
	}
	if got := q.Get("redirect_uri"); got != testRedirect {
		t.Errorf("redirect_uri = %q, want %q", got, testRedirect)
	}
	if state == "" || len(state) != 64 {
		t.Errorf("state = %q, want a 64-character value", state)
	}
	if q.Get("state") != state {
		t.Errorf("the address carries state %q, want %q", q.Get("state"), state)
	}
}

// TestTheCallbackProvesTheIdentity checks that the callback proves the WeChat
// identity under the union key and keeps the open-id key as an alias.
func TestTheCallbackProvesTheIdentity(t *testing.T) {
	_, client := tokenServer(t, http.StatusOK,
		`{"access_token":"t","expires_in":7200,"openid":"OPEN-7","unionid":"UNION-7"}`)
	m := newMethod(client, testRedirect)
	private, state, _ := begin(t, m)

	got := answer(m, private, url.Values{"state": {state}, "code": {"the-code"}})
	if step.KindOf(got) != step.KindProven {
		t.Fatalf("Answer = %s, want a proof", describe(got))
	}
	p := step.ProofOf(got)
	if p.External == nil || p.External.Provider != "wechat" || p.External.Subject != "UNION-7" {
		t.Fatalf("proof external = %+v, want wechat / UNION-7", p.External)
	}
	if len(p.Aliases) != 1 || p.Aliases[0].Provider != "wechat-openid:"+testAppID {
		t.Errorf("aliases = %+v, want the open-id key", p.Aliases)
	}
}

// TestTheCallbackIsRefusedWhenTheStateIsWrongOrMissing checks that the callback is
// refused when the state is wrong or missing, or the code is missing.
func TestTheCallbackIsRefusedWhenTheStateIsWrongOrMissing(t *testing.T) {
	_, client := tokenServer(t, http.StatusOK, `{"openid":"OPEN-7"}`)
	m := newMethod(client, testRedirect)
	private, state, _ := begin(t, m)

	cases := []struct {
		name  string
		query url.Values
	}{
		{"state missing", url.Values{"code": {"the-code"}}},
		{"state wrong", url.Values{"state": {"a-different-state"}, "code": {"the-code"}}},
		{"code missing", url.Values{"state": {state}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := answer(m, private, tc.query); step.KindOf(got) != step.KindRefused {
				t.Errorf("Answer = %s, want a refusal", describe(got))
			}
		})
	}
}

// TestAnErrcodeIsRefusedAndTheSecretStaysIn checks that an errcode is a refusal
// whose cause does not carry the secret.
func TestAnErrcodeIsRefusedAndTheSecretStaysIn(t *testing.T) {
	_, client := tokenServer(t, http.StatusOK, `{"errcode":40029,"errmsg":"invalid code"}`)
	m := newMethod(client, testRedirect)
	private, state, _ := begin(t, m)

	got := answer(m, private, url.Values{"state": {state}, "code": {"the-code"}})
	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("Answer = %s, want a refusal", describe(got))
	}
	if cause := step.CauseOf(got); cause == nil || strings.Contains(cause.Error(), testAppSecret) {
		t.Errorf("the refusal cause %v carries the secret or is missing", cause)
	}
}

// TestANonTwoHundredStatusIsAnOutage checks that a non-200 answer is an outage.
func TestANonTwoHundredStatusIsAnOutage(t *testing.T) {
	_, client := tokenServer(t, http.StatusBadGateway, `{}`)
	m := newMethod(client, testRedirect)
	private, state, _ := begin(t, m)

	if got := answer(m, private, url.Values{"state": {state}, "code": {"the-code"}}); step.KindOf(got) != step.KindUnavailable {
		t.Errorf("Answer = %s, want an outage", describe(got))
	}
}

// TestTheOutageCauseNeverCarriesTheSecret holds that an outage's logged cause carries no part of the token request.
func TestTheOutageCauseNeverCarriesTheSecret(t *testing.T) {
	_, client := tokenServer(t, http.StatusInternalServerError, `{}`)
	m := newMethod(client, testRedirect)
	private, state, _ := begin(t, m)

	got := answer(m, private, url.Values{"state": {state}, "code": {"the-code"}})
	if step.KindOf(got) != step.KindUnavailable {
		t.Fatalf("Answer = %s, want an outage", describe(got))
	}
	text := step.CauseOf(got).Error()
	for _, leak := range []string{testAppSecret, "secret=", "grant_type"} {
		if strings.Contains(text, leak) {
			t.Errorf("the outage cause %q carries %q", text, leak)
		}
	}
}

// TestTheMethodServesLoginLinkAndStepUpOnly checks that the method serves login,
// link and step-up only.
func TestTheMethodServesLoginLinkAndStepUpOnly(t *testing.T) {
	_, client := tokenServer(t, http.StatusOK, `{"openid":"x"}`)
	m := newMethod(client, testRedirect)

	for _, purpose := range []string{authn.PurposeLogin, authn.PurposeLink, authn.PurposeStepUp} {
		if got, _ := m.Begin(context.Background(), authn.Start{Purpose: purpose}); step.KindOf(got) != step.KindRedirect {
			t.Errorf("Begin(%s) = %s, want a redirect", purpose, describe(got))
		}
	}
	for _, purpose := range []string{authn.PurposeEnroll, "", "something"} {
		if got, _ := m.Begin(context.Background(), authn.Start{Purpose: purpose}); step.KindOf(got) != step.KindRefused {
			t.Errorf("Begin(%q) = %s, want a refusal", purpose, describe(got))
		}
	}
}

// TestASubmitAnswerIsRefused checks that a submit answer is refused.
func TestASubmitAnswerIsRefused(t *testing.T) {
	_, client := tokenServer(t, http.StatusOK, `{"openid":"x"}`)
	m := newMethod(client, testRedirect)
	got, _ := m.Answer(context.Background(),
		authn.FlowState{Purpose: authn.PurposeLogin},
		authn.StepInput{Action: authn.InputSubmit})
	if step.KindOf(got) != step.KindRefused {
		t.Errorf("Answer = %s, want a refusal for a non-callback answer", describe(got))
	}
}

// TestANameIsThePublishedOne checks that Name reports the published name
// wechat.web.
func TestANameIsThePublishedOne(t *testing.T) {
	_, client := tokenServer(t, http.StatusOK, `{"openid":"x"}`)
	if got := newMethod(client, testRedirect).Name(); got != "wechat.web" {
		t.Errorf("Name() = %q, want wechat.web", got)
	}
}

func describe(s authn.Step) string {
	return "kind " + step.KindOf(s).String()
}

// TestACancelledAuthorizationIsRefusedAsACancellation holds that a cancelled callback carries the cancellation cause, not a missing code.
func TestACancelledAuthorizationIsRefusedAsACancellation(t *testing.T) {
	_, client := tokenServer(t, http.StatusOK, `{"openid":"x"}`)
	m := newMethod(client, testRedirect)
	private, state, _ := begin(t, m)

	got := answer(m, private, url.Values{"state": {state}, "error": {"access_denied"}})
	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("a cancelled callback answered %s, want a refusal", describe(got))
	}
	if !errors.Is(step.CauseOf(got), errCancelled) {
		t.Errorf("the cause is %v, want the cancellation sentinel", step.CauseOf(got))
	}
}

// TestACancelledContextIsUnavailable holds that an ended context answers an outage before drawing or reaching the provider.
func TestACancelledContextIsUnavailable(t *testing.T) {
	_, client := tokenServer(t, http.StatusOK, `{"openid":"x"}`)
	m := newMethod(client, testRedirect)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if got, _ := m.Begin(ctx, authn.Start{Purpose: authn.PurposeLogin}); step.KindOf(got) != step.KindUnavailable {
		t.Errorf("Begin under a cancelled context answered %s, want an outage", describe(got))
	}
	private, state, _ := begin(t, m)
	got, _ := m.Answer(ctx, authn.FlowState{Purpose: authn.PurposeLogin, Private: private},
		authn.StepInput{Action: authn.InputCallback, Query: url.Values{"state": {state}, "code": {"the-code"}}})
	if step.KindOf(got) != step.KindUnavailable {
		t.Errorf("Answer under a cancelled context answered %s, want an outage", describe(got))
	}
}
