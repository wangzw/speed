package invite

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// TestAnIssuedInvitationIsAuditedByItsIdentifier holds the mandatory issue row:
// it names the invitation the action creates, so one record carries both the
// operator and the invitation identifier. During an impersonation the journal
// files the row under the operator while the invitation names the impersonated
// account, and the invitation identifier is what ties the two together.
func TestAnIssuedInvitationIsAuditedByItsIdentifier(t *testing.T) {
	h := newHarness(t)
	h.mounter.As(
		authntest.WithAccount("acct-target"),
		authntest.WithActor("acct-operator"),
		authntest.WithSession(authn.SessionID("session-1")),
		authntest.WithScope(tenant),
	)

	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var answered struct {
		Invitation AuthnInviteInvitation `json:"invitation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}

	var found bool
	for _, record := range h.audit.recorded() {
		if record.entry.Action != actionIssue || record.entry.Outcome != outcomeSuccess {
			continue
		}
		if record.entry.Object != answered.Invitation.ID {
			continue
		}
		if record.actor != authn.AccountID("acct-operator") {
			t.Errorf("the issue row's actor = %q, want the operator acct-operator", record.actor)
		}
		if record.account != authn.AccountID("acct-target") {
			t.Errorf("the issue row's account = %q, want the impersonated account acct-target", record.account)
		}
		found = true
	}
	if !found {
		t.Errorf("no issue audit row names the invitation %q; rows = %+v", answered.Invitation.ID, h.audit.recorded())
	}
}

// TestAFailedIssueIsAuditedByTheSameInvitationAsItsPermittedRow holds the row a
// refused issuance writes. The identifier is minted before the mandatory row, so
// the permitted row names the invitation the request would create; the row
// closing the same action names it too, and the tenant's name never stands in
// for the object of this action. One action therefore never writes two objects.
func TestAFailedIssueIsAuditedByTheSameInvitationAsItsPermittedRow(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	body := map[string]any{
		"email":      "alice@example.com",
		"expires_at": h.clock.read().Add(maxExplicitLifetime).Add(time.Minute).Format(time.RFC3339),
	}
	w := h.request(http.MethodPost, pathAdmin, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}

	var permittedObject, refusedObject string
	var permittedActionID, refusedActionID string
	for _, record := range h.audit.recorded() {
		if record.entry.Action != actionIssue {
			continue
		}
		switch record.entry.Outcome {
		case outcomeSuccess:
			permittedObject = record.entry.Object
			permittedActionID = record.entry.Detail[detailActionID]
		case outcomeFailed:
			refusedObject = record.entry.Object
			refusedActionID = record.entry.Detail[detailActionID]
		}
	}
	if permittedObject == "" {
		t.Fatalf("no permitted issue audit row; rows = %+v", h.audit.recorded())
	}
	if refusedObject == "" {
		t.Fatalf("no failed issue audit row; rows = %+v", h.audit.recorded())
	}
	if refusedObject != permittedObject {
		t.Errorf("the failed issue row's object = %q, want the permitted row's invitation %q", refusedObject, permittedObject)
	}
	if refusedObject == tenant {
		t.Errorf("the failed issue row's object is the tenant %q, want the invitation identifier", refusedObject)
	}
	if refusedActionID != permittedActionID {
		t.Errorf("the failed row's action_id = %q, want the permitted row's %q", refusedActionID, permittedActionID)
	}
}
