package sso

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
)

// discoverFullPath is the discovery route as the mounter mounts it.
const discoverFullPath = "/auth/sso/discover"

// routed builds a rig with the routes mounted.
func routed(t *testing.T) *rig {
	t.Helper()
	r := newRigSQLite(t)
	r.p.mountRoutes()
	return r
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
// domain cannot be reduced carries.
func TestTheDiscoveryRefusesADomainThatIsNotOne(t *testing.T) {
	r := routed(t)
	w := r.post(discoverFullPath, `{"email":"someone@例子.测试"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("the discovery answered %d for a non-ASCII domain, want 400", w.Code)
	}
	// An address with no domain is the same refusal.
	if w := r.post(discoverFullPath, `{"email":"not-an-address"}`); w.Code != http.StatusBadRequest {
		t.Errorf("the discovery answered %d for an address with no domain, want 400", w.Code)
	}
}

// TestTheDiscoveryRefusesAnUnknownField holds that the body decoder rejects a
// field the request does not carry, so a caller cannot slip a filter in.
func TestTheDiscoveryRefusesAnUnknownField(t *testing.T) {
	r := routed(t)
	w := r.post(discoverFullPath, `{"email":"a@example.com","local":"b"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("the discovery answered %d for an unknown field, want 400", w.Code)
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
