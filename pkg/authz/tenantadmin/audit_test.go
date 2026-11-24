package tenantadmin

import (
	"errors"
	"net/http"
	"testing"
)

// TestTheAuditVocabularyIsPinned checks that the audit vocabulary is pinned.
func TestTheAuditVocabularyIsPinned(t *testing.T) {
	actions := map[string]string{
		"role put":       actionRolePut,
		"role delete":    actionRoleDelete,
		"grant put":      actionGrantPut,
		"grant delete":   actionGrantDelete,
		"binding put":    actionBindingPut,
		"binding delete": actionBindingDelete,
	}
	wantActions := map[string]string{
		"role put":       "authz.tenant.role.put",
		"role delete":    "authz.tenant.role.delete",
		"grant put":      "authz.tenant.grant.put",
		"grant delete":   "authz.tenant.grant.delete",
		"binding put":    "authz.tenant.binding.put",
		"binding delete": "authz.tenant.binding.delete",
	}
	for name, got := range actions {
		if got != wantActions[name] {
			t.Errorf("the %s action = %q, want %q", name, got, wantActions[name])
		}
	}
	if outcomeSuccess != "success" || outcomeFailed != "failed" {
		t.Errorf("the outcomes = %q, %q, want success and failed", outcomeSuccess, outcomeFailed)
	}
	keys := map[string]string{
		"action id":  detailActionID,
		"tenant":     detailTenant,
		"role":       detailRole,
		"permission": detailPermission,
		"account":    detailAccount,
		"change":     detailChange,
	}
	wantKeys := map[string]string{
		"action id":  "action_id",
		"tenant":     "tenant",
		"role":       "role",
		"permission": "permission",
		"account":    "account",
		"change":     "change",
	}
	for name, got := range keys {
		if got != wantKeys[name] {
			t.Errorf("the %s detail key = %q, want %q", name, got, wantKeys[name])
		}
	}
}

// TestTheTenantActionsAreThePlatformActionsPrefixed checks that the tenant actions are the platform actions prefixed.
func TestTheTenantActionsAreThePlatformActionsPrefixed(t *testing.T) {
	platform := []string{
		"authz.role.put", "authz.role.delete", "authz.grant.put", "authz.grant.delete",
		"authz.binding.put", "authz.binding.delete",
	}
	tenant := []string{
		actionRolePut, actionRoleDelete, actionGrantPut, actionGrantDelete,
		actionBindingPut, actionBindingDelete,
	}
	for _, name := range tenant {
		for _, other := range platform {
			if name == other {
				t.Errorf("the tenant action %q is the platform surface's", name)
			}
		}
	}
}

// TestEachActionIDIsFresh checks that each action ID is fresh.
func TestEachActionIDIsFresh(t *testing.T) {
	a, b := newActionID(), newActionID()
	if a == "" || b == "" {
		t.Fatalf("an action id is empty: %q, %q", a, b)
	}
	if a == b {
		t.Fatalf("two calls minted the same action id %q", a)
	}
}

// TestTheEmittedDetailKeysAreTheLiterals checks that the emitted detail keys are the literals.
func TestTheEmittedDetailKeysAreTheLiterals(t *testing.T) {
	r := newRig(t)
	r.manager.fail(errors.New("the store is down"))
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/editor",
		params("tenant", "t1", "role", "editor"), roleBody{})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("the write answered %d, want 503", w.Code)
	}
	rows := r.audit.entries()
	if len(rows) != 2 {
		t.Fatalf("the audit rows = %+v, want the mandatory row and the failed one", rows)
	}
	if rows[0].Detail["action_id"] == "" {
		t.Fatalf("the mandatory row carries no action_id key: %+v", rows[0].Detail)
	}
	if rows[0].Detail["tenant"] != "t1" {
		t.Fatalf("the mandatory row's tenant key = %q, want t1", rows[0].Detail["tenant"])
	}
	if rows[1].Detail["action_id"] != rows[0].Detail["action_id"] {
		t.Fatalf("the failed row's action_id = %q, want the mandatory row's %q",
			rows[1].Detail["action_id"], rows[0].Detail["action_id"])
	}
}
