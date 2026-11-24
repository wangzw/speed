package invite

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

type resendingAccounts struct {
	authn.Accounts
	once   sync.Once
	resend func()
}

func (a *resendingAccounts) Identifiers(ctx context.Context, id authn.AccountID) ([]authn.Identifier, error) {
	a.once.Do(a.resend)
	return a.Accounts.Identifiers(ctx, id)
}

// TestAcceptDoesNotRedeemATokenThatAResendReplaced checks that a token a concurrent resend replaced no longer resolves and the row keeps its resend count.
func TestAcceptDoesNotRedeemATokenThatAResendReplaced(t *testing.T) {
	h := newHarness(t)
	h.proveEmail("acct-alice", "alice@example.com")
	issued := h.issue(tenant, "alice@example.com")

	var resendErr error
	h.p.accounts = &resendingAccounts{Accounts: h.accounts, resend: func() {
		_, resendErr = h.p.Resend(context.Background(), tenant, issued.Invitation.ID)
	}}

	ctx := authntest.Context(t, context.Background(), authntest.WithAccount("acct-alice"))
	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); !errors.Is(err, ErrInvitationNotFound) {
		t.Fatalf("accepting a token a resend replaced gave %v, want ErrInvitationNotFound", err)
	}
	if resendErr != nil {
		t.Fatalf("the concurrent resend gave %v", resendErr)
	}
	row := h.row(issued.Invitation.ID)
	if row.Status != statusPending {
		t.Errorf("the row reads status %d, want it still pending", row.Status)
	}
	if row.SendCount != 2 {
		t.Errorf("send_count = %d, want 2: the issuance and the resend alone", row.SendCount)
	}
}
