package main

// The provisioning seam's cases: every write reaches the transaction handle the
// account module handed in, and the facts become the personal tenant the policy
// describes. The fakes record the handle and panic on a call a case does not
// expect, the same way the personal policy's other stand-ins do.

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/provisioning"
	"github.com/vislake/speed/pkg/authz"
	authzpolicy "github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/tenant"
	tenantcatalog "github.com/vislake/speed/pkg/tenant/catalog"
)

// recordingTxCatalog records the transaction handle and the tenant rows a case
// writes through it.
type recordingTxCatalog struct {
	tenantcatalog.TxCatalog

	tx      *gorm.DB
	created []tenant.Tenant
	deleted []tenant.ID
}

func (c *recordingTxCatalog) CreateTx(_ context.Context, tx *gorm.DB, t tenant.Tenant) (func(context.Context) error, error) {
	c.tx = tx
	c.created = append(c.created, t)
	return func(context.Context) error { return nil }, nil
}

func (c *recordingTxCatalog) DeleteTx(_ context.Context, tx *gorm.DB, id tenant.ID) (func(context.Context) error, error) {
	c.tx = tx
	c.deleted = append(c.deleted, id)
	return func(context.Context) error { return nil }, nil
}

// recordingTxManager records the transaction handle and every policy write.
type recordingTxManager struct {
	authzpolicy.TxManager

	tx      *gorm.DB
	roles   []authz.RoleRecord
	grants  []authz.Grant
	binding []authz.Binding
	cleared []string
	removed []authz.Role
}

func (m *recordingTxManager) PutRoleTx(_ context.Context, tx *gorm.DB, r authz.RoleRecord) (func(context.Context) error, error) {
	m.tx = tx
	m.roles = append(m.roles, r)
	return func(context.Context) error { return nil }, nil
}

func (m *recordingTxManager) DeleteRoleTx(_ context.Context, tx *gorm.DB, _ string, role authz.Role) (func(context.Context) error, error) {
	m.tx = tx
	m.removed = append(m.removed, role)
	return func(context.Context) error { return nil }, nil
}

func (m *recordingTxManager) SeedGrantsTx(_ context.Context, tx *gorm.DB, _ string, _ authz.Role,
	grants []authz.Grant,
) (int, func(context.Context) error, error) {
	m.tx = tx
	m.grants = append(m.grants, grants...)
	return len(grants), func(context.Context) error { return nil }, nil
}

func (m *recordingTxManager) PutBindingTx(_ context.Context, tx *gorm.DB, b authz.Binding) (func(context.Context) error, error) {
	m.tx = tx
	m.binding = append(m.binding, b)
	return func(context.Context) error { return nil }, nil
}

func (m *recordingTxManager) ClearAccountBindingsTx(_ context.Context, tx *gorm.DB, _, account string) (func(context.Context) error, error) {
	m.tx = tx
	m.cleared = append(m.cleared, account)
	return func(context.Context) error { return nil }, nil
}

// recordingMembershipWriter records the transaction handle and the membership.
type recordingMembershipWriter struct {
	provisioning.MembershipWriter

	tx  *gorm.DB
	put []authn.Membership
}

func (w *recordingMembershipWriter) PutMembershipTx(_ context.Context, tx *gorm.DB,
	m authn.Membership,
) (func(context.Context) error, error) {
	w.tx = tx
	w.put = append(w.put, m)
	return func(context.Context) error { return nil }, nil
}

// A user account's provisioning writes the tenant row, the membership, the
// role, its grants and the binding through the account's transaction, and the
// facts become the row the policy names.
func TestProvisionAccountSharesTheAccountTransaction(t *testing.T) {
	tx := &gorm.DB{}
	roster, manager, writer := &recordingTxCatalog{}, &recordingTxManager{}, &recordingMembershipWriter{}
	p := &personalProduct{
		config:           personalConfig{Enabled: true},
		txRoster:         roster,
		txManager:        manager,
		membershipWriter: writer,
		catalog:          fakeCatalog{},
	}
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
	if len(roster.created) != 1 {
		t.Fatalf("the seam wrote %d tenant row(s), want one", len(roster.created))
	}
	row := roster.created[0]
	wantID := personalTenantID(authn.AccountID(facts.ID))
	if row.ID != wantID || row.Alias != string(wantID) || row.Status != tenant.StatusActive ||
		row.OwnerAccount != facts.ID || row.Profile.DisplayName != facts.DisplayName {
		t.Errorf("the personal tenant is %+v, want id and alias %q, owner %q and display name %q",
			row, wantID, facts.ID, facts.DisplayName)
	}
	if len(writer.put) != 1 || writer.put[0].Account != authn.AccountID(facts.ID) ||
		writer.put[0].Tenant != string(wantID) || !writer.put[0].Active {
		t.Errorf("the seam wrote the membership %+v, want the account's active membership in %q", writer.put, wantID)
	}
	if len(manager.roles) != 1 || manager.roles[0].Role != personalTenantAdminRole {
		t.Errorf("the seam wrote the roles %+v, want the tenant-admin role", manager.roles)
	}
	if len(manager.binding) != 1 || manager.binding[0].Account != facts.ID ||
		manager.binding[0].Role != personalTenantAdminRole {
		t.Errorf("the seam wrote the binding %+v, want the account bound to tenant-admin", manager.binding)
	}
}

// A service account owns no organization: the seam is reached but writes
// nothing, and the facts carry no username.
func TestProvisionAccountWritesNothingForAServiceAccount(t *testing.T) {
	roster, manager, writer := &recordingTxCatalog{}, &recordingTxManager{}, &recordingMembershipWriter{}
	p := &personalProduct{
		config:           personalConfig{Enabled: true},
		txRoster:         roster,
		txManager:        manager,
		membershipWriter: writer,
		catalog:          fakeCatalog{},
	}
	facts := provisioning.AccountFacts{ID: "svc-1", Kind: string(authn.KindService), Status: "active"}
	if err := p.ProvisionAccount(context.Background(), &gorm.DB{}, facts); err != nil {
		t.Fatalf("provisioning a service account: %v", err)
	}
	if len(roster.created)+len(manager.roles)+len(manager.binding)+len(writer.put) != 0 {
		t.Errorf("a service account was provisioned: tenant=%v roles=%v bindings=%v memberships=%v",
			roster.created, manager.roles, manager.binding, writer.put)
	}
}

// The host's switch withdraws the personal tenant from account creation, as it
// does from the lifecycle hook; the seam then writes nothing.
func TestProvisionAccountWritesNothingWhileTheSwitchIsOff(t *testing.T) {
	roster := &recordingTxCatalog{}
	p := &personalProduct{config: personalConfig{Enabled: false}, txRoster: roster}
	facts := provisioning.AccountFacts{
		ID: "acc-1", Kind: string(authn.KindUser), Status: "active", Username: "alice",
	}
	if err := p.ProvisionAccount(context.Background(), &gorm.DB{}, facts); err != nil {
		t.Fatalf("provisioning with the switch off: %v", err)
	}
	if len(roster.created) != 0 {
		t.Errorf("the switch was off and the seam wrote %v", roster.created)
	}
}

// A deletion clears the account's policy in its personal tenant and writes the
// tombstone through the account's transaction.
func TestProvisionAccountDeletionSharesTheAccountTransaction(t *testing.T) {
	tx := &gorm.DB{}
	roster, manager := &recordingTxCatalog{}, &recordingTxManager{}
	p := &personalProduct{config: personalConfig{Enabled: true}, txRoster: roster, txManager: manager}
	facts := provisioning.AccountFacts{
		ID: "acc-1", Kind: string(authn.KindUser), Status: "active", Username: "alice",
	}
	if err := p.ProvisionAccountDeletion(context.Background(), tx, facts); err != nil {
		t.Fatalf("provisioning the deletion: %v", err)
	}
	if roster.tx != tx || manager.tx != tx {
		t.Fatalf("the deletion writes did not run on the account's transaction: roster=%p manager=%p want %p",
			roster.tx, manager.tx, tx)
	}
	if len(manager.cleared) != 1 || manager.cleared[0] != facts.ID {
		t.Errorf("the seam cleared the bindings %v, want the deleted account's", manager.cleared)
	}
	if len(manager.removed) != 1 || manager.removed[0] != personalTenantAdminRole {
		t.Errorf("the seam removed the roles %v, want the tenant-admin role", manager.removed)
	}
	wantID := personalTenantID(authn.AccountID(facts.ID))
	if len(roster.deleted) != 1 || roster.deleted[0] != wantID {
		t.Errorf("the seam tombstoned %v, want %q", roster.deleted, wantID)
	}
}
