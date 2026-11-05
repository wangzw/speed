package personal

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/provisioning"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// provisioningProduct assembles the product with the transaction-aware
// stand-ins a provisioning case drives.
func provisioningProduct() (*product, *fakeTxCatalog, *fakeTxManager, *fakeMembershipWriter) {
	roster, manager, writer := &fakeTxCatalog{}, &fakeTxManager{}, &fakeMembershipWriter{}
	return &product{
		txRoster:         roster,
		txManager:        manager,
		membershipWriter: writer,
		catalog:          fakeCatalog{},
		audit:            &fakeAudit{},
	}, roster, manager, writer
}

// A user account's provisioning writes the tenant row, the membership, the
// role, its grants and the binding through the account's transaction.
func TestProvisionAccountWritesTheFivePiecesOnTheAccountTransaction(t *testing.T) {
	tx := &gorm.DB{}
	p, roster, manager, writer := provisioningProduct()
	facts := provisioning.AccountFacts{
		ID: "acc-1", Kind: string(authn.KindUser), Status: "active",
		Username: "alice", DisplayName: "Alice",
	}
	if err := p.ProvisionAccount(context.Background(), tx, facts); err != nil {
		t.Fatalf("provisioning: %v", err)
	}
	if roster.tx != tx || manager.tx != tx || writer.tx != tx {
		t.Fatalf("the writes did not all run on the account's transaction: roster=%p manager=%p writer=%p want %p",
			roster.tx, manager.tx, writer.tx, tx)
	}
	wantID := TenantID(authn.AccountID(facts.ID))
	if len(roster.created) != 1 {
		t.Fatalf("the seam wrote %d tenant row(s), want one", len(roster.created))
	}
	row := roster.created[0]
	if row.ID != wantID || row.Alias != "alice" || row.Status != tenant.StatusActive ||
		row.OwnerAccount != facts.ID || row.Profile.DisplayName != facts.DisplayName {
		t.Errorf("the personal tenant is %+v, want id %q, alias alice, owner %q and display name %q",
			row, wantID, facts.ID, facts.DisplayName)
	}
	if len(writer.put) != 1 || writer.put[0].Account != authn.AccountID(facts.ID) ||
		writer.put[0].Tenant != string(wantID) || !writer.put[0].Active ||
		writer.put[0].Source != personalMemberSource {
		t.Errorf("the seam wrote the membership %+v, want the account's active personal membership in %q",
			writer.put, wantID)
	}
	if len(manager.roles) != 1 || manager.roles[0].Role != personalTenantAdminRole ||
		manager.roles[0].DescriptionKey != personalTenantAdminDescriptionKey {
		t.Errorf("the seam wrote the roles %+v, want the tenant-admin role", manager.roles)
	}
	if len(manager.binding) != 1 || manager.binding[0].Account != facts.ID ||
		manager.binding[0].Role != personalTenantAdminRole {
		t.Errorf("the seam wrote the binding %+v, want the account bound to tenant-admin", manager.binding)
	}
}

// A user account without a username — the defensive case the seam tolerates —
// gets the derived identifier as its alias.
func TestProvisionAccountFallsBackToTheDerivedAliasWithoutAUsername(t *testing.T) {
	p, roster, _, _ := provisioningProduct()
	facts := provisioning.AccountFacts{ID: "acc-1", Kind: string(authn.KindUser), Status: "active"}
	if err := p.ProvisionAccount(context.Background(), &gorm.DB{}, facts); err != nil {
		t.Fatalf("provisioning: %v", err)
	}
	wantID := TenantID(authn.AccountID(facts.ID))
	if len(roster.created) != 1 || roster.created[0].Alias != string(wantID) {
		t.Errorf("the tenant rows are %+v, want the alias %q", roster.created, wantID)
	}
}

// A service account owns no organization: the seam is reached but writes
// nothing.
func TestProvisionAccountWritesNothingForAServiceAccount(t *testing.T) {
	p, roster, manager, writer := provisioningProduct()
	facts := provisioning.AccountFacts{ID: "svc-1", Kind: string(authn.KindService), Status: "active"}
	if err := p.ProvisionAccount(context.Background(), &gorm.DB{}, facts); err != nil {
		t.Fatalf("provisioning a service account: %v", err)
	}
	if len(roster.created)+len(manager.roles)+len(manager.binding)+len(writer.put) != 0 {
		t.Errorf("a service account was provisioned: tenant=%v roles=%v bindings=%v memberships=%v",
			roster.created, manager.roles, manager.binding, writer.put)
	}
}

// A tenant the roster already holds is the idempotent replay: the remaining
// steps still run, so a failure part-way through is completed on the next call.
func TestProvisionAccountReplaysOnAnExistingTenant(t *testing.T) {
	p, roster, manager, writer := provisioningProduct()
	roster.createErr = tenant.ErrTenantExists
	facts := provisioning.AccountFacts{
		ID: "acc-1", Kind: string(authn.KindUser), Status: "active", Username: "alice",
	}
	if err := p.ProvisionAccount(context.Background(), &gorm.DB{}, facts); err != nil {
		t.Fatalf("replaying provisioning: %v", err)
	}
	if len(writer.put) != 1 || len(manager.roles) != 1 || len(manager.binding) != 1 {
		t.Errorf("the replay wrote membership=%v roles=%v binding=%v, want all three steps",
			writer.put, manager.roles, manager.binding)
	}
}

// An alias another tenant holds is this module's exported refusal, and it wraps
// the roster's own error so a caller may match either.
func TestProvisionAccountAliasConflictReturnsTheSentinel(t *testing.T) {
	p, roster, manager, writer := provisioningProduct()
	roster.createErr = tenant.ErrAliasConflict
	facts := provisioning.AccountFacts{
		ID: "acc-1", Kind: string(authn.KindUser), Status: "active", Username: "alice",
	}
	err := p.ProvisionAccount(context.Background(), &gorm.DB{}, facts)
	if !errors.Is(err, ErrAliasConflict) {
		t.Fatalf("provisioning under a held alias gave %v, want ErrAliasConflict", err)
	}
	if !errors.Is(err, tenant.ErrAliasConflict) {
		t.Errorf("the refusal does not wrap the roster's error: %v", err)
	}
	if len(writer.put)+len(manager.roles)+len(manager.binding) != 0 {
		t.Errorf("the refused provisioning wrote past the tenant row")
	}
}

// Any other tenant-row failure aborts before the remaining steps.
func TestProvisionAccountStopsOnAnyOtherFailure(t *testing.T) {
	p, roster, manager, writer := provisioningProduct()
	roster.createErr = errors.New("the roster is unavailable")
	facts := provisioning.AccountFacts{
		ID: "acc-1", Kind: string(authn.KindUser), Status: "active", Username: "alice",
	}
	if err := p.ProvisionAccount(context.Background(), &gorm.DB{}, facts); err == nil {
		t.Fatal("provisioning with a failing roster succeeded")
	}
	if len(writer.put)+len(manager.roles)+len(manager.binding) != 0 {
		t.Errorf("the failed provisioning continued past the tenant row")
	}
}

// Every transaction-aware write hands its after-commit work to the collector on
// the context the account module supplied. The collector runs all five.
func TestProvisionAccountRegistersEveryAfterCommitClosure(t *testing.T) {
	p, roster, manager, writer := provisioningProduct()
	ctx, after := provisioning.WithAfterCommit(context.Background())
	facts := provisioning.AccountFacts{
		ID: "acc-1", Kind: string(authn.KindUser), Status: "active", Username: "alice",
	}
	if err := p.ProvisionAccount(ctx, &gorm.DB{}, facts); err != nil {
		t.Fatalf("provisioning: %v", err)
	}
	if err := after.Run(context.Background()); err != nil {
		t.Fatalf("running the after-commit work: %v", err)
	}
	if got := len(roster.after); got != 1 {
		t.Errorf("the tenant row registered %d closure(s), want one", got)
	}
	if got := len(writer.after); got != 1 {
		t.Errorf("the membership registered %d closure(s), want one", got)
	}
	if got := len(manager.after); got != 3 {
		t.Errorf("the policy writes registered %d closure(s), want the role, the grants and the binding", got)
	}
}

// A deletion clears the account's policy and writes the tombstone through the
// account's transaction.
func TestProvisionAccountDeletionWritesThroughTheAccountTransaction(t *testing.T) {
	tx := &gorm.DB{}
	p, roster, manager, _ := provisioningProduct()
	facts := provisioning.AccountFacts{
		ID: "acc-1", Kind: string(authn.KindUser), Status: "active", Username: "alice",
	}
	ctx, after := provisioning.WithAfterCommit(context.Background())
	if err := p.ProvisionAccountDeletion(ctx, tx, facts); err != nil {
		t.Fatalf("provisioning the deletion: %v", err)
	}
	if roster.tx != tx || manager.tx != tx {
		t.Fatalf("the deletion did not run on the account's transaction")
	}
	wantID := TenantID(authn.AccountID(facts.ID))
	if len(manager.cleared) != 1 || manager.cleared[0] != facts.ID {
		t.Errorf("the deletion cleared %v, want the account's bindings", manager.cleared)
	}
	if len(manager.removed) != 1 || manager.removed[0] != personalTenantAdminRole {
		t.Errorf("the deletion removed %v, want the tenant-admin role", manager.removed)
	}
	if len(roster.deleted) != 1 || roster.deleted[0] != wantID {
		t.Errorf("the deletion tombstoned %v, want %q", roster.deleted, wantID)
	}
	if err := after.Run(context.Background()); err != nil {
		t.Fatalf("running the after-commit work: %v", err)
	}
	if len(manager.after) != 2 || len(roster.after) != 1 {
		t.Errorf("the deletion registered %d policy and %d roster closure(s), want 2 and 1",
			len(manager.after), len(roster.after))
	}
	rows := entriesOfAction(p.audit.(*fakeAudit).rows(), personalDeleteAction)
	if len(rows) != 1 || rows[0].Object != string(wantID) {
		t.Errorf("the deletion wrote the audit rows %+v, want one row on %q", rows, wantID)
	}
}

// A builtin tenant-admin role and an absent tenant row are both tolerated: the
// tombstone makes the first harmless, and the second is no work.
func TestProvisionAccountDeletionToleratesABuiltinRoleAndAMissingRow(t *testing.T) {
	p, roster, manager, _ := provisioningProduct()
	manager.deleteRoleErr = authz.ErrBuiltinRole
	roster.deleteErr = tenant.ErrInvalidTenant
	facts := provisioning.AccountFacts{
		ID: "acc-1", Kind: string(authn.KindUser), Status: "active", Username: "alice",
	}
	if err := p.ProvisionAccountDeletion(context.Background(), &gorm.DB{}, facts); err != nil {
		t.Fatalf("deleting with a builtin role and no row: %v", err)
	}
}

// A service account owns no organization, so its deletion writes nothing.
func TestProvisionAccountDeletionWritesNothingForAServiceAccount(t *testing.T) {
	p, roster, manager, _ := provisioningProduct()
	facts := provisioning.AccountFacts{ID: "svc-1", Kind: string(authn.KindService), Status: "active"}
	ctx, after := provisioning.WithAfterCommit(context.Background())
	if err := p.ProvisionAccountDeletion(ctx, &gorm.DB{}, facts); err != nil {
		t.Fatalf("deleting a service account: %v", err)
	}
	if len(roster.deleted)+len(manager.cleared)+len(manager.removed) != 0 {
		t.Errorf("a service account's deletion wrote something")
	}
	if err := after.Run(context.Background()); err != nil {
		t.Fatalf("running the after-commit work: %v", err)
	}
}
