//go:build integration

package invite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// invitationHandleOnPostgres opens the case's own database on the compose
// stack's server and builds the table this module reads from the PostgreSQL
// migration files, the way the database module applies them.
func invitationHandleOnPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	handle := quietly(dbtest.Postgres(t, pgtest.Acquire(t), nil).DB())
	applyMigrationsFor(t, handle, "postgres")
	return handle
}

// columnShape is what the case asserts about one column on PostgreSQL.
type columnShape struct {
	dataType string
	nullable string
	def      string
}

// TestTheInvitationTableOnPostgres holds the schema the migration file declares
// on the engine the migration was written for: every column, its type, its
// nullability and the two defaults the writers rely on. The token digest is
// unique, and the live-slot index is partial and unique over the stored pending
// state only.
func TestTheInvitationTableOnPostgres(t *testing.T) {
	handle := invitationHandleOnPostgres(t)

	rows, err := handle.Raw(`SELECT column_name, data_type, is_nullable, COALESCE(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = ?
		ORDER BY ordinal_position`, tableInvitations).Rows()
	if err != nil {
		t.Fatalf("reading the column descriptions: %v", err)
	}
	defer rows.Close()
	columns := map[string]columnShape{}
	for rows.Next() {
		var name string
		var shape columnShape
		if scanErr := rows.Scan(&name, &shape.dataType, &shape.nullable, &shape.def); scanErr != nil {
			t.Fatalf("scanning a column description: %v", scanErr)
		}
		columns[name] = shape
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("reading the column descriptions: %v", rowsErr)
	}

	want := map[string]columnShape{
		"id":            {"text", "NO", ""},
		"tenant_id":     {"text", "NO", ""},
		"email":         {"text", "NO", ""},
		"token_digest":  {"text", "NO", ""},
		"status":        {"smallint", "NO", "0"},
		"invited_by":    {"text", "NO", ""},
		"role":          {"text", "NO", "''::text"},
		"expires_at":    {"timestamp with time zone", "NO", ""},
		"accepted_at":   {"timestamp with time zone", "YES", ""},
		"revoked_at":    {"timestamp with time zone", "YES", ""},
		"last_sent_at":  {"timestamp with time zone", "NO", ""},
		"send_count":    {"integer", "NO", "1"},
		"superseded_at": {"timestamp with time zone", "YES", ""},
		"created_at":    {"timestamp with time zone", "NO", ""},
		"updated_at":    {"timestamp with time zone", "NO", ""},
	}
	if len(columns) != len(want) {
		t.Errorf("the table has %d columns, want %d: %v", len(columns), len(want), columns)
	}
	for name, expected := range want {
		got, ok := columns[name]
		if !ok {
			t.Errorf("the table has no column %q", name)
			continue
		}
		if got != expected {
			t.Errorf("column %q = %+v, want %+v", name, got, expected)
		}
	}

	rows, err = handle.Raw(`SELECT indexname, indexdef FROM pg_indexes
		WHERE schemaname = current_schema() AND tablename = ?`, tableInvitations).Rows()
	if err != nil {
		t.Fatalf("reading the indexes: %v", err)
	}
	defer rows.Close()
	indexes := map[string]string{}
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			t.Fatalf("scanning an index: %v", err)
		}
		indexes[name] = definition
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the indexes: %v", err)
	}

	live, ok := indexes["authn_invitations_one_pending"]
	if !ok {
		t.Fatalf("the live-slot index is not there: %v", indexes)
	}
	for _, fragment := range []string{"UNIQUE INDEX", "status = 0", "superseded_at IS NULL"} {
		if !strings.Contains(live, fragment) {
			t.Errorf("the live-slot index does not carry %q: %s", fragment, live)
		}
	}
	digestUnique := false
	for _, definition := range indexes {
		if strings.Contains(definition, "UNIQUE INDEX") && strings.Contains(definition, "token_digest") {
			digestUnique = true
		}
	}
	if !digestUnique {
		t.Errorf("no unique index guards token_digest: %v", indexes)
	}
}

// TestTheListingOrderAndCursorOnPostgres holds the four-group order and the
// cursor predicate on the engine the listing actually runs against: the CASE
// expression that groups rows and the cursor's comparison are PostgreSQL SQL,
// so a page boundary must land in the right group there too.
func TestTheListingOrderAndCursorOnPostgres(t *testing.T) {
	handle := invitationHandleOnPostgres(t)
	h := newHarnessOn(t, handle)
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

	var got []string
	after := ""
	for page := 0; page < 10; page++ {
		rows, next, err := h.p.list(context.Background(), tenant,
			authn.Page{Limit: 2, After: after}, listAll, h.clock.read())
		if err != nil {
			t.Fatalf("listing page %d: %v", page, err)
		}
		for _, row := range rows {
			got = append(got, row.ID)
		}
		if next == "" {
			break
		}
		after = next
	}
	want := []string{
		pendingNew.Invitation.ID,
		pendingOld.Invitation.ID,
		expiring.Invitation.ID,
		accepted.Invitation.ID,
		revoked.Invitation.ID,
	}
	if len(got) != len(want) {
		t.Fatalf("the pages held %d invitations (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d = %s, want %s", i, got[i], want[i])
		}
	}
}

// TestTheLiveSlotAndSupersessionOnPostgres holds the two writes the design puts
// in one transaction, on the engine whose partial index is the arbiter: an
// expired row is replaced in place by superseded_at while a new live row is
// inserted, a second live row for the same pair is refused by the index, and a
// superseded row does not occupy the live slot.
func TestTheLiveSlotAndSupersessionOnPostgres(t *testing.T) {
	handle := invitationHandleOnPostgres(t)
	h := newHarnessOn(t, handle)

	now := h.clock.read()
	first, err := h.p.Issue(context.Background(), IssueInvite{
		Tenant: tenant, Email: "pg@example.com", ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("issuing the first invitation: %v", err)
	}
	h.clock.advance(2 * time.Hour)
	second, err := h.p.Issue(context.Background(), IssueInvite{
		Tenant: tenant, Email: "pg@example.com", ExpiresAt: h.clock.read().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("issuing over the expired invitation: %v", err)
	}

	old := h.row(first.Invitation.ID)
	if old.Status != statusPending {
		t.Errorf("the replaced row's status = %d, want pending so it still reads expired", old.Status)
	}
	if old.SupersededAt == nil || !old.SupersededAt.Equal(h.clock.read()) {
		t.Errorf("superseded_at = %v, want the issuance instant %v", old.SupersededAt, h.clock.read())
	}
	if got := h.row(second.Invitation.ID).Status; got != statusPending {
		t.Errorf("the new row's status = %d, want pending", got)
	}
	if got := h.rowCount(); got != 2 {
		t.Errorf("rows = %d, want the old and the new one", got)
	}

	// A second live pending row for the same tenant and address meets the
	// partial unique index.
	err = handle.Exec(
		`INSERT INTO authn_invitations
			(id, tenant_id, email, token_digest, status, invited_by, expires_at, last_sent_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"inv-second-live", tenant, "pg@example.com", "digest-second-live", statusPending,
		"", h.clock.read().Add(time.Hour), h.clock.read(), h.clock.read(), h.clock.read(),
	).Error
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("a second live row gave %v, want gorm.ErrDuplicatedKey from the live-slot index", err)
	}
	if got := h.rowCount(); got != 2 {
		t.Errorf("the refused insert changed the row count to %d, want 2", got)
	}

	// A superseded row is history rather than a live slot, so it is insertable
	// beside the live one.
	err = handle.Exec(
		`INSERT INTO authn_invitations
			(id, tenant_id, email, token_digest, status, invited_by, expires_at, last_sent_at, superseded_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"inv-superseded", tenant, "pg@example.com", "digest-superseded", statusPending,
		"", h.clock.read().Add(time.Hour), h.clock.read(), h.clock.read(), h.clock.read(), h.clock.read(),
	).Error
	if err != nil {
		t.Fatalf("a superseded row was refused a slot beside the live one: %v", err)
	}
	if got := h.rowCount(); got != 3 {
		t.Errorf("rows = %d, want the superseded history row beside the two invitations", got)
	}
}
