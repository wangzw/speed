package invite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestIssueRefusesAnAddressThatIsAlreadyAMember checks that an active and a suspended member are both refused.
func TestIssueRefusesAnAddressThatIsAlreadyAMember(t *testing.T) {
	for _, active := range []bool{true, false} {
		name := "suspended"
		if active {
			name = "active"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.accounts.put("member@example.com", "acct-member")
			h.members.hold(authn.Membership{Tenant: tenant, Account: "acct-member", Active: active})

			_, err := h.p.Issue(context.Background(), IssueInvite{Tenant: tenant, Email: "member@example.com"})
			if !errors.Is(err, ErrInvitationMember) {
				t.Fatalf("issuing to a %s member gave %v, want ErrInvitationMember", name, err)
			}
			if got := h.rowCount(); got != 0 {
				t.Errorf("the refused issuance wrote %d rows, want none", got)
			}
		})
	}
}

// TestIssueRefusesAServiceAccountsAddress checks that a service account's address is refused.
func TestIssueRefusesAServiceAccountsAddress(t *testing.T) {
	h := newHarness(t)
	h.accounts.put("robot@example.com", "acct-robot")
	h.accounts.kinds["acct-robot"] = authn.KindService

	_, err := h.p.Issue(context.Background(), IssueInvite{Tenant: tenant, Email: "robot@example.com"})
	if !errors.Is(err, ErrInvitationTargetInvalid) {
		t.Fatalf("issuing to a service account gave %v, want ErrInvitationTargetInvalid", err)
	}
	if got := h.rowCount(); got != 0 {
		t.Errorf("the refused issuance wrote %d rows, want none", got)
	}
}

// TestIssueRefusesTheSystemTenant checks that the platform's own tenant is refused.
func TestIssueRefusesTheSystemTenant(t *testing.T) {
	h := newHarness(t)
	h.tenantDescs.byID[tenant] = authn.TenantDescription{Name: tenant, System: true}

	_, err := h.p.Issue(context.Background(), IssueInvite{Tenant: tenant, Email: "alice@example.com"})
	if !errors.Is(err, ErrInvitationTargetInvalid) {
		t.Fatalf("issuing into the system tenant gave %v, want ErrInvitationTargetInvalid", err)
	}
	if got := h.rowCount(); got != 0 {
		t.Errorf("the refused issuance wrote %d rows, want none", got)
	}
}

// TestIssueLimitsAnExplicitDeadlineToNinetyDays checks that a deadline of exactly 90 days is allowed and a minute past it is refused.
func TestIssueLimitsAnExplicitDeadlineToNinetyDays(t *testing.T) {
	t.Run("exactly ninety days", func(t *testing.T) {
		h := newHarness(t)
		deadline := h.clock.read().Add(maxExplicitLifetime)
		issued, err := h.p.Issue(context.Background(), IssueInvite{
			Tenant: tenant, Email: "alice@example.com", ExpiresAt: deadline,
		})
		if err != nil {
			t.Fatalf("issuing with a 90-day deadline gave %v, want it let through", err)
		}
		if got := issued.Invitation.ExpiresAt; !got.Equal(deadline) {
			t.Errorf("expires_at = %v, want %v", got, deadline)
		}
	})
	t.Run("a minute past ninety days", func(t *testing.T) {
		h := newHarness(t)
		deadline := h.clock.read().Add(maxExplicitLifetime).Add(time.Minute)
		_, err := h.p.Issue(context.Background(), IssueInvite{
			Tenant: tenant, Email: "alice@example.com", ExpiresAt: deadline,
		})
		if !errors.Is(err, ErrInvitationExpiryTooLong) {
			t.Fatalf("issuing with a deadline past 90 days gave %v, want ErrInvitationExpiryTooLong", err)
		}
		if got := h.rowCount(); got != 0 {
			t.Errorf("the refused issuance wrote %d rows, want none", got)
		}
	})
}

// TestTheIssueRouteAcceptsAnExplicitDeadlineAtNinetyDays checks that a 90-day deadline on the HTTP face answers 201.
func TestTheIssueRouteAcceptsAnExplicitDeadlineAtNinetyDays(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	deadline := h.clock.read().Add(maxExplicitLifetime)

	w := h.request(http.MethodPost, pathAdmin, map[string]any{
		"email":      "alice@example.com",
		"expires_at": deadline.Format(time.RFC3339),
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("a 90-day deadline answered %d, want 201: %s", w.Code, w.Body.String())
	}
	var answered issuedAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if !answered.Invitation.ExpiresAt.Equal(deadline) {
		t.Errorf("expires_at = %v, want %v", answered.Invitation.ExpiresAt, deadline)
	}
	if got := h.rowCount(); got != 1 {
		t.Errorf("rows = %d, want one", got)
	}
}

// TestTheIssueRouteAnswersTheEligibilityRefusals checks the problem code each eligibility refusal answers on the HTTP face.
func TestTheIssueRouteAnswersTheEligibilityRefusals(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*harness)
		email  string
		status int
		code   string
	}{
		{
			name: "a member",
			setup: func(h *harness) {
				h.accounts.put("member@example.com", "acct-member")
				h.members.hold(authn.Membership{Tenant: tenant, Account: "acct-member", Active: true})
			},
			email:  "member@example.com",
			status: 409,
			code:   "urn:speed:authn:invitation-member",
		},
		{
			name: "a service account",
			setup: func(h *harness) {
				h.accounts.put("robot@example.com", "acct-robot")
				h.accounts.kinds["acct-robot"] = authn.KindService
			},
			email:  "robot@example.com",
			status: 400,
			code:   "urn:speed:authn:invitation-target-invalid",
		},
		{
			name: "the system tenant",
			setup: func(h *harness) {
				h.tenantDescs.byID[tenant] = authn.TenantDescription{Name: tenant, System: true}
			},
			email:  "alice@example.com",
			status: 400,
			code:   "urn:speed:authn:invitation-target-invalid",
		},
		{
			name:   "a deadline past ninety days",
			setup:  func(*harness) {},
			email:  "alice@example.com",
			status: 400,
			code:   "urn:speed:authn:invitation-expiry-too-long",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			c.setup(h)
			h.asAdmin()
			body := map[string]any{"email": c.email}
			if c.code == "urn:speed:authn:invitation-expiry-too-long" {
				body["expires_at"] = h.clock.read().Add(maxExplicitLifetime).Add(time.Minute).Format(time.RFC3339)
			}
			w := h.request(http.MethodPost, pathAdmin, body)
			if w.Code != c.status {
				t.Fatalf("status = %d, want %d: %s", w.Code, c.status, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), c.code) {
				t.Errorf("body %s does not carry %s", w.Body.String(), c.code)
			}
			if got := h.rowCount(); got != 0 {
				t.Errorf("the refused issuance wrote %d rows, want none", got)
			}
		})
	}
}
