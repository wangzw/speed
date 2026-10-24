package main

// The personal tenant's account-lifecycle cases: the two status hooks, the
// four-step deletion, and the create/delete audit rows. They run over the
// stand-ins in personal_test.go, with a step log shared across the seams so a
// case can pin the order the writes happened in.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// The audit vocabulary the design fixes. The literals are written out rather
// than taken from the constants the code uses, because a changed value is a
// changed contract.
const (
	createAuditAction = "workspace.personal.tenant.create"
	deleteAuditAction = "workspace.personal.tenant.delete"
	outcomeSuccess    = "success"
	outcomeFailed     = "failed"
)

// auditorOf narrows the product's audit seam to the recording stand-in.
func auditorOf(t *testing.T, product *personalProduct) *recordedAudit {
	t.Helper()
	auditor, ok := product.audit.(*recordedAudit)
	if !ok {
		t.Fatalf("the product's audit seam is %T, want the recording stand-in", product.audit)
	}
	return auditor
}

// seededPersonal assembles a product and gives the account its personal tenant,
// so a lifecycle case starts from the state the ensure leaves: one active
// tenant, one membership, one owner role and one binding.
func seededPersonal(t *testing.T) (*personalProduct, *fakeRoster, *fakeMemberships, *fakeManager, *recordedAudit) {
	t.Helper()
	product, roster, memberships, manager, _ := unitProduct()
	if _, err := product.ensure(context.Background(), unitAccount); err != nil {
		t.Fatalf("seeding the personal tenant: %v", err)
	}
	return product, roster, memberships, manager, auditorOf(t, product)
}

// entriesOfAction answers the entries written for one action.
func entriesOfAction(entries []audit.Entry, action string) []audit.Entry {
	var out []audit.Entry
	for _, entry := range entries {
		if entry.Action == action {
			out = append(out, entry)
		}
	}
	return out
}

// TestAccountDisabledDisablesThePersonalTenant holds the account-caused
// disable: the tenant is disabled and the source says the account caused it.
func TestAccountDisabledDisablesThePersonalTenant(t *testing.T) {
	product, roster, _, _, _ := seededPersonal(t)
	id := personalTenantID(unitAccount)

	if err := product.AccountDisabled(context.Background(), unitAccount); err != nil {
		t.Fatalf("the disable hook: %v", err)
	}
	row := roster.rows[id]
	if row.Status != tenant.StatusDisabled {
		t.Errorf("the tenant's status is %v, want disabled", row.Status)
	}
	if row.DisabledBy != tenant.DisabledByAccount {
		t.Errorf("the disable source is %v, want the account's", row.DisabledBy)
	}
	if row.Alias != string(id) {
		t.Errorf("the disable changed the alias to %q", row.Alias)
	}
	if roster.statusCalls != 1 || roster.statusWrites != 1 {
		t.Errorf("the hook made %d status call(s) and %d write(s), want one of each",
			roster.statusCalls, roster.statusWrites)
	}
}

// TestTheAccountLifecycleEnableRestoresOnlyTheAccountsDisable holds the two enable
// outcomes: the account's own disable is lifted, and an operator's disable is
// left standing.
func TestTheAccountLifecycleEnableRestoresOnlyTheAccountsDisable(t *testing.T) {
	t.Run("a disable the account made is lifted", func(t *testing.T) {
		product, roster, _, _, _ := seededPersonal(t)
		id := personalTenantID(unitAccount)
		if err := product.AccountDisabled(context.Background(), unitAccount); err != nil {
			t.Fatalf("the disable hook: %v", err)
		}
		if err := product.AccountEnabled(context.Background(), unitAccount); err != nil {
			t.Fatalf("the enable hook: %v", err)
		}
		row := roster.rows[id]
		if row.Status != tenant.StatusActive {
			t.Errorf("the tenant's status is %v, want active", row.Status)
		}
		if row.DisabledBy != tenant.DisabledByOperator {
			t.Errorf("the disable source is %v after the enable, want the operator's zero value", row.DisabledBy)
		}
	})

	t.Run("an operator's disable stands", func(t *testing.T) {
		product, roster, _, _, _ := seededPersonal(t)
		id := personalTenantID(unitAccount)
		row := roster.rows[id]
		row.Status = tenant.StatusDisabled
		row.DisabledBy = tenant.DisabledByOperator
		roster.rows[id] = row

		if err := product.AccountEnabled(context.Background(), unitAccount); err != nil {
			t.Fatalf("the enable hook: %v", err)
		}
		if got := roster.rows[id].Status; got != tenant.StatusDisabled {
			t.Errorf("the tenant's status is %v, want the operator's disable to stand", got)
		}
		if roster.statusWrites != 0 {
			t.Errorf("the hook wrote the status %d time(s), want none", roster.statusWrites)
		}
	})
}

// TestTheLifecycleStatusHooksDoNothingWithoutARowOrOnATombstone holds the two no-ops the
// design asks for: an account that never had a personal tenant, and one whose
// tenant is a tombstone.
func TestTheLifecycleStatusHooksDoNothingWithoutARowOrOnATombstone(t *testing.T) {
	hooks := []struct {
		name string
		call func(*personalProduct) error
	}{
		{"disable", func(p *personalProduct) error { return p.AccountDisabled(context.Background(), unitAccount) }},
		{"enable", func(p *personalProduct) error { return p.AccountEnabled(context.Background(), unitAccount) }},
	}
	t.Run("no row", func(t *testing.T) {
		for _, hook := range hooks {
			product, roster, _, manager, _ := unitProduct()
			auditor := auditorOf(t, product)
			if err := hook.call(product); err != nil {
				t.Errorf("the %s hook over an account with no personal tenant gave %v, want nil", hook.name, err)
			}
			if roster.statusCalls != 0 || manager.deletedRoles != 0 || len(auditor.rows()) != 0 {
				t.Errorf("the %s hook wrote something for an account with no personal tenant", hook.name)
			}
		}
	})

	t.Run("a tombstone", func(t *testing.T) {
		for _, hook := range hooks {
			product, roster, _, _, _ := seededPersonal(t)
			id := personalTenantID(unitAccount)
			row := roster.rows[id]
			row.Status = tenant.StatusDeleted
			roster.rows[id] = row

			if err := hook.call(product); err != nil {
				t.Errorf("the %s hook over a tombstone gave %v, want nil", hook.name, err)
			}
			if roster.statusCalls != 0 {
				t.Errorf("the %s hook asked the roster to change a tombstone's status", hook.name)
			}
			if got := roster.rows[id].Status; got != tenant.StatusDeleted {
				t.Errorf("the %s hook moved the tombstone to %v", hook.name, got)
			}
		}
	})
}

// TestTheLifecycleStatusHooksReadTheTableBeforeTheSnapshot holds the read the design
// requires: EnterRegistered forces one table read, so a stale snapshot cannot
// turn "a row exists" into "no row" and silence the hook.
func TestTheLifecycleStatusHooksReadTheTableBeforeTheSnapshot(t *testing.T) {
	product, roster, memberships, _, _ := unitProduct()
	id := personalTenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusActive}
	roster.lagging[id] = true
	memberships.rows[unitAccount] = []authn.Membership{
		{Account: unitAccount, Tenant: string(id), Active: true, Source: "personal"},
	}

	if err := product.AccountDisabled(context.Background(), unitAccount); err != nil {
		t.Fatalf("the disable hook over a lagging snapshot: %v", err)
	}
	if roster.entered != 1 {
		t.Errorf("the roster was caught up %d time(s), want once", roster.entered)
	}
	if got := roster.rows[id].Status; got != tenant.StatusDisabled {
		t.Errorf("the tenant's status is %v, want disabled: the hook read a stale snapshot", got)
	}
}

// TestTheAccountLifecycleDeletionRunsTheFourStepsInOrder holds the deletion's fixed
// sequence and its result: memberships, then bindings and the owner role, then
// the audit row, then the tombstone.
func TestTheAccountLifecycleDeletionRunsTheFourStepsInOrder(t *testing.T) {
	product, roster, memberships, manager, auditor := seededPersonal(t)
	id := personalTenantID(unitAccount)
	tenantID := string(id)
	other := authn.AccountID("zz-other-member")
	memberships.rows[other] = []authn.Membership{
		{Account: other, Tenant: tenantID, Active: true, Source: "invite"},
	}
	manager.bindings[tenantID] = append(manager.bindings[tenantID],
		authz.Binding{Tenant: tenantID, Account: other.String(), Role: personalOwnerRole})
	roster.steps.reset()

	if err := product.AccountDeleted(context.Background(), unitAccount); err != nil {
		t.Fatalf("the delete hook: %v", err)
	}

	want := []string{
		"members",
		"remove:" + unitAccount.String(),
		"remove:" + other.String(),
		"bindings",
		"unbind:" + unitAccount.String() + ":owner",
		"unbind:" + other.String() + ":owner",
		"role:owner",
		"audit:" + deleteAuditAction + ":success",
		"delete",
	}
	if got := roster.steps.snapshot(); !slices.Equal(got, want) {
		t.Errorf("the deletion ran\n%v\nwant\n%v", got, want)
	}

	if held := memberships.rows[other]; len(held) != 0 {
		t.Errorf("the other account's membership survived: %v", held)
	}
	if held := manager.bindings[tenantID]; len(held) != 0 {
		t.Errorf("the tenant's bindings survived: %v", held)
	}
	if held := manager.roles[tenantID]; len(held) != 0 {
		t.Errorf("the owner role survived: %v", held)
	}
	row := roster.rows[id]
	if row.Status != tenant.StatusDeleted {
		t.Errorf("the tenant's status is %v, want deleted", row.Status)
	}
	if row.Alias != tenantID {
		t.Errorf("the tombstone's alias is %q, want the identifier %q", row.Alias, tenantID)
	}

	rows := entriesOfAction(auditor.rows(), deleteAuditAction)
	if len(rows) != 1 {
		t.Fatalf("the deletion wrote %d audit row(s), want one", len(rows))
	}
	entry := rows[0]
	if entry.Object != tenantID || entry.Outcome != outcomeSuccess {
		t.Errorf("the deletion audit row is %+v, want object %q with outcome %q", entry, tenantID, outcomeSuccess)
	}
	if entry.Detail["account"] != unitAccount.String() || entry.Detail["tenant"] != tenantID {
		t.Errorf("the deletion audit detail is %v, want account %q and tenant %q",
			entry.Detail, unitAccount, tenantID)
	}
	if entry.Detail["action_id"] == "" {
		t.Errorf("the deletion audit row carries no action identifier: %v", entry.Detail)
	}
}

// TestTheAccountLifecycleDeletionToleratesABuiltinOwnerRole holds the tolerance the design
// names: a role an operator marked builtin cannot be deleted, and the deletion
// carries on — the bindings are cleared and the tombstone lands.
func TestTheAccountLifecycleDeletionToleratesABuiltinOwnerRole(t *testing.T) {
	product, roster, _, manager, auditor := seededPersonal(t)
	id := personalTenantID(unitAccount)
	manager.deleteRoleErr = fmt.Errorf("%w: an operator marked the owner role builtin", authz.ErrBuiltinRole)

	if err := product.AccountDeleted(context.Background(), unitAccount); err != nil {
		t.Fatalf("the delete hook over a builtin owner role: %v", err)
	}
	if held := manager.bindings[string(id)]; len(held) != 0 {
		t.Errorf("the tenant's bindings survived: %v", held)
	}
	if got := roster.rows[id].Status; got != tenant.StatusDeleted {
		t.Errorf("the tenant's status is %v, want deleted", got)
	}
	if rows := entriesOfAction(auditor.rows(), deleteAuditAction); len(rows) != 1 {
		t.Errorf("the deletion wrote %d audit row(s), want one", len(rows))
	}
}

// TestTheAccountLifecycleDeletionStopsWhenTheAuditRowFails holds the mandatory
// row of the deletion: an audit module that cannot take it leaves the tenant in
// the roster, so the retry that finishes the cleanup is the same call.
func TestTheAccountLifecycleDeletionStopsWhenTheAuditRowFails(t *testing.T) {
	product, roster, _, _, _ := seededPersonal(t)
	id := personalTenantID(unitAccount)
	auditor := auditorOf(t, product)
	auditor.err = errors.New("the audit module is not accepting records")

	if err := product.AccountDeleted(context.Background(), unitAccount); err == nil {
		t.Fatalf("the delete hook succeeded without the deletion's audit row")
	}
	if got := roster.rows[id].Status; got == tenant.StatusDeleted {
		t.Errorf("the tenant is deleted although the audit row was not written")
	}

	auditor.err = nil
	if err := product.AccountDeleted(context.Background(), unitAccount); err != nil {
		t.Fatalf("the retried delete hook: %v", err)
	}
	if got := roster.rows[id].Status; got != tenant.StatusDeleted {
		t.Errorf("the tenant's status is %v after the retry, want deleted", got)
	}
}

// TestTheAccountLifecycleDeletionIsIdempotentOnATombstone holds the completion marker: a
// repeated hook over the tombstone writes nothing.
func TestTheAccountLifecycleDeletionIsIdempotentOnATombstone(t *testing.T) {
	product, roster, _, manager, auditor := seededPersonal(t)
	ctx := context.Background()
	if err := product.AccountDeleted(ctx, unitAccount); err != nil {
		t.Fatalf("the first delete hook: %v", err)
	}
	auditRows := len(auditor.rows())
	deletes := roster.deleteAttempts
	unbinds := manager.deletedBindings
	roles := manager.deletedRoles

	if err := product.AccountDeleted(ctx, unitAccount); err != nil {
		t.Fatalf("the repeated delete hook: %v", err)
	}
	if got := len(auditor.rows()); got != auditRows {
		t.Errorf("the repeated hook wrote %d audit row(s), want none over the %d already there",
			got-auditRows, auditRows)
	}
	if roster.deleteAttempts != deletes {
		t.Errorf("the repeated hook called the roster's delete")
	}
	if manager.deletedBindings != unbinds || manager.deletedRoles != roles {
		t.Errorf("the repeated hook touched the policy rows")
	}
}

// TestTheAccountLifecycleDeletionRetriesAfterTheTombstoneWriteFails holds the retry: the
// cleanup before the tombstone stays, the audit row is written again (the
// design accepts at-least-once), and the second call lands the tombstone.
func TestTheAccountLifecycleDeletionRetriesAfterTheTombstoneWriteFails(t *testing.T) {
	product, roster, _, _, auditor := seededPersonal(t)
	id := personalTenantID(unitAccount)
	roster.deleteErr = errors.New("the tenant table is unavailable")

	if err := product.AccountDeleted(context.Background(), unitAccount); err == nil {
		t.Fatalf("the delete hook succeeded while the tombstone write failed")
	}
	if rows := entriesOfAction(auditor.rows(), deleteAuditAction); len(rows) != 1 {
		t.Errorf("the failed attempt wrote %d audit row(s), want the one before the tombstone", len(rows))
	}
	if got := roster.rows[id].Status; got == tenant.StatusDeleted {
		t.Errorf("the tenant is deleted although the tombstone write failed")
	}

	roster.deleteErr = nil
	if err := product.AccountDeleted(context.Background(), unitAccount); err != nil {
		t.Fatalf("the retried delete hook: %v", err)
	}
	if got := roster.rows[id].Status; got != tenant.StatusDeleted {
		t.Errorf("the tenant's status is %v after the retry, want deleted", got)
	}
	if rows := entriesOfAction(auditor.rows(), deleteAuditAction); len(rows) != 2 {
		t.Errorf("the deletion wrote %d audit row(s) in all, want the duplicate the retry is allowed", len(rows))
	}
}

// TestTheAccountLifecycleDeletionRecordsTheAuditWithoutAnIdentity holds the system-driven
// deletion: the hook writes the row even when the context carries no identity,
// which is what makes the audit module record an empty actor.
func TestTheAccountLifecycleDeletionRecordsTheAuditWithoutAnIdentity(t *testing.T) {
	product, _, _, _, _ := unitProduct()
	seedCtx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
	if _, err := product.ensure(seedCtx, unitAccount); err != nil {
		t.Fatalf("seeding the personal tenant: %v", err)
	}
	auditor := auditorOf(t, product)

	if err := product.AccountDeleted(context.Background(), unitAccount); err != nil {
		t.Fatalf("the system-driven delete hook: %v", err)
	}
	entries := auditor.rows()
	if len(entries) != 2 {
		t.Fatalf("the audit holds %d row(s), want the creation and the deletion", len(entries))
	}
	if entries[0].Action != createAuditAction || !auditor.identities[0] {
		t.Errorf("the creation row is %q with identity=%t, want the identified creation",
			entries[0].Action, auditor.identities[0])
	}
	if entries[1].Action != deleteAuditAction {
		t.Errorf("the second row is %q, want the deletion", entries[1].Action)
	}
	if auditor.identities[1] {
		t.Errorf("the deletion row was recorded with an identity, want the system action's empty actor")
	}
}

// TestAccountDeletedDoesNothingWithoutAPersonalTenant holds the account that
// never had a workspace: nothing is written and the hook is satisfied.
func TestAccountDeletedDoesNothingWithoutAPersonalTenant(t *testing.T) {
	product, roster, memberships, manager, _ := unitProduct()
	auditor := auditorOf(t, product)
	if err := product.AccountDeleted(context.Background(), unitAccount); err != nil {
		t.Fatalf("the delete hook over an account with no personal tenant: %v", err)
	}
	if len(auditor.rows()) != 0 || roster.deleteAttempts != 0 ||
		memberships.removes != 0 || manager.deletedBindings != 0 || manager.deletedRoles != 0 {
		t.Errorf("the hook wrote something for an account with no personal tenant")
	}
}

// TestServiceSecretRevokedLeavesThePersonalTenantAlone holds the no-op: a
// service account has no personal tenant.
func TestServiceSecretRevokedLeavesThePersonalTenantAlone(t *testing.T) {
	product, roster, memberships, manager, auditor := seededPersonal(t)
	auditRows := len(auditor.rows())
	removes, unbinds, roles := memberships.removes, manager.deletedBindings, manager.deletedRoles

	if err := product.ServiceSecretRevoked(context.Background(), unitAccount, "secret-1"); err != nil {
		t.Fatalf("the service-secret hook: %v", err)
	}
	if len(auditor.rows()) != auditRows || memberships.removes != removes ||
		manager.deletedBindings != unbinds || manager.deletedRoles != roles ||
		roster.deleteAttempts != 0 {
		t.Errorf("the service-secret hook wrote something for a service account")
	}
}

// TestEnsureWritesTheCreationAuditBeforeCreate holds the mandatory row: it
// lands before the roster is called, it names the action's two ends, and both
// rows of an attempt are linked by one action identifier.
func TestEnsureWritesTheCreationAuditBeforeCreate(t *testing.T) {
	product, roster, _, _, _ := unitProduct()
	auditor := auditorOf(t, product)
	if _, err := product.ensure(context.Background(), unitAccount); err != nil {
		t.Fatalf("the ensure: %v", err)
	}

	rows := auditor.rows()
	if len(rows) != 1 {
		t.Fatalf("the ensure wrote %d audit row(s), want the creation", len(rows))
	}
	entry := rows[0]
	id := string(personalTenantID(unitAccount))
	if entry.Action != createAuditAction || entry.Object != id || entry.Outcome != outcomeSuccess {
		t.Errorf("the creation audit row is %+v, want %q on %q with outcome %q",
			entry, createAuditAction, id, outcomeSuccess)
	}
	if entry.Detail["account"] != unitAccount.String() || entry.Detail["tenant"] != id {
		t.Errorf("the creation audit detail is %v, want account %q and tenant %q",
			entry.Detail, unitAccount, id)
	}
	if entry.Detail["action_id"] == "" {
		t.Errorf("the creation audit row carries no action identifier: %v", entry.Detail)
	}

	steps := roster.steps.snapshot()
	if len(steps) < 2 || steps[0] != "audit:"+createAuditAction+":success" || steps[1] != "create" {
		t.Errorf("the ensure ran\n%v\nwant the audit row before the create", steps)
	}
}

// TestTheEnsureAuditClosesAFailedCreateWithALinkedFailedRow holds the two failures the
// design closes with a failed row: the roster refusing the write, and another
// call winning the race the deterministic identifier creates.
func TestTheEnsureAuditClosesAFailedCreateWithALinkedFailedRow(t *testing.T) {
	check := func(t *testing.T, auditor *recordedAudit) {
		t.Helper()
		rows := auditor.rows()
		if len(rows) != 2 {
			t.Fatalf("the ensure wrote %d audit row(s), want the permitted and the failed", len(rows))
		}
		if rows[0].Outcome != outcomeSuccess || rows[1].Outcome != outcomeFailed {
			t.Errorf("the outcomes are %q then %q, want success then failed", rows[0].Outcome, rows[1].Outcome)
		}
		first, second := rows[0].Detail["action_id"], rows[1].Detail["action_id"]
		if first == "" || first != second {
			t.Errorf("the two rows carry action ids %q and %q, want one shared non-empty value", first, second)
		}
		if rows[0].Action != createAuditAction || rows[1].Action != createAuditAction {
			t.Errorf("the rows name %q and %q, want %q", rows[0].Action, rows[1].Action, createAuditAction)
		}
	}

	t.Run("the Create call fails", func(t *testing.T) {
		product, roster, _, _, _ := unitProduct()
		roster.createErr = errors.New("the tenant table is unavailable")
		if _, err := product.ensure(context.Background(), unitAccount); err == nil {
			t.Fatalf("the ensure succeeded while the create failed")
		}
		check(t, auditorOf(t, product))
	})

	t.Run("another call won the race", func(t *testing.T) {
		product, roster, _, _, _ := unitProduct()
		id := personalTenantID(unitAccount)
		roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusActive}

		result, err := product.ensure(context.Background(), unitAccount)
		if err != nil {
			t.Fatalf("the ensure over the row another call wrote: %v", err)
		}
		if result.Created {
			t.Errorf("the ensure reports created=true, want false: another call wrote the row")
		}
		check(t, auditorOf(t, product))
	})
}

// TestEnsureWritesNoCreationAuditForAnExistingMembership holds the other branch:
// an account that already has its personal membership is read back, and no
// creation row is written.
func TestEnsureWritesNoCreationAuditForAnExistingMembership(t *testing.T) {
	product, roster, memberships, _, _ := unitProduct()
	auditor := auditorOf(t, product)
	id := personalTenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusActive}
	memberships.rows[unitAccount] = []authn.Membership{
		{Account: unitAccount, Tenant: string(id), Active: true, Source: "personal"},
	}

	if _, err := product.ensure(context.Background(), unitAccount); err != nil {
		t.Fatalf("the ensure: %v", err)
	}
	if rows := auditor.rows(); len(rows) != 0 {
		t.Errorf("the read-back ensure wrote %d audit row(s), want none", len(rows))
	}
}

// TestTheCreationAuditFailureStopsTheCreate holds the mandatory row's force: an
// audit module that cannot accept the row answers 503 and no tenant is created.
func TestTheCreationAuditFailureStopsTheCreate(t *testing.T) {
	product, roster, _, _, _ := unitProduct()
	auditor := auditorOf(t, product)
	auditor.err = errors.New("the audit module is not accepting records")
	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
	c, recorder := ginContext(ctx)
	serveEnsure(product, c)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("the ensure answered %d, want 503:\n%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "not accepting records") {
		t.Errorf("the 503 body repeats the cause:\n%s", recorder.Body.String())
	}
	if roster.creates != 0 || len(roster.rows) != 0 {
		t.Errorf("the ensure created %d tenant row(s) without the audit row", len(roster.rows))
	}
}

// TestThePersonalModuleDeclaresTheLifecycleAndTheAudit holds the descriptor:
// the audit module is a required dependency and the lifecycle hook is provided.
func TestThePersonalModuleDeclaresTheLifecycleAndTheAudit(t *testing.T) {
	module := personalModule()
	auditRequired := false
	for _, requirement := range module.Requires {
		if requirement.Token == (*audit.Audit)(nil) && !requirement.Optional {
			auditRequired = true
		}
	}
	if !auditRequired {
		t.Errorf("the module does not require the audit capability")
	}
	lifecycleProvided := false
	for _, provision := range module.Provides {
		if provision.Token == (*authn.AccountLifecycle)(nil) {
			lifecycleProvided = true
		}
	}
	if !lifecycleProvided {
		t.Errorf("the module does not provide the account lifecycle hook")
	}
}
