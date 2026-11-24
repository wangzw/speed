package metadata

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/objectstore"
)

// TestCompleteWithoutBytesIsContentMissing checks that completing an object with no bytes fails as
// ErrContentMissing and leaves it pending.
func TestCompleteWithoutBytesIsContentMissing(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{store: newFakeStore()})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			pending, err := objects.Create(tctx, objectstore.Declaration{Size: 4, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			if _, cErr := objects.Complete(tctx, pending.ID); !errors.Is(cErr, objectstore.ErrContentMissing) {
				t.Fatalf("completing with no bytes gave %v, want ErrContentMissing", cErr)
			}
			if state := rowState(t, reg, "acme", pending.ID); state != string(objectstore.StatePending) {
				t.Errorf("the row is %s after the refusal, want pending", state)
			}
			return nil
		})
	})
}

// TestCompleteWithAMismatchedSizeRemovesTheBytes checks that completing an object whose stored size
// differs fails as ErrInvalidRequest, leaves it pending and removes the bytes.
func TestCompleteWithAMismatchedSizeRemovesTheBytes(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			pending, err := objects.Create(tctx, objectstore.Declaration{Size: 4, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			store.mu.Lock()
			store.blobs[pending.Key] = []byte("too long")
			store.mu.Unlock()

			if _, cErr := objects.Complete(tctx, pending.ID); !errors.Is(cErr, objectstore.ErrInvalidRequest) {
				t.Fatalf("completing a mismatched size gave %v, want ErrInvalidRequest", cErr)
			}
			if store.has(pending.Key) {
				t.Error("a mismatched completion left the bytes behind")
			}
			if state := rowState(t, reg, "acme", pending.ID); state != string(objectstore.StatePending) {
				t.Errorf("the row is %s after the refusal, want pending", state)
			}
			return nil
		})
	})
}

// TestCompleteRecordsTheObservedBytes checks that Complete records the stored size and the declared
// media type and moves the object to completed.
func TestCompleteRecordsTheObservedBytes(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			pending, err := objects.Create(tctx, objectstore.Declaration{Size: 4, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			store.mu.Lock()
			store.blobs[pending.Key] = []byte("body")
			store.mu.Unlock()

			completed, cErr := objects.Complete(tctx, pending.ID)
			if cErr != nil {
				t.Fatalf("completing a directly uploaded object: %v", cErr)
			}
			if completed.State != objectstore.StateCompleted {
				t.Errorf("the object is %s, want completed", completed.State)
			}
			if completed.Size != 4 {
				t.Errorf("the observed size is %d, want 4", completed.Size)
			}
			if completed.MIME != "text/plain" {
				t.Errorf("the recorded MIME is %q, want the declared text/plain", completed.MIME)
			}
			return nil
		})
	})
}

// TestCompleteIsIdempotentWithoutObservingTheBackend holds the fourth outcome: a completed row is
// returned unchanged and the backend is not asked again.
func TestCompleteIsIdempotentWithoutObservingTheBackend(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "done", "body")
			first, err := objects.Get(tctx, id)
			if err != nil {
				t.Fatalf("reading the object: %v", err)
			}

			store.mu.Lock()
			store.statErr = errors.New("the backend is unreachable")
			store.mu.Unlock()
			second, cErr := objects.Complete(tctx, id)
			if cErr != nil {
				t.Fatalf("re-completing a completed object observed the backend: %v", cErr)
			}
			if !second.UpdatedAt.Equal(first.UpdatedAt) {
				t.Errorf("re-completing touched the row: updated_at %v, want %v", second.UpdatedAt, first.UpdatedAt)
			}
			return nil
		})
	})
}

// TestCompleteWithAnUnreachableBackendIsStoreUnavailable holds the failure an observation that is
// neither a missing blob nor a size mismatch reports.
func TestCompleteWithAnUnreachableBackendIsStoreUnavailable(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			pending, err := objects.Create(tctx, objectstore.Declaration{Size: 4, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			store.mu.Lock()
			store.statErr = errors.New("the backend is unreachable")
			store.mu.Unlock()
			if _, cErr := objects.Complete(tctx, pending.ID); !errors.Is(cErr, objectstore.ErrStoreUnavailable) {
				t.Fatalf("completing against an unreachable backend gave %v, want ErrStoreUnavailable", cErr)
			}
			if state := rowState(t, reg, "acme", pending.ID); state != string(objectstore.StatePending) {
				t.Errorf("the row is %s after the failure, want pending", state)
			}
			return nil
		})
	})
}
