package sso

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// discoverFullPath is the discovery route as the mounter mounts it.
const discoverFullPath = "/api/v1/auth/sso/discover"

// routed builds a rig with the routes mounted.
func routed(t *testing.T) *rig {
	t.Helper()
	r := newRigSQLite(t)
	r.p.mountRoutes()
	return r
}

// routedAtBase builds a rig whose mounter is configured with base, with the
// routes mounted.
func routedAtBase(t *testing.T, base string) *rig {
	t.Helper()
	r := newRigAtBase(t, openSQLite(t), base)
	r.p.mountRoutes()
	return r
}

// problemType reads a problem document's type.
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

// post sends a POST with a JSON body and an origin the cross-origin check
// trusts, which is what a same-site fetch carries.
func (r *rig) post(path, body string) *httptest.ResponseRecorder {
	r.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", mounttest.DefaultPublicURL)
	return r.mounter.Do(req)
}

// TestTheDiscoveryRouteIsMountedPublic holds the route's registration: the
// discovery is exempt from authentication.
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

// TestTheDiscoveryAnswersTheShape holds the answer shape and its content: an
// enabled provider with a verified domain is named by id, name and protocol.
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

// TestTheDiscoveryOfAnUnknownDomainIsTheSameEmptyShape holds that a domain
// nobody holds answers the same shape with an empty list, whatever the local
// part is.
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

// TestTheDiscoveryRefusesADomainThatIsNotOne holds the 400 a request whose
// domain cannot be reduced carries, with the capability's problem code.
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
	// An address with no domain is the same refusal.
	if w := r.post(discoverFullPath, `{"email":"not-an-address"}`); w.Code != http.StatusBadRequest {
		t.Errorf("the discovery answered %d for an address with no domain, want 400", w.Code)
	}
}

// TestTheDiscoveryRefusesAnotherMediaType holds the 415 a body that is not
// declared as JSON carries, with the capability's problem code.
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

// TestTheDiscoveryRefusesABodyThatIsNotJSON holds the 400 a body under the
// declared media type that is not a JSON object carries, with the capability's
// problem code.
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

// TestTheDiscoveryRefusesABodyOverTheCapabilityLimit holds the capability's
// own body bound: a body of mount.MaxBodyBytes is read and answered, and one
// byte more is refused as a bad request with the capability's problem code, the
// answer the capability's decoder gave before the route was declared as an
// operation.
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

// TestTheDiscoveryRefusesAnUnknownField holds that the body decoder rejects a
// field the request does not carry, so a caller cannot slip a filter in, with
// the capability's problem code.
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

// TestTheDiscoveryFollowsAConfiguredBase holds that a host's configured mount
// base moves the route without moving the contract: the declaration is written
// against the default base and the relative form is joined to the configured
// one.
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

// TestTheModuleDeclaresItsOperationsAsAResource holds that the module's
// operations are declared beside its Spec, which is the declaration the
// contract generation reads.
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

// TestTheDiscoveryRefusesAnotherSite holds the cross-origin check the mounter
// wraps every unsafe request in.
func TestTheDiscoveryRefusesAnotherSite(t *testing.T) {
	r := routed(t)
	req := httptest.NewRequest(http.MethodPost, discoverFullPath, strings.NewReader(`{"email":"a@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example.org")
	if w := r.mounter.Do(req); w.Code != http.StatusUnauthorized {
		t.Errorf("a cross-site discovery answered %d, want 401", w.Code)
	}
}
