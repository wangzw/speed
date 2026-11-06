package admin

// The member read is the only tenant-addressed read that lists accounts, so it
// is the surface on which an ordinary member could learn who the tenant's
// members are. It asks membership.manage before reading; this case holds the
// permission boundary, and the acceptance report records that no view carries
// the owner's identity yet (authn.admin holds no tenant roster).

import (
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// An ordinary member without membership.manage is refused the member list, and
// the membership read is never reached. A tenant administrator, who holds the
// action, reads the same list.
func TestTheMemberListIsGatedByMembershipManage(t *testing.T) {
	t.Run("an ordinary member", func(t *testing.T) {
		r := newRig(t)
		r.decider.allow = false

		rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members", nil, fresh()...)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
		}
		last, ok := r.decider.last()
		if !ok || last.Action != authn.ActionMembershipManage || last.Tenant != tenantA {
			t.Errorf("the decision was %+v, want membership.manage in %q", last, tenantA)
		}
		if r.members.listCalls != 0 {
			t.Errorf("the membership read ran %d time(s) after the refusal, want none", r.members.listCalls)
		}
	})

	t.Run("a tenant administrator", func(t *testing.T) {
		r := newRig(t)
		r.decider.allow = true

		rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members", nil, fresh()...)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		if r.members.listCalls != 1 {
			t.Errorf("the membership read ran %d time(s), want one", r.members.listCalls)
		}
	})
}
