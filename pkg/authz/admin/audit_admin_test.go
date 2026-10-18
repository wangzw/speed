package admin

import (
	"errors"
	"net/http"
	"testing"
)

// TestTheAuditVocabularyIsPinned holds the literal values of the action names,
// the outcome values and the detail keys this module writes. The behavior cases
// read them back through the same constants that produce them, so only a table
// that writes the literals down catches a renamed key or action.
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
		"role put":       "authz.role.put",
		"role delete":    "authz.role.delete",
		"grant put":      "authz.grant.put",
		"grant delete":   "authz.grant.delete",
		"binding put":    "authz.binding.put",
		"binding delete": "authz.binding.delete",
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

// TestEachActionIDIsFresh holds that newActionID mints a fresh identifier per
// write and is not a constant: two writes must carry two different ids, so the
// rows of one write are linked to that write and not to another.
func TestEachActionIDIsFresh(t *testing.T) {
	a, b := newActionID(), newActionID()
	if a == "" || b == "" {
		t.Fatalf("an action id is empty: %q, %q", a, b)
	}
	if a == b {
		t.Fatalf("two calls minted the same action id %q, so the rows they carry cannot be told apart", a)
	}
}

// TestTheEmittedDetailKeysAreTheLiterals holds the keys the module writes on the
// wire of the audit row without going through the module's own constants: a
// consumer keying on "action_id" or "tenant" must find them. The behavior cases
// read the detail through detailActionID and detailTenant, which move with a
// renamed constant, so the literal keys are held here.
func TestTheEmittedDetailKeysAreTheLiterals(t *testing.T) {
	r := newRig(t)
	r.manager.fail(errors.New("the store is down"))
	w := r.serve(r.p.handleRolePut, http.MethodPut, "/api/v1/authz/admin/tenants/t1/roles/editor",
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
