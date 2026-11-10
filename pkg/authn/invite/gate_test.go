package invite

import (
	"errors"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// TestManagementWritesAreRefusedForAnUnverifiedAccount holds the signup gate of
// the management face: issuing (which also supersedes a pending invitation) and
// revoking answer 403 contact-required for a pending self-registration, ask no
// Decider, write no audit row and write no invitation row. Listing is a read and
// is not gated, and an account that never registered or has proved its address
// issues as before.
func TestManagementWritesAreRefusedForAnUnverifiedAccount(t *testing.T) {
	for _, c := range []struct {
		name  string
		state authn.SignupStatus
		code  int
	}{
		{"a pending registration", authn.SignupStatus{State: authn.SignupPending}, http.StatusForbidden},
		{"a never-registered account", authn.SignupStatus{State: authn.SignupNone}, http.StatusCreated},
		{"a verified registration", authn.SignupStatus{State: authn.SignupVerified}, http.StatusCreated},
	} {
		t.Run("issue: "+c.name, func(t *testing.T) {
			h := newHarness(t)
			h.verification.state = c.state
			h.asAdmin()
			w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
			if w.Code != c.code {
				t.Fatalf("status = %d, want %d: %s", w.Code, c.code, w.Body.String())
			}
			if c.code == http.StatusForbidden {
				if h.decider.count() != 0 {
					t.Errorf("the Decider was asked %d times for a refused issue, want none", h.decider.count())
				}
				if len(h.audit.entries()) != 0 {
					t.Errorf("an audit row was written for a refused issue")
				}
				if h.rowCount() != 0 {
					t.Errorf("an invitation row was written for a refused issue")
				}
			}
		})
	}

	for _, c := range []struct {
		name  string
		state authn.SignupStatus
		code  int
	}{
		{"a pending registration", authn.SignupStatus{State: authn.SignupPending}, http.StatusForbidden},
		{"a never-registered account", authn.SignupStatus{State: authn.SignupNone}, http.StatusNoContent},
	} {
		t.Run("revoke: "+c.name, func(t *testing.T) {
			h := newHarness(t)
			issued := h.issue("tenant-a", "alice@example.com")
			h.verification.state = c.state
			h.asAdmin()
			before := len(h.audit.entries())
			w := h.request(http.MethodDelete, pathAdmin+"/"+issued.Invitation.ID, nil)
			if w.Code != c.code {
				t.Fatalf("status = %d, want %d: %s", w.Code, c.code, w.Body.String())
			}
			if c.code == http.StatusForbidden {
				if h.decider.count() != 0 {
					t.Errorf("the Decider was asked %d times for a refused revoke, want none", h.decider.count())
				}
				if len(h.audit.entries()) != before {
					t.Errorf("an audit row was written for a refused revoke")
				}
				if got := h.row(issued.Invitation.ID); got.Status != statusPending {
					t.Errorf("the refused revoke left the invitation %d, want pending", got.Status)
				}
			}
		})
	}
}

// TestResendingIsRefusedForAnUnverifiedAccount holds the superseding issue: a
// second issuance for the same tenant and address replaces the first, and an
// unverified account is refused before the superseding write as well. The old
// invitation stays pending and live.
func TestResendingIsRefusedForAnUnverifiedAccount(t *testing.T) {
	h := newHarness(t)
	first := h.issue("tenant-a", "alice@example.com")
	h.verification.state = authn.SignupStatus{State: authn.SignupPending}
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if got := h.row(first.Invitation.ID); got.Status != statusPending {
		t.Errorf("the refused resend left the first invitation %d, want pending", got.Status)
	}
	if h.rowCount() != 1 {
		t.Errorf("the refused resend changed the row count to %d, want one", h.rowCount())
	}
}

// TestListingIsNotGated holds that the read is unaffected: a pending
// self-registration lists its tenant's invitations.
func TestListingIsNotGated(t *testing.T) {
	h := newHarness(t)
	h.issue("tenant-a", "alice@example.com")
	h.verification.state = authn.SignupStatus{State: authn.SignupPending}
	h.asAdmin()
	w := h.request(http.MethodGet, pathAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if got := h.verification.readCount(); got != 0 {
		t.Errorf("listing read the signup state %d times, want none", got)
	}
}

// TestServiceAccountsAreExemptFromTheGate holds the exemption: a service account
// carries no self-registration, so even a state that reports pending does not
// refuse it.
func TestServiceAccountsAreExemptFromTheGate(t *testing.T) {
	h := newHarness(t)
	h.accounts.kinds[adminID] = authn.KindService
	h.verification.state = authn.SignupStatus{State: authn.SignupPending}
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	if got := h.verification.readCount(); got != 0 {
		t.Errorf("a service account read the signup state %d times, want none", got)
	}
}

// TestTheSignupGateReadsTheOperatorsOwnState holds the gate's subject when the
// request is an impersonation: the operator's hands are on the request, so the
// operator's unproved address is what the write is refused for, even when the
// impersonated account has proved its own. Reading the effective account
// instead would let the operator issue through the target's verification.
func TestTheSignupGateReadsTheOperatorsOwnState(t *testing.T) {
	const targetID = authn.AccountID("acct-target")
	h := newHarness(t)
	h.verification.states = map[authn.AccountID]authn.SignupStatus{
		adminID:  {State: authn.SignupPending},
		targetID: {State: authn.SignupVerified},
	}
	h.mounter.As(
		authntest.WithAccount(targetID),
		authntest.WithActor(adminID),
		authntest.WithSession("sid-op"),
		authntest.WithScope("tenant-a"),
	)
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 contact-required: %s", w.Code, w.Body.String())
	}
	if got := h.verification.asked; len(got) != 1 || got[0] != adminID {
		t.Errorf("the gate read the signup state of %v, want the operator %q", got, adminID)
	}
	if h.decider.count() != 0 {
		t.Errorf("the Decider was asked %d times, want none", h.decider.count())
	}
	if len(h.audit.entries()) != 0 {
		t.Errorf("an audit row was written for a refused issue")
	}
	if h.rowCount() != 0 {
		t.Errorf("an invitation row was written for a refused issue")
	}
}

// TestAnUnreadableSignupStateIs503 holds the failure direction: a state read that
// fails refuses the action as unavailable and never as a pass, and writes
// nothing.
func TestAnUnreadableSignupStateIs503(t *testing.T) {
	h := newHarness(t)
	h.verification.err = errors.New("boom")
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", w.Code, w.Body.String())
	}
	if h.decider.count() != 0 {
		t.Errorf("the Decider was asked %d times, want none", h.decider.count())
	}
	if h.rowCount() != 0 {
		t.Errorf("an invitation row was written")
	}
}

// TestAcceptDoesNotCreateOrProveAnAccount holds the removed paths: an acceptance
// never opens an account for an address that has none, never reads the pending
// contact of a self-registration, and never writes an identifier. A caller
// without a proven address is refused and the invitation stays pending.
func TestAcceptDoesNotCreateOrProveAnAccount(t *testing.T) {
	h := newHarness(t)
	issued := h.issue("tenant-a", "alice@example.com")
	h.verification.owner = "acct-pending"
	h.verification.pending = true
	caller := h.acceptCtx("acct-caller", "caller@example.com")

	if _, err := h.p.Accept(caller, AcceptInvite{Token: issued.Token, Username: "alice"}); !errors.Is(err, ErrInvitationEmailUnproved) {
		t.Fatalf("Accept = %v, want ErrInvitationEmailUnproved", err)
	}
	if h.admin.creates != 0 {
		t.Errorf("the acceptance created %d account(s), want none", h.admin.creates)
	}
	if len(h.admin.added) != 0 {
		t.Errorf("the acceptance wrote %d identifier(s), want none", len(h.admin.added))
	}
	if h.verification.contactReads != 0 {
		t.Errorf("the acceptance read the pending-contact table %d time(s), want none", h.verification.contactReads)
	}
	if h.members.count != 0 {
		t.Errorf("the acceptance wrote %d membership(s), want none", h.members.count)
	}
	if got := h.row(issued.Invitation.ID); got.Status != statusPending {
		t.Errorf("the invitation is %d, want pending so a retry is possible", got.Status)
	}
}
