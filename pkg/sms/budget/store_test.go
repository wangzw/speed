package budget

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestCountsArePerDimensionAndChannel checks that each count returns the rows of its own
// dimension and channel, and an empty filter widens rather than taking a default.
func TestCountsArePerDimensionAndChannel(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		since := at.Add(-time.Second)
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D1", "T1", at))
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D2", "T1", at.Add(time.Second)))
		record(t, store, attempt(ChannelAuthn, "mfa", "S2", "D1", "T1", at.Add(2*time.Second)))
		record(t, store, attempt(ChannelNotification, "alert", "S1", "D3", "T2", at.Add(3*time.Second)))
		record(t, store, attempt(ChannelNotification, "alert", "S1", "D4", "T2", at.Add(4*time.Second)))

		cases := []struct {
			q    Query
			want int64
		}{
			{Query{Since: since}, 5},
			{Query{Channel: ChannelAuthn, Since: since}, 3},
			{Query{Channel: ChannelNotification, Since: since}, 2},
			{Query{Channel: ChannelAuthn, Purpose: "login", Since: since}, 2},
			{Query{Purpose: "alert", Since: since}, 2},
			{Query{Dimension: DimensionSource, Value: "S1", Since: since}, 4},
			{Query{Dimension: DimensionSource, Value: "S1", Channel: ChannelAuthn, Since: since}, 2},
			{Query{Dimension: DimensionSource, Value: "S2", Since: since}, 1},
			{Query{Dimension: DimensionDestination, Value: "D1", Since: since}, 2},
			{Query{Dimension: DimensionTenant, Value: "T1", Since: since}, 3},
			{Query{Dimension: DimensionTenant, Value: "T2", Since: since}, 2},
		}
		for _, c := range cases {
			assertCount(t, count(t, store, c.q), c.want, c.q)
		}
	})
}

// TestDistinctDestinationsDoNotDoubleCount checks that attempts to one destination count as one destination.
func TestDistinctDestinationsDoNotDoubleCount(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		since := at.Add(-time.Second)
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D1", "T1", at))
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D1", "T1", at.Add(time.Second)))
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D2", "T1", at.Add(2*time.Second)))
		record(t, store, attempt(ChannelNotification, "alert", "S1", "D3", "T2", at.Add(3*time.Second)))

		authn := Query{Dimension: DimensionSource, Value: "S1", Channel: ChannelAuthn, Since: since}
		assertCount(t, count(t, store, authn), 3, authn)
		assertCount(t, distinct(t, store, authn), 2, authn)

		both := Query{Dimension: DimensionSource, Value: "S1", Since: since}
		assertCount(t, count(t, store, both), 4, both)
		assertCount(t, distinct(t, store, both), 3, both)
	})
}

// TestARefundedAttemptLeavesTheSendCountAndStaysInTheAttemptCount checks that a
// refunded attempt costs no send budget but stays in the attempt count.
func TestARefundedAttemptLeavesTheSendCountAndStaysInTheAttemptCount(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		first := record(t, store, attempt(ChannelAuthn, "login", "S1", "D1", "T1", at))
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D2", "T1", at.Add(time.Minute)))
		refund(t, store, first, at.Add(2*time.Minute))

		since := at.Add(-time.Second)
		sends := Query{Dimension: DimensionSource, Value: "S1", Since: since}
		attempts := sends
		attempts.IncludeRefunded = true

		assertCount(t, count(t, store, sends), 1, sends)
		assertCount(t, count(t, store, attempts), 2, attempts)
		assertCount(t, distinct(t, store, sends), 1, sends)
		assertCount(t, distinct(t, store, attempts), 2, attempts)

		oldest, ok, err := store.Oldest(context.Background(), sends)
		if err != nil {
			t.Fatalf("reading the oldest send: %v", err)
		}
		if !ok || !oldest.Equal(at.Add(time.Minute)) {
			t.Errorf("the oldest send = %v (present %v), want %v", oldest, ok, at.Add(time.Minute))
		}
		oldest, ok, err = store.Oldest(context.Background(), attempts)
		if err != nil {
			t.Fatalf("reading the oldest attempt: %v", err)
		}
		if !ok || !oldest.Equal(at) {
			t.Errorf("the oldest attempt = %v (present %v), want %v", oldest, ok, at)
		}

		refund(t, store, first, at.Add(3*time.Minute))
		assertCount(t, count(t, store, sends), 1, sends)
		assertCount(t, count(t, store, attempts), 2, attempts)

		empty := Query{Dimension: DimensionSource, Value: "S1", Since: at.Add(time.Hour)}
		if _, ok, err := store.Oldest(context.Background(), empty); err != nil || ok {
			t.Errorf("Oldest over an empty window = (_, %v, %v), want (_, false, nil)", ok, err)
		}
	})
}

// TestTheCeilingCountsBothChannels checks that the ceiling counts both channels and that a refunded attempt spends none of it.
func TestTheCeilingCountsBothChannels(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D1", "T1", at))
		record(t, store, attempt(ChannelNotification, "alert", "P1", "D2", "T2", at.Add(time.Second)))
		refunded := record(t, store, attempt(ChannelAuthn, "login", "S1", "D3", "T1", at.Add(2*time.Second)))
		refund(t, store, refunded, at.Add(3*time.Second))

		ceiling, err := store.CountCeiling(context.Background(), at)
		if err != nil {
			t.Fatalf("counting the ceiling: %v", err)
		}
		if ceiling != 2 {
			t.Errorf("the cross-channel ceiling = %d, want 2 (one row per channel, the refunded one excluded)", ceiling)
		}
		all := Query{Since: at, IncludeRefunded: true}
		assertCount(t, count(t, store, all), 3, all)

		ceiling, err = store.CountCeiling(context.Background(), at.Add(time.Minute))
		if err != nil {
			t.Fatalf("counting the ceiling after every attempt: %v", err)
		}
		if ceiling != 0 {
			t.Errorf("the ceiling of an empty window = %d, want 0", ceiling)
		}
	})
}

// TestTheWindowBoundaryIsInclusive checks that an attempt written exactly at
// since is inside the window and one a second earlier is not.
func TestTheWindowBoundaryIsInclusive(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D0", "T1", at.Add(-time.Second)))
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D1", "T1", at))
		record(t, store, attempt(ChannelAuthn, "login", "S1", "D2", "T1", at.Add(time.Second)))

		for _, c := range []struct {
			since time.Time
			want  int64
		}{
			{at.Add(-time.Second), 3},
			{at, 2},
			{at.Add(time.Second), 1},
			{at.Add(2 * time.Second), 0},
		} {
			q := Query{Dimension: DimensionSource, Value: "S1", Since: c.since}
			assertCount(t, count(t, store, q), c.want, q)
		}

		oldest, ok, err := store.Oldest(context.Background(),
			Query{Dimension: DimensionSource, Value: "S1", Since: at})
		if err != nil {
			t.Fatalf("reading the oldest attempt at the boundary: %v", err)
		}
		if !ok || !oldest.Equal(at) {
			t.Errorf("the oldest attempt since the boundary = %v (present %v), want %v", oldest, ok, at)
		}
	})
}

// TestConcurrentRecordsDoNotLoseCounts checks that concurrent writers lose no
// row, since Record is one INSERT and Count reads the table.
func TestConcurrentRecordsDoNotLoseCounts(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		const (
			workers = 8
			each    = 10
		)
		failures := make(chan error, workers*each)
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := 0; i < each; i++ {
					a := attempt(ChannelAuthn, "login", "S1",
						fmt.Sprintf("D-%d-%d", w, i), "T1",
						at.Add(time.Duration(w*each+i)*time.Microsecond))
					if _, err := store.Record(context.Background(), a); err != nil {
						failures <- err
					}
				}
			}(w)
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			t.Errorf("a concurrent record failed: %v", err)
		}

		q := Query{Dimension: DimensionSource, Value: "S1", Since: at.Add(-time.Second), IncludeRefunded: true}
		assertCount(t, count(t, store, q), workers*each, q)
		assertCount(t, distinct(t, store, q), workers*each, q)
	})
}

// TestRecordRefusesAMalformedAttempt checks that every required field is enforced at the write and a refused attempt leaves no row.
func TestRecordRefusesAMalformedAttempt(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		good := attempt(ChannelAuthn, "login", "S1", "D1", "T1", at)
		empty := ""

		cases := []struct {
			name    string
			attempt Attempt
		}{
			{"no channel", func() Attempt { a := good; a.Channel = ""; return a }()},
			{"unknown channel", func() Attempt { a := good; a.Channel = "carrier"; return a }()},
			{"no purpose", func() Attempt { a := good; a.Purpose = ""; return a }()},
			{"blank purpose", func() Attempt { a := good; a.Purpose = "  "; return a }()},
			{"empty source", func() Attempt { a := good; a.SourceMAC = &empty; return a }()},
			{"no destination", func() Attempt { a := good; a.DestinationMAC = ""; return a }()},
			{"empty account", func() Attempt { a := good; a.AccountID = &empty; return a }()},
			{"no attempted time", func() Attempt { a := good; a.AttemptedAt = time.Time{}; return a }()},
			{"unknown outcome", func() Attempt { a := good; a.Outcome = "delivered"; return a }()},
			{"refund before send", func() Attempt { a := good; early := at.Add(-time.Second); a.RefundedAt = &early; return a }()},
			{"suppressed without a refund", func() Attempt { a := good; a.Suppressed = true; return a }()},
			{"nothing at all", Attempt{}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) { requireInvalidAttempt(t, store, c.attempt) })
		}

		record(t, store, attempt(ChannelAuthn, "login", "", "D2", "T1", at))
		record(t, store, suppressedAttempt(ChannelAuthn, "login", "S1", "D3", "T1", at))
	})
}

// TestCountRefusesAMalformedQuery checks that a query the implementation cannot compile is refused rather than read as a default.
func TestCountRefusesAMalformedQuery(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		queries := []struct {
			name  string
			query Query
		}{
			{"no since", Query{}},
			{"unknown channel", Query{Channel: "carrier", Since: at}},
			{"value without kind", Query{Value: "S1", Since: at}},
			{"kind without value", Query{Dimension: DimensionSource, Since: at}},
			{"unknown dimension", Query{Dimension: "region", Value: "eu", Since: at}},
			{"both refund selectors", Query{OnlyRefunded: true, IncludeRefunded: true, Since: at}},
		}
		for _, c := range queries {
			q := c.query
			t.Run(c.name, func(t *testing.T) {
				if _, err := store.Count(context.Background(), q); !errors.Is(err, ErrInvalidQuery) {
					t.Errorf("Count(%+v) returned %v, want ErrInvalidQuery", q, err)
				}
				if _, err := store.CountDistinctDestinations(context.Background(), q); !errors.Is(err, ErrInvalidQuery) {
					t.Errorf("CountDistinctDestinations(%+v) returned %v, want ErrInvalidQuery", q, err)
				}
				if _, _, err := store.Oldest(context.Background(), q); !errors.Is(err, ErrInvalidQuery) {
					t.Errorf("Oldest(%+v) returned %v, want ErrInvalidQuery", q, err)
				}
				if _, _, err := store.Latest(context.Background(), q); !errors.Is(err, ErrInvalidQuery) {
					t.Errorf("Latest(%+v) returned %v, want ErrInvalidQuery", q, err)
				}
			})
		}
		if _, err := store.CountCeiling(context.Background(), time.Time{}); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("CountCeiling with no since returned %v, want ErrInvalidQuery", err)
		}
		if _, err := store.PruneBefore(context.Background(), time.Time{}); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("PruneBefore with no cutoff returned %v, want ErrInvalidQuery", err)
		}
	})
}

// TestMarkRefundedRefusesAnUnknownAttempt checks that an absent attempt is reported rather than accepted as a no-op.
func TestMarkRefundedRefusesAnUnknownAttempt(t *testing.T) {
	forEachDialect(t, func(t *testing.T, store Store) {
		if err := store.MarkRefunded(context.Background(), ID(9999), at); !errors.Is(err, ErrNotFound) {
			t.Errorf("refunding an absent attempt returned %v, want ErrNotFound", err)
		}
		if err := store.MarkRefunded(context.Background(), ID(0), at); !errors.Is(err, ErrNotFound) {
			t.Errorf("refunding the zero identifier returned %v, want ErrNotFound", err)
		}
		if err := store.MarkRefunded(context.Background(), ID(1), time.Time{}); !errors.Is(err, ErrInvalidAttempt) {
			t.Errorf("refunding at no time returned %v, want ErrInvalidAttempt", err)
		}
	})
}
