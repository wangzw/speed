package self

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

type stubMemberships struct {
	authn.Memberships
	byAccount map[authn.AccountID][]authn.Membership
	err       error
	asked     []authn.AccountID
}

func (s *stubMemberships) Tenants(_ context.Context, id authn.AccountID) ([]authn.Membership, error) {
	s.asked = append(s.asked, id)
	if s.err != nil {
		return nil, s.err
	}
	return append([]authn.Membership(nil), s.byAccount[id]...), nil
}

type stubCatalog struct {
	tenant.Catalog
	rows  map[tenant.ID]tenant.Tenant
	err   error
	asked []tenant.ID
}

func (s *stubCatalog) Get(_ context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
	s.asked = append(s.asked, id)
	if s.err != nil {
		return tenant.Tenant{}, false, s.err
	}
	row, ok := s.rows[id]
	return row, ok, nil
}

type stubKindResolver struct {
	kinds map[tenant.ID]tenant.Kind
	err   error
	asked []tenant.ID
}

func (s *stubKindResolver) KindOf(_ context.Context, id tenant.ID) (tenant.Kind, error) {
	s.asked = append(s.asked, id)
	if s.err != nil {
		return "", s.err
	}
	return s.kinds[id], nil
}

func newRig(memberships authn.Memberships, catalog tenant.Catalog) *product {
	return &product{
		memberships: memberships,
		catalog:     catalog,
		logger:      log.Default(),
		cfg:         resolved{endpoints: []string{"account"}},
	}
}

func newRigWithKind(memberships authn.Memberships, catalog tenant.Catalog, kind tenant.KindResolver) *product {
	p := newRig(memberships, catalog)
	p.kind = kind
	return p
}

func serve(t *testing.T, handler gin.HandlerFunc, ctx context.Context) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, pathTenants, nil)
	req = req.WithContext(ctx)
	c.Request = req
	handler(c)
	return w
}

func mounted(p *product) gin.HandlerFunc {
	return withRealm("account", p.requireIdentity(tenantsOperation.Handle(p.handleTenants)))
}

func decode(t *testing.T, w *httptest.ResponseRecorder) tenantsBody {
	t.Helper()
	var body tenantsBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the answer %q: %v", w.Body.String(), err)
	}
	return body
}

func activeTenant(id, name string) tenant.Tenant {
	return tenant.Tenant{ID: tenant.ID(id), Name: name, Status: tenant.StatusActive}
}

func TestTheReadListsActiveMembershipsOfActiveTenants(t *testing.T) {
	memberships := &stubMemberships{byAccount: map[authn.AccountID][]authn.Membership{
		"acct-bob": {
			{Account: "acct-bob", Tenant: "acme", Active: true},
			{Account: "acct-bob", Tenant: "globex", Active: true},
		},
	}}
	catalog := &stubCatalog{rows: map[tenant.ID]tenant.Tenant{
		"acme":   activeTenant("acme", "Acme"),
		"globex": activeTenant("globex", "Globex"),
	}}
	p := newRig(memberships, catalog)
	ctx := authntest.Context(t, t.Context(), authntest.WithAccount("acct-bob"))

	w := serve(t, tenantsOperation.Handle(p.handleTenants), ctx)
	if w.Code != http.StatusOK {
		t.Fatalf("the read answered %d: %s", w.Code, w.Body.String())
	}
	body := decode(t, w)
	if len(body.Tenants) != 2 {
		t.Fatalf("tenants = %v, want both active memberships", body.Tenants)
	}
	if body.Tenants[0].Name != "Acme" {
		t.Errorf("the first tenant is %+v, want Acme", body.Tenants[0])
	}
	if body.Tenants[1].Name != "Globex" {
		t.Errorf("the second tenant is %+v, want Globex", body.Tenants[1])
	}
}

func TestTheReadCarriesTheOrganizationDisplayName(t *testing.T) {
	memberships := &stubMemberships{byAccount: map[authn.AccountID][]authn.Membership{
		"acct-bob": {
			{Account: "acct-bob", Tenant: "acme", Active: true},
			{Account: "acct-bob", Tenant: "globex", Active: true},
		},
	}}
	catalog := &stubCatalog{rows: map[tenant.ID]tenant.Tenant{
		"acme": {
			ID:      "acme",
			Name:    "acme",
			Status:  tenant.StatusActive,
			Profile: tenant.Profile{DisplayName: "Acme Corporation"},
		},
		"globex": activeTenant("globex", "Globex"),
	}}
	p := newRig(memberships, catalog)
	ctx := authntest.Context(t, t.Context(), authntest.WithAccount("acct-bob"))

	w := serve(t, tenantsOperation.Handle(p.handleTenants), ctx)
	if w.Code != http.StatusOK {
		t.Fatalf("the read answered %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Tenants []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"tenants"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the answer %q: %v", w.Body.String(), err)
	}
	if len(body.Tenants) != 2 {
		t.Fatalf("tenants = %+v, want both", body.Tenants)
	}
	if body.Tenants[0].DisplayName != "Acme Corporation" {
		t.Errorf("acme's display name is %q, want Acme Corporation", body.Tenants[0].DisplayName)
	}
	if body.Tenants[1].DisplayName != "" {
		t.Errorf("globex's display name is %q, want empty", body.Tenants[1].DisplayName)
	}
}

func TestTheKindComesFromTheResolver(t *testing.T) {
	memberships := &stubMemberships{byAccount: map[authn.AccountID][]authn.Membership{
		"acct-bob": {
			{Account: "acct-bob", Tenant: "acme", Active: true},
			{Account: "acct-bob", Tenant: "personal-acct-bob", Active: true},
		},
	}}
	catalog := &stubCatalog{rows: map[tenant.ID]tenant.Tenant{
		"acme":              activeTenant("acme", "Acme"),
		"personal-acct-bob": activeTenant("personal-acct-bob", "bob"),
	}}
	kind := &stubKindResolver{kinds: map[tenant.ID]tenant.Kind{
		"acme":              tenant.KindBusiness,
		"personal-acct-bob": tenant.KindPersonal,
	}}
	p := newRigWithKind(memberships, catalog, kind)
	ctx := authntest.Context(t, t.Context(), authntest.WithAccount("acct-bob"))

	body := decode(t, serve(t, tenantsOperation.Handle(p.handleTenants), ctx))
	if len(body.Tenants) != 2 {
		t.Fatalf("tenants = %v, want both", body.Tenants)
	}
	if body.Tenants[0].Kind != string(tenant.KindBusiness) {
		t.Errorf("acme's kind is %q, want business", body.Tenants[0].Kind)
	}
	if body.Tenants[1].Kind != string(tenant.KindPersonal) {
		t.Errorf("the personal tenant's kind is %q, want personal", body.Tenants[1].Kind)
	}
	if len(kind.asked) != 2 {
		t.Errorf("the resolver was asked %v, want once per listed tenant", kind.asked)
	}
}

func TestNoResolverReadsEveryTenantAsBusiness(t *testing.T) {
	memberships := &stubMemberships{byAccount: map[authn.AccountID][]authn.Membership{
		"acct-bob": {{Account: "acct-bob", Tenant: "acme", Active: true}},
	}}
	catalog := &stubCatalog{rows: map[tenant.ID]tenant.Tenant{
		"acme": activeTenant("acme", "Acme"),
	}}
	p := newRig(memberships, catalog)
	ctx := authntest.Context(t, t.Context(), authntest.WithAccount("acct-bob"))

	body := decode(t, serve(t, tenantsOperation.Handle(p.handleTenants), ctx))
	if len(body.Tenants) != 1 || body.Tenants[0].Kind != string(tenant.KindBusiness) {
		t.Errorf("tenants = %+v, want one row read as business", body.Tenants)
	}
}

func TestAKindReadFailureIsA503(t *testing.T) {
	memberships := &stubMemberships{byAccount: map[authn.AccountID][]authn.Membership{
		"acct-bob": {{Account: "acct-bob", Tenant: "acme", Active: true}},
	}}
	catalog := &stubCatalog{rows: map[tenant.ID]tenant.Tenant{
		"acme": activeTenant("acme", "Acme"),
	}}
	kind := &stubKindResolver{err: errors.New("the kind store is down")}
	p := newRigWithKind(memberships, catalog, kind)
	ctx := authntest.Context(t, t.Context(), authntest.WithAccount("acct-bob"))

	w := serve(t, tenantsOperation.Handle(p.handleTenants), ctx)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("an unreadable kind answered %d: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); strings.Contains(body, "the kind store is down") {
		t.Errorf("the answer carries the cause: %s", body)
	}
}

func TestTheReadKeepsTheMembershipsOrder(t *testing.T) {
	memberships := &stubMemberships{byAccount: map[authn.AccountID][]authn.Membership{
		"acct-bob": {
			{Account: "acct-bob", Tenant: "zeta", Active: true},
			{Account: "acct-bob", Tenant: "alpha", Active: true},
		},
	}}
	catalog := &stubCatalog{rows: map[tenant.ID]tenant.Tenant{
		"zeta":  activeTenant("zeta", "Zeta"),
		"alpha": activeTenant("alpha", "Alpha"),
	}}
	p := newRig(memberships, catalog)
	ctx := authntest.Context(t, t.Context(), authntest.WithAccount("acct-bob"))

	body := decode(t, serve(t, tenantsOperation.Handle(p.handleTenants), ctx))
	if len(body.Tenants) != 2 || body.Tenants[0].Name != "Zeta" || body.Tenants[1].Name != "Alpha" {
		t.Errorf("tenants = %v, want the membership read's order zeta then alpha", body.Tenants)
	}
}

func TestASuspendedMembershipIsLeftOut(t *testing.T) {
	memberships := &stubMemberships{byAccount: map[authn.AccountID][]authn.Membership{
		"acct-bob": {
			{Account: "acct-bob", Tenant: "acme", Active: true},
			{Account: "acct-bob", Tenant: "globex", Active: false},
		},
	}}
	catalog := &stubCatalog{rows: map[tenant.ID]tenant.Tenant{
		"acme":   activeTenant("acme", "Acme"),
		"globex": activeTenant("globex", "Globex"),
	}}
	p := newRig(memberships, catalog)
	ctx := authntest.Context(t, t.Context(), authntest.WithAccount("acct-bob"))

	body := decode(t, serve(t, tenantsOperation.Handle(p.handleTenants), ctx))
	if len(body.Tenants) != 1 || body.Tenants[0].Name != "Acme" {
		t.Errorf("tenants = %v, want only acme", body.Tenants)
	}
	for _, id := range catalog.asked {
		if id == "globex" {
			t.Errorf("the roster was asked about the suspended membership's tenant %q", id)
		}
	}
}

func TestATenantThatIsNotServedIsLeftOut(t *testing.T) {
	memberships := &stubMemberships{byAccount: map[authn.AccountID][]authn.Membership{
		"acct-bob": {
			{Account: "acct-bob", Tenant: "acme", Active: true},
			{Account: "acct-bob", Tenant: "globex", Active: true},
		},
	}}
	catalog := &stubCatalog{rows: map[tenant.ID]tenant.Tenant{
		"acme":   activeTenant("acme", "Acme"),
		"globex": {ID: "globex", Name: "Globex", Status: tenant.StatusDisabled},
	}}
	p := newRig(memberships, catalog)
	ctx := authntest.Context(t, t.Context(), authntest.WithAccount("acct-bob"))

	body := decode(t, serve(t, tenantsOperation.Handle(p.handleTenants), ctx))
	if len(body.Tenants) != 1 || body.Tenants[0].Name != "Acme" {
		t.Errorf("tenants = %v, want only the served tenant acme", body.Tenants)
	}
}

func TestAMembershipWithoutARosterRowIsLeftOut(t *testing.T) {
	memberships := &stubMemberships{byAccount: map[authn.AccountID][]authn.Membership{
		"acct-bob": {
			{Account: "acct-bob", Tenant: "acme", Active: true},
			{Account: "acct-bob", Tenant: "globex", Active: true},
		},
	}}
	catalog := &stubCatalog{rows: map[tenant.ID]tenant.Tenant{
		"acme": activeTenant("acme", "Acme"),
	}}
	p := newRig(memberships, catalog)
	ctx := authntest.Context(t, t.Context(), authntest.WithAccount("acct-bob"))

	body := decode(t, serve(t, tenantsOperation.Handle(p.handleTenants), ctx))
	if len(body.Tenants) != 1 || body.Tenants[0].Name != "Acme" {
		t.Errorf("tenants = %v, want only the roster's tenant acme", body.Tenants)
	}
}

func TestTheEmptyAnswerIsAnEmptyList(t *testing.T) {
	p := newRig(&stubMemberships{}, &stubCatalog{})
	ctx := authntest.Context(t, t.Context(), authntest.WithAccount("acct-bob"))

	w := serve(t, tenantsOperation.Handle(p.handleTenants), ctx)
	if w.Code != http.StatusOK {
		t.Fatalf("the read answered %d: %s", w.Code, w.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decoding the answer %q: %v", w.Body.String(), err)
	}
	if got := string(raw["tenants"]); got != "[]" {
		t.Errorf("tenants is %s, want an empty list", got)
	}
}

func TestAnonymousIsRefusedWithTheGatewayChallenge(t *testing.T) {
	memberships := &stubMemberships{}
	p := newRig(memberships, &stubCatalog{})
	ctx := authntest.Anonymous(t, t.Context(), authn.AccessOptional)

	w := serve(t, mounted(p), ctx)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("an anonymous request answered %d, want 401", w.Code)
	}
	if got, want := w.Header().Get("WWW-Authenticate"), `Bearer realm="account", Cookie realm="account"`; got != want {
		t.Errorf("the challenge is %q, want %q", got, want)
	}
	if len(memberships.asked) != 0 {
		t.Errorf("an anonymous request asked for memberships %v, want nothing", memberships.asked)
	}
}

func TestAMembershipReadFailureIsA503(t *testing.T) {
	p := newRig(&stubMemberships{err: errors.New("the store is down")}, &stubCatalog{})
	ctx := authntest.Context(t, t.Context(), authntest.WithAccount("acct-bob"))

	w := serve(t, tenantsOperation.Handle(p.handleTenants), ctx)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("an unreadable membership table answered %d: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); strings.Contains(body, "the store is down") {
		t.Errorf("the answer carries the cause: %s", body)
	}
}

func TestARosterReadFailureIsA503(t *testing.T) {
	memberships := &stubMemberships{byAccount: map[authn.AccountID][]authn.Membership{
		"acct-bob": {{Account: "acct-bob", Tenant: "acme", Active: true}},
	}}
	p := newRig(memberships, &stubCatalog{err: errors.New("the roster is down")})
	ctx := authntest.Context(t, t.Context(), authntest.WithAccount("acct-bob"))

	w := serve(t, tenantsOperation.Handle(p.handleTenants), ctx)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("an unreadable roster answered %d: %s", w.Code, w.Body.String())
	}
}

func TestAMissingRosterIsA503(t *testing.T) {
	memberships := &stubMemberships{byAccount: map[authn.AccountID][]authn.Membership{
		"acct-bob": {{Account: "acct-bob", Tenant: "acme", Active: true}},
	}}
	p := newRig(memberships, &stubCatalog{err: tenant.ErrRosterMissing})
	ctx := authntest.Context(t, t.Context(), authntest.WithAccount("acct-bob"))

	w := serve(t, tenantsOperation.Handle(p.handleTenants), ctx)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a missing roster answered %d: %s", w.Code, w.Body.String())
	}
}

func TestAnImpersonatingCallerReadsOnlyTheGrantTenant(t *testing.T) {
	memberships := &stubMemberships{byAccount: map[authn.AccountID][]authn.Membership{
		"acct-bob": {
			{Account: "acct-bob", Tenant: "acme", Active: true},
			{Account: "acct-bob", Tenant: "globex", Active: true},
		},
	}}
	catalog := &stubCatalog{rows: map[tenant.ID]tenant.Tenant{
		"acme":   activeTenant("acme", "Acme"),
		"globex": activeTenant("globex", "Globex"),
	}}
	p := newRig(memberships, catalog)
	ctx := authntest.Context(t, t.Context(),
		authntest.WithAccount("acct-bob"),
		authntest.WithActor("acct-alice"),
		authntest.WithSession("0190a6f2-3b6c-7c2e-8a4d-5e6f7a8b9c0d"),
		authntest.WithScope("acme"),
	)

	w := serve(t, tenantsOperation.Handle(p.handleTenants), ctx)
	if w.Code != http.StatusOK {
		t.Fatalf("the read answered %d: %s", w.Code, w.Body.String())
	}
	body := decode(t, w)
	if len(body.Tenants) != 1 {
		t.Fatalf("tenants = %+v, want only the grant tenant", body.Tenants)
	}
	if body.Tenants[0].Name != "Acme" {
		t.Errorf("the tenant is %+v, want acme/Acme", body.Tenants[0])
	}
}

func TestTheReadAsksForTheEffectiveAccount(t *testing.T) {
	memberships := &stubMemberships{}
	p := newRig(memberships, &stubCatalog{})
	ctx := authntest.Context(t, t.Context(),
		authntest.WithAccount("acct-bob"),
		authntest.WithActor("acct-alice"),
		authntest.WithSession("0190a6f2-3b6c-7c2e-8a4d-5e6f7a8b9c0d"),
		authntest.WithScope("acme"),
	)

	if w := serve(t, tenantsOperation.Handle(p.handleTenants), ctx); w.Code != http.StatusOK {
		t.Fatalf("the read answered %d: %s", w.Code, w.Body.String())
	}
	if len(memberships.asked) != 1 || memberships.asked[0] != "acct-bob" {
		t.Errorf("the read asked about %v, want the effective account acct-bob", memberships.asked)
	}
}
