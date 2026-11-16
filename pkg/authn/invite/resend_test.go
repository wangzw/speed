package invite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// TestResendReplacesTheTokenOnTheSameRow holds the rule: a resend keeps the
// invitation's identity, its deadline and its row, and only the token and the
// send record move. The old token stops resolving.
func TestResendReplacesTheTokenOnTheSameRow(t *testing.T) {
	h := newHarness(t)
	first := h.issue(tenant, "alice@example.com")
	before := h.rowCount()
	expires := first.Invitation.ExpiresAt
	h.clock.advance(10 * time.Minute)

	resent, err := h.p.Resend(context.Background(), tenant, first.Invitation.ID)
	if err != nil {
		t.Fatalf("resending: %v", err)
	}
	if resent.Invitation.ID != first.Invitation.ID {
		t.Errorf("resend changed the identifier from %q to %q", first.Invitation.ID, resent.Invitation.ID)
	}
	if got := h.rowCount(); got != before {
		t.Errorf("resend changed the row count from %d to %d", before, got)
	}
	if !resent.Invitation.ExpiresAt.Equal(expires) {
		t.Errorf("resend moved expires_at from %v to %v", expires, resent.Invitation.ExpiresAt)
	}
	if got := resent.Invitation.SendCount; got != 2 {
		t.Errorf("send_count = %d, want 2 after the first resend", got)
	}
	if !resent.Invitation.LastSentAt.Equal(h.clock.read()) {
		t.Errorf("last_sent_at = %v, want the resend instant %v", resent.Invitation.LastSentAt, h.clock.read())
	}
	row := h.row(first.Invitation.ID)
	if row.TokenDigest != tokenDigest(resent.Token) {
		t.Error("the row does not hold the new token's digest")
	}
	if _, _, err := h.p.viewToken(context.Background(), first.Token); !errors.Is(err, ErrInvitationNotFound) {
		t.Errorf("the old token gave %v, want ErrInvitationNotFound", err)
	}
	if _, state, err := h.p.viewToken(context.Background(), resent.Token); err != nil || state != viewStatePending {
		t.Errorf("the new token reads %q (%v), want pending", state, err)
	}

	// The same two answers over the public route: the old link is gone and the
	// new one is the redeemable pending link.
	h.mounter.AsAnonymous()
	if w := h.request(http.MethodGet, "/api/v1/auth/invitations/"+first.Token, nil); w.Code != http.StatusNotFound {
		t.Errorf("the old token over HTTP answered %d, want 404: %s", w.Code, w.Body.String())
	}
	w := h.request(http.MethodGet, "/api/v1/auth/invitations/"+resent.Token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the new token over HTTP answered %d, want 200: %s", w.Code, w.Body.String())
	}
	var view struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("the body %q is not JSON: %v", w.Body.String(), err)
	}
	if view.State != viewStatePending {
		t.Errorf("the new token's HTTP state = %q, want %q", view.State, viewStatePending)
	}
}

// TestResendRefusesTerminalInvitations holds that a terminal row is never
// resent and that a refusal leaves its token and its send record alone.
func TestResendRefusesTerminalInvitations(t *testing.T) {
	cases := []struct {
		name   string
		settle func(*harness, Issued)
		want   error
	}{
		{
			name: "accepted",
			settle: func(h *harness, issued Issued) {
				if _, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token}); err != nil {
					h.t.Fatalf("accepting: %v", err)
				}
			},
			want: ErrInvitationAlreadyAccepted,
		},
		{
			name: "revoked",
			settle: func(h *harness, issued Issued) {
				if err := h.p.Revoke(context.Background(), tenant, issued.Invitation.ID); err != nil {
					h.t.Fatalf("revoking: %v", err)
				}
			},
			want: ErrInvitationRevoked,
		},
		{
			name: "expired",
			settle: func(h *harness, _ Issued) {
				h.clock.advance(DefaultTTL + time.Minute)
			},
			want: ErrInvitationExpired,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			issued := h.issue(tenant, "alice@example.com")
			before := h.row(issued.Invitation.ID)
			digest := before.TokenDigest
			sent := before.LastSentAt
			c.settle(h, issued)

			_, err := h.p.Resend(context.Background(), tenant, issued.Invitation.ID)
			if !errors.Is(err, c.want) {
				t.Fatalf("resending a %s invitation gave %v, want %v", c.name, err, c.want)
			}
			row := h.row(issued.Invitation.ID)
			if row.TokenDigest != digest {
				t.Errorf("the refused resend replaced the token digest")
			}
			if row.SendCount != 1 {
				t.Errorf("send_count = %d, want 1 after a refused resend", row.SendCount)
			}
			if !row.LastSentAt.Equal(sent) {
				t.Errorf("the refused resend moved last_sent_at from %v to %v", sent, row.LastSentAt)
			}
		})
	}
}

// TestResendRefusesAnUnknownIdentifier holds the not-found direction.
func TestResendRefusesAnUnknownIdentifier(t *testing.T) {
	h := newHarness(t)
	_, err := h.p.Resend(context.Background(), tenant, "no-such-invitation")
	if !errors.Is(err, ErrInvitationNotFound) {
		t.Fatalf("resending an unknown identifier gave %v, want ErrInvitationNotFound", err)
	}
}

// TestTheResendRouteAnswersAndAuditsTheInvitation holds the HTTP face: the
// answer is the invitation with the new send record, the decision's target is
// the invitation identifier, and the mandatory audit row names the invitation,
// not the address.
func TestTheResendRouteAnswersAndAuditsTheInvitation(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	h.asAdmin()

	w := h.request(http.MethodPost, pathAdmin+"/"+issued.Invitation.ID+"/resend", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("resend status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var answered AuthnInviteIssued
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if answered.Invitation.ID != issued.Invitation.ID || answered.Invitation.SendCount != 2 {
		t.Errorf("answer = %+v, want the same invitation with send_count 2", answered.Invitation)
	}
	if answered.Token == "" {
		t.Error("a failed or unconfigured notice must hand the one-time token over")
	}
	dec, ok := h.decider.last()
	if !ok || dec.Action != authn.ActionMembershipManage || dec.Target != issued.Invitation.ID {
		t.Errorf("decision = %+v, want membership.manage with target %q", dec, issued.Invitation.ID)
	}
	rows := h.audit.entries()
	last := rows[len(rows)-1]
	if last.Action != actionResend {
		t.Errorf("audit action = %q, want %q", last.Action, actionResend)
	}
	if last.Object != issued.Invitation.ID {
		t.Errorf("audit object = %q, want the invitation %q", last.Object, issued.Invitation.ID)
	}
}

// TestTheResendRouteAnswers409ForATerminalInvitation holds the HTTP status of a
// refused resend for each terminal state: the management face's 409
// invitation-terminal, with the row's token and send record untouched.
func TestTheResendRouteAnswers409ForATerminalInvitation(t *testing.T) {
	cases := []struct {
		name   string
		settle func(*harness, Issued)
	}{
		{
			name: "accepted",
			settle: func(h *harness, issued Issued) {
				if _, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token}); err != nil {
					h.t.Fatalf("accepting: %v", err)
				}
			},
		},
		{
			name: "revoked",
			settle: func(h *harness, issued Issued) {
				if err := h.p.Revoke(context.Background(), tenant, issued.Invitation.ID); err != nil {
					h.t.Fatalf("revoking: %v", err)
				}
			},
		},
		{
			name: "expired",
			settle: func(h *harness, _ Issued) {
				h.clock.advance(DefaultTTL + time.Minute)
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			issued := h.issue(tenant, "alice@example.com")
			c.settle(h, issued)
			before := h.row(issued.Invitation.ID)
			digest := before.TokenDigest
			sent := before.LastSentAt
			count := before.SendCount

			h.asAdmin()
			w := h.request(http.MethodPost, pathAdmin+"/"+issued.Invitation.ID+"/resend", nil)
			if w.Code != http.StatusConflict {
				t.Fatalf("resend status = %d, want 409: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "urn:speed:authn:invitation-terminal") {
				t.Errorf("body %s does not carry invitation-terminal", w.Body.String())
			}
			row := h.row(issued.Invitation.ID)
			if row.TokenDigest != digest {
				t.Error("the refused resend replaced the token digest")
			}
			if row.SendCount != count {
				t.Errorf("send_count = %d, want the unchanged %d", row.SendCount, count)
			}
			if !row.LastSentAt.Equal(sent) {
				t.Errorf("the refused resend moved last_sent_at from %v to %v", sent, row.LastSentAt)
			}
		})
	}
}

// TestARefusedResendSpendsOnlyTheIssuersBudget holds the order of a resend's
// charges: the issuing account is charged before the terminal check, so a
// terminal row still costs the operator, while the address's and the
// deployment's budgets are charged only once the row is known to be live. The
// address limit is the issuance's single unit, so a resend that charged the
// address before deciding the row was terminal would answer throttled instead
// of the terminal state; the counts then pin both budgets at the issuance's one
// unit.
func TestARefusedResendSpendsOnlyTheIssuersBudget(t *testing.T) {
	h := newHarness(t)
	h.store.set(issueAddressPath, 1)
	h.store.set(issueIssuerPath, 100)
	h.store.set(issueGlobalPath, 100)
	issued := h.issue(tenant, "alice@example.com")
	if err := h.p.Revoke(context.Background(), tenant, issued.Invitation.ID); err != nil {
		t.Fatalf("revoking: %v", err)
	}

	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(adminID))
	if _, err := h.p.Resend(ctx, tenant, issued.Invitation.ID); !errors.Is(err, ErrInvitationRevoked) {
		t.Fatalf("resending a revoked invitation gave %v, want ErrInvitationRevoked, not a spend of the "+
			"address or deployment budget", err)
	}
	if got := h.budgetCount(budgetIssueAddress, "alice@example.com"); got != 1 {
		t.Errorf("the address count after the refused resend = %v, want the issuance's 1", got)
	}
	if got := h.budgetCount(budgetIssueGlobal, ""); got != 1 {
		t.Errorf("the deployment count after the refused resend = %v, want the issuance's 1", got)
	}
	if got := h.budgetCount(budgetIssueIssuer, string(adminID)); got != 2 {
		t.Errorf("the issuer count after the refused resend = %v, want 2: the issuance and the attempt "+
			"that the terminal check refused", got)
	}
}

// TestResendsSpendTheAddressBudget holds that a resend is another send: the
// first issuance plus four resends fill a budget of five, and the next resend is
// throttled.
func TestResendsSpendTheAddressBudget(t *testing.T) {
	h := newHarness(t)
	h.store.set(issueAddressPath, 5)
	h.store.set(issueIssuerPath, 100)
	h.store.set(issueGlobalPath, 100)
	issued := h.issue(tenant, "alice@example.com")

	for i := 0; i < 4; i++ {
		if _, err := h.p.Resend(context.Background(), tenant, issued.Invitation.ID); err != nil {
			t.Fatalf("resend %d gave %v, want it let through", i+1, err)
		}
	}
	if _, err := h.p.Resend(context.Background(), tenant, issued.Invitation.ID); err == nil {
		t.Fatal("the sixth send to the address was let through, want throttled")
	} else {
		throttleOf(t, err)
	}
}

// TestTheResendRouteAnswers429OnTheSixthSend holds the same budget on the HTTP
// face: the first issuance and four resends spend a five-per-hour address
// budget, so the sixth send is a 429 problem and the row is not sent again.
func TestTheResendRouteAnswers429OnTheSixthSend(t *testing.T) {
	h := newHarness(t)
	h.store.set(issueAddressPath, 5)
	h.store.set(issueIssuerPath, 100)
	h.store.set(issueGlobalPath, 100)
	issued := h.issue(tenant, "alice@example.com")
	h.asAdmin()

	for i := 0; i < 4; i++ {
		w := h.request(http.MethodPost, pathAdmin+"/"+issued.Invitation.ID+"/resend", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("resend %d answered %d, want 200: %s", i+1, w.Code, w.Body.String())
		}
	}
	w := h.request(http.MethodPost, pathAdmin+"/"+issued.Invitation.ID+"/resend", nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("the sixth send answered %d, want 429: %s", w.Code, w.Body.String())
	}
	if retry := w.Header().Get("Retry-After"); retry == "" || retry == "0" {
		t.Errorf("Retry-After = %q, want the seconds until the window opens", retry)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("the 429 body %q is not JSON: %v", w.Body.String(), err)
	}
	if got, _ := body["type"].(string); got != "urn:speed:authn:throttled" {
		t.Errorf("problem type = %q, want urn:speed:authn:throttled", got)
	}
	if got := h.row(issued.Invitation.ID).SendCount; got != 5 {
		t.Errorf("send_count = %d, want 5 after the throttled sixth send", got)
	}
}

// TestResendAnswersTheWrittenRowWhenTheRereadFails holds that a resend whose
// conditional write landed does not depend on a second read. The answer is
// built from the copy read before the write plus the values this write set, so
// a re-read that fails cannot turn a sent invitation into an error, a lost
// token or a failed audit row.
func TestResendAnswersTheWrittenRowWhenTheRereadFails(t *testing.T) {
	h := newHarness(t)
	first := h.issue(tenant, "alice@example.com")

	var updated atomic.Bool
	const updateCallback = "test:resend-updated"
	if err := h.handle.Callback().Update().After("gorm:update").Register(updateCallback, func(tx *gorm.DB) {
		if tx.Statement.Table == tableInvitations {
			updated.Store(true)
		}
	}); err != nil {
		t.Fatalf("registering the update callback: %v", err)
	}
	const queryCallback = "test:resend-reread-fails"
	if err := h.handle.Callback().Query().Before("gorm:query").Register(queryCallback, func(tx *gorm.DB) {
		if updated.Load() && tx.Statement.Table == tableInvitations {
			_ = tx.AddError(errors.New("the invitation could not be re-read"))
		}
	}); err != nil {
		t.Fatalf("registering the query callback: %v", err)
	}

	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin+"/"+first.Invitation.ID+"/resend", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var answered struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decoding the resend answer %q: %v", w.Body.String(), err)
	}
	if answered.Token == "" {
		t.Error("the resend answer carries no token, so the new link is lost")
	}
	for _, row := range h.audit.entries() {
		if row.Action == actionResend && row.Outcome == outcomeFailed {
			t.Errorf("a landed resend wrote a failed audit row: %+v", row)
		}
	}
	// The token in the answer is the live one. The callback fails every read
	// after the update, which is the re-read a resend no longer makes; turning it
	// off lets this check read the stored row.
	updated.Store(false)
	if _, state, err := h.p.viewToken(context.Background(), answered.Token); err != nil || state != viewStatePending {
		t.Errorf("the answered token reads %q (%v), want pending", state, err)
	}
}
