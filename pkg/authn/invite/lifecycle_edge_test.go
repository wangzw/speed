package invite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestTheDeadlineBoundaryIsTerminalForRevokeAndResend holds the line the clock is
// judged on: an invitation at exactly its deadline is expired, because a pending
// invitation is redeemable only while the deadline is still ahead. Revoke and
// resend refuse it, the management face answers the same 409 invitation-terminal
// as for any expired row, and the row keeps its token, its revocation column and
// its send record.
func TestTheDeadlineBoundaryIsTerminalForRevokeAndResend(t *testing.T) {
	h := newHarness(t)
	deadline := h.clock.read().Add(time.Hour)
	issued, err := h.p.Issue(context.Background(), IssueInvite{
		Tenant: tenant, Email: "alice@example.com", ExpiresAt: deadline,
	})
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}
	before := h.row(issued.Invitation.ID)

	// Exactly at the deadline: not one instant past it.
	h.clock.advance(time.Hour)

	if err := h.p.Revoke(context.Background(), tenant, issued.Invitation.ID); !errors.Is(err, ErrInvitationExpired) {
		t.Errorf("revoking at the deadline gave %v, want ErrInvitationExpired", err)
	}
	if _, err := h.p.Resend(context.Background(), tenant, issued.Invitation.ID); !errors.Is(err, ErrInvitationExpired) {
		t.Errorf("resending at the deadline gave %v, want ErrInvitationExpired", err)
	}
	after := h.row(issued.Invitation.ID)
	if after.Status != statusPending {
		t.Errorf("status = %d, want it left pending so it reads expired", after.Status)
	}
	if after.RevokedAt != nil {
		t.Errorf("revoked_at = %v, want the refused revoke to leave it NULL", after.RevokedAt)
	}
	if after.TokenDigest != before.TokenDigest {
		t.Error("the refused resend replaced the token digest")
	}
	if after.SendCount != before.SendCount {
		t.Errorf("send_count = %d, want the unchanged %d", after.SendCount, before.SendCount)
	}

	h.asAdmin()
	for _, request := range []struct {
		method string
		path   string
	}{
		{http.MethodDelete, pathAdmin + "/" + issued.Invitation.ID},
		{http.MethodPost, pathAdmin + "/" + issued.Invitation.ID + "/resend"},
	} {
		w := h.request(request.method, request.path, nil)
		if w.Code != http.StatusConflict {
			t.Errorf("%s %s answered %d, want 409: %s", request.method, request.path, w.Code, w.Body.String())
			continue
		}
		if !strings.Contains(w.Body.String(), "urn:speed:authn:invitation-terminal") {
			t.Errorf("%s %s body %s does not carry invitation-terminal", request.method, request.path, w.Body.String())
		}
	}
}

// TestAnInvitationAtItsDeadlineIsSuperseded holds the other side of the same
// boundary: an issuance over a row whose deadline has just arrived finds the live
// slot free, records the replacement in superseded_at on the old row, and inserts
// the new one. The old row keeps its stored pending state and its token still
// reads expired; the new token reads pending.
func TestAnInvitationAtItsDeadlineIsSuperseded(t *testing.T) {
	h := newHarness(t)
	deadline := h.clock.read().Add(time.Hour)
	first, err := h.p.Issue(context.Background(), IssueInvite{
		Tenant: tenant, Email: "alice@example.com", ExpiresAt: deadline,
	})
	if err != nil {
		t.Fatalf("issuing the first invitation: %v", err)
	}

	h.clock.advance(time.Hour)
	second, err := h.p.Issue(context.Background(), IssueInvite{
		Tenant: tenant, Email: "alice@example.com", ExpiresAt: h.clock.read().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("issuing at the first invitation's deadline: %v", err)
	}

	old := h.row(first.Invitation.ID)
	if old.Status != statusPending {
		t.Errorf("the replaced row's status = %d, want pending so it still reads expired", old.Status)
	}
	if old.SupersededAt == nil {
		t.Fatal("the replaced row carries no superseded_at")
	}
	if !old.SupersededAt.Equal(h.clock.read()) {
		t.Errorf("superseded_at = %v, want the issuance instant %v", old.SupersededAt, h.clock.read())
	}
	if old.RevokedAt != nil {
		t.Errorf("the replaced row was revoked at %v, want it left as expired", old.RevokedAt)
	}
	if got := h.row(second.Invitation.ID).Status; got != statusPending {
		t.Errorf("the new row's status = %d, want pending", got)
	}
	if got := h.rowCount(); got != 2 {
		t.Errorf("rows = %d, want the old and the new one", got)
	}
	if _, state, err := h.p.viewToken(context.Background(), first.Token); err != nil || state != viewStateExpired {
		t.Errorf("the replaced token reads %q (%v), want expired", state, err)
	}
	if _, state, err := h.p.viewToken(context.Background(), second.Token); err != nil || state != viewStatePending {
		t.Errorf("the new token reads %q (%v), want pending", state, err)
	}
}

// TestTheMaskedAddressKeepsTheDomainAndOneLocalCharacter holds the server-side
// mask every caller of the public read sees: one local character, then the whole
// domain. A single-character local part keeps that one character; an empty local
// part is the mask alone; and an address with no domain, which normalization
// never produces, masks to nothing rather than to a guessed shape.
func TestTheMaskedAddressKeepsTheDomainAndOneLocalCharacter(t *testing.T) {
	for _, c := range []struct {
		email string
		want  string
	}{
		{"alice@example.com", "a***@example.com"},
		{"a@example.com", "a***@example.com"},
		{"@example.com", "***@example.com"},
		{"no-at-sign", ""},
		{"alice@", ""},
	} {
		if got := maskedEmail(c.email); got != c.want {
			t.Errorf("maskedEmail(%q) = %q, want %q", c.email, got, c.want)
		}
	}
}

// TestTheListShowsWhenAnInvitationWasRevoked holds the listing's revoked_at
// mapping: a revoked row carries the moment it was revoked, and a pending row
// carries none. Both are read through the route's own answer, which is the shape
// a client sees.
func TestTheListShowsWhenAnInvitationWasRevoked(t *testing.T) {
	h := newHarness(t)
	pending := h.issue(tenant, "pending@example.com")
	revoked := h.issue(tenant, "revoked@example.com")
	revokedAt := h.clock.read()
	if err := h.p.Revoke(context.Background(), tenant, revoked.Invitation.ID); err != nil {
		t.Fatalf("revoking: %v", err)
	}

	h.asAdmin()
	w := h.request(http.MethodGet, pathAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing answered %d, want 200: %s", w.Code, w.Body.String())
	}
	var page AuthnInvitePage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decoding the page: %v", err)
	}
	views := make(map[string]AuthnInviteInvitation, len(page.Invitations))
	for _, view := range page.Invitations {
		views[view.ID] = view
	}

	got, ok := views[revoked.Invitation.ID]
	if !ok {
		t.Fatalf("the page does not carry the revoked invitation %q", revoked.Invitation.ID)
	}
	if got.Status != viewStateRevoked {
		t.Errorf("the revoked view's status = %q, want %q", got.Status, viewStateRevoked)
	}
	if got.RevokedAt == nil {
		t.Fatal("the revoked view carries no revoked_at")
	}
	if !got.RevokedAt.Equal(revokedAt) {
		t.Errorf("revoked_at = %v, want the revocation instant %v", got.RevokedAt, revokedAt)
	}

	live, ok := views[pending.Invitation.ID]
	if !ok {
		t.Fatalf("the page does not carry the pending invitation %q", pending.Invitation.ID)
	}
	if live.RevokedAt != nil {
		t.Errorf("the pending view carries revoked_at %v, want none", live.RevokedAt)
	}
}

// TestASupersededRowThatHasNotExpiredIsNotLive holds the third predicate the
// conditional writes carry: a row a later invitation replaced is never a live
// slot, even while its deadline is still ahead. The normal path supersedes a row
// only once it has expired, so a case seeds this state directly and holds that
// redemption, resend and revoke all refuse it.
func TestASupersededRowThatHasNotExpiredIsNotLive(t *testing.T) {
	h := newHarness(t)
	token, err := newToken()
	if err != nil {
		t.Fatalf("minting a token: %v", err)
	}
	now := h.clock.read()
	supersededAt := utc(now)
	row := invitationRow{
		ID: "inv-superseded-live", TenantID: tenant, Email: "alice@example.com",
		TokenDigest: tokenDigest(token), Status: statusPending,
		InvitedBy: string(adminID), ExpiresAt: utc(now.Add(time.Hour)),
		LastSentAt: utc(now), SupersededAt: &supersededAt,
		CreatedAt: utc(now), UpdatedAt: utc(now),
	}
	if createErr := h.handle.Create(&row).Error; createErr != nil {
		t.Fatalf("seeding the superseded row: %v", createErr)
	}
	before := h.row(row.ID)

	// The claim matches no live row, so the stale slot is not accepted.
	if _, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: token}); err == nil {
		t.Fatal("accepting a superseded row succeeded, want it refused")
	}
	if h.members.count != 0 {
		t.Errorf("the refused acceptance wrote %d membership(s), want none", h.members.count)
	}

	// Resend and revoke answer as if the row were not there, keeping it as it is.
	if _, err := h.p.Resend(context.Background(), tenant, row.ID); !errors.Is(err, ErrInvitationNotFound) {
		t.Errorf("resending a superseded row gave %v, want ErrInvitationNotFound", err)
	}
	if err := h.p.Revoke(context.Background(), tenant, row.ID); !errors.Is(err, ErrInvitationNotFound) {
		t.Errorf("revoking a superseded row gave %v, want ErrInvitationNotFound", err)
	}

	after := h.row(row.ID)
	if after.Status != statusPending || after.AcceptedAt != nil || after.RevokedAt != nil {
		t.Errorf("the row reads status %d, accepted_at %v and revoked_at %v, want it untouched", after.Status, after.AcceptedAt, after.RevokedAt)
	}
	if after.TokenDigest != before.TokenDigest {
		t.Error("a refused resend replaced the token digest")
	}
	if after.SendCount != before.SendCount {
		t.Errorf("send_count = %d, want the unchanged %d", after.SendCount, before.SendCount)
	}
}
