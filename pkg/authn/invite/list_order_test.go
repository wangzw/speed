package invite

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// TestTheListOrdersFourGroupsAndTheCursorCarriesTheGroup holds the listing's
// order: unexpired pending first, then expired pending, then accepted, then
// revoked; within a group newest first. The cursor carries the group, so a page
// boundary lands in the right group rather than resuming by the stored status,
// which cannot tell pending from expired. Each view's state is judged on the
// server, by the same clock that decided the row's group.
func TestTheListOrdersFourGroupsAndTheCursorCarriesTheGroup(t *testing.T) {
	h := newHarness(t)
	pendingOld := h.issue(tenant, "old@example.com")
	h.clock.advance(time.Minute)
	pendingNew := h.issue(tenant, "new@example.com")
	h.clock.advance(time.Minute)
	expiring := h.issueWithExpiry("expiring@example.com", h.clock.read().Add(time.Minute))
	accepted := h.issue(tenant, "accepted@example.com")
	revoked := h.issue(tenant, "revoked@example.com")
	h.clock.advance(2 * time.Minute)

	if _, err := h.p.Accept(h.acceptCtx("acct-accepted", "accepted@example.com"),
		AcceptInvite{Token: accepted.Token}); err != nil {
		t.Fatalf("accepting: %v", err)
	}
	if err := h.p.Revoke(context.Background(), tenant, revoked.Invitation.ID); err != nil {
		t.Fatalf("revoking: %v", err)
	}

	h.asAdmin()
	type row struct {
		id    string
		state string
	}
	var got []row
	after := ""
	for page := 0; page < 10; page++ {
		path := pathAdmin + "?limit=2"
		if after != "" {
			path += "&after=" + url.QueryEscape(after)
		}
		w := h.request(http.MethodGet, path, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("page %d answered %d: %s", page, w.Code, w.Body.String())
		}
		var answered AuthnInvitePage
		if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
			t.Fatalf("decoding page %d: %v", page, err)
		}
		for _, invitation := range answered.Invitations {
			got = append(got, row{id: invitation.ID, state: invitation.State})
		}
		if answered.NextCursor == "" {
			break
		}
		after = answered.NextCursor
	}

	want := []row{
		{id: pendingNew.Invitation.ID, state: viewStatePending},
		{id: pendingOld.Invitation.ID, state: viewStatePending},
		{id: expiring.Invitation.ID, state: viewStateExpired},
		{id: accepted.Invitation.ID, state: viewStateAccepted},
		{id: revoked.Invitation.ID, state: viewStateRevoked},
	}
	if len(got) != len(want) {
		t.Fatalf("the pages held %d invitations (%+v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestAPageBoundaryOnAnExpiredRowResumesInTheNextGroup holds the cursor a page
// ending on an expired pending row carries: its group is the expired one, so the
// next page starts at the accepted group rather than repeating or skipping rows
// through the stored status, which still reads pending for that row.
func TestAPageBoundaryOnAnExpiredRowResumesInTheNextGroup(t *testing.T) {
	h := newHarness(t)
	pending := h.issue(tenant, "pending@example.com")
	h.clock.advance(time.Minute)
	expiring := h.issueWithExpiry("expiring@example.com", h.clock.read().Add(time.Minute))
	h.clock.advance(time.Minute)
	accepted := h.issue(tenant, "accepted@example.com")
	h.clock.advance(2 * time.Minute)
	if _, err := h.p.Accept(h.acceptCtx("acct-accepted", "accepted@example.com"),
		AcceptInvite{Token: accepted.Token}); err != nil {
		t.Fatalf("accepting: %v", err)
	}

	h.asAdmin()
	first := h.request(http.MethodGet, pathAdmin+"?limit=2", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", first.Code, first.Body.String())
	}
	var page AuthnInvitePage
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatalf("decoding the first page: %v", err)
	}
	if len(page.Invitations) != 2 {
		t.Fatalf("the first page holds %d invitations, want the pending one and the expired one", len(page.Invitations))
	}
	if got := page.Invitations[0].ID; got != pending.Invitation.ID {
		t.Errorf("the first row = %s, want the unexpired pending invitation %s", got, pending.Invitation.ID)
	}
	if got, want := page.Invitations[1], expiring.Invitation.ID; got.ID != want || got.State != viewStateExpired {
		t.Errorf("the page ends on %+v, want the expired invitation %s so the cursor carries its group", got, want)
	}
	if page.NextCursor == "" {
		t.Fatal("the first page answers no cursor")
	}

	secondPath := pathAdmin + "?limit=2&after=" + url.QueryEscape(page.NextCursor)
	second := h.request(http.MethodGet, secondPath, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for the second page: %s", second.Code, second.Body.String())
	}
	var rest AuthnInvitePage
	if err := json.Unmarshal(second.Body.Bytes(), &rest); err != nil {
		t.Fatalf("decoding the second page: %v", err)
	}
	if len(rest.Invitations) != 1 {
		t.Fatalf("the second page holds %d invitations, want only the accepted one: %+v", len(rest.Invitations), rest.Invitations)
	}
	if got, want := rest.Invitations[0], accepted.Invitation.ID; got.ID != want || got.State != viewStateAccepted {
		t.Errorf("the second page = %+v, want the accepted invitation %s", got, want)
	}
}

// TestACursorOutsideTheGroupsIsRefused holds the closed cursor vocabulary: a
// cursor carrying a group the listing never writes is unreadable, so it is
// refused rather than served as a position.
func TestACursorOutsideTheGroupsIsRefused(t *testing.T) {
	for _, group := range []int{-1, listGroupRevoked + 1} {
		raw := encodeCursor(cursor{
			group:     group,
			createdAt: time.Date(2031, 3, 4, 12, 0, 0, 0, time.UTC),
			id:        "inv-1",
		})
		if _, err := decodeCursor(raw); err == nil {
			t.Errorf("a cursor with group %d was read, want it refused", group)
		}
	}
}

// TestACursorFromAnEarlierFormatIsRefused holds the version mark: the
// three-part cursor an earlier build minted and a cursor naming a version this
// build does not know are both refused with 400 rather than read as the current
// sort key.
func TestACursorFromAnEarlierFormatIsRefused(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	at := "2031-03-04T12:00:00Z"
	for name, raw := range map[string]string{
		"the three-part format of an earlier build": at + "\n0\ninv-1",
		"a version this build does not know":        "v2\n" + at + "\n0\ninv-1",
	} {
		after := base64.RawURLEncoding.EncodeToString([]byte(raw))
		w := h.request(http.MethodGet, pathAdmin+"?after="+url.QueryEscape(after), nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("a cursor carrying %s answered %d, want 400: %s", name, w.Code, w.Body.String())
		}
	}
}

// TestTheManagementViewNamesTheInviterAndJudgesTheState holds the management
// view's three new fields: the state is the server's four-state judgement, and
// the inviter's display name and username are resolved from the account that
// issued the invitation. A host command's invitation has no issuer to name, and
// an account that no longer exists is a tombstone with no name, so both keys are
// absent; the issuer identifier stays in invited_by.
func TestTheManagementViewNamesTheInviterAndJudgesTheState(t *testing.T) {
	h := newHarness(t)
	h.accounts.named(adminID, "Ada Admin")
	h.accounts.user(adminID, "ada")
	issued := h.issue(tenant, "alice@example.com")

	h.asAdmin()
	w := h.request(http.MethodGet, pathAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing answered %d: %s", w.Code, w.Body.String())
	}
	var page AuthnInvitePage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decoding the page: %v", err)
	}
	if len(page.Invitations) != 1 {
		t.Fatalf("the page held %d invitations, want one", len(page.Invitations))
	}
	view := page.Invitations[0]
	if view.State != viewStatePending {
		t.Errorf("state = %q, want %q", view.State, viewStatePending)
	}
	if view.InvitedByName != "Ada Admin" {
		t.Errorf("invited_by_name = %q, want Ada Admin", view.InvitedByName)
	}
	if view.InvitedByUsername != "ada" {
		t.Errorf("invited_by_username = %q, want ada", view.InvitedByUsername)
	}
	if view.LastSentAt.IsZero() {
		t.Error("last_sent_at is the zero time, want the issuance instant")
	}

	// A host command's invitation names no issuer.
	hostIssued, err := h.p.Issue(context.Background(), IssueInvite{Tenant: tenant, Email: "host@example.com"})
	if err != nil {
		t.Fatalf("issuing without an identity: %v", err)
	}
	hostPage := h.listPage()
	for _, invitation := range hostPage.Invitations {
		if invitation.ID != hostIssued.Invitation.ID {
			continue
		}
		if invitation.InvitedByName != "" || invitation.InvitedByUsername != "" {
			t.Errorf("a host command's invitation names %q / %q, want both absent",
				invitation.InvitedByName, invitation.InvitedByUsername)
		}
		if invitation.InvitedBy != "" {
			t.Errorf("a host command's invitation names the issuer %q, want the empty string", invitation.InvitedBy)
		}
	}

	// The issuing account gone leaves the identifier and drops both names.
	h.accounts.gone(adminID)
	gonePage := h.listPage()
	for _, invitation := range gonePage.Invitations {
		if invitation.ID != issued.Invitation.ID {
			continue
		}
		if invitation.InvitedByName != "" || invitation.InvitedByUsername != "" {
			t.Errorf("a tombstone's invitation names %q / %q, want both absent",
				invitation.InvitedByName, invitation.InvitedByUsername)
		}
		if invitation.InvitedBy != string(adminID) {
			t.Errorf("invited_by = %q, want the stored identifier", invitation.InvitedBy)
		}
	}
	encoded, err := json.Marshal(gonePage)
	if err != nil {
		t.Fatalf("encoding the page: %v", err)
	}
	if strings.Contains(string(encoded), `"invited_by_name":""`) ||
		strings.Contains(string(encoded), `"invited_by_username":""`) {
		t.Errorf("an absent name reached the answer as an empty string: %s", encoded)
	}
}

// TestAnImpersonatedIssuanceNamesTheImpersonatedAccount holds the naming rule
// during an impersonation: the invitation records the effective account, so the
// view names the impersonated account, while the audit keeps the operator.
func TestAnImpersonatedIssuanceNamesTheImpersonatedAccount(t *testing.T) {
	h := newHarness(t)
	h.accounts.named("acct-target", "Target Person")
	h.accounts.user("acct-target", "target")
	h.mounter.As(
		authntest.WithAccount("acct-target"),
		authntest.WithActor("acct-operator"),
		authntest.WithSession(authn.SessionID("session-1")),
		authntest.WithScope(tenant),
	)
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var answered AuthnInviteIssued
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if answered.Invitation.InvitedBy != "acct-target" {
		t.Errorf("invited_by = %q, want the impersonated account", answered.Invitation.InvitedBy)
	}
	if answered.Invitation.InvitedByName != "Target Person" {
		t.Errorf("invited_by_name = %q, want the impersonated account's name", answered.Invitation.InvitedByName)
	}
	if answered.Invitation.InvitedByUsername != "target" {
		t.Errorf("invited_by_username = %q, want the impersonated account's username", answered.Invitation.InvitedByUsername)
	}
}

// listPage reads the first unfiltered page through the management route.
func (h *harness) listPage() AuthnInvitePage {
	h.t.Helper()
	h.asAdmin()
	w := h.request(http.MethodGet, pathAdmin, nil)
	if w.Code != http.StatusOK {
		h.t.Fatalf("listing answered %d: %s", w.Code, w.Body.String())
	}
	var page AuthnInvitePage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		h.t.Fatalf("decoding the page: %v", err)
	}
	return page
}

// issueWithExpiry issues an invitation with an explicit deadline as the
// administrator would.
func (h *harness) issueWithExpiry(email string, expiresAt time.Time) Issued {
	h.t.Helper()
	ctx := authntest.Context(h.t, context.Background(), authntest.WithAccount(adminID))
	issued, err := h.p.Issue(ctx, IssueInvite{Tenant: tenant, Email: email, ExpiresAt: expiresAt})
	if err != nil {
		h.t.Fatalf("issuing an invitation for %s: %v", email, err)
	}
	return issued
}
