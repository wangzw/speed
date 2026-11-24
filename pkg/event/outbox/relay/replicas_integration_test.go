//go:build integration

package relay

import (
	"testing"
	"time"

	"github.com/vislake/speed/pkg/event"
)

// On PostgreSQL a claim neither takes nor waits for the rows another
// claim is taking.
func TestAClaimSkipsTheRowsAnotherClaimIsTaking(t *testing.T) {
	e := postgresEngine
	dsn := e.locate(t)
	database := migrated(t, e, dsn)
	stager := newQuick(t, database, newRecordingBus("stager", &ledger{}), quick)
	for i := range 3 {
		if err := placeOrder(t.Context(), database, stager, orderID(i), event.NewID(), false); err != nil {
			t.Fatalf("placing order %d: %v", i, err)
		}
	}
	other := newQuick(t, e.open(t, dsn), newRecordingBus("other", &ledger{}), quick)

	tx := database.DB().WithContext(t.Context()).Begin()
	if tx.Error != nil {
		t.Fatalf("beginning the first claim's transaction: %v", tx.Error)
	}
	defer tx.Rollback()
	taken := 0
	err := func() error {
		rows, err := tx.Raw(stager.sql.claim, "first", time.Minute.Milliseconds(), 8).Rows()
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			taken++
		}
		return rows.Err()
	}()
	if err != nil {
		t.Fatalf("the first claim: %v", err)
	}
	if taken != 3 {
		t.Fatalf("the first claim took %d rows, want the 3 staged", taken)
	}

	answered := make(chan []staged, 1)
	failed := make(chan error, 1)
	go func() {
		claimed, err := other.claim()
		if err != nil {
			failed <- err
			return
		}
		answered <- claimed
	}()
	waiting := false
	select {
	case claimed := <-answered:
		if len(claimed) != 0 {
			t.Errorf("the second claim took %+v, which the first claim is taking", claimed)
		}
	case err := <-failed:
		t.Errorf("the second claim failed: %v", err)
	case <-time.After(2 * time.Second):
		waiting = true
		t.Errorf("the second claim is still waiting for the rows the first claim is taking")
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("committing the first claim: %v", err)
	}
	if !waiting {
		return
	}
	select {
	case claimed := <-answered:
		if len(claimed) != 0 {
			t.Errorf("once the first claim committed, the second took its rows as well: %+v", claimed)
		}
	case err := <-failed:
		t.Errorf("the second claim failed: %v", err)
	case <-time.After(settle):
	}
}
