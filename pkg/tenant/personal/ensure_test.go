package personal

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

// The backfill writes the same five pieces, and a second call changes nothing.
func TestEnsureIsIdempotent(t *testing.T) {
	p, roster, memberships, manager, _ := unitProduct()
	ctx := context.Background()

	first, err := p.ensure(ctx, unitAccount)
	if err != nil {
		t.Fatalf("the first ensure: %v", err)
	}
	if !first.Created {
		t.Errorf("the first ensure reports created=false, want true")
	}
	wantID := TenantID(unitAccount)
	if first.Tenant.ID != wantID {
		t.Errorf("the tenant is %q, want %q", first.Tenant.ID, wantID)
	}

	second, err := p.ensure(ctx, unitAccount)
	if err != nil {
		t.Fatalf("the second ensure: %v", err)
	}
	if second.Created {
		t.Errorf("the second ensure reports created=true, want false")
	}
	if roster.creates != 1 || len(memberships.rows[unitAccount]) != 1 ||
		len(manager.roles[string(wantID)]) != 1 || len(manager.bindings[string(wantID)]) != 1 {
		t.Errorf("the second ensure wrote duplicates: tenants=%d memberships=%v roles=%v bindings=%v",
			roster.creates, memberships.rows[unitAccount],
			manager.roles[string(wantID)], manager.bindings[string(wantID)])
	}
}

// The backfill names the tenant's alias after the account's username.
func TestEnsureNamesTheAliasAfterTheUsername(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	p.accounts = &fakeAccounts{identifiers: []authn.Identifier{
		{Kind: authn.IdentifierEmail, Value: "alice@example.com"},
		{Kind: authn.IdentifierUsername, Value: "alice"},
	}}
	if _, err := p.ensure(context.Background(), unitAccount); err != nil {
		t.Fatalf("the ensure: %v", err)
	}
	row := roster.rows[TenantID(unitAccount)]
	if row.Alias != "alice" {
		t.Errorf("the alias is %q, want the account's username", row.Alias)
	}
}

// Without the account read surface the alias falls back to the derived
// identifier.
func TestEnsureFallsBackToTheDerivedAliasWithoutAnAccountRead(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	if _, err := p.ensure(context.Background(), unitAccount); err != nil {
		t.Fatalf("the ensure: %v", err)
	}
	id := TenantID(unitAccount)
	if row := roster.rows[id]; row.Alias != string(id) {
		t.Errorf("the alias is %q, want the derived identifier %q", row.Alias, id)
	}
}

// An alias another tenant holds is the identifier-unavailable refusal.
func TestEnsureReportsAHeldAlias(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	p.accounts = &fakeAccounts{identifiers: []authn.Identifier{{Kind: authn.IdentifierUsername, Value: "alice"}}}
	roster.createErr = tenant.ErrAliasConflict
	_, err := p.ensure(context.Background(), unitAccount)
	if !errors.Is(err, ErrIdentifierUnavailable) {
		t.Fatalf("the ensure gave %v, want ErrIdentifierUnavailable", err)
	}
}

// A disabled tenant is refused without a write.
func TestEnsureRefusesADisabledTenant(t *testing.T) {
	p, roster, memberships, manager, _ := unitProduct()
	id := TenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusDisabled}
	memberships.rows[unitAccount] = []authn.Membership{{Account: unitAccount, Tenant: string(id), Active: true}}
	before := len(manager.bindings[string(id)])
	_, err := p.ensure(context.Background(), unitAccount)
	if !errors.Is(err, ErrTenantUnavailable) {
		t.Fatalf("the ensure gave %v, want ErrTenantUnavailable", err)
	}
	if len(manager.bindings[string(id)]) != before {
		t.Errorf("the refused ensure wrote a binding")
	}
}

// A create this call lost to another call leaves a failed audit row beside the
// winner's tenant.
func TestEnsureClosesALostRaceWithAFailedAuditRow(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	id := TenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusActive}
	roster.creates = 0
	roster.createErr = tenant.ErrTenantExists
	result, err := p.ensure(context.Background(), unitAccount)
	if err != nil {
		t.Fatalf("the ensure: %v", err)
	}
	if result.Created {
		t.Errorf("the lost race reports created=true")
	}
	rows := entriesOfAction(p.audit.(*fakeAudit).rows(), personalCreateAction)
	if len(rows) != 2 || rows[0].Outcome != personalOutcomeSuccess || rows[1].Outcome != personalOutcomeFailed {
		t.Errorf("the create audit rows are %+v, want a success and a linked failure", rows)
	}
}

// The kind resolver names only the caller's own space.
func TestKindOfNamesOnlyTheCallersOwnSpace(t *testing.T) {
	p := &product{}
	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
	if kind, err := p.KindOf(ctx, TenantID(unitAccount)); err != nil || kind != tenant.KindPersonal {
		t.Errorf("KindOf(the caller's space) = (%v, %v), want personal", kind, err)
	}
	for _, id := range []tenant.ID{TenantID("another-account"), "business"} {
		if kind, err := p.KindOf(ctx, id); err != nil || kind != tenant.KindBusiness {
			t.Errorf("KindOf(%q) = (%v, %v), want business", id, kind, err)
		}
	}
	if kind, err := p.KindOf(context.Background(), TenantID(unitAccount)); err != nil || kind != tenant.KindBusiness {
		t.Errorf("KindOf without an identity = (%v, %v), want business", kind, err)
	}
}

// The allowlist is the business set intersected with the governed set, less
// the management actions a single-person space must not have.
func TestTheAllowlistKeepsOnlyGovernedBusinessPermissions(t *testing.T) {
	kept := authz.Permission("crm.deal.read")
	excluded := authz.PermissionMembershipManage
	notGoverned := authz.Permission("crm.deal.delete")
	catalog := fakeCatalog{governed: []authz.Permission{kept, excluded}}
	got := personalAllowlist(catalog, []authz.Permission{kept, excluded, notGoverned})
	if len(got) != 1 || got[0] != kept {
		t.Errorf("the allowlist is %v, want only %q", got, kept)
	}
}

// The handler refuses a caller with no identity, a service account and a
// static-table subject before the ensure runs, and writes nothing for any of
// them.
func TestHandleEnsureRefusesCallersThatOwnNoOrganization(t *testing.T) {
	p, roster, memberships, manager, seeder := unitProduct()

	_, err := p.handleEnsure(context.Background(), struct{}{})
	if fault := faultOf(t, err); fault.Status != http.StatusUnauthorized {
		t.Errorf("an unauthenticated ensure answered %d, want 401", fault.Status)
	}

	refused := []struct {
		name string
		ctx  context.Context
	}{
		{
			name: "service account",
			ctx: authntest.Context(t, context.Background(),
				authntest.WithAccount("svc-automation"),
				authntest.WithMethods(authn.MethodClientSecret)),
		},
		{
			name: "static-table subject",
			ctx: authntest.Context(t, context.Background(),
				authntest.WithAccount("static.ops"),
				authntest.WithBackend("static")),
		},
	}
	for _, caller := range refused {
		t.Run(caller.name, func(t *testing.T) {
			_, err := p.handleEnsure(caller.ctx, struct{}{})
			if fault := faultOf(t, err); fault.Status != http.StatusForbidden {
				t.Errorf("a %s's ensure answered %d, want 403", caller.name, fault.Status)
			}
		})
	}
	if roster.creates != 0 || memberships.puts != 0 || manager.putRole != 0 ||
		manager.putBinding != 0 || seeder.seed != 0 || len(p.audit.(*fakeAudit).rows()) != 0 {
		t.Errorf("a refused ensure wrote tenant=%d membership=%d role=%d binding=%d seed=%d audit=%d, want none",
			roster.creates, memberships.puts, manager.putRole, manager.putBinding, seeder.seed,
			len(p.audit.(*fakeAudit).rows()))
	}
}

// The handler's success answer carries the tenant's identifier, alias, kind and
// whether this call registered it.
func TestHandleEnsureAnswersTheTenantAndKind(t *testing.T) {
	p, _, _, _, _ := unitProduct()
	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))

	reply, err := p.handleEnsure(ctx, struct{}{})
	if err != nil {
		t.Fatalf("the ensure: %v", err)
	}
	wantID := string(TenantID(unitAccount))
	if reply.Tenant.ID != wantID || reply.Tenant.Alias != wantID {
		t.Errorf("the answer names %q/%q, want both %q", reply.Tenant.ID, reply.Tenant.Alias, wantID)
	}
	if reply.Tenant.Kind != personalKind {
		t.Errorf("the answer's kind is %q, want %q", reply.Tenant.Kind, personalKind)
	}
	if !reply.Created {
		t.Errorf("the first ensure answered created=false, want true")
	}

	second, err := p.handleEnsure(ctx, struct{}{})
	if err != nil {
		t.Fatalf("the second ensure: %v", err)
	}
	if second.Created {
		t.Errorf("the second ensure answered created=true, want false")
	}
}

// The handler maps the disabled-tenant refusal to 409 tenant-unavailable and
// writes nothing.
func TestHandleEnsureAnswersADisabledTenant409(t *testing.T) {
	p, roster, memberships, manager, seeder := unitProduct()
	id := TenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusDisabled}
	memberships.rows[unitAccount] = []authn.Membership{
		{Account: unitAccount, Tenant: string(id), Active: true, Source: "admin"},
	}

	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
	_, err := p.handleEnsure(ctx, struct{}{})
	fault := faultOf(t, err)
	if fault.Status != http.StatusConflict || fault.Type != personalProblemPrefix+tenantUnavailableCode {
		t.Errorf("the ensure answered %d %q, want 409 %q",
			fault.Status, fault.Type, personalProblemPrefix+tenantUnavailableCode)
	}
	if memberships.puts != 0 || manager.putRole != 0 || manager.putBinding != 0 || seeder.seed != 0 {
		t.Errorf("the refused ensure wrote membership=%d role=%d binding=%d seed=%d, want none",
			memberships.puts, manager.putRole, manager.putBinding, seeder.seed)
	}
}

// The handler maps an alias another tenant holds and a derived identifier
// outside the roster's domain to 409 identifier-unavailable.
func TestHandleEnsureAnswersAnIdentifierConflict409(t *testing.T) {
	t.Run("the alias is held by another tenant", func(t *testing.T) {
		p, roster, _, _, _ := unitProduct()
		roster.createErr = tenant.ErrAliasConflict
		ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
		_, err := p.handleEnsure(ctx, struct{}{})
		fault := faultOf(t, err)
		if fault.Status != http.StatusConflict || fault.Type != personalProblemPrefix+identifierUnavailableCode {
			t.Errorf("the ensure answered %d %q, want 409 %q",
				fault.Status, fault.Type, personalProblemPrefix+identifierUnavailableCode)
		}
	})
	t.Run("the derived identifier is outside the roster's domain", func(t *testing.T) {
		p, _, _, _, _ := unitProduct()
		_, err := p.ensure(context.Background(), authn.AccountID("outside the domain"))
		if !errors.Is(err, ErrIdentifierUnavailable) {
			t.Fatalf("the ensure gave %v, want ErrIdentifierUnavailable", err)
		}
	})
}

// The handler maps a dependency failure to 503 and does not repeat the cause.
func TestHandleEnsureAnswersADependencyFailure503WithoutTheCause(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	roster.createErr = errors.New("the tenant table is unavailable")
	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))

	_, err := p.handleEnsure(ctx, struct{}{})
	fault := faultOf(t, err)
	if fault.Status != http.StatusServiceUnavailable {
		t.Fatalf("the ensure answered %d, want 503", fault.Status)
	}
	if fault.Detail != "" {
		t.Errorf("the 503 fault carries %q, want no cause", fault.Detail)
	}
}

// Concurrent ensures with the same account collide on the deterministic
// identifier: one creates the tenant and the rest read it back.
func TestEnsureCreatesOneTenantUnderConcurrentCalls(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	const callers = 8
	results := make([]personalResult, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = p.ensure(context.Background(), unitAccount)
		}(i)
	}
	wg.Wait()

	created := 0
	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("caller %d failed: %v", i, errs[i])
		}
		if results[i].Tenant.ID != results[0].Tenant.ID {
			t.Errorf("caller %d answered %q, caller 0 answered %q", i, results[i].Tenant.ID, results[0].Tenant.ID)
		}
		if results[i].Created {
			created++
		}
	}
	if created != 1 {
		t.Errorf("%d callers report created=true, want exactly one", created)
	}
	if len(roster.rows) != 1 || roster.creates != 1 {
		t.Errorf("the roster holds %d row(s) after %d create(s), want one and one", len(roster.rows), roster.creates)
	}
}

// A roster snapshot that lags the table is caught up by EnterRegistered: the
// existing row is read back and no second tenant is created.
func TestEnsureCatchesTheRosterSnapshotUp(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	id := TenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusActive}
	roster.lagging[id] = true

	result, err := p.ensure(context.Background(), unitAccount)
	if err != nil {
		t.Fatalf("the ensure over a lagging snapshot: %v", err)
	}
	if result.Created {
		t.Errorf("the ensure reports created=true, want false: the table already held the row")
	}
	if result.Tenant.ID != id {
		t.Errorf("the ensure answered %q, want %q", result.Tenant.ID, id)
	}
	if roster.entered != 1 {
		t.Errorf("the roster was caught up %d time(s), want once", roster.entered)
	}
}

// stuckLaggingRoster is a roster whose table holds the row while its snapshot
// keeps missing it: EnterRegistered cannot catch it up, which is the lag the
// ensure reports as ErrRosterLagging.
type stuckLaggingRoster struct{ *fakeRoster }

func (r stuckLaggingRoster) EnterRegistered(ctx context.Context, _ tenant.ID) (context.Context, error) {
	return ctx, nil
}

// A roster whose snapshot stays behind the table answers ErrRosterLagging, and
// the handler maps it to 503.
func TestEnsureReportsARosterThatStaysBehind(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	id := TenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusActive}
	roster.lagging[id] = true
	p.roster = stuckLaggingRoster{roster}

	if _, err := p.ensure(context.Background(), unitAccount); !errors.Is(err, ErrRosterLagging) {
		t.Fatalf("the ensure gave %v, want ErrRosterLagging", err)
	}

	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
	_, err := p.handleEnsure(ctx, struct{}{})
	if fault := faultOf(t, err); fault.Status != http.StatusServiceUnavailable {
		t.Errorf("the ensure answered %d, want 503", fault.Status)
	}
}

// The ensure converges the tenant-admin role's grants to the allowlist: every
// grant outside it is revoked. The skeleton's allowlist is empty, so all are.
func TestEnsureConvergesTheTenantAdminsGrantsToTheAllowlist(t *testing.T) {
	p, _, _, manager, seeder := unitProduct()
	ctx := context.Background()
	if _, err := p.ensure(ctx, unitAccount); err != nil {
		t.Fatalf("the first ensure: %v", err)
	}
	id := string(TenantID(unitAccount))
	outer := []authz.Grant{
		{Resource: "account", Action: "read"},
		{Resource: "object", Action: "read"},
	}
	seeder.grants.mu.Lock()
	for _, extra := range outer {
		seeder.grants.sets[id][personalTenantAdminRole][extra] = struct{}{}
	}
	seeder.grants.mu.Unlock()

	if _, err := p.ensure(ctx, unitAccount); err != nil {
		t.Fatalf("the second ensure: %v", err)
	}
	if manager.deleted != len(outer) {
		t.Errorf("the ensure revoked %d grant(s), want the %d outside the allowlist", manager.deleted, len(outer))
	}
	if held := seeder.grants.held(id, personalTenantAdminRole); len(held) != 0 {
		t.Errorf("the tenant-admin role holds %v, want the empty allowlist", held)
	}
}

// A tenant built under the old model is converged to the tenant-admin role:
// the ensure removes the legacy owner role with its grants and bindings.
func TestEnsureMigratesTheLegacyOwnerRole(t *testing.T) {
	p, roster, memberships, manager, seeder := unitProduct()
	ctx := context.Background()
	id := TenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusActive}
	memberships.rows[unitAccount] = []authn.Membership{
		{Account: unitAccount, Tenant: string(id), Active: true, Source: personalMemberSource},
	}
	legacy := authz.Role("owner")
	if err := manager.PutRole(ctx, authz.RoleRecord{Tenant: string(id), Role: legacy}); err != nil {
		t.Fatalf("writing the legacy role: %v", err)
	}
	if err := manager.PutBinding(ctx, authz.Binding{
		Tenant: string(id), Account: unitAccount.String(), Role: legacy,
	}); err != nil {
		t.Fatalf("writing the legacy binding: %v", err)
	}
	if _, err := seeder.SeedGrants(ctx, string(id), legacy, []authz.Grant{{Resource: "legacy", Action: "read"}}); err != nil {
		t.Fatalf("writing the legacy grant: %v", err)
	}

	if _, err := p.ensure(ctx, unitAccount); err != nil {
		t.Fatalf("the ensure over the legacy tenant: %v", err)
	}
	roles, err := manager.Roles(ctx, string(id))
	if err != nil {
		t.Fatalf("reading the tenant's roles: %v", err)
	}
	admin := false
	for _, role := range roles {
		if role.Role == legacy {
			t.Errorf("the legacy owner role survived the ensure: %+v", roles)
		}
		if role.Role == personalTenantAdminRole {
			admin = true
		}
	}
	if !admin {
		t.Errorf("the ensure did not write the tenant-admin role: %+v", roles)
	}
	if held := seeder.grants.held(string(id), legacy); len(held) != 0 {
		t.Errorf("the legacy role's grants survived: %v", held)
	}
	bindings, err := manager.Bindings(ctx, string(id))
	if err != nil {
		t.Fatalf("reading the tenant's bindings: %v", err)
	}
	for _, binding := range bindings {
		if binding.Role == legacy {
			t.Errorf("the legacy owner binding survived: %+v", bindings)
		}
	}
}

// faultOf answers the fault a handler returned.
func faultOf(t *testing.T, err error) speedhttp.Fault {
	t.Helper()
	var fault speedhttp.Fault
	if !errors.As(err, &fault) {
		t.Fatalf("the handler gave %v, want a fault", err)
	}
	return fault
}
