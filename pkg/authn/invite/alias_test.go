package invite

import (
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestTheInvitationViewsNameTheAlias holds the invitation's answer and the
// accepted membership's answer to the alias, never the internal identifier.
func TestTheInvitationViewsNameTheAlias(t *testing.T) {
	if got := invitationViewOf(Invitation{Tenant: "t1"}, "acme").Tenant; got != "acme" {
		t.Errorf("the invitation view names %q, want the alias acme", got)
	}
	if got := acceptViewOf(authn.Membership{Tenant: "t1"}, "acme").Tenant; got != "acme" {
		t.Errorf("the acceptance view names %q, want the alias acme", got)
	}
}
