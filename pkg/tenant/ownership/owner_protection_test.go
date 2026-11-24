package ownership_test

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

func TestAnAccountOwningAnOrganizationCannotBeDeletedOrSuspended(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		a.organization(t, "acme", "acme-org", owner)

		if err := a.admin.SetStatus(t.Context(), owner.ID, authn.StatusDisabled, authn.ReasonAdmin); err == nil {
			t.Fatal("suspending the owner succeeded")
		} else {
			var refusal *authn.AccountOwnsOrganizationError
			if !errors.As(err, &refusal) || refusal.Tenant != "acme" {
				t.Fatalf("the suspension gave %v, want the organization-owner refusal naming acme", err)
			}
		}
		if err := a.admin.Delete(t.Context(), owner.ID, authn.ReasonAdmin); err == nil {
			t.Fatal("deleting the owner succeeded")
		} else {
			var refusal *authn.AccountOwnsOrganizationError
			if !errors.As(err, &refusal) || refusal.Tenant != "acme" {
				t.Fatalf("the deletion gave %v, want the organization-owner refusal naming acme", err)
			}
		}
		if got, err := a.accounts.Get(t.Context(), owner.ID); err != nil || got.Status != authn.StatusActive {
			t.Errorf("the refused operations changed the account: %+v (%v)", got, err)
		}
	})
}
