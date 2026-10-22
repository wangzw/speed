package invite

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

const tenant = "tenant-a"

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
	row := h.row(issued.Invitation.ID)
	if row.TokenDigest != tokenDigest(issued.Token) {
		t.Errorf("stored digest = %q, want the SHA-256 of the token", row.TokenDigest)
	}
	if row.TokenDigest == issued.Token {
		t.Error("the token plaintext reached the table")
	}
}

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

func TestIssueSupersedesAPendingInvitation(t *testing.T) {
	h := newHarness(t)
	first := h.issue(tenant, "alice@example.com")
	h.clock.advance(time.Minute)
	second := h.issue(tenant, "ALICE@example.com")

	if got := h.row(first.Invitation.ID).Status; got != statusRevoked {
		t.Errorf("the superseded row's status = %d, want revoked", got)
	}
	if got := h.row(second.Invitation.ID).Status; got != statusPending {
		t.Errorf("the new row's status = %d, want pending", got)
	}
	if _, err := h.p.Accept(context.Background(), AcceptInvite{Token: first.Token, DisplayName: "Alice"}); !errors.Is(err, ErrInvitationRevoked) {
		t.Errorf("accepting the superseded token gave %v, want ErrInvitationRevoked", err)
	}
}

func TestAcceptWithAnExistingAccountAddsAMembershipOnly(t *testing.T) {
	h := newHarness(t)
	h.accounts.put("alice@example.com", "acct-alice")
	issued := h.issue(tenant, "alice@example.com")

	mem, err := h.p.Accept(context.Background(), AcceptInvite{Token: issued.Token, DisplayName: "ignored"})
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

// TestAcceptProvesAnExistingUnprovedAddress holds that the token proves the
// address it was delivered to: an account that holds it without proof is
// brought up to invite, and the membership follows.
func TestAcceptProvesAnExistingUnprovedAddress(t *testing.T) {
	h := newHarness(t)
	h.accounts.put("alice@example.com", "acct-alice")
	h.accounts.via = authn.VerifiedViaAdmin
	issued := h.issue(tenant, "alice@example.com")

	if _, err := h.p.Accept(context.Background(), AcceptInvite{Token: issued.Token}); err != nil {
		t.Fatalf("accepting: %v", err)
	}
	if h.admin.creates != 0 {
		t.Errorf("an account was created for an address that has one (%d creates)", h.admin.creates)
	}
	if len(h.admin.added) != 1 {
		t.Fatalf("AddIdentifier was called %d times, want once", len(h.admin.added))
	}
	got := h.admin.added[0]
	if got.Account != "acct-alice" || got.Kind != authn.IdentifierEmail ||
		got.Value != "alice@example.com" || got.Via.Via != VerifiedViaInvite {
		t.Errorf("AddIdentifier(%+v), want the address proved through invite", got)
	}
}

func TestAcceptCreatesAnAccountWhenTheAddressHasNone(t *testing.T) {
	h := newHarness(t)
	h.admin.newID = "acct-new"
	issued := h.issue(tenant, "Bob@Example.com")

	mem, err := h.p.Accept(context.Background(), AcceptInvite{Token: issued.Token, DisplayName: "Bob", Password: "correct horse"})
	if err != nil {
		t.Fatalf("accepting: %v", err)
	}
	if h.admin.creates != 1 {
		t.Fatalf("creates = %d, want one", h.admin.creates)
	}
	if h.admin.created.Email != "bob@example.com" {
		t.Errorf("created email = %q, want the normalized address", h.admin.created.Email)
	}
	if h.admin.created.EmailVerifiedVia.Via != VerifiedViaInvite {
		t.Errorf("created verification channel = %q, want %q", h.admin.created.EmailVerifiedVia.Via, VerifiedViaInvite)
	}
	if h.admin.created.Password != "correct horse" || h.admin.created.DisplayName != "Bob" {
		t.Errorf("created account = %+v, want the name and password from the acceptance", h.admin.created)
	}
	if mem.Account != "acct-new" || mem.Source != SourceInvite {
		t.Errorf("membership = %+v, want acct-new with source %q", mem, SourceInvite)
	}
}

func TestAcceptReportsTerminalStates(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.p.Accept(ctx, AcceptInvite{Token: ""}); !errors.Is(err, ErrInvitationNotFound) {
		t.Errorf("empty token gave %v, want ErrInvitationNotFound", err)
	}
	if _, err := h.p.Accept(ctx, AcceptInvite{Token: "spd_inv_nope"}); !errors.Is(err, ErrInvitationNotFound) {
		t.Errorf("unknown token gave %v, want ErrInvitationNotFound", err)
	}

	issued := h.issue(tenant, "dave@example.com")
	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); !errors.Is(err, ErrInvitationAlreadyAccepted) {
		t.Errorf("second accept gave %v, want ErrInvitationAlreadyAccepted", err)
	}

	revoked := h.issue(tenant, "erin@example.com")
	if err := h.p.Revoke(ctx, tenant, revoked.Invitation.ID); err != nil {
		t.Fatalf("revoking: %v", err)
	}
	if _, err := h.p.Accept(ctx, AcceptInvite{Token: revoked.Token}); !errors.Is(err, ErrInvitationRevoked) {
		t.Errorf("accepting a revoked token gave %v, want ErrInvitationRevoked", err)
	}
}

func TestAcceptRefusesAnExpiredToken(t *testing.T) {
	h := newHarness(t)
	deadline := h.clock.read().Add(time.Hour)
	ctx := context.Background()
	issued, err := h.p.Issue(ctx, IssueInvite{Tenant: tenant, Email: "frank@example.com", ExpiresAt: deadline})
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}
	h.clock.advance(2 * time.Hour)

	if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); !errors.Is(err, ErrInvitationExpired) {
		t.Errorf("accepting an expired token gave %v, want ErrInvitationExpired", err)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusPending {
		t.Errorf("an expired row's status = %d, want it left pending (expiry is not a stored state)", got)
	}
}

func TestAcceptSettlesATransientFailureBackToPending(t *testing.T) {
	h := newHarness(t)
	h.members.err = errors.New("the membership store is down")
	issued := h.issue(tenant, "grace@example.com")

	if _, err := h.p.Accept(context.Background(), AcceptInvite{Token: issued.Token}); !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("accepting over a failing membership store gave %v, want ErrUnavailable", err)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusPending {
		t.Errorf("status after a transient failure = %d, want back to pending", got)
	}
}

func TestAcceptSettlesAPermanentFailureAsRevoked(t *testing.T) {
	h := newHarness(t)
	h.admin.err = authn.ErrAccountExists
	issued := h.issue(tenant, "heidi@example.com")

	if _, err := h.p.Accept(context.Background(), AcceptInvite{Token: issued.Token}); !errors.Is(err, authn.ErrAccountExists) {
		t.Fatalf("accepting over a conflicting account gave %v, want ErrAccountExists", err)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusRevoked {
		t.Errorf("status after a permanent failure = %d, want revoked", got)
	}
}

// TestAcceptSettlesATakenContactAsRevoked holds that an address another account
// reserved or proved is a permanent failure like an existing account: the
// invitation is revoked rather than left pending for a retry that cannot work.
func TestAcceptSettlesATakenContactAsRevoked(t *testing.T) {
	h := newHarness(t)
	h.admin.err = authn.ErrContactTaken
	issued := h.issue(tenant, "judy@example.com")

	if _, err := h.p.Accept(context.Background(), AcceptInvite{Token: issued.Token}); !errors.Is(err, authn.ErrContactTaken) {
		t.Fatalf("accepting over a reserved contact gave %v, want ErrContactTaken", err)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusRevoked {
		t.Errorf("status after a taken contact = %d, want revoked", got)
	}
}

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
	if _, err := h.p.Accept(ctx, AcceptInvite{Token: accepted.Token}); err != nil {
		t.Fatalf("accepting: %v", err)
	}
	if err := h.p.Revoke(ctx, tenant, accepted.Invitation.ID); !errors.Is(err, ErrInvitationAlreadyAccepted) {
		t.Errorf("revoking an accepted invitation gave %v, want ErrInvitationAlreadyAccepted", err)
	}
}

func TestListOrdersPendingFirstAndPagesByCursor(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	// Three invitations in one tenant, the middle one accepted so that it falls
	// behind the two still pending.
	a := h.issue(tenant, "a@example.com")
	h.clock.advance(time.Minute)
	b := h.issue(tenant, "b@example.com")
	h.clock.advance(time.Minute)
	c := h.issue(tenant, "c@example.com")
	if _, err := h.p.Accept(ctx, AcceptInvite{Token: b.Token}); err != nil {
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

	// A page of one, then its cursor continues where it stopped.
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

func TestAcceptLeavesOneWinnerUnderConcurrency(t *testing.T) {
	h := newHarness(t)
	h.accounts.put("kate@example.com", "acct-kate")
	issued := h.issue(tenant, "kate@example.com")

	const racers = 4
	var wg sync.WaitGroup
	results := make([]error, racers)
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func(i int) {
			defer wg.Done()
			_, results[i] = h.p.Accept(context.Background(), AcceptInvite{Token: issued.Token})
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
