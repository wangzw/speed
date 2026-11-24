package budget

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestSuppressedRowsCountInDimensionsButNotAsSendsOrCeiling checks that a suppressed row
// spends no send budget or ceiling share but a dimension count can include it.
func TestSuppressedRowsCountInDimensionsButNotAsSendsOrCeiling(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		since := at.Add(-time.Second)
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D1", "T1", at))
		record(t, store, suppressedAttempt(ChannelAuthn, "login", "S1", "D2", "T1", at.Add(time.Second)))
		failed := record(t, store, attempt(ChannelAuthn, "login", "S1", "D3", "T1", at.Add(2*time.Second)))
		refund(t, store, failed, at.Add(3*time.Second))

		plain := Query{Dimension: DimensionSource, Value: "S1", Since: since}
		assertCount(t, count(t, store, plain), 1, plain)

		withSuppressed := plain
		withSuppressed.IncludeSuppressed = true
		assertCount(t, count(t, store, withSuppressed), 2, withSuppressed)
		assertCount(t, distinct(t, store, plain), 1, plain)
		assertCount(t, distinct(t, store, withSuppressed), 2, withSuppressed)

		ceiling, err := store.CountCeiling(context.Background(), since)
		if err != nil {
			t.Fatalf("counting the ceiling: %v", err)
		}
		if ceiling != 1 {
			t.Errorf("the cross-channel ceiling = %d, want 1 (the suppressed and refunded rows excluded)", ceiling)
		}

		attempts := Query{Since: since, IncludeRefunded: true}
		assertCount(t, count(t, store, attempts), 2, attempts)
		attempts.IncludeSuppressed = true
		assertCount(t, count(t, store, attempts), 3, attempts)
	})
}

// TestOnlyRefundedCountsDeterminateFailuresOnly checks the determinate-failure shape: a refunded
// row counts, a successful send does not, and a suppressed row does not by default.
func TestOnlyRefundedCountsDeterminateFailuresOnly(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		since := at.Add(-time.Second)
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D1", "T1", at))
		uncertain := attempt(ChannelAuthn, "login", "S1", "D2", "T1", at.Add(time.Second))
		uncertain.Outcome = OutcomeUncertain
		record(t, store, uncertain)
		failed := record(t, store, attempt(ChannelAuthn, "login", "S1", "D3", "T1", at.Add(2*time.Second)))
		refund(t, store, failed, at.Add(3*time.Second))
		record(t, store, suppressedAttempt(ChannelAuthn, "login", "S1", "D4", "T1", at.Add(4*time.Second)))

		only := Query{Channel: ChannelAuthn, Since: since, OnlyRefunded: true}
		assertCount(t, count(t, store, only), 1, only)

		withSuppressed := only
		withSuppressed.IncludeSuppressed = true
		assertCount(t, count(t, store, withSuppressed), 2, withSuppressed)

		attempts := Query{Channel: ChannelAuthn, Since: since, IncludeRefunded: true}
		assertCount(t, count(t, store, attempts), 3, attempts)

		conflict := Query{Channel: ChannelAuthn, Since: since, OnlyRefunded: true, IncludeRefunded: true}
		if _, err := store.Count(context.Background(), conflict); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("Count(%+v) returned %v, want ErrInvalidQuery", conflict, err)
		}
	})
}

// TestLatestReturnsTheNewestAttempt checks that Latest reads the same rows Count does.
func TestLatestReturnsTheNewestAttempt(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		since := at.Add(-time.Second)
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D1", "T1", at))
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D2", "T1", at.Add(time.Minute)))
		record(t, store, attempt(ChannelNotification, "alert", "S1", "D3", "T1", at.Add(2*time.Minute)))

		q := Query{Dimension: DimensionDestination, Value: "D2", Since: since}
		got, ok, err := store.Latest(context.Background(), q)
		if err != nil {
			t.Fatalf("reading the newest attempt: %v", err)
		}
		if !ok || !got.Equal(at.Add(time.Minute)) {
			t.Errorf("the newest attempt = %v (present %v), want %v", got, ok, at.Add(time.Minute))
		}

		authn := Query{Channel: ChannelAuthn, Since: since}
		got, ok, err = store.Latest(context.Background(), authn)
		if err != nil {
			t.Fatalf("reading the newest authentication attempt: %v", err)
		}
		if !ok || !got.Equal(at.Add(time.Minute)) {
			t.Errorf("the newest authentication attempt = %v (present %v), want %v", got, ok, at.Add(time.Minute))
		}

		newest := record(t, store, attempt(ChannelAuthn, "login", "S1", "D4", "T1", at.Add(3*time.Minute)))
		refund(t, store, newest, at.Add(4*time.Minute))
		got, ok, err = store.Latest(context.Background(), authn)
		if err != nil {
			t.Fatalf("reading the newest send: %v", err)
		}
		if !ok || !got.Equal(at.Add(time.Minute)) {
			t.Errorf("the newest send = %v (present %v), want %v; a refunded row is not a send",
				got, ok, at.Add(time.Minute))
		}

		attempts := authn
		attempts.IncludeRefunded = true
		got, ok, err = store.Latest(context.Background(), attempts)
		if err != nil {
			t.Fatalf("reading the newest attempt: %v", err)
		}
		if !ok || !got.Equal(at.Add(3*time.Minute)) {
			t.Errorf("the newest attempt = %v (present %v), want %v", got, ok, at.Add(3*time.Minute))
		}

		if _, ok, err := store.Latest(context.Background(), Query{Since: at.Add(time.Hour)}); err != nil || ok {
			t.Errorf("Latest over an empty window = (_, %v, %v), want (_, false, nil)", ok, err)
		}
		if _, _, err := store.Latest(context.Background(), Query{}); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("Latest with no since returned %v, want ErrInvalidQuery", err)
		}
	})
}

// TestTheAccountDimensionCountsByAccount checks that rows carry their account
// and that an attempt with none matches no account count.
func TestTheAccountDimensionCountsByAccount(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		since := at.Add(-time.Second)
		record(t, store, withAccount(attempt(ChannelAuthn, "mfa", "S1", "D1", "T1", at), "A1"))
		record(t, store, withAccount(attempt(ChannelAuthn, "enroll", "S1", "D2", "T1", at.Add(time.Second)), "A1"))
		record(t, store, withAccount(attempt(ChannelAuthn, "mfa", "S1", "D3", "T1", at.Add(2*time.Second)), "A2"))
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D4", "T1", at.Add(3*time.Second)))

		if !DimensionAccount.Valid() {
			t.Error("DimensionAccount is not one of the indexed dimensions")
		}
		q := Query{Dimension: DimensionAccount, Value: "A1", Since: since}
		assertCount(t, count(t, store, q), 2, q)
		q = Query{Dimension: DimensionAccount, Value: "A2", Since: since}
		assertCount(t, count(t, store, q), 1, q)
	})
}

// TestPruneBeforeRemovesOnlyOlderRows checks the retention sweep: a row strictly
// older than the cutoff goes, a row at or after it stays.
func TestPruneBeforeRemovesOnlyOlderRows(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D0", "T1", at.Add(-2*time.Hour)))
		old := record(t, store, attempt(ChannelAuthn, "login", "S1", "D1", "T1", at.Add(-time.Hour)))
		refund(t, store, old, at.Add(-30*time.Minute))
		record(t, store, suppressedAttempt(ChannelAuthn, "login", "S1", "D2", "T1", at.Add(-30*time.Minute)))
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D3", "T1", at))

		removed, err := store.PruneBefore(context.Background(), at.Add(-time.Hour))
		if err != nil {
			t.Fatalf("pruning: %v", err)
		}
		if removed != 1 {
			t.Errorf("PruneBefore removed %d rows, want 1 (only the row strictly older than the cutoff)", removed)
		}

		all := Query{Since: at.Add(-3 * time.Hour), IncludeRefunded: true, IncludeSuppressed: true}
		assertCount(t, count(t, store, all), 3, all)

		removed, err = store.PruneBefore(context.Background(), at.Add(-2*time.Hour))
		if err != nil {
			t.Fatalf("pruning an empty interval: %v", err)
		}
		if removed != 0 {
			t.Errorf("PruneBefore of an interval with nothing older removed %d rows, want 0", removed)
		}

		if _, pruneErr := store.PruneBefore(context.Background(), time.Time{}); !errors.Is(pruneErr, ErrInvalidQuery) {
			t.Errorf("PruneBefore with no cutoff returned %v, want ErrInvalidQuery", pruneErr)
		}

		removed, err = store.PruneBefore(context.Background(), at.Add(time.Hour))
		if err != nil {
			t.Fatalf("pruning the rest: %v", err)
		}
		if removed != 3 {
			t.Errorf("PruneBefore of the whole ledger removed %d rows, want 3", removed)
		}
	})
}
