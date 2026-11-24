package invite

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

const tenant = "tenant-a"

// TestIssueNormalizesTheAddressAndStoresOnlyTheDigest checks that an address with surrounding space and capitals is stored normalized and only as a digest.
func TestIssueNormalizesTheAddressAndStoresOnlyTheDigest(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "  Alice@Example.COM ")

	if issued.Invitation.Email != "alice@example.com" {
		t.Errorf("stored address = %q, want the normalized alice@example.com", issued.Invitation.Email)
	}
	if !strings.HasPrefix(issued.Token, "spd_inv_") {
		t.Errorf("token %q does not carry the spd_inv_ prefix", issued.Token)
	}
	if issued.Invitation.Status != InvitationPending {
		t.Errorf("status = %v, want pending", issued.Invitation.Status)
	}
	if issued.Invitation.InvitedBy != adminID {
		t.Errorf("invited_by = %q, want the issuing administrator %q", issued.Invitation.InvitedBy, adminID)
	}
	if got, want := issued.Invitation.ExpiresAt, h.clock.read().Add(DefaultTTL); !got.Equal(want) {
		t.Errorf("expires_at = %v, want the configured default %v", got, want)
	}
	if got := issued.Invitation.SendCount; got != 1 {
		t.Errorf("send_count = %d, want the first issuance counted as one", got)
	}
	if got := issued.Invitation.LastSentAt; !got.Equal(h.clock.read()) {
		t.Errorf("last_sent_at = %v, want the issuance instant %v", got, h.clock.read())
	}
	row := h.row(issued.Invitation.ID)
	if row.TokenDigest != tokenDigest(issued.Token) {
		t.Errorf("stored digest = %q, want the SHA-256 of the token", row.TokenDigest)
	}
	if row.TokenDigest == issued.Token {
		t.Error("the token plaintext reached the table")
	}
}

// TestAnImpersonatedIssuanceNamesTheImpersonatedAccountAndSpendsTheOperatorsBudget holds the issuer the invitation records and the issuer budget it spends.
func TestAnImpersonatedIssuanceNamesTheImpersonatedAccountAndSpendsTheOperatorsBudget(t *testing.T) {
	h := newHarness(t)
	h.store.set(issueAddressPath, 100)
	h.store.set(issueIssuerPath, 1)
	h.store.set(issueGlobalPath, 100)
	ctx := authntest.Context(t, context.Background(),
		authntest.WithAccount("acct-target"),
		authntest.WithActor("acct-operator"),
		authntest.WithSession(authn.SessionID("session-1")),
		authntest.WithScope(tenant),
	)
	issued, err := h.p.Issue(ctx, IssueInvite{Tenant: tenant, Email: "alice@example.com"})
	if err != nil {
		t.Fatalf("issuing during impersonation: %v", err)
	}
	if got := issued.Invitation.InvitedBy; got != authn.AccountID("acct-target") {
		t.Errorf("invited_by = %q, want the impersonated account acct-target", got)
	}

	if got := h.budgetCount(budgetIssueIssuer, "acct-operator"); got != 1 {
		t.Errorf("the issuer count under the operator = %v, want 1", got)
	}
	if got := h.budgetCount(budgetIssueIssuer, "acct-target"); got != 0 {
		t.Errorf("the issuer count under the impersonated account = %v, want none", got)
	}

	again := authntest.Context(t, context.Background(),
		authntest.WithAccount("acct-other-target"),
		authntest.WithActor("acct-operator"),
		authntest.WithSession(authn.SessionID("session-2")),
		authntest.WithScope(tenant),
	)
	if _, err := h.p.Issue(again, IssueInvite{Tenant: tenant, Email: "bob@example.com"}); err == nil {
		t.Fatal("a second impersonation by the same operator was let through, want its spent allowance to throttle")
	} else {
		throttleOf(t, err)
	}

	own := authntest.Context(t, context.Background(), authntest.WithAccount("acct-target"))
	if _, err := h.p.Issue(own, IssueInvite{Tenant: tenant, Email: "carol@example.com"}); err != nil {
		t.Errorf("the impersonated account's own issuance gave %v, want its own allowance to let it through", err)
	}
}

// TestResendDoesNotRecheckTheTarget checks that a resend is not a new issuance: the target judged at issuance is not judged again.
func TestResendDoesNotRecheckTheTarget(t *testing.T) {
	cases := []struct {
		name   string
		change func(*harness)
	}{
		{
			name: "the target becomes a member",
			change: func(h *harness) {
				h.accounts.put("member@example.com", "acct-member")
				h.members.hold(authn.Membership{Tenant: tenant, Account: "acct-member", Active: true})
			},
		},
		{
			name: "the tenant becomes a system tenant",
			change: func(h *harness) {
				h.tenantDescs.byID[tenant] = authn.TenantDescription{Name: tenant, System: true}
			},
		},
		{
			name: "the target address becomes a service account's",
			change: func(h *harness) {
				h.accounts.put("member@example.com", "acct-service")
				h.accounts.kinds["acct-service"] = authn.KindService
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			issued := h.issue(tenant, "member@example.com")
			c.change(h)

			resent, err := h.p.Resend(context.Background(), tenant, issued.Invitation.ID)
			if err != nil {
				t.Fatalf("resending after %s gave %v, want it let through", c.name, err)
			}
			if got := resent.Invitation.SendCount; got != 2 {
				t.Errorf("send_count = %d, want 2", got)
			}
			if row := h.row(issued.Invitation.ID); row.TokenDigest != tokenDigest(resent.Token) {
				t.Error("the row does not hold the resent token's digest")
			}
		})
	}
}

// TestIssueRefusesAMalformedAddress checks that an empty, at-less, double-@, white-space or space-bearing address is ErrInvalidEmail.
func TestIssueRefusesAMalformedAddress(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for _, bad := range []string{"", "no-at-sign", "two@@example.com", "  ", "a b@example.com"} {
		if _, err := h.p.Issue(ctx, IssueInvite{Tenant: tenant, Email: bad}); !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("issuing %q gave %v, want ErrInvalidEmail", bad, err)
		}
	}
	var n int64
	if err := h.handle.Model(&invitationRow{}).Count(&n).Error; err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	if n != 0 {
		t.Errorf("%d rows were written for refused addresses, want none", n)
	}
}

// TestIssueReplacesAnExpiredInvitation checks that an issuance over an expired invitation supersedes it and takes the live slot.
func TestIssueReplacesAnExpiredInvitation(t *testing.T) {
	h := newHarness(t)
	first := h.issue(tenant, "alice@example.com")
	h.clock.advance(DefaultTTL + time.Minute)
	second := h.issue(tenant, "ALICE@example.com")

	old := h.row(first.Invitation.ID)
	if old.Status != statusPending {
		t.Errorf("the replaced row's status = %d, want pending so it still reads expired", old.Status)
	}
	if old.SupersededAt == nil {
		t.Error("the replaced row carries no superseded_at")
	}
	if got := h.row(second.Invitation.ID).Status; got != statusPending {
		t.Errorf("the new row's status = %d, want pending", got)
	}
	if _, state, err := h.p.viewToken(context.Background(), first.Token); err != nil || state != viewStateExpired {
		t.Errorf("the replaced token reads %q (%v), want expired", state, err)
	}
	if _, state, err := h.p.viewToken(context.Background(), second.Token); err != nil || state != viewStatePending {
		t.Errorf("the new token reads %q (%v), want pending", state, err)
	}
}

// TestAcceptWithAnExistingAccountAddsAMembershipOnly checks that an existing account's acceptance adds a membership and ignores the display name.
func TestAcceptWithAnExistingAccountAddsAMembershipOnly(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	ctx := h.acceptCtx("acct-alice", "alice@example.com")

	mem, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token, DisplayName: "ignored"})
	if err != nil {
		t.Fatalf("accepting: %v", err)
	}
	if h.admin.creates != 0 {
		t.Errorf("an account was created for an address that has one (%d creates)", h.admin.creates)
	}
	if mem.Account != "acct-alice" || mem.Tenant != tenant || !mem.Active || mem.Source != SourceInvite {
		t.Errorf("membership = %+v, want acct-alice active in %s with source %q", mem, tenant, SourceInvite)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusAccepted {
		t.Errorf("row status = %d, want accepted", got)
	}
}

// TestAcceptRefusesAnUnprovedInvitedAddress checks that holding the token but not a proven address is refused and leaves the invitation pending.
func TestAcceptRefusesAnUnprovedInvitedAddress(t *testing.T) {
	h := newHarness(t)
	h.accounts.via = authn.VerifiedViaAdmin
	issued := h.issue(tenant, "alice@example.com")
	ctx := h.acceptCtx("acct-alice", "alice@example.com")

	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); !errors.Is(err, ErrInvitationEmailUnproved) {
		t.Fatalf("accepting without a proof gave %v, want ErrInvitationEmailUnproved", err)
	}
	if h.members.count != 0 {
		t.Errorf("a membership was written without a proof")
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusPending {
		t.Errorf("row status = %d, want pending so the caller can prove the address and retry", got)
	}
}

// TestAcceptRefusesAnAccountThatDoesNotHoldTheInvitedAddress checks that a caller proving another address is refused and creates no account.
func TestAcceptRefusesAnAccountThatDoesNotHoldTheInvitedAddress(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "bob@example.com")
	ctx := h.acceptCtx("acct-alice", "alice@example.com")

	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); !errors.Is(err, ErrInvitationEmailUnproved) {
		t.Fatalf("accepting with another address gave %v, want ErrInvitationEmailUnproved", err)
	}
	if h.admin.creates != 0 {
		t.Errorf("the acceptance created %d account(s), want none", h.admin.creates)
	}
	if h.members.count != 0 {
		t.Errorf("a membership was written for an account that does not hold the address")
	}
}

// TestAcceptRefusesARequestWithoutAnIdentity checks that the token alone is no credential and an unauthenticated request is refused.
func TestAcceptRefusesARequestWithoutAnIdentity(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	h.accounts.put("alice@example.com", "acct-alice")

	if _, err := h.p.Accept(context.Background(), AcceptInvite{Token: issued.Token}); !errors.Is(err, ErrInvitationUnauthenticated) {
		t.Fatalf("accepting without an identity gave %v, want ErrInvitationUnauthenticated", err)
	}
	if h.members.count != 0 {
		t.Errorf("a membership was written without an identity")
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusPending {
		t.Errorf("row status = %d, want pending", got)
	}
}

// TestTheInviterCannotAcceptOnTheInviteesBehalf checks that the issuer holding the token cannot accept for the invitee.
func TestTheInviterCannotAcceptOnTheInviteesBehalf(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "invitee@example.com")
	ctx := h.acceptCtx(adminID, "operator@example.com")

	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); !errors.Is(err, ErrInvitationEmailUnproved) {
		t.Fatalf("the issuer accepted its own invitation with %v, want ErrInvitationEmailUnproved", err)
	}
	if h.members.count != 0 {
		t.Errorf("the issuer wrote %d membership(s) for an address it does not hold", h.members.count)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusPending {
		t.Errorf("row status = %d, want pending for the holder of the address", got)
	}
}

// TestAcceptKeepsAnExistingMembershipsSourceAndExemption checks that an existing active membership keeps its source and SSO exemption.
func TestAcceptKeepsAnExistingMembershipsSourceAndExemption(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	ctx := h.acceptCtx("acct-alice", "alice@example.com")
	h.members.hold(authn.Membership{
		Account: "acct-alice", Tenant: tenant, Active: true, Source: "admin", SSOExempt: true,
	})

	mem, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token})
	if err != nil {
		t.Fatalf("accepting with an active membership: %v", err)
	}
	if mem.Source != "admin" || !mem.SSOExempt || !mem.Active {
		t.Errorf("membership = %+v, want the existing source and SSO exemption preserved", mem)
	}
	if len(h.members.puts) != 1 || h.members.puts[0].Source != "admin" || !h.members.puts[0].SSOExempt {
		t.Errorf("the write = %+v, want the existing source and SSO exemption", h.members.puts)
	}
}

// TestAcceptRefusesASuspendedMembershipAndRevokesTheInvitation checks that a suspended member is refused and the invitation revoked.
func TestAcceptRefusesASuspendedMembershipAndRevokesTheInvitation(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	ctx := h.acceptCtx("acct-alice", "alice@example.com")
	h.members.hold(authn.Membership{Account: "acct-alice", Tenant: tenant, Active: false, Source: "admin"})

	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); !errors.Is(err, ErrInvitationMembershipInactive) {
		t.Fatalf("accepting over a suspended membership gave %v, want ErrInvitationMembershipInactive", err)
	}
	if h.members.count != 0 {
		t.Errorf("the acceptance wrote %d membership(s), want none", h.members.count)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusRevoked {
		t.Errorf("row status = %d, want revoked so the token cannot be retried", got)
	}
	assertRevokedAudit(t, h, issued.Invitation.ID, authn.ReasonInvitationMembershipInactive)
}

// TestAcceptReportsTerminalStates checks that an empty or unknown token is not found, a second accept is already accepted and a revoked token is revoked.
func TestAcceptReportsTerminalStates(t *testing.T) {
	h := newHarness(t)
	ctx := h.acceptCtx("acct-dave", "dave@example.com")

	if _, err := h.p.Accept(ctx, AcceptInvite{Token: ""}); !errors.Is(err, ErrInvitationNotFound) {
		t.Errorf("empty token gave %v, want ErrInvitationNotFound", err)
	}
	if _, err := h.p.Accept(ctx, AcceptInvite{Token: "spd_inv_nope"}); !errors.Is(err, ErrInvitationNotFound) {
		t.Errorf("unknown token gave %v, want ErrInvitationNotFound", err)
	}

	issued := h.issue(tenant, "dave@example.com")
	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token, Username: "dave"}); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token, Username: "dave"}); !errors.Is(err, ErrInvitationAlreadyAccepted) {
		t.Errorf("second accept gave %v, want ErrInvitationAlreadyAccepted", err)
	}

	revoked := h.issue(tenant, "erin@example.com")
	if err := h.p.Revoke(ctx, tenant, revoked.Invitation.ID); err != nil {
		t.Fatalf("revoking: %v", err)
	}
	erin := h.acceptCtx("acct-erin", "erin@example.com")
	if _, err := h.p.Accept(erin, AcceptInvite{Token: revoked.Token}); !errors.Is(err, ErrInvitationRevoked) {
		t.Errorf("accepting a revoked token gave %v, want ErrInvitationRevoked", err)
	}
}

// TestAcceptRefusesAnExpiredToken checks that a token past its deadline is ErrInvitationExpired and the row is left pending.
func TestAcceptRefusesAnExpiredToken(t *testing.T) {
	h := newHarness(t)
	deadline := h.clock.read().Add(time.Hour)
	issueCtx := authntest.Context(h.t, context.Background(), authntest.WithAccount(adminID))
	issued, err := h.p.Issue(issueCtx, IssueInvite{Tenant: tenant, Email: "frank@example.com", ExpiresAt: deadline})
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}
	h.clock.advance(2 * time.Hour)

	ctx := h.acceptCtx("acct-frank", "frank@example.com")
	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); !errors.Is(err, ErrInvitationExpired) {
		t.Errorf("accepting an expired token gave %v, want ErrInvitationExpired", err)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusPending {
		t.Errorf("an expired row's status = %d, want it left pending (expiry is not a stored state)", got)
	}
}

// TestAcceptRollsBackWhenTheMembershipWriteFails checks that a failed membership write rolls the claim back.
func TestAcceptRollsBackWhenTheMembershipWriteFails(t *testing.T) {
	h := newHarness(t)
	h.members.err = errors.New("the membership store is down")
	issued := h.issue(tenant, "grace@example.com")
	ctx := h.acceptCtx("acct-grace", "grace@example.com")

	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("accepting over a failing membership store gave %v, want ErrUnavailable", err)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusPending {
		t.Errorf("status after a failed membership write = %d, want pending: the claim rolled back", got)
	}
}

// TestAcceptLeavesTheInvitationPendingOnAPermanentWriteFailure checks that a permanent write failure leaves the invitation pending.
func TestAcceptLeavesTheInvitationPendingOnAPermanentWriteFailure(t *testing.T) {
	h := newHarness(t)
	h.members.err = authn.ErrBindingConflict
	issued := h.issue(tenant, "heidi@example.com")
	ctx := h.acceptCtx("acct-heidi", "heidi@example.com")

	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); !errors.Is(err, authn.ErrBindingConflict) {
		t.Fatalf("accepting over a conflicting binding gave %v, want ErrBindingConflict", err)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusPending {
		t.Errorf("status after a refused write = %d, want pending: the claim rolled back", got)
	}
	if h.members.count != 0 {
		t.Errorf("the failed acceptance wrote %d membership(s), want none", h.members.count)
	}
}

// TestAcceptRollsBackATakenContact checks that a taken contact leaves the invitation pending.
func TestAcceptRollsBackATakenContact(t *testing.T) {
	h := newHarness(t)
	h.members.err = authn.ErrContactTaken
	issued := h.issue(tenant, "judy@example.com")
	ctx := h.acceptCtx("acct-judy", "judy@example.com")

	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); !errors.Is(err, authn.ErrContactTaken) {
		t.Fatalf("accepting over a reserved contact gave %v, want ErrContactTaken", err)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusPending {
		t.Errorf("status after a taken contact = %d, want pending", got)
	}
}

// TestAcceptPropagatesACleanupIncomplete checks that a cleanup failure is propagated as it is and leaves the invitation pending.
func TestAcceptPropagatesACleanupIncomplete(t *testing.T) {
	h := newHarness(t)
	h.members.err = authn.ErrCleanupIncomplete
	issued := h.issue(tenant, "karl@example.com")
	ctx := h.acceptCtx("acct-karl", "karl@example.com")

	_, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token})
	if !errors.Is(err, authn.ErrCleanupIncomplete) {
		t.Fatalf("accepting over a failing cleanup gave %v, want ErrCleanupIncomplete", err)
	}
	if errors.Is(err, authn.ErrUnavailable) {
		t.Error("the cleanup failure was merged into ErrUnavailable instead of being propagated")
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusPending {
		t.Errorf("status after a cleanup failure = %d, want pending: the retry finishes the cleanup", got)
	}
}

// A transient membership failure does not spend the invitation: once the store answers, the same token redeems for the same caller.
func TestAcceptRetriesAfterATransientFailure(t *testing.T) {
	h := newHarness(t)
	h.members.err = authn.ErrCleanupIncomplete
	issued := h.issue(tenant, "lena@example.com")
	ctx := h.acceptCtx("acct-lena", "lena@example.com")
	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); !errors.Is(err, authn.ErrCleanupIncomplete) {
		t.Fatalf("the first acceptance gave %v, want ErrCleanupIncomplete", err)
	}

	h.members.err = nil
	mem, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token})
	if err != nil {
		t.Fatalf("retrying the acceptance: %v", err)
	}
	if mem.Account != "acct-lena" {
		t.Errorf("the retry accepted for %q, want acct-lena", mem.Account)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusAccepted {
		t.Errorf("status after the retry = %d, want accepted", got)
	}
	if h.members.count != 1 {
		t.Errorf("memberships written = %d, want one", h.members.count)
	}
}

// TestAcceptDoesNotResurrectAMembershipSuspendedDuringTheWrite checks that a membership suspended mid-write is not resurrected.
func TestAcceptDoesNotResurrectAMembershipSuspendedDuringTheWrite(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "mia@example.com")
	ctx := h.acceptCtx("acct-mia", "mia@example.com")
	h.members.hold(authn.Membership{Account: "acct-mia", Tenant: tenant, Active: true, Source: "admin"})
	h.members.duringJoin = func() {
		h.members.hold(authn.Membership{Account: "acct-mia", Tenant: tenant, Active: false, Source: "admin"})
	}

	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); !errors.Is(err, ErrInvitationMembershipInactive) {
		t.Fatalf("accepting over a membership suspended mid-write gave %v, want ErrInvitationMembershipInactive", err)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusRevoked {
		t.Errorf("status after the lost race = %d, want revoked", got)
	}
	if h.members.count != 0 {
		t.Errorf("the lost race wrote %d membership(s), want none", h.members.count)
	}
	held, ok, err := h.members.Membership(context.Background(), tenant, "acct-mia")
	if err != nil || !ok || held.Active {
		t.Errorf("the membership reads %+v (%v, %v), want it still suspended", held, ok, err)
	}
}

// TestAFailedAcceptanceLeavesTheLiveRowPending checks that a failed acceptance leaves the live row pending and the superseded row expired.
func TestAFailedAcceptanceLeavesTheLiveRowPending(t *testing.T) {
	h := newHarness(t)
	first := h.issue(tenant, "nora@example.com")
	h.clock.advance(DefaultTTL + time.Minute)
	live := h.issue(tenant, "nora@example.com")
	ctx := h.acceptCtx("acct-nora", "nora@example.com")
	h.members.err = errors.New("the membership store is down")

	if _, err := h.p.Accept(ctx, AcceptInvite{Token: live.Token}); !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("accepting over a failing membership store gave %v, want ErrUnavailable", err)
	}
	if got := h.row(live.Invitation.ID).Status; got != statusPending {
		t.Errorf("the live row's status = %d, want pending", got)
	}
	old := h.row(first.Invitation.ID)
	if old.Status != statusPending {
		t.Errorf("the superseded row's stored status = %d, want pending so it still reads expired", old.Status)
	}
	if old.SupersededAt == nil {
		t.Error("the superseded row lost its superseded_at")
	}
	if _, state, err := h.p.viewToken(context.Background(), first.Token); err != nil || state != viewStateExpired {
		t.Errorf("the superseded token reads %q (%v), want expired", state, err)
	}
}

// A disabled tenant refuses acceptance and the invitation survives: enabling the tenant makes the same token redeemable again.
func TestAcceptRefusesADisabledTenantAndKeepsTheInvitation(t *testing.T) {
	h := newHarness(t)
	h.tenantStates.states[tenant] = authn.TenantDisabled
	issued := h.issue(tenant, "olga@example.com")
	ctx := h.acceptCtx("acct-olga", "olga@example.com")

	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); !errors.Is(err, ErrInvitationTenantInactive) {
		t.Fatalf("accepting into a disabled tenant gave %v, want ErrInvitationTenantInactive", err)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusPending {
		t.Errorf("status into a disabled tenant = %d, want pending", got)
	}
	if h.members.count != 0 {
		t.Errorf("a disabled tenant accepted %d membership(s), want none", h.members.count)
	}

	h.tenantStates.states[tenant] = authn.TenantServing
	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); err != nil {
		t.Fatalf("accepting after the tenant was enabled again: %v", err)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusAccepted {
		t.Errorf("status after re-enabling = %d, want accepted", got)
	}
}

// A deleted tenant refuses acceptance and its live row is withdrawn, so a token the deletion's cleanup missed cannot be redeemed later.
func TestAcceptRefusesADeletedTenantAndWithdrawsTheRow(t *testing.T) {
	h := newHarness(t)
	h.tenantStates.states[tenant] = authn.TenantDeleted
	issued := h.issue(tenant, "pete@example.com")
	ctx := h.acceptCtx("acct-pete", "pete@example.com")

	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); !errors.Is(err, ErrInvitationNotFound) {
		t.Fatalf("accepting into a deleted tenant gave %v, want ErrInvitationNotFound", err)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusRevoked {
		t.Errorf("status into a deleted tenant = %d, want revoked", got)
	}
	if h.members.count != 0 {
		t.Errorf("a deleted tenant accepted %d membership(s), want none", h.members.count)
	}
	assertRevokedAudit(t, h, issued.Invitation.ID, authn.ReasonInvitationTenantDeleted)
}

// TestRevokeTenantInvitationsWithdrawsOnlyThatTenantsPendingRows checks that tenant revocation withdraws only that tenant’s pending invitations.
func TestRevokeTenantInvitationsWithdrawsOnlyThatTenantsPendingRows(t *testing.T) {
	h := newHarness(t)
	kept := h.issue(tenant, "quinn@example.com")
	other := h.issue("tenant-b", "quinn@example.com")
	accepted := h.issue(tenant, "rae@example.com")
	if err := h.handle.Model(&invitationRow{}).Where("id = ?", accepted.Invitation.ID).
		Update("status", statusAccepted).Error; err != nil {
		t.Fatalf("accepting the row by hand: %v", err)
	}

	if err := h.p.RevokeTenantInvitations(context.Background(), tenant); err != nil {
		t.Fatalf("revoking the tenant's invitations: %v", err)
	}
	if got := h.row(kept.Invitation.ID).Status; got != statusRevoked {
		t.Errorf("the tenant's pending row = %d, want revoked", got)
	}
	if got := h.row(other.Invitation.ID).Status; got != statusPending {
		t.Errorf("another tenant's row = %d, want pending", got)
	}
	if got := h.row(accepted.Invitation.ID).Status; got != statusAccepted {
		t.Errorf("the accepted row = %d, want accepted", got)
	}
}

// TestRevokeIsIdempotentAndRefusesAnAcceptedInvitation checks that a repeated revoke succeeds and an accepted invitation is refused with its own error.
func TestRevokeIsIdempotentAndRefusesAnAcceptedInvitation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	issued := h.issue(tenant, "ivan@example.com")
	if err := h.p.Revoke(ctx, tenant, issued.Invitation.ID); err != nil {
		t.Fatalf("revoking: %v", err)
	}
	if err := h.p.Revoke(ctx, tenant, issued.Invitation.ID); err != nil {
		t.Errorf("revoking an already-revoked invitation gave %v, want nil", err)
	}
	if err := h.p.Revoke(ctx, tenant, "no-such-id"); !errors.Is(err, ErrInvitationNotFound) {
		t.Errorf("revoking an unknown identifier gave %v, want ErrInvitationNotFound", err)
	}

	accepted := h.issue(tenant, "jane@example.com")
	jane := h.acceptCtx("acct-jane", "jane@example.com")
	if _, err := h.p.Accept(jane, AcceptInvite{Token: accepted.Token}); err != nil {
		t.Fatalf("accepting: %v", err)
	}
	if err := h.p.Revoke(ctx, tenant, accepted.Invitation.ID); !errors.Is(err, ErrInvitationAlreadyAccepted) {
		t.Errorf("revoking an accepted invitation gave %v, want ErrInvitationAlreadyAccepted", err)
	}
}

// TestListOrdersPendingFirstAndPagesByCursor checks that pending invitations come newest-first ahead of accepted ones and that a limit pages by cursor.
func TestListOrdersPendingFirstAndPagesByCursor(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.issue(tenant, "a@example.com")
	h.clock.advance(time.Minute)
	b := h.issue(tenant, "b@example.com")
	h.clock.advance(time.Minute)
	c := h.issue(tenant, "c@example.com")
	brand := h.acceptCtx("acct-brand", "b@example.com")
	if _, err := h.p.Accept(brand, AcceptInvite{Token: b.Token}); err != nil {
		t.Fatalf("accepting: %v", err)
	}

	page, next, err := h.p.List(ctx, tenant, authn.Page{})
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	want := []string{c.Invitation.ID, a.Invitation.ID, b.Invitation.ID}
	if len(page) != len(want) {
		t.Fatalf("page holds %d, want %d", len(page), len(want))
	}
	for i, id := range want {
		if page[i].ID != id {
			t.Errorf("page[%d] = %s, want %s (pending newest-first, then accepted)", i, page[i].ID, id)
		}
	}
	if next != "" {
		t.Errorf("next cursor = %q, want empty on a short page", next)
	}

	first, cursor, err := h.p.List(ctx, tenant, authn.Page{Limit: 1})
	if err != nil {
		t.Fatalf("listing one: %v", err)
	}
	if len(first) != 1 || first[0].ID != c.Invitation.ID {
		t.Fatalf("first page = %+v, want just %s", first, c.Invitation.ID)
	}
	if cursor == "" {
		t.Fatal("a full page left no cursor")
	}
	second, _, err := h.p.List(ctx, tenant, authn.Page{Limit: 1, After: cursor})
	if err != nil {
		t.Fatalf("listing the second page: %v", err)
	}
	if len(second) != 1 || second[0].ID != a.Invitation.ID {
		t.Fatalf("second page = %+v, want just %s", second, a.Invitation.ID)
	}
}

// TestAcceptLeavesOneWinnerUnderConcurrency checks that four concurrent acceptances leave one winner, the rest already-accepted, and one membership.
func TestAcceptLeavesOneWinnerUnderConcurrency(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "kate@example.com")
	ctx := h.acceptCtx("acct-kate", "kate@example.com")

	const racers = 4
	var wg sync.WaitGroup
	results := make([]error, racers)
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func(i int) {
			defer wg.Done()
			_, results[i] = h.p.Accept(ctx, AcceptInvite{Token: issued.Token})
		}(i)
	}
	wg.Wait()

	wins := 0
	for i, err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrInvitationAlreadyAccepted):
		default:
			t.Errorf("racer %d failed with %v, want nil or ErrInvitationAlreadyAccepted", i, err)
		}
	}
	if wins != 1 {
		t.Errorf("%d racers won, want exactly one", wins)
	}
	if h.members.count != 1 {
		t.Errorf("%d memberships were written, want exactly one", h.members.count)
	}
}
