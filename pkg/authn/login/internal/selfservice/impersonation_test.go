package selfservice_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/authntest"
)

// impersonating makes the next requests those of an administrator acting as the
// account inside one tenant, with a proof that would meet any requirement.
func (h *harness) impersonating() {
	h.t.Helper()
	h.as(append(localMulti(time.Second), authntest.WithActor("admin-1"), authntest.WithScope("tenant-a"))...)
}

// TestAnImpersonatingIdentityIsRefusedByEveryRouteBeforeAnyCall puts an
// impersonating identity in front of the handlers directly, which is what a route
// registered as impersonable by mistake would do. Each is a 403
// impersonation-restricted, and nothing downstream is touched: no factor read, no
// password written, no code sent, no store, no audit row, no event.
func TestAnImpersonatingIdentityIsRefusedByEveryRouteBeforeAnyCall(t *testing.T) {
	for _, tc := range mutating {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, func(c *config) { c.impersonable = true })
			h.impersonating()
			w := h.do(tc.req)
			if w.Code != http.StatusForbidden || problemType(t, w) != "impersonation-restricted" {
				t.Fatalf("answer = %d %s, want 403 impersonation-restricted", w.Code, w.Body)
			}
			if calls := h.log.all(); len(calls) != 0 {
				t.Errorf("an impersonating request made calls: %v", calls)
			}
			if rows, events := h.audit.entries(), h.bus.published(); len(rows) != 0 || len(events) != 0 {
				t.Errorf("an impersonating request wrote %v and published %v", rows, events)
			}
			if w.Header().Get("Set-Cookie") != "" {
				t.Error("an impersonating request was given a cookie")
			}
		})
	}
}

// TestTheImpersonationCheckComesBeforeTheFreshnessOne holds the order: an
// impersonating identity whose proof is also stale is told it is restricted, not
// asked to prove itself again, and the account's factors are not read for it.
func TestTheImpersonationCheckComesBeforeTheFreshnessOne(t *testing.T) {
	h := newHarness(t, func(c *config) { c.impersonable = true })
	h.activeFactor()
	h.as(append(single(time.Hour), authntest.WithActor("admin-1"), authntest.WithScope("tenant-a"))...)
	w := h.put("/password", `{"new_password":"x"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("answer = %d, want 403", w.Code)
	}
	if h.log.count("mfa.Factors") != 0 {
		t.Error("the factors were read for an impersonating identity")
	}
}

// TestTheGatewayRefusesAnImpersonatingIdentityOnEveryRouteButTheProfile holds the
// layering with the routes registered as they are: none of the five is
// impersonable, so the gateway's 401 answers before a handler is called.
func TestTheGatewayRefusesAnImpersonatingIdentityOnEveryRouteButTheProfile(t *testing.T) {
	for _, tc := range mutating {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.impersonating()
			w := h.do(tc.req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("answer = %d, want the gateway's 401", w.Code)
			}
			if calls := h.log.all(); len(calls) != 0 {
				t.Errorf("calls = %v", calls)
			}
		})
	}
}
