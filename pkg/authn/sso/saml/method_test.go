package saml

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

func start(provider string) authn.Start {
	return authn.Start{Purpose: authn.PurposeLogin, Fields: map[string]string{"provider": provider}}
}

// TestBeginRedirectsToTheProvider checks that Begin redirects to the provider.
func TestBeginRedirectsToTheProvider(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, _ := newTestProduct(t, newFakeProviders(row))

	got, err := p.Begin(context.Background(), start(testProviderID))
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if step.KindOf(got) != step.KindRedirect {
		t.Fatalf("Begin = %s, want a redirect (%v)", step.KindOf(got), step.CauseOf(got))
	}
	if step.NameOf(got) != stepAssertion {
		t.Errorf("step name = %q, want %q", step.NameOf(got), stepAssertion)
	}
	u, err := url.Parse(step.RedirectOf(got))
	if err != nil {
		t.Fatalf("the redirect target does not parse: %v", err)
	}
	if !strings.HasPrefix(step.RedirectOf(got), testSSOURL) {
		t.Errorf("redirect target = %q, want it to lead to the provider's SSO URL", step.RedirectOf(got))
	}
	if u.Query().Get("SAMLRequest") == "" || u.Query().Get("RelayState") == "" {
		t.Errorf("the redirect carries no request or RelayState: %q", step.RedirectOf(got))
	}
	st, ok := readState(step.PrivateOf(got))
	if !ok {
		t.Fatal("the private state does not decode")
	}
	if st.Provider != testProviderID || st.Request == "" || st.Relay == "" {
		t.Errorf("state = %+v, want a request, a RelayState and the provider", st)
	}
	if st.Relay != u.Query().Get("RelayState") {
		t.Errorf("the RelayState in the state (%q) is not the one sent (%q)", st.Relay, u.Query().Get("RelayState"))
	}
}

// TestBeginRefusesAProviderThatDoesNotAnswer checks that Begin refuses a provider that does not answer.
func TestBeginRefusesAProviderThatDoesNotAnswer(t *testing.T) {
	idp := newIDP(t)
	cases := []struct {
		name string
		row  authn.SSOProvider
	}{
		{"unknown", providerRow(providerConfigJSON(t, idp, nil))},
		{"disabled", func() authn.SSOProvider {
			r := providerRow(providerConfigJSON(t, idp, nil))
			r.Enabled = false
			return r
		}()},
		{"wrong protocol", func() authn.SSOProvider {
			r := providerRow(providerConfigJSON(t, idp, nil))
			r.Protocol = "oidc"
			return r
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := newFakeProviders(tc.row)
			p, _ := newTestProduct(t, reg)
			id := tc.row.ID
			if tc.name == "unknown" {
				id = "no-such-provider"
			}
			got, err := p.Begin(context.Background(), start(id))
			if err != nil {
				t.Fatalf("Begin: %v", err)
			}
			if step.KindOf(got) != step.KindRefused {
				t.Errorf("Begin = %s, want a refusal", step.KindOf(got))
			}
		})
	}
}

// TestBeginRefusesAStartWithNoProvider checks that Begin refuses a start with no provider.
func TestBeginRefusesAStartWithNoProvider(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, _ := newTestProduct(t, newFakeProviders(row))

	got, err := p.Begin(context.Background(), start(""))
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if step.KindOf(got) != step.KindRefused {
		t.Errorf("Begin = %s, want a refusal", step.KindOf(got))
	}
}

// TestBeginRefusesAnUnservedPurpose checks that Begin refuses an unserved purpose.
func TestBeginRefusesAnUnservedPurpose(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, _ := newTestProduct(t, newFakeProviders(row))

	s := start(testProviderID)
	s.Purpose = authn.PurposeEnroll
	got, err := p.Begin(context.Background(), s)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if step.KindOf(got) != step.KindRefused {
		t.Errorf("Begin = %s, want a refusal", step.KindOf(got))
	}
}

// TestBeginAnswersUnavailableWhenTheRegisterCannotBeRead checks that Begin answers unavailable when the register cannot be read.
func TestBeginAnswersUnavailableWhenTheRegisterCannotBeRead(t *testing.T) {
	idp := newIDP(t)
	providers := newFakeProviders(providerRow(providerConfigJSON(t, idp, nil)))
	providers.getErr = errors.New("the database is down")
	p, _ := newTestProduct(t, providers)

	got, err := p.Begin(context.Background(), start(testProviderID))
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if step.KindOf(got) != step.KindUnavailable {
		t.Fatalf("Begin = %s, want an outage", step.KindOf(got))
	}
	if !errors.Is(step.CauseOf(got), authn.ErrUnavailable) {
		t.Errorf("the cause %v does not wrap ErrUnavailable", step.CauseOf(got))
	}
}

// TestSignRequestsWithoutAKeyIsRefused checks that signing requests without a key is refused.
func TestSignRequestsWithoutAKeyIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, map[string]any{"sign_requests": true}))
	p, _ := newTestProduct(t, newFakeProviders(row))

	if _, err := p.buildServiceProvider(row); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("buildServiceProvider = %v, want ErrInvalidConfig: a provider that signs with no key", err)
	}
}

// TestAnEmptyIdentityProviderIssuerIsRefused checks that an empty identity-provider issuer is refused.
func TestAnEmptyIdentityProviderIssuerIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, map[string]any{"idp_entity_id": ""}))
	p, _ := newTestProduct(t, newFakeProviders(row))

	if _, err := p.buildServiceProvider(row); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("buildServiceProvider = %v, want ErrInvalidConfig: an empty issuer lets the library skip its check", err)
	}
}

// TestTheServiceProviderKeyConfigurationMustBeAPair checks that the service-provider key configuration must be a pair.
func TestTheServiceProviderKeyConfigurationMustBeAPair(t *testing.T) {
	cases := []moduleConfig{
		{SPKeyFile: "/tmp/key.pem"},
		{SPCertFile: "/tmp/cert.pem", SPNextCertFile: "/tmp/next.pem"},
	}
	for _, c := range cases {
		if _, err := c.resolve(); !errors.Is(err, authn.ErrInvalidConfig) {
			t.Errorf("%+v resolved to %v, want ErrInvalidConfig", c, err)
		}
	}
}
