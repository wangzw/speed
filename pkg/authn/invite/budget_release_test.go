package invite

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestAConcurrentIssuanceThatLosesDoesNotSpendTheAddress holds the race's
// accounting: both issuances pass every target check, so both charge the address
// and the deployment before either writes; the one the partial unique index
// refuses gives both counts back, leaving the winner's single charge.
func TestAConcurrentIssuanceThatLosesDoesNotSpendTheAddress(t *testing.T) {
	h := newHarness(t)
	// Hold both issuances at the address charge: reaching it means each one has
	// passed the pending check, so the loser is left for the unique index rather
	// than the pre-check.
	var arrived atomic.Int32
	gate := make(chan struct{})
	h.counters.onIncr = func(key string, delta float64) {
		if delta <= 0 || !strings.Contains(key, budgetIssueAddress) {
			return
		}
		if arrived.Add(1) == 2 {
			close(gate)
		}
		select {
		case <-gate:
		case <-time.After(5 * time.Second):
		}
	}

	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := h.p.Issue(context.Background(), IssueInvite{Tenant: tenant, Email: "alice@example.com"})
			errs <- err
		}()
	}
	var issued, refused int
	for i := 0; i < 2; i++ {
		switch err := <-errs; {
		case err == nil:
			issued++
		case errors.Is(err, ErrInvitationPending):
			refused++
		default:
			t.Errorf("a concurrent issuance gave %v, want success or ErrInvitationPending", err)
		}
	}
	if issued != 1 || refused != 1 {
		t.Fatalf("%d issuances succeeded and %d were refused, want one of each", issued, refused)
	}
	if got := h.budgetCount(budgetIssueAddress, "alice@example.com"); got != 1 {
		t.Errorf("the address count = %v after the race, want 1: only the winner sent", got)
	}
	if got := h.budgetCount(budgetIssueGlobal, ""); got != 1 {
		t.Errorf("the deployment count = %v after the race, want 1: only the winner sent", got)
	}
}

// TestADeploymentRefusalDoesNotSpendTheAddress holds the third budget's order:
// the address is charged before the deployment, and a deployment refusal gives
// the address charge back because nothing was sent.
func TestADeploymentRefusalDoesNotSpendTheAddress(t *testing.T) {
	h := newHarness(t)
	h.store.set(issueAddressPath, 100)
	h.store.set(issueIssuerPath, 100)
	h.store.set(issueGlobalPath, 1)

	if _, err := issueAs(h, adminID, "one@example.com"); err != nil {
		t.Fatalf("the first issuance gave %v", err)
	}
	if _, err := issueAs(h, adminID, "two@example.com"); err == nil {
		t.Fatal("the issuance past the deployment budget was let through")
	} else {
		throttleOf(t, err)
	}
	if got := h.budgetCount(budgetIssueAddress, "two@example.com"); got != 0 {
		t.Errorf("the address count = %v after the deployment refused the send, want 0", got)
	}
}

// TestAResendThatLosesTheRowDoesNotSpendTheAddress holds the resend's accounting:
// the row's terminal check passes, then the row turns terminal before the
// conditional write, so the write lands nowhere and the address and deployment
// charges are given back.
func TestAResendThatLosesTheRowDoesNotSpendTheAddress(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	before := h.budgetCount(budgetIssueAddress, "alice@example.com")
	armed := true
	h.counters.onIncr = func(key string, delta float64) {
		if !armed || delta <= 0 || !strings.Contains(key, budgetIssueAddress) {
			return
		}
		armed = false
		// The row turns terminal after the terminal check and before the
		// conditional write.
		if err := h.p.Revoke(context.Background(), tenant, issued.Invitation.ID); err != nil {
			h.t.Errorf("revoking during the resend: %v", err)
		}
	}

	if _, err := h.p.Resend(context.Background(), tenant, issued.Invitation.ID); !errors.Is(err, ErrInvitationRevoked) {
		t.Fatalf("the resend gave %v, want ErrInvitationRevoked", err)
	}
	if got := h.budgetCount(budgetIssueAddress, "alice@example.com"); got != before {
		t.Errorf("the address count moved from %v to %v although the resend wrote nothing", before, got)
	}
}
