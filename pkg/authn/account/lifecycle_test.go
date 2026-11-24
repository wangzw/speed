package account

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// Disabling moves valid_after to the next whole second and calls the disabled hook, voiding every earlier session.
func TestDisablingMovesValidAfterAndCallsTheDisabledHook(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("downer", "correct horse battery")
		at := h.clock.read()
		if err := h.p.SetStatus(ctx, id, authn.StatusDisabled, authn.ReasonAdmin); err != nil {
			t.Fatalf("disabling: %v", err)
		}
		var row accountRow
		if err := h.handle.Where("id = ?", id.String()).Take(&row).Error; err != nil {
			t.Fatalf("reading back: %v", err)
		}
		want := at.Truncate(time.Second).Add(time.Second)
		if row.Status != statusDisabled || !utc(row.ValidAfter).Equal(want) || row.DisabledAt == nil || !utc(*row.DisabledAt).Equal(at) {
			t.Errorf("after disabling: status %d, valid_after %v, disabled_at %v; want disabled, %v, %v",
				row.Status, row.ValidAfter, row.DisabledAt, want, at)
		}
		if seen := h.hook.seen(); len(seen) != 1 || seen[0] != "disabled:"+id.String() {
			t.Errorf("the hooks saw %v, want the disabled hook once", seen)
		}
		if events := h.bus.of(authn.EventAccountStatusChanged); len(events) != 1 {
			t.Errorf("%d status-changed events, want one", len(events))
		}
	})
}

// Enabling does not roll valid_after back, so a session from before the disable stays void.
func TestEnablingDoesNotRollValidAfterBack(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("rounder", "correct horse battery")
		if err := h.p.SetStatus(ctx, id, authn.StatusDisabled, authn.ReasonAdmin); err != nil {
			t.Fatalf("disabling: %v", err)
		}
		var before accountRow
		_ = h.handle.Where("id = ?", id.String()).Take(&before).Error
		h.clock.advance(time.Hour)
		if err := h.p.SetStatus(ctx, id, authn.StatusActive, authn.ReasonAdmin); err != nil {
			t.Fatalf("enabling: %v", err)
		}
		var after accountRow
		if err := h.handle.Where("id = ?", id.String()).Take(&after).Error; err != nil {
			t.Fatalf("reading back: %v", err)
		}
		if after.Status != statusActive || !utc(after.ValidAfter).Equal(utc(before.ValidAfter)) {
			t.Errorf("after enabling: status %d, valid_after %v; want active and the disable's %v",
				after.Status, after.ValidAfter, before.ValidAfter)
		}
		want := []string{"disabled:" + id.String(), "enabled:" + id.String()}
		if seen := h.hook.seen(); !slices.Equal(seen, want) {
			t.Errorf("the hooks saw %v, want the disable's and the enable's %v", seen, want)
		}
	})
}

// Enabling calls the enabled hook.
func TestEnablingCallsTheEnabledHook(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("restored", "correct horse battery")
		if err := h.p.SetStatus(ctx, id, authn.StatusDisabled, authn.ReasonAdmin); err != nil {
			t.Fatalf("disabling: %v", err)
		}
		if err := h.p.SetStatus(ctx, id, authn.StatusActive, authn.ReasonAdmin); err != nil {
			t.Fatalf("enabling: %v", err)
		}
		want := []string{"disabled:" + id.String(), "enabled:" + id.String()}
		if seen := h.hook.seen(); !slices.Equal(seen, want) {
			t.Errorf("the hooks saw %v, want %v", seen, want)
		}
	})
}

// Enabling an account already active changes nothing and still runs the enabled hook, which is how an incomplete cleanup is repeated.
func TestSetStatusToActiveOnAnActiveAccountRunsTheEnabledHook(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("already", "correct horse battery")
		if err := h.p.SetStatus(ctx, id, authn.StatusActive, authn.ReasonAdmin); err != nil {
			t.Fatalf("enabling: %v", err)
		}
		want := []string{"enabled:" + id.String()}
		if seen := h.hook.seen(); !slices.Equal(seen, want) {
			t.Errorf("the hooks saw %v, want %v", seen, want)
		}
		if events := h.bus.of(authn.EventAccountStatusChanged); len(events) != 0 {
			t.Errorf("%d status-changed events, want none for a status the account already had", len(events))
		}
	})
}

// A failing enabled hook leaves the account enabled and answers ErrCleanupIncomplete, and repeating the call finishes the cleanup.
func TestAFailingEnabledHookLeavesTheStateInForce(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("unlucky-up", "correct horse battery")
		if err := h.p.SetStatus(ctx, id, authn.StatusDisabled, authn.ReasonAdmin); err != nil {
			t.Fatalf("disabling: %v", err)
		}
		h.hook.fail("enabled:"+id.String(), errors.New("the session store is down"))
		err := h.p.SetStatus(ctx, id, authn.StatusActive, authn.ReasonAdmin)
		if !errors.Is(err, authn.ErrCleanupIncomplete) {
			t.Fatalf("a failing enabled hook gave %v, want ErrCleanupIncomplete", err)
		}
		var row accountRow
		if err := h.handle.Where("id = ?", id.String()).Take(&row).Error; err != nil {
			t.Fatalf("reading back: %v", err)
		}
		if row.Status != statusActive {
			t.Errorf("the account is status %d, want active in force despite the hook", row.Status)
		}
		h.hook.fail("enabled:"+id.String(), nil)
		if err := h.p.SetStatus(ctx, id, authn.StatusActive, authn.ReasonAdmin); err != nil {
			t.Fatalf("repeating the call: %v", err)
		}
	})
}

// Repeating a status the account already holds changes nothing but runs the cleanup again.
func TestSetStatusToTheStatusAlreadyHeldRunsTheCleanupAgain(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("repeat", "correct horse battery")
		if err := h.p.SetStatus(ctx, id, authn.StatusDisabled, authn.ReasonSelf); err != nil {
			t.Fatalf("disabling: %v", err)
		}
		var first accountRow
		_ = h.handle.Where("id = ?", id.String()).Take(&first).Error
		h.clock.advance(30 * time.Minute)
		if err := h.p.SetStatus(ctx, id, authn.StatusDisabled, authn.ReasonSelf); err != nil {
			t.Fatalf("disabling again: %v", err)
		}
		var second accountRow
		_ = h.handle.Where("id = ?", id.String()).Take(&second).Error
		if !utc(second.ValidAfter).Equal(utc(first.ValidAfter)) {
			t.Errorf("the second disable moved valid_after to %v, want the first's %v", second.ValidAfter, first.ValidAfter)
		}
		if len(h.hook.seen()) != 2 {
			t.Errorf("the hooks saw %d calls, want two", len(h.hook.seen()))
		}
		if events := h.bus.of(authn.EventAccountStatusChanged); len(events) != 1 {
			t.Errorf("%d status-changed events, want only the first disable's", len(events))
		}
	})
}

// Delete erases every row naming the account, keeps the tombstone, moves valid_after on, and calls the deleted hook.
func TestDeleteScrubsEveryTableAndLeavesOnlyTheTombstone(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("goner", "correct horse battery")
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierPhone, "+8613812345678", admin); err != nil {
			t.Fatalf("adding a phone: %v", err)
		}
		if err := h.p.LinkExternal(ctx, id, authn.ExternalLink{Provider: "google", Subject: "sub-1"}); err != nil {
			t.Fatalf("linking: %v", err)
		}
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true, Source: "admin"}); err != nil {
			t.Fatalf("joining: %v", err)
		}
		service := h.service("robot")
		if _, _, err := h.p.CreateServiceSecret(ctx, service, "ci", nil); err != nil {
			t.Fatalf("making a secret: %v", err)
		}
		at := h.clock.read()
		if err := h.p.Delete(ctx, id, authn.ReasonAccountDeleted); err != nil {
			t.Fatalf("deleting: %v", err)
		}
		var row accountRow
		if err := h.handle.Where("id = ?", id.String()).Take(&row).Error; err != nil {
			t.Fatalf("reading the tombstone: %v", err)
		}
		want := at.Truncate(time.Second).Add(time.Second)
		if row.Kind != "user" || row.Status != statusDeleted || row.DisplayName != "" || row.Attributes != "{}" ||
			row.MFARequired || row.Protected || row.LastLoginAt != nil || row.DisabledAt != nil ||
			row.DeletedAt == nil || !utc(row.ValidAfter).Equal(want) {
			t.Errorf("the tombstone is %+v, want only id, kind, status, valid_after, created_at and deleted_at", row)
		}
		for _, table := range []string{tableIdentifiers, tableExternals, tableMemberships, tableCredentials} {
			if n := h.count(table, "account_id = ?", id.String()); n != 0 {
				t.Errorf("%s still holds %d row(s) for the account", table, n)
			}
		}

		if n := h.count(tableServiceKeys, "account_id = ?", service.String()); n != 1 {
			t.Errorf("the service account's secret is %d rows, want one left alone", n)
		}
		if seen := h.hook.seen(); len(seen) != 1 || seen[0] != "deleted:"+id.String() {
			t.Errorf("the hooks saw %v, want the deleted hook once", seen)
		}
	})
}

// A failing hook leaves the new state in force and answers ErrCleanupIncomplete, and repeating the call finishes the cleanup idempotently.
func TestAFailingHookLeavesTheStateInForceAndCompletesOnRetry(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("unlucky", "correct horse battery")
		failure := errors.New("the session store is down")
		h.hook.fail("disabled:"+id.String(), failure)
		at := h.clock.read()
		err := h.p.SetStatus(ctx, id, authn.StatusDisabled, authn.ReasonAdmin)
		if !errors.Is(err, authn.ErrCleanupIncomplete) {
			t.Fatalf("a failing hook gave %v, want ErrCleanupIncomplete", err)
		}
		var row accountRow
		if err := h.handle.Where("id = ?", id.String()).Take(&row).Error; err != nil {
			t.Fatalf("reading back: %v", err)
		}
		want := at.Truncate(time.Second).Add(time.Second)
		if row.Status != statusDisabled || !utc(row.ValidAfter).Equal(want) {
			t.Errorf("the state is status %d, valid_after %v; want disabled and %v in force despite the hook",
				row.Status, row.ValidAfter, want)
		}
		h.hook.fail("disabled:"+id.String(), nil)
		if err := h.p.SetStatus(ctx, id, authn.StatusDisabled, authn.ReasonAdmin); err != nil {
			t.Fatalf("repeating the call: %v", err)
		}
		if len(h.hook.seen()) != 2 {
			t.Errorf("the hooks saw %d calls, want the failure and the retry", len(h.hook.seen()))
		}
	})
}

// The cleanup counter names the failure, so that an operator sees it.
func TestTheCleanupCounterNamesAFailingHook(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("counted", "correct horse battery")
		h.hook.fail("disabled:"+id.String(), errors.New("down"))
		meters := h.withMeters()
		if err := h.p.SetStatus(ctx, id, authn.StatusDisabled, authn.ReasonAdmin); !errors.Is(err, authn.ErrCleanupIncomplete) {
			t.Fatalf("the disable gave %v, want ErrCleanupIncomplete", err)
		}
		if got := meters.counter(cleanupInstrument, nil); got < 1 {
			t.Errorf("the cleanup counter is %d, want at least one failure counted", got)
		}
	})
}

// A status change and a deletion stage their events in the same transaction, so they publish on commit and never on rollback.
func TestAChangeStagesItsEventsInTheSameTransaction(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.withOutbox()
		id := h.user("stager", "correct horse battery")
		if err := h.p.SetStatus(ctx, id, authn.StatusDisabled, authn.ReasonAdmin); err != nil {
			t.Fatalf("disabling: %v", err)
		}
		if !slicesContains(h.staged(), authn.EventAccountStatusChanged) {
			t.Errorf("the status change staged %v, want the status-changed event", h.staged())
		}
		if got := h.bus.of(authn.EventAccountStatusChanged); len(got) != 0 {
			t.Errorf("the status-changed event reached the bus %d times, want it staged and not published", len(got))
		}
		if err := h.p.Delete(ctx, id, authn.ReasonAdmin); err != nil {
			t.Fatalf("deleting: %v", err)
		}
		if !slicesContains(h.staged(), authn.EventAccountDeleted) {
			t.Errorf("the deletion staged %v, want the deleted event", h.staged())
		}
	})
}

// Deletion stages a membership change for every tenant the account belonged to, each carrying its tenant.
func TestDeletionStagesAMembershipChangeForEveryTenant(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.withOutbox()
		id := h.user("member", "correct horse battery")
		for _, tenant := range []string{"acme", "globex"} {
			if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: tenant, Active: true, Source: "admin"}); err != nil {
				t.Fatalf("joining %s: %v", tenant, err)
			}
		}

		if err := h.handle.Exec("DELETE FROM test_staged").Error; err != nil {
			t.Fatalf("clearing the staged rows: %v", err)
		}
		if err := h.p.Delete(ctx, id, authn.ReasonAdmin); err != nil {
			t.Fatalf("deleting: %v", err)
		}
		got := map[string]bool{}
		for _, row := range h.staged() {
			if row.Type == authn.EventMembershipChanged {
				got[row.TenantID] = true
			}
		}
		if len(got) != 2 || !got["acme"] || !got["globex"] {
			t.Errorf("the deletion staged membership changes with tenants %v, want one each for acme and globex", got)
		}
	})
}

// A staging failure fails the write and rolls its own tables back: the event and the change stand or fall together.
func TestAStagingFailureRollsTheWriteBack(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("atomic", "correct horse battery")
		outbox := h.withOutbox()
		outbox.fail = errors.New("the outbox is down")
		err := h.p.SetStatus(ctx, id, authn.StatusDisabled, authn.ReasonAdmin)
		if !errors.Is(err, authn.ErrUnavailable) {
			t.Fatalf("a staging failure gave %v, want ErrUnavailable", err)
		}
		var row accountRow
		if err := h.handle.Where("id = ?", id.String()).Take(&row).Error; err != nil {
			t.Fatalf("reading back: %v", err)
		}
		if row.Status != statusActive {
			t.Errorf("the status is %d after a staging failure, want the change rolled back to active", row.Status)
		}
	})
}

// A bus that refuses an event is a Warn and nothing more: the write stands.
func TestABusThatRefusesAnEventDoesNotFailTheWrite(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("shrug", "correct horse battery")
		h.bus.failWith = errors.New("no subscribers reach")
		if err := h.p.SetStatus(ctx, id, authn.StatusDisabled, authn.ReasonAdmin); err != nil {
			t.Fatalf("the write gave %v, want it to stand despite the bus", err)
		}
		if lines := h.logs.lines("could not be published"); len(lines) == 0 {
			t.Errorf("no line warns that the event could not be published: %s", h.logs.String())
		}
	})
}

// Revoking a service secret writes the revocation, calls the revoked hook, and reports an incomplete cleanup.
func TestRevokingASecretCallsTheHookAndReportsIncompleteCleanup(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		service := h.service("robot")
		secretID, _, err := h.p.CreateServiceSecret(ctx, service, "ci", nil)
		if err != nil {
			t.Fatalf("making a secret: %v", err)
		}
		h.hook.fail("revoked:"+service.String()+":"+secretID, errors.New("down"))
		err = h.p.RevokeServiceSecret(ctx, service, secretID)
		if !errors.Is(err, authn.ErrCleanupIncomplete) {
			t.Fatalf("the revocation gave %v, want ErrCleanupIncomplete", err)
		}
		var row secretRow
		if err := h.handle.Where("id = ?", secretID).Take(&row).Error; err != nil {
			t.Fatalf("reading the secret: %v", err)
		}
		if row.RevokedAt == nil {
			t.Error("the revocation is not in force though the hook failed")
		}
		h.hook.fail("revoked:"+service.String()+":"+secretID, nil)
		if err := h.p.RevokeServiceSecret(ctx, service, secretID); err != nil {
			t.Fatalf("repeating the revocation: %v", err)
		}
	})
}

func slicesContains(rows []stagedRow, eventType string) bool {
	for _, row := range rows {
		if row.Type == eventType {
			return true
		}
	}
	return false
}

func (h *harness) service(name string) authn.AccountID {
	h.t.Helper()
	created, err := h.p.Create(context.Background(), authn.CreateAccount{
		Kind: authn.KindService, Username: name, Via: "admin",
	})
	if err != nil {
		h.t.Fatalf("creating the service account: %v", err)
	}
	h.hook.reset()
	return created.ID
}
