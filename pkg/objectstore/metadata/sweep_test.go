package metadata

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/objectstore"
)

// rowState reads one row's state directly, past the filter.
func rowState(t *testing.T, reg *core.Registry, tenantID, id string) string {
	t.Helper()
	var rows []struct {
		State string `gorm:"column:state"`
	}
	err := dbHandle(t, reg).Raw("SELECT state FROM objects WHERE tenant_id = ? AND id = ?", tenantID, id).Scan(&rows).Error
	if err != nil {
		t.Fatalf("reading the row %s: %v", id, err)
	}
	if len(rows) == 0 {
		t.Fatalf("no row %s for tenant %s", id, tenantID)
	}
	return rows[0].State
}

// setColumn runs one raw update against a row, for a case steering a row's clock.
func setColumn(t *testing.T, reg *core.Registry, column string, value int64, tenantID, id string) {
	t.Helper()
	stmt := "UPDATE objects SET " + column + " = ? WHERE tenant_id = ? AND id = ?"
	if err := dbHandle(t, reg).Exec(stmt, value, tenantID, id).Error; err != nil {
		t.Fatalf("setting %s: %v", column, err)
	}
}

// TestSweepTombstonesAClosedUploadWindow holds that a pending object whose upload
// window has closed is tombstoned and its key deleted through the seam — a
// pending row cannot be assumed to have no bytes.
func TestSweepTombstonesAClosedUploadWindow(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			declared, err := objects.Create(tctx, objectstore.Declaration{Size: 4, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			setColumn(t, reg, "upload_expires_at", time.Now().Add(-time.Minute).UnixMilli(), "acme", declared.ID)

			productOfAssembly(t, reg).sweepOnce(t.Context())

			if state := rowState(t, reg, "acme", declared.ID); state != stateDeleted {
				t.Errorf("the row is %s after the sweep, want deleted", state)
			}
			if got := store.deletions(); len(got) != 1 || got[0] != declared.Key {
				t.Errorf("the sweep deleted %v, want the object's key once", got)
			}
			return nil
		})
	})
}

// TestSweepTombstonesAnExpiredCompletedObject holds that a completed object past
// its expiry is tombstoned and its bytes deleted.
func TestSweepTombstonesAnExpiredCompletedObject(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "temp", "body")
			// Plant an expiry in the past; the module would not write one without
			// a TTL, so the sweep's expiry arm is exercised directly.
			setColumn(t, reg, "expires_at", time.Now().Add(-time.Minute).UnixMilli(), "acme", id)

			productOfAssembly(t, reg).sweepOnce(t.Context())

			if state := rowState(t, reg, "acme", id); state != stateDeleted {
				t.Errorf("the row is %s after the sweep, want deleted", state)
			}
			if len(store.deletions()) == 0 {
				t.Error("the sweep did not delete the expired object's bytes")
			}
			return nil
		})
	})
}

// TestSweepLeavesRowsWhoseTimeIsNotUp holds that a row with an open window and no
// expiry is left alone.
func TestSweepLeavesRowsWhoseTimeIsNotUp(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			pending, err := objects.Create(tctx, objectstore.Declaration{Size: 4, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			id := declare(t, tctx, objects, "fresh", "body")

			productOfAssembly(t, reg).sweepOnce(t.Context())

			if state := rowState(t, reg, "acme", pending.ID); state != statePending {
				t.Errorf("a live pending row is %s after the sweep, want pending", state)
			}
			if state := rowState(t, reg, "acme", id); state != stateCompleted {
				t.Errorf("a live completed row is %s after the sweep, want completed", state)
			}
			return nil
		})
	})
}

// TestSweepLeavesARowWhoseBytesCouldNotBeDeleted holds that a failed byte
// deletion leaves the row as it is, for the next pass to retry.
func TestSweepLeavesARowWhoseBytesCouldNotBeDeleted(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "temp", "body")
			setColumn(t, reg, "expires_at", time.Now().Add(-time.Minute).UnixMilli(), "acme", id)

			store.mu.Lock()
			store.delErr = errors.New("the backend is unreachable")
			store.mu.Unlock()
			productOfAssembly(t, reg).sweepOnce(t.Context())

			if state := rowState(t, reg, "acme", id); state == stateDeleted {
				t.Error("a row whose bytes could not be deleted was tombstoned anyway")
			}

			// With the backend reachable again the next pass closes it.
			store.mu.Lock()
			store.delErr = nil
			store.mu.Unlock()
			productOfAssembly(t, reg).sweepOnce(t.Context())

			if state := rowState(t, reg, "acme", id); state != stateDeleted {
				t.Errorf("the retry left the row %s, want deleted", state)
			}
			return nil
		})
	})
}

// TestSweepIsIdempotent holds that a second pass over a swept table tombstones
// nothing more.
func TestSweepIsIdempotent(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "temp", "body")
			setColumn(t, reg, "expires_at", time.Now().Add(-time.Minute).UnixMilli(), "acme", id)

			product := productOfAssembly(t, reg)
			product.sweepOnce(t.Context())
			product.sweepOnce(t.Context())

			if len(store.deletions()) != 1 {
				t.Errorf("two passes deleted %d blobs, want one", len(store.deletions()))
			}
			return nil
		})
	})
}

// grantCount answers how many grant rows carry a token hash.
func grantCount(t *testing.T, reg *core.Registry, hash string) int64 {
	t.Helper()
	var n int64
	if err := dbHandle(t, reg).Raw(
		"SELECT count(*) FROM object_grants WHERE token_hash = ?", hash).Row().Scan(&n); err != nil {
		t.Fatalf("counting the grants: %v", err)
	}
	return n
}

// TestSweepRemovesExpiredGrants holds the sweep's second job: grants past their
// expiry are removed, and a live one is left alone.
func TestSweepRemovesExpiredGrants(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{router: newRecordingRouter("public")})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "done", "body")
			live := contentToken(t, objects, tctx, id, objectstore.URLAccessRead)
			stale := contentToken(t, objects, tctx, id, objectstore.URLAccessRead)
			if err := dbHandle(t, reg).Exec(
				"UPDATE object_grants SET expires_at = ? WHERE token_hash = ?",
				time.Now().Add(-time.Minute).UnixMilli(), hashToken(stale)).Error; err != nil {
				t.Fatalf("expiring the grant: %v", err)
			}

			productOfAssembly(t, reg).sweepOnce(t.Context())

			if n := grantCount(t, reg, hashToken(stale)); n != 0 {
				t.Errorf("the sweep left %d expired grant rows, want none", n)
			}
			if n := grantCount(t, reg, hashToken(live)); n != 1 {
				t.Errorf("the sweep removed the live grant: %d rows, want one", n)
			}
			return nil
		})
	})
}

// TestSweepDoesNotAssumeAPendingRowHasNoBytes holds that the sweep calls the byte
// seam for a pending row too: a Put that succeeded with a state advance that did
// not leaves bytes behind.
func TestSweepDoesNotAssumeAPendingRowHasNoBytes(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			declared, err := objects.Create(tctx, objectstore.Declaration{Size: 4, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			// Stand in for bytes left behind by an upload whose row advance failed.
			store.mu.Lock()
			store.blobs[declared.Key] = []byte("left behind")
			store.mu.Unlock()
			setColumn(t, reg, "upload_expires_at", time.Now().Add(-time.Minute).UnixMilli(), "acme", declared.ID)

			productOfAssembly(t, reg).sweepOnce(t.Context())

			if store.has(declared.Key) {
				t.Error("the sweep left a pending object's bytes behind")
			}
			return nil
		})
	})
}
