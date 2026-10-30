package selfservice_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// impersonating makes the next requests those of an administrator acting as the
// account inside one tenant, with a proof that would meet any requirement.
func (h *harness) impersonating() {
	h.t.Helper()
	h.as(append(localMulti(time.Second), authntest.WithActor("admin-1"), authntest.WithScope("tenant-a"))...)
}

// TestAnImpersonatingIdentityChangesTheEffectiveAccountsPassword holds that the
// routes act for the impersonated account instead of refusing the identity: the
// password written is the effective account's, the rotated session is the
// operator's own, and the sessions revoked as the others are the effective
// account's.
func TestAnImpersonatingIdentityChangesTheEffectiveAccountsPassword(t *testing.T) {
	h := newHarness(t)
	h.impersonating()
	w := h.put("/password", newPassword)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT /password while impersonating = %d %s, want 204", w.Code, w.Body)
	}
	if len(h.pw.sets) != 1 || h.pw.sets[0].account != account {
		t.Errorf("Set was called with %+v, want the effective account %q", h.pw.sets, account)
	}
	if len(h.sessions.rotated) != 1 || h.sessions.rotated[0] != session {
		t.Errorf("Rotate was called for %v, want the operator's session %q", h.sessions.rotated, session)
	}
	if len(h.sessions.revoked) != 1 || h.sessions.revoked[0] != (revokeCall{account, session, authn.ReasonPasswordChange}) {
		t.Errorf("RevokeAccount was called with %+v, want the effective account's other sessions", h.sessions.revoked)
	}
}

// TestNoSelfServiceRouteRefusesAnImpersonatingIdentity holds that the guard of
// every self-service route lets an impersonating identity through: the route
// then acts for the effective account or refuses for its own reason, and the
// freshness the effective account calls for is decided from that account. Only
// the routes that change how the account signs in read the factors; the contact
// registration routes ask for none.
func TestNoSelfServiceRouteRefusesAnImpersonatingIdentity(t *testing.T) {
	for _, tc := range mutating {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.richAccount()
			h.impersonating()
			w := h.do(tc.req)
			if w.Code == http.StatusForbidden && problemType(t, w) == "impersonation-restricted" {
				t.Fatalf("answer = %d %s, want the route to act for the effective account", w.Code, w.Body)
			}
			want := 0
			if tc.fresh {
				want = 1
			}
			if got := h.log.count("mfa.Factors"); got != want {
				t.Errorf("the guard's factor read ran %d times, want %d: calls = %v", got, want, h.log.names())
			}
		})
	}
}
