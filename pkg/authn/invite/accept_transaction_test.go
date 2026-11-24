package invite

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

type failingBinder struct{ err error }

func (f failingBinder) BindRoleTx(context.Context, *gorm.DB, string, string, string) (func(context.Context) error, error) {
	return nil, f.err
}

// TestABindingFailureRollsTheAcceptanceBack checks that a failed role binding rolls back the claim and the membership write.
func TestABindingFailureRollsTheAcceptanceBack(t *testing.T) {
	withRealAccountProduct(t, func(handle *gorm.DB, _ authn.Signups, admin authn.AccountAdmin, accounts authn.Accounts,
		verification authn.SignupVerification, memberships authn.Memberships,
	) {
		applyMigrations(t, handle)
		p := newRealProduct(t, handle, admin, accounts, verification, memberships)
		p.rolePolicy = &fakeRolePolicy{}
		p.roleBinder = failingBinder{err: errors.New("the policy store refused the binding")}

		ctx := context.Background()
		inviter, err := admin.Create(ctx, authn.CreateAccount{
			Kind: authn.KindUser, Username: "inviter", Email: "inviter@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaEmailCode}, Via: authn.ReasonAdmin,
		})
		if err != nil {
			t.Fatalf("creating the inviter: %v", err)
		}
		created, err := admin.Create(ctx, authn.CreateAccount{
			Kind: authn.KindUser, Username: "newcomer", Email: "newcomer@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaEmailCode}, Via: authn.ReasonAdmin,
		})
		if err != nil {
			t.Fatalf("creating the account: %v", err)
		}

		issueCtx := authntest.Context(t, ctx, authntest.WithAccount(inviter.ID))
		issued, err := p.Issue(issueCtx, IssueInvite{Tenant: tenant, Email: "newcomer@example.com", Role: "editor"})
		if err != nil {
			t.Fatalf("issuing an invitation with a role: %v", err)
		}

		acceptCtx := authntest.Context(t, ctx, authntest.WithAccount(created.ID))
		if _, err = p.Accept(acceptCtx, AcceptInvite{Token: issued.Token}); !errors.Is(err, authn.ErrUnavailable) {
			t.Fatalf("accepting over a failing binding wrote %v, want the write failure as unavailable", err)
		}

		row := rowOf(t, handle, issued.Invitation.ID)
		if row.Status != statusPending || row.AcceptedAt != nil {
			t.Errorf("the row reads status %d and accepted_at %v, want pending with no acceptance: "+
				"the claim rolled back with the binding", row.Status, row.AcceptedAt)
		}
		if got, ok, readErr := memberships.Membership(ctx, tenant, created.ID); readErr != nil || ok {
			t.Errorf("the membership reads %+v (%v, %v), want none: the insert rolled back with the binding",
				got, ok, readErr)
		}
	})
}
