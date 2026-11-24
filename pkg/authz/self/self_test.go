package self

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/log"
)

type stubPermissions struct {
	mu       sync.Mutex
	byDomain map[authz.Domain][]authz.Permission
	err      error
	asked    []authz.Domain
	subjects []string
}

func (s *stubPermissions) Held(_ context.Context, domain authz.Domain, subject string) ([]authz.Permission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, domain)
	s.subjects = append(s.subjects, subject)
	if s.err != nil {
		return nil, s.err
	}
	held, ok := s.byDomain[domain]
	if !ok {
		return []authz.Permission{}, nil
	}
	return append([]authz.Permission(nil), held...), nil
}

func (s *stubPermissions) domains() []authz.Domain {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]authz.Domain(nil), s.asked...)
}

func newRig(permissions *stubPermissions) *product {
	return &product{
		permissions: permissions,
		logger:      log.Default(),
		cfg:         resolved{endpoints: []string{"public"}},
	}
}

func serve(t *testing.T, handler gin.HandlerFunc, ctx context.Context) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, pathPermissions, nil)
	req = req.WithContext(ctx)
	c.Request = req
	handler(c)
	return w
}

func mounted(p *product) gin.HandlerFunc {
	return withRealm("public", p.requireIdentity(permissionsOperation.Handle(p.handlePermissions)))
}

func decode(t *testing.T, w *httptest.ResponseRecorder) permissionsBody {
	t.Helper()
	var body permissionsBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the answer %q: %v", w.Body.String(), err)
	}
	return body
}

// TestTheReadAnswersPlatformAndTenantPermissions checks that the read answers platform and tenant permissions.
func TestTheReadAnswersPlatformAndTenantPermissions(t *testing.T) {
	stub := &stubPermissions{byDomain: map[authz.Domain][]authz.Permission{
		authz.PlatformDomain: {authz.PermissionAccountRead},
		"acme":               {authz.PermissionRoleRead},
	}}
	p := newRig(stub)
	ctx := authntest.Context(t, tenantContext(t, "acme"))

	w := serve(t, permissionsOperation.Handle(p.handlePermissions), ctx)
	if w.Code != http.StatusOK {
		t.Fatalf("the read answered %d: %s", w.Code, w.Body.String())
	}
	body := decode(t, w)
	if len(body.Platform) != 1 || body.Platform[0] != string(authz.PermissionAccountRead) {
		t.Errorf("platform = %v, want the platform-domain permission", body.Platform)
	}
	if len(body.Tenant) != 1 || body.Tenant[0] != string(authz.PermissionRoleRead) {
		t.Errorf("tenant = %v, want the tenant-domain permission", body.Tenant)
	}
}

// TestAnotherTenantsGrantDoesNotAppear checks that another tenants grant does not appear.
func TestAnotherTenantsGrantDoesNotAppear(t *testing.T) {
	stub := &stubPermissions{byDomain: map[authz.Domain][]authz.Permission{
		"acme":   {authz.PermissionRoleRead},
		"globex": {authz.PermissionSettingsWrite},
	}}
	p := newRig(stub)
	ctx := authntest.Context(t, tenantContext(t, "acme"))

	w := serve(t, permissionsOperation.Handle(p.handlePermissions), ctx)
	if w.Code != http.StatusOK {
		t.Fatalf("the read answered %d: %s", w.Code, w.Body.String())
	}
	body := decode(t, w)
	if len(body.Tenant) != 1 || body.Tenant[0] != string(authz.PermissionRoleRead) {
		t.Fatalf("tenant = %v, want only acme's permission", body.Tenant)
	}
	for _, name := range body.Tenant {
		if name == string(authz.PermissionSettingsWrite) {
			t.Errorf("the answer carries globex's grant %q under acme", name)
		}
	}
	asked := stub.domains()
	if len(asked) != 2 || asked[0] != authz.PlatformDomain || asked[1] != "acme" {
		t.Errorf("the read asked the domains %v, want the platform domain then acme", asked)
	}
}

// TestNoTenantLeavesTheTenantListEmpty checks that no tenant leaves the tenant list empty.
func TestNoTenantLeavesTheTenantListEmpty(t *testing.T) {
	stub := &stubPermissions{byDomain: map[authz.Domain][]authz.Permission{
		authz.PlatformDomain: {authz.PermissionAccountRead},
	}}
	p := newRig(stub)
	ctx := authntest.Context(t, t.Context())

	w := serve(t, permissionsOperation.Handle(p.handlePermissions), ctx)
	if w.Code != http.StatusOK {
		t.Fatalf("the read answered %d: %s", w.Code, w.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decoding the answer %q: %v", w.Body.String(), err)
	}
	if got := string(raw["tenant"]); got != "[]" {
		t.Errorf("tenant is %s, want an empty list", got)
	}
	body := decode(t, w)
	if len(body.Platform) != 1 {
		t.Errorf("platform = %v, want the platform-domain permission", body.Platform)
	}
}

// TestAnonymousIsRefusedWithTheGatewayChallenge checks that anonymous is refused with the gateway challenge.
func TestAnonymousIsRefusedWithTheGatewayChallenge(t *testing.T) {
	stub := &stubPermissions{}
	p := newRig(stub)
	ctx := authntest.Anonymous(t, t.Context(), authn.AccessOptional)

	w := serve(t, mounted(p), ctx)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("an anonymous request answered %d, want 401", w.Code)
	}
	if got, want := w.Header().Get("WWW-Authenticate"), `Bearer realm="public", Cookie realm="public"`; got != want {
		t.Errorf("the challenge is %q, want %q", got, want)
	}
	if asked := stub.domains(); len(asked) != 0 {
		t.Errorf("an anonymous request asked the policy %v, want nothing", asked)
	}
}

// TestAnUnreadablePolicyIsA503 checks that an unreadable policy is a 503.
func TestAnUnreadablePolicyIsA503(t *testing.T) {
	stub := &stubPermissions{err: errors.New("the store is down")}
	p := newRig(stub)
	ctx := authntest.Context(t, t.Context())

	w := serve(t, permissionsOperation.Handle(p.handlePermissions), ctx)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("an unreadable policy answered %d: %s", w.Code, w.Body.String())
	}
}

// TestTheReadAsksForTheEffectiveAccount checks that the read asks for the effective account.
func TestTheReadAsksForTheEffectiveAccount(t *testing.T) {
	stub := &stubPermissions{byDomain: map[authz.Domain][]authz.Permission{}}
	p := newRig(stub)
	ctx := authntest.Context(t, tenantContext(t, "acme"),
		authntest.WithAccount("acct-bob"),
		authntest.WithActor("acct-alice"),
		authntest.WithSession("sess-1"),
		authntest.WithScope("acme"),
	)

	w := serve(t, permissionsOperation.Handle(p.handlePermissions), ctx)
	if w.Code != http.StatusOK {
		t.Fatalf("the read answered %d: %s", w.Code, w.Body.String())
	}
	if len(stub.subjects) == 0 {
		t.Fatal("the read asked the policy about no subject")
	}
	for _, subject := range stub.subjects {
		if subject != "acct-bob" {
			t.Errorf("the read asked about %q, want the effective account acct-bob", subject)
		}
	}
}
