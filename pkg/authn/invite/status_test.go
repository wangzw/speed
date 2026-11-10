package invite

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// listStatus reads one page from the management listing under an optional query
// and returns the identifiers the page answered, in order.
func (h *harness) listStatus(query string) []string {
	h.t.Helper()
	h.asAdmin()
	path := pathAdmin
	if query != "" {
		path += "?" + query
	}
	w := h.request(http.MethodGet, path, nil)
	if w.Code != http.StatusOK {
		h.t.Fatalf("listing with %q: status = %d, want 200: %s", query, w.Code, w.Body.String())
	}
	var answered struct {
		Invitations []AuthnInviteInvitation `json:"invitations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		h.t.Fatalf("decoding the page: %v", err)
	}
	ids := make([]string, len(answered.Invitations))
	for i, inv := range answered.Invitations {
		ids[i] = inv.ID
	}
	return ids
}

// sameIdentifiers reports whether got and want hold the same identifiers,
// whatever their order: a page of rows issued in the same instant has no stable
// order beyond the identifier tie-break.
func sameIdentifiers(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	a := slices.Clone(got)
	b := slices.Clone(want)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// TestTheListRouteFiltersByStatus holds each value of the status query to the
// rows it names. The stored states are pending, accepted and revoked; expired is
// a pending row whose deadline has passed, so the pending and expired filters
// split the pending rows by the clock and the stored state is unchanged.
func TestTheListRouteFiltersByStatus(t *testing.T) {
	h := newHarness(t)
	const tenant = "tenant-a"
	// This one expires: the default lifetime is 168 hours and the clock moves
	// past it before the rest are issued.
	past := h.issue(tenant, "past@example.com")
	h.clock.advance(169 * time.Hour)
	pending := h.issue(tenant, "pending@example.com")
	accepted := h.issue(tenant, "accepted@example.com")
	if _, err := h.p.Accept(h.acceptCtx("acct-accepted", "accepted@example.com"), AcceptInvite{Token: accepted.Token}); err != nil {
		t.Fatalf("accepting: %v", err)
	}
	revoked := h.issue(tenant, "revoked@example.com")
	if err := h.p.Revoke(context.Background(), tenant, revoked.Invitation.ID); err != nil {
		t.Fatalf("revoking: %v", err)
	}

	cases := []struct {
		query string
		want  []string
	}{
		{"status=pending", []string{pending.Invitation.ID}},
		{"status=expired", []string{past.Invitation.ID}},
		{"status=accepted", []string{accepted.Invitation.ID}},
		{"status=revoked", []string{revoked.Invitation.ID}},
		{"", []string{past.Invitation.ID, pending.Invitation.ID, accepted.Invitation.ID, revoked.Invitation.ID}},
	}
	for _, c := range cases {
		if got := h.listStatus(c.query); !sameIdentifiers(got, c.want) {
			t.Errorf("listing with %q answered %v, want %v", c.query, got, c.want)
		}
	}
}

// TestTheExpiredFilterStartsAtTheDeadline holds the boundary: an invitation is
// redeemable up to, but not at, its deadline, which is where the expired filter
// starts. A second before the deadline it is still pending; at the deadline it
// is expired; later it stays expired.
func TestTheExpiredFilterStartsAtTheDeadline(t *testing.T) {
	h := newHarness(t)
	const tenant = "tenant-a"
	deadline := h.clock.read().Add(time.Hour)
	issued, err := h.p.Issue(context.Background(), IssueInvite{
		Tenant: tenant, Email: "edge@example.com", ExpiresAt: deadline,
	})
	if err != nil {
		t.Fatalf("issuing with a deadline: %v", err)
	}
	id := issued.Invitation.ID

	h.clock.advance(time.Hour - time.Second)
	if got := h.listStatus("status=pending"); !sameIdentifiers(got, []string{id}) {
		t.Errorf("a second before the deadline, pending answered %v, want %s", got, id)
	}
	if got := h.listStatus("status=expired"); len(got) != 0 {
		t.Errorf("a second before the deadline, expired answered %v, want nothing", got)
	}

	h.clock.advance(time.Second)
	if got := h.listStatus("status=expired"); !sameIdentifiers(got, []string{id}) {
		t.Errorf("exactly at the deadline, expired answered %v, want %s", got, id)
	}
	if got := h.listStatus("status=pending"); len(got) != 0 {
		t.Errorf("exactly at the deadline, pending answered %v, want nothing", got)
	}

	h.clock.advance(time.Second)
	if got := h.listStatus("status=expired"); !sameIdentifiers(got, []string{id}) {
		t.Errorf("after the deadline, expired answered %v, want %s", got, id)
	}
}

// seed writes one invitation row straight into the table, with the identifier,
// status and creation time the case names, so an ordering and a cursor are
// deterministic.
func (h *harness) seed(id, tenant, email string, status int16, createdAt time.Time) {
	h.t.Helper()
	row := invitationRow{
		ID: id, TenantID: tenant, Email: email, TokenDigest: "digest-" + id,
		Status: status, ExpiresAt: utc(createdAt.Add(time.Hour)),
		CreatedAt: utc(createdAt), UpdatedAt: utc(createdAt),
	}
	if err := h.handle.Create(&row).Error; err != nil {
		h.t.Fatalf("seeding %s: %v", id, err)
	}
}

func identifiersOf(invitations []Invitation) []string {
	ids := make([]string, len(invitations))
	for i, inv := range invitations {
		ids[i] = inv.ID
	}
	return ids
}

// TestACursorPageKeepsTheTenantAndTheFilter holds the cursor predicate to a
// conjunction with the tenant and the status filter. The cursor carries an OR
// of its own, so without parentheses around it the second page widens to every
// tenant's pending rows instead of continuing this tenant's filtered page.
func TestACursorPageKeepsTheTenantAndTheFilter(t *testing.T) {
	h := newHarness(t)
	base := h.clock.read()
	h.seed("a2", "tenant-a", "a2@example.com", statusPending, base.Add(3*time.Minute))
	h.seed("a1", "tenant-a", "a1@example.com", statusPending, base.Add(2*time.Minute))
	h.seed("b1", "tenant-b", "b1@example.com", statusPending, base.Add(1*time.Minute))

	first, cursor, err := h.p.list(context.Background(), "tenant-a", authn.Page{Limit: 1}, listPending)
	if err != nil {
		t.Fatalf("listing the first page: %v", err)
	}
	if got := identifiersOf(first); !sameIdentifiers(got, []string{"a2"}) {
		t.Fatalf("first page = %v, want a2", got)
	}
	second, _, err := h.p.list(context.Background(), "tenant-a", authn.Page{Limit: 10, After: cursor}, listPending)
	if err != nil {
		t.Fatalf("listing the second page: %v", err)
	}
	if got := identifiersOf(second); !sameIdentifiers(got, []string{"a1"}) {
		t.Errorf("second page = %v, want only a1: a cursor must not widen the tenant or the filter", got)
	}
}

// TestTheListRouteRefusesAnUnknownStatus holds the status query to its closed
// set: a value outside it is a 400, not silently the unfiltered page.
func TestTheListRouteRefusesAnUnknownStatus(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	w := h.request(http.MethodGet, pathAdmin+"?status=bogus", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown status: %s", w.Code, w.Body.String())
	}
}
