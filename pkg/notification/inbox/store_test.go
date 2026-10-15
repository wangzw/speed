package inbox

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/notification"
)

// sample is one rendered message a case writes.
func sample() notification.Message {
	return notification.Message{
		TypeKey: "billing.invoice_paid",
		Group:   "billing",
		Title:   "Invoice paid",
		Body:    "Your invoice was paid.",
		Params:  map[string]any{"invoice": "INV-1", "amount": float64(42)},
		Link:    "/invoices/INV-1",
	}
}

// writeOne writes the sample with the given dedupe key for the recipient.
func writeOne(t *testing.T, in notification.Inbox, ctx context.Context, recipient, dedupeKey string) notification.Message {
	t.Helper()
	m, err := in.Write(ctx, recipient, sample(), dedupeKey)
	if err != nil {
		t.Errorf("writing the message: %v", err)
	}
	return m
}

// Writing a message lands one row and publishes one created event; writing the
// same dedupe key again returns the row already there, writes nothing more and
// publishes no second event.
func TestWriteIsIdempotentOnTheDedupeKey(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		sink := &sink{}
		e.on(t, host{bus: true, extra: []core.Module{sink.module()}}).drive(t, func(_ context.Context, reg *core.Registry) error {
			in := inboxOf(t, reg)
			s := product(t, reg)
			ctx := tenantCtx(t, reg, "acme")

			first := writeOne(t, in, ctx, "user-1", "key-1")
			if first.ID == "" {
				t.Errorf("the written message carries no identifier")
			}
			if first.TypeKey != "billing.invoice_paid" || first.Group != "billing" {
				t.Errorf("the written message is %+v, want the type and group of the sample", first)
			}
			if first.ReadAt != nil {
				t.Errorf("a freshly written message reads as read at %v", first.ReadAt)
			}

			second := writeOne(t, in, ctx, "user-1", "key-1")
			if second.ID != first.ID {
				t.Errorf("repeating the dedupe key returned %s, want the row already there %s", second.ID, first.ID)
			}
			if n := rowCount(t, s); n != 1 {
				t.Errorf("the table holds %d rows, want the one message", n)
			}
			if got := len(sink.events()); got != 1 {
				t.Errorf("the bus carries %d created events, want one", got)
			}
			return nil
		})
	})
}

// A write with no event bus in the assembly publishes nothing and lands the row
// exactly as it otherwise would: the created event accelerates a push, and a
// write is not what depends on it.
func TestWriteLandsTheRowWithoutABus(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		e.on(t, host{}).drive(t, func(_ context.Context, reg *core.Registry) error {
			s := product(t, reg)
			m := writeOne(t, inboxOf(t, reg), tenantCtx(t, reg, "acme"), "user-1", "key-1")
			if m.ID == "" {
				t.Error("the written message carries no identifier")
			}
			if n := rowCount(t, s); n != 1 {
				t.Errorf("the table holds %d rows, want the one message", n)
			}
			return nil
		})
	})
}

// An event the inbox publishes carries the closed set of the payload's fields.
func TestTheCreatedEventCarriesTheClosedPayload(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		sink := &sink{}
		e.on(t, host{bus: true, extra: []core.Module{sink.module()}}).drive(t, func(_ context.Context, reg *core.Registry) error {
			in := inboxOf(t, reg)
			ctx := tenantCtx(t, reg, "acme")
			m := writeOne(t, in, ctx, "user-1", "key-1")

			events := sink.events()
			if len(events) != 1 {
				t.Errorf("the bus carries %d events, want one", len(events))
				return nil
			}
			evt := events[0]
			if evt.Type != EventInboxCreated {
				t.Errorf("the event type is %q, want %q", evt.Type, EventInboxCreated)
			}
			if evt.TenantID != "acme" {
				t.Errorf("the event tenant is %q, want acme", evt.TenantID)
			}
			var payload Created
			if err := event.DecodePayload(evt.Payload, &payload); err != nil {
				t.Errorf("decoding the payload: %v", err)
				return nil
			}
			if payload.MessageID != m.ID || payload.TenantID != "acme" ||
				payload.RecipientUserID != "user-1" || payload.TypeKey != "billing.invoice_paid" {
				t.Errorf("the payload is %+v, want the row's own fields", payload)
			}
			return nil
		})
	})
}

// A write with an empty recipient or an empty dedupe key is refused with
// ErrInvalidRequest and lands nothing.
func TestWriteRefusesAnEmptyRecipientOrDedupeKey(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		e.on(t, host{}).drive(t, func(_ context.Context, reg *core.Registry) error {
			in := inboxOf(t, reg)
			s := product(t, reg)
			ctx := tenantCtx(t, reg, "acme")

			if _, err := in.Write(ctx, "", sample(), "key"); !errors.Is(err, notification.ErrInvalidRequest) {
				t.Errorf("writing to an empty recipient gave %v, want ErrInvalidRequest", err)
			}
			if _, err := in.Write(ctx, "user-1", sample(), ""); !errors.Is(err, notification.ErrInvalidRequest) {
				t.Errorf("writing with an empty dedupe key gave %v, want ErrInvalidRequest", err)
			}
			if n := rowCount(t, s); n != 0 {
				t.Errorf("the table holds %d rows, want none written", n)
			}
			return nil
		})
	})
}

// A write with no tenant in the context lands no row.
func TestWriteWithNoTenantLandsNothing(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		e.on(t, host{}).drive(t, func(_ context.Context, reg *core.Registry) error {
			in := inboxOf(t, reg)
			s := product(t, reg)
			if _, err := in.Write(context.Background(), "user-1", sample(), "key"); err == nil {
				t.Errorf("writing with no tenant in the context succeeded, want a refusal")
			}
			if n := rowCount(t, s); n != 0 {
				t.Errorf("the table holds %d rows, want none written", n)
			}
			return nil
		})
	})
}

// The listing is ordered created_at DESC, id DESC, honours the group filter and
// the page bound, and reports the total the filter matches.
func TestListOrdersFiltersAndPaginates(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		e.on(t, host{}).drive(t, func(_ context.Context, reg *core.Registry) error {
			in := inboxOf(t, reg)
			s := product(t, reg)
			ctx := tenantCtx(t, reg, "acme")

			// Two rows in one instant and one later; the two share created_at so
			// their order is decided by the identifier.
			plant(t, s, "acme", message{ID: "a", RecipientUserID: "user-1", TypeKey: "t", Group: "billing", CreatedAt: 100})
			plant(t, s, "acme", message{ID: "b", RecipientUserID: "user-1", TypeKey: "t", Group: "billing", CreatedAt: 100})
			plant(t, s, "acme", message{ID: "c", RecipientUserID: "user-1", TypeKey: "t", Group: "security", CreatedAt: 200})
			// Another recipient's row must never appear.
			plant(t, s, "acme", message{ID: "z", RecipientUserID: "user-2", TypeKey: "t", Group: "billing", CreatedAt: 300})

			page, err := in.List(ctx, "user-1", notification.ListQuery{Limit: 2})
			if err != nil {
				t.Errorf("listing: %v", err)
				return errCaseFailed
			}
			if page.Total != 3 {
				t.Errorf("the total is %d, want 3", page.Total)
			}
			if len(page.Items) != 2 {
				t.Fatalf("the page holds %d items, want 2", len(page.Items))
			}
			if page.Items[0].ID != "c" || page.Items[1].ID != "b" {
				t.Errorf("the first page is %s,%s, want c,b", page.Items[0].ID, page.Items[1].ID)
			}

			next, err := in.List(ctx, "user-1", notification.ListQuery{Limit: 2, Offset: 2})
			if err != nil {
				t.Errorf("listing the second page: %v", err)
				return errCaseFailed
			}
			if len(next.Items) != 1 || next.Items[0].ID != "a" {
				t.Errorf("the second page is %+v, want just a", next.Items)
			}

			grouped, err := in.List(ctx, "user-1", notification.ListQuery{Group: "billing"})
			if err != nil {
				t.Errorf("listing by group: %v", err)
				return errCaseFailed
			}
			if grouped.Total != 2 || len(grouped.Items) != 2 {
				t.Errorf("the billing page is %+v (total %d), want the two billing rows", grouped.Items, grouped.Total)
			}
			return nil
		})
	})
}

// The listing hides expired rows.
func TestListHidesExpiredRows(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		e.on(t, host{}).drive(t, func(_ context.Context, reg *core.Registry) error {
			in := inboxOf(t, reg)
			s := product(t, reg)
			ctx := tenantCtx(t, reg, "acme")

			future := int64(1) << 62
			past := int64(1)
			plant(t, s, "acme", message{ID: "live", RecipientUserID: "user-1", TypeKey: "t", ExpiresAt: &future, CreatedAt: 100})
			plant(t, s, "acme", message{ID: "dead", RecipientUserID: "user-1", TypeKey: "t", ExpiresAt: &past, CreatedAt: 200})
			plant(t, s, "acme", message{ID: "forever", RecipientUserID: "user-1", TypeKey: "t", CreatedAt: 300})

			page, err := in.List(ctx, "user-1", notification.ListQuery{})
			if err != nil {
				t.Errorf("listing: %v", err)
				return errCaseFailed
			}
			if page.Total != 2 || len(page.Items) != 2 {
				t.Errorf("the page is %+v (total %d), want the unexpired two", page.Items, page.Total)
			}
			for _, m := range page.Items {
				if m.ID == "dead" {
					t.Errorf("the expired row appears in the listing")
				}
			}
			return nil
		})
	})
}

// The unread count counts only unread, unexpired rows.
func TestUnreadCountCountsOnlyUnreadUnexpired(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		e.on(t, host{}).drive(t, func(_ context.Context, reg *core.Registry) error {
			in := inboxOf(t, reg)
			s := product(t, reg)
			ctx := tenantCtx(t, reg, "acme")

			future := int64(1) << 62
			past := int64(1)
			read := int64(50)
			plant(t, s, "acme", message{ID: "unread", RecipientUserID: "user-1", TypeKey: "t", CreatedAt: 100})
			plant(t, s, "acme", message{ID: "read", RecipientUserID: "user-1", TypeKey: "t", ReadAt: &read, CreatedAt: 200})
			plant(t, s, "acme", message{ID: "expired", RecipientUserID: "user-1", TypeKey: "t", ExpiresAt: &past, CreatedAt: 300})
			plant(t, s, "acme", message{ID: "live", RecipientUserID: "user-1", TypeKey: "t", ExpiresAt: &future, CreatedAt: 400})
			plant(t, s, "acme", message{ID: "other", RecipientUserID: "user-2", TypeKey: "t", CreatedAt: 500})

			n, err := in.UnreadCount(ctx, "user-1")
			if err != nil {
				t.Errorf("counting the unread messages: %v", err)
				return errCaseFailed
			}
			if n != 2 {
				t.Errorf("the unread count is %d, want 2 (unread and live)", n)
			}
			return nil
		})
	})
}

// MarkRead sets read_at once, keeps the first-read timestamp across a repeat,
// and answers the same ErrMessageNotFound for an unknown identifier, another
// recipient's and another tenant's.
func TestMarkReadIsIdempotentAndAnswersOneNotFound(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		e.on(t, host{}).drive(t, func(_ context.Context, reg *core.Registry) error {
			in := inboxOf(t, reg)
			s := product(t, reg)
			acme := tenantCtx(t, reg, "acme")

			plant(t, s, "acme", message{ID: "m1", RecipientUserID: "user-1", TypeKey: "t", CreatedAt: 100})
			plant(t, s, "acme", message{ID: "mine-other", RecipientUserID: "user-2", TypeKey: "t", CreatedAt: 100})
			plant(t, s, "globex", message{ID: "theirs", RecipientUserID: "user-1", TypeKey: "t", CreatedAt: 100})

			if err := in.MarkRead(acme, "user-1", "m1"); err != nil {
				t.Errorf("marking the message read: %v", err)
				return errCaseFailed
			}
			firstRead := rowsOf(t, s)["m1"].ReadAt
			if firstRead == nil {
				t.Errorf("the message carries no first-read timestamp after being marked")
				return errCaseFailed
			}

			// A repeat is a successful no-op that keeps the first-read instant.
			if err := in.MarkRead(acme, "user-1", "m1"); err != nil {
				t.Errorf("repeating the marking gave %v, want success", err)
			}
			if again := rowsOf(t, s)["m1"].ReadAt; again == nil || *again != *firstRead {
				t.Errorf("the first-read timestamp moved from %v to %v across a repeat", *firstRead, again)
			}

			// The three invisible cases are one answer.
			for _, id := range []string{"ghost", "mine-other", "theirs"} {
				if err := in.MarkRead(acme, "user-1", id); !errors.Is(err, notification.ErrMessageNotFound) {
					t.Errorf("marking %q gave %v, want ErrMessageNotFound", id, err)
				}
			}
			return nil
		})
	})
}

// ReadAll marks every unread row of the recipient read, including an expired
// one, returns the count, and a repeat matches nothing.
func TestReadAllIsIdempotentAndIgnoresExpiry(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		e.on(t, host{}).drive(t, func(_ context.Context, reg *core.Registry) error {
			in := inboxOf(t, reg)
			s := product(t, reg)
			ctx := tenantCtx(t, reg, "acme")

			read := int64(50)
			past := int64(1)
			plant(t, s, "acme", message{ID: "a", RecipientUserID: "user-1", TypeKey: "t", CreatedAt: 100})
			plant(t, s, "acme", message{ID: "b", RecipientUserID: "user-1", TypeKey: "t", ReadAt: &read, CreatedAt: 100})
			plant(t, s, "acme", message{ID: "c", RecipientUserID: "user-1", TypeKey: "t", ExpiresAt: &past, CreatedAt: 100})
			plant(t, s, "acme", message{ID: "d", RecipientUserID: "user-2", TypeKey: "t", CreatedAt: 100})

			n, err := in.ReadAll(ctx, "user-1")
			if err != nil {
				t.Errorf("marking all read: %v", err)
				return errCaseFailed
			}
			if n != 2 {
				t.Errorf("ReadAll marked %d rows, want 2 (the unread rows, expired included)", n)
			}
			rows := rowsOf(t, s)
			for _, id := range []string{"a", "c"} {
				if rows[id].ReadAt == nil {
					t.Errorf("the row %q was not marked read", id)
				}
			}
			if rows["b"].ReadAt == nil || *rows["b"].ReadAt != read {
				t.Errorf("the already-read row's timestamp was rewritten to %v", rows["b"].ReadAt)
			}
			if rows["d"].ReadAt != nil {
				t.Errorf("another recipient's row was marked read")
			}

			again, err := in.ReadAll(ctx, "user-1")
			if err != nil {
				t.Errorf("repeating ReadAll: %v", err)
				return errCaseFailed
			}
			if again != 0 {
				t.Errorf("the repeat marked %d rows, want 0", again)
			}
			return nil
		})
	})
}

// A row written under one tenant is invisible to another: the recipient's read
// never reaches across tenants.
func TestRowsAreScopedToTheTenant(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		e.on(t, host{}).drive(t, func(_ context.Context, reg *core.Registry) error {
			in := inboxOf(t, reg)
			s := product(t, reg)
			acme := tenantCtx(t, reg, "acme")
			globex := tenantCtx(t, reg, "globex")

			writeOne(t, in, acme, "user-1", "key-1")

			n, err := in.UnreadCount(globex, "user-1")
			if err != nil {
				t.Errorf("counting in the other tenant: %v", err)
				return errCaseFailed
			}
			if n != 0 {
				t.Errorf("the other tenant sees %d unread messages, want 0", n)
			}
			page, err := in.List(globex, "user-1", notification.ListQuery{})
			if err != nil {
				t.Errorf("listing in the other tenant: %v", err)
				return errCaseFailed
			}
			if page.Total != 0 || len(page.Items) != 0 {
				t.Errorf("the other tenant's page is %+v, want empty", page)
			}
			if n := rowCount(t, s); n != 1 {
				t.Errorf("the table holds %d rows, want the single acme row", n)
			}
			return nil
		})
	})
}
