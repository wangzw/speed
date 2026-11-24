package session

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// Start probes the table, and a table the migrations did not create is
// ErrNotMigrated, naming the remedy.
func TestStartRefusesAnUnmigratedTable(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d, withoutMigration())
		err := f.p.start(context.Background())
		if !errors.Is(err, authn.ErrNotMigrated) {
			t.Fatalf("start = %v, want ErrNotMigrated", err)
		}
	})
}

// Both dialects' wording for a missing table is recognised: SQLite says "no
// such table", PostgreSQL says the relation does not exist.
func TestAMissingTableIsRecognisedInBothDialects(t *testing.T) {
	for name, err := range map[string]error{
		"sqlite":   errors.New("no such table: authn_sessions"),
		"postgres": errors.New(`ERROR: relation "authn_sessions" does not exist (SQLSTATE 42P01)`),
	} {
		if !isMissingTable(err) {
			t.Errorf("%s: a missing table was not recognised: %v", name, err)
		}
	}
	if isMissingTable(errors.New("the connection was reset")) || isMissingTable(nil) {
		t.Error("a failure that is not a missing table was recognised as one")
	}
}

// Start over a migrated table succeeds, and Stop ends the sweep.
func TestStartAndStopDriveTheSweep(t *testing.T) {
	f := newFixture(t, dialects[0])
	if err := f.p.start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if f.p.sweep == nil {
		t.Fatal("start did not leave a sweep running")
	}
	f.p.stop(context.Background())
	if f.p.sweep != nil {
		t.Error("stop left the sweep in place")
	}
}

// The sweep deletes browser and token rows whose absolute period ended past
// retention, and service rows only once revoked and past retention.
func TestTheSweepDeletesEndedRowsPastRetentionAndNoOthers(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		f.settings.set(retentionPath, time.Hour)
		ctx := context.Background()

		endedBrowser := f.create(newBrowser(alice))
		endedToken := f.create(newToken1(alice))
		freshBrowser := f.create(newBrowser(alice))

		secret, err := f.p.ForCredential(ctx, alice, "secret-live", nil)
		noErr(t, err)
		endedSecret, err := f.p.ForCredential(ctx, alice, "secret-ended", nil)
		noErr(t, err)
		freshSecret, err := f.p.ForCredential(ctx, alice, "secret-fresh", nil)
		noErr(t, err)
		noErr(t, f.p.RevokeCredential(ctx, "secret-ended", authn.ReasonAdmin))
		noErr(t, f.p.RevokeCredential(ctx, "secret-fresh", authn.ReasonAdmin))

		now := f.clock.Now()
		f.exec("UPDATE "+tableName+" SET expires_at = ? WHERE id = ?", now.Add(-2*time.Hour), string(endedBrowser.ID))
		f.exec("UPDATE "+tableName+" SET expires_at = ? WHERE id = ?", now.Add(-2*time.Hour), string(endedToken.ID))
		f.exec("UPDATE "+tableName+" SET expires_at = ? WHERE id = ?", now.Add(-30*time.Minute), string(freshBrowser.ID))
		f.exec("UPDATE "+tableName+" SET revoked_at = ? WHERE id = ?", now.Add(-2*time.Hour), string(endedSecret))
		f.exec("UPDATE "+tableName+" SET revoked_at = ? WHERE id = ?", now.Add(-30*time.Minute), string(freshSecret))

		n, err := f.p.sweepOnce(ctx)
		noErr(t, err)
		if n != 3 {
			t.Errorf("the sweep deleted %d rows, want 3", n)
		}

		remaining := func(id authn.SessionID) bool {
			_, err := f.p.byID(ctx, id)
			return err == nil
		}
		if remaining(endedBrowser.ID) || remaining(endedToken.ID) {
			t.Error("a browser or token row past retention was kept")
		}
		if !remaining(freshBrowser.ID) {
			t.Error("a browser row within retention was deleted")
		}
		if !remaining(secret) {
			t.Error("a live service row was deleted by the clock")
		}
		if remaining(endedSecret) {
			t.Error("a revoked service row past retention was kept")
		}
		if !remaining(freshSecret) {
			t.Error("a revoked service row within retention was deleted")
		}
	})
}

// A run that deletes a whole batch of sweepBatch rows does not stop there: a
// backlog larger than one batch is worked off in the same run.
func TestTheSweepWorksOffMoreThanOneBatchInARun(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	f.settings.set(retentionPath, time.Hour)
	ctx := context.Background()
	now := f.clock.Now().UTC()
	ended := now.Add(-2 * time.Hour)

	const extra = 5
	want := sweepBatch + extra
	for i := range want {
		f.exec("INSERT INTO "+tableName+" (id, account_id, kind, data, created_at, last_seen_at, expires_at) "+
			"VALUES (?, ?, 'token', '{}', ?, ?, ?)",
			fmt.Sprintf("sid-%05d", i), string(alice), now, now, ended)
	}

	got, err := f.p.sweepOnce(ctx)
	noErr(t, err)
	if got != want {
		t.Errorf("the sweep deleted %d rows in one run, want %d: a full batch must not end the run", got, want)
	}
}

// TestTheSweepKeepsWhatIsExactlyAtRetention keeps a row exactly at the
// retention boundary, which the sweep deletes only strictly past.
func TestTheSweepKeepsWhatIsExactlyAtRetention(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	f.settings.set(retentionPath, time.Hour)
	ctx := context.Background()
	cutoff := f.clock.Now().Add(-time.Hour).UTC()

	browser := f.create(newBrowser(alice))
	token := f.create(newToken1(alice))
	svc, err := f.p.ForCredential(ctx, alice, "cred-boundary", nil)
	noErr(t, err)
	noErr(t, f.p.RevokeCredential(ctx, "cred-boundary", authn.ReasonAdmin))
	f.exec("UPDATE "+tableName+" SET expires_at = ? WHERE id = ?", cutoff, string(browser.ID))
	f.exec("UPDATE "+tableName+" SET expires_at = ? WHERE id = ?", cutoff, string(token.ID))
	f.exec("UPDATE "+tableName+" SET revoked_at = ? WHERE id = ?", cutoff, string(svc))

	n, err := f.p.sweepOnce(ctx)
	noErr(t, err)
	if n != 0 {
		t.Errorf("the sweep deleted %d rows exactly at the retention boundary, want 0", n)
	}
	for _, id := range []authn.SessionID{browser.ID, token.ID, svc} {
		if _, err := f.p.byID(ctx, id); err != nil {
			t.Errorf("the row %s exactly at the boundary was deleted: %v", id, err)
		}
	}
}
