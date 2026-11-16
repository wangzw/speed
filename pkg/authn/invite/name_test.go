package invite

import (
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestTheInvitationViewsNameTheName holds the invitation's answer and the
// accepted membership's answer to the name, never the internal identifier.
func TestTheInvitationViewsNameTheName(t *testing.T) {
	if got := invitationViewOf(Invitation{Tenant: "t1"}, "acme", time.Time{}, inviterNames{}).Tenant; got != "acme" {
		t.Errorf("the invitation view names %q, want the name acme", got)
	}
	if got := acceptViewOf(Accepted{Membership: authn.Membership{Tenant: "t1"}}, "acme").Tenant; got != "acme" {
		t.Errorf("the acceptance view names %q, want the name acme", got)
	}
}
