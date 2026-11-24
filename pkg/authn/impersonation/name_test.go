package impersonation

import (
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestTheGrantAndAuditNameTheName checks that the grant views and audit detail name the tenant.
func TestTheGrantAndAuditNameTheName(t *testing.T) {
	grant := authn.ImpersonationGrant{
		ID: "g1", Target: "acct-user", Tenant: "t1",
		EndsAt: time.Date(2026, 3, 14, 12, 30, 0, 0, time.UTC),
	}
	if got := postedGrant(grant, "acme").Tenant; got != "acme" {
		t.Errorf("the posted grant names %q, want the name acme", got)
	}
	if got := currentGrant(grant, "acme").Tenant; got != "acme" {
		t.Errorf("the current grant names %q, want the name acme", got)
	}
	detail := startDetail(authn.Identity{}, authn.ImpersonationRequest{Tenant: "t1"}, "g1", time.Minute, "acme")
	if detail[detailTenant] != "acme" {
		t.Errorf("the audit detail names %q, want the name acme", detail[detailTenant])
	}
}
