package sso

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	speedhttp "github.com/vislake/speed/pkg/http"
)

const discoverFullPath = "/api/v1/auth/sso/discover"

func routed(t *testing.T) *rig {
	t.Helper()
	r := newRigSQLite(t)
	r.p.mountRoutes()
	return r
}

func routedAtBase(t *testing.T, base string) *rig {
	t.Helper()
	r := newRigAtBase(t, openSQLite(t), base)
	r.p.mountRoutes()
	return r
}

func problemType(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("the answer is not a problem document: %v", err)
	}
	return p.Type
}

func (r *rig) post(path, body string) *httptest.ResponseRecorder {
	r.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", mounttest.DefaultPublicURL)
	return r.mounter.Do(req)
}

// TestTheDiscoveryRouteIsMountedPublic checks that the discovery route is mounted public.
func TestTheDiscoveryRouteIsMountedPublic(t *testing.T) {
	r := routed(t)
	reg, ok := r.mounter.Registered(http.MethodPost, discoverFullPath)
	if !ok {
		t.Fatalf("no route is registered for POST %s; the registrations are %+v",
			discoverFullPath, r.mounter.Registrations())
	}
	if reg.Kind != mounttest.Public {
		t.Errorf("the discovery is registered %s, want public", reg.Kind)
	}
}

// TestTheDiscoveryAnswersTheShape checks that the discovery answers the declared shape.
func TestTheDiscoveryAnswersTheShape(t *testing.T) {
	r := routed(t)
	idp := r.verified("tenant-a", "acme", "example.com")

	w := r.post(discoverFullPath, `{"email":"someone@example.com"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("the discovery answered %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	var body discoverResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("the body %q is not the answer shape: %v", w.Body.String(), err)
	}
	if len(body.Providers) != 1 || body.Providers[0].ID != idp.ID ||
		body.Providers[0].Name != "acme" || body.Providers[0].Protocol != "oidc" {
		t.Errorf("the discovery answered %+v, want the one provider", body.Providers)
	}
}

// TestTheDiscoveryListsOnlyDeliveredProtocols checks that the discovery lists only delivered protocols.
func TestTheDiscoveryListsOnlyDeliveredProtocols(t *testing.T) {
	r := routed(t)
	oidc := r.verified("tenant-a", "oidc-idp", "oidc.example.com")
	saml := r.verifiedSAML("tenant-b", "saml-idp", "saml.example.com")

	cases := []struct {
		name      string
		delivered []string
		want      map[string]string
	}{
		{"only oidc", []string{authn.SSOProtocolOIDC}, map[string]string{"oidc.example.com": oidc.ID}},
		{"only saml", []string{authn.SSOProtocolSAML}, map[string]string{"saml.example.com": saml.ID}},
		{"neither", nil, map[string]string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r.deliverProtocols(tc.delivered...)
			for _, domain := range []string{"oidc.example.com", "saml.example.com"} {
				w := r.post(discoverFullPath, `{"email":"someone@`+domain+`"}`)
				if w.Code != http.StatusOK {
					t.Fatalf("the discovery of %s answered %d: %s", domain, w.Code, w.Body.String())
				}
				var body discoverResponse
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatalf("the body %q is not the answer shape: %v", w.Body.String(), err)
				}
				wantID, listed := tc.want[domain]
				if listed {
					if len(body.Providers) != 1 || body.Providers[0].ID != wantID {
						t.Errorf("the discovery of %s listed %+v, want only provider %s", domain, body.Providers, wantID)
					}
					continue
				}
				if len(body.Providers) != 0 {
					t.Errorf("the discovery of %s listed %+v, want none: its protocol is not delivered", domain, body.Providers)
				}
			}
		})
	}
}

// TestTheDiscoveryWithNoDeliveredSetListsNothing checks that the discovery with no delivered set lists nothing.
func TestTheDiscoveryWithNoDeliveredSetListsNothing(t *testing.T) {
	r := routed(t)
	r.verified("tenant-a", "oidc-idp", "oidc.example.com")
	r.p.delivered = nil

	w := r.post(discoverFullPath, `{"email":"someone@oidc.example.com"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("the discovery answered %d: %s", w.Code, w.Body.String())
	}
	var body discoverResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("the body %q is not the answer shape: %v", w.Body.String(), err)
	}
	if len(body.Providers) != 0 {
		t.Errorf("the discovery with no delivered set listed %+v, want none", body.Providers)
	}
}

// TestTheDiscoveryWithNothingDeliveredMatchesNoMatch checks that a discovery request with nothing delivered matches no route.
func TestTheDiscoveryWithNothingDeliveredMatchesNoMatch(t *testing.T) {
	r := routed(t)
	r.verified("tenant-a", "oidc-idp", "oidc.example.com")
	r.deliverProtocols()

	held := r.post(discoverFullPath, `{"email":"someone@oidc.example.com"}`)
	unheld := r.post(discoverFullPath, `{"email":"someone@nobody.example.org"}`)
	if held.Code != http.StatusOK || unheld.Code != http.StatusOK {
		t.Fatalf("the discovery answered %d and %d, want 200 both", held.Code, unheld.Code)
	}
	if held.Body.String() != unheld.Body.String() {
		t.Errorf("a held domain with nothing delivered answered %q, an unknown domain %q; want the same body",
			held.Body.String(), unheld.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(held.Body.Bytes(), &raw); err != nil {
		t.Fatalf("the body %q is not a JSON object: %v", held.Body.String(), err)
	}
	if got := string(raw["providers"]); got != "[]" {
		t.Errorf("the answer's providers = %s, want an empty array", got)
	}
}

// TestTheDiscoveryAnswersUnavailableWhenTheSwitchCannotBeRead checks that the discovery answers 503 when the switch cannot be read.
func TestTheDiscoveryAnswersUnavailableWhenTheSwitchCannotBeRead(t *testing.T) {
	r := routed(t)
	r.verified("tenant-a", "oidc-idp", "oidc.example.com")
	sw := &standInSwitch{}
	r.p.loginSwitch = sw

	if w := r.post(discoverFullPath, `{"email":"someone@oidc.example.com"}`); w.Code != http.StatusOK {
		t.Fatalf("the discovery with a readable switch answered %d: %s", w.Code, w.Body.String())
	}
	sw.set(nil, errors.New("the settings store is unreachable"))

	held := r.post(discoverFullPath, `{"email":"someone@oidc.example.com"}`)
	unheld := r.post(discoverFullPath, `{"email":"someone@nobody.example.org"}`)
	unreducible := r.post(discoverFullPath, `{"email":"someone@"}`)
	for _, w := range []*httptest.ResponseRecorder{held, unheld, unreducible} {
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("the discovery answered %d, want 503: %s", w.Code, w.Body.String())
		}
	}
	if held.Body.String() != unheld.Body.String() || held.Body.String() != unreducible.Body.String() {
		t.Errorf("a failed switch read answered %q for a held domain, %q for an unknown one and %q for an address with no domain; want the same body",
			held.Body.String(), unheld.Body.String(), unreducible.Body.String())
	}
	if want := "urn:speed:authn:" + mount.CodeUnavailable; problemType(t, held) != want {
		t.Errorf("the 503's type = %q, want %q", problemType(t, held), want)
	}
	if got := sw.calls(); got != 4 {
		t.Errorf("the switch was read %d times over 4 requests, want one read per request", got)
	}
}

// TestTheDiscoveryReadsTheRunTimeSwitchOncePerRequest checks that the discovery reads the run-time switch once per request.
func TestTheDiscoveryReadsTheRunTimeSwitchOncePerRequest(t *testing.T) {
	r := routed(t)
	sw := &standInSwitch{}
	r.p.loginSwitch = sw

	steps := []struct {
		name    string
		email   string
		listing int
		add     func()
	}{
		{"no provider", "someone@nobody.example.org", 0, nil},
		{"one provider", "someone@oidc.example.com", 1, func() {
			r.verified("tenant-a", "oidc-idp", "oidc.example.com")
		}},
		{"two in the register", "someone@saml.example.com", 1, func() {
			r.verifiedSAML("tenant-b", "saml-idp", "saml.example.com")
		}},
	}
	for i, step := range steps {
		if step.add != nil {
			step.add()
		}
		w := r.post(discoverFullPath, `{"email":"`+step.email+`"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("the discovery of %s answered %d: %s", step.email, w.Code, w.Body.String())
		}
		var body discoverResponse
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("the body %q is not the answer shape: %v", w.Body.String(), err)
		}
		if len(body.Providers) != step.listing {
			t.Fatalf("the discovery of %s listed %+v, want %d", step.email, body.Providers, step.listing)
		}
		if got := sw.calls(); got != i+1 {
			t.Errorf("after the request with %s the switch had been read %d times, want %d (one per request)",
				step.name, got, i+1)
		}
	}
}

// TestTheDiscoveryOfAnUnknownDomainIsTheSameEmptyShape checks that an unknown domain answers the same empty shape.
func TestTheDiscoveryOfAnUnknownDomainIsTheSameEmptyShape(t *testing.T) {
	r := routed(t)

	for _, email := range []string{"a@nobody.example.org", "b@nobody.example.org"} {
		w := r.post(discoverFullPath, `{"email":"`+email+`"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("the discovery answered %d for %s: %s", w.Code, email, w.Body.String())
		}
		var body discoverResponse
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("the body %q is not the answer shape: %v", w.Body.String(), err)
		}
		if len(body.Providers) != 0 {
			t.Errorf("the discovery of %s listed %+v, want none", email, body.Providers)
		}
	}
}

// TestTheDiscoveryRefusesADomainThatIsNotOne checks that the discovery refuses a domain that cannot be normalized.
func TestTheDiscoveryRefusesADomainThatIsNotOne(t *testing.T) {
	r := routed(t)
	w := r.post(discoverFullPath, `{"email":"someone@例子.测试"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("the discovery answered %d for a non-ASCII domain, want 400", w.Code)
	}
	var p struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("the answer is not a problem document: %v", err)
	}
	if want := "urn:speed:authn:" + mount.CodeBadRequest; p.Type != want {
		t.Errorf("the 400's type = %q, want %q", p.Type, want)
	}
	if w := r.post(discoverFullPath, `{"email":"not-an-address"}`); w.Code != http.StatusBadRequest {
		t.Errorf("the discovery answered %d for an address with no domain, want 400", w.Code)
	}
}

// TestTheDiscoveryRefusesAnotherMediaType checks that the discovery refuses another media type.
func TestTheDiscoveryRefusesAnotherMediaType(t *testing.T) {
	r := routed(t)
	req := httptest.NewRequest(http.MethodPost, discoverFullPath, strings.NewReader(`{"email":"a@example.com"}`))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Origin", mounttest.DefaultPublicURL)
	w := r.mounter.Do(req)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("a text/plain discovery answered %d, want 415", w.Code)
	}
	if want := "urn:speed:authn:" + mount.CodeUnsupportedMediaType; problemType(t, w) != want {
		t.Errorf("the 415's type = %q, want %q", problemType(t, w), want)
	}
}

// TestTheDiscoveryRefusesABodyThatIsNotJSON checks that the discovery refuses a body that is not JSON.
func TestTheDiscoveryRefusesABodyThatIsNotJSON(t *testing.T) {
	r := routed(t)
	for _, body := range []string{``, `null`, `[]`, `{"email":`, `{"email":"a@example.com"} {}`} {
		w := r.post(discoverFullPath, body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("the discovery answered %d for the body %q, want 400", w.Code, body)
			continue
		}
		if want := "urn:speed:authn:" + mount.CodeBadRequest; problemType(t, w) != want {
			t.Errorf("the 400's type for %q = %q, want %q", body, problemType(t, w), want)
		}
	}
}

// TestTheDiscoveryRefusesABodyOverTheCapabilityLimit checks that the discovery refuses a body over the capability's limit.
func TestTheDiscoveryRefusesABodyOverTheCapabilityLimit(t *testing.T) {
	r := routed(t)
	base := `{"email":"a@example.com"}`
	atLimit := base + strings.Repeat(" ", mount.MaxBodyBytes-len(base))
	if w := r.post(discoverFullPath, atLimit); w.Code != http.StatusOK {
		t.Fatalf("the discovery answered %d for a body of exactly %d bytes, want 200",
			w.Code, mount.MaxBodyBytes)
	}
	overLimit := base + strings.Repeat(" ", mount.MaxBodyBytes)
	w := r.post(discoverFullPath, overLimit)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("the discovery answered %d for a body over the capability limit, want 400", w.Code)
	}
	if want := "urn:speed:authn:" + mount.CodeBadRequest; problemType(t, w) != want {
		t.Errorf("the 400's type = %q, want %q", problemType(t, w), want)
	}
}

// TestTheDiscoveryRefusesAnUnknownField checks that the discovery refuses an unknown field.
func TestTheDiscoveryRefusesAnUnknownField(t *testing.T) {
	r := routed(t)
	w := r.post(discoverFullPath, `{"email":"a@example.com","local":"b"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("the discovery answered %d for an unknown field, want 400", w.Code)
	}
	if want := "urn:speed:authn:" + mount.CodeBadRequest; problemType(t, w) != want {
		t.Errorf("the 400's type = %q, want %q", problemType(t, w), want)
	}
}

// TestTheDiscoveryFollowsAConfiguredBase checks that the discovery route follows a configured base.
func TestTheDiscoveryFollowsAConfiguredBase(t *testing.T) {
	const base = "/identity"
	r := routedAtBase(t, base)
	path := base + "/sso/discover"
	reg, ok := r.mounter.Registered(http.MethodPost, path)
	if !ok {
		t.Fatalf("no route is registered for POST %s; the registrations are %+v", path, r.mounter.Registrations())
	}
	if reg.Kind != mounttest.Public {
		t.Errorf("the discovery is registered %s, want public", reg.Kind)
	}
	if w := r.post(path, `{"email":"someone@example.com"}`); w.Code != http.StatusOK {
		t.Errorf("the discovery under a configured base answered %d: %s", w.Code, w.Body.String())
	}
}

// TestTheModuleDeclaresItsOperationsAsAResource checks that the module declares its operations as a resource.
func TestTheModuleDeclaresItsOperationsAsAResource(t *testing.T) {
	var spec, operationsDeclared bool
	for _, resource := range Module().Resources {
		switch resource.(type) {
		case speedhttp.Spec:
			spec = true
		case speedhttp.Operations:
			operationsDeclared = true
		}
	}
	if !spec || !operationsDeclared {
		t.Errorf("the module's resources declare the spec %t and the operations %t, want both",
			spec, operationsDeclared)
	}
}

// TestTheDiscoveryRefusesAnotherSite checks that the discovery refuses another site.
func TestTheDiscoveryRefusesAnotherSite(t *testing.T) {
	r := routed(t)
	req := httptest.NewRequest(http.MethodPost, discoverFullPath, strings.NewReader(`{"email":"a@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example.org")
	if w := r.mounter.Do(req); w.Code != http.StatusUnauthorized {
		t.Errorf("a cross-site discovery answered %d, want 401", w.Code)
	}
}
