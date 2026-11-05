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

// stubMemberships is the membership read keyed by account, recording every
// account a case asked about. An account it holds no entry for has none.
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

// stubCatalog is the roster read keyed by identifier, recording every lookup.
// A row the map does not carry is absent, as the roster reports one.
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

// stubKindResolver is the host's kind answer keyed by identifier, recording
// every lookup. An identifier the map does not carry reads as the zero kind.
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

// newRig builds the module over the stub reads, with one endpoint configured.
func newRig(memberships authn.Memberships, catalog tenant.Catalog) *product {
	return &product{
		memberships: memberships,
		catalog:     catalog,
		logger:      log.Default(),
		cfg:         resolved{endpoints: []string{"account"}},
	}
}

// newRigWithKind is newRig over a host kind resolver.
func newRigWithKind(memberships authn.Memberships, catalog tenant.Catalog, kind tenant.KindResolver) *product {
	p := newRig(memberships, catalog)
	p.kind = kind
	return p
}

// serve calls one handler over a real *gin.Context carrying ctx.
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

// mounted is the handler the route is mounted with: the identity check in front
// of the declared handler, bound to the endpoint's realm.
func mounted(p *product) gin.HandlerFunc {
	return withRealm("account", p.requireIdentity(tenantsOperation.Handle(p.handleTenants)))
}

// decode reads the 200 body.
func decode(t *testing.T, w *httptest.ResponseRecorder) tenantsBody {
	t.Helper()
	var body tenantsBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the answer %q: %v", w.Body.String(), err)
	}
	return body
}

// activeTenant is one roster row in the state the read lists.
func activeTenant(id, alias string) tenant.Tenant {
	return tenant.Tenant{ID: tenant.ID(id), Alias: alias, Status: tenant.StatusActive}
}

// TestTheReadListsActiveMembershipsOfActiveTenants holds the answer: every
// membership that is active and whose roster row is active, carrying the
// identifier and the alias and nothing else.
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
	if body.Tenants[0].ID != "acme" || body.Tenants[0].Alias != "Acme" {
		t.Errorf("the first tenant is %+v, want acme/Acme", body.Tenants[0])
	}
	if body.Tenants[1].ID != "globex" || body.Tenants[1].Alias != "Globex" {
		t.Errorf("the second tenant is %+v, want globex/Globex", body.Tenants[1])
	}
}

// TestTheReadCarriesTheOrganizationDisplayName holds the host's display-name
// source: the entry carries the roster's display name beside the alias, and a
// tenant whose profile names none carries an empty one rather than omitting the
// field, so a host can fall back to the alias without a missing-property branch.
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
			Alias:   "acme",
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
			DisplayName string `json:"displayName"`
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

// TestTheKindComesFromTheResolver holds the host seam: each row's kind is the
// resolver's answer for that tenant, passed through unchanged, and the resolver
// is asked once per listed tenant.
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

// TestNoResolverReadsEveryTenantAsBusiness holds the assembly with no
// personal-tenant policy: no resolver means every row is business, not an
// error and not an absent field.
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

// TestAKindReadFailureIsA503 holds a resolver outage apart from a business
// answer: a failure is a 503, never a row silently read as business.
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

// TestTheReadKeepsTheMembershipsOrder holds the answer to the order the
// membership read gives: the interface already lists by identifier, and the
// filter preserves that order rather than sorting again. A sort here would
// answer alpha before zeta, which is the failure this case names.
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
	if len(body.Tenants) != 2 || body.Tenants[0].ID != "zeta" || body.Tenants[1].ID != "alpha" {
		t.Errorf("tenants = %v, want the membership read's order zeta then alpha", body.Tenants)
	}
}

// TestASuspendedMembershipIsLeftOut holds the membership's own flag to the
// answer: a suspended membership is not a member, and the roster is not even
// asked about it.
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
	if len(body.Tenants) != 1 || body.Tenants[0].ID != "acme" {
		t.Errorf("tenants = %v, want only acme", body.Tenants)
	}
	for _, id := range catalog.asked {
		if id == "globex" {
			t.Errorf("the roster was asked about the suspended membership's tenant %q", id)
		}
	}
}

// TestATenantThatIsNotServedIsLeftOut holds the roster's state to the answer: a
// disabled tenant is not served, so a member is not given an entrance to it.
func TestATenantThatIsNotServedIsLeftOut(t *testing.T) {
	memberships := &stubMemberships{byAccount: map[authn.AccountID][]authn.Membership{
		"acct-bob": {
			{Account: "acct-bob", Tenant: "acme", Active: true},
			{Account: "acct-bob", Tenant: "globex", Active: true},
		},
	}}
	catalog := &stubCatalog{rows: map[tenant.ID]tenant.Tenant{
		"acme":   activeTenant("acme", "Acme"),
		"globex": {ID: "globex", Alias: "Globex", Status: tenant.StatusDisabled},
	}}
	p := newRig(memberships, catalog)
	ctx := authntest.Context(t, t.Context(), authntest.WithAccount("acct-bob"))

	body := decode(t, serve(t, tenantsOperation.Handle(p.handleTenants), ctx))
	if len(body.Tenants) != 1 || body.Tenants[0].ID != "acme" {
		t.Errorf("tenants = %v, want only the served tenant acme", body.Tenants)
	}
}

// TestAMembershipWithoutARosterRowIsLeftOut holds the snapshot's absence to the
// answer: a membership naming a tenant this replica's roster does not carry
// would switch into an absent tenant, so it is left out.
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
	if len(body.Tenants) != 1 || body.Tenants[0].ID != "acme" {
		t.Errorf("tenants = %v, want only the roster's tenant acme", body.Tenants)
	}
}

// TestTheEmptyAnswerIsAnEmptyList holds the empty result's spelling: an account
// with no active membership is an answer of an empty list, never null.
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

// TestAnonymousIsRefusedWithTheGatewayChallenge holds the no-identity fallback:
// the mounted route answers the same 401 the authenticated surfaces answer, and
// nothing is read.
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

// TestAMembershipReadFailureIsA503 holds an outage apart from an empty list: a
// membership read that cannot answer is a 503, and the cause never reaches the
// body.
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

// TestARosterReadFailureIsA503 holds the second dependency: a roster that cannot
// answer is a 503, not a list missing the tenant it could not resolve.
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

// TestAMissingRosterIsA503 holds the snapshot that has not been published: a
// roster refusing its read with ErrRosterMissing is an outage of the same kind,
// never an empty list.
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

// TestAnImpersonatingCallerReadsOnlyTheGrantTenant holds the narrowed answer
// an impersonating caller receives: the host needs the grant tenant's alias
// after a full page load, when no roster cache survives, and the impersonated
// account's memberships in other tenants are outside the grant and are not
// handed over.
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
	if body.Tenants[0].ID != "acme" || body.Tenants[0].Alias != "Acme" {
		t.Errorf("the tenant is %+v, want acme/Acme", body.Tenants[0])
	}
}

// TestTheReadAsksForTheEffectiveAccount holds an impersonating caller to the
// account the request is taken to be from: the read asks about the impersonated
// account, never the actor whose hands are on the request.
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
