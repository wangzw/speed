package sms

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// TestTheHooksForEnabledDisabledAndRevokedSecretsAreNoOps checks that the enabled, disabled and revoked-secret hooks are no-ops.
func TestTheHooksForEnabledDisabledAndRevokedSecretsAreNoOps(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.insert("row-1", authn.SMSPurposeMFA, r.database.BlindIndex(phone), ptr(string(acct)), false, "mac", 0, 5,
		base, base.Add(time.Minute), nil)

	if err := r.p.AccountEnabled(context.Background(), acct); err != nil {
		t.Errorf("AccountEnabled returned %v, want nil", err)
	}
	if err := r.p.AccountDisabled(context.Background(), acct); err != nil {
		t.Errorf("AccountDisabled returned %v, want nil", err)
	}
	if err := r.p.ServiceSecretRevoked(context.Background(), acct, "secret-1"); err != nil {
		t.Errorf("ServiceSecretRevoked returned %v, want nil", err)
	}
	if got := r.rowCount(); got != 1 {
		t.Errorf("the table holds %d rows after the two no-op hooks, want 1", got)
	}
}

// TestAccountDeletedRemovesTheAccountsMFAAndEnrollRowsOnly checks that AccountDeleted removes only the account's mfa and enroll rows.
func TestAccountDeletedRemovesTheAccountsMFAAndEnrollRowsOnly(t *testing.T) {
	r := newRigSQLite(t)
	account := ptr(string(acct))
	other := ptr("acct-2")
	r.insert("mfa", authn.SMSPurposeMFA, "m1", account, false, "mac", 0, 5, base, base.Add(time.Minute), nil)
	r.insert("enroll", authn.SMSPurposeEnroll, "m2", account, false, "mac", 0, 5, base, base.Add(time.Minute), nil)
	r.insert("login", authn.SMSPurposeLogin, "m3", nil, false, "mac", 0, 5, base, base.Add(time.Minute), nil)
	r.insert("other", authn.SMSPurposeMFA, "m4", other, false, "mac", 0, 5, base, base.Add(time.Minute), nil)

	if err := r.p.AccountDeleted(context.Background(), acct); err != nil {
		t.Fatalf("AccountDeleted: %v", err)
	}
	if _, ok := r.column("mfa", "id"); ok {
		t.Error("an mfa row of the deleted account survived")
	}
	if _, ok := r.column("enroll", "id"); ok {
		t.Error("an enroll row of the deleted account survived")
	}
	if _, ok := r.column("login", "id"); !ok {
		t.Error("a login row was removed by the account deletion")
	}
	if _, ok := r.column("other", "id"); !ok {
		t.Error("another account's row was removed")
	}
	if err := r.p.AccountDeleted(context.Background(), acct); err != nil {
		t.Fatalf("the second AccountDeleted: %v", err)
	}
}

// TestTheDeletionEventCompensatesAndIsDeduplicatedByItsID checks that the deletion event compensates and is deduplicated by its identifier.
func TestTheDeletionEventCompensatesAndIsDeduplicatedByItsID(t *testing.T) {
	r := newRigSQLite(t)
	types := r.bus.subscribedTypes()
	if !contains(types, authn.EventAccountDeleted) || !contains(types, settings.ChangedEventType) {
		t.Fatalf("the module subscribed to %v, want the deletion and the settings change", types)
	}

	account := ptr(string(acct))
	r.insert("mfa", authn.SMSPurposeMFA, "m1", account, false, "mac", 0, 5, base, base.Add(time.Minute), nil)
	evt := event.Event{ID: "evt-1", Type: authn.EventAccountDeleted, Payload: authn.AccountDeleted{AccountID: string(acct)}}
	if err := r.bus.deliver(context.Background(), evt); err != nil {
		t.Fatalf("delivering the deletion event: %v", err)
	}
	if _, ok := r.column("mfa", "id"); ok {
		t.Error("the deletion event did not clean the account's rows")
	}

	r.insert("mfa2", authn.SMSPurposeMFA, "m1", account, false, "mac", 0, 5, base, base.Add(time.Minute), nil)
	if err := r.bus.deliver(context.Background(), evt); err != nil {
		t.Fatalf("redelivering the event: %v", err)
	}
	if _, ok := r.column("mfa2", "id"); !ok {
		t.Error("a redelivery of one event identifier ran the cleanup again")
	}

	fresh := event.Event{ID: "evt-2", Type: authn.EventAccountDeleted, Payload: authn.AccountDeleted{AccountID: string(acct)}}
	if err := r.bus.deliver(context.Background(), fresh); err != nil {
		t.Fatalf("delivering a fresh event: %v", err)
	}
	if _, ok := r.column("mfa2", "id"); ok {
		t.Error("a fresh event did not clean the account's rows")
	}
}

// TestAccountDeletedRefusesAnEmptyIdentifier checks that AccountDeleted refuses an empty identifier.
func TestAccountDeletedRefusesAnEmptyIdentifier(t *testing.T) {
	r := newRigSQLite(t)
	if err := r.p.AccountDeleted(context.Background(), ""); !errors.Is(err, authn.ErrInvalidAccountID) {
		t.Fatalf("AccountDeleted with an empty identifier returned %v, want ErrInvalidAccountID", err)
	}

	empty := ptr("")
	r.insert("row", authn.SMSPurposeMFA, "m1", empty, false, "mac", 0, 5, base, base.Add(time.Minute), nil)
	if err := r.p.AccountDeleted(context.Background(), ""); !errors.Is(err, authn.ErrInvalidAccountID) {
		t.Fatalf("AccountDeleted with an empty identifier returned %v, want ErrInvalidAccountID", err)
	}
	if _, ok := r.column("row", "id"); !ok {
		t.Error("an empty-identifier call removed a row")
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func ptr[T any](v T) *T { return &v }
