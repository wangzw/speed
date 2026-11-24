package mfa

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

// TestTheSweepDeletesOnlyPendingRowsPastTheirTime checks that the sweep deletes only pending rows past their time.
func TestTheSweepDeletesOnlyPendingRowsPastTheirTime(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		ttl := time.Hour
		now := r.clock.read()
		at := func(age time.Duration) time.Time { return now.Add(-age) }

		old := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, at(ttl+time.Second), nil)
		oldTheirs := r.insertRow("someone-else", authn.FactorSMS, authn.FactorPending, at(48*time.Hour), nil)
		boundary := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, at(ttl), nil)
		young := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, at(ttl-time.Second), nil)
		activated := at(30 * time.Hour)
		active := r.insertRow(acct, authn.FactorTOTP, authn.FactorActive, at(48*time.Hour), &activated)
		disabled := r.insertRow(acct, authn.FactorTOTP, authn.FactorDisabled, at(48*time.Hour), nil)

		removed, err := r.p.Sweep(context.Background())
		if err != nil {
			t.Fatalf("Sweep: %v", err)
		}
		if removed != 2 {
			t.Errorf("Sweep removed %d, want the 2 pending rows past their time", removed)
		}
		for name, c := range map[string]struct {
			id   string
			gone bool
		}{
			"a pending row past its time":                 {old, true},
			"another account's pending row past its time": {oldTheirs, true},
			"a pending row exactly at its time":           {boundary, false},
			"a pending row inside its time":               {young, false},
			"an active row, however old":                  {active, false},
			"a disabled row, however old":                 {disabled, false},
		} {
			if _, there := r.status(c.id); there == c.gone {
				t.Errorf("%s: the row is there = %v, want %v", name, there, !c.gone)
			}
		}
		if len(totp.removed) != 1 || totp.removed[0] != old {
			t.Errorf("the factor was asked to clean %v, want the one row of its kind that went", totp.removed)
		}

		r.store.set(pendingTTLPath, 10*time.Minute)
		if n, err := r.p.Sweep(context.Background()); err != nil || n != 2 {
			t.Errorf("under a ten-minute setting Sweep = %d, %v, want the boundary and the young row", n, err)
		}
	})
}

// TestTheSweepReadsThePendingTTLFromTheSettingWithinItsDomain checks that the sweep reads the pending TTL from the setting within its domain.
func TestTheSweepReadsThePendingTTLFromTheSettingWithinItsDomain(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		r.store.set(pendingTTLPath, 100*time.Hour)
		id := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, r.clock.read().Add(-2*time.Hour), nil)
		if n, err := r.p.Sweep(context.Background()); err != nil || n != 1 {
			t.Fatalf("Sweep = %d, %v, want the two-hour-old row gone under the default", n, err)
		}
		if _, there := r.status(id); there {
			t.Error("the row stayed")
		}
		if len(r.logs.lines("level=ERROR", pendingTTLPath)) != 1 {
			t.Error("the setting outside its domain was not reported at Error")
		}
	})
}

// TestTheSweepGoesOnPastOneBatch checks that the sweep goes on past one batch.
func TestTheSweepGoesOnPastOneBatch(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h, newFactor(authn.FactorTOTP))
		old := r.clock.read().Add(-3 * time.Hour).UTC()
		total := sweepBatch + 250
		err := r.h.Transaction(func(tx *gorm.DB) error {
			for i := 0; i < total; i++ {
				if err := tx.Exec("INSERT INTO authn_factors (id, account_id, kind, label, status, created_at) VALUES (?, ?, 'totp', '', 0, ?)",
					fmt.Sprintf("bulk-%05d", i), "acct-bulk", old).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		n, err := r.p.Sweep(context.Background())
		if err != nil || n != total {
			t.Errorf("Sweep = %d, %v, want all %d", n, err, total)
		}
		if left := len(r.rows("acct-bulk")); left != 0 {
			t.Errorf("%d rows are left", left)
		}
	})
}

// TestADeleteIsConditionalOnTheRowStillBeingPending checks that a delete is conditional on the row still being pending.
func TestADeleteIsConditionalOnTheRowStillBeingPending(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		id := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, r.clock.read().Add(-5*time.Hour), nil)
		won, err := r.p.register.activate(context.Background(), acct, id, r.clock.read())
		if err != nil || !won {
			t.Fatalf("activate = %v, %v", won, err)
		}
		removed, err := r.p.register.removePending(context.Background(), id)
		if err != nil || removed {
			t.Errorf("removePending on an active row = %v, %v, want it to delete nothing", removed, err)
		}
		if s, there := r.status(id); !there || s != authn.FactorActive {
			t.Error("a row that had been activated was swept")
		}
		again, err := r.p.register.activate(context.Background(), acct, id, r.clock.read())
		if err != nil || again {
			t.Errorf("activating a row that is already active = %v, %v, want it to win nothing", again, err)
		}
	})
}

// TestTheSweepAndARegistrationThatIsFinishingDoNotDeleteEachOthersWork checks that a finishing registration and the sweep never both take one row.
func TestTheSweepAndARegistrationThatIsFinishingDoNotDeleteEachOthersWork(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		const n = 60
		ids := make([]string, n)
		for i := range ids {
			ids[i] = r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, r.clock.read().Add(-3*time.Hour), nil)
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		won := map[string]bool{}
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			if _, err := r.p.Sweep(context.Background()); err != nil {
				t.Errorf("Sweep: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			for _, id := range ids {
				ok, err := r.p.register.activate(context.Background(), acct, id, r.clock.read())
				if err != nil {
					t.Errorf("activate: %v", err)
					return
				}
				mu.Lock()
				won[id] = ok
				mu.Unlock()
			}
		}()
		close(start)
		wg.Wait()

		cleaned := map[string]bool{}
		for _, id := range totp.removed {
			cleaned[id] = true
		}
		for _, id := range ids {
			status, there := r.status(id)
			switch {
			case there && status == authn.FactorActive:
				if !won[id] || cleaned[id] {
					t.Errorf("the row %s is active, activation won = %v, its data cleaned = %v", id, won[id], cleaned[id])
				}
			case !there:
				if won[id] || !cleaned[id] {
					t.Errorf("the row %s is gone, activation won = %v, its data cleaned = %v", id, won[id], cleaned[id])
				}
			default:
				t.Errorf("the row %s is %v, want it activated or swept", id, status)
			}
		}
	})
}

// TestASweepThatCannotCleanAFactorsDataStillRemovesTheRowAndSaysSo checks that a sweep that cannot clean a factor's data still removes the row and says so.
func TestASweepThatCannotCleanAFactorsDataStillRemovesTheRowAndSaysSo(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		r.p.factors[authn.FactorTOTP].(*fakeFactor).removeFn = func(context.Context, authn.AccountID, authn.FactorRecord) error {
			return errors.New("seed store down")
		}
		id := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, r.clock.read().Add(-3*time.Hour), nil)
		if n, err := r.p.Sweep(context.Background()); err != nil || n != 1 {
			t.Errorf("Sweep = %d, %v, want 1 and no error", n, err)
		}
		if _, there := r.status(id); there {
			t.Error("the row stayed")
		}
		if len(r.logs.lines("level=WARN", "could not be removed")) != 1 {
			t.Errorf("the failure was not a Warn: %s", r.logs.String())
		}
	})
}

// TestTheSweepRunsAtStartAndOnItsInterval checks that the sweep runs at start and on its interval.
func TestTheSweepRunsAtStartAndOnItsInterval(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h, newFactor(authn.FactorTOTP))
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		cleaned := make(chan string, 4)
		totp.removeFn = func(_ context.Context, _ authn.AccountID, rec authn.FactorRecord) error {
			cleaned <- rec.ID
			return nil
		}
		r.p.sweepEvery = 5 * time.Millisecond
		first := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, r.clock.read().Add(-3*time.Hour), nil)

		r.p.startSweep(context.Background())
		defer func() {
			if err := r.p.stopSweep(); err != nil {
				t.Errorf("stopSweep: %v", err)
			}
		}()
		expect := func(want string) {
			t.Helper()
			select {
			case got := <-cleaned:
				if got != want {
					t.Errorf("the sweep cleaned %s, want %s", got, want)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("the sweep did not reach %s", want)
			}
		}
		expect(first)
		second := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, r.clock.read().Add(-4*time.Hour), nil)
		expect(second)
	})
}

// TestStopEndsTheSweepWithinItsBound checks that Stop ends the sweep within its bound.
func TestStopEndsTheSweepWithinItsBound(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h, newFactor(authn.FactorTOTP))
		if r.p.stopBound != 5*time.Second {
			t.Errorf("the bound on Stop is %v, want 5s", r.p.stopBound)
		}
		if r.p.sweepEvery != time.Hour {
			t.Errorf("the interval is %v, want an hour", r.p.sweepEvery)
		}
		if err := r.p.stopSweep(); err != nil {
			t.Errorf("stopSweep before any start: %v", err)
		}

		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		inside := make(chan struct{})
		totp.removeFn = func(ctx context.Context, _ authn.AccountID, _ authn.FactorRecord) error {
			close(inside)
			<-ctx.Done()
			return ctx.Err()
		}
		r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, r.clock.read().Add(-3*time.Hour), nil)
		r.p.startSweep(context.Background())
		<-inside
		began := time.Now()
		if err := r.p.stopSweep(); err != nil {
			t.Errorf("stopSweep: %v", err)
		}
		if took := time.Since(began); took > 4*time.Second {
			t.Errorf("Stop took %v, want it well inside 5s", took)
		}
	})
}

// TestStopGivesUpOnASweepThatDoesNotEnd checks that Stop gives up on a sweep that does not end.
func TestStopGivesUpOnASweepThatDoesNotEnd(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h, newFactor(authn.FactorTOTP))
		r.p.stopBound = 50 * time.Millisecond
		release := make(chan struct{})
		inside := make(chan struct{})
		var once sync.Once
		r.p.factors[authn.FactorTOTP].(*fakeFactor).removeFn = func(context.Context, authn.AccountID, authn.FactorRecord) error {
			once.Do(func() { close(inside) })
			<-release
			return nil
		}
		defer close(release)
		r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, r.clock.read().Add(-3*time.Hour), nil)
		r.p.startSweep(context.Background())
		<-inside
		if err := r.p.stopSweep(); !errors.Is(err, errSweepWait) {
			t.Errorf("stopSweep = %v, want it to give up with errSweepWait once the bound passed", err)
		}
	})
}

// TestTheSweepUnderAnUnreadablePendingTTLUsesTheDeclaredDefault checks that an unreadable pending-ttl reads as its declared default of one hour.
func TestTheSweepUnderAnUnreadablePendingTTLUsesTheDeclaredDefault(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		r.store.fail(pendingTTLPath, errors.New("settings: not loaded"))
		now := r.clock.read()
		kept := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, now.Add(-10*time.Minute), nil)
		swept := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, now.Add(-2*time.Hour), nil)
		n, err := r.p.Sweep(context.Background())
		if err != nil || n != 1 {
			t.Fatalf("Sweep = %d, %v, want the one row past the declared hour", n, err)
		}
		if _, there := r.status(kept); !there {
			t.Error("a row ten minutes old was swept although the declared default is an hour")
		}
		if _, there := r.status(swept); there {
			t.Error("a row two hours old was kept although the declared default is an hour")
		}
	})
}

// TestAnOutOfDomainPendingTTLUsesTheDeclaredDefault checks that a pending-ttl outside its domain reads as the declared default of one hour.
func TestAnOutOfDomainPendingTTLUsesTheDeclaredDefault(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		r.store.set(pendingTTLPath, time.Minute)
		id := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, r.clock.read().Add(-30*time.Minute), nil)
		n, err := r.p.Sweep(context.Background())
		if err != nil || n != 0 {
			t.Fatalf("Sweep = %d, %v, want 0 under the declared hour", n, err)
		}
		if _, there := r.status(id); !there {
			t.Error("a row thirty minutes old was swept although the declared default is an hour")
		}
	})
}

// TestTheSweepReturnsWhenAFullBatchRemovesNothing checks that the sweep returns when a full batch removes nothing.
func TestTheSweepReturnsWhenAFullBatchRemovesNothing(t *testing.T) {
	h := openDialect(t, sqliteDialect)
	r := newRig(t, h, newFactor(authn.FactorTOTP))
	old := r.clock.read().Add(-3 * time.Hour).UTC()
	if err := r.h.Exec("CREATE TRIGGER keep_pending_rows BEFORE DELETE ON authn_factors " +
		"WHEN OLD.status = 0 BEGIN SELECT RAISE(IGNORE); END").Error; err != nil {
		t.Fatalf("creating the trigger: %v", err)
	}
	err := r.h.Transaction(func(tx *gorm.DB) error {
		for i := 0; i < sweepBatch; i++ {
			if err := tx.Exec("INSERT INTO authn_factors (id, account_id, kind, label, status, created_at) "+
				"VALUES (?, ?, 'totp', '', 0, ?)", fmt.Sprintf("keep-%05d", i), "acct-keep", old).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var removed int
	var sweepErr error
	go func() {
		defer close(done)
		removed, sweepErr = r.p.Sweep(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Sweep did not return: the guard let it reread the same full batch forever")
	}
	if sweepErr != nil || removed != 0 {
		t.Errorf("Sweep = %d, %v, want 0 and no error", removed, sweepErr)
	}
}
