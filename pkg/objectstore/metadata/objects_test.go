package metadata

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/objectstore"
)

// TestCreateRefusesADeclarationItCannotRead holds that a declaration whose shape
// is illegal is refused and lands no row.
func TestCreateRefusesADeclarationItCannotRead(t *testing.T) {
	cases := map[string]objectstore.Declaration{
		"zero size":      {Size: 0, ContentType: "text/plain"},
		"negative size":  {Size: -1, ContentType: "text/plain"},
		"empty type":     {Size: 1, ContentType: "  "},
		"short checksum": {Size: 1, ContentType: "text/plain", ChecksumSHA256: "abc"},
		"non-hex digest": {Size: 1, ContentType: "text/plain", ChecksumSHA256: strings.Repeat("z", 64)},
	}
	forEachEngine(t, func(t *testing.T, e engine) {
		for name, d := range cases {
			t.Run(name, func(t *testing.T) {
				h := e.on(t, harness{})
				h.drive(t, func(ctx context.Context, reg *core.Registry) error {
					tctx := tenantCtx(t, reg, "acme")
					if _, err := objectsOf(t, reg).Create(tctx, d); !errors.Is(err, objectstore.ErrInvalidRequest) {
						t.Fatalf("Create of %+v gave %v, want ErrInvalidRequest", d, err)
					}
					var n int64
					if err := dbHandle(t, reg).Raw("SELECT count(*) FROM objects").Row().Scan(&n); err != nil {
						t.Fatalf("counting rows: %v", err)
					}
					if n != 0 {
						t.Errorf("a refused declaration landed %d rows, want none", n)
					}
					return nil
				})
			})
		}
	})
}

// TestCreateRequiresATenant holds that a declaration under a context with no
// tenant is refused, because the internal key needs the tenant.
func TestCreateRequiresATenant(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			_, err := objectsOf(t, reg).Create(t.Context(), objectstore.Declaration{Size: 1, ContentType: "text/plain"})
			if !errors.Is(err, objectstore.ErrInvalidRequest) && err == nil {
				t.Fatal("Create under a context with no tenant succeeded, want a refusal")
			}
			return nil
		})
	})
}

// TestLifecycleWritesThreeStepsAndServesReads walks an object through
// declaration, upload and completion, then reads its bytes back.
func TestLifecycleWritesThreeStepsAndServesReads(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			body := "hello objects"

			declared, err := objects.Create(tctx, objectstore.Declaration{Name: "greeting", Size: int64(len(body)), ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			if declared.State != objectstore.StatePending {
				t.Errorf("a declared object is %s, want pending", declared.State)
			}
			if upErr := objects.Upload(tctx, declared.ID, bytes.NewReader([]byte(body)), int64(len(body))); upErr != nil {
				t.Fatalf("uploading: %v", upErr)
			}

			// OpenContent is closed to an uploaded object.
			if _, openErr := objects.OpenContent(tctx, declared.ID); !errors.Is(openErr, objectstore.ErrStateConflict) {
				t.Errorf("opening an uploaded object gave %v, want ErrStateConflict", openErr)
			}

			completed, err := objects.Complete(tctx, declared.ID)
			if err != nil {
				t.Fatalf("completing: %v", err)
			}
			if completed.State != objectstore.StateCompleted {
				t.Errorf("a completed object is %s, want completed", completed.State)
			}
			if completed.Size != int64(len(body)) {
				t.Errorf("the observed size is %d, want %d", completed.Size, len(body))
			}
			if len(completed.Checksum) != 64 {
				t.Errorf("the observed checksum is %q, want 64 hex digits", completed.Checksum)
			}
			if completed.MIME != "text/plain" {
				t.Errorf("the recorded MIME is %q, want text/plain", completed.MIME)
			}
			if completed.ExpiresAt != nil {
				t.Errorf("an object with no TTL expires at %v, want no expiry", completed.ExpiresAt)
			}

			content, err := objects.OpenContent(tctx, declared.ID)
			if err != nil {
				t.Fatalf("opening the content: %v", err)
			}
			defer func() { _ = content.Close() }()
			got, err := io.ReadAll(content)
			if err != nil {
				t.Fatalf("reading the content: %v", err)
			}
			if string(got) != body {
				t.Errorf("the content is %q, want %q", got, body)
			}
			return nil
		})
	})
}

// TestUploadRefusesAByteCountThatDisagreesWithTheDeclaration holds that a mismatched
// upload writes no byte and advances no state.
func TestUploadRefusesAByteCountThatDisagreesWithTheDeclaration(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			declared, err := objects.Create(tctx, objectstore.Declaration{Size: 10, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			err = objects.Upload(tctx, declared.ID, bytes.NewReader([]byte("short")), 5)
			if !errors.Is(err, objectstore.ErrInvalidRequest) {
				t.Fatalf("a mismatched upload gave %v, want ErrInvalidRequest", err)
			}
			if len(store.deletions()) != 0 || store.has(declared.Key) {
				t.Error("a refused upload wrote bytes to the seam")
			}
			got, err := objects.Get(tctx, declared.ID)
			if err != nil {
				t.Fatalf("reading the row back: %v", err)
			}
			if got.State != objectstore.StatePending {
				t.Errorf("a refused upload left the object %s, want pending", got.State)
			}
			return nil
		})
	})
}

// TestUploadWindowClosedIsAStateConflict holds that an upload past the window is
// refused.
func TestUploadWindowClosedIsAStateConflict(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			declared, err := objects.Create(tctx, objectstore.Declaration{Size: 4, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			// Close the window by moving its close instant into the past.
			past := time.Now().Add(-time.Hour).UnixMilli()
			if setErr := dbHandle(t, reg).Exec(
				"UPDATE objects SET upload_expires_at = ? WHERE tenant_id = ? AND id = ?",
				past, "acme", declared.ID).Error; setErr != nil {
				t.Fatalf("closing the window: %v", setErr)
			}
			err = objects.Upload(tctx, declared.ID, bytes.NewReader([]byte("four")), 4)
			if !errors.Is(err, objectstore.ErrStateConflict) {
				t.Fatalf("an upload past the window gave %v, want ErrStateConflict", err)
			}
			return nil
		})
	})
}

// TestCompleteIsIdempotentAndRefusesStatesItCannotComplete holds both idempotent
// and refusing paths of Complete: a re-completion of a completed row returns it
// unchanged, and a pending row is a state conflict.
func TestCompleteIsIdempotentAndRefusesStatesItCannotComplete(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")

			// A pending row cannot be completed.
			pending, err := objects.Create(tctx, objectstore.Declaration{Size: 1, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			if _, cErr := objects.Complete(tctx, pending.ID); !errors.Is(cErr, objectstore.ErrStateConflict) {
				t.Fatalf("completing a pending object gave %v, want ErrStateConflict", cErr)
			}

			// Complete the same object twice: the second call returns it unchanged.
			if upErr := objects.Upload(tctx, pending.ID, bytes.NewReader([]byte("x")), 1); upErr != nil {
				t.Fatalf("uploading: %v", upErr)
			}
			first, err := objects.Complete(tctx, pending.ID)
			if err != nil {
				t.Fatalf("completing: %v", err)
			}
			second, err := objects.Complete(tctx, pending.ID)
			if err != nil {
				t.Fatalf("re-completing: %v", err)
			}
			if !second.UpdatedAt.Equal(first.UpdatedAt) {
				t.Errorf("re-completing touched the row: updated_at %v, want %v", second.UpdatedAt, first.UpdatedAt)
			}
			if second.State != objectstore.StateCompleted {
				t.Errorf("a re-completed object is %s, want completed", second.State)
			}
			return nil
		})
	})
}

// TestCompleteLeavesACompletedRowUnchanged holds that Complete's conditional
// statement does not rewrite a row that is already completed.
func TestCompleteLeavesACompletedRowUnchanged(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "done", "body")

			// Plant an expiry this module would never write on its own, then
			// complete again: the planted value must survive.
			planted := time.Now().Add(72 * time.Hour).UnixMilli()
			if err := dbHandle(t, reg).Exec(
				"UPDATE objects SET expires_at = ? WHERE tenant_id = ? AND id = ?",
				planted, "acme", id).Error; err != nil {
				t.Fatalf("planting the expiry: %v", err)
			}
			got, err := objects.Complete(tctx, id)
			if err != nil {
				t.Fatalf("re-completing: %v", err)
			}
			if got.ExpiresAt == nil || got.ExpiresAt.UnixMilli() != planted {
				t.Errorf("re-completing a completed object changed its expiry to %v, want the planted %v",
					got.ExpiresAt, msTime(planted))
			}
			return nil
		})
	})
}

// TestADeletedObjectIsNotFoundForEveryOperationItCannotServe holds that a
// deleted row is a tombstone rather than a state conflict: completing or
// uploading it answers ErrObjectNotFound, the same shape an unknown identifier
// gets, because the object is gone and not in a state this call could move it
// out of.
func TestADeletedObjectIsNotFoundForEveryOperationItCannotServe(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "gone", "body")
			if err := objects.Delete(tctx, id); err != nil {
				t.Fatalf("deleting: %v", err)
			}

			if _, err := objects.Complete(tctx, id); !errors.Is(err, objectstore.ErrObjectNotFound) {
				t.Errorf("completing a deleted object gave %v, want ErrObjectNotFound", err)
			}
			body := []byte("body")
			if err := objects.Upload(tctx, id, bytes.NewReader(body), int64(len(body))); !errors.Is(err, objectstore.ErrObjectNotFound) {
				t.Errorf("uploading a deleted object gave %v, want ErrObjectNotFound", err)
			}
			return nil
		})
	})
}

// TestDeleteIsIdempotentAndTombstones holds that Delete removes the bytes and
// tombstones the row, that a second delete removes no byte, and that a deleted
// object reads as not found.
func TestDeleteIsIdempotentAndTombstones(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "temp", "body")

			if err := objects.Delete(tctx, id); err != nil {
				t.Fatalf("deleting: %v", err)
			}
			if len(store.deletions()) != 1 {
				t.Errorf("the first delete removed %d blobs, want one", len(store.deletions()))
			}
			if _, err := objects.Get(tctx, id); !errors.Is(err, objectstore.ErrObjectNotFound) {
				t.Errorf("reading a deleted object gave %v, want ErrObjectNotFound", err)
			}
			if _, err := objects.OpenContent(tctx, id); !errors.Is(err, objectstore.ErrObjectNotFound) {
				t.Errorf("opening a deleted object gave %v, want ErrObjectNotFound", err)
			}
			// A second delete is an idempotent success and removes no byte.
			if err := objects.Delete(tctx, id); err != nil {
				t.Fatalf("re-deleting: %v", err)
			}
			if len(store.deletions()) != 1 {
				t.Errorf("the second delete removed another blob: %d deletions, want one", len(store.deletions()))
			}
			return nil
		})
	})
}

// TestObjectsAreScopedToTheirTenant holds that one tenant cannot see another's
// object, and that a listing returns only the tenant's own.
func TestObjectsAreScopedToTheirTenant(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			acme := tenantCtx(t, reg, "acme")
			globex := tenantCtx(t, reg, "globex")
			id := declare(t, acme, objects, "acme-doc", "acme body")

			if _, err := objects.Get(globex, id); !errors.Is(err, objectstore.ErrObjectNotFound) {
				t.Errorf("another tenant's object read as %v, want ErrObjectNotFound", err)
			}
			globexList, err := objects.List(globex, objectstore.ListQuery{})
			if err != nil {
				t.Fatalf("listing globex: %v", err)
			}
			if len(globexList) != 0 {
				t.Errorf("globex sees %d objects, want none", len(globexList))
			}
			acmeList, err := objects.List(acme, objectstore.ListQuery{})
			if err != nil {
				t.Fatalf("listing acme: %v", err)
			}
			if len(acmeList) != 1 || acmeList[0].ID != id {
				t.Errorf("acme sees %+v, want just %s", acmeList, id)
			}
			return nil
		})
	})
}

// TestListPagesByKeysetCursor holds that the cursor continues from the last row of
// the previous page and that a deleted object leaves the listing.
func TestListPagesByKeysetCursor(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			var ids []string
			for i := 0; i < 5; i++ {
				ids = append(ids, declare(t, tctx, objects, "obj", "body"))
			}
			// Remove the middle object: it must leave the listing.
			middle := ids[2]
			if err := objects.Delete(tctx, middle); err != nil {
				t.Fatalf("deleting the middle object: %v", err)
			}

			var walked []string
			cursor := ""
			for page := 0; page < 10; page++ {
				got, err := objects.List(tctx, objectstore.ListQuery{Limit: 2, Cursor: cursor})
				if err != nil {
					t.Fatalf("listing page %d: %v", page, err)
				}
				if len(got) == 0 {
					break
				}
				for _, o := range got {
					walked = append(walked, o.ID)
				}
				last := got[len(got)-1]
				cursor = encodeCursor(last.CreatedAt.UnixMilli(), last.ID)
			}
			if len(walked) != 4 {
				t.Fatalf("walking the pages visited %d objects (%v), want 4", len(walked), walked)
			}
			seen := map[string]bool{}
			for _, id := range walked {
				if id == middle {
					t.Errorf("the deleted object %s reappeared in the listing", id)
				}
				seen[id] = true
			}
			if len(seen) != 4 {
				t.Errorf("the pages returned %d distinct objects, want 4", len(seen))
			}
			return nil
		})
	})
}

// TestListRefusesALimitAboveTheMaximum holds that a limit above the maximum is
// refused rather than silently truncated.
func TestListRefusesALimitAboveTheMaximum(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			if _, err := objects.List(tctx, objectstore.ListQuery{Limit: maxListLimit + 1}); !errors.Is(err, objectstore.ErrInvalidRequest) {
				t.Errorf("a limit above the maximum gave %v, want ErrInvalidRequest", err)
			}
			if _, err := objects.List(tctx, objectstore.ListQuery{Limit: -1}); !errors.Is(err, objectstore.ErrInvalidRequest) {
				t.Errorf("a negative limit gave %v, want ErrInvalidRequest", err)
			}
			return nil
		})
	})
}
