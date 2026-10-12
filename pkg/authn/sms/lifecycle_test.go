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

// TestTheHooksForDisabledAndRevokedSecretsAreNoOps holds the two hooks that touch
// nothing: a disabled account keeps its challenges, and a revoked service secret
// has none.
func TestTheHooksForDisabledAndRevokedSecretsAreNoOps(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.insert("row-1", authn.SMSPurposeMFA, r.database.BlindIndex(phone), ptr(string(acct)), false, "mac", 0, 5,
		base, base.Add(time.Minute), nil)

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

// TestAccountDeletedRemovesTheAccountsMFAAndEnrollRowsOnly holds the deletion: the
// account's mfa and enroll rows go, a login row and another account's row stay,
// and a repeated call removes nothing and is not an error.
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
	// Idempotent: a second call removes nothing and is not an error.
	if err := r.p.AccountDeleted(context.Background(), acct); err != nil {
		t.Fatalf("the second AccountDeleted: %v", err)
	}
}

// TestTheDeletionEventCompensatesAndIsDeduplicatedByItsID holds the compensation:
// the subscription cleans the account again, and a redelivery of the same event
// identifier is dropped so its cleanup does not run twice.
func TestTheDeletionEventCompensatesAndIsDeduplicatedByItsID(t *testing.T) {
	r := newRigSQLite(t)
	// The two subscriptions are registered in Init.
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

	// A redelivery of the same identifier is dropped: a row put back after the
	// first delivery survives the second.
	r.insert("mfa2", authn.SMSPurposeMFA, "m1", account, false, "mac", 0, 5, base, base.Add(time.Minute), nil)
	if err := r.bus.deliver(context.Background(), evt); err != nil {
		t.Fatalf("redelivering the event: %v", err)
	}
	if _, ok := r.column("mfa2", "id"); !ok {
		t.Error("a redelivery of one event identifier ran the cleanup again")
	}

	// A new identifier cleans again.
	fresh := event.Event{ID: "evt-2", Type: authn.EventAccountDeleted, Payload: authn.AccountDeleted{AccountID: string(acct)}}
	if err := r.bus.deliver(context.Background(), fresh); err != nil {
		t.Fatalf("delivering a fresh event: %v", err)
	}
	if _, ok := r.column("mfa2", "id"); ok {
		t.Error("a fresh event did not clean the account's rows")
	}
}

// TestAccountDeletedRefusesAnEmptyIdentifier holds the guard: an empty account
// identifier is malformed and is refused with the sentinel, not run as a delete
// scoped to the empty string, which would remove rows carrying an empty account.
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
