package fs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/objectstore"
)

// storeOver builds a backend over a fresh directory under the case's temporary
// area and answers the store and the directory.
func storeOver(t *testing.T) (*store, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "root")
	return &store{root: root}, root
}

// putBytes stores body under key, failing the case on any error.
func putBytes(t *testing.T, s *store, key string, body string) {
	t.Helper()
	if err := s.Put(t.Context(), key, strings.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
}

// readAll reads back what is stored under key, failing the case on any error.
func readAll(t *testing.T, s *store, key string) string {
	t.Helper()
	r, err := s.Open(t.Context(), key)
	if err != nil {
		t.Fatalf("Open(%q): %v", key, err)
	}
	defer func() {
		if cerr := r.Close(); cerr != nil {
			t.Errorf("closing the stream for %q: %v", key, cerr)
		}
	}()
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading %q: %v", key, err)
	}
	return string(body)
}

// TestPutAndOpenRoundTrip holds that what is stored under a key is what is read
// back, across a multi-segment key, which is the shape the metadata module
// generates.
func TestPutAndOpenRoundTrip(t *testing.T) {
	s, root := storeOver(t)
	const key = "tenant-1/object-id/uuid.bin"
	putBytes(t, s, key, "the object's bytes")

	if got := readAll(t, s, key); got != "the object's bytes" {
		t.Errorf("read back %q, want the bytes that were stored", got)
	}
	if _, err := os.Stat(filepath.Join(root, "tenant-1", "object-id", "uuid.bin")); err != nil {
		t.Errorf("the bytes are not under the key's path: %v", err)
	}
}

// TestPutCreatesMissingParentDirectories holds that a key whose segments name
// directories that do not exist yet still stores, which is what lets an assembly
// start before the root is laid out.
func TestPutCreatesMissingParentDirectories(t *testing.T) {
	s, _ := storeOver(t)
	putBytes(t, s, "a/b/c/leaf", "nested")

	if got := readAll(t, s, "a/b/c/leaf"); got != "nested" {
		t.Errorf("read back %q, want the nested bytes", got)
	}
}

// TestPutOverwritesAnExistingKey holds the upload step's retry: a second Put to
// one key replaces the bytes the first left, and the atomic replacement leaves
// no temporary file behind.
func TestPutOverwritesAnExistingKey(t *testing.T) {
	s, root := storeOver(t)
	putBytes(t, s, "k", "the first byte set")
	putBytes(t, s, "k", "the second")

	if got := readAll(t, s, "k"); got != "the second" {
		t.Errorf("read back %q, want the second byte set", got)
	}
	if leftovers := partFiles(t, root); len(leftovers) != 0 {
		t.Errorf("the atomic replacement left temporary files behind: %v", leftovers)
	}
}

// partFiles lists the temporary files a Put writes before renaming one into
// place, under every directory of the root.
func partFiles(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.IsDir() && strings.Contains(d.Name(), ".part-") {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the backend root: %v", err)
	}
	return found
}

// TestPutRefusesAShortStream holds the exact-size promise: a stream that ends
// before the declared byte count is refused with the capability's invalid-request
// sentinel, the target file is not created and the temporary file is removed, so
// a caller never observes a partly written object.
func TestPutRefusesAShortStream(t *testing.T) {
	s, root := storeOver(t)
	const key = "short/stream"
	err := s.Put(t.Context(), key, strings.NewReader("abc"), 5)
	if !errors.Is(err, objectstore.ErrInvalidRequest) {
		t.Fatalf("Put of a short stream answered %v, want ErrInvalidRequest", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "short", "stream")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("the target exists after a refused Put: %v", statErr)
	}
	if leftovers := partFiles(t, root); len(leftovers) != 0 {
		t.Errorf("a refused Put left temporary files behind: %v", leftovers)
	}
}

// TestPutRefusesAnOverlongStream holds the other side of the exact-size promise:
// a stream carrying more than the declared byte count is refused the same way,
// and again neither the target nor a temporary file remains.
func TestPutRefusesAnOverlongStream(t *testing.T) {
	s, root := storeOver(t)
	const key = "long/stream"
	err := s.Put(t.Context(), key, strings.NewReader("abcdef"), 3)
	if !errors.Is(err, objectstore.ErrInvalidRequest) {
		t.Fatalf("Put of an overlong stream answered %v, want ErrInvalidRequest", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "long", "stream")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("the target exists after a refused Put: %v", statErr)
	}
	if leftovers := partFiles(t, root); len(leftovers) != 0 {
		t.Errorf("a refused Put left temporary files behind: %v", leftovers)
	}
}

// TestStatReportsTheStoredSize holds that Stat answers the number of bytes the
// backend holds under a key without reading them.
func TestStatReportsTheStoredSize(t *testing.T) {
	s, _ := storeOver(t)
	putBytes(t, s, "a/b/leaf", "twelve bytes")

	blob, err := s.Stat(t.Context(), "a/b/leaf")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if blob.Size != int64(len("twelve bytes")) {
		t.Errorf("Stat answered %d bytes, want %d", blob.Size, len("twelve bytes"))
	}
}

// TestStatOnAKeyWithNoBytesWrapsContentMissing holds the shape the completion
// step reads: a key the backend holds no bytes under reports an error wrapping
// objectstore.ErrContentMissing — both for a key under an existing root and for
// one under a root that was never created.
func TestStatOnAKeyWithNoBytesWrapsContentMissing(t *testing.T) {
	t.Run("missing key", func(t *testing.T) {
		s, _ := storeOver(t)
		putBytes(t, s, "present", "bytes")

		_, err := s.Stat(t.Context(), "absent")
		if !errors.Is(err, objectstore.ErrContentMissing) {
			t.Errorf("Stat of an absent key answered %v, want one wrapping ErrContentMissing", err)
		}
	})

	t.Run("root not created", func(t *testing.T) {
		s, _ := storeOver(t)

		_, err := s.Stat(t.Context(), "absent")
		if !errors.Is(err, objectstore.ErrContentMissing) {
			t.Errorf("Stat under a root that does not exist answered %v, want one wrapping ErrContentMissing", err)
		}
	})
}

// TestDeleteIsIdempotent holds that deleting a key removes its bytes and that a
// second delete of the same key is not an error.
func TestDeleteIsIdempotent(t *testing.T) {
	s, root := storeOver(t)
	putBytes(t, s, "gone/soon", "bytes")

	if err := s.Delete(t.Context(), "gone/soon"); err != nil {
		t.Fatalf("the first Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "gone", "soon")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the bytes are still there after Delete: %v", err)
	}
	if err := s.Delete(t.Context(), "gone/soon"); err != nil {
		t.Errorf("the second Delete answered %v, want success", err)
	}
}

// TestDeleteOfAKeyWithNoBytesSucceeds holds the idempotence from the other side:
// a key never written is deleted without an error, which is what the expiry
// sweep relies on when a Put landed bytes and the row never moved.
func TestDeleteOfAKeyWithNoBytesSucceeds(t *testing.T) {
	s, _ := storeOver(t)
	if err := s.Delete(t.Context(), "never/written"); err != nil {
		t.Errorf("Delete of a key with no bytes answered %v, want success", err)
	}
}

// TestOpenOfAKeyWithNoBytesReportsNotExist holds that a read with no bytes
// behind it reports the file system's own not-exist error, which is the shape
// the metadata module tells apart from an unavailable backend.
func TestOpenOfAKeyWithNoBytesReportsNotExist(t *testing.T) {
	s, _ := storeOver(t)
	_, err := s.Open(t.Context(), "no/such/bytes")
	if err == nil {
		t.Fatal("Open of a key with no bytes succeeded, want a not-exist error")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open answered %v, want a not-exist error", err)
	}
}

// illegalKeys are the keys this backend must refuse: each names a key that
// cannot become a sequence of plain file names under the root.
var illegalKeys = map[string]string{
	"empty":            "",
	"empty segment":    "a//b",
	"leading segment":  "/a/b",
	"trailing segment": "a/b/",
	"current dir":      "a/./b",
	"parent dir":       "a/../b",
	"bare parent":      "..",
	"bare current":     ".",
	"would escape":     "../outside",
	"deeper escape":    "a/../../outside",
	"nul":              "a/b\x00c",
	"backslash":        `a\b`,
	"overlong segment": "a/" + strings.Repeat("x", maxSegmentLength+1),
}

// TestAnIllegalKeyIsRefused holds the key grammar to be a boundary: every method
// refuses a key that is not a legal sequence of path elements, through the one
// sentinel, and nothing is created for it.
func TestAnIllegalKeyIsRefused(t *testing.T) {
	s, root := storeOver(t)
	for name, key := range illegalKeys {
		t.Run(name, func(t *testing.T) {
			if err := s.Put(t.Context(), key, strings.NewReader("bytes"), 5); !errors.Is(err, errInvalidKey) {
				t.Errorf("Put(%q) answered %v, want errInvalidKey", key, err)
			}
			if _, err := s.Open(t.Context(), key); !errors.Is(err, errInvalidKey) {
				t.Errorf("Open(%q) answered %v, want errInvalidKey", key, err)
			}
			if _, err := s.Stat(t.Context(), key); !errors.Is(err, errInvalidKey) {
				t.Errorf("Stat(%q) answered %v, want errInvalidKey", key, err)
			}
			if err := s.Delete(t.Context(), key); !errors.Is(err, errInvalidKey) {
				t.Errorf("Delete(%q) answered %v, want errInvalidKey", key, err)
			}
			if _, err := os.Stat(root); err == nil {
				t.Errorf("the root was created for the refused key %q", key)
			}
		})
	}
}

// TestAKeyThatWouldEscapeTheRootLeavesNothingOutside holds the security boundary
// as a property rather than a single key: for a set of keys that try to climb
// out of the root, the store refuses every one and writes nothing outside the
// root.
func TestAKeyThatWouldEscapeTheRootLeavesNothingOutside(t *testing.T) {
	s, root := storeOver(t)
	parent := filepath.Dir(root)

	attempts := []string{"../escaped", "a/../../escaped", "..", `..\..\escaped`}
	for _, key := range attempts {
		if err := s.Put(t.Context(), key, strings.NewReader("x"), 1); !errors.Is(err, errInvalidKey) {
			t.Errorf("Put(%q) answered %v, want errInvalidKey", key, err)
		}
	}

	entries, err := os.ReadDir(parent)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("reading the parent directory: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() == "escaped" {
			t.Errorf("a refused key landed a file outside the root: %s", filepath.Join(parent, entry.Name()))
		}
	}
}

// TestACancelledContextIsRefused holds that every method honours the call's
// context rather than ignoring it.
func TestACancelledContextIsRefused(t *testing.T) {
	s, _ := storeOver(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := s.Put(ctx, "k", strings.NewReader("x"), 1); !errors.Is(err, context.Canceled) {
		t.Errorf("Put answered %v, want context.Canceled", err)
	}
	if _, err := s.Open(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("Open answered %v, want context.Canceled", err)
	}
	if _, err := s.Stat(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("Stat answered %v, want context.Canceled", err)
	}
	if err := s.Delete(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete answered %v, want context.Canceled", err)
	}
}

// TestTheStoreIsSafeForConcurrentUse holds that one store answers several
// callers at once: the store holds no per-call state, so distinct keys put,
// read and deleted from many goroutines never interfere.
func TestTheStoreIsSafeForConcurrentUse(t *testing.T) {
	s, _ := storeOver(t)
	const workers = 16
	const rounds = 25

	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range rounds {
				key := fmt.Sprintf("worker/%d/%d", w, r)
				body := []byte{byte(w), byte(r)}
				if err := s.Put(t.Context(), key, bytes.NewReader(body), int64(len(body))); err != nil {
					t.Errorf("Put(%q): %v", key, err)
					return
				}
				rc, err := s.Open(t.Context(), key)
				if err != nil {
					t.Errorf("Open(%q): %v", key, err)
					return
				}
				got, err := io.ReadAll(rc)
				if cerr := rc.Close(); cerr != nil {
					t.Errorf("closing the stream for %q: %v", key, cerr)
					return
				}
				if err != nil {
					t.Errorf("reading %q: %v", key, err)
					return
				}
				if !bytes.Equal(got, body) {
					t.Errorf("read back %v under %q, want %v", got, key, body)
					return
				}
				if err := s.Delete(t.Context(), key); err != nil {
					t.Errorf("Delete(%q): %v", key, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestNameReportsTheBackend holds that the diagnostic name is this backend's
// constant.
func TestNameReportsTheBackend(t *testing.T) {
	s, _ := storeOver(t)
	if got := s.Name(); got != "fs" {
		t.Errorf("Name = %q, want %q", got, "fs")
	}
}
